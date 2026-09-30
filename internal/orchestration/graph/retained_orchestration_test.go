package graph_test

import (
	"context"
	"github.com/adro-project/adro/core/budget"
	graphmodel "github.com/adro-project/adro/internal/orchestration/graph"
	"regexp"
	"strings"
	"testing"
)

func TestDiagnoseGraphSummarizesExecutionControls(t *testing.T) {
	graph := graphmodel.WorkflowGraph{
		ID: "diagnostics", Version: 1, EntryNodeIDs: []string{"a"}, ExitNodeIDs: []string{"review"},
		Nodes: []graphmodel.WorkflowNode{
			{ID: "a", Kind: graphmodel.NodeAgent, RetryPolicy: graphmodel.RetryPolicy{MaxAttempts: 3}, Budget: budget.Budget{Tokens: 100, ToolCalls: 2, Concurrent: 2}},
			{ID: "b", Kind: graphmodel.NodeMerge, JoinPolicy: graphmodel.JoinAll, Budget: budget.Budget{Tokens: 50}},
			{ID: "review", Kind: graphmodel.NodeHuman},
		},
		Edges: []graphmodel.WorkflowEdge{{ID: "loop", From: "b", To: "a", On: graphmodel.EdgeFailure, LoopGroup: "repair", MaxTraversals: 2, RequiredEvidence: []string{"test"}}},
	}
	d := graphmodel.DiagnoseGraph(graph)
	if d.NodeCount != 3 || d.EdgeCount != 1 || d.AgentNodeCount != 1 || d.StructuralNodeCount != 1 || d.HumanNodeCount != 1 || !d.RequiresHuman {
		t.Fatalf("unexpected graph counts: %+v", d)
	}
	if len(d.JoinNodeIDs) != 1 || d.JoinNodeIDs[0] != "b" || len(d.LoopEdgeIDs) != 1 || d.LoopEdgeIDs[0] != "loop" || len(d.RetryNodeIDs) != 1 || d.RetryNodeIDs[0] != "a" {
		t.Fatalf("execution controls were not summarized: %+v", d)
	}
	if d.TokenBudget != 150 || d.ToolCallBudget != 2 || d.MaxConcurrency != 2 || d.RequiredEvidenceEdges != 1 {
		t.Fatalf("budget summary=%+v", d)
	}
}

