package compat

import (
	"testing"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/orchestration"
)

func TestGraphFromPipelineEncodesBoundedFeedbackAndTerminalFallback(t *testing.T) {
	run := domain.PipelineRun{
		ID: "pipeline", WorkspaceID: "workspace", RequirementID: "requirement", SessionID: "session",
		PipelineStage: domain.PipelineDesign, Status: domain.PipelineRunning,
		Roles:      domain.PipelineAgentRoles{Designer: "designer", Developer: "developer", Tester: "tester", Arbitrator: "arbitrator"},
		MaxRetries: 3, CoverageThreshold: 80, Version: 1,
	}
	graph, mapping, err := GraphFromPipeline(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.ExitNodeIDs) != len(graph.Nodes) {
		t.Fatalf("compatibility graph must allow fail-closed terminal at every stage: exits=%d nodes=%d", len(graph.ExitNodeIDs), len(graph.Nodes))
	}
	development := mapping["2"]
	arbitration := mapping["5"]
	foundFeedback := false
	for _, edge := range graph.Edges {
		if edge.From == arbitration && edge.To == development && edge.On == orchestration.EdgeSuccess {
			foundFeedback = edge.MaxTraversals == run.MaxRetries+1
			break
		}
	}
	if !foundFeedback {
		t.Fatal("arbitration-to-development feedback edge is missing or unbounded")
	}
}

func TestPipelineDispatchScopeIsStableAndStageScoped(t *testing.T) {
	run := domain.PipelineRun{ID: "pipeline", PipelineStage: domain.PipelineUnitTest}
	first, err := PipelineDispatchScope(run, "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := PipelineDispatchScope(run, "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.NodeID != PipelineNodeID(domain.PipelineUnitTest) {
		t.Fatalf("scope is not deterministic: first=%+v second=%+v", first, second)
	}
	run.PipelineStage = domain.PipelineIntegration
	other, err := PipelineDispatchScope(run, "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if other.AttemptID == first.AttemptID || other.NodeID == first.NodeID {
		t.Fatalf("different stages shared graph identity: first=%+v other=%+v", first, other)
	}
}
