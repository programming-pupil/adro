package runtime

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

const lifecycleOwner = "lifecycle-worker"

type lifecycleFixture struct {
	journal *Journal
	scope   Scope
	fence   int64
	config  ConfigSnapshot
	context StepContextSnapshot
}

func newLifecycleFixture(t *testing.T, path string) lifecycleFixture {
	t.Helper()
	journal := mustJournal(t, path)
	scope := testScope()
	lease, err := journal.AcquireLease(scope, lifecycleOwner, time.Hour, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	config := mustConfigSnapshot(t)
	contextSnapshot, err := FreezeStepContext(StepContextSnapshot{
		ContextManifestDigest: "context-sha256",
		ToolCatalogDigest:     "tools-sha256",
		PolicySnapshotDigest:  "policy-sha256",
		Config:                config,
		FrozenAt:              time.Date(2026, time.September, 19, 3, 4, 5, 6000, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	return lifecycleFixture{journal: journal, scope: scope, fence: lease.FencingToken, config: config, context: contextSnapshot}
}

func mustConfigSnapshot(t *testing.T) ConfigSnapshot {
	t.Helper()
	snapshot, err := FreezeConfigSnapshot(ConfigSnapshot{
		Version:          "runtime-v1",
		FeatureGates:     map[string]bool{"durable_effects": true, "strict_replay": false},
		AdapterVersions:  map[string]string{"model": "v2", "tool": "v1"},
		ProtocolVersions: map[string]string{"events": "v1"},
		PolicyBundle:     "policy-sha256",
		TokenizerID:      "tokenizer-v1",
		CapturedAt:       time.Date(2026, time.September, 19, 1, 2, 3, 4000, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func startSessionTurn(t *testing.T, fixture lifecycleFixture, turnID string) {
	t.Helper()
	if _, err := fixture.journal.StartSession(fixture.scope, lifecycleOwner, fixture.fence, map[string]any{"source": "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.CreateTurn(fixture.scope, turnID, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.FreezeTurnContext(fixture.scope, turnID, lifecycleOwner, fixture.fence, fixture.config); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.StartTurn(fixture.scope, turnID, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
}

func createStep(t *testing.T, fixture lifecycleFixture, turnID, stepID string) {
	t.Helper()
	if _, err := fixture.journal.CreateStep(fixture.scope, stepID, turnID, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
}

func completeStep(t *testing.T, fixture lifecycleFixture, stepID string) {
	t.Helper()
	transitions := []func() error{
		func() error {
			_, err := fixture.journal.FreezeStepContext(fixture.scope, stepID, lifecycleOwner, fixture.fence, fixture.context)
			return err
		},
		func() error {
			_, err := fixture.journal.CommitStepModelRequest(fixture.scope, stepID, "request-1", lifecycleOwner, fixture.fence)
			return err
		},
		func() error {
			_, err := fixture.journal.StartStepModelStream(fixture.scope, stepID, lifecycleOwner, fixture.fence)
			return err
		},
		func() error {
			_, err := fixture.journal.MarkStepToolRequestsReady(fixture.scope, stepID, lifecycleOwner, fixture.fence)
			return err
		},
		func() error {
			_, err := fixture.journal.StartStepEffects(fixture.scope, stepID, lifecycleOwner, fixture.fence)
			return err
		},
		func() error {
			_, err := fixture.journal.AssembleStepResult(fixture.scope, stepID, lifecycleOwner, fixture.fence)
			return err
		},
		func() error {
			_, err := fixture.journal.CheckpointStep(fixture.scope, stepID, lifecycleOwner, fixture.fence, "checkpoint-sha256")
			return err
		},
		func() error {
			_, err := fixture.journal.CompleteStep(fixture.scope, stepID, lifecycleOwner, fixture.fence)
			return err
		},
	}
	for index, transition := range transitions {
		if err := transition(); err != nil {
			t.Fatalf("step transition %d: %v", index, err)
		}
	}
}

func TestLifecycleStateMachineHappyPathReplaysAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	fixture := newLifecycleFixture(t, path)
	startEvent, err := fixture.journal.StartSession(fixture.scope, lifecycleOwner, fixture.fence, map[string]any{"source": "test"})
	if err != nil {
		t.Fatal(err)
	}
	retried, err := fixture.journal.StartSession(fixture.scope, lifecycleOwner, fixture.fence, map[string]any{"source": "test"})
	if err != nil || retried.EventID != startEvent.EventID {
		t.Fatalf("session start retry=%+v err=%v", retried, err)
	}

	const turnID, stepID = "turn-1", "step-1"
	if _, err := fixture.journal.CreateTurn(fixture.scope, turnID, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.FreezeTurnContext(fixture.scope, turnID, lifecycleOwner, fixture.fence, fixture.config); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.StartTurn(fixture.scope, turnID, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	createStep(t, fixture, turnID, stepID)
	completeStep(t, fixture, stepID)
	if _, err := fixture.journal.BeginTurnCheckpoint(fixture.scope, turnID, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.CompleteTurn(fixture.scope, turnID, lifecycleOwner, fixture.fence, StopCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.CompleteSession(fixture.scope, lifecycleOwner, fixture.fence, StopCompleted); err != nil {
		t.Fatal(err)
	}
	if err := fixture.journal.Verify(); err != nil {
		t.Fatal(err)
	}

	session, err := fixture.journal.SessionState(fixture.scope)
	if err != nil || session.Status != SessionCompleted || session.StopReason != StopCompleted {
		t.Fatalf("session=%+v err=%v", session, err)
	}
	turn, err := fixture.journal.TurnState(fixture.scope, turnID)
	if err != nil || turn.Status != TurnCompleted || turn.StopReason != StopCompleted || turn.ConfigSnapshotDigest != fixture.config.Digest {
		t.Fatalf("turn=%+v err=%v", turn, err)
	}
	step, err := fixture.journal.StepState(fixture.scope, stepID)
	if err != nil || step.Status != StepCompleted || step.StopReason != StopCompleted || step.ContextSnapshotDigest != fixture.context.Digest || step.ModelRequestID != "request-1" {
		t.Fatalf("step=%+v err=%v", step, err)
	}
	if len(fixture.journal.List(fixture.scope)) != 16 {
		t.Fatalf("event count=%d", len(fixture.journal.List(fixture.scope)))
	}

	restarted, err := NewJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	replayedSession, _ := restarted.SessionState(fixture.scope)
	replayedTurn, _ := restarted.TurnState(fixture.scope, turnID)
	replayedStep, _ := restarted.StepState(fixture.scope, stepID)
	if replayedSession != session || replayedTurn != turn || replayedStep != step {
		t.Fatalf("restart mismatch session=%+v turn=%+v step=%+v", replayedSession, replayedTurn, replayedStep)
	}
	if _, err := restarted.CreateTurn(fixture.scope, "turn-after-terminal", lifecycleOwner, fixture.fence); !errors.Is(err, ErrTurnTransition) {
		t.Fatalf("terminal session accepted turn: %v", err)
	}
}

func TestLifecycleRepeatedSuspendResumeUsesDistinctBoundaries(t *testing.T) {
	fixture := newLifecycleFixture(t, "")
	if _, err := fixture.journal.StartSession(fixture.scope, lifecycleOwner, fixture.fence, nil); err != nil {
		t.Fatal(err)
	}
	for cycle := 0; cycle < 2; cycle++ {
		if _, err := fixture.journal.SuspendSession(fixture.scope, lifecycleOwner, fixture.fence, "operator"); err != nil {
			t.Fatalf("suspend cycle %d: %v", cycle, err)
		}
		if _, err := fixture.journal.ResumeSession(fixture.scope, lifecycleOwner, fixture.fence); err != nil {
			t.Fatalf("resume cycle %d: %v", cycle, err)
		}
	}
	const turnID = "turn-cycles"
	if _, err := fixture.journal.CreateTurn(fixture.scope, turnID, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.FreezeTurnContext(fixture.scope, turnID, lifecycleOwner, fixture.fence, fixture.config); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.StartTurn(fixture.scope, turnID, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.WaitTurnApproval(fixture.scope, turnID, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.ResumeTurn(fixture.scope, turnID, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.WaitTurnInput(fixture.scope, turnID, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.ResumeTurn(fixture.scope, turnID, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.SuspendTurn(fixture.scope, turnID, lifecycleOwner, fixture.fence, "maintenance"); err != nil {
		t.Fatal(err)
	}
	lastResume, err := fixture.journal.ResumeTurn(fixture.scope, turnID, lifecycleOwner, fixture.fence)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := fixture.journal.ResumeTurn(fixture.scope, turnID, lifecycleOwner, fixture.fence)
	if err != nil || retry.EventID != lastResume.EventID {
		t.Fatalf("resume retry=%+v err=%v", retry, err)
	}

	keys := map[string]struct{}{}
	resumeCount := 0
	for _, event := range fixture.journal.List(fixture.scope) {
		if event.EventType == EventTurnResumed {
			resumeCount++
			keys[event.IdempotencyKey] = struct{}{}
		}
	}
	if resumeCount != 3 || len(keys) != 3 {
		t.Fatalf("turn resume events=%d distinct keys=%d", resumeCount, len(keys))
	}
}

func TestLifecycleRejectsIllegalTransitionsAndParentTerminalization(t *testing.T) {
	fixture := newLifecycleFixture(t, "")
	startSessionTurn(t, fixture, "turn-1")
	createStep(t, fixture, "turn-1", "step-1")

	if _, err := fixture.journal.CommitStepModelRequest(fixture.scope, "step-1", "request-early", lifecycleOwner, fixture.fence); !errors.Is(err, ErrStepTransition) {
		t.Fatalf("model request committed before context freeze: %v", err)
	}
	if _, err := fixture.journal.BeginTurnCheckpoint(fixture.scope, "turn-1", lifecycleOwner, fixture.fence); !errors.Is(err, ErrTurnTransition) {
		t.Fatalf("turn checkpointed with active step: %v", err)
	}
	if _, err := fixture.journal.CancelTurn(fixture.scope, "turn-1", lifecycleOwner, fixture.fence, StopCancelledByUser); !errors.Is(err, ErrTurnTransition) {
		t.Fatalf("turn cancelled with active step: %v", err)
	}
	if _, err := fixture.journal.CompleteSession(fixture.scope, lifecycleOwner, fixture.fence, StopCompleted); !errors.Is(err, ErrSessionTransition) {
		t.Fatalf("session completed with active child: %v", err)
	}
	if _, err := fixture.journal.CancelStep(fixture.scope, "step-1", lifecycleOwner, fixture.fence, StopCancelledByUser); !errors.Is(err, ErrStepTransition) {
		t.Fatalf("step cancellation skipped cancelling: %v", err)
	}
	if _, err := fixture.journal.BeginStepCancellation(fixture.scope, "step-1", lifecycleOwner, fixture.fence, StopCancelledByUser); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.CancelStep(fixture.scope, "step-1", lifecycleOwner, fixture.fence, StopCancelledByUser); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.CancelTurn(fixture.scope, "turn-1", lifecycleOwner, fixture.fence, StopCancelledByUser); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.BeginSessionCancellation(fixture.scope, lifecycleOwner, fixture.fence, StopCancelledByUser); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.CancelSession(fixture.scope, lifecycleOwner, fixture.fence, StopCancelledByUser); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleUnknownOutcomeCannotCompleteOrRedispatch(t *testing.T) {
	fixture := newLifecycleFixture(t, "")
	startSessionTurn(t, fixture, "turn-unknown")
	createStep(t, fixture, "turn-unknown", "step-unknown")
	if _, err := fixture.journal.FreezeStepContext(fixture.scope, "step-unknown", lifecycleOwner, fixture.fence, fixture.context); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.CommitStepModelRequest(fixture.scope, "step-unknown", "request-unknown", lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.MarkStepModelOutcomeUnknown(fixture.scope, "step-unknown", lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	operations := []func() error{
		func() error {
			_, err := fixture.journal.StartStepModelStream(fixture.scope, "step-unknown", lifecycleOwner, fixture.fence)
			return err
		},
		func() error {
			_, err := fixture.journal.AssembleStepResult(fixture.scope, "step-unknown", lifecycleOwner, fixture.fence)
			return err
		},
		func() error {
			_, err := fixture.journal.CompleteStep(fixture.scope, "step-unknown", lifecycleOwner, fixture.fence)
			return err
		},
	}
	for index, operation := range operations {
		if err := operation(); !errors.Is(err, ErrStepTransition) {
			t.Fatalf("unknown operation %d error=%v", index, err)
		}
	}
	state, err := fixture.journal.StepState(fixture.scope, "step-unknown")
	if err != nil || state.Status != StepModelOutcomeUnknown || state.StopReason != StopProviderOutcomeUnknown {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestLifecycleSnapshotsAreCanonicalAndFailClosed(t *testing.T) {
	first := mustConfigSnapshot(t)
	second, err := FreezeConfigSnapshot(ConfigSnapshot{
		Version:          first.Version,
		FeatureGates:     map[string]bool{"strict_replay": false, "durable_effects": true},
		AdapterVersions:  map[string]string{"tool": "v1", "model": "v2"},
		ProtocolVersions: map[string]string{"events": "v1"},
		PolicyBundle:     first.PolicyBundle,
		TokenizerID:      first.TokenizerID,
		CapturedAt:       first.CapturedAt,
	})
	if err != nil || second.Digest != first.Digest {
		t.Fatalf("canonical digest first=%s second=%s err=%v", first.Digest, second.Digest, err)
	}
	if _, err := FreezeConfigSnapshot(ConfigSnapshot{
		Version: "v1", FeatureGates: map[string]bool{" invalid ": true}, PolicyBundle: "policy", TokenizerID: "tokens", CapturedAt: time.Now(),
	}); !errors.Is(err, ErrConfigSnapshotInvalid) {
		t.Fatalf("invalid feature gate accepted: %v", err)
	}
	tampered := first
	tampered.PolicyBundle = "different"
	if err := tampered.Validate(); !errors.Is(err, ErrConfigSnapshotInvalid) {
		t.Fatalf("tampered config accepted: %v", err)
	}
	contextSnapshot, err := FreezeStepContext(StepContextSnapshot{
		ContextManifestDigest: "context", ToolCatalogDigest: "tools", PolicySnapshotDigest: "policy", Config: first,
		FrozenAt: time.Date(2026, time.September, 19, 6, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	contextSnapshot.ToolCatalogDigest = "changed"
	if err := contextSnapshot.Validate(); !errors.Is(err, ErrStepContextInvalid) {
		t.Fatalf("tampered context accepted: %v", err)
	}
}

func TestLifecycleValidationIdempotencyConflictAndStaleFence(t *testing.T) {
	fixture := newLifecycleFixture(t, "")
	if _, err := fixture.journal.StartSession(Scope{}, lifecycleOwner, fixture.fence, nil); !errors.Is(err, ErrLifecycleInputInvalid) {
		t.Fatalf("invalid scope error=%v", err)
	}
	if _, err := fixture.journal.StartSession(fixture.scope, "", fixture.fence, nil); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("empty owner error=%v", err)
	}
	if _, err := fixture.journal.StartSession(fixture.scope, lifecycleOwner, fixture.fence, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.SuspendSession(fixture.scope, lifecycleOwner, fixture.fence, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.SuspendSession(fixture.scope, lifecycleOwner, fixture.fence, "changed"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed retry error=%v", err)
	}

	lease, err := fixture.journal.AcquireLease(fixture.scope, "replacement-worker", time.Hour, time.Now().Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.SuspendSession(fixture.scope, lifecycleOwner, fixture.fence, "first"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale retry error=%v", err)
	}
	if _, err := fixture.journal.ResumeSession(fixture.scope, "replacement-worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleStopReasonsFailClosed(t *testing.T) {
	fixture := newLifecycleFixture(t, "")
	if _, err := fixture.journal.StartSession(fixture.scope, lifecycleOwner, fixture.fence, nil); err != nil {
		t.Fatal(err)
	}
	invalid := []StopReason{"", "unknown", StopCancelledByUser, StopProviderFailed}
	for _, reason := range invalid {
		if _, err := fixture.journal.CompleteSession(fixture.scope, lifecycleOwner, fixture.fence, reason); !errors.Is(err, ErrStopReasonInvalid) {
			t.Fatalf("completion reason %q error=%v", reason, err)
		}
	}
	if _, err := fixture.journal.BeginSessionCancellation(fixture.scope, lifecycleOwner, fixture.fence, StopCompleted); !errors.Is(err, ErrStopReasonInvalid) {
		t.Fatalf("invalid cancellation reason error=%v", err)
	}
}
