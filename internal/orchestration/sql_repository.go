package orchestration

// This file contains the database/sql contract adapter. The repository keeps
// domain validation and projection semantics in MemoryRepository, while this
// adapter makes the durable commit boundary a real SQL transaction. A driver
// is deliberately injected by the application (the core module does not pick
// a SQLite or PostgreSQL driver on behalf of a deployment).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const orchestrationSQLTable = "adro_orchestration_state"

// These names intentionally match migrations/011_orchestration.sql and
// migrations/013_orchestration_production_hardening.sql. The SQL adapter may
// keep a compact JSON snapshot for recovery, but its materialized facts must
// be queryable by the same operational tooling as a PostgreSQL deployment.
const (
	sqlAgentTable    = "agent_definitions"
	sqlSquadTable    = "squad_definitions"
	sqlPlanTable     = "execution_plans"
	sqlNodeTable     = "workflow_nodes"
	sqlAttemptTable  = "node_attempts"
	sqlDecisionTable = "workflow_edge_decisions"
	sqlEventTable    = "execution_events"
	sqlOutboxTable   = "orchestration_outbox_events"
)

type SQLRepository struct {
	*MemoryRepository
	db          *sql.DB
	profile     string
	dialect     string
	tenantID    string
	workspaceID string
	mu          sync.Mutex
	revision    int64
}

var errSQLRevisionConflict = errors.New("stale orchestration SQL state")

// OpenSQLRepository opens a caller-registered database/sql driver and applies
// the orchestration schema. The package intentionally does not import a
// concrete SQLite or PostgreSQL driver; deployments choose and version that
// dependency themselves.
func OpenSQLRepository(driverName, dsn, dialect string) (*SQLRepository, *sql.DB, error) {
	driverName = strings.TrimSpace(driverName)
	dsn = strings.TrimSpace(dsn)
	if driverName == "" || dsn == "" {
		return nil, nil, errors.New("sql driver and dsn are required")
	}
	if dialect != "sqlite" && dialect != "postgres" {
		return nil, nil, fmt.Errorf("unsupported orchestration SQL dialect %q", dialect)
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open orchestration SQL database: %w", err)
	}
	var repo *SQLRepository
	if dialect == "postgres" {
		repo, err = NewPostgresSQLRepository(db)
	} else {
		repo, err = NewSQLiteSQLRepository(db)
	}
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	return repo, db, nil
}

// NewSQLiteSQLRepository wires a SQLite database/sql handle. The caller owns
// driver registration and connection lifecycle; this constructor only creates
// the durable state table and loads its latest snapshot.
func NewSQLiteSQLRepository(db *sql.DB) (*SQLRepository, error) {
	return newSQLRepository(db, "sqlite", "sqlite")
}

// NewPostgresSQLRepository wires a PostgreSQL database/sql handle. Every
// mutation is committed atomically behind a transaction advisory lock and a
// transaction-local RLS scope; backup scheduling remains a deployment concern.
func NewPostgresSQLRepository(db *sql.DB) (*SQLRepository, error) {
	return newSQLRepository(db, "postgres", "postgres")
}

