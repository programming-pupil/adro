package runtime

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestToolLoopRequiresApprovalAndFailsClosedOnDenial(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	loop := ToolLoop{Journal: j, Scope: scope, Owner: "worker", FencingToken: lease.FencingToken, AllowedTools: []string{"deploy"}}
	_, err = loop.Run(context.Background(), "call-approval", ToolContract{Name: "deploy", SideEffectClass: EffectNonRetriableWrite, ReconcilePolicy: ReconcileHuman, RequiresApproval: true}, nil, func(context.Context) (any, error) {
		t.Fatal("tool executed before approval")
		return nil, nil
	})
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected approval required, got %v", err)
	}
	if _, err := j.ApproveTool(scope, "call-approval", "worker", lease.FencingToken, "denied"); err != nil {
		t.Fatal(err)
	}
	_, err = loop.Run(context.Background(), "call-approval", ToolContract{Name: "deploy", SideEffectClass: EffectNonRetriableWrite, ReconcilePolicy: ReconcileHuman, RequiresApproval: true}, nil, func(context.Context) (any, error) {
		t.Fatal("denied tool executed")
		return nil, nil
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected denial, got %v", err)
	}
}

func TestToolLoopReceiptPreventsDuplicateCallback(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	loop := ToolLoop{Journal: j, Scope: scope, Owner: "worker", FencingToken: lease.FencingToken, AllowedTools: []string{"search"}}
	var calls atomic.Int32
	execute := func(context.Context) (any, error) {
		calls.Add(1)
		return map[string]any{"ok": true}, nil
	}
	first, err := loop.Run(context.Background(), "call-fence", ToolContract{Name: "search", SideEffectClass: EffectReadOnly, ReconcilePolicy: ReconcileNone}, map[string]any{"q": "x"}, execute)
	if err != nil || first.Status != "finished" {
		t.Fatalf("first execution=%+v err=%v", first, err)
	}
	second, err := loop.Run(context.Background(), "call-fence", ToolContract{Name: "search", SideEffectClass: EffectReadOnly, ReconcilePolicy: ReconcileNone}, map[string]any{"q": "x"}, execute)
	if err != nil || !second.Replayed || second.Status != "replayed" {
		t.Fatalf("replayed execution=%+v err=%v", second, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("effect callback invoked %d times", calls.Load())
	}
}

func TestToolLoopRetriesAndPreservesLineage(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	loop := ToolLoop{Journal: j, Scope: scope, Owner: "worker", FencingToken: lease.FencingToken, AllowedTools: []string{"flaky"}}
	var calls atomic.Int32
	result, err := loop.Run(context.Background(), "call-retry", ToolContract{Name: "flaky", SideEffectClass: EffectReadOnly, ReconcilePolicy: ReconcileNone, MaxRetries: 1}, nil, func(context.Context) (any, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("transient")
		}
		return "ok", nil
	})
	if err != nil || result.Status != "finished" || result.Attempt != 2 {
		t.Fatalf("retry result=%+v err=%v", result, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected two attempts, got %d", calls.Load())
	}
	state, err := j.ToolState(scope, "call-retry:retry:1")
	if err != nil || !state.Finished {
		t.Fatalf("retry tool state=%+v err=%v", state, err)
	}
	if err := j.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestToolLoopAppliesTimeoutAndCancelsTool(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	loop := ToolLoop{Journal: j, Scope: scope, Owner: "worker", FencingToken: lease.FencingToken, AllowedTools: []string{"slow"}}
	result, err := loop.Run(context.Background(), "call-timeout", ToolContract{Name: "slow", SideEffectClass: EffectReadOnly, ReconcilePolicy: ReconcileNone, Timeout: 5 * time.Millisecond}, nil, func(ctx context.Context) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) || result.Status != "timed_out" || result.Reason != "tool_timeout" {
		t.Fatalf("timeout result=%+v err=%v", result, err)
	}
	effect, err := j.EffectState(scope, "tool-effect:call-timeout")
	if err != nil || !effect.OutcomeUnknown {
		t.Fatalf("timeout effect state=%+v err=%v", effect, err)
	}
}

func TestToolLoopDoesNotReplayWriteAfterUnknownOutcome(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	loop := ToolLoop{Journal: j, Scope: scope, Owner: "worker", FencingToken: lease.FencingToken, AllowedTools: []string{"write"}}
	var calls atomic.Int32
	result, err := loop.Run(context.Background(), "call-write", ToolContract{Name: "write", SideEffectClass: EffectNonRetriableWrite, ReconcilePolicy: ReconcileHuman}, map[string]any{"value": 1}, func(context.Context) (any, error) {
		calls.Add(1)
		return nil, errors.New("connection lost after dispatch")
	})
	if !errors.Is(err, ErrEffectOutcomeUnknown) || result.Status != "outcome_unknown" {
		t.Fatalf("first result=%+v err=%v", result, err)
	}
	result, err = loop.Run(context.Background(), "call-write", ToolContract{Name: "write", SideEffectClass: EffectNonRetriableWrite, ReconcilePolicy: ReconcileHuman}, map[string]any{"value": 1}, func(context.Context) (any, error) {
		calls.Add(1)
		return "unexpected", nil
	})
	if !errors.Is(err, ErrEffectOutcomeUnknown) || result.Status != "outcome_unknown" {
		t.Fatalf("recovery result=%+v err=%v", result, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("write callback invoked %d times", calls.Load())
	}
}

func TestToolLoopReconcilesUnknownOutcomeWithoutReDispatch(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	loop := ToolLoop{Journal: j, Scope: scope, Owner: "worker", FencingToken: lease.FencingToken, AllowedTools: []string{"write"}}
	contract := ToolContract{Name: "write", SideEffectClass: EffectReconcilableWrite, ReconcilePolicy: ReconcileQuery}
	var calls atomic.Int32
	first, err := loop.Run(context.Background(), "call-reconcile", contract, map[string]any{"value": 1}, func(context.Context) (any, error) {
		calls.Add(1)
		return nil, errors.New("response lost")
	})
	if !errors.Is(err, ErrEffectOutcomeUnknown) || first.Status != "outcome_unknown" {
		t.Fatalf("first result=%+v err=%v", first, err)
	}
	if _, err := j.ReconcileEffect(scope, "tool-effect:call-reconcile", "call-reconcile", "worker", lease.FencingToken, "confirmed", map[string]any{"remote_id": "r-1"}); err != nil {
		t.Fatal(err)
	}
	second, err := loop.Run(context.Background(), "call-reconcile", contract, map[string]any{"value": 1}, func(context.Context) (any, error) {
		calls.Add(1)
		return "must-not-dispatch", nil
	})
	if err != nil || second.Status != "reconciled" || !second.Replayed || calls.Load() != 1 {
		t.Fatalf("reconciled result=%+v calls=%d err=%v", second, calls.Load(), err)
	}
	state, err := j.EffectState(scope, "tool-effect:call-reconcile")
	if err != nil || !state.Reconciled || state.OutcomeUnknown || state.ReconcileDecision != "confirmed" {
		t.Fatalf("reconciled effect state=%+v err=%v", state, err)
	}
	if _, err := j.ReconcileEffect(scope, "tool-effect:call-reconcile", "call-reconcile", "worker", lease.FencingToken, "not_found", nil); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed reconciliation unexpectedly succeeded: %v", err)
	}
}

func TestToolLoopRequiresExplicitWriteReconcilePolicy(t *testing.T) {
	j := mustJournal(t, "")
	scope := testScope()
	lease, err := j.AcquireLease(scope, "worker", time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	loop := ToolLoop{Journal: j, Scope: scope, Owner: "worker", FencingToken: lease.FencingToken, AllowedTools: []string{"write"}}
	_, err = loop.Run(context.Background(), "call-policy", ToolContract{Name: "write", SideEffectClass: EffectNonRetriableWrite}, nil, func(context.Context) (any, error) {
		t.Fatal("tool executed without a reconciliation policy")
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "reconcile_policy") {
		t.Fatalf("missing policy error=%v", err)
	}
}
