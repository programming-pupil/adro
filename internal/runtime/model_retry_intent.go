package runtime

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// ModelRetryIntentScheduler appends a model.retry_scheduled fact and its
// timer projection request in one journal batch. The TimerStore is populated
// later by ProjectTimerSchedules, so a retry cannot be lost when the timer
// worker is temporarily unavailable.
type ModelRetryIntentScheduler struct {
	Journal      *Journal
	Owner        string
	FencingToken int64
}

func (s ModelRetryIntentScheduler) Schedule(request ModelRequest, decision RetryDecision) (Event, error) {
	if s.Journal == nil {
		return Event{}, errors.New("model retry intent scheduler requires journal")
	}
	if strings.TrimSpace(s.Owner) == "" || s.FencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	if err := request.Validate(); err != nil {
		return Event{}, err
	}
	if !request.Scope.valid() {
		return Event{}, ErrModelRequestInvalid
	}
	if !decision.Retryable || decision.NextAttempt <= request.Attempt || decision.Attempt != request.Attempt || decision.Action == ModelRetryNone || strings.TrimSpace(decision.Reason) == "" || decision.Delay < 0 {
		return Event{}, errors.New("retry decision does not schedule a valid next model attempt")
	}
	if decision.NextAttempt < 2 {
		return Event{}, errors.New("retry next attempt must be at least two")
	}

	j := s.Journal
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	state := j.modelStateLocked(request.Scope, request.RequestID)
	if !state.Requested || state.Completed || state.Cancelled {
		return Event{}, ErrModelTransition
	}
	for _, event := range j.events {
		if event.Scope != request.Scope || event.AggregateType != "model" || event.AggregateID != request.RequestID || event.EventType != EventModelRequested {
			continue
		}
		var committed struct {
			RequestDigest string `json:"request_digest"`
		}
		if err := json.Unmarshal(event.Payload, &committed); err != nil || committed.RequestDigest != request.RequestDigest {
			return Event{}, ErrModelIdempotencyConflict
		}
		break
	}
	if state.OutcomeUnknown && decision.Action != ModelRetryQuery && decision.Action != ModelRetryResume && decision.Action != ModelRetrySuspendUnknown {
		return Event{}, ErrModelTransition
	}

	retryKey := "model:" + request.RequestID + ":retry:" + formatAttempt(decision.NextAttempt)
	// The retry attempt is the idempotency identity. If it already exists,
	// compare every immutable scheduling fact before returning the prior event;
	// this prevents a clock change or a changed recovery decision from silently
	// producing a second timer for the same attempt.
	var existingRetry *Event
	for index := range j.events {
		event := j.events[index]
		if event.Scope == request.Scope && event.IdempotencyKey == retryKey {
			copy := cloneEvent(event)
			existingRetry = &copy
			break
		}
	}
	if existingRetry != nil {
		var prior modelRetrySchedulePayload
		if err := json.Unmarshal(existingRetry.Payload, &prior); err != nil || prior.RequestID != request.RequestID || prior.RequestDigest != request.RequestDigest || prior.Attempt != decision.NextAttempt || prior.Action != decision.Action || prior.Reason != decision.Reason || prior.Delay != decision.Delay || prior.DueAt.IsZero() {
			return Event{}, ErrModelIdempotencyConflict
		}
		timerKey := "timer:schedule:model:" + request.RequestID + ":retry:" + formatAttempt(decision.NextAttempt)
		for _, timerEvent := range j.events {
			if timerEvent.Scope != request.Scope || timerEvent.EventType != EventTimerScheduleRequested || timerEvent.IdempotencyKey != timerKey {
				continue
			}
			if err := validateModelRetryCompanion(timerEvent, request, prior); err != nil {
				return Event{}, err
			}
			return *existingRetry, nil
		}
		return Event{}, ErrCorrupt
	}

	spec, err := ModelRetryTimerSpec(request, decision, j.now())
	if err != nil {
		return Event{}, err
	}
	payload := ModelRetryTimerPayload{
		RequestID: request.RequestID, RequestDigest: request.RequestDigest,
		Attempt: decision.NextAttempt, Action: decision.Action, FailureReason: decision.Reason,
	}
	if err := payload.Validate(); err != nil {
		return Event{}, err
	}
	spec.Command.Name = ModelRetryCommandName
	spec.Command.Payload = payload
	schedule, err := NewTimerScheduleRequest(spec)
	if err != nil {
		return Event{}, err
	}
	inputs := []Input{
		{
			EventType: EventModelRetryScheduled, AggregateType: "model", AggregateID: request.RequestID,
			Scope: request.Scope, IdempotencyKey: retryKey, WriterID: s.Owner,
			FencingToken: s.FencingToken, Status: StatusPending,
			Payload: modelRetrySchedulePayload{RequestID: request.RequestID, RequestDigest: request.RequestDigest, Attempt: decision.NextAttempt, Action: decision.Action, Reason: decision.Reason, Delay: decision.Delay, DueAt: spec.DueAt},
		},
		{
			EventType: EventTimerScheduleRequested, AggregateType: "timer", AggregateID: spec.ScheduleKey,
			Scope: request.Scope, IdempotencyKey: "timer:schedule:" + spec.ScheduleKey, WriterID: s.Owner,
			FencingToken: s.FencingToken, Status: StatusPending,
			Payload: map[string]any{"request": schedule},
		},
	}
	if _, err := j.appendBatchLocked(inputs); err != nil {
		return Event{}, err
	}
	if event, ok := j.eventByIdempotencyKeyLocked(request.Scope, retryKey); ok {
		return event, nil
	}
	return Event{}, ErrCorrupt
}

