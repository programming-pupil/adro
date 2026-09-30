package orchestration

import (
	"context"
	"errors"
	"github.com/adro-project/adro/internal/events"
	graphmodel "github.com/adro-project/adro/internal/orchestration/graph"
	"github.com/adro-project/adro/internal/provider"
	"reflect"
	"testing"
	"time"
)

func TestProviderOutcomeRequiresExplicitStructuredMarker(t *testing.T) {
	if outcome, fields := providerOutcome("the QA report says this is a bug"); outcome != "" || fields != nil {
		t.Fatalf("free-form output was classified: outcome=%q fields=%v", outcome, fields)
	}

	outcome, fields := providerOutcome("ADRO_RESULT_JSON={\"final_outcome\":\"bug\",\"fields\":{\"bug\":true}}")
	if outcome != "bug" || !reflect.DeepEqual(fields["bug"], true) || fields["provider_outcome"] != "bug" {
		t.Fatalf("structured bug marker was not classified: outcome=%q fields=%v", outcome, fields)
	}
}

func TestProviderOutcomeUsesLastValidStructuredRecord(t *testing.T) {
	output := "{\"outcome\":\"failure\"}\n" +
		"ADRO_RESULT_JSON={\"adro_outcome\":\"pass\",\"fields\":{\"tests\":3}}\n"
	outcome, fields := providerOutcome(output)
	if outcome != "pass" || fields["tests"] != float64(3) {
		t.Fatalf("last structured record was not selected: outcome=%q fields=%v", outcome, fields)
	}
}

func TestProviderOutcomeReadsCodexAgentMessageJSONL(t *testing.T) {
	output := `{"type":"item.completed","item":{"type":"command_execution","aggregated_output":"ADRO_RESULT_JSON={\"outcome\":\"bug\"}"}}
{"type":"item.completed","item":{"type":"agent_message","text":"ADRO_RESULT_JSON={\"outcome\":\"failure\",\"reason_code\":\"unit_failed\",\"summary\":\"unit test failed\",\"evidence_ids\":[\"unit-1\"],\"fields\":{\"exit_code\":1}}"}}`
	outcome, fields := providerOutcome(output)
	if outcome != "failure" {
		t.Fatalf("Codex agent message was not classified: outcome=%q fields=%v", outcome, fields)
	}
	if fields["provider_reason_code"] != "unit_failed" || fields["provider_summary"] != "unit test failed" {
		t.Fatalf("provider metadata was not retained: fields=%v", fields)
	}
	evidence, ok := fields["provider_evidence_ids"].([]string)
	if !ok || len(evidence) != 1 || evidence[0] != "unit-1" {
		t.Fatalf("provider evidence was not retained: %#v", fields["provider_evidence_ids"])
	}
	if fields["exit_code"] != float64(1) {
		t.Fatalf("provider fields were not retained: fields=%v", fields)
	}
}

func TestProviderOutcomeReadsCodexAppServerAgentMessage(t *testing.T) {
	output := `{"method":"item/completed","params":{"item":{"type":"agentMessage","text":"ADRO_RESULT_JSON={\"outcome\":\"pass\",\"reason_code\":\"design\"}"}}}`
	outcome, fields := providerOutcome(output)
	if outcome != "pass" || fields["provider_reason_code"] != "design" {
		t.Fatalf("app-server agent message was not classified: outcome=%q fields=%v", outcome, fields)
	}
}

func TestProviderOutcomeReadsNestedCodexAgentMessageContent(t *testing.T) {
	output := `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":[{"type":"Text","text":"ADRO_RESULT_JSON={\"outcome\":\"bug\",\"reason_code\":\"qa_bug\"}"}]}}}`
	outcome, fields := providerOutcome(output)
	if outcome != "bug" || fields["provider_reason_code"] != "qa_bug" {
		t.Fatalf("nested Codex agent message was not classified: outcome=%q fields=%v", outcome, fields)
	}
}

func TestProviderToolEvidenceRequiresMatchedBeforeAfterPair(t *testing.T) {
	base := provider.RunSnapshot{Output: `{"type":"item.completed","item":{"type":"command_execution"}}`}
	if hasProviderToolEvidence(base) {
		t.Fatal("command_execution text without tool events was accepted")
	}
	base.ToolEvents = []provider.ToolEvent{{CallID: "call-1", Name: "command_execution", Phase: "before"}}
	if hasProviderToolEvidence(base) {
		t.Fatal("unpaired command_execution event was accepted")
	}
	base.ToolEvents = append(base.ToolEvents, provider.ToolEvent{CallID: "call-1", Name: "command_execution", Phase: "after"})
	if !hasProviderToolEvidence(base) {
		t.Fatal("matched command_execution before/after pair was rejected")
	}

	appServer := provider.RunSnapshot{Output: `{"method":"item/completed","params":{"item":{"type":"commandExecution"}}}`}
	appServer.ToolEvents = []provider.ToolEvent{
		{CallID: "exec-1", Name: "commandExecution", Phase: "before"},
		{CallID: "exec-1", Name: "commandExecution", Phase: "after"},
	}
	if !hasProviderToolEvidence(appServer) {
		t.Fatal("camelCase app-server commandExecution evidence was rejected")
	}

	dsh := provider.RunSnapshot{ExecutorPath: "/usr/local/bin/dsh", Output: `{"v":1,"type":"result","output":"done"}`}
	dsh.ToolEvents = []provider.ToolEvent{
		{CallID: "tool-1", Name: "bash", Phase: "before"},
		{CallID: "tool-1", Name: "bash", Phase: "after"},
	}
	if !hasProviderToolEvidence(dsh) {
		t.Fatal("DSH native tool call/result evidence was rejected")
	}
	dsh.ToolEvents[1].CallID = "other-tool"
	if hasProviderToolEvidence(dsh) {
		t.Fatal("unmatched DSH tool evidence was accepted")
	}
}

