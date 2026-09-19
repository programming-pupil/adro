package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/adro-project/adro/core/testkit"
)

func TestTimerDispatcherClaimsAcknowledgesAndRetriesWithFencing(t *testing.T) {
	clock := testkit.NewManualClock(time.Date(2026, 9, 19, 4, 0, 0, 0, time.UTC))
	store, err := NewTimerStore(filepath.Join(t.TempDir(), "timers.json"), timerOptions(clock))
	if err != nil {
		t.Fatal(err)
	}
	scope := timerScope()
	if _, _, err := store.Schedule(timerSpec(scope, "ok", "timer-ok", clock.Now())); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	dispatcher, err := NewTimerDispatcher(TimerDispatcherConfig{
		Store: store, Owner: "worker", LeaseTTL: time.Minute, PollInterval: time.Second, BatchSize: 4,
		Handlers: map[string]TimerCommandHandler{"resume": func(context.Context, TimerClaim) error {
			attempts++
			if attempts == 1 {
				return ErrTimerRetryable
			}
			return nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := dispatcher.DispatchDue(context.Background(), clock.Now())
	if err != nil || report.Retried != 1 || attempts != 1 {
		t.Fatalf("first report=%+v attempts=%d err=%v", report, attempts, err)
	}
	report, err = dispatcher.DispatchDue(context.Background(), clock.Now())
	if err != nil || report.Acknowledged != 1 || attempts != 2 {
		t.Fatalf("second report=%+v attempts=%d err=%v", report, attempts, err)
	}
	loaded, err := store.Get("timer-ok")
	if err != nil || loaded.State != TimerFired || loaded.TerminalReason != "completed" {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
}

func TestTimerDispatcherUnknownHandlerFailsClosedAndContextCancellationReleases(t *testing.T) {
	clock := testkit.NewManualClock(time.Date(2026, 9, 19, 4, 0, 0, 0, time.UTC))
	store, err := NewTimerStore(filepath.Join(t.TempDir(), "timers.json"), timerOptions(clock))
	if err != nil {
		t.Fatal(err)
	}
	scope := timerScope()
	unknown := timerSpec(scope, "unknown", "timer-unknown", clock.Now())
	unknown.Command.Name = "missing"
	if _, _, err := store.Schedule(unknown); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewTimerDispatcher(TimerDispatcherConfig{Store: store, Owner: "worker", LeaseTTL: time.Minute, PollInterval: time.Second, BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	report, err := dispatcher.DispatchDue(context.Background(), clock.Now())
	if err != nil || report.Unavailable != 1 || report.Failed != 1 {
		t.Fatalf("unknown report=%+v err=%v", report, err)
	}
	loaded, err := store.Get(unknown.ID)
	if err != nil || loaded.State != TimerFired || loaded.TerminalReason != "failed:handler_unavailable" {
		t.Fatalf("unknown loaded=%+v err=%v", loaded, err)
	}

	cancelled := timerSpec(scope, "cancelled", "timer-cancelled", clock.Now())
	if _, _, err := store.Schedule(cancelled); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := dispatcher.DispatchDue(ctx, clock.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled dispatch err=%v", err)
	}
	pending, err := store.Get(cancelled.ID)
	if err != nil || pending.State != TimerPending {
		t.Fatalf("cancelled timer=%+v err=%v", pending, err)
	}
}

func TestTimerStoreFailConvergesDuplicateAcknowledgement(t *testing.T) {
	clock := testkit.NewManualClock(time.Date(2026, 9, 19, 4, 0, 0, 0, time.UTC))
	store, err := NewTimerStore(filepath.Join(t.TempDir(), "timers.json"), timerOptions(clock))
	if err != nil {
		t.Fatal(err)
	}
	spec := timerSpec(timerScope(), "fail", "timer-fail", clock.Now())
	if _, _, err := store.Schedule(spec); err != nil {
		t.Fatal(err)
	}
	claims, err := store.ClaimDue(clock.Now(), "worker", time.Minute, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	first, err := store.Fail(spec.ID, "worker", claims[0].Timer.FencingToken, claims[0].OccurrenceKey, clock.Now(), "handler_failed")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Fail(spec.ID, "worker", claims[0].Timer.FencingToken-1, claims[0].OccurrenceKey, clock.Now(), "different")
	if err != nil || second != first {
		t.Fatalf("duplicate fail=%+v first=%+v err=%v", second, first, err)
	}
}
