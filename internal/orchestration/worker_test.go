package orchestration

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/provider"
)

func TestCloneProjectionPreservesRepairLineageMaps(t *testing.T) {
	original := PlanProjection{RepairPlans: map[string]RepairPlan{
		"repair-1": {
			ID:                  "repair-1",
			PlanID:              "plan-1",
			RepairNodeID:        "repair",
			RepairAttemptID:     "repair-attempt-1",
			TargetNodeID:        "developer",
			VerificationNodeIDs: []string{"unit", "qa"},
			MaxRounds:           2,
			Round:               1,
			State:               RepairVerifying,
			StateHistory:        []RepairLifecycle{RepairPlanned, RepairDispatched, RepairPatched, RepairVerifying},
			TargetAttemptID:     "developer-attempt-2",
			VerificationAttempts: map[string]string{
				"unit": "unit-attempt-2",
			},
			VerifiedNodes: map[string]bool{"unit": true},
		},
	}}

	cloned := cloneProjection(original)
	got := cloned.RepairPlans["repair-1"]
	if got.VerificationAttempts["unit"] != "unit-attempt-2" || !got.VerifiedNodes["unit"] {
		t.Fatalf("repair lineage maps were lost while cloning: %+v", got)
	}
	got.VerificationAttempts["qa"] = "unit-test-only"
	got.VerifiedNodes["qa"] = true
	if _, ok := original.RepairPlans["repair-1"].VerificationAttempts["qa"]; ok {
		t.Fatal("clone shares verification attempts map with original")
	}
	if original.RepairPlans["repair-1"].VerifiedNodes["qa"] {
		t.Fatal("clone shares verified nodes map with original")
	}
}

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

func TestMissingProviderResultIsRetryableForRepairLifecycle(t *testing.T) {
	attempt := NodeAttempt{
		Status: AttemptFailed,
		FailureReason: &FailureReason{
			Code:      "provider_result_missing",
			Retryable: true,
		},
	}
	if !isRetryableRepairProviderFailure(attempt) {
		t.Fatal("missing structured provider result must be retryable")
	}
}

func TestWorkerCancellationClosesRunningAttemptAndLeavesTerminalProjection(t *testing.T) {
	plan, err := (RequirementExecutionPlan{
		ID: "cancel-plan", RequirementID: "req", WorkspaceID: "w",
		GraphSnapshot: WorkflowGraph{ID: "cancel-graph", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []WorkflowNode{{ID: "node", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "agent", Revision: 1}}}},
		Status:        PlanDraft,
	}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	lease := Lease{Key: "cancel-plan:node", Owner: "worker", FencingToken: now.UnixNano(), ExpiresAt: now.Add(time.Minute)}
	attempt, err := projection.StartAttempt(plan, "node", "attempt-1", 1, lease, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: lease.FencingToken, IdempotencyKey: "cancel-dispatch", PayloadHash: "payload", Now: now})
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
	if projection.Status != PlanTerminal || projection.TerminalOutcome != "timed_out" {
		t.Fatalf("cancelled graph was not terminalized: status=%s outcome=%q", projection.Status, projection.TerminalOutcome)
	}
	if got := projection.Attempts[attempt.ID].Status; got != AttemptTimedOut {
		t.Fatalf("active attempt status=%s, want timed_out", got)
	}
}

func TestWorkerReconcileResolvesProviderFromFrozenAgent(t *testing.T) {
	repo := NewMemoryRepository()
	agent := AgentDefinition{
		ID: "selected-agent", WorkspaceID: "w", Revision: 1, Name: "selected", Status: AgentActive,
		ExecutorBinding: ExecutorBinding{ProviderID: "local", RuntimeID: "codex"},
		InputSchema:     SchemaRef{ID: "input", Version: 1},
		OutputSchema:    SchemaRef{ID: "output", Version: 1},
	}
	if err := repo.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	plan, err := (RequirementExecutionPlan{
		ID: "selected-provider-plan", RequirementID: "req", WorkspaceID: "w",
		GraphSnapshot: WorkflowGraph{ID: "selected-provider-graph", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []WorkflowNode{{ID: "node", Kind: NodeAgent, AgentRef: &VersionedRef{ID: agent.ID, Revision: 1}}}},
		Status:        PlanDraft,
	}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	lease := Lease{Key: "selected-provider-plan:node", Owner: "worker", FencingToken: now.UnixNano(), ExpiresAt: now.Add(time.Minute)}
	attempt, err := projection.StartAttempt(plan, "node", "selected-attempt", 1, lease, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: lease.FencingToken, IdempotencyKey: "selected-dispatch", PayloadHash: "payload", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	attempt.RunID = "selected-run"
	projection.Attempts[attempt.ID] = attempt
	selected := &blockingProvider{MockProvider: provider.NewMockProvider(events.NewBus())}
	fallback := provider.NewMockProvider(events.NewBus())
	worker := Worker{Scheduler: Scheduler{Repository: repo, Executor: Executor{
		Provider: fallback, Repository: repo,
		ProviderResolver: func(_ context.Context, resolved AgentDefinition) (provider.ExecutionProvider, error) {
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
