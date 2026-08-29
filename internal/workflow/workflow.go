// Package workflow contains deterministic quality-gate decisions. Temporal
// activities can call this pure engine and persist the returned transition.
package workflow

import (
	"fmt"

	"github.com/adro-project/adro/internal/domain"
)

type GateInput struct {
	Name           string
	Decision       string
	RepairAttempts int
}

type Engine struct{ MaxRepairAttempts int }

func NewEngine(maxRepairAttempts int) Engine {
	if maxRepairAttempts < 1 {
		maxRepairAttempts = 3
	}
	return Engine{MaxRepairAttempts: maxRepairAttempts}
}

// ApplyGate returns the only status transition permitted for a gate. A failed
// machine gate never advances a requirement, and repair attempts are bounded.
func (e Engine) ApplyGate(current domain.RequirementStatus, gate GateInput) (domain.RequirementStatus, error) {
	decision := gate.Decision
	if decision != "pass" && decision != "fail" {
		return current, fmt.Errorf("gate decision must be pass or fail")
	}
	to := current
	switch gate.Name {
	case "design":
		if decision == "pass" {
			to = domain.RequirementDesignReview
		} else {
			to = domain.RequirementDesignRework
		}
	case "design_review":
		if decision == "pass" {
			to = domain.RequirementDeveloping
		} else {
			to = domain.RequirementDesignRework
		}
	case "unit_test":
		if decision == "pass" {
			to = domain.RequirementUnitVerified
		} else {
			to = domain.RequirementTestFailed
		}
	case "api_doc":
		if decision == "pass" {
			to = domain.RequirementAPIDocReady
		}
	case "test_deploy":
		if decision == "pass" {
			to = domain.RequirementTesting
		} else {
			to = domain.RequirementBlockedEnvironment
		}
	case "api_log_data":
		if decision == "pass" {
			to = domain.RequirementReadyForHumanQA
		} else {
			to = domain.RequirementTestFailed
		}
	case "repair":
		if gate.RepairAttempts >= e.MaxRepairAttempts {
			to = domain.RequirementHumanTriageRequired
		} else if decision == "pass" {
			to = domain.RequirementDeveloping
		} else {
			to = domain.RequirementTestFailed
		}
	default:
		return current, fmt.Errorf("unknown gate %q", gate.Name)
	}
	if err := domain.Transition(current, to); err != nil {
		return current, err
	}
	return to, nil
}
