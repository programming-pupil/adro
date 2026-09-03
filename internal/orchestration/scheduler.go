package orchestration

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/harness"
)

// ReadyNodes computes a deterministic ready queue from the graph snapshot and
// projection. It is safe to call repeatedly after a crash because it derives
// state from attempts rather than maintaining a hidden queue.
func ReadyNodes(plan RequirementExecutionPlan, projection PlanProjection) []WorkflowNode {
	return readyNodesAt(plan, projection, time.Now().UTC())
}

// ReadyNodesAt is the clock-injected readiness calculation used by workers and
// tests. Retry backoff is part of readiness, so callers must use a consistent
// time source when replaying a plan.
func ReadyNodesAt(plan RequirementExecutionPlan, projection PlanProjection, now time.Time) []WorkflowNode {
	return readyNodesAt(plan, projection, now)
}

func readyNodesAt(plan RequirementExecutionPlan, projection PlanProjection, now time.Time) []WorkflowNode {
	nodes := make(map[string]WorkflowNode, len(plan.GraphSnapshot.Nodes))
	for _, n := range plan.GraphSnapshot.Nodes {
		nodes[n.ID] = n
	}
	incoming := make(map[string][]WorkflowEdge)
	for _, e := range plan.GraphSnapshot.Edges {
		incoming[e.To] = append(incoming[e.To], e)
	}
	ready := make([]WorkflowNode, 0)
	for id, n := range nodes {
		state := projection.Nodes[id]
		if state.Status != AttemptReady {
			continue
		}
		if state.RetryAt != nil && now.Before(*state.RetryAt) {
			continue
		}
		edges := incoming[id]
		if len(edges) == 0 {
			ready = append(ready, n)
			continue
		}
		// A node is ready only after an incoming edge's source attempt has
		// committed. Pending/running sources cannot be inferred as success.
		passed := 0
		failed := 0
		shortCircuit := false
		for _, e := range edges {
			source := projection.Nodes[e.From]
			if source.Status == AttemptFailed || source.Status == AttemptTimedOut || source.Status == AttemptCancelled {
				failed++
			}
			if source.CurrentAttempt == "" {
				continue
			}
			attempt, ok := projection.Attempts[source.CurrentAttempt]
			if !ok {
				continue
			}
			if edgeSatisfied(e, attempt) {
				passed++
				if n.JoinFailurePolicy == "short_circuit" && attempt.Status != AttemptPassed && e.On != EdgeSuccess {
					shortCircuit = true
				}
			}
		}
		need := 1
		switch n.JoinPolicy {
		case JoinAll:
			need = len(edges)
		case JoinQuorum:
			need = n.JoinQuorum
			if need <= 0 {
				need = len(edges)/2 + 1
			}
		case JoinFirstSuccess:
			need = 1
		}
		if n.JoinFailurePolicy == "short_circuit" && failed > 0 && shortCircuit {
			// A merge with an explicit short-circuit policy is executable once a
			// branch has failed; the structural adapter records the failure evidence
			// and routes the configured failure edge.
			need = 0
		}
		if passed >= need {
			ready = append(ready, n)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].ID < ready[j].ID })
	return ready
}

// edgeSatisfied mirrors the reducer's edge event and predicate semantics for
// join readiness. A branch only counts once its current attempt has produced
// the event type represented by the incoming edge and committed evidence.
func edgeSatisfied(edge WorkflowEdge, attempt NodeAttempt) bool {
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
		if !contains(attempt.Result.EvidenceIDs, required) {
			return false
		}
	}
	return true
}

type SchedulerConfig struct {
	MaxConcurrent int
	LeaseTTL      time.Duration
	Now           func() time.Time
}

// Scheduler is a deterministic worker facade. It derives readiness from the
// projection on every tick, applies capacity/deadline gates, and delegates
// provider calls only after the attempt.started intent has been committed.
type Scheduler struct {
	Repository Repository
	Executor   Executor
	Config     SchedulerConfig
}

type ScheduleReport struct {
	Started  []NodeAttempt     `json:"started,omitempty"`
	Advanced []NodeAttempt     `json:"advanced,omitempty"`
	Waiting  []string          `json:"waiting,omitempty"`
	Blocked  map[string]string `json:"blocked,omitempty"`
	Terminal bool              `json:"terminal"`
}

func (s Scheduler) now() time.Time {
	if s.Config.Now != nil {
		return s.Config.Now().UTC()
	}
	return time.Now().UTC()
}

