package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// EventTimerScheduleRequested is an authoritative request for a timer
// projection. The event is committed with the human/model/effect transition;
// a worker later materializes it in TimerStore idempotently. This keeps the
// event stream authoritative even when the timer backend is temporarily down.
const EventTimerScheduleRequested = "timer.schedule_requested"

// TimerScheduleRequest is the versioned event payload used to project a
// TimerSpec into a durable TimerStore. It intentionally carries no worker
// lease or mutable timer state.
type TimerScheduleRequest struct {
	ScheduleKey      string        `json:"schedule_key"`
	Scope            Scope         `json:"scope"`
	DueAt            time.Time     `json:"due_at"`
	Interval         time.Duration `json:"interval,omitempty"`
	Command          TimerCommand  `json:"command"`
	StreamID         string        `json:"stream_id,omitempty"`
	ExpectedSequence int64         `json:"expected_sequence"`
	Generation       int64         `json:"generation"`
	Policy           string        `json:"policy"`
	MaxCatchUp       int           `json:"max_catch_up"`
	MaxLateness      time.Duration `json:"max_lateness,omitempty"`
}

func NewTimerScheduleRequest(spec TimerSpec) (TimerScheduleRequest, error) {
	if err := validateTimerSpec(spec); err != nil {
		return TimerScheduleRequest{}, err
	}
	return TimerScheduleRequest{ScheduleKey: spec.ScheduleKey, Scope: spec.Scope, DueAt: spec.DueAt.UTC(), Interval: spec.Interval, Command: cloneCommand(spec.Command), StreamID: spec.StreamID, ExpectedSequence: spec.ExpectedSequence, Generation: spec.Generation, Policy: normalizeTimerPolicy(spec.Policy), MaxCatchUp: normalizeMaxCatchUp(spec.MaxCatchUp), MaxLateness: spec.MaxLateness}, nil
}

func (r TimerScheduleRequest) Spec() (TimerSpec, error) {
	spec := TimerSpec{ScheduleKey: strings.TrimSpace(r.ScheduleKey), Scope: r.Scope, DueAt: r.DueAt.UTC(), Interval: r.Interval, Command: cloneCommand(r.Command), StreamID: strings.TrimSpace(r.StreamID), ExpectedSequence: r.ExpectedSequence, Generation: r.Generation, Policy: r.Policy, MaxCatchUp: r.MaxCatchUp, MaxLateness: r.MaxLateness}
	if err := validateTimerSpec(spec); err != nil {
		return TimerSpec{}, err
	}
	return spec, nil
}

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

// validateTimerClaimShape verifies the immutable, lease-bound fields copied
// from TimerStore into a command claim. The timer worker lease is distinct
// from the runtime journal lease used by the handler, so callers validate
// both boundaries separately.
func validateTimerClaimShape(claim TimerClaim, command string) error {
	if strings.TrimSpace(command) == "" || claim.Timer.Command.Name != command || !claim.Timer.Scope.valid() || claim.Timer.State != TimerClaimed ||
		strings.TrimSpace(claim.Timer.Owner) == "" || claim.Timer.FencingToken <= 0 || strings.TrimSpace(claim.Timer.Command.IdempotencyKey) == "" || strings.TrimSpace(claim.OccurrenceKey) == "" ||
		claim.Timer.ClaimKey != claim.OccurrenceKey || claim.Timer.ClaimGeneration != claim.Timer.Generation ||
		!claim.Timer.LeaseExpiresAt.After(time.Time{}) || claim.Timer.DueAt.IsZero() ||
		claim.Timer.CommandDigest != commandDigest(claim.Timer.Command) || occurrenceKey(claim.Timer) != claim.OccurrenceKey {
		return ErrTimerConflict
	}
	return nil
}

func decodeEffectTimeoutClaim(claim TimerClaim) (effectTimeoutPayload, error) {
	if err := validateTimerClaimShape(claim, EffectTimeoutCommandName); err != nil {
		return effectTimeoutPayload{}, err
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
	if err := validateTimerClaimShape(claim, command); err != nil {
		return continuationTimerPayload{}, err
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
