package orchestration

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/core"
	"github.com/adro-project/adro/core/budget"
	coreencoding "github.com/adro-project/adro/core/encoding"
	"github.com/adro-project/adro/internal/durable"
)

const resourceLedgerSchemaVersion = 1

type ResourceLedgerOptions struct {
	Clock core.Clock
	IDs   core.IDGenerator
}

type resourceLedgerState struct {
	Version      int                                   `json:"version"`
	Revision     int64                                 `json:"revision"`
	Quotas       map[string]budget.ResourceQuota       `json:"quotas"`
	Reservations map[string]budget.ResourceReservation `json:"reservations"`
	Usage        map[string]budget.UsageRecord         `json:"usage"`
	Operations   map[string]string                     `json:"operations"`
}

// ResourceLedger is the reference durable accounting backend. Mutations are
// atomic JSON snapshots under an inter-process lock. Production databases can
// implement the same reserve/record/settle contract without changing scheduler
// decisions or resource event semantics.
type ResourceLedger struct {
	mu           sync.RWMutex
	path         string
	revision     int64
	quotas       map[string]budget.ResourceQuota
	reservations map[string]budget.ResourceReservation
	usage        map[string]budget.UsageRecord
	operations   map[string]string
	clock        core.Clock
	ids          core.IDGenerator
}

func NewResourceLedger(path string, options ResourceLedgerOptions) (*ResourceLedger, error) {
	if options.Clock == nil {
		options.Clock = core.SystemClock{}
	}
	if options.IDs == nil {
		options.IDs = &core.CryptoIDs{}
	}
	ledger := &ResourceLedger{
		path: strings.TrimSpace(path), quotas: map[string]budget.ResourceQuota{},
		reservations: map[string]budget.ResourceReservation{}, usage: map[string]budget.UsageRecord{},
		operations: map[string]string{}, clock: options.Clock, ids: options.IDs,
	}
	if ledger.path != "" {
		if err := ledger.loadFromDisk(); err != nil {
			return nil, err
		}
	}
	return ledger, nil
}

func (l *ResourceLedger) SetQuota(quota budget.ResourceQuota) error {
	quota = quota.Normalized()
	if quota.UpdatedAt.IsZero() {
		quota.UpdatedAt = l.clock.Now().UTC()
	}
	if err := quota.Validate(); err != nil {
		return err
	}
	return l.mutate(func() error {
		l.quotas[quota.Scope.QuotaKey()] = quota
		return nil
	})
}

func (l *ResourceLedger) DeleteQuota(scope budget.ResourceScope) error {
	scope = scope.Normalized()
	if err := scope.ValidateHierarchy(); err != nil {
		return err
	}
	return l.mutate(func() error {
		if _, ok := l.quotas[scope.QuotaKey()]; !ok {
			return budget.ErrResourceNotFound
		}
		delete(l.quotas, scope.QuotaKey())
		return nil
	})
}

func (l *ResourceLedger) ListQuotas() ([]budget.ResourceQuota, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return nil, err
	}
	result := make([]budget.ResourceQuota, 0, len(l.quotas))
	for _, quota := range l.quotas {
		result = append(result, quota)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Scope.QuotaKey() < result[j].Scope.QuotaKey() })
	return result, nil
}

// CheckReservation returns every applicable tenant/workspace/agent limit in
// deterministic hierarchy order without mutating the ledger.
func (l *ResourceLedger) CheckReservation(spec budget.ResourceReservationSpec) ([]budget.ResourceLimitReport, error) {
	spec.Scope = spec.Scope.Normalized()
	if err := validateReservationSpec(spec); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return nil, err
	}
	if err := l.checkParentLocked(spec); err != nil {
		return nil, err
	}
	return l.limitReportsLocked(spec.Scope, spec.Requested)
}