// validateModelRetryCompanion checks the second half of the atomic retry
// commit. The retry fact and its timer request have different event types but
// share one attempt identity; accepting only the schedule key would allow a
// damaged or partially rewritten timer payload to dispatch a different retry.
func validateModelRetryCompanion(event Event, request ModelRequest, prior modelRetrySchedulePayload) error {
	if event.AggregateType != "timer" || event.AggregateID == "" || event.AggregateID != priorTimerScheduleKey(request.RequestID, prior.Attempt) {
		return ErrModelIdempotencyConflict
	}
	var envelope struct {
		Request TimerScheduleRequest `json:"request"`
	}
	if err := json.Unmarshal(event.Payload, &envelope); err != nil {
		return ErrCorrupt
	}
	spec, err := envelope.Request.Spec()
	if err != nil {
		return ErrCorrupt
	}
	if spec.Scope != request.Scope || spec.ScheduleKey != priorTimerScheduleKey(request.RequestID, prior.Attempt) ||
		!spec.DueAt.Equal(prior.DueAt) || spec.Generation != int64(prior.Attempt) ||
		spec.Command.Name != ModelRetryCommandName || spec.Command.IdempotencyKey != spec.ScheduleKey {
		return ErrModelIdempotencyConflict
	}
	raw, err := json.Marshal(spec.Command.Payload)
	if err != nil {
		return ErrCorrupt
	}
	var payload ModelRetryTimerPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ErrCorrupt
	}
	if err := payload.Validate(); err != nil || payload.RequestID != request.RequestID || payload.RequestDigest != request.RequestDigest || payload.Attempt != prior.Attempt || payload.Action != prior.Action || payload.FailureReason != prior.Reason {
		return ErrModelIdempotencyConflict
	}
	return nil
}

func priorTimerScheduleKey(requestID string, attempt int) string {
	return "model:" + requestID + ":retry:" + formatAttempt(attempt)
}

type modelRetrySchedulePayload struct {
	RequestID     string           `json:"request_id"`
	RequestDigest string           `json:"request_digest"`
	Attempt       int              `json:"attempt"`
	Action        ModelRetryAction `json:"action"`
	Reason        string           `json:"reason"`
	Delay         time.Duration    `json:"delay"`
	DueAt         time.Time        `json:"due_at"`
}

func formatAttempt(attempt int) string {
	return strconv.Itoa(attempt)
}

func (j *Journal) eventByIdempotencyKeyLocked(scope Scope, key string) (Event, bool) {
	for index := len(j.events) - 1; index >= 0; index-- {
		event := j.events[index]
		if event.Scope == scope && event.IdempotencyKey == key {
			return cloneEvent(event), true
		}
	}
	return Event{}, false
}
