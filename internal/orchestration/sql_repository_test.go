package orchestration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type sqlTestState struct {
	mu       sync.Mutex
	exists   bool
	revision int64
	data     []byte
}

type sqlTestDriver struct {
	mu     sync.Mutex
	states map[string]*sqlTestState
}

type sqlTestConn struct {
	state  *sqlTestState
	active *sqlTestTx
}

type sqlTestTx struct {
	conn     *sqlTestConn
	exists   bool
	revision int64
	data     []byte
	done     bool
}

type sqlTestResult int64

func (r sqlTestResult) LastInsertId() (int64, error) { return 0, nil }
func (r sqlTestResult) RowsAffected() (int64, error) { return int64(r), nil }

type sqlTestRows struct {
	columns []string
	values  []driver.Value
	read    bool
}

type sqlTestStmt struct {
	conn  *sqlTestConn
	query string
}

func (d *sqlTestDriver) Open(name string) (driver.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.states == nil {
		d.states = map[string]*sqlTestState{}
	}
	state := d.states[name]
	if state == nil {
		state = &sqlTestState{}
		d.states[name] = state
	}
	return &sqlTestConn{state: state}, nil
}

func (c *sqlTestConn) Close() error { return nil }
func (c *sqlTestConn) Prepare(query string) (driver.Stmt, error) {
	return &sqlTestStmt{conn: c, query: query}, nil
}
func (c *sqlTestConn) Begin() (driver.Tx, error) {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	if c.active != nil {
		return nil, errors.New("transaction already active")
	}
	tx := &sqlTestTx{conn: c, exists: c.state.exists, revision: c.state.revision, data: append([]byte(nil), c.state.data...)}
	c.active = tx
	return tx, nil
}
func (c *sqlTestConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) { return c.Begin() }

func (c *sqlTestConn) Exec(query string, args []driver.Value) (driver.Result, error) {
	if c.active != nil {
		return execSQLTest(c.active, query, args)
	}
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	tx := &sqlTestTx{conn: c, exists: c.state.exists, revision: c.state.revision, data: c.state.data}
	result, err := execSQLTest(tx, query, args)
	if err == nil {
		c.state.exists, c.state.revision, c.state.data = tx.exists, tx.revision, append([]byte(nil), tx.data...)
	}
	return result, err
}
func (c *sqlTestConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	return c.Exec(query, values)
}
func (c *sqlTestConn) Query(query string, args []driver.Value) (driver.Rows, error) {
	if c.active != nil {
		return c.active.Query(query, args)
	}
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	return querySQLTest(c.state, query, args)
}
func (c *sqlTestConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	return c.Query(query, values)
}
func (c *sqlTestConn) CheckNamedValue(value *driver.NamedValue) error { return nil }

func (s *sqlTestStmt) Close() error  { return nil }
func (s *sqlTestStmt) NumInput() int { return -1 }
func (s *sqlTestStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.conn.Exec(s.query, args)
}
func (s *sqlTestStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.conn.Query(s.query, args)
}

func (t *sqlTestTx) Commit() error {
	if t.done {
		return errors.New("transaction already closed")
	}
	t.conn.state.mu.Lock()
	t.conn.state.exists, t.conn.state.revision, t.conn.state.data = t.exists, t.revision, append([]byte(nil), t.data...)
	t.conn.state.mu.Unlock()
	t.conn.active = nil
	t.done = true
	return nil
}
func (t *sqlTestTx) Rollback() error { t.done = true; t.conn.active = nil; return nil }
func (t *sqlTestTx) Exec(query string, args []driver.Value) (driver.Result, error) {
	return execSQLTest(t, query, args)
}
func (t *sqlTestTx) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	return t.Exec(query, values)
}
func (t *sqlTestTx) Query(query string, args []driver.Value) (driver.Rows, error) {
	state := &sqlTestState{exists: t.exists, revision: t.revision, data: t.data}
	return querySQLTest(state, query, args)
}
func (t *sqlTestTx) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	return t.Query(query, values)
}

