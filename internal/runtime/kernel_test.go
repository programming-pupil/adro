package runtime

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testScope() Scope {
	return Scope{TenantID: "tenant-1", WorkspaceID: "workspace-1", SessionID: "session-1", RunID: "run-1"}
}

func TestJournalToolLoopIsAuthorizedAndAtomic(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker-1", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	search := ToolContract{Name: "search", Capabilities: []string{"knowledge.read"}, SideEffectClass: EffectReadOnly, ReconcilePolicy: ReconcileNone}
	if _, err := j.AuthorizeToolContract(scope, "call-1", "worker-1", lease.FencingToken, search, []string{"knowledge.read"}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.StartTool(scope, "call-1", "search", "worker-1", lease.FencingToken, map[string]any{"q": "durability"}); err != nil {
		t.Fatal(err)
	}
	finished, err := j.FinishTool(scope, "call-1", "worker-1", lease.FencingToken, map[string]any{"ok": true})
	if err != nil || finished.EventType != EventToolFinished {
		t.Fatalf("finish=%+v err=%v", finished, err)
	}
	if err := j.Verify(); err != nil {
		t.Fatal(err)
	}
	shell := ToolContract{Name: "shell", Capabilities: []string{"process.execute"}, SideEffectClass: EffectNonRetriableWrite, ReconcilePolicy: ReconcileHuman}
	if _, err := j.AuthorizeToolContract(scope, "call-2", "worker-1", lease.FencingToken, shell, []string{"knowledge.read"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected deny-by-default authorization, got %v", err)
	}
}

func TestJournalRejectsStaleFenceAndEffectIntentConflicts(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker-1", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Append(Input{EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID, Scope: scope, WriterID: "worker-1", FencingToken: lease.FencingToken, IdempotencyKey: "turn-1", Payload: map[string]any{"input": "hello"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.AcquireLease(scope, "worker-2", time.Minute, time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Append(Input{EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID, Scope: scope, WriterID: "worker-1", FencingToken: lease.FencingToken, Payload: map[string]any{"input": "stale"}}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expected stale fence rejection, got %v", err)
	}
	first, created, err := j.CommitEffectIntent(scope, "effect-1", "call-1", "write", EffectNonRetriableWrite, map[string]any{"value": 1}, "worker-2", 2)
	if err != nil || !created {
		t.Fatalf("first effect=%+v created=%v err=%v", first, created, err)
	}
	second, created, err := j.CommitEffectIntent(scope, "effect-1", "call-1", "write", EffectNonRetriableWrite, map[string]any{"value": 1}, "worker-2", 2)
	if err != nil || created || second.EventID != first.EventID {
		t.Fatalf("retry did not converge first=%+v second=%+v created=%v err=%v", first, second, created, err)
	}
	if _, _, err := j.CommitEffectIntent(scope, "effect-1", "call-1", "write", EffectNonRetriableWrite, map[string]any{"value": 2}, "worker-2", 2); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected changed intent to conflict, got %v", err)
	}
}

func TestJournalIdempotencyConflictAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	j := mustJournal(t, path)
	scope := testScope()
	if _, err := j.Append(Input{EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID, Scope: scope, IdempotencyKey: "turn-1", Payload: map[string]any{"input": "hello"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Append(Input{EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID, Scope: scope, IdempotencyKey: "turn-1", Payload: map[string]any{"input": "different"}}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
	restarted, err := NewJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(restarted.List(scope)) != 1 || restarted.Verify() != nil {
		t.Fatalf("restart did not preserve verified journal: %+v", restarted.List(scope))
	}
}

func TestJournalConcurrentEffectIntentIsIdempotent(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker-1", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan string, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			event, _, err := j.CommitEffectIntent(scope, "effect-concurrent", "call-concurrent", "write", EffectIdempotentWrite, map[string]any{"ok": true}, "worker-1", lease.FencingToken)
			if err == nil {
				results <- event.EventID
			}
		}()
	}
	wg.Wait()
	close(results)
	ids := map[string]struct{}{}
	for id := range results {
		ids[id] = struct{}{}
	}
	if len(ids) != 1 || len(j.List(scope)) != 1 {
		t.Fatalf("expected one fenced effect, ids=%v events=%+v", ids, j.List(scope))
	}
}

// Threat ID: TM-DURABLE-001
func TestStaleWorkerCannotCommitEffectReceipt(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker-1", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	write := ToolContract{Name: "write", Capabilities: []string{"record.write"}, SideEffectClass: EffectNonRetriableWrite, ReconcilePolicy: ReconcileHuman}
	if _, err := j.AuthorizeToolContract(scope, "call-stale", "worker-1", lease.FencingToken, write, []string{"record.write"}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.StartTool(scope, "call-stale", "write", "worker-1", lease.FencingToken, map[string]any{"value": 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := j.CommitEffectIntent(scope, "effect-stale", "call-stale", "write", EffectNonRetriableWrite, map[string]any{"value": 1}, "worker-1", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := j.PrepareEffectDispatch(scope, "effect-stale", "worker-1", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := j.MarkEffectDispatched(scope, "effect-stale", "worker-1", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := j.AcquireLease(scope, "worker-2", time.Minute, time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := j.CompleteToolEffect(scope, "effect-stale", "call-stale", "worker-1", lease.FencingToken, map[string]any{"ok": true}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expected stale receipt rejection, got %v", err)
	}
	state, err := j.EffectState(scope, "effect-stale")
	if err != nil || state.Receipted {
		t.Fatalf("effect state=%+v err=%v", state, err)
	}
}

func TestEffectReceiptAndUnknownOutcomeCannotBothCommit(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	write := ToolContract{Name: "write", Capabilities: []string{"record.write"}, SideEffectClass: EffectReconcilableWrite, ReconcilePolicy: ReconcileQuery}
	if _, err := j.AuthorizeToolContract(scope, "call-race", "worker", lease.FencingToken, write, []string{"record.write"}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.StartTool(scope, "call-race", "write", "worker", lease.FencingToken, map[string]any{"value": 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := j.CommitEffectIntent(scope, "effect-race", "call-race", "write", EffectReconcilableWrite, map[string]any{"value": 1}, "worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := j.PrepareEffectDispatch(scope, "effect-race", "worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := j.MarkEffectDispatched(scope, "effect-race", "worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	results := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := j.CompleteToolEffect(scope, "effect-race", "call-race", "worker", lease.FencingToken, map[string]any{"ok": true})
		results <- err
	}()
	go func() {
		defer wg.Done()
		_, err := j.MarkEffectOutcomeUnknown(scope, "effect-race", "lost_response", "worker", lease.FencingToken)
		results <- err
	}()
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatalf("unexpected transition error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("terminal transitions succeeded=%d", succeeded)
	}
	state, err := j.EffectState(scope, "effect-race")
	if err != nil || state.Receipted == state.OutcomeUnknown {
		t.Fatalf("effect state=%+v err=%v", state, err)
	}
}

func mustJournal(t *testing.T, path string) *Journal {
	t.Helper()
	j, err := NewJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestToolContractApprovalCannotBeBypassedByDirectStart(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	contract := ToolContract{
		Name: "deploy", Capabilities: []string{"deployment.write"},
		SideEffectClass: EffectNonRetriableWrite, ReconcilePolicy: ReconcileHuman,
		RequiresApproval: true,
	}
	if _, err := j.AuthorizeToolContract(scope, "approval-direct", "worker", lease.FencingToken, contract, []string{"deployment.write"}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.StartTool(scope, "approval-direct", "deploy", "worker", lease.FencingToken, nil); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("direct start bypassed approval: %v", err)
	}
	if _, err := j.ApproveTool(scope, "approval-direct", "worker", lease.FencingToken, "approved"); err != nil {
		t.Fatal(err)
	}
	if _, err := j.StartTool(scope, "approval-direct", "deploy", "worker", lease.FencingToken, nil); err != nil {
		t.Fatalf("approved direct start failed: %v", err)
	}
}

func TestEffectTransitionsRequirePositiveFenceAndMatchingTool(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	contract := ToolContract{Name: "write", Capabilities: []string{"record.write"}, SideEffectClass: EffectReconcilableWrite, ReconcilePolicy: ReconcileQuery}
	if _, err := j.AuthorizeToolContract(scope, "effect-call", "worker", lease.FencingToken, contract, []string{"record.write"}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.StartTool(scope, "effect-call", "write", "worker", lease.FencingToken, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := j.CommitEffectIntent(scope, "effect-call-id", "effect-call", "write", EffectReconcilableWrite, nil, "worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := j.PrepareEffectDispatch(scope, "effect-call-id", "", 0); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("zero fence accepted by prepare: %v", err)
	}
	if _, err := j.PrepareEffectDispatch(scope, "effect-call-id", "worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := j.MarkEffectDispatched(scope, "effect-call-id", "worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := j.CompleteToolEffect(scope, "effect-call-id", "different-call", "worker", lease.FencingToken, map[string]any{"ok": true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("receipt completed mismatched tool: %v", err)
	}
	if _, err := j.MarkEffectOutcomeUnknown(scope, "effect-call-id", "lost", "worker", lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := j.ReconcileEffect(scope, "effect-call-id", "different-call", "worker", lease.FencingToken, "confirmed", nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("reconciliation completed mismatched tool: %v", err)
	}
}
