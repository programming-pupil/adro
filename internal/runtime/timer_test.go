package runtime

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/adro-project/adro/core/testkit"
	"github.com/adro-project/adro/internal/durable"
)

func timerScope() Scope {
	return Scope{TenantID: "tenant-timer", WorkspaceID: "workspace-timer", SessionID: "session-timer", RunID: "run-timer"}
}

func timerOptions(clock *testkit.ManualClock) TimerStoreOptions {
	return TimerStoreOptions{Clock: clock, IDs: &testkit.SequenceIDs{}}
}

func timerSpec(scope Scope, key, id string, due time.Time) TimerSpec {
	return TimerSpec{
		ID: id, ScheduleKey: key, Scope: scope, DueAt: due,
		Command: TimerCommand{Name: "resume", IdempotencyKey: key, Payload: map[string]any{"key": key}},
	}
}

func TestTimerStorePersistsAndRetriesSchedulesIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timers.json")
	clock := testkit.NewManualClock(time.Date(2026, 3, 29, 1, 30, 0, 0, time.FixedZone("CST", 8*60*60)))
	store, err := NewTimerStore(path, timerOptions(clock))
	if err != nil {
		t.Fatal(err)
	}
	scope := timerScope()
	spec := timerSpec(scope, "approval:42", "approval-timer", clock.Now().Add(time.Hour))
	first, created, err := store.Schedule(spec)
	if err != nil || !created || first.ID != spec.ID {
		t.Fatalf("first schedule=%+v created=%v err=%v", first, created, err)
	}
	retry, created, err := store.Schedule(spec)
	if err != nil || created || retry.ID != first.ID || retry.CommandDigest != first.CommandDigest {
		t.Fatalf("idempotent schedule retry=%+v created=%v err=%v", retry, created, err)
	}
	changed := spec
	changed.Command.Payload = map[string]any{"key": "changed"}
	if _, _, err := store.Schedule(changed); !errors.Is(err, ErrTimerIdempotencyConflict) {
		t.Fatalf("changed schedule was accepted: %v", err)
	}

	restarted, err := NewTimerStore(path, timerOptions(clock))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := restarted.Get(first.ID)
	if err != nil || loaded.ID != first.ID || loaded.Scope != scope || loaded.DueAt.UTC() != first.DueAt.UTC() {
		t.Fatalf("restarted timer=%+v err=%v", loaded, err)
	}
}

func TestTimerStoreClaimsInOrderAndFencesWorkers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timers.json")
	clock := testkit.NewManualClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, err := NewTimerStore(path, timerOptions(clock))
	if err != nil {
		t.Fatal(err)
	}
	scope := timerScope()
	first, _, err := store.Schedule(timerSpec(scope, "first", "timer-first", clock.Now()))
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := store.Schedule(timerSpec(scope, "second", "timer-second", clock.Now()))
	if err != nil {
		t.Fatal(err)
	}
	claims, err := store.ClaimDue(clock.Now(), "worker-1", time.Minute, 1)
	if err != nil || len(claims) != 1 || claims[0].Timer.ID != first.ID {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	if _, err := store.Acknowledge(first.ID, "worker-1", claims[0].Timer.FencingToken-1, claims[0].OccurrenceKey, clock.Now()); !errors.Is(err, ErrTimerLeaseLost) {
		t.Fatalf("stale fencing token accepted: %v", err)
	}
	secondClaims, err := store.ClaimDue(clock.Now(), "worker-2", time.Minute, 1)
	if err != nil || len(secondClaims) != 1 || secondClaims[0].Timer.ID != second.ID {
		t.Fatalf("second claims=%+v err=%v", secondClaims, err)
	}
	result, err := store.Acknowledge(first.ID, "worker-1", claims[0].Timer.FencingToken, claims[0].OccurrenceKey, clock.Now())
	if err != nil || result.Status != "completed" {
		t.Fatalf("ack result=%+v err=%v", result, err)
	}
	retry, err := store.Acknowledge(first.ID, "worker-1", 999, claims[0].OccurrenceKey, clock.Now())
	if err != nil || retry != result {
		t.Fatalf("duplicate ack did not converge result=%+v retry=%+v err=%v", result, retry, err)
	}
	loaded, err := store.Get(first.ID)
	if err != nil || loaded.State != TimerFired {
		t.Fatalf("one-shot state=%+v err=%v", loaded, err)
	}
}

