package orchestration

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

type AdmissionState string

const (
	AdmissionAdmitted AdmissionState = "admitted"
	AdmissionWaiting  AdmissionState = "waiting"
	AdmissionRejected AdmissionState = "rejected"
	AdmissionShed     AdmissionState = "shed"
)

type AdmissionRequest struct {
	ID                  string         `json:"id"`
	PlanID              string         `json:"plan_id"`
	NodeID              string         `json:"node_id"`
	Scope               ResourceScope  `json:"scope"`
	ParentReservationID string         `json:"parent_reservation_id,omitempty"`
	Resources           ResourceVector `json:"resources"`
	Priority            int            `json:"priority"`
	Emergency           bool           `json:"emergency,omitempty"`
	Persisted           bool           `json:"persisted"`
	Recovery            bool           `json:"recovery,omitempty"`
	SchedulingCost      int64          `json:"scheduling_cost,omitempty"`
	SubmittedAt         time.Time      `json:"submitted_at"`
	Deadline            time.Time      `json:"deadline,omitempty"`
}

func (r AdmissionRequest) normalized() AdmissionRequest {
	r.ID = strings.TrimSpace(r.ID)
	r.PlanID = strings.TrimSpace(r.PlanID)
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.ParentReservationID = strings.TrimSpace(r.ParentReservationID)
	r.Scope = r.Scope.normalized()
	r.SubmittedAt = r.SubmittedAt.UTC()
	r.Deadline = r.Deadline.UTC()
	if r.SchedulingCost <= 0 {
		r.SchedulingCost = 1
	}
	return r
}

func (r AdmissionRequest) validate() error {
	r = r.normalized()
	if r.ID == "" || r.PlanID == "" || r.NodeID == "" {
		return errors.New("admission id, plan_id and node_id are required")
	}
	if err := r.Scope.validateHierarchy(); err != nil {
		return err
	}
	if r.Scope.WorkspaceID == "" {
		return errors.New("admission workspace_id is required")
	}
	if err := r.Resources.Validate(); err != nil {
		return err
	}
	if r.Resources.IsZero() {
		return errors.New("admission resources cannot be empty")
	}
	if r.Priority < 0 || r.SchedulingCost < 0 {
		return errors.New("admission priority and scheduling cost cannot be negative")
	}
	if r.SubmittedAt.IsZero() {
		return errors.New("admission submitted_at is required")
	}
	if !r.Deadline.IsZero() && !r.Deadline.After(r.SubmittedAt) {
		return errors.New("admission deadline must be after submitted_at")
	}
	return nil
}

type AdmissionDecision struct {
	RequestID          string                `json:"request_id"`
	State              AdmissionState        `json:"state"`
	Reason             string                `json:"reason"`
	StableKey          string                `json:"stable_key"`
	EffectivePriority  int                   `json:"effective_priority"`
	VirtualFinish      int64                 `json:"virtual_finish"`
	StarvationDeadline time.Time             `json:"starvation_deadline"`
	ReservationID      string                `json:"reservation_id,omitempty"`
	SoftActions        []string              `json:"soft_actions,omitempty"`
	LimitReports       []ResourceLimitReport `json:"limit_reports,omitempty"`
	PriorityWasCapped  bool                  `json:"priority_was_capped,omitempty"`
	OriginalPriority   int                   `json:"original_priority,omitempty"`
	ConfiguredPriority int                   `json:"configured_priority,omitempty"`
}

type FairQueuePolicy struct {
	AgingInterval          time.Duration    `json:"aging_interval"`
	MaxPriority            int              `json:"max_priority"`
	MaxPending             int              `json:"max_pending"`
	ShedThreshold          int              `json:"shed_threshold"`
	DefaultWeight          int64            `json:"default_weight"`
	TenantWeights          map[string]int64 `json:"tenant_weights,omitempty"`
	EmergencyCeilings      map[string]int   `json:"emergency_ceilings,omitempty"`
	StarvationRoundPenalty time.Duration    `json:"starvation_round_penalty"`
}

func (p FairQueuePolicy) normalized() FairQueuePolicy {
	if p.AgingInterval <= 0 {
		p.AgingInterval = time.Minute
	}
	if p.MaxPriority <= 0 {
		p.MaxPriority = 100
	}
	if p.MaxPending <= 0 {
		p.MaxPending = 10_000
	}
	if p.ShedThreshold <= 0 || p.ShedThreshold > p.MaxPending {
		p.ShedThreshold = p.MaxPending
	}
	if p.DefaultWeight <= 0 {
		p.DefaultWeight = 1
	}
	if p.StarvationRoundPenalty <= 0 {
		p.StarvationRoundPenalty = p.AgingInterval
	}
	if p.TenantWeights == nil {
		p.TenantWeights = map[string]int64{}
	}
	if p.EmergencyCeilings == nil {
		p.EmergencyCeilings = map[string]int{}
	}
	return p
}

