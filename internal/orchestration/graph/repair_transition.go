package graph

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

func repairLifecycleAtStart(projection PlanProjection, graph WorkflowGraph, nodeID string) RepairLifecycle {
	if repairID, state := repairPlanForDispatch(projection, graph, nodeID); repairID != "" {
		return state
	}
	return ""
}

func validRepairLifecycle(state RepairLifecycle) bool {
	switch state {
	case RepairPlanned, RepairDispatched, RepairPatched, RepairVerifying, RepairVerified, RepairFailed, RepairExhausted:
		return true
	default:
		return false
	}
}

func repairPlanForDispatch(projection PlanProjection, graph WorkflowGraph, nodeID string) (string, RepairLifecycle) {
	ids := make([]string, 0, len(projection.RepairPlans))
	for id := range projection.RepairPlans {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		repair := projection.RepairPlans[id]
		targetRetry := repair.TargetNodeID == nodeID && repair.State == RepairDispatched && retryableRepairProviderAttempt(projection, repair.TargetAttemptID)
		if repair.TargetNodeID == nodeID && ((repair.State == RepairPlanned && repair.TargetAttemptID == "") || targetRetry) && (targetRetry || repairReadyFrom(projection, graph, nodeID, repair.RepairNodeID, repair.ID, false)) {
			return id, RepairDispatched
		}
		verificationAttemptID := ""
		if repair.VerificationAttempts != nil {
			verificationAttemptID = repair.VerificationAttempts[nodeID]
		}
		verificationRetry := Contains(repair.VerificationNodeIDs, nodeID) && (repair.State == RepairPatched || repair.State == RepairVerifying) && repair.TargetAttemptID != "" && retryableRepairProviderAttempt(projection, verificationAttemptID)
		if Contains(repair.VerificationNodeIDs, nodeID) && (repair.State == RepairPatched || repair.State == RepairVerifying) && repair.TargetAttemptID != "" && repairReadyFrom(projection, graph, nodeID, repair.TargetNodeID, repair.ID, true) {
			if verificationAttemptID == "" || verificationRetry {
				return id, RepairVerifying
			}
		}
		if verificationRetry {
			return id, RepairVerifying
		}
	}
	return "", ""
}

// A transient provider failure should consume the node retry budget, not a
// repair round. The latter represents a new semantic patch/verification
// cycle; advancing it for an unavailable upstream provider can strand the
// repair plan in a live-but-unverifiable state.
func IsRetryableRepairProviderFailure(attempt NodeAttempt) bool {
	if (attempt.Status != AttemptFailed && attempt.Status != AttemptTimedOut) || attempt.FailureReason == nil || !attempt.FailureReason.Retryable {
		return false
	}
	switch attempt.FailureReason.Code {
	case "provider_failed", "provider_timeout", "provider_result_missing", "provider_tool_evidence_missing", "upstream_error", "timeout", "lease_expired":
		return true
	default:
		return false
	}
}

func retryableRepairProviderAttempt(projection PlanProjection, attemptID string) bool {
	if attemptID == "" {
		return false
	}
	attempt, ok := projection.Attempts[attemptID]
	return ok && IsRetryableRepairProviderFailure(attempt)
}

// repairReadyFrom proves that the node was opened by the repair contract's
// committed edge. The virtual marker is used only when a bounded retry round
// reopens the target after a failed patch; ordinary dispatches must name an
// actual success edge in the frozen graph.
func repairReadyFrom(projection PlanProjection, graph WorkflowGraph, nodeID, from, repairID string, verification bool) bool {
	node, ok := projection.Nodes[nodeID]
	if !ok {
		return false
	}
	virtual := "repair:" + repairID + ":target"
	if !verification && Contains(node.ReadyEdgeIDs, virtual) {
		return true
	}
	for _, readyID := range node.ReadyEdgeIDs {
		for _, edge := range graph.Edges {
			if edge.ID == readyID && edge.From == from && edge.To == nodeID && edge.On == EdgeSuccess {
				return true
			}
		}
	}
	if verification {
		// Verification may be a chain (target -> unit -> QA), not only a
		// direct fan-out from the patched target. The node still must have a
		// committed ready edge, and the frozen graph must prove that the edge's
		// source lies on a success-only path from the target. This prevents an
		// unrelated entry or feedback edge from satisfying the verification
		// contract while allowing ordinary graph composition between checks.
		for _, readyID := range node.ReadyEdgeIDs {
			for _, edge := range graph.Edges {
				if edge.ID != readyID || edge.To != nodeID || edge.On != EdgeSuccess {
					continue
				}
				if repairPathExists(graph, from, edge.From, EdgeSuccess) {
					return true
				}
			}
		}
	}
	return false
}