func TestTimerStoreExpiredClaimCanBeTakenOverWithNewFence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timers.json")
	clock := testkit.NewManualClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, err := NewTimerStore(path, timerOptions(clock))
	if err != nil {
		t.Fatal(err)
	}
	spec := timerSpec(timerScope(), "takeover", "timer-takeover", clock.Now())
	if _, _, err := store.Schedule(spec); err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimDue(clock.Now(), "worker-1", time.Minute, 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	clock.Advance(2 * time.Minute)
	second, err := store.ClaimDue(clock.Now(), "worker-2", time.Minute, 1)
	if err != nil || len(second) != 1 || second[0].Timer.FencingToken <= first[0].Timer.FencingToken {
		t.Fatalf("takeover claim=%+v first=%+v err=%v", second, first, err)
	}
	if _, err := store.Acknowledge(spec.ID, "worker-1", first[0].Timer.FencingToken, first[0].OccurrenceKey, clock.Now()); !errors.Is(err, ErrTimerLeaseLost) {
		t.Fatalf("expired worker acknowledged timer: %v", err)
	}
}

func TestTimerStoreRecurringCatchUpCoalescesAndExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timers.json")
	base := time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC)
	clock := testkit.NewManualClock(base)
	store, err := NewTimerStore(path, timerOptions(clock))
	if err != nil {
		t.Fatal(err)
	}
	scope := timerScope()
	coalesce := timerSpec(scope, "coalesce", "timer-coalesce", base)
	coalesce.Interval, coalesce.Policy, coalesce.MaxCatchUp = time.Hour, TimerCoalesce, 2
	if _, _, err := store.Schedule(coalesce); err != nil {
		t.Fatal(err)
	}
	expire := timerSpec(scope, "expire", "timer-expire", base)
	expire.Interval, expire.Policy, expire.MaxCatchUp = time.Hour, TimerExpire, 2
	if _, _, err := store.Schedule(expire); err != nil {
		t.Fatal(err)
	}
	now := base.Add(24 * time.Hour)
	claims, err := store.ClaimDue(now, "worker", time.Minute, 10)
	if err != nil || len(claims) != 1 || !claims[0].Coalesced || claims[0].Timer.ID != coalesce.ID {
		t.Fatalf("catch-up claims=%+v err=%v", claims, err)
	}
	if _, err := store.Acknowledge(coalesce.ID, "worker", claims[0].Timer.FencingToken, claims[0].OccurrenceKey, now); err != nil {
		t.Fatal(err)
	}
	coalesced, err := store.Get(coalesce.ID)
	if err != nil || coalesced.State != TimerPending || !coalesced.DueAt.Equal(now.Add(time.Hour)) || coalesced.Generation != 1 {
		t.Fatalf("coalesced next occurrence=%+v err=%v", coalesced, err)
	}
	expired, err := store.Get(expire.ID)
	if err != nil || expired.State != TimerExpired || expired.TerminalReason != "timer_catch_up_expired" {
		t.Fatalf("expired timer=%+v err=%v", expired, err)
	}
}

func TestTimerStoreReleaseAndDurabilityFailureRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timers.json")
	clock := testkit.NewManualClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, err := NewTimerStore(path, timerOptions(clock))
	if err != nil {
		t.Fatal(err)
	}
	spec := timerSpec(timerScope(), "fault", "timer-fault", clock.Now())
	restore := durable.SetFaultInjector(func(point string) error {
		if point == "timer.persist.before_rename" {
			return errors.New("simulated crash before timer rename")
		}
		return nil
	})
	_, _, err = store.Schedule(spec)
	restore()
	if err == nil {
		t.Fatal("faulted timer schedule succeeded")
	}
	if _, err := store.Get(spec.ID); !errors.Is(err, ErrTimerNotFound) {
		t.Fatalf("faulted schedule remained in memory: %v", err)
	}
	if _, _, err := store.Schedule(spec); err != nil {
		t.Fatal(err)
	}
	claims, err := store.ClaimDue(clock.Now(), "worker", time.Minute, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim=%+v err=%v", claims, err)
	}
	if err := store.ReleaseClaim(spec.ID, "worker", claims[0].Timer.FencingToken, claims[0].OccurrenceKey, clock.Now()); err != nil {
		t.Fatal(err)
	}
	released, err := store.Get(spec.ID)
	if err != nil || released.State != TimerPending || released.Owner != "" {
		t.Fatalf("released timer=%+v err=%v", released, err)
	}
}

