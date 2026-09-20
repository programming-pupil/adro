package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// ToolBatchCall is one model-ordered tool request. Execute is called only
// after authorization, intent, prepare and dispatch have been durably
// recorded for this call.
type ToolBatchCall struct {
	CallID   string
	Contract ToolContract
	Input    any
	Execute  ToolExecuteFunc
}

// ToolBatchResult retains per-call errors while the enclosing RunBatch error
// provides a convenient aggregate for callers that want fail-fast handling.
// Results always use the input order, never callback completion order.
type ToolBatchResult struct {
	CallID    string
	Execution ToolExecution
	Err       error
}

type batchPreparedCall struct {
	call     ToolBatchCall
	contract ToolContract
	result   ToolExecution
	prepared bool
}

// RunBatch executes an ordered tool batch using a bounded rolling pool.
// Parallel-safe calls share a barrier and may overlap up to concurrency.
// Exclusive calls form single-call barriers: all preceding work is complete
// before the exclusive callback starts, and later calls wait for it.
func (l ToolLoop) RunBatch(ctx context.Context, calls []ToolBatchCall, concurrency int) ([]ToolBatchResult, error) {
	if concurrency < 1 {
		return nil, errors.New("batch concurrency must be positive")
	}
	if l.Journal == nil {
		return nil, errors.New("journal is required")
	}
	if !l.Scope.valid() {
		return nil, errors.New("scope is required")
	}
	if strings.TrimSpace(l.Owner) == "" || l.FencingToken <= 0 {
		return nil, ErrLeaseLost
	}
	if ctx == nil {
		ctx = context.Background()
	}
	results := make([]ToolBatchResult, len(calls))
	for index, call := range calls {
		results[index].CallID = call.CallID
		results[index].Execution.CallID = call.CallID
		results[index].Execution.Attempt = 1
		results[index].Execution.Status = "pending"
	}
	if len(calls) == 0 {
		return results, nil
	}

	seen := make(map[string]struct{}, len(calls))
	for index, call := range calls {
		if strings.TrimSpace(call.CallID) == "" || call.Execute == nil {
			return nil, fmt.Errorf("tool batch call %d requires call_id and execute callback", index)
		}
		if _, exists := seen[call.CallID]; exists {
			return nil, fmt.Errorf("duplicate tool batch call_id %q", call.CallID)
		}
		seen[call.CallID] = struct{}{}
		frozen, err := FreezeToolContract(call.Contract)
		if err != nil {
			return nil, fmt.Errorf("tool batch call %q contract: %w", call.CallID, err)
		}
		if _, err := validateToolInput(frozen, call.Input); err != nil {
			return nil, fmt.Errorf("tool batch call %q input: %w", call.CallID, err)
		}
		calls[index].Contract = frozen
	}

	var batchErrs []error
	for start := 0; start < len(calls); {
		end := start + 1
		if calls[start].Contract.ConcurrencyMode == ToolParallelSafe {
			for end < len(calls) && calls[end].Contract.ConcurrencyMode == ToolParallelSafe {
				end++
			}
		}
		group := calls[start:end]
		prepared := make([]batchPreparedCall, len(group))
		aborted := false
		for index, call := range group {
			if ctx.Err() != nil {
				aborted = true
				break
			}
			item, result, err := l.prepareBatchCall(call)
			results[start+index].Execution = result
			if err != nil {
				results[start+index].Err = err
				batchErrs = append(batchErrs, err)
				aborted = true
				break
			}
			if item == nil {
				continue
			}
			prepared[index] = *item
		}
		if aborted {
			l.markUnstarted(calls, results, start, len(calls), "batch_aborted", &batchErrs)
			break
		}

		l.runPreparedGroup(ctx, prepared, start, concurrency, results, &batchErrs)
		if groupHadError(results[start:end]) || ctx.Err() != nil {
			l.markUnstarted(calls, results, start, len(calls), "batch_aborted", &batchErrs)
			break
		}
		start = end
	}
	return results, errors.Join(batchErrs...)
}

func (l ToolLoop) prepareBatchCall(call ToolBatchCall) (*batchPreparedCall, ToolExecution, error) {
	result := ToolExecution{CallID: call.CallID, Attempt: 1, Status: "pending"}
	authorized, err := l.Journal.AuthorizeToolContract(l.Scope, call.CallID, l.Owner, l.FencingToken, call.Contract, l.AllowedCapabilities)
	if err != nil {
		result.Status, result.Reason = "blocked", "authorization_denied"
		return nil, result, err
	}
	result.EventIDs = append(result.EventIDs, authorized.EventID)
	effectID := "tool-effect:" + call.CallID
	effectState, err := l.Journal.EffectState(l.Scope, effectID)
	if err != nil {
		return nil, result, err
	}
	state, err := l.Journal.ToolState(l.Scope, call.CallID)
	if err != nil {
		return nil, result, err
	}
	if effectState.Receipted {
		if state.Finished {
			result.Status, result.Replayed, result.Reason = "replayed", true, "tool_already_finished"
			return nil, result, nil
		}
		if state.Failed {
			result.Status, result.Replayed, result.Reason = "failed", true, state.ReasonCode
			return nil, result, ErrToolOutputInvalid
		}
		return nil, result, ErrCorrupt
	}
	if effectState.Reconciled {
		if !state.Finished {
			return nil, result, ErrCorrupt
		}
		result.Status, result.Replayed, result.Reason, result.Output = "reconciled", true, "effect_reconciled", effectState.ReconcileOutput
		return nil, result, nil
	}
	if state.Finished {
		result.Status, result.Replayed, result.Reason = "replayed", true, "tool_already_finished"
		return nil, result, nil
	}
	if effectState.Dispatched || effectState.OutcomeUnknown {
		result.Status, result.Reason = "outcome_unknown", "effect_outcome_unknown"
		return nil, result, ErrEffectOutcomeUnknown
	}
	if call.Contract.RequiresApproval && state.Approved == nil {
		result.Status, result.Reason = "waiting", "approval_required"
		return nil, result, ErrApprovalRequired
	}
	if state.Approved != nil && !*state.Approved {
		result.Status, result.Reason = "blocked", "approval_denied"
		return nil, result, ErrUnauthorized
	}
	intent, _, err := l.Journal.CommitEffectIntentWithPolicy(l.Scope, effectID, call.CallID, call.Contract.Name, call.Contract.SideEffectClass, call.Contract.ReconcilePolicy, call.Input, l.Owner, l.FencingToken)
	if err != nil {
		result.Status, result.Reason = "blocked", "effect_intent_failed"
		return nil, result, err
	}
	result.EventIDs = append(result.EventIDs, intent.EventID)
	return &batchPreparedCall{call: call, contract: call.Contract, result: result, prepared: true}, result, nil
}

