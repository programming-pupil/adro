package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adro-project/adro/core"
)

const ModelRetryCommandName = "model.retry"

var (
	ErrModelRetryInvalid = errors.New("runtime model retry timer is invalid")
	ErrModelRetryStale   = errors.New("runtime model retry timer is stale")
)

// ModelRetryTimerPayload is the immutable command body stored in TimerStore.
// The original request digest is retained so a worker can reject a timer after
// a newer request or a different route has already been committed.
type ModelRetryTimerPayload struct {
	RequestID     string           `json:"request_id"`
	RequestDigest string           `json:"request_digest"`
	Attempt       int              `json:"attempt"`
	Action        ModelRetryAction `json:"action"`
	FailureReason string           `json:"failure_reason"`
}

func (p ModelRetryTimerPayload) Validate() error {
	if strings.TrimSpace(p.RequestID) == "" || strings.TrimSpace(p.RequestDigest) == "" || p.Attempt < 2 || p.Action == ModelRetryNone || strings.TrimSpace(p.FailureReason) == "" {
		return ErrModelRetryInvalid
	}
	if p.Action != ModelRetrySameRequest && p.Action != ModelRetryRecompile && p.Action != ModelRetryResume && p.Action != ModelRetryQuery && p.Action != ModelRetrySuspendUnknown {
		return ErrModelRetryInvalid
	}
	return nil
}

// DecodeModelRetryClaim verifies the timer command and its lease-bound claim.
// It does not consult mutable model state; callers must compare the payload to
// the request they are about to dispatch.
func DecodeModelRetryClaim(claim TimerClaim) (ModelRetryTimerPayload, error) {
	if claim.Timer.Command.Name != ModelRetryCommandName || claim.Timer.State != TimerClaimed || claim.Timer.FencingToken <= 0 || strings.TrimSpace(claim.OccurrenceKey) == "" || claim.Timer.ClaimKey != claim.OccurrenceKey || claim.Timer.LeaseExpiresAt.IsZero() {
		return ModelRetryTimerPayload{}, ErrModelRetryInvalid
	}
	raw, err := json.Marshal(claim.Timer.Command.Payload)
	if err != nil {
		return ModelRetryTimerPayload{}, fmt.Errorf("%w: payload encoding: %v", ErrModelRetryInvalid, err)
	}
	var payload ModelRetryTimerPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ModelRetryTimerPayload{}, fmt.Errorf("%w: payload decoding: %v", ErrModelRetryInvalid, err)
	}
	if err := payload.Validate(); err != nil {
		return ModelRetryTimerPayload{}, err
	}
	if claim.Timer.Generation != int64(payload.Attempt) {
		return ModelRetryTimerPayload{}, fmt.Errorf("%w: generation does not match attempt", ErrModelRetryInvalid)
	}
	return payload, nil
}

// ModelRetryScheduler persists retry decisions as durable timers. The caller
// owns the model request journal; this type only creates an idempotent timer
// and never dispatches a provider request itself.
type ModelRetryScheduler struct {
	Store *TimerStore
	Clock core.Clock
}

func NewModelRetryScheduler(store *TimerStore, clock core.Clock) (*ModelRetryScheduler, error) {
	if store == nil {
		return nil, errors.New("model retry scheduler requires a timer store")
	}
	if clock == nil {
		clock = core.SystemClock{}
	}
	return &ModelRetryScheduler{Store: store, Clock: clock}, nil
}

func (s *ModelRetryScheduler) Schedule(request ModelRequest, decision RetryDecision) (Timer, bool, error) {
	if s == nil || s.Store == nil || s.Clock == nil {
		return Timer{}, false, errors.New("model retry scheduler is not configured")
	}
	spec, err := ModelRetryTimerSpec(request, decision, s.Clock.Now().UTC())
	if err != nil {
		return Timer{}, false, err
	}
	// Keep the command shape explicit even if ModelRetryTimerSpec evolves. A
	// malformed payload must fail before it reaches a worker queue.
	payload := ModelRetryTimerPayload{
		RequestID: request.RequestID, RequestDigest: request.RequestDigest,
		Attempt: decision.NextAttempt, Action: decision.Action, FailureReason: decision.Reason,
	}
	if err := payload.Validate(); err != nil {
		return Timer{}, false, err
	}
	spec.Command.Name = ModelRetryCommandName
	spec.Command.Payload = payload
	return s.Store.Schedule(spec)
}

// ModelRetryLookup reads the immutable request committed before the retry
// timer. It must return the same request digest that was used to schedule the
// timer or the handler rejects the command as stale.
type ModelRetryLookup func(context.Context, Scope, string) (ModelRequest, error)

// ModelRetryDispatch is the provider-neutral dispatch boundary. It receives
// the prior request and the frozen retry action; constructing the next request
// and committing its request event remains the caller's durable responsibility.
type ModelRetryDispatch func(context.Context, ModelRequest, ModelRetryTimerPayload) error

// NewModelRetryCommandHandler creates a TimerCommandHandler suitable for a
// TimerDispatcher. It rejects stale request digests and skipped attempts before
// invoking a provider, preventing a duplicate or out-of-order retry.
func NewModelRetryCommandHandler(lookup ModelRetryLookup, dispatch ModelRetryDispatch) TimerCommandHandler {
	return func(ctx context.Context, claim TimerClaim) error {
		if lookup == nil || dispatch == nil {
			return ErrModelRetryInvalid
		}
		payload, err := DecodeModelRetryClaim(claim)
		if err != nil {
			return err
		}
		request, err := lookup(ctx, claim.Timer.Scope, payload.RequestID)
		if err != nil {
			return err
		}
		if err := request.Validate(); err != nil {
			return fmt.Errorf("%w: request: %v", ErrModelRetryStale, err)
		}
		if request.Scope != claim.Timer.Scope || request.RequestID != payload.RequestID || request.RequestDigest != payload.RequestDigest {
			return ErrModelRetryStale
		}
		if request.Attempt+1 != payload.Attempt {
			return fmt.Errorf("%w: expected next attempt %d got %d", ErrModelRetryStale, request.Attempt+1, payload.Attempt)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return dispatch(ctx, request, payload)
	}
}

// ModelRetryTimerDue reports whether a claimed retry is eligible at now. It is
// intentionally separate from the handler so a worker can use the same check
// in an explain/reconcile endpoint without dispatching a provider.
func ModelRetryTimerDue(claim TimerClaim, now time.Time) error {
	if _, err := DecodeModelRetryClaim(claim); err != nil {
		return err
	}
	if now.IsZero() || now.Before(claim.Timer.DueAt) {
		return ErrTimerConflict
	}
	if !claim.Timer.LeaseExpiresAt.After(now) {
		return ErrTimerLeaseLost
	}
	return nil
}
