package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ToolExecuteFunc is the only provider-specific portion of a tool turn. The
// loop owns all durable authorization and effect fencing around this callback.
type ToolExecuteFunc func(context.Context) (any, error)

// ToolExecution is the replayable result of a typed tool turn. Output is
// intentionally returned to the caller but is not copied into diagnostics by
// the loop; the Journal only records lifecycle facts and hashes.
type ToolExecution struct {
	CallID   string   `json:"call_id"`
	Attempt  int      `json:"attempt"`
	Status   string   `json:"status"`
	Reason   string   `json:"reason,omitempty"`
	Replayed bool     `json:"replayed,omitempty"`
	Output   any      `json:"output,omitempty"`
	EventIDs []string `json:"event_ids,omitempty"`
}

// ToolLoop is the provider-neutral authorize -> approve -> start -> fence ->
// execute -> finish loop. A caller supplies a leased Journal and an explicit
// allow-list; an empty allow-list remains deny-by-default.
type ToolLoop struct {
	Journal      *Journal
	Scope        Scope
	Owner        string
	FencingToken int64
	AllowedTools []string
}

// Run executes a tool with bounded retries. Every retry gets a new immutable
// call ID linked by RetryTool. A duplicate effect fence is returned as a
// replay without invoking the callback again, which closes the lost-response
// window around external side effects.
func (l ToolLoop) Run(ctx context.Context, callID string, contract ToolContract, input any, execute ToolExecuteFunc) (ToolExecution, error) {
	if l.Journal == nil {
		return ToolExecution{}, errors.New("journal is required")
	}
	if !l.Scope.valid() {
		return ToolExecution{}, errors.New("scope is required")
	}
	if strings.TrimSpace(callID) == "" || execute == nil {
		return ToolExecution{}, errors.New("call_id and execute callback are required")
	}
	if strings.TrimSpace(l.Owner) == "" || l.FencingToken <= 0 {
		return ToolExecution{}, ErrLeaseLost
	}
	if strings.TrimSpace(contract.Name) == "" {
		return ToolExecution{}, errors.New("tool contract name is required")
	}
	if contract.MaxRetries < 0 {
		return ToolExecution{}, errors.New("tool max_retries cannot be negative")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	result := ToolExecution{CallID: callID, Attempt: 1, Status: "pending"}
	for attempt := 1; attempt <= contract.MaxRetries+1; attempt++ {
		currentID := callID
		if attempt > 1 {
			currentID = fmt.Sprintf("%s:retry:%d", callID, attempt-1)
		}
		result.CallID, result.Attempt = currentID, attempt

		authorized, err := l.Journal.AuthorizeTool(l.Scope, currentID, contract.Name, l.Owner, l.FencingToken, l.AllowedTools)
		if err != nil {
			result.Status = "blocked"
			result.Reason = "authorization_denied"
			return result, err
		}
		result.EventIDs = append(result.EventIDs, authorized.EventID)
		state, stateErr := l.Journal.ToolState(l.Scope, currentID)
		if stateErr != nil {
			return result, stateErr
		}
		if state.Finished {
			// A caller retrying after a lost response must not attempt to start a
			// completed call again. The effect receipt is the durable result.
			result.Status = "replayed"
			result.Replayed = true
			result.Reason = "tool_already_finished"
			return result, nil
		}
		if contract.RequiresApproval && state.Approved == nil {
			result.Status = "waiting"
			result.Reason = "approval_required"
			return result, ErrApprovalRequired
		}
		if _, err := l.Journal.StartTool(l.Scope, currentID, contract.Name, l.Owner, l.FencingToken, input); err != nil {
			result.Status = "blocked"
			if errors.Is(err, ErrUnauthorized) {
				result.Reason = "approval_denied"
			}
			return result, err
		} else {
			if started, startedErr := l.Journal.ToolState(l.Scope, currentID); startedErr == nil && started.LastEventID != "" {
				result.EventIDs = append(result.EventIDs, started.LastEventID)
			}
		}

		fence, created, err := l.Journal.FenceEffect(l.Scope, "tool-effect:"+currentID, l.Owner, l.FencingToken, map[string]any{"call_id": currentID, "tool": contract.Name})
		if err != nil {
			result.Status = "blocked"
			result.Reason = "effect_fence_failed"
			return result, err
		}
		result.EventIDs = append(result.EventIDs, fence.EventID)
		if !created {
			result.Status = "replayed"
			result.Replayed = true
			result.Reason = "effect_already_committed"
			return result, nil
		}

		execCtx := ctx
		cancel := func() {}
		if contract.Timeout > 0 {
			execCtx, cancel = context.WithTimeout(ctx, contract.Timeout)
		}
		output, execErr := execute(execCtx)
		timedOut := errors.Is(execErr, context.DeadlineExceeded) || errors.Is(execCtx.Err(), context.DeadlineExceeded)
		cancel()
		if execErr == nil {
			finished, finishErr := l.Journal.FinishTool(l.Scope, currentID, l.Owner, l.FencingToken, output)
			if finishErr != nil {
				result.Status = "blocked"
				result.Reason = "finish_commit_failed"
				return result, finishErr
			}
			result.Status, result.Output = "finished", output
			result.EventIDs = append(result.EventIDs, finished.EventID)
			return result, nil
		}
		if timedOut {
			_, _ = l.Journal.CancelTool(l.Scope, currentID, l.Owner, l.FencingToken, "tool_timeout")
			result.Status, result.Reason = "timed_out", "tool_timeout"
			return result, execErr
		}

		// Persist a generic failure fact without copying provider error text into
		// the journal. The caller still receives the original error for handling.
		failed, failErr := l.Journal.Append(Input{EventType: EventToolFailed, AggregateType: "tool", AggregateID: currentID, Scope: l.Scope, IdempotencyKey: "tool:" + currentID + ":failure", WriterID: l.Owner, FencingToken: l.FencingToken, Status: StatusRejected, Payload: map[string]any{"reason_code": "tool_execution_failed"}})
		if failErr != nil {
			result.Status = "blocked"
			result.Reason = "failure_commit_failed"
			return result, failErr
		}
		result.EventIDs = append(result.EventIDs, failed.EventID)
		if ctx.Err() != nil {
			_, _ = l.Journal.CancelTool(l.Scope, currentID, l.Owner, l.FencingToken, "context_cancelled")
			result.Status, result.Reason = "cancelled", "context_cancelled"
			return result, ctx.Err()
		}
		if attempt <= contract.MaxRetries {
			if _, retryEvent, retryErr := l.Journal.RetryTool(l.Scope, currentID, l.Owner, l.FencingToken, attempt, "tool_execution_failed"); retryErr != nil {
				result.Status, result.Reason = "blocked", "retry_commit_failed"
				return result, retryErr
			} else {
				result.EventIDs = append(result.EventIDs, retryEvent.EventID)
			}
			continue
		}
		result.Status, result.Reason = "failed", "tool_execution_failed"
		return result, fmt.Errorf("%w: %v", ErrToolExecution, execErr)
	}
	return result, ErrToolExecution
}