// Reserve is idempotent on IdempotencyKey and charges all matching hierarchy
// quotas before external work is dispatched. Child reservations carve capacity
// out of their parent's reserved pool and therefore cannot recursively oversell
// a top-level task budget.
func (l *ResourceLedger) Reserve(spec budget.ResourceReservationSpec) (budget.ResourceReservation, bool, []budget.ResourceLimitReport, error) {
	spec.Scope = spec.Scope.Normalized()
	spec.ParentID = strings.TrimSpace(spec.ParentID)
	spec.IdempotencyKey = strings.TrimSpace(spec.IdempotencyKey)
	if err := validateReservationSpec(spec); err != nil {
		return budget.ResourceReservation{}, false, nil, err
	}
	digest, err := budget.ReservationDigest(spec)
	if err != nil {
		return budget.ResourceReservation{}, false, nil, err
	}
	var result budget.ResourceReservation
	var reports []budget.ResourceLimitReport
	created := false
	err = l.mutate(func() error {
		if prior, ok := l.findReservationByKeyLocked(spec.IdempotencyKey); ok {
			if prior.PayloadDigest != digest {
				return budget.ErrResourceConflict
			}
			result = cloneReservation(prior)
			reports, _ = l.limitReportsLocked(spec.Scope, budget.ResourceVector{})
			return nil
		}
		if err := l.checkParentLocked(spec); err != nil {
			return err
		}
		var checkErr error
		reports, checkErr = l.limitReportsLocked(spec.Scope, spec.Requested)
		if checkErr != nil {
			return checkErr
		}
		for _, report := range reports {
			if !report.HardExcess.IsZero() {
				return &ResourceLimitError{Report: report}
			}
		}
		id := strings.TrimSpace(spec.ID)
		if id == "" {
			id = l.ids.NewID("reservation")
		}
		if _, exists := l.reservations[id]; exists {
			return budget.ErrResourceConflict
		}
		now := spec.CreatedAt.UTC()
		if now.IsZero() {
			now = l.clock.Now().UTC()
		}
		reservation := budget.ResourceReservation{
			ID: id, IdempotencyKey: spec.IdempotencyKey, PayloadDigest: digest,
			Scope: spec.Scope, ParentID: spec.ParentID,
			State:  budget.ResourceState{Requested: spec.Requested, Reserved: spec.Requested},
			Status: budget.ResourceReserved, ExpiresAt: spec.ExpiresAt.UTC(), CreatedAt: now, UpdatedAt: now,
		}
		for _, report := range reports {
			if !report.SoftExcess.IsZero() {
				reservation.SoftActions = []string{"warning", "compact_context", "degrade_execution"}
				break
			}
		}
		l.reservations[id] = reservation
		result, created = cloneReservation(reservation), true
		return nil
	})
	return result, created, reports, err
}

func validateReservationSpec(spec budget.ResourceReservationSpec) error {
	if strings.TrimSpace(spec.IdempotencyKey) == "" {
		return errors.New("resource reservation idempotency_key is required")
	}
	if err := spec.Scope.ValidateHierarchy(); err != nil {
		return err
	}
	if spec.Scope.WorkspaceID == "" {
		return errors.New("resource reservation workspace_id is required")
	}
	if err := spec.Requested.Validate(); err != nil {
		return err
	}
	if spec.Requested.IsZero() {
		return errors.New("resource reservation request cannot be empty")
	}
	if !spec.ExpiresAt.IsZero() && !spec.CreatedAt.IsZero() && !spec.ExpiresAt.After(spec.CreatedAt) {
		return errors.New("resource reservation expiry must be after creation")
	}
	return nil
}

// ResourceLimitError preserves a machine-readable report while supporting
// errors.Is(err, budget.ErrResourceHardLimit).
type ResourceLimitError struct {
	Report budget.ResourceLimitReport
}

func (e *ResourceLimitError) Error() string {
	return fmt.Sprintf("%v: %s", budget.ErrResourceHardLimit, e.Report.Explanation)
}

func (e *ResourceLimitError) Unwrap() error { return budget.ErrResourceHardLimit }

func (l *ResourceLedger) GetReservation(id string) (budget.ResourceReservation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return budget.ResourceReservation{}, err
	}
	reservation, ok := l.reservations[strings.TrimSpace(id)]
	if !ok {
		return budget.ResourceReservation{}, budget.ErrResourceNotFound
	}
	return cloneReservation(reservation), nil
}

