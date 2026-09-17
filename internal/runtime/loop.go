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

// ToolLoop is the provider-neutral authorize -> approve -> intent -> dispatch
// -> receipt loop. A caller supplies a leased Journal and an explicit
// allow-list; an empty allow-list remains deny-by-default.
type ToolLoop struct {
	Journal      *Journal
	Scope        Scope
	Owner        string
	FencingToken int64
	AllowedTools []string
}

// Run executes a tool with bounded retries. Every retry gets a new immutable
// call ID linked by RetryTool. Once dispatch is durable, a missing receipt is
// treated as an unknown outcome; write effects are never replayed blindly.
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
	if !contract.SideEffectClass.valid() {
		return ToolExecution{}, errors.New("valid tool side_effect_class is required")
	}
	if contract.SideEffectClass != EffectReadOnly && contract.MaxRetries > 0 {
		return ToolExecution{}, errors.New("automatic retries are only supported for read_only tools")
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
		effectID := "tool-effect:" + currentID

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
			result.Status = "replayed"
			result.Replayed = true
			result.Reason = "tool_already_finished"
			return result, nil
		}
		effectState, stateErr := l.Journal.EffectState(l.Scope, effectID)
		if stateErr != nil {
			return result, stateErr
		}
		if effectState.Receipted {
			return result, ErrCorrupt
		}
		if effectState.Dispatched || effectState.OutcomeUnknown {
			result.Status = "outcome_unknown"
			result.Reason = "effect_outcome_unknown"
			return result, ErrEffectOutcomeUnknown
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

		intent, _, err := l.Journal.CommitEffectIntent(l.Scope, effectID, currentID, contract.Name, contract.SideEffectClass, input, l.Owner, l.FencingToken)
		if err != nil {
			result.Status = "blocked"
			result.Reason = "effect_intent_failed"
			return result, err
		}
		result.EventIDs = append(result.EventIDs, intent.EventID)
		prepared, err := l.Journal.PrepareEffectDispatch(l.Scope, effectID, l.Owner, l.FencingToken)
		if err != nil {
			result.Status = "blocked"
			result.Reason = "effect_prepare_failed"
			return result, err
		}
		result.EventIDs = append(result.EventIDs, prepared.EventID)
		dispatched, err := l.Journal.MarkEffectDispatched(l.Scope, effectID, l.Owner, l.FencingToken)
		if err != nil {
			result.Status = "blocked"
			result.Reason = "effect_dispatch_commit_failed"
			return result, err
		}
		result.EventIDs = append(result.EventIDs, dispatched.EventID)

		execCtx := ctx
		cancel := func() {}
		if contract.Timeout > 0 {
			execCtx, cancel = context.WithTimeout(ctx, contract.Timeout)
		}
		output, execErr := execute(execCtx)
		timedOut := errors.Is(execErr, context.DeadlineExceeded) || errors.Is(execCtx.Err(), context.DeadlineExceeded)
		cancel()
		if execErr == nil {
			finished, finishErr := l.Journal.CompleteToolEffect(l.Scope, effectID, currentID, l.Owner, l.FencingToken, output)
			if finishErr != nil {
				result.Status = "blocked"
				result.Reason = "receipt_commit_failed"
				return result, finishErr
			}
			result.Status, result.Output = "finished", output
			result.EventIDs = append(result.EventIDs, finished.EventID)
			return result, nil
		}
		unknown, unknownErr := l.Journal.MarkEffectOutcomeUnknown(l.Scope, effectID, map[bool]string{true: "tool_timeout", false: "tool_execution_failed"}[timedOut], l.Owner, l.FencingToken)
		if unknownErr != nil {
			result.Status = "blocked"
			result.Reason = "unknown_outcome_commit_failed"
			return result, unknownErr
		}
		result.EventIDs = append(result.EventIDs, unknown.EventID)
		if ctx.Err() != nil {
			result.Status, result.Reason = "outcome_unknown", "context_cancelled_after_dispatch"
			return result, fmt.Errorf("%w: %v", ErrEffectOutcomeUnknown, ctx.Err())
		}
		if contract.SideEffectClass == EffectReadOnly && attempt <= contract.MaxRetries {
			if _, retryEvent, retryErr := l.Journal.RetryTool(l.Scope, currentID, l.Owner, l.FencingToken, attempt, "tool_execution_failed"); retryErr != nil {
				result.Status, result.Reason = "blocked", "retry_commit_failed"
				return result, retryErr
			} else {
				result.EventIDs = append(result.EventIDs, retryEvent.EventID)
			}
			continue
		}
		if contract.SideEffectClass != EffectReadOnly {
			result.Status, result.Reason = "outcome_unknown", "effect_outcome_unknown"
			return result, fmt.Errorf("%w: %v", ErrEffectOutcomeUnknown, execErr)
		}
		if timedOut {
			result.Status, result.Reason = "timed_out", "tool_timeout"
			return result, execErr
		}
		result.Status, result.Reason = "failed", "tool_execution_failed"
		return result, fmt.Errorf("%w: %v", ErrToolExecution, execErr)
	}
	return result, ErrToolExecution
}
