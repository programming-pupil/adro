package orchestration

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/harness"
)

var (
	ErrStaleAttempt        = errors.New("stale attempt")
	ErrInvalidTransition   = errors.New("invalid transition")
	ErrAmbiguousTransition = errors.New("ambiguous transition")
	ErrLoopExhausted       = errors.New("loop traversal limit exhausted")
	ErrLeaseLost           = errors.New("lease or fencing token is no longer valid")
	ErrIdempotencyConflict = errors.New("idempotency key payload conflict")
	ErrEvidenceRequired    = errors.New("terminal transition requires committed evidence")
	ErrDeadlineExceeded    = errors.New("plan or node deadline exceeded")
	ErrBudgetExceeded      = errors.New("execution budget exceeded")
)

type NodeProjection struct {
	NodeID         string        `json:"node_id"`
	Status         AttemptStatus `json:"status"`
	CurrentAttempt string        `json:"current_attempt,omitempty"`
	AttemptNo      int           `json:"attempt_no,omitempty"`
}
type PlanProjection struct {
	PlanID      string                    `json:"plan_id"`
	Revision    int64                     `json:"revision"`
	Status      PlanStatus                `json:"status"`
	Nodes       map[string]NodeProjection `json:"nodes"`
	Attempts    map[string]NodeAttempt    `json:"attempts"`
	Traversals  map[string]int            `json:"traversals,omitempty"`
	Decisions   []FeedbackDecision        `json:"decisions,omitempty"`
	Idempotency map[string]string         `json:"idempotency,omitempty"`
	TokenUsage  int64                     `json:"token_usage,omitempty"`
	ToolCalls   int                       `json:"tool_calls,omitempty"`
	CostCents   int64                     `json:"cost_cents,omitempty"`
}

// Validate checks projection invariants before persistence or replay.
func (p PlanProjection) Validate() error {
	if p.PlanID == "" || p.Revision < 1 {
		return errors.New("projection plan_id and revision are required")
	}
	for id, a := range p.Attempts {
		if a.ID != id || a.PlanID != p.PlanID || a.NodeID == "" || a.AttemptNo < 1 {
			return fmt.Errorf("invalid attempt %s", id)
		}
		if a.Status == AttemptRunning && a.Lease.ExpiresAt.IsZero() {
			return fmt.Errorf("running attempt %s has no lease expiry", id)
		}
	}
	for id, n := range p.Nodes {
		if n.NodeID != id {
			return fmt.Errorf("invalid node projection %s", id)
		}
		if n.CurrentAttempt != "" {
			a, ok := p.Attempts[n.CurrentAttempt]
			if !ok || a.NodeID != id {
				return fmt.Errorf("node %s points to unknown attempt", id)
			}
		}
	}
	return nil
}

type TransitionInput struct {
	PlanRevision   int64
	AttemptID      string
	LeaseToken     int64
	Event          string
	Result         StructuredResult
	Failure        *FailureReason
	IdempotencyKey string
	PayloadHash    string
	Now            time.Time
}

func NewProjection(plan RequirementExecutionPlan) (PlanProjection, error) {
	if plan.ID == "" {
		return PlanProjection{}, errors.New("plan id is required")
	}
	if (plan.Status != PlanReady && plan.Status != PlanRunning && plan.Status != PlanWaiting && plan.Status != PlanTerminal) || plan.PlanHash == "" || plan.Revision < 1 {
		return PlanProjection{}, errors.New("projection requires a frozen execution plan")
	}
	if err := ValidateGraph(plan.GraphSnapshot); err != nil {
		return PlanProjection{}, err
	}
	p := PlanProjection{PlanID: plan.ID, Revision: plan.Revision, Status: plan.Status, Nodes: map[string]NodeProjection{}, Attempts: map[string]NodeAttempt{}, Traversals: map[string]int{}, Idempotency: map[string]string{}}
	for _, n := range plan.GraphSnapshot.Nodes {
		status := AttemptPending
		for _, entry := range plan.GraphSnapshot.EntryNodeIDs {
			if n.ID == entry {
				status = AttemptReady
			}
		}
		p.Nodes[n.ID] = NodeProjection{NodeID: n.ID, Status: status}
	}
	return p, nil
}

