package runtime

import (
	"strings"
	"time"
)

// EffectTimeoutResult records the durable effect transition made by a timeout
// command. A terminal receipt or reconciliation that won the race is a
// successful no-op and can be acknowledged by the timer worker.
type EffectTimeoutResult struct {
	Event    Event  `json:"event"`
	TimedOut bool   `json:"timed_out"`
	Reason   string `json:"reason"`
}

// MarkEffectDispatchedWithTimeout commits the timeout intent and the dispatch
// fact in one journal batch. The caller may invoke the external adapter only
// after this method returns successfully. TimerStore materialization is a
// projection of the committed timer.schedule_requested event, so a temporary
// timer backend outage cannot erase the timeout contract.
func (j *Journal) MarkEffectDispatchedWithTimeout(scope Scope, effectID string, timeout time.Duration, owner string, fencingToken int64) (Event, error) {
	if timeout <= 0 {
		return j.MarkEffectDispatched(scope, effectID, owner, fencingToken)
	}
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	if !scope.valid() || strings.TrimSpace(effectID) == "" {
		return Event{}, ErrConflict
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	state := j.effectStateLocked(scope, strings.TrimSpace(effectID))
	if !state.IntentCommitted || state.DispatchPrepared == false || state.Receipted || state.OutcomeUnknown || state.Reconciled {
		return Event{}, ErrConflict
	}
	if state.Dispatched {
		if event, ok := j.effectEventLocked(scope, effectID, EventEffectDispatched); ok {
			return event, nil
		}
		return Event{}, ErrCorrupt
	}
	// Read-only calls are resolved synchronously by ToolLoop. They have no
	// unknown external write outcome that needs a durable timeout command.
	if state.Class == EffectReadOnly {
		return j.appendBatchLocked([]Input{{
			EventType: EventEffectDispatched, AggregateType: "effect", AggregateID: effectID,
			Scope: scope, IdempotencyKey: "effect:" + effectID + ":dispatch",
			WriterID: owner, FencingToken: fencingToken, Status: StatusPending,
			Payload: map[string]any{"effect_id": effectID},
		}})
	}
	if strings.TrimSpace(state.InputDigest) == "" {
		return Event{}, ErrCorrupt
	}
	spec, err := EffectTimeoutTimerSpec(scope, effectID, state.InputDigest, 1, j.now().Add(timeout), state.ReconcilePolicy)
	if err != nil {
		return Event{}, err
	}
	schedule, err := NewTimerScheduleRequest(spec)
	if err != nil {
		return Event{}, err
	}
	// Keep the schedule request before the dispatch fact in sequence order. The
	// two records still share one fsync/transaction boundary.
	return j.appendBatchLocked([]Input{
		{
			EventType: EventTimerScheduleRequested, AggregateType: "timer", AggregateID: spec.ScheduleKey,
			Scope: scope, IdempotencyKey: "timer:schedule:" + spec.ScheduleKey,
			WriterID: owner, FencingToken: fencingToken, Status: StatusPending,
			Payload: map[string]any{"request": schedule},
		},
		{
			EventType: EventEffectDispatched, AggregateType: "effect", AggregateID: effectID,
			Scope: scope, IdempotencyKey: "effect:" + effectID + ":dispatch",
			WriterID: owner, FencingToken: fencingToken, Status: StatusPending,
			Payload: map[string]any{"effect_id": effectID, "timeout": timeout.String()},
		},
	})
}

// ApplyEffectTimeoutClaim turns a claimed durable timeout into an unknown
// outcome. It never executes or retries the external effect. A receipt or
// reconciliation that committed first is returned as a successful no-op.
func (j *Journal) ApplyEffectTimeoutClaim(claim TimerClaim, now time.Time, owner string, fencingToken int64) (EffectTimeoutResult, error) {
	if j == nil || strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return EffectTimeoutResult{}, ErrLeaseLost
	}
	if now.IsZero() {
		return EffectTimeoutResult{}, ErrTimerConflict
	}
	now = now.UTC()
	payload, err := decodeEffectTimeoutClaim(claim)
	if err != nil {
		return EffectTimeoutResult{}, err
	}
	if !claim.Timer.Scope.valid() || now.Before(claim.Timer.DueAt) || !claim.Timer.LeaseExpiresAt.After(now) {
		return EffectTimeoutResult{}, ErrTimerConflict
	}
	state, err := j.EffectState(claim.Timer.Scope, payload.EffectID)
	if err != nil {
		return EffectTimeoutResult{}, err
	}
	if state.InputDigest != payload.InputDigest || state.Scope != claim.Timer.Scope {
		return EffectTimeoutResult{}, ErrTimerIdempotencyConflict
	}
	if state.Receipted || state.Reconciled {
		return EffectTimeoutResult{Reason: "terminal_receipt_won_before_timeout"}, nil
	}
	if state.OutcomeUnknown {
		j.mu.RLock()
		event, ok := j.effectEventLocked(claim.Timer.Scope, payload.EffectID, EventEffectUnknown)
		j.mu.RUnlock()
		if !ok {
			return EffectTimeoutResult{}, ErrCorrupt
		}
		return EffectTimeoutResult{Event: event, TimedOut: true, Reason: "timeout_already_recorded"}, nil
	}
	if !state.Dispatched || state.Class == EffectReadOnly {
		return EffectTimeoutResult{}, ErrTimerConflict
	}
	event, err := j.MarkEffectOutcomeUnknown(claim.Timer.Scope, payload.EffectID, "effect_timeout", owner, fencingToken)
	if err != nil {
		return EffectTimeoutResult{}, err
	}
	return EffectTimeoutResult{Event: event, TimedOut: true, Reason: "effect_timeout"}, nil
}

func (j *Journal) effectEventLocked(scope Scope, effectID, eventType string) (Event, bool) {
	for index := len(j.events) - 1; index >= 0; index-- {
		event := j.events[index]
		if event.Scope == scope && event.AggregateType == "effect" && event.AggregateID == effectID && event.EventType == eventType {
			return cloneEvent(event), true
		}
	}
	return Event{}, false
}
