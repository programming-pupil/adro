package orchestration

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/provider"
)

func normalizedProviderResources(snapshot provider.RunSnapshot) (ResourceVector, json.RawMessage, error) {
	tokens, err := safeUsageSum(snapshot.Usage.InputTokens, snapshot.Usage.OutputTokens)
	if err != nil {
		return ResourceVector{}, nil, err
	}
	duration := snapshot.Usage.DurationMS
	if duration < 0 {
		duration = 0
	}
	if duration > math.MaxInt64/int64(time.Millisecond) {
		return ResourceVector{}, nil, fmt.Errorf("%w: wall_time_nanos", ErrResourceOverflow)
	}
	vector := ResourceVector{
		Tokens: tokens, ToolCalls: int64(len(snapshot.ToolEvents)),
		WallTimeNanos: duration * int64(time.Millisecond), OutputBytes: int64(len([]byte(snapshot.Output))),
		ConcurrencySlots: 1,
	}
	raw, err := json.Marshal(map[string]any{"provider_usage": snapshot.Usage, "tool_event_count": len(snapshot.ToolEvents)})
	if err != nil {
		return ResourceVector{}, nil, err
	}
	return vector, raw, nil
}

func safeUsageSum(values ...int64) (int64, error) {
	var total int64
	for _, value := range values {
		if value < 0 {
			continue
		}
		if value > math.MaxInt64-total {
			return 0, fmt.Errorf("%w: tokens", ErrResourceOverflow)
		}
		total += value
	}
	return total, nil
}

func settleAttemptReservation(ledger *ResourceLedger, plan RequirementExecutionPlan, attempt NodeAttempt, raw json.RawMessage, normalized ResourceVector, providerMissing bool, now time.Time) error {
	if ledger == nil || strings.TrimSpace(attempt.ResourceReservationID) == "" {
		return nil
	}
	reservation, err := ledger.GetReservation(attempt.ResourceReservationID)
	if err != nil {
		return err
	}
	if !reservation.active() {
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
	reports := []ResourceLimitReport(nil)
	if existing, usageErr := ledger.GetUsage(usageID); usageErr == nil {
		if existing.ReservationID != reservation.ID {
			return ErrResourceConflict
		}
	} else if !errors.Is(usageErr, ErrResourceNotFound) {
		return usageErr
	} else {
		record, recordErr := NewUsageRecord(usageID, reservation.ID, scope, raw, normalized, reservation.State.Requested, providerMissing, false, now)
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
