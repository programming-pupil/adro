package workflow

import (
	"github.com/adro-project/adro/internal/domain"
	"testing"
)

func TestEngineRejectsFailedUnitGateAndCapsRepair(t *testing.T) {
	e := NewEngine(3)
	if _, err := e.ApplyGate(domain.RequirementDeveloping, GateInput{Name: "unit_test", Decision: "pass"}); err != nil {
		t.Fatal(err)
	}
	failed, err := e.ApplyGate(domain.RequirementTesting, GateInput{Name: "api_log_data", Decision: "fail"})
	if err != nil || failed != domain.RequirementTestFailed {
		t.Fatalf("failed gate: %s %v", failed, err)
	}
	escalated, err := e.ApplyGate(domain.RequirementTestFailed, GateInput{Name: "repair", Decision: "fail", RepairAttempts: 3})
	if err != nil || escalated != domain.RequirementHumanTriageRequired {
		t.Fatalf("repair cap: %s %v", escalated, err)
	}
}