// Tick executes all currently ready agent nodes up to the configured
// concurrency limit. Non-agent nodes are surfaced as waiting because they
// require a gate/merge/human adapter rather than being silently skipped.
func (s Scheduler) Tick(ctx context.Context, plan RequirementExecutionPlan, projection *PlanProjection, envelope harness.ContextEnvelope, workItemID, agentBindingID string) (ScheduleReport, error) {
	if projection == nil {
		return ScheduleReport{}, errors.New("projection is required")
	}
	if plan.Deadline.IsZero() == false && !s.now().Before(plan.Deadline) {
		report := ScheduleReport{Blocked: map[string]string{"plan": "deadline_exceeded"}}
		now := s.now()
		executor := s.Executor
		executor.Repository = s.Repository
		executor.Now = s.Config.Now
		executor.LeaseTTL = s.Config.LeaseTTL
		// Close every active provider attempt through the normal reducer/event
		// path so late results remain fenced and replay sees the timeout evidence.
		attemptIDs := make([]string, 0)
		for id, attempt := range projection.Attempts {
			if attempt.Status == AttemptRunning {
				attemptIDs = append(attemptIDs, id)
			}
		}
		sort.Strings(attemptIDs)
		for _, attemptID := range attemptIDs {
			attempt := projection.Attempts[attemptID]
			finished, err := executor.FinishAttempt(ctx, plan, projection, attemptID, TransitionInput{
				PlanRevision: plan.Revision,
				AttemptID:    attemptID,
				LeaseToken:   attempt.Lease.FencingToken,
				Event:        "timeout",
				Result:       StructuredResult{Outcome: "timeout", Summary: "plan deadline exceeded", EvidenceIDs: []string{"deadline:" + plan.ID + ":" + attemptID}},
				Failure:      &FailureReason{Code: "plan_deadline_exceeded", Message: "plan deadline exceeded", Retryable: false},
				Now:          now,
			})
			if err != nil {
				return report, err
			}
			report.Advanced = append(report.Advanced, finished)
		}
		if projection.Status != PlanTerminal {
			projection.Status = PlanTerminal
			projection.TerminalOutcome = "timed_out"
			if s.Repository != nil {
				if err := s.Repository.SaveProjection(*projection); err != nil {
					return report, err
				}
			}
		}
		report.Terminal = true
		return report, ErrDeadlineExceeded
	}
	ready := ReadyNodesAt(plan, *projection, s.now())
	if len(ready) == 0 {
		return ScheduleReport{Terminal: projection.Status == PlanTerminal}, nil
	}
	report := ScheduleReport{Blocked: map[string]string{}}
	structural, structuralErr := s.Executor.AdvanceStructural(ctx, plan, projection, envelope, limitForStructural(plan, s.Config.MaxConcurrent))
	if structuralErr != nil {
		return report, structuralErr
	}
	report.Advanced = append(report.Advanced, structural...)
	// Structural transitions can make additional branches ready. Recompute the
	// queue before provider dispatch so a merge/gate never consumes an old view.
	ready = ReadyNodesAt(plan, *projection, s.now())
	limit := s.Config.MaxConcurrent
	if limit <= 0 {
		limit = len(ready)
	}
	running := 0
	for _, attempt := range projection.Attempts {
		if attempt.Status == AttemptRunning {
			running++
		}
	}
	if plan.PolicySnapshot.Budget.Concurrent > 0 && (limit > plan.PolicySnapshot.Budget.Concurrent) {
		limit = plan.PolicySnapshot.Budget.Concurrent
	}
	if available := limit - running; available < limit {
		limit = available
	}
	if limit < 0 {
		limit = 0
	}
	for _, node := range ready {
		if node.Kind != NodeAgent && node.Kind != NodeSquad {
			report.Waiting = append(report.Waiting, node.ID)
		}
	}
	// A zero available capacity means every configured permit is currently
	// held by a running attempt. DispatchReadyLimited treats zero as the
	// legacy "unbounded" value, so return before calling it or we would violate
	// the scheduler's concurrency gate.
	if limit == 0 {
		for _, node := range ready {
			if node.Kind == NodeAgent || node.Kind == NodeSquad {
				report.Blocked[node.ID] = "concurrency_limit"
			}
		}
		if len(report.Blocked) == 0 {
			report.Blocked = nil
		}
		report.Terminal = projection.Status == PlanTerminal
		return report, nil
	}
	executor := s.Executor
	executor.Repository = s.Repository
	executor.Now = s.Config.Now
	executor.LeaseTTL = s.Config.LeaseTTL
	started, err := executor.DispatchReadyLimited(ctx, plan, projection, envelope, workItemID, agentBindingID, limit)
	if err != nil {
		return report, err
	}
	report.Started = append(report.Started, started...)
	for _, node := range ready {
		if (node.Kind == NodeAgent || node.Kind == NodeSquad) && !containsAttemptNode(started, node.ID) {
			report.Blocked[node.ID] = "concurrency_limit"
		}
	}
	if report.Blocked != nil && len(report.Blocked) == 0 {
		report.Blocked = nil
	}
	report.Terminal = projection.Status == PlanTerminal
	return report, nil
}

func limitForStructural(plan RequirementExecutionPlan, configured int) int {
	limit := configured
	if limit <= 0 {
		limit = len(plan.GraphSnapshot.Nodes)
	}
	if plan.PolicySnapshot.Budget.Concurrent > 0 && plan.PolicySnapshot.Budget.Concurrent < limit {
		limit = plan.PolicySnapshot.Budget.Concurrent
	}
	return limit
}

func containsAttemptNode(attempts []NodeAttempt, nodeID string) bool {
	for _, attempt := range attempts {
		if attempt.NodeID == nodeID {
			return true
		}
	}
	return false
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