func (l *ResourceLedger) ListReservations(scope budget.ResourceScope, includeTerminal bool) ([]budget.ResourceReservation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return nil, err
	}
	scope = scope.Normalized()
	result := make([]budget.ResourceReservation, 0, len(l.reservations))
	for _, reservation := range l.reservations {
		if scope.TenantID != "" && !scope.Matches(reservation.Scope) {
			continue
		}
		if !includeTerminal && !reservation.Active() {
			continue
		}
		result = append(result, cloneReservation(reservation))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result, nil
}

// RecordUsage accepts at-least-once provider or estimator events. Duplicate
// IDs with identical payload converge; changed payloads fail closed. Actual
// usage is always retained, even when it crosses a hard limit, because
// discarding an overage would turn untrusted telemetry into a budget credit.
func (l *ResourceLedger) GetUsage(id string) (budget.UsageRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return budget.UsageRecord{}, err
	}
	record, ok := l.usage[strings.TrimSpace(id)]
	if !ok {
		return budget.UsageRecord{}, budget.ErrResourceNotFound
	}
	return cloneUsage(record), nil
}

func (l *ResourceLedger) RecordUsage(record budget.UsageRecord) (budget.UsageRecord, bool, []budget.ResourceLimitReport, error) {
	if err := validateUsageRecord(record); err != nil {
		return budget.UsageRecord{}, false, nil, err
	}
	var result budget.UsageRecord
	var reports []budget.ResourceLimitReport
	created := false
	err := l.mutate(func() error {
		if prior, ok := l.usage[record.ID]; ok {
			if prior.PayloadDigest != record.PayloadDigest {
				return budget.ErrResourceConflict
			}
			result = cloneUsage(prior)
			return nil
		}
		reservation, ok := l.reservations[record.ReservationID]
		if !ok {
			return budget.ErrResourceNotFound
		}
		if !sameAccountingScope(reservation.Scope, record.Scope) {
			return errors.New("usage scope does not match reservation")
		}
		if !reservation.Active() && !record.DelayedBilling {
			return budget.ErrResourceReservationTerminal
		}
		updatedOwn, err := reservation.OwnConsumed.Add(record.Normalized)
		if err != nil {
			return err
		}
		reservation.OwnConsumed = updatedOwn
		if err := l.addConsumptionLocked(reservation.ID, record.Normalized); err != nil {
			return err
		}
		reservation = l.reservations[reservation.ID]
		reservation.OwnConsumed = updatedOwn
		reservation.UpdatedAt = record.ObservedAt.UTC()
		l.recomputeTerminalStateLocked(&reservation)
		l.reservations[reservation.ID] = reservation
		l.usage[record.ID] = cloneUsage(record)
		result, created = cloneUsage(record), true
		var checkErr error
		reports, checkErr = l.limitReportsLocked(record.Scope, budget.ResourceVector{})
		if checkErr != nil {
			return checkErr
		}
		for _, report := range reports {
			if report.HardExcess.IsZero() {
				continue
			}
			reservation = l.reservations[record.ReservationID]
			reservation.TerminalReason = "hard_limit_exceeded"
			reservation.UpdatedAt = record.ObservedAt.UTC()
			l.reservations[record.ReservationID] = reservation
			break
		}
		return nil
	})
	return result, created, reports, err
}

func validateUsageRecord(record budget.UsageRecord) error {
	if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.ReservationID) == "" || strings.TrimSpace(record.PayloadDigest) == "" {
		return errors.New("usage id, reservation_id and payload_digest are required")
	}
	if err := record.Scope.ValidateUsageAttribution(); err != nil {
		return err
	}
	if err := record.Normalized.Validate(); err != nil {
		return err
	}
	if err := record.Estimated.Validate(); err != nil {
		return err
	}
	if record.ObservedAt.IsZero() {
		return errors.New("usage observed_at is required")
	}
	if len(record.RawProvider) > 0 && !json.Valid(record.RawProvider) {
		return errors.New("raw provider usage must be valid JSON")
	}
	return nil
}

