package eval

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testBundle(t *testing.T, events []BundleEvent) SessionBundle {
	t.Helper()
	bundle, err := NewSessionBundle("session-1", "tenant-1", "workspace-1", "event-digest", events, true)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestBuiltinTrajectoryEvaluatorProducesMinimalEvidenceBundle(t *testing.T) {
	bundle := testBundle(t, []BundleEvent{
		{Sequence: 1, Type: "effect.intent_committed", EffectID: "e1", Capability: "artifact.write", Authorized: true},
		{Sequence: 2, Type: "effect.dispatched", EffectID: "e1", Capability: "artifact.write", Authorized: true, Dispatched: true, Attempt: 1},
		{Sequence: 3, Type: "effect.outcome_unknown", EffectID: "e1", Capability: "artifact.write", Authorized: true},
		{Sequence: 4, Type: "effect.dispatched", EffectID: "e1", Capability: "artifact.write", Authorized: true, Dispatched: true, Attempt: 2},
		{Sequence: 5, Type: "effect.receipted", EffectID: "e2", Receipted: true},
	})
	result, err := BuiltinTrajectoryEvaluator().Evaluate(context.Background(), bundle)
	if err != nil {
		t.Fatal(err)
	}
	if result.Score >= 1 || result.EvidenceDigest == "" || result.MinimalBundle == nil {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.MinimalBundle.Events) == 0 || result.MinimalBundle.Redacted != true {
		t.Fatalf("minimal bundle=%+v", result.MinimalBundle)
	}
	if err := result.MinimalBundle.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEvalRunStateMachineBudgetCancellationAndResume(t *testing.T) {
	bundle := testBundle(t, []BundleEvent{{Sequence: 1, Type: "session.started"}})
	clock := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	nextID := 0
	store := NewStore(StoreOptions{Now: func() time.Time { return clock }, ID: func() string { nextID++; return "run-" + string(rune('0'+nextID)) }})
	evaluator := RuleEvaluator{EvaluatorID: "test", VersionID: "v1", Checks: map[string]Check{
		"ok": func(context.Context, SessionBundle) (Finding, error) {
			return Finding{RuleID: "ok", Severity: SeverityInfo, Message: "ok"}, nil
		},
	}}
	run, err := store.Start(bundle, evaluator, 100)
	if err != nil || run.State != RunQueued || run.Revision != 1 {
		t.Fatalf("start=%+v err=%v", run, err)
	}
	completed, err := store.Execute(context.Background(), run.ID, run.Revision, evaluator)
	if err != nil || completed.State != RunCompleted || completed.Result == nil {
		t.Fatalf("execute=%+v err=%v", completed, err)
	}
	if _, err := store.Execute(context.Background(), completed.ID, completed.Revision, evaluator); !errors.Is(err, ErrTerminal) {
		t.Fatalf("terminal run was re-executed: %v", err)
	}

	cancelRun, err := store.Start(bundle, evaluator, 100)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	paused, err := store.Execute(ctx, cancelRun.ID, cancelRun.Revision, evaluator)
	if !errors.Is(err, context.Canceled) || paused.State != RunPaused {
		t.Fatalf("cancelled execution=%+v err=%v", paused, err)
	}
	resumed, err := store.Execute(context.Background(), paused.ID, paused.Revision, evaluator)
	if err != nil || resumed.State != RunCompleted {
		t.Fatalf("resume=%+v err=%v", resumed, err)
	}
}

func TestEvalRejectsCrossTenantOrTamperedBundle(t *testing.T) {
	bundle := testBundle(t, []BundleEvent{{Sequence: 1, Type: "session.started"}})
	bundle.TenantID = "other"
	if err := bundle.Validate(); err == nil {
		t.Fatal("tampered bundle digest was accepted")
	}
	if _, err := NewSessionBundle("", "tenant", "workspace", "digest", nil, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid identity error=%v", err)
	}
}
