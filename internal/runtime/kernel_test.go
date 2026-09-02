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
	if _, err := j.AuthorizeTool(scope, "call-1", "search", "worker-1", lease.FencingToken, []string{"search"}); err != nil {
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
	if _, err := j.AuthorizeTool(scope, "call-2", "shell", "worker-1", lease.FencingToken, []string{"search"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected deny-by-default authorization, got %v", err)
	}
}

func TestJournalRejectsStaleFenceAndEffectRetries(t *testing.T) {
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
	first, created, err := j.FenceEffect(scope, "effect-1", "worker-2", 2, map[string]any{"value": 1})
	if err != nil || !created {
		t.Fatalf("first effect=%+v created=%v err=%v", first, created, err)
	}
	second, created, err := j.FenceEffect(scope, "effect-1", "worker-2", 2, map[string]any{"value": 1})
	if err != nil || created || second.EventID != first.EventID {
		t.Fatalf("retry did not converge first=%+v second=%+v created=%v err=%v", first, second, created, err)
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

func TestJournalConcurrentEffectFencingIsExactlyOnce(t *testing.T) {
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
			event, _, err := j.FenceEffect(scope, "effect-concurrent", "worker-1", lease.FencingToken, map[string]any{"ok": true})
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

func mustJournal(t *testing.T, path string) *Journal {
	t.Helper()
	j, err := NewJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	return j
}