func TestTimerStoreExplainsRecoveryState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timers.json")
	clock := testkit.NewManualClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, err := NewTimerStore(path, timerOptions(clock))
	if err != nil {
		t.Fatal(err)
	}
	spec := timerSpec(timerScope(), "explain", "timer-explain", clock.Now().Add(time.Hour))
	if _, _, err := store.Schedule(spec); err != nil {
		t.Fatal(err)
	}
	explanation, err := store.Explain(spec.ID)
	if err != nil || explanation.Reason != "waiting_for_due_time" || explanation.NextAction != "claim_when_due" {
		t.Fatalf("explanation=%+v err=%v", explanation, err)
	}
}

func TestTimerStoreCancellationIsConvergentForSameReason(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timers.json")
	clock := testkit.NewManualClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, err := NewTimerStore(path, timerOptions(clock))
	if err != nil {
		t.Fatal(err)
	}
	spec := timerSpec(timerScope(), "cancel-convergent", "timer-cancel-convergent", clock.Now().Add(time.Hour))
	if _, _, err := store.Schedule(spec); err != nil {
		t.Fatal(err)
	}
	first, err := store.Cancel(spec.ID, "operator_review")
	if err != nil || first.State != TimerCancelled {
		t.Fatalf("first cancellation=%+v err=%v", first, err)
	}
	second, err := store.Cancel(spec.ID, "operator_review")
	if err != nil || second.State != TimerCancelled || !second.CancelledAt.Equal(first.CancelledAt) {
		t.Fatalf("repeat cancellation=%+v err=%v", second, err)
	}
	if _, err := store.Cancel(spec.ID, "different_reason"); !errors.Is(err, ErrTimerConflict) {
		t.Fatalf("different cancellation reason err=%v", err)
	}
}

func TestTimerStoreUsesStableOrderingAndUTCForClockJumpsAndDST(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timers.json")
	zone := time.FixedZone("CST", 8*60*60)
	clock := testkit.NewManualClock(time.Date(2026, 3, 29, 1, 59, 0, 0, zone))
	store, err := NewTimerStore(path, timerOptions(clock))
	if err != nil {
		t.Fatal(err)
	}
	scope := timerScope()
	due := time.Date(2026, 3, 29, 2, 30, 0, 0, zone)
	late := timerSpec(scope, "z-order", "timer-z", due)
	early := timerSpec(scope, "a-order", "timer-a", due)
	if _, _, err := store.Schedule(late); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Schedule(early); err != nil {
		t.Fatal(err)
	}
	clock.Advance(90 * time.Minute)
	claims, err := store.ClaimDue(clock.Now(), "worker", time.Minute, 2)
	if err != nil || len(claims) != 2 || claims[0].Timer.ID != early.ID || claims[1].Timer.ID != late.ID {
		t.Fatalf("stable ordering claims=%+v err=%v", claims, err)
	}
	loaded, err := store.Get(early.ID)
	if err != nil || loaded.DueAt.Location() != time.UTC {
		t.Fatalf("DST due time was not normalized to UTC: timer=%+v err=%v", loaded, err)
	}
	// Go normalizes a leap-second-shaped input to the next minute. The
	// persisted UTC instant remains a valid, deterministic scheduling point.
	leap := timerSpec(scope, "leap", "timer-leap", time.Date(2026, 1, 1, 0, 0, 60, 0, time.UTC))
	if _, _, err := store.Schedule(leap); err != nil {
		t.Fatal(err)
	}
	leapLoaded, err := store.Get(leap.ID)
	if err != nil || !leapLoaded.DueAt.Equal(time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC)) {
		t.Fatalf("leap-second normalized timer=%+v err=%v", leapLoaded, err)
	}
}
