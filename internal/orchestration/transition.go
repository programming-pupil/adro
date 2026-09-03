package orchestration

import (
	"crypto/sha256"
	"encoding/json"
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
	ErrRetryBackoff        = errors.New("retry backoff is still active")
)

type NodeProjection struct {
	NodeID         string        `json:"node_id"`
	Status         AttemptStatus `json:"status"`
	CurrentAttempt string        `json:"current_attempt,omitempty"`
	AttemptNo      int           `json:"attempt_no,omitempty"`
	RetryAt        *time.Time    `json:"retry_at,omitempty"`
	RetryCount     int           `json:"retry_count,omitempty"`
}
type PlanProjection struct {
	PlanID   string     `json:"plan_id"`
	Revision int64      `json:"revision"`
	Status   PlanStatus `json:"status"`
	// TerminalOutcome distinguishes a successful terminal graph from a
	// fail-closed terminal denial/cancellation. PlanStatus remains compatible
	// with existing clients while diagnostics and replay retain the outcome.
	TerminalOutcome string                    `json:"terminal_outcome,omitempty"`
	Nodes           map[string]NodeProjection `json:"nodes"`
	Attempts        map[string]NodeAttempt    `json:"attempts"`
	Traversals      map[string]int            `json:"traversals,omitempty"`
	Decisions       []FeedbackDecision        `json:"decisions,omitempty"`
	Idempotency     map[string]string         `json:"idempotency,omitempty"`
	TokenUsage      int64                     `json:"token_usage,omitempty"`
	ToolCalls       int                       `json:"tool_calls,omitempty"`
	CostCents       int64                     `json:"cost_cents,omitempty"`
}

