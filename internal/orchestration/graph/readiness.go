package graph

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// EdgeSatisfied mirrors the reducer's edge event and predicate semantics for
// join readiness. A branch only counts once its current attempt has produced
// the event type represented by the incoming edge and committed evidence.
func EdgeSatisfied(edge WorkflowEdge, attempt NodeAttempt) bool {
	event := ""
	switch attempt.Status {
	case AttemptPassed:
		event = "success"
	case AttemptFailed:
		if strings.EqualFold(attempt.Result.Outcome, "bug") {
			event = "bug"
		} else {
			event = "failure"
		}
	case AttemptTimedOut:
		event = "timeout"
	case AttemptCancelled:
		event = "cancel"
	case AttemptWaiting:
		event = "waiting"
	}
	if !eventMatches(edge.On, attempt.Status, event, attempt.Result.Outcome) {
		return false
	}
	matched, err := EvaluatePredicate(edge.Predicate, resultFields(attempt.Result))
	if err != nil || !matched {
		return false
	}
	for _, required := range edge.RequiredEvidence {
		if !Contains(attempt.Result.EvidenceIDs, required) {
			return false
		}
	}
	return true
}

// Retry marks a failed/timed-out node ready for a new immutable attempt. It
// is deliberately explicit so callers cannot jump over configured feedback
// edges or mutate a historical attempt.
func (p *PlanProjection) Retry(plan RequirementExecutionPlan, nodeID string) error {
	n, ok := p.Nodes[strings.TrimSpace(nodeID)]
	if !ok {
		return fmt.Errorf("unknown node %q", nodeID)
	}
	if n.Status != AttemptFailed && n.Status != AttemptTimedOut {
		return ErrInvalidTransition
	}
	n.Status = AttemptReady
	p.Nodes[nodeID] = n
	if p.Status == PlanTerminal {
		p.Status = PlanRunning
	}
	return nil
}

func (p *PlanProjection) Cancel(plan RequirementExecutionPlan, attemptID string, leaseToken int64, reason string) (NodeAttempt, error) {
	return p.FinishAttempt(plan, attemptID, TransitionInput{PlanRevision: plan.Revision, AttemptID: attemptID, LeaseToken: leaseToken, Event: "cancel", Result: StructuredResult{Outcome: "cancelled", Summary: reason, EvidenceIDs: []string{"cancel:" + attemptID}}, Failure: &FailureReason{Code: "cancelled", Message: reason}})
}

func (p *PlanProjection) Timeout(plan RequirementExecutionPlan, attemptID string, leaseToken int64, reason string) (NodeAttempt, error) {
	return p.FinishAttempt(plan, attemptID, TransitionInput{PlanRevision: plan.Revision, AttemptID: attemptID, LeaseToken: leaseToken, Event: "timeout", Result: StructuredResult{Outcome: "timeout", Summary: reason, EvidenceIDs: []string{"timeout:" + attemptID}}, Failure: &FailureReason{Code: "timeout", Message: reason, Retryable: true}})
}

// TakeOver expires a running attempt and opens its configured timeout/repair
// route. A takeover always changes the fencing boundary; the previous worker
// can no longer commit a late result.
func (p *PlanProjection) TakeOver(plan RequirementExecutionPlan, attemptID, owner string, now time.Time) error {
	a, ok := p.Attempts[attemptID]
	if !ok {
		return ErrStaleAttempt
	}
	if a.Status != AttemptRunning {
		return ErrInvalidTransition
	}
	if owner == "" {
		return errors.New("takeover owner is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	a.Lease.Owner = owner
	a.Lease.FencingToken++
	a.Lease.ExpiresAt = now.Add(15 * time.Minute)
	p.Attempts[attemptID] = a
	// Finishing as timeout routes through the graph and records lineage; the
	// new attempt receives a separate ID when the scheduler ticks again.
	_, err := p.FinishAttempt(plan, attemptID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: a.Lease.FencingToken, Event: "timeout", Result: StructuredResult{Outcome: "timeout", Summary: "human takeover", EvidenceIDs: []string{"human-takeover:" + attemptID}}, Failure: &FailureReason{Code: "human_takeover", Message: "attempt taken over", Retryable: true}, Now: now})
	return err
}
