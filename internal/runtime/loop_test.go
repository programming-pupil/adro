package runtime

import (
	"context"
	"errors"
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
	_, err = loop.Run(context.Background(), "call-approval", ToolContract{Name: "deploy", RequiresApproval: true}, nil, func(context.Context) (any, error) {
		t.Fatal("tool executed before approval")
		return nil, nil
	})
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected approval required, got %v", err)
	}
	if _, err := j.ApproveTool(scope, "call-approval", "worker", lease.FencingToken, "denied"); err != nil {
		t.Fatal(err)
	}
	_, err = loop.Run(context.Background(), "call-approval", ToolContract{Name: "deploy", RequiresApproval: true}, nil, func(context.Context) (any, error) {
		t.Fatal("denied tool executed")
		return nil, nil
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected denial, got %v", err)
	}
}

func TestToolLoopEffectFencePreventsDuplicateCallback(t *testing.T) {
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
	first, err := loop.Run(context.Background(), "call-fence", ToolContract{Name: "search"}, map[string]any{"q": "x"}, execute)
	if err != nil || first.Status != "finished" {
		t.Fatalf("first execution=%+v err=%v", first, err)
	}
	second, err := loop.Run(context.Background(), "call-fence", ToolContract{Name: "search"}, map[string]any{"q": "x"}, execute)
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
	result, err := loop.Run(context.Background(), "call-retry", ToolContract{Name: "flaky", MaxRetries: 1}, nil, func(context.Context) (any, error) {
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
	result, err := loop.Run(context.Background(), "call-timeout", ToolContract{Name: "slow", Timeout: 5 * time.Millisecond}, nil, func(ctx context.Context) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) || result.Status != "timed_out" || result.Reason != "tool_timeout" {
		t.Fatalf("timeout result=%+v err=%v", result, err)
	}
	state, err := j.ToolState(scope, "call-timeout")
	if err != nil || !state.Cancelled {
		t.Fatalf("timeout cancellation state=%+v err=%v", state, err)
	}
}
