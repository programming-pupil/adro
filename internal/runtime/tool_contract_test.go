package runtime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func contractForTest(name string) ToolContract {
	return ToolContract{
		Name: name, Capabilities: []string{" knowledge.read ", "context.read", "knowledge.read"},
		SideEffectClass: EffectReadOnly, ReconcilePolicy: ReconcileNone,
	}
}

func TestFreezeToolContractCanonicalizesDigestAndCapabilities(t *testing.T) {
	first := contractForTest("search")
	first.InputSchema = `{"properties":{"q":{"maxLength":3,"type":"string"}},"required":["q"],"type":"object","additionalProperties":false}`
	second := contractForTest(" search ")
	second.Capabilities = []string{"context.read", "knowledge.read"}
	second.InputSchema = `{"additionalProperties":false,"type":"object","required":["q"],"properties":{"q":{"type":"string","maxLength":3}}}`
	a, err := FreezeToolContract(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := FreezeToolContract(second)
	if err != nil {
		t.Fatal(err)
	}
	if a.ContractDigest != b.ContractDigest || !reflect.DeepEqual(a.Capabilities, []string{"context.read", "knowledge.read"}) {
		t.Fatalf("canonical contracts differ: a=%+v b=%+v", a, b)
	}
}

func TestToolSchemaFailsClosedAndEnforcesBounds(t *testing.T) {
	base := contractForTest("bounded")
	base.InputSchema = `{"type":"object","required":["q","n"],"additionalProperties":false,"properties":{"q":{"type":"string","maxLength":0},"n":{"type":"integer","minimum":2,"maximum":4}}}`
	if _, err := FreezeToolContract(base); err != nil {
		t.Fatal(err)
	}
	for _, input := range []any{
		map[string]any{"q": "x", "n": 2},
		map[string]any{"q": "", "n": 5},
		map[string]any{"q": "", "n": 2, "extra": true},
	} {
		if _, err := validateToolInput(base, input); err == nil {
			t.Fatalf("invalid input accepted: %#v", input)
		}
	}
	unsupported := base
	unsupported.InputSchema = `{"type":"object","oneOf":[]}`
	if _, err := FreezeToolContract(unsupported); err == nil || !errors.Is(err, ErrToolContractInvalid) {
		t.Fatalf("unsupported schema keyword accepted: %v", err)
	}
	invalidType := base
	invalidType.InputSchema = `{"type":"object","maxLength":1}`
	if _, err := FreezeToolContract(invalidType); err == nil {
		t.Fatal("type-invalid schema accepted")
	}
}

func TestToolPayloadLimitAndFieldClassification(t *testing.T) {
	contract := contractForTest("limited")
	contract.MaxInputBytes = 4
	contract.FieldClasses = map[string]string{"$.token": ToolFieldSecret, "$.label": ToolFieldPublic}
	if _, err := FreezeToolContract(contract); err != nil {
		t.Fatal(err)
	}
	if _, err := validateToolInput(contract, "too long"); !errors.Is(err, ErrToolPayloadTooLarge) {
		t.Fatalf("expected input size failure, got %v", err)
	}
	bad := contract
	bad.FieldClasses = map[string]string{"$.token": "unknown"}
	if _, err := FreezeToolContract(bad); err == nil {
		t.Fatal("invalid field classification accepted")
	}
}

func TestToolCapabilityAuthorizationAndHotReloadConflict(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	contract := contractForTest("same-name")
	if _, err := j.AuthorizeToolContract(scope, "call", "worker", lease.FencingToken, contract, []string{"context.read", "knowledge.read"}); err != nil {
		t.Fatal(err)
	}
	other := contract
	other.Capabilities = []string{"knowledge.write"}
	if _, err := j.AuthorizeToolContract(scope, "call", "worker", lease.FencingToken, other, []string{"knowledge.write"}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("hot reload was not rejected: %v", err)
	}
	if _, err := j.AuthorizeToolContract(scope, "name-only", "worker", lease.FencingToken, contract, []string{"same-name"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("tool name unexpectedly authorized: %v", err)
	}
}

func TestInvalidInputSkipsCallbackAndInvalidOutputIsReplayable(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	loop := ToolLoop{Journal: j, Scope: scope, Owner: "worker", FencingToken: lease.FencingToken, AllowedCapabilities: []string{"context.read", "knowledge.read"}}
	contract := contractForTest("typed")
	contract.InputSchema = `{"type":"object","required":["q"],"properties":{"q":{"type":"string"}},"additionalProperties":false}`
	contract.OutputSchema = `{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}},"additionalProperties":false}`
	var calls atomic.Int32
	if _, err := loop.Run(context.Background(), "bad-input", contract, map[string]any{"wrong": true}, func(context.Context) (any, error) {
		calls.Add(1)
		return nil, nil
	}); err == nil || !errors.Is(err, ErrToolInputInvalid) || calls.Load() != 0 {
		t.Fatalf("invalid input result err=%v calls=%d", err, calls.Load())
	}
	first, err := loop.Run(context.Background(), "bad-output", contract, map[string]any{"q": "ok"}, func(context.Context) (any, error) {
		calls.Add(1)
		return map[string]any{"ok": "wrong"}, nil
	})
	if err == nil || !errors.Is(err, ErrToolOutputInvalid) || first.Status != "failed" {
		t.Fatalf("invalid output result=%+v err=%v", first, err)
	}
	second, err := loop.Run(context.Background(), "bad-output", contract, map[string]any{"q": "ok"}, func(context.Context) (any, error) {
		calls.Add(1)
		return map[string]any{"ok": true}, nil
	})
	if err == nil || !errors.Is(err, ErrToolOutputInvalid) || !second.Replayed || calls.Load() != 1 {
		t.Fatalf("invalid output replay=%+v err=%v calls=%d", second, err, calls.Load())
	}
	state, err := j.ToolState(scope, "bad-output")
	if err != nil || !state.Failed || state.ReasonCode != "tool_output_schema_invalid" {
		t.Fatalf("failed state=%+v err=%v", state, err)
	}
}

func TestMarkToolNotStartedIsDurableTerminal(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	contract := contractForTest("queued")
	if _, err := j.AuthorizeToolContract(scope, "queued", "worker", lease.FencingToken, contract, []string{"context.read", "knowledge.read"}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.MarkToolNotStarted(scope, "queued", "worker", lease.FencingToken, "cancelled_before_dispatch"); err != nil {
		t.Fatal(err)
	}
	if _, err := j.StartToolWithContract(scope, "queued", "worker", lease.FencingToken, contract, nil, []string{"context.read", "knowledge.read"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("not-started call became runnable: %v", err)
	}
	state, err := j.ToolState(scope, "queued")
	if err != nil || !state.NotStarted || state.ReasonCode != "cancelled_before_dispatch" {
		t.Fatalf("not-started state=%+v err=%v", state, err)
	}
}

func TestToolApprovalIsImmutableAndIdempotent(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	contract := contractForTest("approval")
	first, err := j.AuthorizeToolContract(scope, "approval", "worker", lease.FencingToken, contract, []string{"context.read", "knowledge.read"})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := j.ApproveTool(scope, "approval", "worker", lease.FencingToken, "approved")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := j.ApproveTool(scope, "approval", "worker", lease.FencingToken, "approved")
	if err != nil || replay.EventID != approved.EventID {
		t.Fatalf("same approval was not idempotent: replay=%+v err=%v", replay, err)
	}
	if _, err := j.ApproveTool(scope, "approval", "worker", lease.FencingToken, "denied"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("approval decision changed after commit: %v", err)
	}
	if first.EventID == approved.EventID {
		t.Fatal("authorization and approval reused an event id")
	}
}

func TestToolStartAndNotStartedHaveOneDurableWinner(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	contract := contractForTest("race")
	for attempt := 0; attempt < 100; attempt++ {
		callID := fmt.Sprintf("race-%d", attempt)
		if _, err := j.AuthorizeToolContract(scope, callID, "worker", lease.FencingToken, contract, []string{"context.read", "knowledge.read"}); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var startErr, notStartedErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, startErr = j.StartTool(scope, callID, contract.Name, "worker", lease.FencingToken, nil)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, notStartedErr = j.MarkToolNotStarted(scope, callID, "worker", lease.FencingToken, "cancelled_before_dispatch")
		}()
		close(start)
		wg.Wait()
		state, err := j.ToolState(scope, callID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Started == state.NotStarted {
			t.Fatalf("iteration %d produced ambiguous terminal state: state=%+v startErr=%v notStartedErr=%v", attempt, state, startErr, notStartedErr)
		}
		if state.Started && !errors.Is(notStartedErr, ErrConflict) {
			t.Fatalf("iteration %d start won without rejecting not_started: startErr=%v notStartedErr=%v", attempt, startErr, notStartedErr)
		}
		if state.NotStarted && !errors.Is(startErr, ErrConflict) {
			t.Fatalf("iteration %d not_started won without rejecting start: startErr=%v notStartedErr=%v", attempt, startErr, notStartedErr)
		}
	}
}

func TestToolBatchBoundedConcurrencyExclusiveBarrierAndOrderedResults(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	loop := ToolLoop{Journal: j, Scope: scope, Owner: "worker", FencingToken: lease.FencingToken, AllowedCapabilities: []string{"context.read", "knowledge.read"}}
	var active, maxActive atomic.Int32
	var exclusiveOverlap atomic.Bool
	makeCall := func(id string, mode string, delay time.Duration) ToolBatchCall {
		contract := contractForTest(id)
		contract.ConcurrencyMode = mode
		return ToolBatchCall{CallID: id, Contract: contract, Input: nil, Execute: func(context.Context) (any, error) {
			current := active.Add(1)
			for {
				old := maxActive.Load()
				if current <= old || maxActive.CompareAndSwap(old, current) {
					break
				}
			}
			if mode == ToolExclusive && current != 1 {
				exclusiveOverlap.Store(true)
			}
			time.Sleep(delay)
			active.Add(-1)
			return id, nil
		}}
	}
	results, err := loop.RunBatch(context.Background(), []ToolBatchCall{
		makeCall("a", ToolParallelSafe, 30*time.Millisecond),
		makeCall("b", ToolParallelSafe, 5*time.Millisecond),
		makeCall("x", ToolExclusive, 2*time.Millisecond),
		makeCall("c", ToolParallelSafe, time.Millisecond),
	}, 2)
	if err != nil {
		t.Fatalf("batch error: %v", err)
	}
	if maxActive.Load() > 2 || exclusiveOverlap.Load() {
		t.Fatalf("concurrency invariant violated: max=%d exclusive_overlap=%v", maxActive.Load(), exclusiveOverlap.Load())
	}
	for index, result := range results {
		if result.Err != nil || result.Execution.Status != "finished" || fmt.Sprint(result.Execution.Output) == "" {
			t.Fatalf("result %d=%+v", index, result)
		}
		if result.CallID != []string{"a", "b", "x", "c"}[index] {
			t.Fatalf("result order=%+v", results)
		}
	}
}

func TestToolBatchCancellationMarksOnlyUnstartedCalls(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	loop := ToolLoop{Journal: j, Scope: scope, Owner: "worker", FencingToken: lease.FencingToken, AllowedCapabilities: []string{"context.read", "knowledge.read"}}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var calls atomic.Int32
	makeCall := func(id string) ToolBatchCall {
		contract := contractForTest(id)
		contract.ConcurrencyMode = ToolParallelSafe
		return ToolBatchCall{CallID: id, Contract: contract, Input: nil, Execute: func(ctx context.Context) (any, error) {
			calls.Add(1)
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}}
	}
	go func() {
		<-started
		cancel()
	}()
	results, batchErr := loop.RunBatch(ctx, []ToolBatchCall{makeCall("running"), makeCall("queued-1"), makeCall("queued-2")}, 1)
	if batchErr == nil || len(results) != 3 || calls.Load() != 1 {
		t.Fatalf("cancellation batch results=%+v err=%v calls=%d", results, batchErr, calls.Load())
	}
	if results[0].Execution.Status != "outcome_unknown" {
		t.Fatalf("started call was not preserved as unknown: %+v", results[0])
	}
	for _, result := range results[1:] {
		if result.Execution.Status != "not_started" || result.Execution.Reason != "batch_aborted" {
			t.Fatalf("queued call status=%+v", result)
		}
		state, stateErr := j.ToolState(scope, result.CallID)
		if stateErr != nil || !state.NotStarted || state.Started {
			t.Fatalf("queued call state=%+v err=%v", state, stateErr)
		}
	}
}
