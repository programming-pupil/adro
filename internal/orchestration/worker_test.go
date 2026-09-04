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

func TestProviderOutcomeReadsNestedCodexAgentMessageContent(t *testing.T) {
	output := `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":[{"type":"Text","text":"ADRO_RESULT_JSON={\"outcome\":\"bug\",\"reason_code\":\"qa_bug\"}"}]}}}`
	outcome, fields := providerOutcome(output)
	if outcome != "bug" || fields["provider_reason_code"] != "qa_bug" {
		t.Fatalf("nested Codex agent message was not classified: outcome=%q fields=%v", outcome, fields)
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