func (p FairQueuePolicy) validate() error {
	p = p.normalized()
	if p.AgingInterval <= 0 || p.MaxPriority <= 0 || p.MaxPending <= 0 || p.ShedThreshold <= 0 || p.DefaultWeight <= 0 || p.StarvationRoundPenalty <= 0 {
		return errors.New("fair queue policy values must be positive")
	}
	for tenant, weight := range p.TenantWeights {
		if strings.TrimSpace(tenant) == "" || weight <= 0 {
			return errors.New("fair queue tenant weights require non-empty tenant and positive weight")
		}
	}
	for tenant, ceiling := range p.EmergencyCeilings {
		if strings.TrimSpace(tenant) == "" || ceiling < 0 || ceiling > p.MaxPriority {
			return errors.New("fair queue emergency ceiling is invalid")
		}
	}
	return nil
}

type queuedAdmission struct {
	Request       AdmissionRequest
	StableKey     string
	VirtualFinish int64
	BasePriority  int
	Capped        bool
}

// FairAdmissionQueue implements integer weighted fair queuing with deterministic
// tie-breakers. Priority aging eventually raises every request to MaxPriority;
// the exposed starvation deadline is a conservative, verifiable upper bound
// assuming workers continue claiming at least one request per round penalty.
type FairAdmissionQueue struct {
	mu          sync.Mutex
	policy      FairQueuePolicy
	pending     map[string]queuedAdmission
	flowFinish  map[string]int64
	virtualTime int64
}

func NewFairAdmissionQueue(policy FairQueuePolicy) (*FairAdmissionQueue, error) {
	policy = policy.normalized()
	if err := policy.validate(); err != nil {
		return nil, err
	}
	return &FairAdmissionQueue{policy: policy, pending: map[string]queuedAdmission{}, flowFinish: map[string]int64{}}, nil
}