func execSQLTest(tx *sqlTestTx, query string, args []driver.Value) (driver.Result, error) {
	upper := strings.ToUpper(strings.TrimSpace(query))
	if strings.HasPrefix(upper, "CREATE TABLE") {
		return sqlTestResult(0), nil
	}
	// The contract driver models only the compact snapshot row; materialized
	// tables and their indexes are schema-only concerns for these tests. Accept
	// both ordinary and partial unique index DDL so schema initialization stays
	// covered without pretending this tiny driver enforces SQL constraints.
	if strings.HasPrefix(upper, "CREATE INDEX") || strings.HasPrefix(upper, "CREATE UNIQUE INDEX") {
		return sqlTestResult(0), nil
	}
	if strings.HasPrefix(upper, "INSERT INTO") {
		if !strings.Contains(upper, "ADRO_ORCHESTRATION_STATE") {
			return sqlTestResult(1), nil
		}
		if tx.exists {
			return nil, errors.New("duplicate key")
		}
		tx.exists = true
		tx.revision = int64(args[1].(int64))
		tx.data = append([]byte(nil), args[2].([]byte)...)
		return sqlTestResult(1), nil
	}
	if strings.HasPrefix(upper, "UPDATE") {
		if !strings.Contains(upper, "ADRO_ORCHESTRATION_STATE") {
			return sqlTestResult(1), nil
		}
		if !tx.exists || tx.revision != int64(args[3].(int64)) {
			return sqlTestResult(0), nil
		}
		tx.revision = int64(args[0].(int64))
		tx.data = append([]byte(nil), args[1].([]byte)...)
		return sqlTestResult(1), nil
	}
	if strings.HasPrefix(upper, "DELETE FROM") {
		return sqlTestResult(1), nil
	}
	return nil, errors.New("unsupported SQL test exec: " + query)
}

func querySQLTest(state *sqlTestState, query string, _ []driver.Value) (driver.Rows, error) {
	includeState := strings.Contains(strings.ToUpper(query), "STATE_JSON")
	if !state.exists {
		if includeState {
			return &sqlTestRows{columns: []string{"revision", "state_json"}}, nil
		}
		return &sqlTestRows{columns: []string{"revision"}}, nil
	}
	if strings.Contains(strings.ToUpper(query), "SELECT REVISION") {
		if includeState {
			return &sqlTestRows{columns: []string{"revision", "state_json"}, values: []driver.Value{state.revision, append([]byte(nil), state.data...)}}, nil
		}
		return &sqlTestRows{columns: []string{"revision"}, values: []driver.Value{state.revision}}, nil
	}
	return nil, errors.New("unsupported SQL test query: " + query)
}
func (r *sqlTestRows) Columns() []string { return r.columns }
func (r *sqlTestRows) Close() error      { return nil }
func (r *sqlTestRows) Next(dest []driver.Value) error {
	if r.read || len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values)
	r.read = true
	return nil
}

func TestSQLRepositoryTransactionAndRecoveryContract(t *testing.T) {
	name := "sql-contract-" + t.Name()
	driverName := "adro-sql-contract"
	registerSQLTestDriver(t, driverName)
	db, err := sql.Open(driverName, name)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	repo, err := NewSQLiteSQLRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := (RequirementExecutionPlan{ID: "sql-plan", RequirementID: "req", WorkspaceID: "ws", GraphSnapshot: graphForTest(), Status: PlanDraft}).Freeze()
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
	event, err := NewEvent(nil, plan.ID, plan.WorkspaceID, "plan.created", "sql-plan", plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(event); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSQLiteSQLRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reopened.GetPlan("ws", plan.ID); err != nil || got.PlanHash != plan.PlanHash {
		t.Fatalf("reopened plan=%+v err=%v", got, err)
	}
	backup := t.TempDir() + "/orchestration.backup.json"
	if err := reopened.Backup(backup); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Restore(backup); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.GetProjection(plan.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSQLRepositoryMutationsAreDurableWithoutExplicitFlush(t *testing.T) {
	name := "sql-auto-commit-" + t.Name()
	driverName := "adro-sql-auto-commit"
	registerSQLTestDriver(t, driverName)
	db, err := sql.Open(driverName, name)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	repo, err := NewSQLiteSQLRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	agent := AgentDefinition{ID: "durable-agent", WorkspaceID: "ws", Revision: 1, Name: "durable", Status: AgentDraft, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := repo.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSQLiteSQLRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reopened.GetAgent("ws", agent.ID, 0); err != nil || got.ID != agent.ID {
		t.Fatalf("agent was not committed by mutation: got=%+v err=%v", got, err)
	}
}

func registerSQLTestDriver(t *testing.T, name string) {
	t.Helper()
	d := &sqlTestDriver{}
	// database/sql rejects duplicate registration, so each test uses a unique
	// name and registers once for its process lifetime.
	sql.Register(name, d)
}
