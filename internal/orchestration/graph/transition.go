package graph

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
	ErrRetryBackoff        = errors.New("retry backoff is still active")
)

type NodeProjection struct {
	NodeID         string        `json:"node_id"`
	Status         AttemptStatus `json:"status"`
	CurrentAttempt string        `json:"current_attempt,omitempty"`
	AttemptNo      int           `json:"attempt_no,omitempty"`
	RetryAt        *time.Time    `json:"retry_at,omitempty"`
	RetryCount     int           `json:"retry_count,omitempty"`
	// ReadyEdgeIDs records the committed graph decisions that opened this node.
	// It prevents a pending repair target or verification node from being
	// consumed merely because it was also an entry node or became ready through
	// an unrelated edge.
	ReadyEdgeIDs          []string       `json:"ready_edge_ids,omitempty"`
	AdmissionState        AdmissionState `json:"admission_state,omitempty"`
	AdmissionReason       string         `json:"admission_reason,omitempty"`
	ResourceReservationID string         `json:"resource_reservation_id,omitempty"`
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
	RepairPlans     map[string]RepairPlan     `json:"repair_plans,omitempty"`
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
	if p.RepairPlans == nil {
		p.RepairPlans = map[string]RepairPlan{}
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
		if a.RepairState != "" && !validRepairLifecycle(a.RepairState) {
			return fmt.Errorf("attempt %s has invalid repair state %q", id, a.RepairState)
		}
	}
	for id, repair := range p.RepairPlans {
		if repair.ID != id || repair.PlanID != p.PlanID || repair.RepairNodeID == "" || repair.RepairAttemptID == "" || repair.TargetNodeID == "" || len(repair.VerificationNodeIDs) == 0 || repair.MaxRounds < 1 || repair.Round < 1 || !validRepairLifecycle(repair.State) {
			return fmt.Errorf("invalid repair plan %s", id)
		}
		if repair.Round > repair.MaxRounds {
			return fmt.Errorf("repair plan %s exceeds max rounds", id)
		}
		seenVerification := make(map[string]struct{}, len(repair.VerificationNodeIDs))
		for _, nodeID := range repair.VerificationNodeIDs {
			if strings.TrimSpace(nodeID) == "" {
				return fmt.Errorf("repair plan %s has an empty verification node", id)
			}
			if _, exists := seenVerification[nodeID]; exists {
				return fmt.Errorf("repair plan %s has duplicate verification node %s", id, nodeID)
			}
			seenVerification[nodeID] = struct{}{}
		}
		if len(repair.RepairAttemptIDs) == 0 || !Contains(repair.RepairAttemptIDs, repair.RepairAttemptID) {
			return fmt.Errorf("repair plan %s has incomplete attempt lineage", id)
		}
		repairAttempt, ok := p.Attempts[repair.RepairAttemptID]
		if !ok || repairAttempt.NodeID != repair.RepairNodeID || repairAttempt.Status != AttemptPassed {
			return fmt.Errorf("repair plan %s has no passed planning attempt", id)
		}
		if len(repair.StateHistory) == 0 || repair.StateHistory[0] != RepairPlanned || repair.StateHistory[len(repair.StateHistory)-1] != repair.State {
			return fmt.Errorf("repair plan %s has incomplete state history", id)
		}
		for index := 1; index < len(repair.StateHistory); index++ {
			if !repairLifecycleTransitionAllowed(repair.StateHistory[index-1], repair.StateHistory[index]) {
				return fmt.Errorf("repair plan %s has invalid state transition %s -> %s", id, repair.StateHistory[index-1], repair.StateHistory[index])
			}
		}
		if repair.State == RepairDispatched || repair.State == RepairPatched || repair.State == RepairVerifying || repair.State == RepairVerified {
			if repair.TargetAttemptID == "" {
				return fmt.Errorf("repair plan %s is %s without a target attempt", id, repair.State)
			}
			target, ok := p.Attempts[repair.TargetAttemptID]
			if !ok || target.NodeID != repair.TargetNodeID {
				return fmt.Errorf("repair plan %s points to an invalid target attempt", id)
			}
			if repair.State == RepairDispatched && target.Status != AttemptRunning {
				return fmt.Errorf("repair plan %s is dispatched without a running target attempt", id)
			}
			if repair.State == RepairPatched || repair.State == RepairVerifying || repair.State == RepairVerified {
				if target.Status != AttemptPassed {
					return fmt.Errorf("repair plan %s has state %s without a passed target attempt", id, repair.State)
				}
			}
		}
		if repair.State == RepairVerifying || repair.State == RepairVerified {
			if len(repair.VerificationAttempts) == 0 {
				return fmt.Errorf("repair plan %s is %s without verification attempts", id, repair.State)
			}
			for nodeID, attemptID := range repair.VerificationAttempts {
				if _, declared := seenVerification[nodeID]; !declared || strings.TrimSpace(attemptID) == "" {
					return fmt.Errorf("repair plan %s has an undeclared verification attempt for %s", id, nodeID)
				}
				verification, ok := p.Attempts[attemptID]
				if !ok || verification.NodeID != nodeID {
					return fmt.Errorf("repair plan %s has an invalid verification attempt for %s", id, nodeID)
				}
				if repair.State == RepairVerifying && verification.Status != AttemptRunning && verification.Status != AttemptPassed {
					return fmt.Errorf("repair plan %s is verifying with non-running verification attempt for %s", id, nodeID)
				}
			}
		}
		if repair.State == RepairVerified {
			if repair.TargetAttemptID == "" {
				return fmt.Errorf("repair plan %s is verified without target patch attempt", id)
			}
			if len(repair.VerificationAttempts) < len(repair.VerificationNodeIDs) {
				return fmt.Errorf("repair plan %s is verified without all verification attempts", id)
			}
			for _, nodeID := range repair.VerificationNodeIDs {
				if !repair.VerifiedNodes[nodeID] {
					return fmt.Errorf("repair plan %s is verified without verification result for %s", id, nodeID)
				}
				verification, ok := p.Attempts[repair.VerificationAttempts[nodeID]]
				if !ok || verification.NodeID != nodeID || verification.Status != AttemptPassed {
					return fmt.Errorf("repair plan %s is verified without a passed verification attempt for %s", id, nodeID)
				}
			}
		}
	}
	if p.Status == PlanTerminal && p.TerminalOutcome == "succeeded" && !allRepairsVerified(p) {
		return errors.New("terminal success requires every repair plan to be verified")
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
	PlanRevision          int64
	AttemptID             string
	LeaseToken            int64
	Event                 string
	Result                StructuredResult
	Failure               *FailureReason
	OutputArtifacts       []string
	IdempotencyKey        string
	PayloadHash           string
	ResourceReservationID string
	Now                   time.Time
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
	p := PlanProjection{PlanID: plan.ID, Revision: plan.Revision, Status: plan.Status, Nodes: map[string]NodeProjection{}, Attempts: map[string]NodeAttempt{}, Traversals: map[string]int{}, RepairPlans: map[string]RepairPlan{}, Idempotency: map[string]string{}}
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
	if p.RepairPlans == nil {
		p.RepairPlans = map[string]RepairPlan{}
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
	a := NodeAttempt{ID: attemptID, PlanID: plan.ID, NodeID: nodeID, AttemptNo: attemptNo, Lease: lease, IdempotencyKey: input.IdempotencyKey, InputManifest: in, Status: AttemptRunning, StartedAt: &now, ParentAttemptID: parentAttempt, RetryOf: parentAttempt, ResourceReservationID: strings.TrimSpace(input.ResourceReservationID), RepairState: repairLifecycleAtStart(*p, plan.GraphSnapshot, nodeID)}
	if repairID, state := repairPlanForDispatch(*p, plan.GraphSnapshot, nodeID); repairID != "" {
		a.RepairPlanID = repairID
		a.RepairState = state
	}
	if nodeDefinition.Kind == NodeRepair {
		a.RepairState = RepairPlanned
	}
	p.Attempts[a.ID] = a
	if a.RepairPlanID != "" {
		repair := p.RepairPlans[a.RepairPlanID]
		switch a.RepairState {
		case RepairDispatched:
			repair.TargetAttemptID = a.ID
			if !setRepairState(&repair, RepairDispatched) {
				delete(p.Attempts, a.ID)
				if input.IdempotencyKey != "" {
					delete(p.Idempotency, input.IdempotencyKey)
				}
				return NodeAttempt{}, fmt.Errorf("%w: repair %s cannot transition to dispatched", ErrInvalidTransition, repair.ID)
			}
		case RepairVerifying:
			if repair.VerificationAttempts == nil {
				repair.VerificationAttempts = map[string]string{}
			}
			repair.VerificationAttempts[nodeID] = a.ID
			if !setRepairState(&repair, RepairVerifying) {
				delete(repair.VerificationAttempts, nodeID)
				delete(p.Attempts, a.ID)
				if input.IdempotencyKey != "" {
					delete(p.Idempotency, input.IdempotencyKey)
				}
				return NodeAttempt{}, fmt.Errorf("%w: repair %s cannot transition to verifying", ErrInvalidTransition, repair.ID)
			}
		}
		repair.LastReasonCode = string(a.RepairState)
		p.RepairPlans[a.RepairPlanID] = repair
	}
	n.Status = AttemptRunning
	n.CurrentAttempt = a.ID
	n.AttemptNo = attemptNo
	n.RetryAt = nil
	n.ReadyEdgeIDs = nil
	p.Nodes[nodeID] = n
	p.Status = PlanRunning
	p.TokenUsage += in.Manifest.TokenEstimate
	return a, nil
}

// FinishAttempt only accepts the current attempt and its fencing token. A
// late completion can therefore never overwrite a newer repair attempt.
func (p *PlanProjection) FinishAttempt(plan RequirementExecutionPlan, attemptID string, input TransitionInput) (NodeAttempt, error) {
	if p == nil {
		return NodeAttempt{}, errors.New("nil projection")
	}
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
	outputTokens, outputTools, outputCost := ResultUsage(input.Result)
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
		inTokens, inTools, inCost := ResultUsage(existing.Result)
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
	a.OutputArtifacts = appendUnique(a.OutputArtifacts, input.OutputArtifacts...)
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
	if nodeDefinition.Kind == NodeRepair {
		if a.Status == AttemptPassed {
			// The graph validator and controller both support the ergonomic form
			// where a Repair node has exactly one success edge to its patch target.
			// Do not reintroduce a second, stricter contract here: requiring the
			// raw field would make a validated derived-target repair impossible to
			// finish and would leave the state machine stuck before planning.
			target := strings.TrimSpace(nodeDefinition.RepairPolicy.TargetNodeID)
			if target == "" {
				target = resolveRepairTarget(plan.GraphSnapshot, nodeDefinition)
			}
			if target == "" || len(repairVerificationNodes(a)) == 0 {
				return NodeAttempt{}, fmt.Errorf("%w: repair target and verification contract are required", ErrInvalidTransition)
			}
			a.RepairState = RepairPlanned
			if repair, err := createRepairPlan(p, plan, a); err != nil {
				return NodeAttempt{}, err
			} else {
				// A feedback loop revisits the same logical Repair node. Reuse its
				// durable contract so a second bug does not leave an older `planned`
				// plan competing for the next target dispatch. Attempts remain
				// immutable; only the active projection contract is refreshed.
				for id, existing := range p.RepairPlans {
					if existing.RepairNodeID != a.NodeID || existing.State == RepairVerified || existing.State == RepairExhausted {
						continue
					}
					repair.ID = id
					repair.Round = existing.Round
					repair.State = existing.State
					repair.StateHistory = append([]RepairLifecycle(nil), existing.StateHistory...)
					repair.RepairAttemptIDs = appendUnique(existing.RepairAttemptIDs, a.ID)
					if len(repair.StateHistory) == 0 {
						repair.StateHistory = []RepairLifecycle{repair.State}
					}
					p.RepairPlans[id] = repair
					a.RepairPlanID = id
					break
				}
				if a.RepairPlanID == "" {
					p.RepairPlans[repair.ID] = repair
					a.RepairPlanID = repair.ID
				}
			}
		} else if a.Status == AttemptFailed || a.Status == AttemptTimedOut {
			a.RepairState = RepairExhausted
		}
	} else if a.RepairState != "" {
		state, stateErr := advanceRepairAttemptState(p, a)
		if stateErr != nil {
			return NodeAttempt{}, stateErr
		}
		a.RepairState = state
	}
	if a.RepairState != "" {
		if a.Result.Fields == nil {
			a.Result.Fields = map[string]any{}
		} else {
			fields := make(map[string]any)
			for key, value := range a.Result.Fields {
				fields[key] = value
			}
			a.Result.Fields = fields
		}
		a.Result.Fields["repair_state"] = string(a.RepairState)
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
	previousRepairPlans := CloneProjection(*p).RepairPlans
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
				if edge.From == a.NodeID && edge.To == candidate.ID && EdgeSatisfied(edge, a) {
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
			p.RepairPlans = previousRepairPlans
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
	if a.RepairPlanID != "" {
		if repair, ok := p.RepairPlans[a.RepairPlanID]; ok {
			repair.LastReasonCode = a.Result.ReasonCode
			p.RepairPlans[a.RepairPlanID] = repair
		}
	}
	return a, nil
}

// repairLifecycleAtStart links target and verification attempts to the latest
// immutable repair plan without mutating the historical repair attempt.
func CloneProjection(p PlanProjection) PlanProjection {
	cp := p
	cp.Nodes = map[string]NodeProjection{}
	for k, v := range p.Nodes {
		v.ReadyEdgeIDs = append([]string(nil), v.ReadyEdgeIDs...)
		cp.Nodes[k] = v
	}
	cp.Attempts = map[string]NodeAttempt{}
	for k, v := range p.Attempts {
		cp.Attempts[k] = v
	}
	cp.Traversals = map[string]int{}
	for k, v := range p.Traversals {
		cp.Traversals[k] = v
	}
	cp.Idempotency = map[string]string{}
	for k, v := range p.Idempotency {
		cp.Idempotency[k] = v
	}
	cp.Decisions = append([]FeedbackDecision(nil), p.Decisions...)
	cp.RepairPlans = map[string]RepairPlan{}
	for id, repair := range p.RepairPlans {
		repair.VerificationNodeIDs = append([]string(nil), repair.VerificationNodeIDs...)
		repair.StateHistory = append([]RepairLifecycle(nil), repair.StateHistory...)
		repair.RepairAttemptIDs = append([]string(nil), repair.RepairAttemptIDs...)
		repair.SourceAttemptIDs = append([]string(nil), repair.SourceAttemptIDs...)
		repair.Scope = append([]string(nil), repair.Scope...)
		repair.PatchArtifactIDs = append([]string(nil), repair.PatchArtifactIDs...)
		repair.VerificationArtifactIDs = append([]string(nil), repair.VerificationArtifactIDs...)
		verificationAttempts := repair.VerificationAttempts
		repair.VerificationAttempts = map[string]string{}
		for nodeID, attemptID := range verificationAttempts {
			repair.VerificationAttempts[nodeID] = attemptID
		}
		verifiedNodes := repair.VerifiedNodes
		repair.VerifiedNodes = map[string]bool{}
		for nodeID, verified := range verifiedNodes {
			repair.VerifiedNodes[nodeID] = verified
		}
		cp.RepairPlans[id] = repair
	}
	return cp
}