// Validate checks projection invariants before persistence or replay.
func (p PlanProjection) Validate() error {
	if p.PlanID == "" || p.Revision < 1 {
		return errors.New("projection plan_id and revision are required")
	}
	if p.Status != PlanReady && p.Status != PlanRunning && p.Status != PlanWaiting && p.Status != PlanTerminal {
		return fmt.Errorf("invalid projection plan status %q", p.Status)
	}
	if p.TokenUsage < 0 || p.ToolCalls < 0 || p.CostCents < 0 {
		return errors.New("projection usage counters cannot be negative")
	}
	if p.Nodes == nil || p.Attempts == nil {
		return errors.New("projection nodes and attempts are required")
	}
	for id, a := range p.Attempts {
		if a.ID != id || a.PlanID != p.PlanID || a.NodeID == "" || a.AttemptNo < 1 || a.InputManifest.Manifest.SessionID == "" || a.InputManifest.Manifest.Digest == "" {
			return fmt.Errorf("invalid attempt %s", id)
		}
		switch a.Status {
		case AttemptPending, AttemptReady:
			return fmt.Errorf("attempt %s has non-running persisted status %q", id, a.Status)
		case AttemptRunning:
			if a.Lease.ExpiresAt.IsZero() || a.StartedAt == nil {
				return fmt.Errorf("running attempt %s has no lease or start time", id)
			}
		case AttemptWaiting:
			if a.FinishedAt != nil {
				return fmt.Errorf("waiting attempt %s cannot have finish time", id)
			}
		case AttemptPassed, AttemptFailed, AttemptCancelled, AttemptTimedOut:
			if a.FinishedAt == nil {
				return fmt.Errorf("terminal attempt %s has no finish time", id)
			}
		default:
			return fmt.Errorf("attempt %s has invalid status %q", id, a.Status)
		}
	}
	for id, n := range p.Nodes {
		if n.NodeID != id {
			return fmt.Errorf("invalid node projection %s", id)
		}
		switch n.Status {
		case AttemptPending, AttemptReady, AttemptRunning, AttemptWaiting, AttemptPassed, AttemptFailed, AttemptCancelled, AttemptTimedOut:
		default:
			return fmt.Errorf("node %s has invalid status %q", id, n.Status)
		}
		if n.CurrentAttempt != "" {
			a, ok := p.Attempts[n.CurrentAttempt]
			if !ok || a.NodeID != id {
				return fmt.Errorf("node %s points to unknown attempt", id)
			}
			if n.Status == AttemptRunning && a.Status != AttemptRunning {
				return fmt.Errorf("node %s is running but current attempt is %s", id, a.Status)
			}
			if n.Status == AttemptWaiting && a.Status != AttemptWaiting {
				return fmt.Errorf("node %s is waiting but current attempt is %s", id, a.Status)
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
	input.Now = now
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
	if err := in.Validate(); err != nil {
		return NodeAttempt{}, fmt.Errorf("attempt context envelope: %w", err)
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
	if n.RetryAt != nil && now.Before(*n.RetryAt) {
		return NodeAttempt{}, ErrRetryBackoff
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
	if nodeDefinition.ContextPolicy.MaxTokens > 0 && in.Manifest.TokenEstimate > nodeDefinition.ContextPolicy.MaxTokens {
		return NodeAttempt{}, fmt.Errorf("%w: node context token budget exceeded", ErrBudgetExceeded)
	}
	if nodeDefinition.Budget.Concurrent > 0 {
		running := 0
		for _, existing := range p.Attempts {
			if existing.NodeID == nodeID && existing.Status == AttemptRunning {
				running++
			}
		}
		if running >= nodeDefinition.Budget.Concurrent {
			return NodeAttempt{}, fmt.Errorf("%w: node concurrency budget exceeded", ErrBudgetExceeded)
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
	parentAttempt := n.CurrentAttempt
	a := NodeAttempt{ID: attemptID, PlanID: plan.ID, NodeID: nodeID, AttemptNo: attemptNo, Lease: lease, IdempotencyKey: input.IdempotencyKey, InputManifest: in, Status: AttemptRunning, StartedAt: &now, ParentAttemptID: parentAttempt, RetryOf: parentAttempt}
	p.Attempts[a.ID] = a
	n.Status = AttemptRunning
	n.CurrentAttempt = a.ID
	n.AttemptNo = attemptNo
	n.RetryAt = nil
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
	wasWaiting := a.Status == AttemptWaiting
	finishKey := strings.TrimSpace(input.IdempotencyKey)
	finishHash := input.PayloadHash
	if finishKey != "" && finishHash == "" {
		finishHash = transitionPayloadHash(input)
	}
	if finishKey != "" && p.Idempotency != nil {
		if previousHash, exists := p.Idempotency[finishKey]; exists {
			if previousHash != "" && previousHash != finishHash {
				return NodeAttempt{}, ErrIdempotencyConflict
			}
			if a.Status != AttemptRunning && (a.Status != AttemptWaiting || input.Event != "waiting") {
				return a, nil
			}
		}
	}
	if a.Status != AttemptRunning && !wasWaiting {
		return NodeAttempt{}, ErrInvalidTransition
	}
	if input.LeaseToken != 0 && a.Lease.FencingToken != input.LeaseToken {
		return NodeAttempt{}, ErrLeaseLost
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	input.Now = now
	// A fencing token alone is insufficient: a worker that was paused past its
	// lease must be rejected even when no newer attempt has claimed the node.
	// This closes the late-result window during provider/network partitions.
	// A server-side timeout is allowed to close an expired attempt. All other
	// late provider results remain fenced out, including a late cancel/success.
	if !wasWaiting && a.Status == AttemptRunning && !a.Lease.ExpiresAt.IsZero() && !now.Before(a.Lease.ExpiresAt) && input.Event != "timeout" {
		return NodeAttempt{}, ErrLeaseLost
	}
	outputTokens, outputTools, outputCost := resultUsage(input.Result)
	if input.Event != "waiting" && input.Event != "approval" {
		if len(input.Result.EvidenceIDs) == 0 {
			return NodeAttempt{}, ErrEvidenceRequired
		}
		for index, evidenceID := range input.Result.EvidenceIDs {
			if strings.TrimSpace(evidenceID) == "" {
				return NodeAttempt{}, fmt.Errorf("%w: evidence_ids[%d] is empty", ErrEvidenceRequired, index)
			}
		}
	}
	if plan.PolicySnapshot.Budget.Tokens > 0 && p.TokenUsage+outputTokens > plan.PolicySnapshot.Budget.Tokens {
		return NodeAttempt{}, ErrBudgetExceeded
	}
	if plan.PolicySnapshot.Budget.ToolCalls > 0 && p.ToolCalls+outputTools > plan.PolicySnapshot.Budget.ToolCalls {
		return NodeAttempt{}, ErrBudgetExceeded
	}
	if plan.PolicySnapshot.Budget.CostCents > 0 && p.CostCents+outputCost > plan.PolicySnapshot.Budget.CostCents {
		return NodeAttempt{}, ErrBudgetExceeded
	}
	var nodeTokens int64
	var nodeTools int
	var nodeCost int64
	for _, existing := range p.Attempts {
		if existing.NodeID != a.NodeID {
			continue
		}
		inTokens, inTools, inCost := resultUsage(existing.Result)
		nodeTokens += existing.InputManifest.Manifest.TokenEstimate + inTokens
		nodeTools += inTools
		nodeCost += inCost
	}
	// The current attempt's output is not in p.Attempts yet, so include it
	// explicitly before evaluating the node-local limits.
	nodeTokens += outputTokens
	nodeTools += outputTools
	nodeCost += outputCost
	var nodeDefinition WorkflowNode
	for _, candidate := range plan.GraphSnapshot.Nodes {
		if candidate.ID == a.NodeID {
			nodeDefinition = candidate
			break
		}
	}
	if nodeDefinition.Budget.Tokens > 0 && nodeTokens > nodeDefinition.Budget.Tokens {
		return NodeAttempt{}, fmt.Errorf("%w: node token budget exceeded", ErrBudgetExceeded)
	}
	if nodeDefinition.Budget.ToolCalls > 0 && nodeTools > nodeDefinition.Budget.ToolCalls {
		return NodeAttempt{}, fmt.Errorf("%w: node tool budget exceeded", ErrBudgetExceeded)
	}
	if nodeDefinition.Budget.CostCents > 0 && nodeCost > nodeDefinition.Budget.CostCents {
		return NodeAttempt{}, fmt.Errorf("%w: node cost budget exceeded", ErrBudgetExceeded)
	}
	if nodeDefinition.Budget.Duration > 0 && a.StartedAt != nil && now.Sub(*a.StartedAt) > nodeDefinition.Budget.Duration {
		return NodeAttempt{}, fmt.Errorf("%w: node duration budget exceeded", ErrBudgetExceeded)
	}
	a.Result = input.Result
	a.FailureReason = input.Failure
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
	case "waiting":
		a.Status = AttemptWaiting
	case "approval_granted":
		a.Status = AttemptPassed
	case "approval_denied":
		a.Status = AttemptFailed
	default:
		return NodeAttempt{}, fmt.Errorf("%w: unsupported event %s", ErrInvalidTransition, input.Event)
	}
	if a.Status != AttemptWaiting {
		a.FinishedAt = &now
	} else {
		a.FinishedAt = nil
	}
	previousAttempt := p.Attempts[attemptID]
	previousNode := p.Nodes[a.NodeID]
	previousStatus := p.Status
	previousTraversals := make(map[string]int, len(p.Traversals))
	for key, value := range p.Traversals {
		previousTraversals[key] = value
	}
	previousDecisionCount := len(p.Decisions)
	previousFinishHash, hadFinishHash := p.Idempotency[finishKey]
	p.Attempts[attemptID] = a
	n.Status = a.Status
	p.Nodes[a.NodeID] = n
	p.TokenUsage += outputTokens
	p.ToolCalls += outputTools
	p.CostCents += outputCost
	if finishKey != "" {
		if p.Idempotency == nil {
			p.Idempotency = map[string]string{}
		}
		p.Idempotency[finishKey] = finishHash
	}
	if a.Status == AttemptFailed || a.Status == AttemptTimedOut || a.Status == AttemptCancelled {
		for _, candidate := range plan.GraphSnapshot.Nodes {
			if candidate.JoinFailurePolicy != "short_circuit" {
				continue
			}
			for _, edge := range plan.GraphSnapshot.Edges {
				if edge.From == a.NodeID && edge.To == candidate.ID && edgeSatisfied(edge, a) {
					join := p.Nodes[candidate.ID]
					if join.Status != AttemptRunning && join.Status != AttemptPassed {
						join.Status = AttemptReady
						p.Nodes[candidate.ID] = join
					}
					break
				}
			}
		}
	}
	if a.Status == AttemptPassed || a.Status == AttemptFailed || a.Status == AttemptTimedOut || a.Status == AttemptCancelled || (a.Status == AttemptWaiting && input.Event != "waiting" && input.Event != "approval") {
		if err := p.route(plan, a, input); err != nil {
			p.Attempts[attemptID] = previousAttempt
			p.Nodes[a.NodeID] = previousNode
			p.Status = previousStatus
			p.Traversals = previousTraversals
			p.Decisions = p.Decisions[:previousDecisionCount]
			if finishKey != "" {
				if hadFinishHash {
					p.Idempotency[finishKey] = previousFinishHash
				} else {
					delete(p.Idempotency, finishKey)
				}
			}
			return NodeAttempt{}, err
		}
	}
	return a, nil
}

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
				if !contains(a.Result.EvidenceIDs, required) {
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
		if contains(plan.GraphSnapshot.ExitNodeIDs, a.NodeID) {
			if len(a.Result.EvidenceIDs) == 0 {
				return ErrEvidenceRequired
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
func resultUsage(result StructuredResult) (int64, int, int64) {
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
func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