func TestProviderOutcomeReadsDSHTerminalResult(t *testing.T) {
	output := `{"v":1,"type":"result","status":"completed","output":"ADRO_RESULT_JSON={\"outcome\":\"pass\",\"reason_code\":\"dsh_real\",\"evidence_ids\":[\"dsh-1\"]}"}`
	outcome, fields := providerOutcome(output)
	if outcome != "pass" || fields["provider_reason_code"] != "dsh_real" {
		t.Fatalf("DSH terminal result was not classified: outcome=%q fields=%v", outcome, fields)
	}
}

func TestWorkerCancellationClosesRunningAttemptAndLeavesTerminalProjection(t *testing.T) {
	plan, err := (graphmodel.RequirementExecutionPlan{
		ID: "cancel-plan", RequirementID: "req", WorkspaceID: "w",
		GraphSnapshot: graphmodel.WorkflowGraph{ID: "cancel-graph", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []graphmodel.WorkflowNode{{ID: "node", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "agent", Revision: 1}}}},
		Status:        graphmodel.PlanDraft,
	}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	lease := graphmodel.Lease{Key: "cancel-plan:node", Owner: "worker", FencingToken: now.UnixNano(), ExpiresAt: now.Add(time.Minute)}
	attempt, err := projection.StartAttempt(plan, "node", "attempt-1", 1, lease, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: lease.FencingToken, IdempotencyKey: "cancel-dispatch", PayloadHash: "payload", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	attempt.RunID = "provider-run-1"
	projection.Attempts[attempt.ID] = attempt
	provider := provider.NewMockProvider(events.NewBus())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker := Worker{Scheduler: Scheduler{Executor: Executor{Provider: provider}, Config: SchedulerConfig{Now: func() time.Time { return now }}}}
	_, runErr := worker.Run(ctx, plan, &projection, testEnvelope(), "work", "")
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("worker error=%v, want context cancellation", runErr)
	}
	if projection.Status != graphmodel.PlanTerminal || projection.TerminalOutcome != "timed_out" {
		t.Fatalf("cancelled graph was not terminalized: status=%s outcome=%q", projection.Status, projection.TerminalOutcome)
	}
	if got := projection.Attempts[attempt.ID].Status; got != graphmodel.AttemptTimedOut {
		t.Fatalf("active attempt status=%s, want timed_out", got)
	}
}

func TestWorkerReconcileResolvesProviderFromFrozenAgent(t *testing.T) {
	repo := NewMemoryRepository()
	agent := graphmodel.AgentDefinition{
		ID: "selected-agent", WorkspaceID: "w", Revision: 1, Name: "selected", Status: graphmodel.AgentActive,
		ExecutorBinding: graphmodel.ExecutorBinding{ProviderID: "local", RuntimeID: "codex"},
		InputSchema:     graphmodel.SchemaRef{ID: "input", Version: 1},
		OutputSchema:    graphmodel.SchemaRef{ID: "output", Version: 1},
	}
	if err := repo.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	plan, err := (graphmodel.RequirementExecutionPlan{
		ID: "selected-provider-plan", RequirementID: "req", WorkspaceID: "w",
		GraphSnapshot: graphmodel.WorkflowGraph{ID: "selected-provider-graph", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []graphmodel.WorkflowNode{{ID: "node", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: agent.ID, Revision: 1}}}},
		Status:        graphmodel.PlanDraft,
	}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	lease := graphmodel.Lease{Key: "selected-provider-plan:node", Owner: "worker", FencingToken: now.UnixNano(), ExpiresAt: now.Add(time.Minute)}
	attempt, err := projection.StartAttempt(plan, "node", "selected-attempt", 1, lease, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: lease.FencingToken, IdempotencyKey: "selected-dispatch", PayloadHash: "payload", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	attempt.RunID = "selected-run"
	projection.Attempts[attempt.ID] = attempt
	selected := &blockingProvider{MockProvider: provider.NewMockProvider(events.NewBus())}
	fallback := provider.NewMockProvider(events.NewBus())
	worker := Worker{Scheduler: Scheduler{Repository: repo, Executor: Executor{
		Provider: fallback, Repository: repo,
		ProviderResolver: func(_ context.Context, resolved graphmodel.AgentDefinition) (provider.ExecutionProvider, error) {
			if resolved.ID != agent.ID || resolved.Revision != agent.Revision {
				t.Fatalf("resolved agent=%+v", resolved)
			}
			return selected, nil
		},
	}, Config: SchedulerConfig{Now: func() time.Time { return now }}}}
	finished, err := worker.Reconcile(context.Background(), plan, &projection)
	if err != nil || len(finished) != 0 {
		t.Fatalf("finished=%+v err=%v", finished, err)
	}
}
