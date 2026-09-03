package orchestration

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestSQLiteDriverConformance exercises the adapter against a real on-disk
// SQLite engine. The custom driver contract tests remain useful for fault
// injection, but they cannot catch SQL typing, locking, or transaction errors
// in the actual deployment profile.
func TestSQLiteDriverConformance(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "orchestration.db")
	db1, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db1.Close()
	db1.SetMaxOpenConns(1)
	r1, err := NewSQLiteSQLRepository(db1)
	if err != nil {
		t.Fatal(err)
	}

	db2, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	db2.SetMaxOpenConns(1)
	r2, err := NewSQLiteSQLRepository(db2)
	if err != nil {
		t.Fatal(err)
	}

	agent := AgentDefinition{ID: "sqlite-agent", WorkspaceID: "ws", Revision: 1, Name: "SQLite agent", Status: AgentActive, ExecutorBinding: ExecutorBinding{ProviderID: "local"}, InputSchema: SchemaRef{ID: "input", Version: 1}, OutputSchema: SchemaRef{ID: "output", Version: 1}}
	if err := r1.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	if got, err := r2.GetAgent("ws", agent.ID, 1); err != nil || got.Name != agent.Name {
		t.Fatalf("cross-handle agent refresh got=%+v err=%v", got, err)
	}

	plan, err := (RequirementExecutionPlan{ID: "sqlite-plan", RequirementID: "req", WorkspaceID: "ws", GraphSnapshot: WorkflowGraph{ID: "sqlite-graph", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []WorkflowNode{{ID: "node", Kind: NodeGate}}}, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	if err := r1.CreatePlan(plan); err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := r1.SaveProjection(projection); err != nil {
		t.Fatal(err)
	}
	if got, err := r2.GetPlan("ws", plan.ID); err != nil || got.PlanHash != plan.PlanHash {
		t.Fatalf("cross-handle plan refresh got=%+v err=%v", got, err)
	}

	event, err := NewEvent(nil, plan.ID, plan.WorkspaceID, "plan.created", "sqlite-plan-created", map[string]any{"source": "real-sqlite"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r1.AppendEvent(event); err != nil {
		t.Fatal(err)
	}
	if got := r2.ListEvents(plan.ID, 0); len(got) != 1 || got[0].EnvelopeHash == "" {
		t.Fatalf("cross-handle event refresh got=%+v", got)
	}

	outbox, created, err := r1.EnqueueOutbox(OutboxRecord{ID: "sqlite-outbox", PlanID: plan.ID, WorkspaceID: plan.WorkspaceID, Kind: "provider.start", IdempotencyKey: "sqlite-start", Payload: map[string]any{"attempt": 1}, CreatedAt: time.Now().UTC()})
	if err != nil || !created || outbox.ID == "" {
		t.Fatalf("enqueue outbox=%+v created=%v err=%v", outbox, created, err)
	}
	duplicate, created, err := r2.EnqueueOutbox(OutboxRecord{ID: "different-id", PlanID: plan.ID, WorkspaceID: plan.WorkspaceID, Kind: "provider.start", IdempotencyKey: "sqlite-start", Payload: map[string]any{"attempt": 1}})
	if err != nil || created || duplicate.ID != outbox.ID {
		t.Fatalf("duplicate outbox=%+v created=%v err=%v", duplicate, created, err)
	}

	backup := filepath.Join(t.TempDir(), "orchestration.backup.json")
	if err := r1.Backup(backup); err != nil {
		t.Fatal(err)
	}
	if err := r2.Restore(backup); err != nil {
		t.Fatal(err)
	}
	if got, err := r2.GetProjection(plan.ID); err != nil || got.PlanID != plan.ID {
		t.Fatalf("restored projection=%+v err=%v", got, err)
	}
}

func TestSQLiteMaterializationKeepsWorkspaceRowsAndGraphFacts(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "orchestration-scoped.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	repo, err := NewSQLiteSQLRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, workspace := range []string{"workspace-a", "workspace-b"} {
		agent := AgentDefinition{ID: "agent-" + workspace, WorkspaceID: workspace, Revision: 1, Name: workspace, Status: AgentActive, ExecutorBinding: ExecutorBinding{ProviderID: "local"}, InputSchema: SchemaRef{ID: "input", Version: 1}, OutputSchema: SchemaRef{ID: "output", Version: 1}}
		if err := repo.SaveAgent(agent, 0); err != nil {
			t.Fatal(err)
		}
		graph := WorkflowGraph{ID: "graph-" + workspace, Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []WorkflowNode{{ID: "node", Kind: NodeGate}}}
		plan, freezeErr := (RequirementExecutionPlan{ID: "plan-" + workspace, RequirementID: "req-" + workspace, WorkspaceID: workspace, GraphSnapshot: graph, Status: PlanDraft}).Freeze()
		if freezeErr != nil {
			t.Fatal(freezeErr)
		}
		if err := repo.CreatePlan(plan); err != nil {
			t.Fatal(err)
		}
		projection, projectionErr := NewProjection(plan)
		if projectionErr != nil {
			t.Fatal(projectionErr)
		}
		if err := repo.SaveProjection(projection); err != nil {
			t.Fatal(err)
		}
	}
	var agents, plans, graphs int
	if err := db.QueryRow("SELECT count(*) FROM agent_definitions").Scan(&agents); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM execution_plans").Scan(&plans); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM workflow_graphs").Scan(&graphs); err != nil {
		t.Fatal(err)
	}
	if agents != 2 || plans != 2 || graphs != 2 {
		t.Fatalf("materialized workspace rows were lost: agents=%d plans=%d graphs=%d", agents, plans, graphs)
	}
	var scope string
	if err := db.QueryRow("SELECT workspace_id FROM workflow_graphs WHERE id = ?", "graph-workspace-a").Scan(&scope); err != nil {
		t.Fatal(err)
	}
	if scope != "workspace-a" {
		t.Fatalf("graph scope=%q", scope)
	}
}