func (q *FairAdmissionQueue) Submit(request AdmissionRequest) (AdmissionDecision, error) {
	request = request.normalized()
	if err := request.validate(); err != nil {
		return AdmissionDecision{}, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if existing, ok := q.pending[request.ID]; ok {
		if admissionRequestDigest(existing.Request) != admissionRequestDigest(request) {
			return AdmissionDecision{}, ErrResourceConflict
		}
		return q.decisionLocked(existing, request.SubmittedAt, AdmissionWaiting, "already_queued"), nil
	}
	if len(q.pending) >= q.policy.MaxPending {
		return q.rejectedDecisionLocked(request, AdmissionRejected, "queue_capacity_exhausted"), nil
	}
	priority, capped := q.cappedPriorityLocked(request)
	weight := q.weightLocked(request.Scope.TenantID)
	start := q.virtualTime
	if q.flowFinish[request.Scope.TenantID] > start {
		start = q.flowFinish[request.Scope.TenantID]
	}
	increment := weightedIncrement(request.SchedulingCost, weight)
	if increment > math.MaxInt64-start {
		return AdmissionDecision{}, ErrResourceOverflow
	}
	finish := start + increment
	q.flowFinish[request.Scope.TenantID] = finish
	queued := queuedAdmission{Request: request, StableKey: stableAdmissionKey(request), VirtualFinish: finish, BasePriority: priority, Capped: capped}
	q.pending[request.ID] = queued
	return q.decisionLocked(queued, request.SubmittedAt, AdmissionWaiting, "queued"), nil
}

func (q *FairAdmissionQueue) Claim(now time.Time, limit int) ([]AdmissionDecision, error) {
	if now.IsZero() || limit <= 0 {
		return nil, errors.New("claim time and positive limit are required")
	}
	now = now.UTC()
	q.mu.Lock()
	defer q.mu.Unlock()
	candidates := make([]queuedAdmission, 0, len(q.pending))
	for _, item := range q.pending {
		if !item.Request.Deadline.IsZero() && !now.Before(item.Request.Deadline) {
			delete(q.pending, item.Request.ID)
			continue
		}
		candidates = append(candidates, item)
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := q.effectivePriorityLocked(candidates[i], now), q.effectivePriorityLocked(candidates[j], now)
		if left != right {
			return left > right
		}
		if candidates[i].VirtualFinish != candidates[j].VirtualFinish {
			return candidates[i].VirtualFinish < candidates[j].VirtualFinish
		}
		return candidates[i].StableKey < candidates[j].StableKey
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	decisions := make([]AdmissionDecision, 0, len(candidates))
	for _, item := range candidates {
		delete(q.pending, item.Request.ID)
		if item.VirtualFinish > q.virtualTime {
			q.virtualTime = item.VirtualFinish
		}
		decisions = append(decisions, q.decisionLocked(item, now, AdmissionAdmitted, "weighted_fair_claim"))
	}
	return decisions, nil
}

// ShedOverload removes only low-priority, not-yet-persisted ordinary work.
// Durable dispatch recovery remains non-sheddable even at maximum pressure.
func (q *FairAdmissionQueue) ShedOverload(now time.Time) []AdmissionDecision {
	if now.IsZero() {
		return nil
	}
	now = now.UTC()
	q.mu.Lock()
	defer q.mu.Unlock()
	excess := len(q.pending) - q.policy.ShedThreshold
	if excess <= 0 {
		return nil
	}
	candidates := make([]queuedAdmission, 0, len(q.pending))
	for _, item := range q.pending {
		if item.Request.Persisted || item.Request.Recovery {
			continue
		}
		candidates = append(candidates, item)
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := q.effectivePriorityLocked(candidates[i], now), q.effectivePriorityLocked(candidates[j], now)
		if left != right {
			return left < right
		}
		if candidates[i].VirtualFinish != candidates[j].VirtualFinish {
			return candidates[i].VirtualFinish > candidates[j].VirtualFinish
		}
		return candidates[i].StableKey > candidates[j].StableKey
	})
	if len(candidates) > excess {
		candidates = candidates[:excess]
	}
	decisions := make([]AdmissionDecision, 0, len(candidates))
	for _, item := range candidates {
		delete(q.pending, item.Request.ID)
		decisions = append(decisions, q.decisionLocked(item, now, AdmissionShed, "overload_low_priority_unpersisted"))
	}
	return decisions
}

func (q *FairAdmissionQueue) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// Remove forgets a queued request after another scheduler tick successfully
// reserved it. Without this convergence step, a temporarily blocked request
// would remain claimable after dispatch and could consume queue capacity until
// its deadline even though the durable reservation already exists.
func (q *FairAdmissionQueue) Remove(requestID string) bool {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.pending[requestID]; !ok {
		return false
	}
	delete(q.pending, requestID)
	return true
}

func (q *FairAdmissionQueue) effectivePriorityLocked(item queuedAdmission, now time.Time) int {
	priority := item.BasePriority
	if now.After(item.Request.SubmittedAt) {
		age := int(now.Sub(item.Request.SubmittedAt) / q.policy.AgingInterval)
		if age > q.policy.MaxPriority-priority {
			age = q.policy.MaxPriority - priority
		}
		priority += age
	}
	if priority > q.policy.MaxPriority {
		priority = q.policy.MaxPriority
	}
	return priority
}

func (q *FairAdmissionQueue) cappedPriorityLocked(request AdmissionRequest) (int, bool) {
	priority := request.Priority
	if priority > q.policy.MaxPriority {
		priority = q.policy.MaxPriority
	}
	capped := priority != request.Priority
	if request.Emergency {
		ceiling := q.policy.EmergencyCeilings[request.Scope.TenantID]
		if ceiling <= 0 {
			ceiling = q.policy.MaxPriority
		}
		if priority > ceiling {
			priority, capped = ceiling, true
		}
	}
	return priority, capped
}

func (q *FairAdmissionQueue) weightLocked(tenantID string) int64 {
	weight := q.policy.TenantWeights[tenantID]
	if weight <= 0 {
		weight = q.policy.DefaultWeight
	}
	return weight
}

func (q *FairAdmissionQueue) starvationDeadlineLocked(item queuedAdmission) time.Time {
	agingRounds := q.policy.MaxPriority - item.BasePriority
	queueRounds := len(q.pending) + 1
	return item.Request.SubmittedAt.Add(time.Duration(agingRounds) * q.policy.AgingInterval).Add(time.Duration(queueRounds) * q.policy.StarvationRoundPenalty)
}

func (q *FairAdmissionQueue) decisionLocked(item queuedAdmission, now time.Time, state AdmissionState, reason string) AdmissionDecision {
	return AdmissionDecision{
		RequestID: item.Request.ID, State: state, Reason: reason, StableKey: item.StableKey,
		EffectivePriority: q.effectivePriorityLocked(item, now), VirtualFinish: item.VirtualFinish,
		StarvationDeadline: q.starvationDeadlineLocked(item), PriorityWasCapped: item.Capped,
		OriginalPriority: item.Request.Priority, ConfiguredPriority: item.BasePriority,
	}
}

func (q *FairAdmissionQueue) rejectedDecisionLocked(request AdmissionRequest, state AdmissionState, reason string) AdmissionDecision {
	priority, capped := q.cappedPriorityLocked(request)
	item := queuedAdmission{Request: request, StableKey: stableAdmissionKey(request), BasePriority: priority, Capped: capped}
	return q.decisionLocked(item, request.SubmittedAt, state, reason)
}

func weightedIncrement(cost, weight int64) int64 {
	if cost <= 0 {
		cost = 1
	}
	if weight <= 0 {
		weight = 1
	}
	const scale int64 = 1_000_000
	if cost > math.MaxInt64/scale {
		return math.MaxInt64
	}
	numerator := cost * scale
	return (numerator + weight - 1) / weight
}

func stableAdmissionKey(request AdmissionRequest) string {
	return fmt.Sprintf("%s/%s/%s/%020d/%s", request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.AgentID, request.SubmittedAt.UnixNano(), request.ID)
}

func admissionRequestDigest(request AdmissionRequest) string {
	request = request.normalized()
	return fmt.Sprintf("%s|%s|%s|%s|%+v|%d|%t|%t|%t|%d|%s|%s",
		request.ID, request.PlanID, request.NodeID, request.Scope.quotaKey(), request.Resources,
		request.Priority, request.Emergency, request.Persisted, request.Recovery,
		request.SchedulingCost, request.SubmittedAt.Format(time.RFC3339Nano), request.Deadline.Format(time.RFC3339Nano))
}

// AdmissionController binds deterministic queue policy to durable accounting.
// TryAdmit is the scheduler boundary: every admitted dispatch already owns a
// persisted reservation, while exhausted shared capacity is explicitly waiting
// and an impossible single request is explicitly rejected.
type AdmissionController struct {
	Ledger *ResourceLedger
	Queue  *FairAdmissionQueue
}

func (c *AdmissionController) TryAdmit(request AdmissionRequest) (AdmissionDecision, error) {
	if c == nil || c.Ledger == nil {
		return AdmissionDecision{}, errors.New("resource ledger is required")
	}
	request = request.normalized()
	if err := request.validate(); err != nil {
		return AdmissionDecision{}, err
	}
	reservation, _, reports, err := c.Ledger.Reserve(ResourceReservationSpec{
		ID: request.ID + ":reservation", IdempotencyKey: "admission:" + request.ID,
		Scope: request.Scope, ParentID: request.ParentReservationID,
		Requested: request.Resources, ExpiresAt: request.Deadline, CreatedAt: request.SubmittedAt,
	})
	if err == nil {
		if c.Queue != nil {
			c.Queue.Remove(request.ID)
		}
		decision := AdmissionDecision{
			RequestID: request.ID, State: AdmissionAdmitted, Reason: "quota_reserved",
			StableKey: stableAdmissionKey(request), EffectivePriority: request.Priority,
			ReservationID: reservation.ID, SoftActions: append([]string(nil), reservation.SoftActions...),
			LimitReports: reports,
		}
		return decision, nil
	}
	var limitErr *ResourceLimitError
	if errors.As(err, &limitErr) {
		state, reason := AdmissionWaiting, "quota_temporarily_exhausted"
		if limitErr.Report.Permanent {
			state, reason = AdmissionRejected, "request_exceeds_hard_limit"
		}
		decision := AdmissionDecision{
			RequestID: request.ID, State: state, Reason: reason, StableKey: stableAdmissionKey(request),
			EffectivePriority: request.Priority, LimitReports: append(reports, limitErr.Report),
		}
		if state == AdmissionWaiting && c.Queue != nil {
			queued, queueErr := c.Queue.Submit(request)
			if queueErr != nil {
				return AdmissionDecision{}, queueErr
			}
			decision.VirtualFinish = queued.VirtualFinish
			decision.StarvationDeadline = queued.StarvationDeadline
			decision.PriorityWasCapped = queued.PriorityWasCapped
			decision.OriginalPriority = queued.OriginalPriority
			decision.ConfiguredPriority = queued.ConfiguredPriority
		}
		return decision, nil
	}
	if errors.Is(err, ErrResourceParentExhausted) {
		return AdmissionDecision{RequestID: request.ID, State: AdmissionRejected, Reason: "parent_budget_exhausted", StableKey: stableAdmissionKey(request)}, nil
	}
	return AdmissionDecision{}, err
}
