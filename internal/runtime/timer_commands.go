package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	EffectTimeoutCommandName   = "runtime.effect.timeout"
	SleepCommandName           = "runtime.session.sleep"
	ScheduledResumeCommandName = "runtime.session.resume"
)

type effectTimeoutPayload struct {
	EffectID    string          `json:"effect_id"`
	InputDigest string          `json:"input_digest"`
	Generation  int64           `json:"generation"`
	Reconcile   ReconcilePolicy `json:"reconcile_policy"`
}

type continuationTimerPayload struct {
	ContinuationID string `json:"continuation_id"`
	Generation     int64  `json:"generation"`
	Reason         string `json:"reason"`
}

// EffectTimeoutTimerSpec persists a timeout command before an external effect
// is dispatched. A worker must compare the effect digest and generation before
// applying the timeout; a late receipt therefore converges instead of racing a
// mutable in-memory timer.
func EffectTimeoutTimerSpec(scope Scope, effectID, inputDigest string, generation int64, dueAt time.Time, policy ReconcilePolicy) (TimerSpec, error) {
	if !scope.valid() || strings.TrimSpace(effectID) == "" || strings.TrimSpace(inputDigest) == "" || generation < 1 || dueAt.IsZero() || !policy.valid() || policy == ReconcileNone {
		return TimerSpec{}, errors.New("invalid effect timeout timer")
	}
	key := fmt.Sprintf("effect:%s:timeout:g%d", effectID, generation)
	return TimerSpec{ScheduleKey: key, Scope: scope, DueAt: dueAt.UTC(), Generation: generation, Policy: TimerExpire, MaxLateness: 24 * time.Hour, Command: TimerCommand{Name: EffectTimeoutCommandName, IdempotencyKey: key, Payload: effectTimeoutPayload{EffectID: effectID, InputDigest: inputDigest, Generation: generation, Reconcile: policy}}, StreamID: "session:" + scope.SessionID}, nil
}

// SleepTimerSpec models a durable pause boundary. It carries only a
// continuation identity and reason; the continuation itself is reconstructed
// from committed events after the timer fires.
func SleepTimerSpec(scope Scope, continuationID, reason string, generation int64, dueAt time.Time) (TimerSpec, error) {
	return continuationSpec(scope, SleepCommandName, continuationID, reason, generation, dueAt)
}

func ScheduledResumeTimerSpec(scope Scope, continuationID, reason string, generation int64, dueAt time.Time) (TimerSpec, error) {
	return continuationSpec(scope, ScheduledResumeCommandName, continuationID, reason, generation, dueAt)
}

func continuationSpec(scope Scope, command, continuationID, reason string, generation int64, dueAt time.Time) (TimerSpec, error) {
	if !scope.valid() || strings.TrimSpace(continuationID) == "" || strings.TrimSpace(reason) == "" || generation < 1 || dueAt.IsZero() {
		return TimerSpec{}, errors.New("invalid continuation timer")
	}
	key := fmt.Sprintf("continuation:%s:%s:g%d", command, continuationID, generation)
	return TimerSpec{ScheduleKey: key, Scope: scope, DueAt: dueAt.UTC(), Generation: generation, Policy: TimerCoalesce, MaxCatchUp: 1, Command: TimerCommand{Name: command, IdempotencyKey: key, Payload: continuationTimerPayload{ContinuationID: continuationID, Generation: generation, Reason: reason}}, StreamID: "session:" + scope.SessionID}, nil
}

func decodeEffectTimeoutClaim(claim TimerClaim) (effectTimeoutPayload, error) {
	if claim.Timer.Command.Name != EffectTimeoutCommandName || claim.Timer.State != TimerClaimed || claim.Timer.FencingToken <= 0 || claim.Timer.ClaimKey != claim.OccurrenceKey || claim.OccurrenceKey == "" {
		return effectTimeoutPayload{}, ErrTimerConflict
	}
	data, err := json.Marshal(claim.Timer.Command.Payload)
	if err != nil {
		return effectTimeoutPayload{}, ErrTimerConflict
	}
	var payload effectTimeoutPayload
	if err := json.Unmarshal(data, &payload); err != nil || strings.TrimSpace(payload.EffectID) == "" || strings.TrimSpace(payload.InputDigest) == "" || payload.Generation < 1 || !payload.Reconcile.valid() || payload.Reconcile == ReconcileNone || claim.Timer.Generation != payload.Generation {
		return effectTimeoutPayload{}, ErrTimerConflict
	}
	return payload, nil
}

func decodeContinuationClaim(claim TimerClaim, command string) (continuationTimerPayload, error) {
	if claim.Timer.Command.Name != command || claim.Timer.State != TimerClaimed || claim.Timer.FencingToken <= 0 || claim.Timer.ClaimKey != claim.OccurrenceKey || claim.OccurrenceKey == "" {
		return continuationTimerPayload{}, ErrTimerConflict
	}
	data, err := json.Marshal(claim.Timer.Command.Payload)
	if err != nil {
		return continuationTimerPayload{}, ErrTimerConflict
	}
	var payload continuationTimerPayload
	if err := json.Unmarshal(data, &payload); err != nil || strings.TrimSpace(payload.ContinuationID) == "" || strings.TrimSpace(payload.Reason) == "" || payload.Generation < 1 || claim.Timer.Generation != payload.Generation {
		return continuationTimerPayload{}, ErrTimerConflict
	}
	return payload, nil
}
