package orchestration

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	coreencoding "github.com/adro-project/adro/core/encoding"
)

var (
	ErrResourceNotFound            = errors.New("resource accounting record not found")
	ErrResourceConflict            = errors.New("resource accounting idempotency conflict")
	ErrResourceHardLimit           = errors.New("resource hard limit exceeded")
	ErrResourceParentExhausted     = errors.New("parent reservation exhausted")
	ErrResourceActiveChildren      = errors.New("resource reservation has active children")
	ErrResourceReservationTerminal = errors.New("resource reservation is terminal")
	ErrResourceOverflow            = errors.New("resource accounting overflow")
)

// ResourceVector is the provider-neutral resource unit used by admission,
// reservation, settlement, telemetry, and operator projections. Durations are
// stored as nanoseconds so every dimension has exact integer arithmetic.
type ResourceVector struct {
	CPUTimeNanos     int64 `json:"cpu_time_nanos,omitempty"`
	MemoryPeakBytes  int64 `json:"memory_peak_bytes,omitempty"`
	DiskBytes        int64 `json:"disk_bytes,omitempty"`
	OutputBytes      int64 `json:"output_bytes,omitempty"`
	NetworkBytes     int64 `json:"network_bytes,omitempty"`
	Tokens           int64 `json:"tokens,omitempty"`
	ToolCalls        int64 `json:"tool_calls,omitempty"`
	WallTimeNanos    int64 `json:"wall_time_nanos,omitempty"`
	ConcurrencySlots int64 `json:"concurrency_slots,omitempty"`
}

var resourceDimensionNames = [...]string{
	"cpu_time_nanos", "memory_peak_bytes", "disk_bytes", "output_bytes",
	"network_bytes", "tokens", "tool_calls", "wall_time_nanos", "concurrency_slots",
}

func (v ResourceVector) values() [len(resourceDimensionNames)]int64 {
	return [...]int64{
		v.CPUTimeNanos, v.MemoryPeakBytes, v.DiskBytes, v.OutputBytes,
		v.NetworkBytes, v.Tokens, v.ToolCalls, v.WallTimeNanos, v.ConcurrencySlots,
	}
}

func vectorFromValues(values [len(resourceDimensionNames)]int64) ResourceVector {
	return ResourceVector{
		CPUTimeNanos: values[0], MemoryPeakBytes: values[1], DiskBytes: values[2],
		OutputBytes: values[3], NetworkBytes: values[4], Tokens: values[5],
		ToolCalls: values[6], WallTimeNanos: values[7], ConcurrencySlots: values[8],
	}
}

func (v ResourceVector) Validate() error {
	for index, value := range v.values() {
		if value < 0 {
			return fmt.Errorf("resource %s cannot be negative", resourceDimensionNames[index])
		}
	}
	return nil
}

func (v ResourceVector) IsZero() bool {
	for _, value := range v.values() {
		if value != 0 {
			return false
		}
	}
	return true
}

func (v ResourceVector) Add(other ResourceVector) (ResourceVector, error) {
	left, right := v.values(), other.values()
	var result [len(resourceDimensionNames)]int64
	for index := range left {
		if left[index] < 0 || right[index] < 0 {
			return ResourceVector{}, fmt.Errorf("resource %s cannot be negative", resourceDimensionNames[index])
		}
		if right[index] > math.MaxInt64-left[index] {
			return ResourceVector{}, fmt.Errorf("%w: %s", ErrResourceOverflow, resourceDimensionNames[index])
		}
		result[index] = left[index] + right[index]
	}
	return vectorFromValues(result), nil
}

func (v ResourceVector) SubtractFloor(other ResourceVector) ResourceVector {
	left, right := v.values(), other.values()
	var result [len(resourceDimensionNames)]int64
	for index := range left {
		if right[index] < left[index] {
			result[index] = left[index] - right[index]
		}
	}
	return vectorFromValues(result)
}

func (v ResourceVector) Max(other ResourceVector) ResourceVector {
	left, right := v.values(), other.values()
	for index := range left {
		if right[index] > left[index] {
			left[index] = right[index]
		}
	}
	return vectorFromValues(left)
}

// Excess returns the amount above limit. A zero limit means that dimension is
// intentionally unlimited, not that every non-zero request is rejected.
func (v ResourceVector) Excess(limit ResourceVector) ResourceVector {
	values, limits := v.values(), limit.values()
	var excess [len(resourceDimensionNames)]int64
	for index := range values {
		if limits[index] > 0 && values[index] > limits[index] {
			excess[index] = values[index] - limits[index]
		}
	}
	return vectorFromValues(excess)
}

