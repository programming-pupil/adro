package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/adro-project/adro/core/testkit"
)

func TestModelRetryIntentAndTimerProjectionAreAtomicAndIdempotent(t *testing.T) {
	clock := testkit.NewManualClock(time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC))
	journal, err := NewJournalWithOptions(filepath.Join(t.TempDir(), "runtime.json"), JournalOptions{Clock: clock, IDs: &testkit.SequenceIDs{}})
	if err != nil {
		t.Fatal(err)
	}
	scope := testScope()
	lease, err := journal.AcquireLease(scope, "model-worker", 48*time.Hour, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	request := newModelRequest(t)
	request.Scope = scope
	request, err = NewModelRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := journal.CommitModelRequest(scope, request, "model-worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	decision := RetryDecision{Action: ModelRetrySameRequest, Retryable: true, Attempt: 1, NextAttempt: 2, Delay: 30 * time.Second, Reason: "pre_dispatch_retryable_failure"}
	scheduler := ModelRetryIntentScheduler{Journal: journal, Owner: "model-worker", FencingToken: lease.FencingToken}
	first, err := scheduler.Schedule(request, decision)
	if err != nil || first.EventType != EventModelRetryScheduled {
		t.Fatalf("first retry event=%+v err=%v", first, err)
	}
	second, err := scheduler.Schedule(request, decision)
	if err != nil || second.EventID != first.EventID {
		t.Fatalf("retry replay first=%+v second=%+v err=%v", first, second, err)
	}
	events := journal.List(scope)
	if len(events) != 3 || events[1].EventType != EventModelRetryScheduled || events[2].EventType != EventTimerScheduleRequested {
		t.Fatalf("retry events=%+v", events)
	}
	timers, err := NewTimerStore(filepath.Join(t.TempDir(), "timers.json"), TimerStoreOptions{Clock: clock, IDs: &testkit.SequenceIDs{}})
	if err != nil {
		t.Fatal(err)
	}
	report, err := journal.ProjectTimerSchedules(context.Background(), timers)
	if err != nil || report.Created != 1 || report.Replayed != 0 {
		t.Fatalf("projection=%+v err=%v", report, err)
	}
	report, err = journal.ProjectTimerSchedules(context.Background(), timers)
	if err != nil || report.Created != 0 || report.Replayed != 1 {
		t.Fatalf("replayed projection=%+v err=%v", report, err)
	}
	items, err := timers.List(scope, false)
	if err != nil || len(items) != 1 || items[0].Command.Name != ModelRetryCommandName || !items[0].DueAt.Equal(clock.Now().Add(decision.Delay)) {
		t.Fatalf("projected timers=%+v err=%v", items, err)
	}
}

func TestModelRetryIntentRejectsChangedFactsAfterClockMoves(t *testing.T) {
	clock := testkit.NewManualClock(time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC))
	journal, err := NewJournalWithOptions("", JournalOptions{Clock: clock, IDs: &testkit.SequenceIDs{}})
	if err != nil {
		t.Fatal(err)
	}
	scope := testScope()
	lease, err := journal.AcquireLease(scope, "model-worker", 48*time.Hour, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	request := newModelRequest(t)
	request.Scope = scope
	request, err = NewModelRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := journal.CommitModelRequest(scope, request, "model-worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	base := RetryDecision{Action: ModelRetrySameRequest, Retryable: true, Attempt: 1, NextAttempt: 2, Delay: time.Minute, Reason: "transport_retry"}
	scheduler := ModelRetryIntentScheduler{Journal: journal, Owner: "model-worker", FencingToken: lease.FencingToken}
	first, err := scheduler.Schedule(request, base)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(24 * time.Hour)
	replayed, err := scheduler.Schedule(request, base)
	if err != nil || replayed.EventID != first.EventID {
		t.Fatalf("clock-shifted replay=%+v err=%v", replayed, err)
	}
	for name, changed := range map[string]RetryDecision{
		"delay":  func() RetryDecision { value := base; value.Delay += time.Second; return value }(),
		"reason": func() RetryDecision { value := base; value.Reason = "different"; return value }(),
		"action": func() RetryDecision { value := base; value.Action = ModelRetryResume; return value }(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := scheduler.Schedule(request, changed); !errors.Is(err, ErrModelIdempotencyConflict) {
				t.Fatalf("changed retry facts err=%v", err)
			}
		})
	}
}

func TestModelRetryIntentRejectsMissingOrMismatchedCompanionTimer(t *testing.T) {
	newFixture := func(t *testing.T) (*Journal, ModelRequest, ModelRetryIntentScheduler, RetryDecision) {
		t.Helper()
		clock := testkit.NewManualClock(time.Date(2026, 9, 20, 13, 0, 0, 0, time.UTC))
		journal, err := NewJournalWithOptions("", JournalOptions{Clock: clock, IDs: &testkit.SequenceIDs{}})
		if err != nil {
			t.Fatal(err)
		}
		scope := testScope()
		lease, err := journal.AcquireLease(scope, "model-worker", time.Hour, clock.Now())
		if err != nil {
			t.Fatal(err)
		}
		request := newModelRequest(t)
		request.Scope = scope
		request, err = NewModelRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := journal.CommitModelRequest(scope, request, "model-worker", lease.FencingToken); err != nil {
			t.Fatal(err)
		}
		decision := RetryDecision{Action: ModelRetrySameRequest, Retryable: true, Attempt: 1, NextAttempt: 2, Delay: time.Minute, Reason: "transport_retry"}
		return journal, request, ModelRetryIntentScheduler{Journal: journal, Owner: "model-worker", FencingToken: lease.FencingToken}, decision
	}

	t.Run("missing", func(t *testing.T) {
		journal, request, scheduler, decision := newFixture(t)
		if _, err := scheduler.Schedule(request, decision); err != nil {
			t.Fatal(err)
		}
		journal.mu.Lock()
		journal.events = journal.events[:len(journal.events)-1]
		journal.mu.Unlock()
		if _, err := scheduler.Schedule(request, decision); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("missing companion err=%v", err)
		}
	})

	t.Run("payload-drift", func(t *testing.T) {
		journal, request, scheduler, decision := newFixture(t)
		if _, err := scheduler.Schedule(request, decision); err != nil {
			t.Fatal(err)
		}
		journal.mu.Lock()
		for index := range journal.events {
			if journal.events[index].EventType != EventTimerScheduleRequested {
				continue
			}
			var envelope struct {
				Request TimerScheduleRequest `json:"request"`
			}
			if err := json.Unmarshal(journal.events[index].Payload, &envelope); err != nil {
				journal.mu.Unlock()
				t.Fatal(err)
			}
			payload, ok := envelope.Request.Command.Payload.(map[string]any)
			if !ok {
				journal.mu.Unlock()
				t.Fatalf("unexpected timer payload type %T", envelope.Request.Command.Payload)
			}
			payload["failure_reason"] = "tampered"
			envelope.Request.Command.Payload = payload
			journal.events[index].Payload, _ = json.Marshal(envelope)
			break
		}
		journal.mu.Unlock()
		if _, err := scheduler.Schedule(request, decision); !errors.Is(err, ErrModelIdempotencyConflict) {
			t.Fatalf("tampered companion err=%v", err)
		}
	})
}