func createRepairPlan(projection *PlanProjection, plan RequirementExecutionPlan, attempt NodeAttempt) (RepairPlan, error) {
	if projection == nil {
		return RepairPlan{}, errors.New("nil projection")
	}
	fields := attempt.Result.Fields
	target, _ := fields["target_node_id"].(string)
	target = strings.TrimSpace(target)
	verification := repairVerificationNodes(attempt)
	maxRounds := intValue(fields["max_rounds"])
	if target == "" || len(verification) == 0 || maxRounds < 1 {
		return RepairPlan{}, fmt.Errorf("%w: repair decision is incomplete", ErrInvalidTransition)
	}
	if _, ok := projection.Nodes[target]; !ok {
		return RepairPlan{}, fmt.Errorf("%w: repair target %s is unknown", ErrInvalidTransition, target)
	}
	for _, id := range verification {
		if _, ok := projection.Nodes[id]; !ok {
			return RepairPlan{}, fmt.Errorf("%w: repair verification node %s is unknown", ErrInvalidTransition, id)
		}
	}
	if !repairPathExists(plan.GraphSnapshot, attempt.NodeID, target, EdgeSuccess) {
		return RepairPlan{}, fmt.Errorf("%w: repair target %s is not on a success edge", ErrInvalidTransition, target)
	}
	for _, id := range verification {
		if !repairPathExists(plan.GraphSnapshot, target, id, EdgeSuccess) {
			return RepairPlan{}, fmt.Errorf("%w: verification node %s is not reachable from target", ErrInvalidTransition, id)
		}
	}
	return RepairPlan{ID: plan.ID + ":repair:" + attempt.ID, PlanID: plan.ID, RepairNodeID: attempt.NodeID, RepairAttemptID: attempt.ID, RepairAttemptIDs: []string{attempt.ID}, TargetNodeID: target, VerificationNodeIDs: append([]string(nil), verification...), SourceAttemptIDs: stringSliceValue(fields["source_attempt_ids"]), Scope: stringSliceValue(fields["scope"]), MaxRounds: maxRounds, Round: 1, State: RepairPlanned, StateHistory: []RepairLifecycle{RepairPlanned}, VerifiedNodes: map[string]bool{}}, nil
}

func advanceRepairAttemptState(projection *PlanProjection, attempt NodeAttempt) (RepairLifecycle, error) {
	if projection == nil || attempt.RepairPlanID == "" {
		return attempt.RepairState, nil
	}
	repair, ok := projection.RepairPlans[attempt.RepairPlanID]
	if !ok {
		return attempt.RepairState, fmt.Errorf("%w: repair plan %s is missing", ErrInvalidTransition, attempt.RepairPlanID)
	}
	if IsRetryableRepairProviderFailure(attempt) {
		// Keep the active repair lifecycle in place. The ordinary node retry path
		// will reopen this exact target or verification attempt with new lineage.
		projection.RepairPlans[repair.ID] = repair
		return attempt.RepairState, nil
	}
	switch attempt.RepairState {
	case RepairDispatched:
		if attempt.Status == AttemptPassed {
			if !setRepairState(&repair, RepairPatched) {
				return attempt.RepairState, fmt.Errorf("%w: repair %s cannot transition to patched", ErrInvalidTransition, repair.ID)
			}
			repair.PatchArtifactIDs = appendUnique(repair.PatchArtifactIDs, attempt.OutputArtifacts...)
			projection.RepairPlans[repair.ID] = repair
			return RepairPatched, nil
		}
		if attempt.Status == AttemptFailed || attempt.Status == AttemptTimedOut || attempt.Status == AttemptCancelled {
			state := RepairFailed
			if !setRepairState(&repair, RepairFailed) {
				return attempt.RepairState, fmt.Errorf("%w: repair %s cannot transition to failed", ErrInvalidTransition, repair.ID)
			}
			if repair.Round < repair.MaxRounds {
				repair.Round++
				repair.TargetAttemptID = ""
				repair.VerificationAttempts = map[string]string{}
				repair.VerifiedNodes = map[string]bool{}
				resetRepairRound(projection, repair)
				if !setRepairState(&repair, RepairPlanned) {
					return attempt.RepairState, fmt.Errorf("%w: repair %s cannot reopen after failure", ErrInvalidTransition, repair.ID)
				}
			} else {
				if !setRepairState(&repair, RepairExhausted) {
					return attempt.RepairState, fmt.Errorf("%w: repair %s cannot transition to exhausted", ErrInvalidTransition, repair.ID)
				}
				state = RepairExhausted
			}
			projection.RepairPlans[repair.ID] = repair
			return state, nil
		}
	case RepairVerifying:
		if attempt.Status == AttemptPassed {
			if repair.VerifiedNodes == nil {
				repair.VerifiedNodes = map[string]bool{}
			}
			repair.VerifiedNodes[attempt.NodeID] = true
			repair.VerificationArtifactIDs = appendUnique(repair.VerificationArtifactIDs, attempt.OutputArtifacts...)
			if len(repair.VerifiedNodes) == len(repair.VerificationNodeIDs) {
				if !setRepairState(&repair, RepairVerified) {
					return attempt.RepairState, fmt.Errorf("%w: repair %s cannot transition to verified", ErrInvalidTransition, repair.ID)
				}
			} else {
				if !setRepairState(&repair, RepairVerifying) {
					return attempt.RepairState, fmt.Errorf("%w: repair %s cannot remain verifying", ErrInvalidTransition, repair.ID)
				}
			}
			projection.RepairPlans[repair.ID] = repair
			if repair.State == RepairVerified {
				return RepairVerified, nil
			}
			return RepairVerifying, nil
		}
		if attempt.Status == AttemptFailed || attempt.Status == AttemptTimedOut || attempt.Status == AttemptCancelled {
			state := RepairFailed
			if !setRepairState(&repair, RepairFailed) {
				return attempt.RepairState, fmt.Errorf("%w: repair %s cannot transition to failed", ErrInvalidTransition, repair.ID)
			}
			if repair.Round < repair.MaxRounds {
				repair.Round++
				repair.TargetAttemptID = ""
				repair.VerificationAttempts = map[string]string{}
				repair.VerifiedNodes = map[string]bool{}
				resetRepairRound(projection, repair)
				if !setRepairState(&repair, RepairPlanned) {
					return attempt.RepairState, fmt.Errorf("%w: repair %s cannot reopen after verification failure", ErrInvalidTransition, repair.ID)
				}
			} else {
				if !setRepairState(&repair, RepairExhausted) {
					return attempt.RepairState, fmt.Errorf("%w: repair %s cannot transition to exhausted", ErrInvalidTransition, repair.ID)
				}
				state = RepairExhausted
			}
			projection.RepairPlans[repair.ID] = repair
			return state, nil
		}
	}
	return attempt.RepairState, nil
}