func (v ResourceVector) Within(limit ResourceVector) bool {
	return v.Excess(limit).IsZero()
}

func (v ResourceVector) Duration() time.Duration {
	return time.Duration(v.WallTimeNanos)
}

// ResourceDelta preserves signed differences between normalized provider
// usage and the local estimator. It must never be used as a budget credit.
type ResourceDelta struct {
	CPUTimeNanos     int64 `json:"cpu_time_nanos,omitempty"`
	MemoryPeakBytes  int64 `json:"memory_peak_bytes,omitempty"`
	DiskBytes        int64 `json:"disk_bytes,omitempty"`
	OutputBytes      int64 `json:"output_bytes,omitempty"`
	NetworkBytes     int64 `json:"network_bytes,omitempty"`
	Tokens           int64 `json:"tokens,omitempty"`
	ToolCalls        int64 `json:"tool_calls,omitempty"`
	WallTimeNanos    int64 `json:"wall_time_nanos,omitempty"`
	ConcurrencySlots int64 `json:"concurrency_slots,omitempty"`
}

func resourceDelta(actual, estimate ResourceVector) ResourceDelta {
	a, e := actual.values(), estimate.values()
	return ResourceDelta{
		CPUTimeNanos: a[0] - e[0], MemoryPeakBytes: a[1] - e[1], DiskBytes: a[2] - e[2],
		OutputBytes: a[3] - e[3], NetworkBytes: a[4] - e[4], Tokens: a[5] - e[5],
		ToolCalls: a[6] - e[6], WallTimeNanos: a[7] - e[7], ConcurrencySlots: a[8] - e[8],
	}
}

