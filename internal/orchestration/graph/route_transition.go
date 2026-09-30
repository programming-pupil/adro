package graph

import (
	"crypto/sha256"
	"encoding/json"

	"fmt"

	"strings"
	"time"
)

func (p *PlanProjection) route(plan RequirementExecutionPlan, a NodeAttempt, input TransitionInput) error {
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	// A plan deadline is a hard execution boundary. Once the reducer records
	// the timeout, do not follow a retry/feedback edge or leave ready nodes
	// behind for an unbounded worker loop.
	if a.Status == AttemptTimedOut && a.FailureReason != nil && a.FailureReason.Code == "plan_deadline_exceeded" {
		p.Status = PlanTerminal
		p.TerminalOutcome = "timed_out"
		return nil
	}
	var matched []WorkflowEdge
	for _, e := range plan.GraphSnapshot.Edges {
		if e.From != a.NodeID || !eventMatches(e.On, a.Status, input.Event, a.Result.Outcome) {
			continue
		}
		ok, err := EvaluatePredicate(e.Predicate, resultFields(a.Result))
		if err != nil {
			return err
		}
		if ok {
			for _, required := range e.RequiredEvidence {
				if !Contains(a.Result.EvidenceIDs, required) {
					return fmt.Errorf("edge %s requires evidence %s", e.ID, required)
				}
			}
			matched = append(matched, e)
		}
	}
	if len(matched) > 1 {
		allFanOut := true
		for _, edge := range matched {
			if !edge.FanOut {
				allFanOut = false
				break
			}
		}
		if allFanOut {
			for _, edge := range matched {
				if edge.MaxTraversals > 0 {
					p.Traversals[edge.ID]++
					if p.Traversals[edge.ID] > edge.MaxTraversals {
						return ErrLoopExhausted
					}
				}
				target := p.Nodes[edge.To]
				if target.Status != AttemptRunning {
					target.Status = AttemptReady
					target.RetryAt = nil
					target.ReadyEdgeIDs = appendUnique(target.ReadyEdgeIDs, edge.ID)
					p.Nodes[edge.To] = target
				}
				if input.IdempotencyKey != "" {
					p.Decisions = append(p.Decisions, FeedbackDecision{PlanID: plan.ID, SourceAttempt: a.ID, SourceNode: a.NodeID, TargetNode: edge.To, EdgeID: edge.ID, StructuredResult: a.Result, EvidenceIDs: a.Result.EvidenceIDs, Reason: "fan-out predicate matched", LoopCount: p.Traversals[edge.ID], IdempotencyKey: input.IdempotencyKey + ":" + edge.ID})
				}
			}
			return nil
		}
		best := matched[0]
		for _, e := range matched[1:] {
			if e.Priority > best.Priority {
				best = e
			}
		}
		count := 0
		for _, e := range matched {
			if e.Priority == best.Priority {
				count++
			}
		}
		if count > 1 {
			return ErrAmbiguousTransition
		}
		matched = []WorkflowEdge{best}
	}
	if len(matched) == 0 {
		var node WorkflowNode
		for _, candidate := range plan.GraphSnapshot.Nodes {
			if candidate.ID == a.NodeID {
				node = candidate
				break
			}
		}
		if (a.Status == AttemptFailed || a.Status == AttemptTimedOut) && a.FailureReason != nil && retryAllowed(node, a, input) {
			n := p.Nodes[a.NodeID]
			if node.RetryPolicy.MaxAttempts > a.AttemptNo && node.RetryPolicy.MaxAttempts > 1 {
				n.RetryCount++
				backoff := node.RetryPolicy.Backoff
				for i := 1; i < n.RetryCount && backoff > 0 && backoff < 24*time.Hour; i++ {
					if backoff > 12*time.Hour {
						backoff = 24 * time.Hour
						break
					}
					backoff += backoff
				}
				retryAt := now.Add(backoff)
				n.RetryAt = &retryAt
				n.Status = AttemptReady
				p.Nodes[a.NodeID] = n
				p.Status = PlanRunning
				return nil
			}
		}
		if Contains(plan.GraphSnapshot.ExitNodeIDs, a.NodeID) {
			if len(a.Result.EvidenceIDs) == 0 {
				return ErrEvidenceRequired
			}
			if !allRepairsVerified(*p) {
				// A successful exit may be observed before every declared
				// verification branch has completed, so keep the plan live in
				// that case. A failed/timed-out/cancelled exit is different: if
				// there is no matching failure edge, the graph has exhausted its
				// configured recovery path and must fail closed. Leaving it in
				// running merely because an older repair plan is still planned
				// strands the watcher forever after provider retry exhaustion.
				if a.Status == AttemptPassed && repairVerificationPending(*p) {
					// Multiple verification nodes may be exits in a fan-out graph.
					// Keep the plan live until every declared verification attempt has
					// completed instead of failing at the first passing exit.
					p.Status = PlanRunning
					return nil
				}
				// A graph may contain a shortcut edge to an exit after a patch.
				// Never let that shortcut turn a repair plan into a successful
				// terminal outcome before every declared verification node has
				// produced a passing immutable attempt.
				p.Status = PlanTerminal
				p.TerminalOutcome = "failed"
				return nil
			}
			p.Status = PlanTerminal
			switch a.Status {
			case AttemptPassed:
				p.TerminalOutcome = "succeeded"
			case AttemptCancelled:
				p.TerminalOutcome = "cancelled"
			case AttemptTimedOut:
				p.TerminalOutcome = "timed_out"
			default:
				p.TerminalOutcome = "failed"
			}
			return nil
		}
		// A terminal failure without a configured retry or failure/timeout/cancel
		// edge is a fail-closed graph outcome. Leaving the failed node in place
		// would make the worker appear healthy while the plan remains running
		// forever, with no durable route that could make progress.
		if a.Status == AttemptFailed || a.Status == AttemptTimedOut || a.Status == AttemptCancelled {
			p.Status = PlanTerminal
			switch a.Status {
			case AttemptTimedOut:
				p.TerminalOutcome = "timed_out"
			case AttemptCancelled:
				p.TerminalOutcome = "cancelled"
			default:
				p.TerminalOutcome = "failed"
			}
			return nil
		}
		return nil
	}
	e := matched[0]
	if e.MaxTraversals > 0 {
		p.Traversals[e.ID]++
		if p.Traversals[e.ID] > e.MaxTraversals {
			return ErrLoopExhausted
		}
	}
	target := p.Nodes[e.To]
	// A feedback edge deliberately re-opens its target with a new attempt. The
	// previous attempt remains immutable in Attempts, even when the node had
	// already reached a terminal status.
	if target.Status != AttemptRunning {
		target.Status = AttemptReady
		target.ReadyEdgeIDs = appendUnique(target.ReadyEdgeIDs, e.ID)
		p.Nodes[e.To] = target
	}
	if input.IdempotencyKey != "" {
		p.Decisions = append(p.Decisions, FeedbackDecision{PlanID: plan.ID, SourceAttempt: a.ID, SourceNode: a.NodeID, TargetNode: e.To, EdgeID: e.ID, StructuredResult: a.Result, EvidenceIDs: a.Result.EvidenceIDs, Reason: "predicate matched", LoopCount: p.Traversals[e.ID], IdempotencyKey: input.IdempotencyKey})
	}
	return nil
}

