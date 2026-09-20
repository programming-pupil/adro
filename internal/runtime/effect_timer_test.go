package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/adro-project/adro/core/testkit"
)

func TestEffectDispatchTimeoutIntentCommitsBeforeExternalCallback(t *testing.T) {
	clock := testkit.NewManualClock(time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC))
	journal, err := NewJournalWithOptions("", JournalOptions{Clock: clock, IDs: &testkit.SequenceIDs{}})
	if err != nil {
		t.Fatal(err)
	}
	scope := testScope()
	lease, err := journal.AcquireLease(scope, "worker", time.Hour, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	contract := ToolContract{
		Name: "write", Capabilities: []string{"record.write"},
		SideEffectClass: EffectReconcilableWrite, ReconcilePolicy: ReconcileQuery,
		Timeout: 5 * time.Minute,
	}
	loop := ToolLoop{Journal: journal, Scope: scope, Owner: "worker", FencingToken: lease.FencingToken, AllowedCapabilities: []string{"record.write"}}
	called := false
	_, runErr := loop.Run(context.Background(), "call-timeout", contract, map[string]any{"value": 1}, func(context.Context) (any, error) {
		called = true
		events := journal.List(scope)
		if len(events) != 6 || events[4].EventType != EventTimerScheduleRequested || events[5].EventType != EventEffectDispatched {
			t.Fatalf("dispatch callback observed events=%+v", events)
		}
		return nil, errors.New("connection lost")
	})
	if !called || !errors.Is(runErr, ErrEffectOutcomeUnknown) {
		t.Fatalf("called=%v err=%v", called, runErr)
	}
	state, err := journal.EffectState(scope, "tool-effect:call-timeout")
	if err != nil || !state.OutcomeUnknown || state.InputDigest == "" {
		t.Fatalf("effect state=%+v err=%v", state, err)
	}
}

func TestEffectTimeoutClaimConvergesWithReceiptAndUnknownOutcome(t *testing.T) {
	clock := testkit.NewManualClock(time.Date(2026, 9, 19, 11, 0, 0, 0, time.UTC))
	path := filepath.Join(t.TempDir(), "runtime.json")
	journal, err := NewJournalWithOptions(path, JournalOptions{Clock: clock, IDs: &testkit.SequenceIDs{}})
	if err != nil {
		t.Fatal(err)
	}
	scope := testScope()
	lease, err := journal.AcquireLease(scope, "worker", time.Hour, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.AuthorizeToolContract(scope, "call-receipt", "worker", lease.FencingToken, ToolContract{Name: "write", Capabilities: []string{"record.write"}, SideEffectClass: EffectReconcilableWrite, ReconcilePolicy: ReconcileQuery}, []string{"record.write"}); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.StartTool(scope, "call-receipt", "write", "worker", lease.FencingToken, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := journal.CommitEffectIntentWithPolicy(scope, "effect-receipt", "call-receipt", "write", EffectReconcilableWrite, ReconcileQuery, map[string]any{"v": 1}, "worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.PrepareEffectDispatch(scope, "effect-receipt", "worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.MarkEffectDispatchedWithTimeout(scope, "effect-receipt", 5*time.Minute, "worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	timers, err := NewTimerStore(filepath.Join(t.TempDir(), "timers.json"), TimerStoreOptions{Clock: clock, IDs: &testkit.SequenceIDs{}})
	if err != nil {
		t.Fatal(err)
	}
	if report, err := journal.ProjectTimerSchedules(context.Background(), timers); err != nil || report.Created != 1 {
		t.Fatalf("projection=%+v err=%v", report, err)
	}
	items, err := timers.List(scope, false)
	if err != nil || len(items) != 1 {
		t.Fatalf("timers=%+v err=%v", items, err)
	}
	clock.Advance(5 * time.Minute)
	claims, err := timers.ClaimDue(clock.Now(), "timer-worker", time.Minute, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	if _, err := journal.CompleteToolEffect(scope, "effect-receipt", "call-receipt", "worker", lease.FencingToken, map[string]any{"ok": true}); err != nil {
		t.Fatal(err)
	}
	result, err := journal.ApplyEffectTimeoutClaim(claims[0], clock.Now(), "worker", lease.FencingToken)
	if err != nil || result.TimedOut || result.Reason != "terminal_receipt_won_before_timeout" {
		t.Fatalf("receipt won result=%+v err=%v", result, err)
	}
	if _, err := timers.Acknowledge(claims[0].Timer.ID, "timer-worker", claims[0].Timer.FencingToken, claims[0].OccurrenceKey, clock.Now()); err != nil {
		t.Fatal(err)
	}

	// A second effect with no receipt is converted to unknown exactly once.
	if _, err := journal.AuthorizeToolContract(scope, "call-unknown", "worker", lease.FencingToken, ToolContract{Name: "write", Capabilities: []string{"record.write"}, SideEffectClass: EffectReconcilableWrite, ReconcilePolicy: ReconcileQuery}, []string{"record.write"}); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.StartTool(scope, "call-unknown", "write", "worker", lease.FencingToken, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := journal.CommitEffectIntentWithPolicy(scope, "effect-unknown", "call-unknown", "write", EffectReconcilableWrite, ReconcileQuery, map[string]any{"v": 2}, "worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.PrepareEffectDispatch(scope, "effect-unknown", "worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	if _, err := journal.MarkEffectDispatchedWithTimeout(scope, "effect-unknown", time.Minute, "worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if report, err := journal.ProjectTimerSchedules(context.Background(), timers); err != nil || report.Created != 1 {
		t.Fatalf("second projection=%+v err=%v", report, err)
	}
	clock.Advance(time.Minute)
	claims, err = timers.ClaimDue(clock.Now(), "timer-worker", time.Minute, 10)
	if err != nil || len(claims) != 1 {
		t.Fatalf("unknown claims=%+v err=%v", claims, err)
	}
	result, err = journal.ApplyEffectTimeoutClaim(claims[0], clock.Now(), "worker", lease.FencingToken)
	if err != nil || !result.TimedOut || result.Reason != "effect_timeout" || result.Event.EventType != EventEffectUnknown {
		t.Fatalf("unknown result=%+v err=%v", result, err)
	}
	again, err := journal.ApplyEffectTimeoutClaim(claims[0], clock.Now(), "worker", lease.FencingToken)
	if err != nil || !again.TimedOut || again.Reason != "timeout_already_recorded" || again.Event.EventID != result.Event.EventID {
		t.Fatalf("duplicate timeout result=%+v err=%v", again, err)
	}
}
