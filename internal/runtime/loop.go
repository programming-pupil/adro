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
	Journal             *Journal
	Scope               Scope
	Owner               string
	FencingToken        int64
	AllowedCapabilities []string
}

type preparedTool struct {
	CallID   string
	EffectID string
	Contract ToolContract
	Input    any
}

// prepareAttempt records the complete durable pre-dispatch boundary. Keeping
// this separate from callback execution lets a batch freeze and commit all
// calls in model order before any parallel callback is invoked.
func (l ToolLoop) prepareAttempt(callID string, contract ToolContract, input any, attempt int) (*preparedTool, ToolExecution, error) {
	currentID := callID
	if attempt > 1 {
		currentID = fmt.Sprintf("%s:retry:%d", callID, attempt-1)
	}
	result := ToolExecution{CallID: currentID, Attempt: attempt, Status: "pending"}
	effectID := "tool-effect:" + currentID

	authorized, err := l.Journal.AuthorizeToolContract(l.Scope, currentID, l.Owner, l.FencingToken, contract, l.AllowedCapabilities)
	if err != nil {
		result.Status = "blocked"
		result.Reason = "authorization_denied"
		return nil, result, err
	}
	result.EventIDs = append(result.EventIDs, authorized.EventID)
	effectState, err := l.Journal.EffectState(l.Scope, effectID)
	if err != nil {
		return nil, result, err
	}
	state, err := l.Journal.ToolState(l.Scope, currentID)
	if err != nil {
		return nil, result, err
	}
	if effectState.Receipted {
		if state.Finished {
			result.Status = "replayed"
			result.Replayed = true
			result.Reason = "tool_already_finished"
			return nil, result, nil
		}
		if state.Failed {
			result.Status = "failed"
			result.Replayed = true
			result.Reason = state.ReasonCode
			return nil, result, ErrToolOutputInvalid
		}
		return nil, result, ErrCorrupt
	}
	if effectState.Reconciled {
		if !state.Finished {
			return nil, result, ErrCorrupt
		}
		result.Status = "reconciled"
		result.Replayed = true
		result.Reason = "effect_reconciled"
		result.Output = effectState.ReconcileOutput
		return nil, result, nil
	}
	if state.Finished {
		result.Status = "replayed"
		result.Replayed = true
		result.Reason = "tool_already_finished"
		return nil, result, nil
	}
	if effectState.Dispatched || effectState.OutcomeUnknown {
		result.Status = "outcome_unknown"
		result.Reason = "effect_outcome_unknown"
		return nil, result, ErrEffectOutcomeUnknown
	}
	if contract.RequiresApproval && state.Approved == nil {
		result.Status = "waiting"
		result.Reason = "approval_required"
		return nil, result, ErrApprovalRequired
	}
	if _, err := l.Journal.StartTool(l.Scope, currentID, contract.Name, l.Owner, l.FencingToken, input); err != nil {
		result.Status = "blocked"
		if errors.Is(err, ErrUnauthorized) {
			result.Reason = "approval_denied"
		}
		return nil, result, err
	}
	if started, startedErr := l.Journal.ToolState(l.Scope, currentID); startedErr == nil && started.LastEventID != "" {
		result.EventIDs = append(result.EventIDs, started.LastEventID)
	}
	intent, _, err := l.Journal.CommitEffectIntentWithPolicy(l.Scope, effectID, currentID, contract.Name, contract.SideEffectClass, contract.ReconcilePolicy, input, l.Owner, l.FencingToken)
	if err != nil {
		result.Status = "blocked"
		result.Reason = "effect_intent_failed"
		return nil, result, err
	}
	result.EventIDs = append(result.EventIDs, intent.EventID)
	prepared, err := l.Journal.PrepareEffectDispatch(l.Scope, effectID, l.Owner, l.FencingToken)
	if err != nil {
		result.Status = "blocked"
		result.Reason = "effect_prepare_failed"
		return nil, result, err
	}
	result.EventIDs = append(result.EventIDs, prepared.EventID)
	dispatched, err := l.Journal.MarkEffectDispatched(l.Scope, effectID, l.Owner, l.FencingToken)
	if err != nil {
		result.Status = "blocked"
		result.Reason = "effect_dispatch_commit_failed"
		return nil, result, err
	}
	result.EventIDs = append(result.EventIDs, dispatched.EventID)
	return &preparedTool{CallID: currentID, EffectID: effectID, Contract: contract, Input: input}, result, nil
}