func sameAccountingScope(reservation, usage budget.ResourceScope) bool {
	reservation, usage = reservation.Normalized(), usage.Normalized()
	return reservation.TenantID == usage.TenantID && reservation.WorkspaceID == usage.WorkspaceID && reservation.AgentID == usage.AgentID
}

// Settle closes a reservation after all children have settled or released.
// Usage can still receive an explicitly delayed billing adjustment later.
func (l *ResourceLedger) Settle(id, idempotencyKey, reason string, at time.Time) (budget.ResourceReservation, error) {
	return l.finishReservation(id, idempotencyKey, reason, at, budget.ResourceSettled)
}

func (l *ResourceLedger) Release(id, idempotencyKey, reason string, at time.Time) (budget.ResourceReservation, error) {
	return l.finishReservation(id, idempotencyKey, reason, at, budget.ResourceReleased)
}

func (l *ResourceLedger) finishReservation(id, idempotencyKey, reason string, at time.Time, status budget.ResourceReservationStatus) (budget.ResourceReservation, error) {
	id, idempotencyKey, reason = strings.TrimSpace(id), strings.TrimSpace(idempotencyKey), strings.TrimSpace(reason)
	if id == "" || idempotencyKey == "" {
		return budget.ResourceReservation{}, errors.New("reservation id and operation idempotency key are required")
	}
	if at.IsZero() {
		at = l.clock.Now()
	}
	at = at.UTC()
	operationDigest, err := coreencoding.Digest(struct {
		ID     string                           `json:"id"`
		Status budget.ResourceReservationStatus `json:"status"`
		Reason string                           `json:"reason"`
	}{id, status, reason})
	if err != nil {
		return budget.ResourceReservation{}, err
	}
	var result budget.ResourceReservation
	err = l.mutate(func() error {
		if priorDigest, ok := l.operations[idempotencyKey]; ok {
			if priorDigest != operationDigest {
				return budget.ErrResourceConflict
			}
			prior, ok := l.reservations[id]
			if !ok {
				return budget.ErrResourceNotFound
			}
			result = cloneReservation(prior)
			return nil
		}
		reservation, ok := l.reservations[id]
		if !ok {
			return budget.ErrResourceNotFound
		}
		if !reservation.Active() {
			return budget.ErrResourceReservationTerminal
		}
		if l.hasActiveChildrenLocked(id) {
			return budget.ErrResourceActiveChildren
		}
		reservation.Status = status
		reservation.TerminalAt, reservation.UpdatedAt = at, at
		if reason == "" {
			if status == budget.ResourceSettled {
				reason = "completed"
			} else {
				reason = "released"
			}
		}
		reservation.TerminalReason = reason
		l.recomputeTerminalStateLocked(&reservation)
		l.reservations[id] = reservation
		l.operations[idempotencyKey] = operationDigest
		result = cloneReservation(reservation)
		return nil
	})
	return result, err
}

// ReapOrphans releases expired reservations deepest-first. This recovers child
// allocations before parents and is safe to retry after a crash.
func (l *ResourceLedger) ReapOrphans(now time.Time) ([]budget.ResourceReservation, error) {
	if now.IsZero() {
		now = l.clock.Now()
	}
	now = now.UTC()
	var expired []budget.ResourceReservation
	err := l.mutate(func() error {
		ids := make([]string, 0)
		for id, reservation := range l.reservations {
			if reservation.Active() && !reservation.ExpiresAt.IsZero() && !reservation.ExpiresAt.After(now) {
				ids = append(ids, id)
			}
		}
		sort.Slice(ids, func(i, j int) bool {
			left, right := l.reservationDepthLocked(ids[i]), l.reservationDepthLocked(ids[j])
			if left == right {
				return ids[i] < ids[j]
			}
			return left > right
		})
		for _, id := range ids {
			reservation := l.reservations[id]
			if !reservation.Active() || l.hasActiveChildrenLocked(id) {
				continue
			}
			reservation.Status = budget.ResourceExpired
			reservation.TerminalAt, reservation.UpdatedAt = now, now
			reservation.TerminalReason = "reservation_lease_expired"
			l.recomputeTerminalStateLocked(&reservation)
			l.reservations[id] = reservation
			expired = append(expired, cloneReservation(reservation))
		}
		return nil
	})
	return expired, err
}

