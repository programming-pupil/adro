package orchestration

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// TestPostgresDriverConformance runs against a real PostgreSQL server when a
// DSN is supplied by CI or an operator. It is intentionally skipped otherwise:
// a local unit run must not silently claim production database evidence.
func TestPostgresDriverConformance(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("ADRO_POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("set ADRO_POSTGRES_TEST_DSN to run PostgreSQL conformance")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(4)
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}

	workspace := "pg-conformance-" + strings.ReplaceAll(t.Name(), "/", "-")
	tenant := "tenant-" + workspace
	repo, err := NewPostgresSQLRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	repo.SetScope(tenant, workspace)

	agent := AgentDefinition{
		ID: "agent-" + workspace, WorkspaceID: workspace, Revision: 1,
		Name: "Postgres conformance", Status: AgentActive,
		ExecutorBinding: ExecutorBinding{ProviderID: "local"},
		InputSchema:     SchemaRef{ID: "input", Version: 1}, OutputSchema: SchemaRef{ID: "output", Version: 1},
	}
	if err := repo.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}

	reader, err := NewPostgresSQLRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	reader.SetScope(tenant, workspace)
	if got, err := reader.GetAgent(workspace, agent.ID, 1); err != nil || got.ID != agent.ID {
		t.Fatalf("cross-connection agent=%+v err=%v", got, err)
	}

	plan, err := (RequirementExecutionPlan{
		ID: "plan-" + workspace, RequirementID: "req-" + workspace, WorkspaceID: workspace,
		GraphSnapshot: WorkflowGraph{ID: "graph-" + workspace, Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []WorkflowNode{{ID: "node", Kind: NodeGate}}},
		Status:        PlanDraft, IdempotencyKey: "idempotency-" + workspace,
	}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreatePlan(plan); err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveProjection(projection); err != nil {
		t.Fatal(err)
	}
	event, err := NewEvent(nil, plan.ID, workspace, "plan.created", "event-"+workspace, map[string]any{"source": "postgres"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(event); err != nil {
		t.Fatal(err)
	}
	outbox, created, err := repo.EnqueueOutbox(OutboxRecord{ID: "outbox-" + workspace, PlanID: plan.ID, WorkspaceID: workspace, Kind: "provider.start", IdempotencyKey: "outbox-" + workspace, Payload: map[string]any{"attempt": 1}, CreatedAt: time.Now().UTC()})
	if err != nil || !created || outbox.ID == "" {
		t.Fatalf("outbox=%+v created=%v err=%v", outbox, created, err)
	}

	if got, err := reader.GetPlan(workspace, plan.ID); err != nil || got.PlanHash != plan.PlanHash {
		t.Fatalf("cross-connection plan=%+v err=%v", got, err)
	}
	if got := reader.ListEvents(plan.ID, 0); len(got) != 1 || got[0].EnvelopeHash == "" {
		t.Fatalf("events=%+v", got)
	}
	if got, err := reader.GetProjection(plan.ID); err != nil || got.PlanID != plan.ID {
		t.Fatalf("projection=%+v err=%v", got, err)
	}
	if got := reader.ListOutbox(plan.ID, "pending"); len(got) != 1 || got[0].IdempotencyKey != outbox.IdempotencyKey {
		t.Fatalf("outbox rows=%+v", got)
	}
	var unscoped int
	if err := db.QueryRow("SELECT count(*) FROM agent_definitions").Scan(&unscoped); err != nil {
		t.Fatal(err)
	}
	if unscoped != 0 {
		t.Fatalf("RLS exposed %d rows without transaction scope", unscoped)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("SELECT set_config('app.tenant_id', $1, true)", tenant); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec("SELECT set_config('app.workspace_id', $1, true)", workspace); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	var scopedRows int
	if err := tx.QueryRow("SELECT count(*) FROM agent_definitions").Scan(&scopedRows); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if scopedRows != 1 {
		t.Fatalf("RLS scoped rows=%d", scopedRows)
	}

	// Two independently constructed repositories model two API replicas. They
	// may start from the same revision, but non-conflicting mutations must both
	// survive the advisory-lock/CAS window.
	writers := make([]*SQLRepository, 2)
	for i := range writers {
		writers[i], err = NewPostgresSQLRepository(db)
		if err != nil {
			t.Fatal(err)
		}
		writers[i].SetScope(tenant, workspace)
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(writers))
	for i, writer := range writers {
		wg.Add(1)
		go func(index int, writer *SQLRepository) {
			defer wg.Done()
			candidate := agent
			candidate.ID = "replica-agent-" + string(rune('a'+index)) + "-" + workspace
			candidate.Name = "Replica writer " + string(rune('A'+index))
			errs <- writer.SaveAgent(candidate, 0)
		}(i, writer)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("multi-replica mutation failed: %v", err)
		}
	}
	for _, id := range []string{"replica-agent-a-" + workspace, "replica-agent-b-" + workspace} {
		if got, err := reader.GetAgent(workspace, id, 1); err != nil || got.ID != id {
			t.Fatalf("replica mutation %s missing: got=%+v err=%v", id, got, err)
		}
	}

	backup := filepath.Join(t.TempDir(), "postgres-orchestration.backup.json")
	if err := reader.Backup(backup); err != nil {
		t.Fatal(err)
	}
	marker := agent
	marker.ID, marker.Name = "restore-marker-"+workspace, "must disappear after restore"
	if err := reader.SaveAgent(marker, 0); err != nil {
		t.Fatal(err)
	}
	if err := reader.Restore(backup); err != nil {
		t.Fatal(err)
	}
	restored, err := NewPostgresSQLRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	restored.SetScope(tenant, workspace)
	if _, err := restored.GetAgent(workspace, marker.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore retained post-backup marker: %v", err)
	}
	if got, err := restored.GetPlan(workspace, plan.ID); err != nil || got.PlanHash != plan.PlanHash {
		t.Fatalf("restore lost plan: got=%+v err=%v", got, err)
	}

	// A scoped handle must not be able to read another workspace from the
	// compact recovery snapshot, even though the SQL tables contain both rows.
	foreign, err := NewPostgresSQLRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	foreign.SetScope(tenant, workspace+"-foreign")
	if _, err := foreign.GetPlan(workspace, plan.ID); err == nil {
		t.Fatal("foreign workspace unexpectedly read a plan")
	}
}