func newSQLRepository(db *sql.DB, profile, dialect string) (*SQLRepository, error) {
	if db == nil {
		return nil, errors.New("database handle is required")
	}
	r := &SQLRepository{MemoryRepository: newMemoryRepository(""), db: db, profile: profile, dialect: dialect}
	if err := r.init(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *SQLRepository) Profile() string { return r.profile + "-sql" }

// SetScope supplies the transaction-local identity consumed by PostgreSQL RLS
// policies. Values are never persisted and are applied only while committing a
// mutation, preventing pooled connection scope leakage.
func (r *SQLRepository) SetScope(tenantID, workspaceID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.tenantID, r.workspaceID = strings.TrimSpace(tenantID), strings.TrimSpace(workspaceID)
	r.mu.Unlock()
}

// Flush commits the current SQL snapshot through the same transaction used by
// mutations. The embedded MemoryRepository.Flush is intentionally bypassed
// because its file profile has no database connection.
func (r *SQLRepository) Flush() error {
	if r == nil {
		return errors.New("sql repository is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Every SQL mutation is committed synchronously by mutate. Flush therefore
	// only refreshes peer commits; writing the same snapshot again would create
	// artificial revisions and widen the multi-replica CAS race.
	return r.refreshLocked()
}

func (r *SQLRepository) init() error {
	blobType := "BLOB"
	if r.dialect == "postgres" {
		blobType = "BYTEA"
	}
	statements := []string{
		"CREATE TABLE IF NOT EXISTS " + orchestrationSQLTable + " (id INTEGER PRIMARY KEY, revision BIGINT NOT NULL, state_json " + blobType + " NOT NULL)",
		// The snapshot table is the local atomic commit record. These logical
		// tables make the adapter's production boundary explicit and allow a
		// deployment to migrate to row-level storage without changing the
		// domain contract. They are additive; the JSON snapshot remains the
		// source of truth for this adapter implementation.
		"CREATE TABLE IF NOT EXISTS agent_definitions (id VARCHAR(255) NOT NULL, tenant_id VARCHAR(255) NOT NULL DEFAULT '', workspace_id VARCHAR(255) NOT NULL, revision BIGINT NOT NULL, name VARCHAR(255) NOT NULL DEFAULT '', role VARCHAR(255) NOT NULL DEFAULT '', spec_json " + blobType + " NOT NULL, status VARCHAR(32) NOT NULL, created_by VARCHAR(255) NOT NULL DEFAULT '', created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY (workspace_id, id, revision))",
		"CREATE TABLE IF NOT EXISTS squad_definitions (id VARCHAR(255) NOT NULL, tenant_id VARCHAR(255) NOT NULL DEFAULT '', workspace_id VARCHAR(255) NOT NULL, revision BIGINT NOT NULL, name VARCHAR(255) NOT NULL DEFAULT '', spec_json " + blobType + " NOT NULL, status VARCHAR(32) NOT NULL, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY (workspace_id, id, revision))",
		"CREATE TABLE IF NOT EXISTS execution_plans (id VARCHAR(255) PRIMARY KEY, tenant_id VARCHAR(255) NOT NULL DEFAULT '', requirement_id VARCHAR(255) NOT NULL, workspace_id VARCHAR(255) NOT NULL, plan_hash VARCHAR(128) NOT NULL, snapshot_json " + blobType + " NOT NULL, revision BIGINT NOT NULL, status VARCHAR(32) NOT NULL, idempotency_key VARCHAR(255) NOT NULL DEFAULT '', created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)",
		"CREATE TABLE IF NOT EXISTS workflow_nodes (plan_id VARCHAR(255) NOT NULL, tenant_id VARCHAR(255) NOT NULL DEFAULT '', workspace_id VARCHAR(255) NOT NULL DEFAULT '', node_id VARCHAR(255) NOT NULL, kind VARCHAR(32) NOT NULL, status VARCHAR(32) NOT NULL, projection_json " + blobType + " NOT NULL, PRIMARY KEY (plan_id, node_id))",
		"CREATE TABLE IF NOT EXISTS node_attempts (id VARCHAR(255) PRIMARY KEY, plan_id VARCHAR(255) NOT NULL, tenant_id VARCHAR(255) NOT NULL DEFAULT '', workspace_id VARCHAR(255) NOT NULL DEFAULT '', node_id VARCHAR(255) NOT NULL, attempt_no INTEGER NOT NULL, run_id VARCHAR(255), session_id VARCHAR(255), workdir VARCHAR(1024), status VARCHAR(32) NOT NULL, input_manifest_json " + blobType + " NOT NULL, result_json " + blobType + " NOT NULL, lease_json " + blobType + " NOT NULL, UNIQUE (plan_id, node_id, attempt_no))",
		"CREATE TABLE IF NOT EXISTS workflow_edge_decisions (id VARCHAR(255) PRIMARY KEY, plan_id VARCHAR(255) NOT NULL, tenant_id VARCHAR(255) NOT NULL DEFAULT '', workspace_id VARCHAR(255) NOT NULL DEFAULT '', source_attempt_id VARCHAR(255) NOT NULL, edge_id VARCHAR(255) NOT NULL, target_node_id VARCHAR(255) NOT NULL, predicate_json " + blobType + " NOT NULL, evidence_json " + blobType + " NOT NULL, loop_count INTEGER NOT NULL DEFAULT 0, idempotency_key VARCHAR(255) NOT NULL DEFAULT '')",
		"CREATE TABLE IF NOT EXISTS execution_events (event_id VARCHAR(255) PRIMARY KEY, tenant_id VARCHAR(255) NOT NULL DEFAULT '', workspace_id VARCHAR(255) NOT NULL, plan_id VARCHAR(255) NOT NULL, run_id VARCHAR(255) NOT NULL DEFAULT '', node_id VARCHAR(255) NOT NULL DEFAULT '', attempt_id VARCHAR(255) NOT NULL DEFAULT '', sequence BIGINT NOT NULL, event_type VARCHAR(128) NOT NULL, payload_json " + blobType + " NOT NULL, payload_hash VARCHAR(128) NOT NULL, previous_hash VARCHAR(128), envelope_hash VARCHAR(128) NOT NULL, idempotency_key VARCHAR(255) NOT NULL DEFAULT '', writer_id VARCHAR(255) NOT NULL DEFAULT '', fencing_token BIGINT NOT NULL DEFAULT 0, committed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE (plan_id, sequence))",
		"CREATE TABLE IF NOT EXISTS orchestration_outbox_events (id VARCHAR(255) PRIMARY KEY, tenant_id VARCHAR(255) NOT NULL DEFAULT '', workspace_id VARCHAR(255) NOT NULL, plan_id VARCHAR(255) NOT NULL, idempotency_key VARCHAR(255) NOT NULL, kind VARCHAR(128) NOT NULL DEFAULT '', payload_json " + blobType + " NOT NULL, status VARCHAR(32) NOT NULL, owner VARCHAR(255), lease_expires_at TIMESTAMP, attempts INTEGER NOT NULL DEFAULT 0, max_attempts INTEGER NOT NULL DEFAULT 8, last_error TEXT, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE (plan_id, idempotency_key))",
		"CREATE TABLE IF NOT EXISTS workflow_graphs (id VARCHAR(255) NOT NULL, tenant_id VARCHAR(255) NOT NULL DEFAULT '', workspace_id VARCHAR(255) NOT NULL, version BIGINT NOT NULL, graph_json " + blobType + " NOT NULL, validation_digest VARCHAR(128) NOT NULL DEFAULT '', PRIMARY KEY (workspace_id, id, version))",
		"CREATE UNIQUE INDEX IF NOT EXISTS execution_plans_idempotency_idx ON execution_plans (workspace_id, idempotency_key) WHERE idempotency_key <> ''",
		"CREATE UNIQUE INDEX IF NOT EXISTS workflow_edge_decisions_idempotency_idx ON workflow_edge_decisions (plan_id, idempotency_key) WHERE idempotency_key <> ''",
		"CREATE UNIQUE INDEX IF NOT EXISTS execution_events_idempotency_idx ON execution_events (plan_id, idempotency_key) WHERE idempotency_key <> ''",
	}
	for _, create := range statements {
		if _, err := r.db.Exec(create); err != nil {
			if strings.HasPrefix(create, "ALTER TABLE") && (strings.Contains(strings.ToLower(err.Error()), "duplicate column") || strings.Contains(strings.ToLower(err.Error()), "already exists")) {
				continue
			}
			return fmt.Errorf("create orchestration SQL schema: %w", err)
		}
	}
	if r.dialect == "postgres" {
		if err := ensurePostgresScopePolicies(r.db); err != nil {
			return err
		}
	}
	var revision int64
	var raw any
	query := "SELECT revision, state_json FROM " + orchestrationSQLTable + " WHERE id=" + r.placeholder(1)
	err := r.db.QueryRow(query, 1).Scan(&revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load orchestration SQL state: %w", err)
	}
	data, err := sqlBytes(raw)
	if err != nil {
		return err
	}
	var state persistedRepository
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode orchestration SQL state: %w", err)
	}
	if err := validateRepositoryState(state); err != nil {
		return err
	}
	r.restoreSnapshot(state)
	r.revision = revision
	return nil
}

// ensurePostgresScopePolicies closes the adapter's tenant/workspace boundary
// even when an operator starts from an empty database rather than applying
// migrations/013 first.  The policy deliberately reads transaction-local
// settings: pooled connections never retain a caller's scope after commit.
// FORCE ROW LEVEL SECURITY also subjects the table owner to the same policy,
// preventing a privileged application role from accidentally bypassing the
// isolation contract.
func ensurePostgresScopePolicies(db *sql.DB) error {
	if db == nil {
		return errors.New("postgres orchestration database is nil")
	}
	tables := []string{sqlAgentTable, sqlSquadTable, "workflow_graphs", sqlPlanTable, sqlNodeTable, sqlAttemptTable, sqlDecisionTable, sqlEventTable, sqlOutboxTable}
	for _, table := range tables {
		statements := []string{
			"ALTER TABLE " + table + " ENABLE ROW LEVEL SECURITY",
			"ALTER TABLE " + table + " FORCE ROW LEVEL SECURITY",
			"DROP POLICY IF EXISTS adro_scope_isolation ON " + table,
			"CREATE POLICY adro_scope_isolation ON " + table + " USING (tenant_id = current_setting('app.tenant_id', true) AND workspace_id = current_setting('app.workspace_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true) AND workspace_id = current_setting('app.workspace_id', true))",
		}
		for _, statement := range statements {
			if _, err := db.Exec(statement); err != nil {
				return fmt.Errorf("configure postgres scope policy for %s: %w", table, err)
			}
		}
	}
	return nil
}

// Dialect reports the SQL profile used by this adapter. It is intentionally
// exposed so diagnostics and deployment checks can distinguish the SQLite
// single-node profile from a PostgreSQL transaction boundary.
func (r *SQLRepository) Dialect() string {
	if r == nil {
		return ""
	}
	return r.dialect
}

// scopeAllowsRead mirrors PostgreSQL RLS at the repository boundary. The
// compact snapshot is global for refresh/recovery, but reads must remain
// tenant/workspace scoped before they reach the embedded memory projection.
func (r *SQLRepository) scopeAllowsRead(workspaceID string) bool {
	if r == nil || r.dialect != "postgres" {
		return true
	}
	r.mu.Lock()
	scoped := strings.TrimSpace(r.workspaceID)
	r.mu.Unlock()
	return scoped != "" && strings.TrimSpace(workspaceID) == scoped
}

func (r *SQLRepository) scopedWorkspace(requested string) (string, bool) {
	if r == nil || r.dialect != "postgres" {
		return requested, true
	}
	r.mu.Lock()
	scoped := strings.TrimSpace(r.workspaceID)
	r.mu.Unlock()
	if scoped == "" {
		return "", false
	}
	if strings.TrimSpace(requested) != "" && strings.TrimSpace(requested) != scoped {
		return "", false
	}
	return scoped, true
}

// refresh reloads a newer committed snapshot before a read.  The SQL adapter
// is intentionally safe for more than one API process: mutations use a CAS
// revision, and readers must not continue serving a stale in-memory copy after
// a peer has committed.  A missing row is a valid empty database, not an error.
func (r *SQLRepository) refresh() error {
	if r == nil || r.db == nil {
		return errors.New("sql repository is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.refreshLocked()
}

func (r *SQLRepository) refreshLocked() error {
	var revision int64
	var raw any
	err := r.db.QueryRow("SELECT revision, state_json FROM "+orchestrationSQLTable+" WHERE id="+r.placeholder(1), 1).Scan(&revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("refresh orchestration SQL state: %w", err)
	}
	if revision <= r.revision {
		return nil
	}
	data, err := sqlBytes(raw)
	if err != nil {
		return err
	}
	var state persistedRepository
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode refreshed orchestration SQL state: %w", err)
	}
	if err := validateRepositoryState(state); err != nil {
		return fmt.Errorf("validate refreshed orchestration SQL state: %w", err)
	}
	r.restoreSnapshot(state)
	r.revision = revision
	return nil
}

// Read methods refresh from SQL first so a second API process observes the
// committed plan, projection, event and roster without requiring a restart.
func (r *SQLRepository) GetAgent(ws, id string, rev int64) (AgentDefinition, error) {
	if err := r.refresh(); err != nil {
		return AgentDefinition{}, err
	}
	if scoped, ok := r.scopedWorkspace(ws); !ok {
		return AgentDefinition{}, ErrNotFound
	} else {
		ws = scoped
	}
	return r.MemoryRepository.GetAgent(ws, id, rev)
}
func (r *SQLRepository) ListAgents(ws string, status AgentStatus) []AgentDefinition {
	if err := r.refresh(); err != nil {
		return nil
	}
	if scoped, ok := r.scopedWorkspace(ws); !ok {
		return nil
	} else {
		ws = scoped
	}
	return r.MemoryRepository.ListAgents(ws, status)
}
func (r *SQLRepository) GetSquad(ws, id string, rev int64) (SquadDefinition, error) {
	if err := r.refresh(); err != nil {
		return SquadDefinition{}, err
	}
	if scoped, ok := r.scopedWorkspace(ws); !ok {
		return SquadDefinition{}, ErrNotFound
	} else {
		ws = scoped
	}
	return r.MemoryRepository.GetSquad(ws, id, rev)
}
func (r *SQLRepository) ListSquads(ws string, status SquadStatus) []SquadDefinition {
	if err := r.refresh(); err != nil {
		return nil
	}
	if scoped, ok := r.scopedWorkspace(ws); !ok {
		return nil
	} else {
		ws = scoped
	}
	return r.MemoryRepository.ListSquads(ws, status)
}
func (r *SQLRepository) GetPlanByIdempotency(ws, key string) (RequirementExecutionPlan, error) {
	if err := r.refresh(); err != nil {
		return RequirementExecutionPlan{}, err
	}
	if scoped, ok := r.scopedWorkspace(ws); !ok {
		return RequirementExecutionPlan{}, ErrNotFound
	} else {
		ws = scoped
	}
	return r.MemoryRepository.GetPlanByIdempotency(ws, key)
}
func (r *SQLRepository) GetPlan(ws, id string) (RequirementExecutionPlan, error) {
	if err := r.refresh(); err != nil {
		return RequirementExecutionPlan{}, err
	}
	if scoped, ok := r.scopedWorkspace(ws); !ok {
		return RequirementExecutionPlan{}, ErrNotFound
	} else {
		ws = scoped
	}
	return r.MemoryRepository.GetPlan(ws, id)
}
func (r *SQLRepository) ListPlans(ws string) []RequirementExecutionPlan {
	if err := r.refresh(); err != nil {
		return nil
	}
	if scoped, ok := r.scopedWorkspace(ws); !ok {
		return nil
	} else {
		ws = scoped
	}
	return r.MemoryRepository.ListPlans(ws)
}
func (r *SQLRepository) GetProjection(id string) (PlanProjection, error) {
	if err := r.refresh(); err != nil {
		return PlanProjection{}, err
	}
	plan, planErr := r.planForRead(id)
	if planErr != nil || !r.scopeAllowsRead(plan.WorkspaceID) {
		return PlanProjection{}, ErrNotFound
	}
	return r.MemoryRepository.GetProjection(id)
}
func (r *SQLRepository) ListEvents(planID string, after int64) []Event {
	if err := r.refresh(); err != nil {
		return nil
	}
	plan, planErr := r.planForRead(planID)
	if planErr != nil || !r.scopeAllowsRead(plan.WorkspaceID) {
		return nil
	}
	return r.MemoryRepository.ListEvents(planID, after)
}
func (r *SQLRepository) ListOutbox(planID, status string) []OutboxRecord {
	if err := r.refresh(); err != nil {
		return nil
	}
	plan, planErr := r.planForRead(planID)
	if planErr != nil || !r.scopeAllowsRead(plan.WorkspaceID) {
		return nil
	}
	return r.MemoryRepository.ListOutbox(planID, status)
}

func (r *SQLRepository) planForRead(id string) (RequirementExecutionPlan, error) {
	if r == nil || r.MemoryRepository == nil {
		return RequirementExecutionPlan{}, ErrNotFound
	}
	for _, plan := range r.MemoryRepository.ListPlans("") {
		if plan.ID == id {
			return plan, nil
		}
	}
	return RequirementExecutionPlan{}, ErrNotFound
}

func sqlBytes(value any) ([]byte, error) {
	switch v := value.(type) {
	case []byte:
		return append([]byte(nil), v...), nil
	case string:
		return []byte(v), nil
	default:
		return nil, fmt.Errorf("orchestration SQL state has unsupported value type %T", value)
	}
}

func (r *SQLRepository) placeholder(index int) string {
	if r.dialect == "postgres" {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

func (r *SQLRepository) snapshot() persistedRepository {
	r.MemoryRepository.mu.RLock()
	defer r.MemoryRepository.mu.RUnlock()
	return persistedRepository{Version: 1, Revision: r.revision, Agents: cloneValue(r.agents), Squads: cloneValue(r.squads), Plans: cloneValue(r.plans), Projections: cloneValue(r.projections), Keys: cloneValue(r.keys), Events: cloneValue(r.events), Outbox: cloneValue(r.outbox)}
}

func (r *SQLRepository) restoreSnapshot(state persistedRepository) {
	r.MemoryRepository.mu.Lock()
	defer r.MemoryRepository.mu.Unlock()
	r.agents, r.squads, r.plans = state.Agents, state.Squads, state.Plans
	r.projections, r.keys, r.events, r.outbox = state.Projections, state.Keys, state.Events, state.Outbox
	if r.agents == nil {
		r.agents = map[string]AgentDefinition{}
	}
	if r.squads == nil {
		r.squads = map[string]SquadDefinition{}
	}
	if r.plans == nil {
		r.plans = map[string]RequirementExecutionPlan{}
	}
	if r.projections == nil {
		r.projections = map[string]PlanProjection{}
	}
	if r.keys == nil {
		r.keys = map[string]string{}
	}
	if r.events == nil {
		r.events = map[string][]Event{}
	}
	if r.outbox == nil {
		r.outbox = map[string]OutboxRecord{}
	}
}

func (r *SQLRepository) persistSnapshot(state persistedRepository) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin orchestration SQL transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if r.dialect == "postgres" {
		workspace := r.workspaceID
		if workspace == "" {
			workspace = snapshotWorkspace(state)
		}
		if workspace == "" {
			return errors.New("postgres orchestration scope requires workspace_id")
		}
		tenant := r.tenantID
		if tenant == "" {
			tenant = tenantForWorkspace(workspace)
		}
		if _, err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext('adro-orchestration-state'))"); err != nil {
			return fmt.Errorf("acquire orchestration advisory lock: %w", err)
		}
		if _, err := tx.Exec("SELECT set_config('app.tenant_id', "+r.placeholder(1)+", true)", tenant); err != nil {
			return fmt.Errorf("set orchestration tenant scope: %w", err)
		}
		if _, err := tx.Exec("SELECT set_config('app.workspace_id', "+r.placeholder(1)+", true)", workspace); err != nil {
			return fmt.Errorf("set orchestration workspace scope: %w", err)
		}
	}
	var current int64
	row := tx.QueryRow("SELECT revision FROM "+orchestrationSQLTable+" WHERE id="+r.placeholder(1), 1)
	queryErr := row.Scan(&current)
	if errors.Is(queryErr, sql.ErrNoRows) {
		current = 0
	} else if queryErr != nil {
		return fmt.Errorf("read orchestration SQL revision: %w", queryErr)
	}
	if current != r.revision {
		return fmt.Errorf("%w: expected revision %d, found %d", errSQLRevisionConflict, r.revision, current)
	}
	next := r.revision + 1
	state.Revision = next
	data, err = json.Marshal(state)
	if err != nil {
		return err
	}
	if current == 0 {
		if _, err := tx.Exec("INSERT INTO "+orchestrationSQLTable+" (id, revision, state_json) VALUES ("+r.placeholder(1)+", "+r.placeholder(2)+", "+r.placeholder(3)+")", 1, next, data); err != nil {
			return fmt.Errorf("insert orchestration SQL state: %w", err)
		}
	} else {
		result, err := tx.Exec("UPDATE "+orchestrationSQLTable+" SET revision="+r.placeholder(1)+", state_json="+r.placeholder(2)+" WHERE id="+r.placeholder(3)+" AND revision="+r.placeholder(4), next, data, 1, current)
		if err != nil {
			return fmt.Errorf("update orchestration SQL state: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return fmt.Errorf("%w: expected revision %d, found %d", errSQLRevisionConflict, r.revision, current)
		}
	}
	materialized := state
	materializeTenant, materializeWorkspace := "", ""
	if r.dialect == "postgres" {
		materializeTenant = strings.TrimSpace(r.tenantID)
		if materializeTenant == "" {
			materializeTenant = tenantForWorkspace(snapshotWorkspace(state))
		}
		materializeWorkspace = strings.TrimSpace(r.workspaceID)
		if materializeWorkspace == "" {
			materializeWorkspace = snapshotWorkspace(state)
		}
		if materializeTenant == "" || materializeWorkspace == "" {
			return errors.New("postgres orchestration scope requires tenant_id and workspace_id")
		}
		materialized = scopedSnapshot(state, materializeWorkspace)
	}
	if err := replaceLogicalRows(tx, materialized, r.placeholder, materializeTenant, materializeWorkspace, r.dialect); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit orchestration SQL state: %w", err)
	}
	r.revision = next
	return nil
}

// replaceLogicalRows materializes the repository contract in queryable SQL
// rows while retaining the validated snapshot as the compact recovery source.
// Every table is replaced in the same transaction as the snapshot CAS, so a
// reader never observes a partially updated plan or outbox.
func replaceLogicalRows(tx *sql.Tx, state persistedRepository, placeholder func(int) string, tenantID, workspaceID, dialect string) error {
	for _, table := range []string{"workflow_graphs", sqlDecisionTable, sqlAttemptTable, sqlNodeTable, sqlEventTable, sqlOutboxTable, sqlPlanTable, sqlSquadTable, sqlAgentTable} {
		deleteQuery := "DELETE FROM " + table
		deleteArgs := []any(nil)
		if dialect == "postgres" {
			// FORCE ROW LEVEL SECURITY rejects an unscoped DELETE. Restrict the
			// replacement to the transaction-local scope so another workspace's
			// materialized rows remain untouched.
			deleteQuery += " WHERE tenant_id=" + placeholder(1) + " AND workspace_id=" + placeholder(2)
			deleteArgs = []any{tenantID, workspaceID}
		}
		if _, err := tx.Exec(deleteQuery, deleteArgs...); err != nil {
			return fmt.Errorf("clear orchestration SQL table %s: %w", table, err)
		}
	}
	logicalTenant := func(workspace string) string {
		if tenantID != "" {
			return tenantID
		}
		return tenantForWorkspace(workspace)
	}
	args := func(values ...any) []any { return values }
	graphRows := make(map[string]struct {
		id        string
		workspace string
		version   int64
		graph     WorkflowGraph
	})
	addGraph := func(graph WorkflowGraph, workspace string) {
		if strings.TrimSpace(graph.ID) == "" || strings.TrimSpace(workspace) == "" || graph.Version <= 0 {
			return
		}
		key := workspace + "\x00" + graph.ID + fmt.Sprintf("\x00%d", graph.Version)
		graphRows[key] = struct {
			id        string
			workspace string
			version   int64
			graph     WorkflowGraph
		}{id: graph.ID, workspace: workspace, version: graph.Version, graph: graph}
	}
	for _, squad := range state.Squads {
		addGraph(squad.Graph, squad.WorkspaceID)
	}
	for _, plan := range state.Plans {
		addGraph(plan.GraphSnapshot, plan.WorkspaceID)
	}
	graphKeys := make([]string, 0, len(graphRows))
	for key := range graphRows {
		graphKeys = append(graphKeys, key)
	}
	sort.Strings(graphKeys)
	for _, key := range graphKeys {
		row := graphRows[key]
		blob, err := json.Marshal(row.graph)
		if err != nil {
			return err
		}
		digest := row.graph.ValidationDigest
		if digest == "" {
			digest, err = row.graph.CanonicalHash()
			if err != nil {
				return err
			}
		}
		q := "INSERT INTO workflow_graphs (id, tenant_id, workspace_id, version, graph_json, validation_digest) VALUES (" + placeholder(1) + "," + placeholder(2) + "," + placeholder(3) + "," + placeholder(4) + "," + placeholder(5) + "," + placeholder(6) + ")"
		if _, err := tx.Exec(q, row.id, logicalTenant(row.workspace), row.workspace, row.version, blob, digest); err != nil {
			return fmt.Errorf("write workflow graph: %w", err)
		}
	}
	keys := make([]string, 0, len(state.Agents))
	for key := range state.Agents {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		a := state.Agents[key]
		blob, err := json.Marshal(a)
		if err != nil {
			return err
		}
		q := "INSERT INTO " + sqlAgentTable + " (id, tenant_id, workspace_id, revision, name, role, spec_json, status, created_by, created_at, updated_at) VALUES (" + placeholder(1) + "," + placeholder(2) + "," + placeholder(3) + "," + placeholder(4) + "," + placeholder(5) + "," + placeholder(6) + "," + placeholder(7) + "," + placeholder(8) + "," + placeholder(9) + "," + placeholder(10) + "," + placeholder(11) + ")"
		if _, err := tx.Exec(q, args(a.ID, logicalTenant(a.WorkspaceID), a.WorkspaceID, a.Revision, a.Name, a.Role, blob, string(a.Status), a.CreatedBy, sqlTime(a.CreatedAt), sqlTime(a.UpdatedAt))...); err != nil {
			return fmt.Errorf("write agent definition: %w", err)
		}
	}
	keys = keys[:0]
	for key := range state.Squads {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		s := state.Squads[key]
		blob, err := json.Marshal(s)
		if err != nil {
			return err
		}
		q := "INSERT INTO " + sqlSquadTable + " (id, tenant_id, workspace_id, revision, name, spec_json, status, created_at, updated_at) VALUES (" + placeholder(1) + "," + placeholder(2) + "," + placeholder(3) + "," + placeholder(4) + "," + placeholder(5) + "," + placeholder(6) + "," + placeholder(7) + "," + placeholder(8) + "," + placeholder(9) + ")"
		if _, err := tx.Exec(q, args(s.ID, logicalTenant(s.WorkspaceID), s.WorkspaceID, s.Revision, s.Name, blob, string(s.Status), sqlTime(time.Time{}), sqlTime(time.Time{}))...); err != nil {
			return fmt.Errorf("write squad definition: %w", err)
		}
	}
	keys = keys[:0]
	for key := range state.Plans {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		p := state.Plans[key]
		blob, err := json.Marshal(p)
		if err != nil {
			return err
		}
		q := "INSERT INTO " + sqlPlanTable + " (id, tenant_id, requirement_id, workspace_id, plan_hash, snapshot_json, revision, status, idempotency_key, created_at) VALUES (" + placeholder(1) + "," + placeholder(2) + "," + placeholder(3) + "," + placeholder(4) + "," + placeholder(5) + "," + placeholder(6) + "," + placeholder(7) + "," + placeholder(8) + "," + placeholder(9) + "," + placeholder(10) + ")"
		if _, err := tx.Exec(q, args(p.ID, logicalTenant(p.WorkspaceID), p.RequirementID, p.WorkspaceID, p.PlanHash, blob, p.Revision, string(p.Status), p.IdempotencyKey, sqlTime(p.CreatedAt))...); err != nil {
			return fmt.Errorf("write execution plan: %w", err)
		}
		projection := state.Projections[p.ID]
		for nodeID, node := range projection.Nodes {
			nodeBlob, err := json.Marshal(node)
			if err != nil {
				return err
			}
			kind := nodeID
			for _, candidate := range p.GraphSnapshot.Nodes {
				if candidate.ID == nodeID {
					kind = string(candidate.Kind)
					break
				}
			}
			q = "INSERT INTO " + sqlNodeTable + " (plan_id, tenant_id, workspace_id, node_id, kind, status, projection_json) VALUES (" + placeholder(1) + "," + placeholder(2) + "," + placeholder(3) + "," + placeholder(4) + "," + placeholder(5) + "," + placeholder(6) + "," + placeholder(7) + ")"
			if _, err := tx.Exec(q, args(p.ID, logicalTenant(p.WorkspaceID), p.WorkspaceID, nodeID, kind, string(node.Status), nodeBlob)...); err != nil {
				return fmt.Errorf("write workflow node: %w", err)
			}
		}
		for attemptID, attempt := range projection.Attempts {
			inputBlob, err := json.Marshal(attempt.InputManifest)
			if err != nil {
				return err
			}
			resultBlob, err := json.Marshal(attempt.Result)
			if err != nil {
				return err
			}
			leaseBlob, err := json.Marshal(attempt.Lease)
			if err != nil {
				return err
			}
			q = "INSERT INTO " + sqlAttemptTable + " (id, plan_id, tenant_id, workspace_id, node_id, attempt_no, run_id, session_id, workdir, status, input_manifest_json, result_json, lease_json) VALUES (" + placeholder(1) + "," + placeholder(2) + "," + placeholder(3) + "," + placeholder(4) + "," + placeholder(5) + "," + placeholder(6) + "," + placeholder(7) + "," + placeholder(8) + "," + placeholder(9) + "," + placeholder(10) + "," + placeholder(11) + "," + placeholder(12) + "," + placeholder(13) + ")"
			if _, err := tx.Exec(q, args(attemptID, p.ID, logicalTenant(p.WorkspaceID), p.WorkspaceID, attempt.NodeID, attempt.AttemptNo, attempt.RunID, attempt.SessionID, attempt.WorkDir, string(attempt.Status), inputBlob, resultBlob, leaseBlob)...); err != nil {
				return fmt.Errorf("write node attempt: %w", err)
			}
		}
		for _, decision := range projection.Decisions {
			decisionBlob, err := json.Marshal(decision)
			if err != nil {
				return err
			}
			q = "INSERT INTO " + sqlDecisionTable + " (id, plan_id, tenant_id, workspace_id, source_attempt_id, edge_id, target_node_id, predicate_json, evidence_json, loop_count, idempotency_key) VALUES (" + placeholder(1) + "," + placeholder(2) + "," + placeholder(3) + "," + placeholder(4) + "," + placeholder(5) + "," + placeholder(6) + "," + placeholder(7) + "," + placeholder(8) + "," + placeholder(9) + "," + placeholder(10) + "," + placeholder(11) + ")"
			evidenceBlob, err := json.Marshal(decision.EvidenceIDs)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(q, args(decision.ID, p.ID, logicalTenant(p.WorkspaceID), p.WorkspaceID, decision.SourceAttempt, decision.EdgeID, decision.TargetNode, decisionBlob, evidenceBlob, decision.LoopCount, decision.IdempotencyKey)...); err != nil {
				return fmt.Errorf("write edge decision: %w", err)
			}
		}
	}
	for planID, chain := range state.Events {
		for _, event := range chain {
			q := "INSERT INTO " + sqlEventTable + " (event_id, tenant_id, workspace_id, plan_id, run_id, node_id, attempt_id, sequence, event_type, payload_json, payload_hash, previous_hash, envelope_hash, idempotency_key, writer_id, fencing_token, committed_at) VALUES (" + placeholder(1) + "," + placeholder(2) + "," + placeholder(3) + "," + placeholder(4) + "," + placeholder(5) + "," + placeholder(6) + "," + placeholder(7) + "," + placeholder(8) + "," + placeholder(9) + "," + placeholder(10) + "," + placeholder(11) + "," + placeholder(12) + "," + placeholder(13) + "," + placeholder(14) + "," + placeholder(15) + "," + placeholder(16) + "," + placeholder(17) + ")"
			if _, err := tx.Exec(q, args(event.ID, logicalTenant(event.WorkspaceID), event.WorkspaceID, planID, event.RunID, event.NodeID, event.AttemptID, event.Sequence, event.Type, []byte(event.Payload), event.PayloadHash, event.PreviousHash, event.EnvelopeHash, event.IdempotencyKey, "orchestration", event.FencingToken, event.CreatedAt)...); err != nil {
				return fmt.Errorf("write execution event: %w", err)
			}
		}
	}
	for _, outbox := range state.Outbox {
		blob, err := json.Marshal(outbox.Payload)
		if err != nil {
			return err
		}
		q := "INSERT INTO " + sqlOutboxTable + " (id, tenant_id, workspace_id, plan_id, idempotency_key, kind, payload_json, status, owner, lease_expires_at, attempts, max_attempts, last_error, created_at, updated_at) VALUES (" + placeholder(1) + "," + placeholder(2) + "," + placeholder(3) + "," + placeholder(4) + "," + placeholder(5) + "," + placeholder(6) + "," + placeholder(7) + "," + placeholder(8) + "," + placeholder(9) + "," + placeholder(10) + "," + placeholder(11) + "," + placeholder(12) + "," + placeholder(13) + "," + placeholder(14) + "," + placeholder(15) + ")"
		if _, err := tx.Exec(q, args(outbox.ID, logicalTenant(outbox.WorkspaceID), outbox.WorkspaceID, outbox.PlanID, outbox.IdempotencyKey, outbox.Kind, blob, outbox.Status, outbox.Owner, nullableTime(outbox.LeaseExpiresAt), outbox.Attempts, outbox.MaxAttempts, outbox.LastError, sqlTime(outbox.CreatedAt), sqlTime(outbox.UpdatedAt))...); err != nil {
			return fmt.Errorf("write outbox event: %w", err)
		}
	}
	return nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func sqlTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func tenantForWorkspace(workspace string) string {
	if configured := strings.TrimSpace(os.Getenv("ADRO_SQL_TENANT_ID")); configured != "" {
		return configured
	}
	return strings.TrimSpace(workspace)
}

func snapshotWorkspace(state persistedRepository) string {
	for _, plan := range state.Plans {
		if workspace := strings.TrimSpace(plan.WorkspaceID); workspace != "" {
			return workspace
		}
	}
	for _, agent := range state.Agents {
		if workspace := strings.TrimSpace(agent.WorkspaceID); workspace != "" {
			return workspace
		}
	}
	for _, squad := range state.Squads {
		if workspace := strings.TrimSpace(squad.WorkspaceID); workspace != "" {
			return workspace
		}
	}
	return ""
}

// scopedSnapshot narrows only the materialized view. The compact snapshot
// remains global so a SQL handle can refresh and recover every workspace, but
// a PostgreSQL RLS transaction may write rows for exactly one scope. Keeping
// this separation avoids cross-tenant DELETE/INSERT attempts while preserving
// the repository's single CAS revision.
func scopedSnapshot(state persistedRepository, workspaceID string) persistedRepository {
	workspaceID = strings.TrimSpace(workspaceID)
	out := persistedRepository{Version: state.Version, Revision: state.Revision,
		Agents: map[string]AgentDefinition{}, Squads: map[string]SquadDefinition{},
		Plans: map[string]RequirementExecutionPlan{}, Projections: map[string]PlanProjection{},
		Keys: map[string]string{}, Events: map[string][]Event{}, Outbox: map[string]OutboxRecord{}}
	for key, agent := range state.Agents {
		if agent.WorkspaceID == workspaceID {
			out.Agents[key] = cloneValue(agent)
		}
	}
	for key, squad := range state.Squads {
		if squad.WorkspaceID == workspaceID {
			out.Squads[key] = cloneValue(squad)
		}
	}
	for key, plan := range state.Plans {
		if plan.WorkspaceID != workspaceID {
			continue
		}
		out.Plans[key] = cloneValue(plan)
		if projection, ok := state.Projections[plan.ID]; ok {
			out.Projections[plan.ID] = cloneValue(projection)
		}
		if chain, ok := state.Events[plan.ID]; ok {
			out.Events[plan.ID] = cloneValue(chain)
		}
	}
	for key, record := range state.Outbox {
		if record.WorkspaceID == workspaceID {
			out.Outbox[key] = cloneValue(record)
		}
	}
	// Idempotency keys are scoped by workspace in MemoryRepository. Retaining
	// only this workspace prevents a RLS transaction from attempting to expose
	// another tenant's key material through a logical table in future adapters.
	for key, value := range state.Keys {
		if strings.HasPrefix(key, workspaceID+":") {
			out.Keys[key] = value
		}
	}
	return out
}

func (r *SQLRepository) mutate(fn func() error) error {
	if r == nil || r.MemoryRepository == nil || r.db == nil {
		return errors.New("sql repository is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// The advisory lock lives inside persistSnapshot's transaction. Another
	// replica can commit between this handle's refresh and that lock. Replay a
	// safe repository mutation against the new snapshot on a bounded CAS
	// conflict; domain revision checks still reject conflicting edits to the
	// same entity after refresh.
	for attempt := 0; attempt < 8; attempt++ {
		if err := r.refreshLocked(); err != nil {
			return err
		}
		before := r.snapshot()
		if err := fn(); err != nil {
			return err
		}
		state := r.snapshot()
		if err := r.persistSnapshot(state); err != nil {
			r.restoreSnapshot(before)
			r.revision = before.Revision
			if errors.Is(err, errSQLRevisionConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("%w after 8 retries", errSQLRevisionConflict)
}

func (r *SQLRepository) SaveAgent(a AgentDefinition, expected int64) error {
	return r.mutate(func() error { return r.MemoryRepository.SaveAgent(a, expected) })
}
func (r *SQLRepository) SaveSquad(s SquadDefinition, expected int64) error {
	return r.mutate(func() error { return r.MemoryRepository.SaveSquad(s, expected) })
}
func (r *SQLRepository) CreatePlan(p RequirementExecutionPlan) error {
	return r.mutate(func() error { return r.MemoryRepository.CreatePlan(p) })
}
func (r *SQLRepository) CreatePlanWithEvent(p RequirementExecutionPlan, e Event) error {
	return r.mutate(func() error { return r.MemoryRepository.CreatePlanWithEvent(p, e) })
}
func (r *SQLRepository) SaveProjection(p PlanProjection) error {
	return r.mutate(func() error { return r.MemoryRepository.SaveProjection(p) })
}
func (r *SQLRepository) AppendEvent(e Event) error {
	return r.mutate(func() error { return r.MemoryRepository.AppendEvent(e) })
}
func (r *SQLRepository) CommitEventProjection(e Event, p PlanProjection) error {
	return r.mutate(func() error { return r.MemoryRepository.CommitEventProjection(e, p) })
}
func (r *SQLRepository) EnqueueOutbox(o OutboxRecord) (OutboxRecord, bool, error) {
	var out OutboxRecord
	var created bool
	err := r.mutate(func() error {
		var err error
		out, created, err = r.MemoryRepository.EnqueueOutbox(o)
		return err
	})
	return out, created, err
}
func (r *SQLRepository) ClaimOutbox(planID, owner string, ttl time.Duration, now time.Time) (OutboxRecord, error) {
	var out OutboxRecord
	err := r.mutate(func() error {
		var err error
		out, err = r.MemoryRepository.ClaimOutbox(planID, owner, ttl, now)
		return err
	})
	return out, err
}
func (r *SQLRepository) ClaimOutboxByID(id, owner string, ttl time.Duration, now time.Time) (OutboxRecord, error) {
	var out OutboxRecord
	err := r.mutate(func() error {
		var err error
		out, err = r.MemoryRepository.ClaimOutboxByID(id, owner, ttl, now)
		return err
	})
	return out, err
}
func (r *SQLRepository) AckOutbox(id, owner string, now time.Time, deliveryErr error) error {
	return r.mutate(func() error { return r.MemoryRepository.AckOutbox(id, owner, now, deliveryErr) })
}
func (r *SQLRepository) FailOutbox(id, owner string, now time.Time, reason string) error {
	return r.mutate(func() error { return r.MemoryRepository.FailOutbox(id, owner, now, reason) })
}

// Backup/Restore use the same validated snapshot format as the file profile,
// but Restore commits the resulting state through the SQL transaction. This
// makes disaster-recovery tests exercise the real adapter rather than only its
// embedded in-memory implementation.
func (r *SQLRepository) Backup(path string) error {
	if err := r.refresh(); err != nil {
		return err
	}
	state := r.snapshot()
	if err := validateRepositoryState(state); err != nil {
		return err
	}
	return writeRepositorySnapshot(path, state)
}

func (r *SQLRepository) Restore(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read orchestration SQL backup: %w", err)
	}
	var state persistedRepository
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode orchestration SQL backup: %w", err)
	}
	if err := validateRepositoryState(state); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.refreshLocked(); err != nil {
		return err
	}
	before := r.snapshot()
	r.restoreSnapshot(state)
	if err := r.persistSnapshot(r.snapshot()); err != nil {
		r.restoreSnapshot(before)
		return fmt.Errorf("persist restored orchestration SQL state: %w", err)
	}
	return nil
}

// Compile-time checks keep this adapter honest as the repository contract
// evolves.
var _ Repository = (*SQLRepository)(nil)