func (l *ResourceLedger) checkParentLocked(spec budget.ResourceReservationSpec) error {
	if spec.ParentID == "" {
		return nil
	}
	parent, ok := l.reservations[spec.ParentID]
	if !ok {
		return budget.ErrResourceNotFound
	}
	if !parent.Active() {
		return budget.ErrResourceReservationTerminal
	}
	if parent.Scope.TenantID != spec.Scope.TenantID || parent.Scope.WorkspaceID != spec.Scope.WorkspaceID {
		return errors.New("child reservation must remain in the parent tenant and workspace")
	}
	allocated := parent.OwnConsumed
	for _, child := range l.reservations {
		if child.ParentID != parent.ID {
			continue
		}
		var err error
		allocated, err = allocated.Add(child.Liability())
		if err != nil {
			return err
		}
	}
	projected, err := allocated.Add(spec.Requested)
	if err != nil {
		return err
	}
	if excess := projected.Excess(parent.State.Reserved); !excess.IsZero() {
		return fmt.Errorf("%w: parent=%s excess=%+v", budget.ErrResourceParentExhausted, parent.ID, excess)
	}
	return nil
}

func (l *ResourceLedger) addConsumptionLocked(id string, delta budget.ResourceVector) error {
	currentID := id
	visited := map[string]bool{}
	for currentID != "" {
		if visited[currentID] {
			return errors.New("resource reservation parent cycle")
		}
		visited[currentID] = true
		reservation, ok := l.reservations[currentID]
		if !ok {
			return budget.ErrResourceNotFound
		}
		consumed, err := reservation.State.Consumed.Add(delta)
		if err != nil {
			return err
		}
		reservation.State.Consumed = consumed
		reservation.UpdatedAt = l.clock.Now().UTC()
		l.recomputeTerminalStateLocked(&reservation)
		l.reservations[currentID] = reservation
		currentID = reservation.ParentID
	}
	return nil
}

func (l *ResourceLedger) recomputeTerminalStateLocked(reservation *budget.ResourceReservation) {
	if reservation == nil || reservation.Active() {
		return
	}
	reservation.State.Released = reservation.State.Reserved.SubtractFloor(reservation.State.Consumed)
	// Concurrency is a capacity lease. It is released at terminal even though
	// the consumed state preserves that one slot participated in execution.
	reservation.State.Released.ConcurrencySlots = reservation.State.Reserved.ConcurrencySlots
	reservation.State.Overage = reservation.State.Consumed.Excess(reservation.State.Reserved)
}

func (l *ResourceLedger) hasActiveChildrenLocked(parentID string) bool {
	for _, reservation := range l.reservations {
		if reservation.ParentID == parentID && reservation.Active() {
			return true
		}
	}
	return false
}

func (l *ResourceLedger) reservationDepthLocked(id string) int {
	depth := 0
	seen := map[string]bool{}
	for id != "" && !seen[id] {
		seen[id] = true
		reservation, ok := l.reservations[id]
		if !ok || reservation.ParentID == "" {
			break
		}
		depth++
		id = reservation.ParentID
	}
	return depth
}

func (l *ResourceLedger) findReservationByKeyLocked(key string) (budget.ResourceReservation, bool) {
	for _, reservation := range l.reservations {
		if reservation.IdempotencyKey == key {
			return reservation, true
		}
	}
	return budget.ResourceReservation{}, false
}