// StartAttempt reserves an attempt. The expected plan revision and fencing
// token are checked before any state mutation; retries with the same key are
// idempotent when their payload hash matches.
func (p *PlanProjection) StartAttempt(plan RequirementExecutionPlan, nodeID, attemptID string, attemptNo int, lease Lease, in harness.ContextEnvelope, input TransitionInput) (NodeAttempt, error) {
	if p == nil {
		return NodeAttempt{}, errors.New("nil projection")
	}
	if p.PlanID != plan.ID {
		return NodeAttempt{}, ErrInvalidTransition
	}
	if plan.Status != PlanReady && plan.Status != PlanRunning {
		return NodeAttempt{}, ErrInvalidTransition
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !plan.Deadline.IsZero() && !now.Before(plan.Deadline) {
		return NodeAttempt{}, ErrDeadlineExceeded
	}
	if plan.PolicySnapshot.Budget.Tokens > 0 && p.TokenUsage+in.Manifest.TokenEstimate > plan.PolicySnapshot.Budget.Tokens {
		return NodeAttempt{}, ErrBudgetExceeded
	}
	if plan.PolicySnapshot.Budget.Concurrent > 0 {
		running := 0
		for _, existing := range p.Attempts {
			if existing.Status == AttemptRunning {
				running++
			}
		}
		if running >= plan.PolicySnapshot.Budget.Concurrent {
			return NodeAttempt{}, ErrBudgetExceeded
		}
	}
	if strings.TrimSpace(in.Manifest.SessionID) == "" || strings.TrimSpace(in.Manifest.Digest) == "" || strings.TrimSpace(in.SelectionDigest) == "" || strings.TrimSpace(in.ReplayKey) == "" || in.Manifest.TokenBudget <= 0 || in.Manifest.TokenEstimate > in.Manifest.TokenBudget {
		return NodeAttempt{}, errors.New("attempt requires a complete context envelope")
	}
	if p.Idempotency == nil {
		p.Idempotency = map[string]string{}
	}
	if p.Attempts == nil {
		p.Attempts = map[string]NodeAttempt{}
	}
	if p.Nodes == nil {
		p.Nodes = map[string]NodeProjection{}
	}
	if input.PlanRevision != 0 && input.PlanRevision != p.Revision {
		return NodeAttempt{}, ErrStaleAttempt
	}
	if input.IdempotencyKey != "" {
		if h, ok := p.Idempotency[input.IdempotencyKey]; ok {
			if h != input.PayloadHash {
				return NodeAttempt{}, ErrIdempotencyConflict
			}
			for _, old := range p.Attempts {
				if old.IdempotencyKey == input.IdempotencyKey {
					return old, nil
				}
			}
		}
	}
	n, ok := p.Nodes[nodeID]
	if !ok {
		return NodeAttempt{}, fmt.Errorf("unknown node %q", nodeID)
	}
	var nodeDefinition WorkflowNode
	for _, candidate := range plan.GraphSnapshot.Nodes {
		if candidate.ID == nodeID {
			nodeDefinition = candidate
			break
		}
	}
	if nodeDefinition.Budget.Tokens > 0 {
		used := int64(0)
		for _, existing := range p.Attempts {
			if existing.NodeID == nodeID {
				used += existing.InputManifest.Manifest.TokenEstimate
			}
		}
		if used+in.Manifest.TokenEstimate > nodeDefinition.Budget.Tokens {
			return NodeAttempt{}, ErrBudgetExceeded
		}
	}
	if n.Status != AttemptReady && n.Status != AttemptFailed && n.Status != AttemptTimedOut {
		return NodeAttempt{}, fmt.Errorf("node %s is not ready", nodeID)
	}
	if attemptNo < 1 {
		attemptNo = n.AttemptNo + 1
	}
	if n.CurrentAttempt != "" {
		if old, ok := p.Attempts[n.CurrentAttempt]; ok && old.Status == AttemptRunning {
			return NodeAttempt{}, ErrInvalidTransition
		}
	}
	if strings.TrimSpace(attemptID) == "" {
		return NodeAttempt{}, errors.New("attempt id is required")
	}
	if existing, exists := p.Attempts[attemptID]; exists {
		if existing.IdempotencyKey == input.IdempotencyKey && input.IdempotencyKey != "" {
			return existing, nil
		}
		return NodeAttempt{}, ErrInvalidTransition
	}
	for _, existing := range p.Attempts {
		if existing.PlanID == plan.ID && existing.NodeID == nodeID && existing.AttemptNo == attemptNo {
			return NodeAttempt{}, ErrInvalidTransition
		}
	}
	if input.LeaseToken != 0 && lease.FencingToken != input.LeaseToken {
		return NodeAttempt{}, ErrLeaseLost
	}
	if !lease.ExpiresAt.IsZero() {
		now := input.Now
		if now.IsZero() {
			now = time.Now().UTC()
		}
		if !now.Before(lease.ExpiresAt) {
			return NodeAttempt{}, ErrLeaseLost
		}
	}
	if input.IdempotencyKey != "" {
		p.Idempotency[input.IdempotencyKey] = input.PayloadHash
	}
	a := NodeAttempt{ID: attemptID, PlanID: plan.ID, NodeID: nodeID, AttemptNo: attemptNo, Lease: lease, IdempotencyKey: input.IdempotencyKey, InputManifest: in, Status: AttemptRunning, StartedAt: &now}
	p.Attempts[a.ID] = a
	n.Status = AttemptRunning
	n.CurrentAttempt = a.ID
	n.AttemptNo = attemptNo
	p.Nodes[nodeID] = n
	p.Status = PlanRunning
	p.TokenUsage += in.Manifest.TokenEstimate
	return a, nil
}

// FinishAttempt only accepts the current attempt and its fencing token. A
// late completion can therefore never overwrite a newer repair attempt.
func (p *PlanProjection) FinishAttempt(plan RequirementExecutionPlan, attemptID string, input TransitionInput) (NodeAttempt, error) {
	a, ok := p.Attempts[attemptID]
	if !ok {
		return NodeAttempt{}, ErrStaleAttempt
	}
	if input.PlanRevision != 0 && input.PlanRevision != p.Revision {
		return NodeAttempt{}, ErrStaleAttempt
	}
	n, ok := p.Nodes[a.NodeID]
	if !ok || n.CurrentAttempt != attemptID {
		return NodeAttempt{}, ErrStaleAttempt
	}
	if a.Status != AttemptRunning {
		return NodeAttempt{}, ErrInvalidTransition
	}
	if input.LeaseToken != 0 && a.Lease.FencingToken != input.LeaseToken {
		return NodeAttempt{}, ErrLeaseLost
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	// A fencing token alone is insufficient: a worker that was paused past its
	// lease must be rejected even when no newer attempt has claimed the node.
	// This closes the late-result window during provider/network partitions.
	if !a.Lease.ExpiresAt.IsZero() && !now.Before(a.Lease.ExpiresAt) {
		return NodeAttempt{}, ErrLeaseLost
	}
	a.Result = input.Result
	a.FailureReason = input.Failure
	a.FinishedAt = &now
	switch input.Event {
	case "success", "passed", "pass":
		a.Status = AttemptPassed
	case "failure", "failed", "fail", "bug":
		a.Status = AttemptFailed
	case "timeout", "timed_out":
		a.Status = AttemptTimedOut
	case "cancel", "cancelled":
		a.Status = AttemptCancelled
	case "approval", "approved":
		a.Status = AttemptWaiting
	default:
		return NodeAttempt{}, fmt.Errorf("%w: unsupported event %s", ErrInvalidTransition, input.Event)
	}
	previousAttempt := p.Attempts[attemptID]
	previousNode := p.Nodes[a.NodeID]
	previousStatus := p.Status
	previousTraversals := make(map[string]int, len(p.Traversals))
	for key, value := range p.Traversals {
		previousTraversals[key] = value
	}
	previousDecisionCount := len(p.Decisions)
	p.Attempts[attemptID] = a
	n.Status = a.Status
	p.Nodes[a.NodeID] = n
	if a.Status == AttemptPassed || a.Status == AttemptFailed || a.Status == AttemptTimedOut || a.Status == AttemptCancelled || a.Status == AttemptWaiting {
		if err := p.route(plan, a, input); err != nil {
			p.Attempts[attemptID] = previousAttempt
			p.Nodes[a.NodeID] = previousNode
			p.Status = previousStatus
			p.Traversals = previousTraversals
			p.Decisions = p.Decisions[:previousDecisionCount]
			return NodeAttempt{}, err
		}
	}
	return a, nil
}

func (p *PlanProjection) route(plan RequirementExecutionPlan, a NodeAttempt, input TransitionInput) error {
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
			matched = append(matched, e)
		}
	}
	if len(matched) > 1 {
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
		if contains(plan.GraphSnapshot.ExitNodeIDs, a.NodeID) {
			if len(a.Result.EvidenceIDs) == 0 {
				return ErrEvidenceRequired
			}
			p.Status = PlanTerminal
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
		p.Nodes[e.To] = target
	}
	if input.IdempotencyKey != "" {
		p.Decisions = append(p.Decisions, FeedbackDecision{PlanID: plan.ID, SourceAttempt: a.ID, SourceNode: a.NodeID, TargetNode: e.To, EdgeID: e.ID, StructuredResult: a.Result, EvidenceIDs: a.Result.EvidenceIDs, Reason: "predicate matched", LoopCount: p.Traversals[e.ID], IdempotencyKey: input.IdempotencyKey})
	}
	return nil
}
func eventMatches(e EdgeEvent, s AttemptStatus, event, outcome string) bool {
	switch e {
	case EdgeSuccess:
		return s == AttemptPassed
	case EdgeFailure:
		return s == AttemptFailed && event != "bug" && outcome != "bug"
	case EdgeBug:
		return s == AttemptFailed && (event == "bug" || outcome == "bug")
	case EdgeTimeout:
		return s == AttemptTimedOut
	case EdgeCancel:
		return s == AttemptCancelled
	case EdgeApproval:
		return event == "approval" || outcome == "approval"
	default:
		return false
	}
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
func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