func (l ToolLoop) executePrepared(ctx context.Context, prepared *preparedTool, result ToolExecution, execute ToolExecuteFunc) (ToolExecution, error) {
	execCtx := ctx
	cancel := func() {}
	if prepared.Contract.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, prepared.Contract.Timeout)
	}
	output, execErr := execute(execCtx)
	timedOut := errors.Is(execErr, context.DeadlineExceeded) || errors.Is(execCtx.Err(), context.DeadlineExceeded)
	cancel()
	if execErr == nil {
		outputBytes, validationErr := validateToolOutput(prepared.Contract, output)
		if validationErr != nil {
			failed, failErr := l.Journal.FailToolEffectOutput(l.Scope, prepared.EffectID, prepared.CallID, l.Owner, l.FencingToken, payloadDigest(outputBytes))
			if failErr != nil {
				result.Status, result.Reason = "blocked", "output_failure_commit_failed"
				return result, failErr
			}
			result.EventIDs = append(result.EventIDs, failed.EventID)
			result.Status, result.Reason = "failed", "tool_output_schema_invalid"
			return result, validationErr
		}
		finished, finishErr := l.Journal.CompleteToolEffect(l.Scope, prepared.EffectID, prepared.CallID, l.Owner, l.FencingToken, output)
		if finishErr != nil {
			result.Status, result.Reason = "blocked", "receipt_commit_failed"
			return result, finishErr
		}
		result.Status, result.Output = "finished", output
		result.EventIDs = append(result.EventIDs, finished.EventID)
		return result, nil
	}
	unknown, unknownErr := l.Journal.MarkEffectOutcomeUnknown(l.Scope, prepared.EffectID, map[bool]string{true: "tool_timeout", false: "tool_execution_failed"}[timedOut], l.Owner, l.FencingToken)
	if unknownErr != nil {
		result.Status, result.Reason = "blocked", "unknown_outcome_commit_failed"
		return result, unknownErr
	}
	result.EventIDs = append(result.EventIDs, unknown.EventID)
	if ctx.Err() != nil {
		result.Status, result.Reason = "outcome_unknown", "context_cancelled_after_dispatch"
		return result, fmt.Errorf("%w: %v", ErrEffectOutcomeUnknown, ctx.Err())
	}
	if prepared.Contract.SideEffectClass != EffectReadOnly {
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
	frozen, err := FreezeToolContract(contract)
	if err != nil {
		return ToolExecution{}, err
	}
	if _, err := validateToolInput(frozen, input); err != nil {
		return ToolExecution{}, err
	}
	contract = frozen
	if ctx == nil {
		ctx = context.Background()
	}

	result := ToolExecution{CallID: callID, Attempt: 1, Status: "pending"}
	for attempt := 1; attempt <= contract.MaxRetries+1; attempt++ {
		prepared, result, err := l.prepareAttempt(callID, contract, input, attempt)
		if err != nil || prepared == nil {
			return result, err
		}
		result, err = l.executePrepared(ctx, prepared, result, execute)
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			return result, err
		}
		if contract.SideEffectClass == EffectReadOnly && attempt <= contract.MaxRetries {
			if _, retryEvent, retryErr := l.Journal.RetryTool(l.Scope, prepared.CallID, l.Owner, l.FencingToken, attempt, "tool_execution_failed"); retryErr != nil {
				result.Status, result.Reason = "blocked", "retry_commit_failed"
				return result, retryErr
			} else {
				result.EventIDs = append(result.EventIDs, retryEvent.EventID)
			}
			continue
		}
		return result, err
	}
	return result, ErrToolExecution
}
