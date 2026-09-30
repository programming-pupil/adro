package orchestration

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adro-project/adro/core/budget"
	"github.com/adro-project/adro/internal/harness"
	graphmodel "github.com/adro-project/adro/internal/orchestration/graph"
)

// ReadyNodes computes a deterministic ready queue from the graph snapshot and
// projection. It is safe to call repeatedly after a crash because it derives
// state from attempts rather than maintaining a hidden queue.
func ReadyNodes(plan graphmodel.RequirementExecutionPlan, projection graphmodel.PlanProjection) []graphmodel.WorkflowNode {
	return readyNodesAt(plan, projection, time.Now().UTC())
}

// ReadyNodesAt is the clock-injected readiness calculation used by workers and
// tests. Retry backoff is part of readiness, so callers must use a consistent
// time source when replaying a plan.
func ReadyNodesAt(plan graphmodel.RequirementExecutionPlan, projection graphmodel.PlanProjection, now time.Time) []graphmodel.WorkflowNode {
	return readyNodesAt(plan, projection, now)
}

func readyNodesAt(plan graphmodel.RequirementExecutionPlan, projection graphmodel.PlanProjection, now time.Time) []graphmodel.WorkflowNode {
	nodes := make(map[string]graphmodel.WorkflowNode, len(plan.GraphSnapshot.Nodes))
	for _, n := range plan.GraphSnapshot.Nodes {
		nodes[n.ID] = n
	}
	incoming := make(map[string][]graphmodel.WorkflowEdge)
	for _, e := range plan.GraphSnapshot.Edges {
		incoming[e.To] = append(incoming[e.To], e)
	}
	ready := make([]graphmodel.WorkflowNode, 0)
	for id, n := range nodes {
		state := projection.Nodes[id]
		if state.Status != graphmodel.AttemptReady {
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
			if source.Status == graphmodel.AttemptFailed || source.Status == graphmodel.AttemptTimedOut || source.Status == graphmodel.AttemptCancelled {
				failed++
			}
			if source.CurrentAttempt == "" {
				continue
			}
			attempt, ok := projection.Attempts[source.CurrentAttempt]
			if !ok {
				continue
			}
			if graphmodel.EdgeSatisfied(e, attempt) {
				passed++
				if n.JoinFailurePolicy == "short_circuit" && attempt.Status != graphmodel.AttemptPassed && e.On != graphmodel.EdgeSuccess {
					shortCircuit = true
				}
			}
		}
		need := 1
		switch n.JoinPolicy {
		case graphmodel.JoinAll:
			need = len(edges)
		case graphmodel.JoinQuorum:
			need = n.JoinQuorum
			if need <= 0 {
				need = len(edges)/2 + 1
			}
		case graphmodel.JoinFirstSuccess:
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

type SchedulerConfig struct {
	MaxConcurrent              int
	LeaseTTL                   time.Duration
	ReservationTTL             time.Duration
	TenantID                   string
	DefaultPriority            int
	EmergencyPriorityThreshold int
	Now                        func() time.Time
}

// Scheduler is a deterministic worker facade. It derives readiness from the
// projection on every tick, applies capacity/deadline gates, and delegates
// provider calls only after the attempt.started intent has been committed.
type Scheduler struct {
	Repository Repository
	Executor   Executor
	Admission  *AdmissionController
	Config     SchedulerConfig
}

type ScheduleReport struct {
	Started    []graphmodel.NodeAttempt     `json:"started,omitempty"`
	Advanced   []graphmodel.NodeAttempt     `json:"advanced,omitempty"`
	Waiting    []string                     `json:"waiting,omitempty"`
	Blocked    map[string]string            `json:"blocked,omitempty"`
	Admissions map[string]AdmissionDecision `json:"admissions,omitempty"`
	Terminal   bool                         `json:"terminal"`
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
func (s Scheduler) Tick(ctx context.Context, plan graphmodel.RequirementExecutionPlan, projection *graphmodel.PlanProjection, envelope harness.ContextEnvelope, workItemID, agentBindingID string) (ScheduleReport, error) {
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
			if attempt.Status == graphmodel.AttemptRunning {
				attemptIDs = append(attemptIDs, id)
			}
		}
		sort.Strings(attemptIDs)
		for _, attemptID := range attemptIDs {
			attempt := projection.Attempts[attemptID]
			finished, err := executor.FinishAttempt(ctx, plan, projection, attemptID, graphmodel.TransitionInput{
				PlanRevision: plan.Revision,
				AttemptID:    attemptID,
				LeaseToken:   attempt.Lease.FencingToken,
				Event:        "timeout",
				Result:       graphmodel.StructuredResult{Outcome: "timeout", Summary: "plan deadline exceeded", EvidenceIDs: []string{"deadline:" + plan.ID + ":" + attemptID}},
				Failure:      &graphmodel.FailureReason{Code: "plan_deadline_exceeded", Message: "plan deadline exceeded", Retryable: false},
				Now:          now,
			})
			if err != nil {
				return report, err
			}
			if s.Admission != nil {
				if settleErr := settleAttemptReservation(s.Admission.Ledger, plan, finished, nil, budget.ResourceVector{}, true, now); settleErr != nil {
					return report, settleErr
				}
			}
			report.Advanced = append(report.Advanced, finished)
		}
		if projection.Status != graphmodel.PlanTerminal {
			projection.Status = graphmodel.PlanTerminal
			projection.TerminalOutcome = "timed_out"
			if s.Repository != nil {
				if err := s.Repository.SaveProjection(*projection); err != nil {
					return report, err
				}
			}
		}
		report.Terminal = true
		return report, graphmodel.ErrDeadlineExceeded
	}
	ready := ReadyNodesAt(plan, *projection, s.now())
	if len(ready) == 0 {
		return ScheduleReport{Terminal: projection.Status == graphmodel.PlanTerminal}, nil
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
		if attempt.Status == graphmodel.AttemptRunning {
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
		if node.Kind != graphmodel.NodeAgent && node.Kind != graphmodel.NodeSquad {
			report.Waiting = append(report.Waiting, node.ID)
		}
	}
	// A zero available capacity means every configured permit is currently
	// held by a running attempt. DispatchReadyLimited treats zero as the
	// legacy "unbounded" value, so return before calling it or we would violate
	// the scheduler's concurrency gate.
	if limit == 0 {
		for _, node := range ready {
			if node.Kind == graphmodel.NodeAgent || node.Kind == graphmodel.NodeSquad {
				report.Blocked[node.ID] = "concurrency_limit"
			}
		}
		if len(report.Blocked) == 0 {
			report.Blocked = nil
		}
		report.Terminal = projection.Status == graphmodel.PlanTerminal
		return report, nil
	}
	executor := s.Executor
	executor.Repository = s.Repository
	executor.Now = s.Config.Now
	executor.LeaseTTL = s.Config.LeaseTTL
	var started []graphmodel.NodeAttempt
	var err error
	if s.Admission == nil {
		started, err = executor.DispatchReadyLimited(ctx, plan, projection, envelope, workItemID, agentBindingID, limit)
	} else {
		report.Admissions = map[string]AdmissionDecision{}
		selections := make([]DispatchSelection, 0, limit)
		for _, node := range ready {
			if node.Kind != graphmodel.NodeAgent && node.Kind != graphmodel.NodeSquad {
				continue
			}
			if len(selections) >= limit {
				report.Blocked[node.ID] = "concurrency_limit"
				continue
			}
			request := s.dispatchAdmissionRequest(plan, *projection, node, envelope, workItemID)
			decision, admissionErr := s.Admission.TryAdmit(request)
			if admissionErr != nil {
				err = admissionErr
				break
			}
			report.Admissions[node.ID] = decision
			nodeProjection := projection.Nodes[node.ID]
			nodeProjection.AdmissionState = decision.State
			nodeProjection.AdmissionReason = decision.Reason
			nodeProjection.ResourceReservationID = decision.ReservationID
			projection.Nodes[node.ID] = nodeProjection
			switch decision.State {
			case AdmissionAdmitted:
				selections = append(selections, DispatchSelection{NodeID: node.ID, ResourceReservationID: decision.ReservationID})
			case AdmissionWaiting:
				report.Blocked[node.ID] = "admission_waiting:" + decision.Reason
			case AdmissionRejected:
				report.Blocked[node.ID] = "admission_rejected:" + decision.Reason
			case AdmissionShed:
				report.Blocked[node.ID] = "admission_shed:" + decision.Reason
			}
		}
		if err == nil && len(selections) > 0 {
			started, err = executor.DispatchSelectedLimited(ctx, plan, projection, envelope, workItemID, agentBindingID, selections, limit)
		}
		if releaseErr := s.releaseUnstartedReservations(selections, started); err == nil && releaseErr != nil {
			err = releaseErr
		}
		if len(started) == 0 && len(report.Blocked) > 0 && projection.Status != graphmodel.PlanTerminal {
			projection.Status = graphmodel.PlanWaiting
			if s.Repository != nil {
				if saveErr := s.Repository.SaveProjection(*projection); err == nil && saveErr != nil {
					err = saveErr
				}
			}
		}
	}
	if err != nil {
		return report, err
	}
	report.Started = append(report.Started, started...)
	for _, node := range ready {
		if (node.Kind == graphmodel.NodeAgent || node.Kind == graphmodel.NodeSquad) && !containsAttemptNode(started, node.ID) {
			if _, explained := report.Blocked[node.ID]; !explained {
				report.Blocked[node.ID] = "concurrency_limit"
			}
		}
	}
	if report.Blocked != nil && len(report.Blocked) == 0 {
		report.Blocked = nil
	}
	report.Terminal = projection.Status == graphmodel.PlanTerminal
	return report, nil
}

func (s Scheduler) dispatchAdmissionRequest(plan graphmodel.RequirementExecutionPlan, projection graphmodel.PlanProjection, node graphmodel.WorkflowNode, envelope harness.ContextEnvelope, workItemID string) AdmissionRequest {
	now := s.now()
	attemptNo := projection.Nodes[node.ID].AttemptNo + 1
	requestID := fmt.Sprintf("%s:%s:%d", plan.ID, node.ID, attemptNo)
	tenantID := strings.TrimSpace(s.Config.TenantID)
	if tenantID == "" {
		tenantID = tenantForWorkspace(plan.WorkspaceID)
	}
	agentID := ""
	if node.AgentRef != nil {
		agentID = node.AgentRef.ID
	} else if node.SquadRef != nil {
		agentID = "squad:" + node.SquadRef.ID
	}
	requestedTokens := node.Budget.Tokens
	if envelope.Manifest.TokenEstimate > requestedTokens {
		requestedTokens = envelope.Manifest.TokenEstimate
	}
	wallTime := node.Timeout
	if wallTime <= 0 {
		wallTime = node.Budget.Duration
	}
	resources := budget.ResourceVector{Tokens: requestedTokens, ToolCalls: int64(node.Budget.ToolCalls), WallTimeNanos: int64(wallTime), ConcurrencySlots: 1}
	deadline := plan.Deadline.UTC()
	if deadline.IsZero() {
		ttl := s.Config.ReservationTTL
		if ttl <= 0 {
			ttl = s.Config.LeaseTTL
		}
		if ttl <= 0 {
			ttl = 15 * time.Minute
		}
		deadline = now.Add(ttl)
	}
	if wallTime > 0 && now.Add(wallTime).Before(deadline) {
		deadline = now.Add(wallTime)
	}
	priority := s.Config.DefaultPriority
	for _, edge := range plan.GraphSnapshot.Edges {
		if edge.To == node.ID && edge.Priority > priority {
			priority = edge.Priority
		}
	}
	cost := resources.Tokens/1000 + resources.ToolCalls + 1
	costCenter := strings.TrimSpace(workItemID)
	if costCenter == "" {
		costCenter = plan.RequirementID
	}
	return AdmissionRequest{
		ID: requestID, PlanID: plan.ID, NodeID: node.ID,
		Scope:               budget.ResourceScope{TenantID: tenantID, WorkspaceID: plan.WorkspaceID, AgentID: agentID, SessionID: envelope.Manifest.SessionID, StepID: requestID, CostCenter: costCenter},
		ParentReservationID: s.parentReservationID(plan), Resources: resources,
		Priority: priority, Emergency: s.Config.EmergencyPriorityThreshold > 0 && priority >= s.Config.EmergencyPriorityThreshold,
		Persisted: s.Repository != nil, SchedulingCost: cost, SubmittedAt: now, Deadline: deadline,
	}
}

func (s Scheduler) parentReservationID(plan graphmodel.RequirementExecutionPlan) string {
	if s.Repository == nil || strings.TrimSpace(plan.ParentPlanID) == "" || strings.TrimSpace(plan.ParentAttemptID) == "" {
		return ""
	}
	parent, err := s.Repository.GetProjection(plan.ParentPlanID)
	if err != nil {
		return ""
	}
	return parent.Attempts[plan.ParentAttemptID].ResourceReservationID
}

func (s Scheduler) releaseUnstartedReservations(selections []DispatchSelection, started []graphmodel.NodeAttempt) error {
	if s.Admission == nil || s.Admission.Ledger == nil {
		return nil
	}
	startedReservations := make(map[string]bool, len(started))
	for _, attempt := range started {
		startedReservations[attempt.ResourceReservationID] = true
	}
	for _, selection := range selections {
		if selection.ResourceReservationID == "" || startedReservations[selection.ResourceReservationID] {
			continue
		}
		if _, err := s.Admission.Ledger.Release(selection.ResourceReservationID, "dispatch-release:"+selection.ResourceReservationID, "dispatch_not_started", s.now()); err != nil && !errors.Is(err, budget.ErrResourceReservationTerminal) {
			return err
		}
	}
	return nil
}

func limitForStructural(plan graphmodel.RequirementExecutionPlan, configured int) int {
	limit := configured
	if limit <= 0 {
		limit = len(plan.GraphSnapshot.Nodes)
	}
	if plan.PolicySnapshot.Budget.Concurrent > 0 && plan.PolicySnapshot.Budget.Concurrent < limit {
		limit = plan.PolicySnapshot.Budget.Concurrent
	}
	return limit
}

func containsAttemptNode(attempts []graphmodel.NodeAttempt, nodeID string) bool {
	for _, attempt := range attempts {
		if attempt.NodeID == nodeID {
			return true
		}
	}
	return false
}
