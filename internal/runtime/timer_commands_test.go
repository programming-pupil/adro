package runtime

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/adro-project/adro/core/testkit"
)

func validModelRetryClaim(t *testing.T) TimerClaim {
	t.Helper()
	clock := testkit.NewManualClock(time.Date(2026, 9, 20, 14, 0, 0, 0, time.UTC))
	store, err := NewTimerStore(filepath.Join(t.TempDir(), "timers.json"), TimerStoreOptions{Clock: clock, IDs: &testkit.SequenceIDs{}})
	if err != nil {
		t.Fatal(err)
	}
	request := newModelRequest(t)
	decision := RetryDecision{Action: ModelRetrySameRequest, Retryable: true, Attempt: 1, NextAttempt: 2, Delay: 0, Reason: "retry"}
	spec, err := ModelRetryTimerSpec(request, decision, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	spec.Command.Name = ModelRetryCommandName
	spec.Command.Payload = ModelRetryTimerPayload{RequestID: request.RequestID, RequestDigest: request.RequestDigest, Attempt: 2, Action: decision.Action, FailureReason: decision.Reason}
	timer, _, err := store.Schedule(spec)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := store.ClaimDue(clock.Now(), "timer-worker", time.Minute, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	if _, err := DecodeModelRetryClaim(claims[0]); err != nil {
		t.Fatalf("fixture claim invalid: %v", err)
	}
	_ = timer
	return claims[0]
}

func TestValidateTimerClaimShapeRejectsMalformedLeaseAndOccurrence(t *testing.T) {
	mutations := map[string]func(*TimerClaim){
		"owner":               func(claim *TimerClaim) { claim.Timer.Owner = "" },
		"fencing":             func(claim *TimerClaim) { claim.Timer.FencingToken = 0 },
		"claim-generation":    func(claim *TimerClaim) { claim.Timer.ClaimGeneration++ },
		"command-digest":      func(claim *TimerClaim) { claim.Timer.CommandDigest = "bad" },
		"occurrence":          func(claim *TimerClaim) { claim.OccurrenceKey = "different" },
		"claim-key":           func(claim *TimerClaim) { claim.Timer.ClaimKey = "different" },
		"lease-expiry":        func(claim *TimerClaim) { claim.Timer.LeaseExpiresAt = time.Time{} },
		"due-time":            func(claim *TimerClaim) { claim.Timer.DueAt = time.Time{} },
		"command-idempotency": func(claim *TimerClaim) { claim.Timer.Command.IdempotencyKey = "" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			claim := validModelRetryClaim(t)
			mutate(&claim)
			if !errors.Is(validateTimerClaimShape(claim, ModelRetryCommandName), ErrTimerConflict) {
				t.Fatalf("malformed claim accepted: %+v", claim)
			}
			if _, err := DecodeModelRetryClaim(claim); !errors.Is(err, ErrModelRetryInvalid) {
				t.Fatalf("malformed model claim error=%v", err)
			}
		})
	}
}
