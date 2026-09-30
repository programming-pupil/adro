package orchestration

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/adro-project/adro/core/budget"
	graphmodel "github.com/adro-project/adro/internal/orchestration/graph"
	"github.com/adro-project/adro/internal/provider"
)

func normalizedProviderResources(snapshot provider.RunSnapshot) (budget.ResourceVector, json.RawMessage, error) {
	vector, err := budget.NormalizeUsage(snapshot.Usage.InputTokens, snapshot.Usage.OutputTokens, snapshot.Usage.DurationMS, int64(len(snapshot.ToolEvents)), int64(len([]byte(snapshot.Output))))
	if err != nil {
		return budget.ResourceVector{}, nil, err
	}
	raw, err := json.Marshal(map[string]any{"provider_usage": snapshot.Usage, "tool_event_count": len(snapshot.ToolEvents)})
	if err != nil {
		return budget.ResourceVector{}, nil, err
	}
	return vector, raw, nil
}

func settleAttemptReservation(ledger *ResourceLedger, plan graphmodel.RequirementExecutionPlan, attempt graphmodel.NodeAttempt, raw json.RawMessage, normalized budget.ResourceVector, providerMissing bool, now time.Time) error {
	if ledger == nil || strings.TrimSpace(attempt.ResourceReservationID) == "" {
		return nil
	}
	reservation, err := ledger.GetReservation(attempt.ResourceReservationID)
	if err != nil {
		return err
	}
	if !reservation.Active() {
		return nil
	}
	scope := reservation.Scope
	if scope.SessionID == "" {
		scope.SessionID = attempt.SessionID
		if scope.SessionID == "" {
			scope.SessionID = attempt.InputManifest.Manifest.SessionID
		}
	}
	if scope.StepID == "" {
		scope.StepID = attempt.ID
	}
	if scope.ModelCallID == "" && scope.ToolEffectID == "" {
		scope.ModelCallID = attempt.RunID
		if scope.ModelCallID == "" {
			scope.ModelCallID = "attempt:" + attempt.ID
		}
	}
	if scope.CostCenter == "" {
		scope.CostCenter = plan.RequirementID
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	usageID := "usage:" + attempt.ID
	reports := []budget.ResourceLimitReport(nil)
	if existing, usageErr := ledger.GetUsage(usageID); usageErr == nil {
		if existing.ReservationID != reservation.ID {
			return budget.ErrResourceConflict
		}
	} else if !errors.Is(usageErr, budget.ErrResourceNotFound) {
		return usageErr
	} else {
		record, recordErr := budget.NewUsageRecord(usageID, reservation.ID, scope, raw, normalized, reservation.State.Requested, providerMissing, false, now)
		if recordErr != nil {
			return recordErr
		}
		_, _, reports, recordErr = ledger.RecordUsage(record)
		if recordErr != nil {
			return recordErr
		}
	}
	reason := "attempt_terminal"
	if current, currentErr := ledger.GetReservation(reservation.ID); currentErr == nil && current.TerminalReason == "hard_limit_exceeded" {
		reason = "hard_limit_exceeded"
	}
	for _, report := range reports {
		if !report.HardExcess.IsZero() {
			reason = "hard_limit_exceeded"
			break
		}
	}
	_, err = ledger.Settle(reservation.ID, "settle:"+attempt.ID, reason, now)
	return err
}