func TestRepairControllerFailsClosedWithoutReachableVerification(t *testing.T) {
	graph := graphmodel.WorkflowGraph{ID: "repair-invalid-runtime", Version: 1, EntryNodeIDs: []string{"repair"}, ExitNodeIDs: []string{"repair"}, Nodes: []graphmodel.WorkflowNode{
		{ID: "repair", Kind: graphmodel.NodeRepair, RepairPolicy: graphmodel.RepairPolicy{TargetNodeID: "target", VerificationNodeIDs: []string{"missing"}, MaxRounds: 1}},
		{ID: "target", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "dev", Revision: 1}},
	}, Edges: []graphmodel.WorkflowEdge{{ID: "repair-target", From: "repair", To: "target", On: graphmodel.EdgeSuccess, MaxTraversals: 1}}}
	plan := graphmodel.RequirementExecutionPlan{ID: "repair-invalid-runtime-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanReady, Revision: 1}
	decision, err := (graphmodel.DefaultRepairController{}).PlanRepair(context.Background(), graphmodel.StructuralInput{Plan: plan, Node: graph.Nodes[0], Incoming: []graphmodel.StructuralSource{{Attempt: graphmodel.NodeAttempt{ID: "failed", Result: graphmodel.StructuredResult{EvidenceIDs: []string{"failure"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Event != "failure" || decision.Result.ReasonCode != graphmodel.RepairReasonVerificationUnreachable || decision.Failure == nil {
		t.Fatalf("unreachable verification was not rejected: %+v", decision)
	}
}

func TestValidateGraphDerivesAUniqueRepairTarget(t *testing.T) {
	base := graphmodel.WorkflowGraph{
		ID: "repair-derived-target", Version: 1, EntryNodeIDs: []string{"source"}, ExitNodeIDs: []string{"verify"},
		Nodes: []graphmodel.WorkflowNode{
			{ID: "source", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "source-agent", Revision: 1}},
			{ID: "repair", Kind: graphmodel.NodeRepair, RepairPolicy: graphmodel.RepairPolicy{VerificationNodeIDs: []string{"verify"}, MaxRounds: 1}},
			{ID: "patch", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "developer", Revision: 1}},
			{ID: "verify", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "qa", Revision: 1}},
		},
		Edges: []graphmodel.WorkflowEdge{
			{ID: "source-repair", From: "source", To: "repair", On: graphmodel.EdgeBug, MaxTraversals: 1},
			{ID: "repair-patch", From: "repair", To: "patch", On: graphmodel.EdgeSuccess, MaxTraversals: 1},
			{ID: "patch-verify", From: "patch", To: "verify", On: graphmodel.EdgeSuccess, MaxTraversals: 1},
		},
	}
	if err := graphmodel.ValidateGraph(base); err != nil {
		t.Fatalf("unique success target should be derivable: %v", err)
	}
	ambiguous := base
	ambiguous.ID = "repair-ambiguous-target"
	ambiguous.Nodes = append(append([]graphmodel.WorkflowNode(nil), base.Nodes...), graphmodel.WorkflowNode{ID: "patch-alt", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "developer-2", Revision: 1}})
	ambiguous.Edges = append(append([]graphmodel.WorkflowEdge(nil), base.Edges...), graphmodel.WorkflowEdge{ID: "repair-patch-alt", From: "repair", To: "patch-alt", On: graphmodel.EdgeSuccess, MaxTraversals: 1}, graphmodel.WorkflowEdge{ID: "patch-alt-verify", From: "patch-alt", To: "verify", On: graphmodel.EdgeSuccess, MaxTraversals: 1})
	if err := graphmodel.ValidateGraph(ambiguous); err == nil || !strings.Contains(err.Error(), "target_node_id.required_or_uniquely_derivable") {
		t.Fatalf("ambiguous success targets must fail closed: %v", err)
	}
}

func TestOrchestrationIDsAreUUIDs(t *testing.T) {
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(graphmodel.NewID()) {
		t.Fatal("orchestration id is not a canonical UUID")
	}
}

func TestValidateGraphRequiresStableID(t *testing.T) {
	graph := graphmodel.WorkflowGraph{Version: 1, EntryNodeIDs: []string{"gate"}, ExitNodeIDs: []string{"gate"}, Nodes: []graphmodel.WorkflowNode{{ID: "gate", Kind: graphmodel.NodeGate}}}
	if err := graphmodel.ValidateGraph(graph); err == nil || !strings.Contains(err.Error(), "graph.id.required") {
		t.Fatalf("expected graph.id.required, got %v", err)
	}
}

func TestValidateGraphAcceptsCycleWithOneBoundedEdgeDeterministically(t *testing.T) {
	graph := graphmodel.WorkflowGraph{
		ID: "bounded-feedback", Version: 1, EntryNodeIDs: []string{"agent"}, ExitNodeIDs: []string{"gate"},
		Nodes: []graphmodel.WorkflowNode{{ID: "agent", Kind: graphmodel.NodeGate}, {ID: "gate", Kind: graphmodel.NodeGate}},
		Edges: []graphmodel.WorkflowEdge{
			{ID: "agent-to-gate", From: "agent", To: "gate", On: graphmodel.EdgeSuccess},
			{ID: "gate-to-agent", From: "gate", To: "agent", On: graphmodel.EdgeSuccess, MaxTraversals: 1},
		},
	}
	for i := 0; i < 100; i++ {
		if err := graphmodel.ValidateGraph(graph); err != nil {
			t.Fatalf("bounded feedback graph rejected on iteration %d: %v", i, err)
		}
	}
}

func TestValidateGraphRejectsRemainingUnboundedSubcycle(t *testing.T) {
	graph := graphmodel.WorkflowGraph{
		ID: "partially-bounded-feedback", Version: 1, EntryNodeIDs: []string{"a"}, ExitNodeIDs: []string{"c"},
		Nodes: []graphmodel.WorkflowNode{{ID: "a", Kind: graphmodel.NodeGate}, {ID: "b", Kind: graphmodel.NodeGate}, {ID: "c", Kind: graphmodel.NodeGate}},
		Edges: []graphmodel.WorkflowEdge{
			{ID: "a-to-b", From: "a", To: "b", On: graphmodel.EdgeSuccess},
			{ID: "b-to-a", From: "b", To: "a", On: graphmodel.EdgeFailure},
			{ID: "b-to-c", From: "b", To: "c", On: graphmodel.EdgeSuccess, MaxTraversals: 1},
		},
	}
	if err := graphmodel.ValidateGraph(graph); err == nil || !strings.Contains(err.Error(), "max_traversals.required") {
		t.Fatalf("expected unbounded subcycle rejection, got %v", err)
	}
}

func TestLoopGroupRequiresHumanExit(t *testing.T) {
	graph := graphmodel.WorkflowGraph{ID: "bounded-loop", Version: 1, EntryNodeIDs: []string{"a"}, ExitNodeIDs: []string{"a"}, Nodes: []graphmodel.WorkflowNode{{ID: "a", Kind: graphmodel.NodeGate}}, Edges: []graphmodel.WorkflowEdge{{ID: "loop", From: "a", To: "a", On: graphmodel.EdgeSuccess, LoopGroup: "repair", MaxTraversals: 2}}}
	if err := graphmodel.ValidateGraph(graph); err == nil || !strings.Contains(err.Error(), "human_exit.required") {
		t.Fatalf("expected loop human exit error, got %v", err)
	}
	graph.Nodes = append(graph.Nodes, graphmodel.WorkflowNode{ID: "human", Kind: graphmodel.NodeHuman})
	graph.ExitNodeIDs = []string{"human"}
	graph.Edges = append(graph.Edges, graphmodel.WorkflowEdge{ID: "to-human", From: "a", To: "human", On: graphmodel.EdgeFailure, MaxTraversals: 1})
	if err := graphmodel.ValidateGraph(graph); err != nil {
		t.Fatalf("loop with human exit rejected: %v", err)
	}
}