func (l *ResourceLedger) limitReportsLocked(scope budget.ResourceScope, requested budget.ResourceVector) ([]budget.ResourceLimitReport, error) {
	quotas := make([]budget.ResourceQuota, 0, 3)
	for _, quota := range l.quotas {
		if quota.Scope.Matches(scope) {
			quotas = append(quotas, quota)
		}
	}
	sort.Slice(quotas, func(i, j int) bool {
		return quotaSpecificity(quotas[i].Scope) < quotaSpecificity(quotas[j].Scope)
	})
	reports := make([]budget.ResourceLimitReport, 0, len(quotas))
	for _, quota := range quotas {
		current, err := l.exposureLocked(quota.Scope)
		if err != nil {
			return nil, err
		}
		projected, err := current.Add(requested)
		if err != nil {
			return nil, err
		}
		report := budget.ResourceLimitReport{
			Scope: quota.Scope, Current: current, Requested: requested, Projected: projected,
			SoftLimit: quota.SoftLimit, HardLimit: quota.HardLimit,
			SoftExcess: projected.Excess(quota.SoftLimit), HardExcess: projected.Excess(quota.HardLimit),
		}
		if !report.HardExcess.IsZero() {
			report.Permanent = !requested.Excess(quota.HardLimit).IsZero()
			if report.Permanent {
				report.Explanation = "request exceeds the configured hard limit even with no competing work"
			} else {
				report.Explanation = "current reservations or consumption exhausted the configured hard limit"
			}
		} else if !report.SoftExcess.IsZero() {
			report.Explanation = "soft limit crossed; warning, context compaction, and degraded execution are required"
		} else {
			report.Explanation = "within configured limits"
		}
		reports = append(reports, report)
	}
	return reports, nil
}

func quotaSpecificity(scope budget.ResourceScope) int {
	if scope.AgentID != "" {
		return 2
	}
	if scope.WorkspaceID != "" {
		return 1
	}
	return 0
}

func (l *ResourceLedger) exposureLocked(scope budget.ResourceScope) (budget.ResourceVector, error) {
	var total budget.ResourceVector
	for _, reservation := range l.reservations {
		if !scope.Matches(reservation.Scope) || l.hasMatchingAncestorLocked(reservation, scope) {
			continue
		}
		var err error
		total, err = total.Add(reservation.Liability())
		if err != nil {
			return budget.ResourceVector{}, err
		}
	}
	return total, nil
}

func (l *ResourceLedger) hasMatchingAncestorLocked(reservation budget.ResourceReservation, scope budget.ResourceScope) bool {
	seen := map[string]bool{}
	parentID := reservation.ParentID
	for parentID != "" && !seen[parentID] {
		seen[parentID] = true
		parent, ok := l.reservations[parentID]
		if !ok {
			return false
		}
		if scope.Matches(parent.Scope) {
			return true
		}
		parentID = parent.ParentID
	}
	return false
}

// ResourceDashboard is the bounded read model consumed by APIs and UIs. It
// exposes burn, active reservations, overage, delayed bills, missing provider
// usage, and child-agent attribution without allowing direct row mutation.
type ResourceDashboard struct {
	Scope                budget.ResourceScope             `json:"scope"`
	Exposure             budget.ResourceVector            `json:"exposure"`
	Consumed             budget.ResourceVector            `json:"consumed"`
	ActiveReservations   []budget.ResourceReservation     `json:"active_reservations,omitempty"`
	RecentUsage          []budget.UsageRecord             `json:"recent_usage,omitempty"`
	OverageReservations  []string                         `json:"overage_reservation_ids,omitempty"`
	MissingProviderUsage int                              `json:"missing_provider_usage"`
	DelayedBills         int                              `json:"delayed_bills"`
	ChildAgentUsage      map[string]budget.ResourceVector `json:"child_agent_usage,omitempty"`
	GeneratedAt          time.Time                        `json:"generated_at"`
}