func allRepairsVerified(projection PlanProjection) bool {
	for _, repair := range projection.RepairPlans {
		if repair.State != RepairVerified {
			return false
		}
		if len(repair.VerificationNodeIDs) == 0 || len(repair.VerifiedNodes) < len(repair.VerificationNodeIDs) {
			return false
		}
		for _, nodeID := range repair.VerificationNodeIDs {
			if !repair.VerifiedNodes[nodeID] {
				return false
			}
		}
	}
	return true
}

func repairVerificationPending(projection PlanProjection) bool {
	for _, repair := range projection.RepairPlans {
		switch repair.State {
		case RepairPlanned, RepairDispatched, RepairPatched, RepairVerifying:
			return true
		}
	}
	return false
}

func eventMatches(e EdgeEvent, s AttemptStatus, event, outcome string) bool {
	switch e {
	case EdgeSuccess:
		return s == AttemptPassed && event != "approval_granted" && event != "approved" && outcome != "approved"
	case EdgeFailure:
		return s == AttemptFailed && event != "bug" && outcome != "bug"
	case EdgeBug:
		return s == AttemptFailed && (event == "bug" || outcome == "bug")
	case EdgeTimeout:
		return s == AttemptTimedOut
	case EdgeCancel:
		return s == AttemptCancelled
	case EdgeApproval:
		return event == "approval_granted" || event == "approved" || outcome == "approved"
	default:
		return false
	}
}