func (l ToolLoop) runPreparedGroup(ctx context.Context, calls []batchPreparedCall, offset, concurrency int, results []ToolBatchResult, batchErrs *[]error) {
	active := 0
	for _, call := range calls {
		if call.prepared {
			active++
		}
	}
	if active == 0 {
		return
	}
	if concurrency > active {
		concurrency = active
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	var mu sync.Mutex
	started := make([]bool, len(calls))
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				index := int(next.Add(1)) - 1
				if index >= len(calls) {
					return
				}
				if !calls[index].prepared {
					continue
				}
				if ctx.Err() != nil {
					return
				}
				prepared := &calls[index]
				result := prepared.result
				if _, err := l.Journal.StartTool(l.Scope, prepared.call.CallID, prepared.contract.Name, l.Owner, l.FencingToken, prepared.call.Input); err != nil {
					result.Status, result.Reason = "blocked", "tool_start_failed"
					mu.Lock()
					results[offset+index] = ToolBatchResult{CallID: prepared.call.CallID, Execution: result, Err: err}
					*batchErrs = append(*batchErrs, err)
					mu.Unlock()
					continue
				}
				if startedState, err := l.Journal.ToolState(l.Scope, prepared.call.CallID); err == nil && startedState.LastEventID != "" {
					result.EventIDs = append(result.EventIDs, startedState.LastEventID)
				}
				if event, err := l.Journal.PrepareEffectDispatch(l.Scope, "tool-effect:"+prepared.call.CallID, l.Owner, l.FencingToken); err != nil {
					result.Status, result.Reason = "blocked", "effect_prepare_failed"
					mu.Lock()
					results[offset+index] = ToolBatchResult{CallID: prepared.call.CallID, Execution: result, Err: err}
					*batchErrs = append(*batchErrs, err)
					mu.Unlock()
					continue
				} else {
					result.EventIDs = append(result.EventIDs, event.EventID)
				}
				if event, err := l.Journal.MarkEffectDispatchedWithTimeout(l.Scope, "tool-effect:"+prepared.call.CallID, prepared.contract.Timeout, l.Owner, l.FencingToken); err != nil {
					result.Status, result.Reason = "blocked", "effect_dispatch_commit_failed"
					mu.Lock()
					results[offset+index] = ToolBatchResult{CallID: prepared.call.CallID, Execution: result, Err: err}
					*batchErrs = append(*batchErrs, err)
					mu.Unlock()
					continue
				} else {
					result.EventIDs = append(result.EventIDs, event.EventID)
				}
				mu.Lock()
				started[index] = true
				mu.Unlock()
				execution, err := l.executePrepared(ctx, &preparedTool{CallID: prepared.call.CallID, EffectID: "tool-effect:" + prepared.call.CallID, Contract: prepared.contract, Input: prepared.call.Input}, result, prepared.call.Execute)
				mu.Lock()
				results[offset+index] = ToolBatchResult{CallID: prepared.call.CallID, Execution: execution, Err: err}
				if err != nil {
					*batchErrs = append(*batchErrs, err)
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	for index, call := range calls {
		if call.prepared && !started[index] && results[offset+index].Execution.Status == "pending" {
			results[offset+index] = ToolBatchResult{CallID: call.call.CallID, Execution: call.result}
		}
	}
}

func groupHadError(results []ToolBatchResult) bool {
	for _, result := range results {
		if result.Err != nil {
			return true
		}
	}
	return false
}

func (l ToolLoop) markUnstarted(calls []ToolBatchCall, results []ToolBatchResult, start, end int, reason string, batchErrs *[]error) {
	for index := start; index < end; index++ {
		state, stateErr := l.Journal.ToolState(l.Scope, calls[index].CallID)
		if stateErr == nil && (state.Started || state.Finished || state.Failed || state.Cancelled || state.NotStarted) {
			continue
		}
		if _, err := l.Journal.MarkToolNotStarted(l.Scope, calls[index].CallID, l.Owner, l.FencingToken, reason); err != nil && !errors.Is(err, ErrConflict) {
			*batchErrs = append(*batchErrs, err)
			continue
		}
		results[index].Execution.Status = "not_started"
		results[index].Execution.Reason = reason
	}
}