func (l *ResourceLedger) Dashboard(scope budget.ResourceScope, usageLimit int) (ResourceDashboard, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return ResourceDashboard{}, err
	}
	scope = scope.Normalized()
	if err := scope.ValidateHierarchy(); err != nil {
		return ResourceDashboard{}, err
	}
	if usageLimit <= 0 || usageLimit > 1000 {
		usageLimit = 100
	}
	exposure, err := l.exposureLocked(scope)
	if err != nil {
		return ResourceDashboard{}, err
	}
	dashboard := ResourceDashboard{Scope: scope, Exposure: exposure, ChildAgentUsage: map[string]budget.ResourceVector{}, GeneratedAt: l.clock.Now().UTC()}
	for _, reservation := range l.reservations {
		if !scope.Matches(reservation.Scope) {
			continue
		}
		if reservation.Active() {
			dashboard.ActiveReservations = append(dashboard.ActiveReservations, cloneReservation(reservation))
		}
		if !reservation.State.Overage.IsZero() {
			dashboard.OverageReservations = append(dashboard.OverageReservations, reservation.ID)
		}
		if reservation.ParentID != "" && reservation.Scope.AgentID != "" {
			current := dashboard.ChildAgentUsage[reservation.Scope.AgentID]
			current, err = current.Add(reservation.State.Consumed)
			if err != nil {
				return ResourceDashboard{}, err
			}
			dashboard.ChildAgentUsage[reservation.Scope.AgentID] = current
		}
	}
	usage := make([]budget.UsageRecord, 0)
	for _, record := range l.usage {
		if !scope.Matches(record.Scope) {
			continue
		}
		dashboard.Consumed, err = dashboard.Consumed.Add(record.Normalized)
		if err != nil {
			return ResourceDashboard{}, err
		}
		if record.ProviderUsageMissing {
			dashboard.MissingProviderUsage++
		}
		if record.DelayedBilling {
			dashboard.DelayedBills++
		}
		usage = append(usage, cloneUsage(record))
	}
	sort.Slice(usage, func(i, j int) bool {
		if usage[i].ObservedAt.Equal(usage[j].ObservedAt) {
			return usage[i].ID > usage[j].ID
		}
		return usage[i].ObservedAt.After(usage[j].ObservedAt)
	})
	if len(usage) > usageLimit {
		usage = usage[:usageLimit]
	}
	dashboard.RecentUsage = usage
	sort.Slice(dashboard.ActiveReservations, func(i, j int) bool { return dashboard.ActiveReservations[i].ID < dashboard.ActiveReservations[j].ID })
	sort.Strings(dashboard.OverageReservations)
	return dashboard, nil
}

func (l *ResourceLedger) mutate(fn func() error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	mutate := func() error {
		if err := l.loadLocked(); err != nil {
			return err
		}
		before := l.snapshotLocked()
		if err := fn(); err != nil {
			l.restoreLocked(before)
			return err
		}
		if err := l.persistLocked(); err != nil {
			l.restoreLocked(before)
			return err
		}
		return nil
	}
	if l.path == "" {
		return mutate()
	}
	return durable.WithExclusive(l.path, mutate)
}

func (l *ResourceLedger) loadFromDisk() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loadLocked()
}

func (l *ResourceLedger) loadLocked() error {
	if l.path == "" {
		return nil
	}
	data, err := os.ReadFile(l.path)
	if errors.Is(err, os.ErrNotExist) {
		l.revision = 0
		l.quotas = map[string]budget.ResourceQuota{}
		l.reservations = map[string]budget.ResourceReservation{}
		l.usage = map[string]budget.UsageRecord{}
		l.operations = map[string]string{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read resource ledger: %w", err)
	}
	var state resourceLedgerState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode resource ledger: %w", err)
	}
	if err := validateResourceLedgerState(state); err != nil {
		return err
	}
	l.revision = state.Revision
	l.quotas = cloneQuotas(state.Quotas)
	l.reservations = cloneReservations(state.Reservations)
	l.usage = cloneUsageMap(state.Usage)
	l.operations = cloneStrings(state.Operations)
	return nil
}

func (l *ResourceLedger) persistLocked() error {
	if l.path == "" {
		l.revision++
		return nil
	}
	state := resourceLedgerState{
		Version: resourceLedgerSchemaVersion, Revision: l.revision + 1,
		Quotas: cloneQuotas(l.quotas), Reservations: cloneReservations(l.reservations),
		Usage: cloneUsageMap(l.usage), Operations: cloneStrings(l.operations),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".adro-resource-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := durable.Inject("resource.persist.before_rename"); err != nil {
		return err
	}
	if err := os.Rename(name, l.path); err != nil {
		return err
	}
	l.revision = state.Revision
	return nil
}

