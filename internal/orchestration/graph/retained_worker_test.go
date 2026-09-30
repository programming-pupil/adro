package graph_test

import (
	graphmodel "github.com/adro-project/adro/internal/orchestration/graph"
	"testing"
)

func TestCloneProjectionPreservesRepairLineageMaps(t *testing.T) {
	original := graphmodel.PlanProjection{RepairPlans: map[string]graphmodel.RepairPlan{
		"repair-1": {
			ID:                  "repair-1",
			PlanID:              "plan-1",
			RepairNodeID:        "repair",
			RepairAttemptID:     "repair-attempt-1",
			TargetNodeID:        "developer",
			VerificationNodeIDs: []string{"unit", "qa"},
			MaxRounds:           2,
			Round:               1,
			State:               graphmodel.RepairVerifying,
			StateHistory:        []graphmodel.RepairLifecycle{graphmodel.RepairPlanned, graphmodel.RepairDispatched, graphmodel.RepairPatched, graphmodel.RepairVerifying},
			TargetAttemptID:     "developer-attempt-2",
			VerificationAttempts: map[string]string{
				"unit": "unit-attempt-2",
			},
			VerifiedNodes: map[string]bool{"unit": true},
		},
	}}

	cloned := graphmodel.CloneProjection(original)
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

func TestMissingProviderResultIsRetryableForRepairLifecycle(t *testing.T) {
	attempt := graphmodel.NodeAttempt{
		Status: graphmodel.AttemptFailed,
		FailureReason: &graphmodel.FailureReason{
			Code:      "provider_result_missing",
			Retryable: true,
		},
	}
	if !graphmodel.IsRetryableRepairProviderFailure(attempt) {
		t.Fatal("missing structured provider result must be retryable")
	}
}
