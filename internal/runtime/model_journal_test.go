package runtime

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func prepareModelForJournal(t *testing.T, j *Journal, owner string) (Scope, ModelRequest, Lease) {
	t.Helper()
	scope := testScope()
	lease, err := j.AcquireLease(scope, owner, time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	request := newModelRequest(t)
	request.Scope = scope
	request, err = NewModelRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	return scope, request, lease
}

func TestJournalModelLifecycleReplaysRequestStreamAndTerminalAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	j := mustJournal(t, path)
	scope, request, lease := prepareModelForJournal(t, j, "model-worker")
	first, created, err := j.CommitModelRequest(scope, request, "model-worker", lease.FencingToken)
	if err != nil || !created || first.EventType != EventModelRequested {
		t.Fatalf("request event=%+v created=%v err=%v", first, created, err)
	}
	second, created, err := j.CommitModelRequest(scope, request, "model-worker", lease.FencingToken)
	if err != nil || created || second.EventID != first.EventID {
		t.Fatalf("request retry=%+v created=%v err=%v", second, created, err)
	}
	if _, err := j.PrepareModelDispatch(scope, request.RequestID, "model-worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := j.MarkModelDispatched(scope, request.RequestID, "model-worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	text := modelEvent(request.RequestID, 1, ModelEventTextDelta)
	if _, err := j.AppendModelEvent(scope, text, "model-worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	finish := modelEvent(request.RequestID, 2, ModelEventFinish)
	terminal, err := j.CompleteModelCall(scope, finish, "model-worker", lease.FencingToken)
	if err != nil || terminal.EventType != EventModelCompleted {
		t.Fatalf("terminal=%+v err=%v", terminal, err)
	}
	state, err := j.ModelState(scope, request.RequestID)
	if err != nil || !state.Requested || !state.DispatchPrepared || !state.Dispatched || !state.Completed || state.OutcomeUnknown || state.StreamSequence != 2 || state.FinishReason != ModelFinishCompleted {
		t.Fatalf("model state=%+v err=%v", state, err)
	}
	if _, err := j.MarkModelOutcomeUnknown(scope, request.RequestID, "late", "model-worker", lease.FencingToken); !errors.Is(err, ErrModelTransition) {
		t.Fatalf("completed model entered unknown state: %v", err)
	}
	restarted, err := NewJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := restarted.ModelState(scope, request.RequestID)
	if err != nil || replayed != state {
		t.Fatalf("replayed model state=%+v want=%+v err=%v", replayed, state, err)
	}
}

func TestJournalModelUnknownOutcomeIsTerminalUntilRecovery(t *testing.T) {
	j := mustJournal(t, "")
	scope, request, lease := prepareModelForJournal(t, j, "model-worker")
	if _, _, err := j.CommitModelRequest(scope, request, "model-worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := j.PrepareModelDispatch(scope, request.RequestID, "model-worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := j.MarkModelDispatched(scope, request.RequestID, "model-worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := j.MarkModelOutcomeUnknown(scope, request.RequestID, "connection_lost", "model-worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	state, err := j.ModelState(scope, request.RequestID)
	if err != nil || !state.OutcomeUnknown || state.Completed {
		t.Fatalf("unknown model state=%+v err=%v", state, err)
	}
	if _, err := j.AppendModelEvent(scope, modelEvent(request.RequestID, 1, ModelEventTextDelta), "model-worker", lease.FencingToken); !errors.Is(err, ErrModelTransition) {
		t.Fatalf("unknown model accepted late stream event: %v", err)
	}
	if _, err := j.MarkModelDispatched(scope, request.RequestID, "model-worker", lease.FencingToken); !errors.Is(err, ErrModelTransition) {
		t.Fatalf("unknown model was dispatched again: %v", err)
	}
}

func TestJournalRejectsModelRequestDigestConflictsAndOutOfOrderFrames(t *testing.T) {
	j := mustJournal(t, "")
	scope, request, lease := prepareModelForJournal(t, j, "model-worker")
	if _, _, err := j.CommitModelRequest(scope, request, "model-worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	changed := request
	changed.Prompt = []byte(`{"changed":true}`)
	changed, err := NewModelRequest(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := j.CommitModelRequest(scope, changed, "model-worker", lease.FencingToken); !errors.Is(err, ErrModelIdempotencyConflict) {
		t.Fatalf("changed request accepted: %v", err)
	}
	if _, err := j.PrepareModelDispatch(scope, request.RequestID, "model-worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := j.MarkModelDispatched(scope, request.RequestID, "model-worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := j.AppendModelEvent(scope, modelEvent(request.RequestID, 2, ModelEventTextDelta), "model-worker", lease.FencingToken); err == nil {
		t.Fatal("out-of-order model frame accepted")
	}
}