// ResourceScope is the immutable attribution carried by every reservation and
// usage event. Tenant/workspace/agent form the quota hierarchy; the remaining
// fields make model and tool costs auditable to one execution step.
type ResourceScope struct {
	TenantID     string `json:"tenant_id"`
	WorkspaceID  string `json:"workspace_id"`
	AgentID      string `json:"agent_id,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	StepID       string `json:"step_id,omitempty"`
	ModelCallID  string `json:"model_call_id,omitempty"`
	ToolEffectID string `json:"tool_effect_id,omitempty"`
	CostCenter   string `json:"cost_center,omitempty"`
}

func (s ResourceScope) normalized() ResourceScope {
	s.TenantID = strings.TrimSpace(s.TenantID)
	s.WorkspaceID = strings.TrimSpace(s.WorkspaceID)
	s.AgentID = strings.TrimSpace(s.AgentID)
	s.SessionID = strings.TrimSpace(s.SessionID)
	s.StepID = strings.TrimSpace(s.StepID)
	s.ModelCallID = strings.TrimSpace(s.ModelCallID)
	s.ToolEffectID = strings.TrimSpace(s.ToolEffectID)
	s.CostCenter = strings.TrimSpace(s.CostCenter)
	return s
}

func (s ResourceScope) validateHierarchy() error {
	s = s.normalized()
	if s.TenantID == "" {
		return errors.New("resource tenant_id is required")
	}
	if s.WorkspaceID == "" && s.AgentID != "" {
		return errors.New("resource agent scope requires workspace_id")
	}
	return nil
}

func (s ResourceScope) validateUsageAttribution() error {
	if err := s.validateHierarchy(); err != nil {
		return err
	}
	s = s.normalized()
	if s.WorkspaceID == "" || s.SessionID == "" || s.StepID == "" || s.CostCenter == "" {
		return errors.New("usage requires workspace_id, session_id, step_id and cost_center")
	}
	if s.ModelCallID == "" && s.ToolEffectID == "" {
		return errors.New("usage requires model_call_id or tool_effect_id")
	}
	return nil
}

func (s ResourceScope) quotaKey() string {
	s = s.normalized()
	return strings.Join([]string{s.TenantID, s.WorkspaceID, s.AgentID}, "\x00")
}

func (s ResourceScope) matches(candidate ResourceScope) bool {
	s, candidate = s.normalized(), candidate.normalized()
	if s.TenantID != candidate.TenantID {
		return false
	}
	if s.WorkspaceID != "" && s.WorkspaceID != candidate.WorkspaceID {
		return false
	}
	return s.AgentID == "" || s.AgentID == candidate.AgentID
}

func (s ResourceScope) String() string {
	s = s.normalized()
	parts := []string{"tenant=" + s.TenantID}
	if s.WorkspaceID != "" {
		parts = append(parts, "workspace="+s.WorkspaceID)
	}
	if s.AgentID != "" {
		parts = append(parts, "agent="+s.AgentID)
	}
	return strings.Join(parts, "/")
}

// ResourceQuota applies at exactly one tenant, workspace, or agent level.
// Zero hard-limit dimensions are unlimited. Soft limits may be configured
// independently to trigger warning/compaction/degradation actions.
type ResourceQuota struct {
	Scope                    ResourceScope  `json:"scope"`
	SoftLimit                ResourceVector `json:"soft_limit,omitempty"`
	HardLimit                ResourceVector `json:"hard_limit,omitempty"`
	QueueWeight              int64          `json:"queue_weight,omitempty"`
	EmergencyPriorityCeiling int            `json:"emergency_priority_ceiling,omitempty"`
	UpdatedAt                time.Time      `json:"updated_at"`
}

func (q ResourceQuota) validate() error {
	q.Scope = q.Scope.normalized()
	if err := q.Scope.validateHierarchy(); err != nil {
		return err
	}
	if q.Scope.SessionID != "" || q.Scope.StepID != "" || q.Scope.ModelCallID != "" || q.Scope.ToolEffectID != "" || q.Scope.CostCenter != "" {
		return errors.New("resource quota scope may contain only tenant_id, workspace_id and agent_id")
	}
	if q.Scope.WorkspaceID == "" && q.Scope.AgentID != "" {
		return errors.New("agent quota requires workspace scope")
	}
	if err := q.SoftLimit.Validate(); err != nil {
		return fmt.Errorf("soft limit: %w", err)
	}
	if err := q.HardLimit.Validate(); err != nil {
		return fmt.Errorf("hard limit: %w", err)
	}
	soft, hard := q.SoftLimit.values(), q.HardLimit.values()
	for index := range soft {
		if hard[index] > 0 && soft[index] > hard[index] {
			return fmt.Errorf("soft %s exceeds hard limit", resourceDimensionNames[index])
		}
	}
	if q.QueueWeight < 0 || q.EmergencyPriorityCeiling < 0 {
		return errors.New("quota weight and emergency priority ceiling cannot be negative")
	}
	return nil
}

func (q ResourceQuota) normalized() ResourceQuota {
	q.Scope = q.Scope.normalized()
	if q.QueueWeight <= 0 {
		q.QueueWeight = 1
	}
	if q.EmergencyPriorityCeiling <= 0 {
		q.EmergencyPriorityCeiling = 100
	}
	q.UpdatedAt = q.UpdatedAt.UTC()
	return q
}

type ResourceReservationStatus string

const (
	ResourceReserved ResourceReservationStatus = "reserved"
	ResourceSettled  ResourceReservationStatus = "settled"
	ResourceReleased ResourceReservationStatus = "released"
	ResourceExpired  ResourceReservationStatus = "expired"
)

// ResourceState retains each required accounting phase without replacing the
// original request or reservation when consumption arrives later.
type ResourceState struct {
	Requested ResourceVector `json:"requested"`
	Reserved  ResourceVector `json:"reserved"`
	Consumed  ResourceVector `json:"consumed"`
	Released  ResourceVector `json:"released"`
	Overage   ResourceVector `json:"overage"`
}

type ResourceReservationSpec struct {
	ID             string
	IdempotencyKey string
	Scope          ResourceScope
	ParentID       string
	Requested      ResourceVector
	ExpiresAt      time.Time
	CreatedAt      time.Time
}

type ResourceReservation struct {
	ID             string                    `json:"id"`
	IdempotencyKey string                    `json:"idempotency_key"`
	PayloadDigest  string                    `json:"payload_digest"`
	Scope          ResourceScope             `json:"scope"`
	ParentID       string                    `json:"parent_id,omitempty"`
	State          ResourceState             `json:"state"`
	OwnConsumed    ResourceVector            `json:"own_consumed"`
	Status         ResourceReservationStatus `json:"status"`
	ExpiresAt      time.Time                 `json:"expires_at,omitempty"`
	CreatedAt      time.Time                 `json:"created_at"`
	UpdatedAt      time.Time                 `json:"updated_at"`
	TerminalAt     time.Time                 `json:"terminal_at,omitempty"`
	TerminalReason string                    `json:"terminal_reason,omitempty"`
	SoftActions    []string                  `json:"soft_actions,omitempty"`
}

func (r ResourceReservation) active() bool {
	return r.Status == ResourceReserved
}

func (r ResourceReservation) liability() ResourceVector {
	if r.active() {
		return r.State.Reserved.Max(r.State.Consumed)
	}
	consumed := r.State.Consumed
	consumed.ConcurrencySlots = 0
	return consumed
}

func reservationDigest(spec ResourceReservationSpec) (string, error) {
	payload := struct {
		Scope     ResourceScope  `json:"scope"`
		ParentID  string         `json:"parent_id,omitempty"`
		Requested ResourceVector `json:"requested"`
		ExpiresAt time.Time      `json:"expires_at,omitempty"`
	}{Scope: spec.Scope.normalized(), ParentID: strings.TrimSpace(spec.ParentID), Requested: spec.Requested, ExpiresAt: spec.ExpiresAt.UTC()}
	return coreencoding.Digest(payload)
}

// UsageRecord keeps the untrusted provider payload beside normalized values,
// the independent local estimate, and their signed discrepancy.
type UsageRecord struct {
	ID                   string          `json:"id"`
	ReservationID        string          `json:"reservation_id"`
	Scope                ResourceScope   `json:"scope"`
	RawProvider          json.RawMessage `json:"raw_provider,omitempty"`
	Normalized           ResourceVector  `json:"normalized"`
	Estimated            ResourceVector  `json:"estimated"`
	Discrepancy          ResourceDelta   `json:"discrepancy"`
	ProviderUsageMissing bool            `json:"provider_usage_missing,omitempty"`
	DelayedBilling       bool            `json:"delayed_billing,omitempty"`
	ObservedAt           time.Time       `json:"observed_at"`
	PayloadDigest        string          `json:"payload_digest"`
}

// NewUsageRecord validates attribution and never trusts a negative or
// overflowing provider value. When provider usage is absent, normalized usage
// falls back to the estimator and records that fact explicitly.
func NewUsageRecord(id, reservationID string, scope ResourceScope, raw json.RawMessage, normalized, estimated ResourceVector, providerMissing, delayed bool, observedAt time.Time) (UsageRecord, error) {
	id, reservationID = strings.TrimSpace(id), strings.TrimSpace(reservationID)
	scope = scope.normalized()
	if id == "" || reservationID == "" {
		return UsageRecord{}, errors.New("usage id and reservation_id are required")
	}
	if err := scope.validateUsageAttribution(); err != nil {
		return UsageRecord{}, err
	}
	if err := normalized.Validate(); err != nil {
		return UsageRecord{}, fmt.Errorf("normalized usage: %w", err)
	}
	if err := estimated.Validate(); err != nil {
		return UsageRecord{}, fmt.Errorf("estimated usage: %w", err)
	}
	if providerMissing {
		normalized = estimated
	}
	if observedAt.IsZero() {
		return UsageRecord{}, errors.New("usage observed_at is required")
	}
	if len(raw) > 0 && !json.Valid(raw) {
		return UsageRecord{}, errors.New("raw provider usage must be valid JSON")
	}
	record := UsageRecord{
		ID: id, ReservationID: reservationID, Scope: scope,
		RawProvider: append(json.RawMessage(nil), raw...), Normalized: normalized,
		Estimated: estimated, Discrepancy: resourceDelta(normalized, estimated),
		ProviderUsageMissing: providerMissing, DelayedBilling: delayed, ObservedAt: observedAt.UTC(),
	}
	digest, err := coreencoding.Digest(struct {
		ReservationID        string          `json:"reservation_id"`
		Scope                ResourceScope   `json:"scope"`
		RawProvider          json.RawMessage `json:"raw_provider,omitempty"`
		Normalized           ResourceVector  `json:"normalized"`
		Estimated            ResourceVector  `json:"estimated"`
		ProviderUsageMissing bool            `json:"provider_usage_missing,omitempty"`
		DelayedBilling       bool            `json:"delayed_billing,omitempty"`
		ObservedAt           time.Time       `json:"observed_at"`
	}{record.ReservationID, record.Scope, record.RawProvider, record.Normalized, record.Estimated, record.ProviderUsageMissing, record.DelayedBilling, record.ObservedAt})
	if err != nil {
		return UsageRecord{}, err
	}
	record.PayloadDigest = digest
	return record, nil
}

// ResourceLimitReport is stable operator-facing evidence for why a request was
// admitted, warned, queued, or rejected.
type ResourceLimitReport struct {
	Scope       ResourceScope  `json:"scope"`
	Current     ResourceVector `json:"current"`
	Requested   ResourceVector `json:"requested"`
	Projected   ResourceVector `json:"projected"`
	SoftLimit   ResourceVector `json:"soft_limit,omitempty"`
	HardLimit   ResourceVector `json:"hard_limit,omitempty"`
	SoftExcess  ResourceVector `json:"soft_excess,omitempty"`
	HardExcess  ResourceVector `json:"hard_excess,omitempty"`
	Permanent   bool           `json:"permanent,omitempty"`
	Explanation string         `json:"explanation,omitempty"`
}