func retryAllowed(node WorkflowNode, a NodeAttempt, input TransitionInput) bool {
	if a.FailureReason == nil || !a.FailureReason.Retryable {
		return false
	}
	if len(node.RetryPolicy.RetryOn) == 0 {
		return true
	}
	values := []string{a.FailureReason.Code, input.Event, a.Result.Outcome}
	for _, configured := range node.RetryPolicy.RetryOn {
		configured = strings.TrimSpace(configured)
		for _, value := range values {
			if configured != "" && strings.EqualFold(configured, value) {
				return true
			}
		}
	}
	return false
}

func transitionPayloadHash(input TransitionInput) string {
	payload := struct {
		Event   string           `json:"event"`
		Result  StructuredResult `json:"result"`
		Failure *FailureReason   `json:"failure,omitempty"`
	}{input.Event, input.Result, input.Failure}
	b, _ := json.Marshal(payload)
	h := sha256.Sum256(b)
	return fmt.Sprintf("%x", h[:])
}

// resultUsage reads provider-neutral usage fields from a structured result.
// Providers may encode JSON numbers as int, int64 or float64; invalid or
// negative values are ignored so malformed telemetry cannot grant budget.
func ResultUsage(result StructuredResult) (int64, int, int64) {
	return usageInt64(result.Fields, "tokens"), usageInt(result.Fields, "tool_calls"), usageInt64(result.Fields, "cost_cents")
}

func usageInt64(fields map[string]any, key string) int64 {
	if fields == nil {
		return 0
	}
	var value int64
	switch n := fields[key].(type) {
	case int:
		value = int64(n)
	case int8:
		value = int64(n)
	case int16:
		value = int64(n)
	case int32:
		value = int64(n)
	case int64:
		value = n
	case uint:
		if uint64(n) <= uint64(^uint64(0)>>1) {
			value = int64(n)
		}
	case uint64:
		if n <= uint64(^uint64(0)>>1) {
			value = int64(n)
		}
	case float64:
		if n >= 0 && n <= float64(^uint64(0)>>1) && n == float64(int64(n)) {
			value = int64(n)
		}
	case float32:
		f := float64(n)
		if f >= 0 && f <= float64(^uint64(0)>>1) && f == float64(int64(f)) {
			value = int64(f)
		}
	}
	if value < 0 {
		return 0
	}
	return value
}

func usageInt(fields map[string]any, key string) int {
	value := usageInt64(fields, key)
	if value > int64(^uint(0)>>1) {
		return int(^uint(0) >> 1)
	}
	return int(value)
}

func resultFields(r StructuredResult) map[string]any {
	f := map[string]any{}
	for k, v := range r.Fields {
		f[k] = v
	}
	f["outcome"] = r.Outcome
	f["summary"] = r.Summary
	return f
}

func Contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