func validateResourceLedgerState(state resourceLedgerState) error {
	if state.Version != resourceLedgerSchemaVersion || state.Revision < 0 {
		return errors.New("unsupported resource ledger state")
	}
	if state.Quotas == nil || state.Reservations == nil || state.Usage == nil || state.Operations == nil {
		return errors.New("resource ledger maps are required")
	}
	for key, quota := range state.Quotas {
		if quota.Scope.QuotaKey() != key {
			return fmt.Errorf("resource quota key mismatch %q", key)
		}
		if err := quota.Validate(); err != nil {
			return fmt.Errorf("resource quota %q: %w", key, err)
		}
	}
	for id, reservation := range state.Reservations {
		if reservation.ID != id || reservation.IdempotencyKey == "" || reservation.PayloadDigest == "" {
			return fmt.Errorf("invalid resource reservation %q", id)
		}
		if err := reservation.Scope.ValidateHierarchy(); err != nil {
			return fmt.Errorf("resource reservation %q: %w", id, err)
		}
		for _, vector := range []budget.ResourceVector{reservation.State.Requested, reservation.State.Reserved, reservation.State.Consumed, reservation.State.Released, reservation.State.Overage, reservation.OwnConsumed} {
			if err := vector.Validate(); err != nil {
				return fmt.Errorf("resource reservation %q: %w", id, err)
			}
		}
		switch reservation.Status {
		case budget.ResourceReserved, budget.ResourceSettled, budget.ResourceReleased, budget.ResourceExpired:
		default:
			return fmt.Errorf("resource reservation %q has invalid status", id)
		}
		if reservation.ParentID != "" {
			if _, ok := state.Reservations[reservation.ParentID]; !ok {
				return fmt.Errorf("resource reservation %q has missing parent", id)
			}
		}
	}
	for id, record := range state.Usage {
		if record.ID != id {
			return fmt.Errorf("resource usage key mismatch %q", id)
		}
		if err := validateUsageRecord(record); err != nil {
			return fmt.Errorf("resource usage %q: %w", id, err)
		}
		if _, ok := state.Reservations[record.ReservationID]; !ok {
			return fmt.Errorf("resource usage %q has missing reservation", id)
		}
	}
	return nil
}

func (l *ResourceLedger) snapshotLocked() resourceLedgerState {
	return resourceLedgerState{Version: resourceLedgerSchemaVersion, Revision: l.revision, Quotas: cloneQuotas(l.quotas), Reservations: cloneReservations(l.reservations), Usage: cloneUsageMap(l.usage), Operations: cloneStrings(l.operations)}
}

func (l *ResourceLedger) restoreLocked(state resourceLedgerState) {
	l.revision = state.Revision
	l.quotas = cloneQuotas(state.Quotas)
	l.reservations = cloneReservations(state.Reservations)
	l.usage = cloneUsageMap(state.Usage)
	l.operations = cloneStrings(state.Operations)
}

func cloneReservation(value budget.ResourceReservation) budget.ResourceReservation {
	value.SoftActions = append([]string(nil), value.SoftActions...)
	return value
}

func cloneReservations(values map[string]budget.ResourceReservation) map[string]budget.ResourceReservation {
	result := make(map[string]budget.ResourceReservation, len(values))
	for key, value := range values {
		result[key] = cloneReservation(value)
	}
	return result
}

func cloneUsage(value budget.UsageRecord) budget.UsageRecord {
	value.RawProvider = append(json.RawMessage(nil), value.RawProvider...)
	return value
}

func cloneUsageMap(values map[string]budget.UsageRecord) map[string]budget.UsageRecord {
	result := make(map[string]budget.UsageRecord, len(values))
	for key, value := range values {
		result[key] = cloneUsage(value)
	}
	return result
}

func cloneQuotas(values map[string]budget.ResourceQuota) map[string]budget.ResourceQuota {
	result := make(map[string]budget.ResourceQuota, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneStrings(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