func repairLifecycleTransitionAllowed(from, to RepairLifecycle) bool {
	if from == to {
		return true
	}
	switch from {
	case RepairPlanned:
		return to == RepairDispatched || to == RepairFailed || to == RepairExhausted
	case RepairDispatched:
		return to == RepairPatched || to == RepairFailed || to == RepairExhausted
	case RepairPatched:
		return to == RepairVerifying || to == RepairFailed || to == RepairExhausted
	case RepairVerifying:
		return to == RepairVerified || to == RepairFailed || to == RepairExhausted
	case RepairFailed:
		return to == RepairPlanned || to == RepairExhausted
	case RepairVerified, RepairExhausted:
		return false
	default:
		return false
	}
}

func setRepairState(repair *RepairPlan, next RepairLifecycle) bool {
	if repair == nil || !validRepairLifecycle(next) {
		return false
	}
	if repair.State == "" {
		repair.State = next
		repair.StateHistory = append(repair.StateHistory, next)
		return true
	}
	if !repairLifecycleTransitionAllowed(repair.State, next) {
		return false
	}
	if len(repair.StateHistory) == 0 {
		repair.StateHistory = []RepairLifecycle{repair.State}
	}
	if repair.StateHistory[len(repair.StateHistory)-1] != repair.State {
		repair.StateHistory = append(repair.StateHistory, repair.State)
	}
	if repair.State != next {
		repair.State = next
		repair.StateHistory = append(repair.StateHistory, next)
	}
	return true
}

// resetRepairRound reopens the target and clears all verification nodes. The
// historical attempts stay immutable; only the node readiness projection is
// changed so the next scheduler tick must create a new attempt for the round.
func resetRepairRound(projection *PlanProjection, repair RepairPlan) {
	if projection == nil {
		return
	}
	for _, nodeID := range append([]string{repair.TargetNodeID}, repair.VerificationNodeIDs...) {
		node, ok := projection.Nodes[nodeID]
		if !ok || node.Status == AttemptRunning {
			continue
		}
		node.RetryAt = nil
		if nodeID == repair.TargetNodeID {
			node.Status = AttemptReady
			node.ReadyEdgeIDs = []string{"repair:" + repair.ID + ":target"}
		} else {
			node.Status = AttemptPending
			node.ReadyEdgeIDs = nil
		}
		projection.Nodes[nodeID] = node
	}
}

func repairPathExists(graph WorkflowGraph, from, to string, on EdgeEvent) bool {
	if from == to {
		return true
	}
	queue := []string{from}
	seen := map[string]bool{from: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range graph.Edges {
			if edge.From != current || edge.On != on || edge.MaxTraversals == 0 && edge.LoopGroup != "" {
				continue
			}
			if edge.To == to {
				return true
			}
			if !seen[edge.To] {
				seen[edge.To] = true
				queue = append(queue, edge.To)
			}
		}
	}
	return false
}

func stringSliceValue(value any) []string {
	if values, ok := value.([]string); ok {
		return append([]string(nil), values...)
	}
	values, _ := value.([]any)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if item, ok := value.(string); ok && strings.TrimSpace(item) != "" {
			result = append(result, item)
		}
	}
	return result
}

func intValue(value any) int {
	switch n := value.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func appendUnique(values []string, additions ...string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)
	for _, value := range append(append([]string(nil), values...), additions...) {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func repairVerificationNodes(attempt NodeAttempt) []string {
	if attempt.Result.Fields == nil {
		return nil
	}
	if values, ok := attempt.Result.Fields["verification_node_ids"].([]string); ok {
		return values
	}
	values, _ := attempt.Result.Fields["verification_node_ids"].([]any)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if item, ok := value.(string); ok {
			result = append(result, item)
		}
	}
	return result
}
