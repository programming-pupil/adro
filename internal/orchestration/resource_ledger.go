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
	coreencoding "github.com/adro-project/adro/core/encoding"
	"github.com/adro-project/adro/internal/durable"
)

const resourceLedgerSchemaVersion = 1

type ResourceLedgerOptions struct {
	Clock core.Clock
	IDs   core.IDGenerator
}

type resourceLedgerState struct {
	Version      int                            `json:"version"`
	Revision     int64                          `json:"revision"`
	Quotas       map[string]ResourceQuota       `json:"quotas"`
	Reservations map[string]ResourceReservation `json:"reservations"`
	Usage        map[string]UsageRecord         `json:"usage"`
	Operations   map[string]string              `json:"operations"`
}

// ResourceLedger is the reference durable accounting backend. Mutations are
// atomic JSON snapshots under an inter-process lock. Production databases can
// implement the same reserve/record/settle contract without changing scheduler
// decisions or resource event semantics.
type ResourceLedger struct {
	mu           sync.RWMutex
	path         string
	revision     int64
	quotas       map[string]ResourceQuota
	reservations map[string]ResourceReservation
	usage        map[string]UsageRecord
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
		path: strings.TrimSpace(path), quotas: map[string]ResourceQuota{},
		reservations: map[string]ResourceReservation{}, usage: map[string]UsageRecord{},
		operations: map[string]string{}, clock: options.Clock, ids: options.IDs,
	}
	if ledger.path != "" {
		if err := ledger.loadFromDisk(); err != nil {
			return nil, err
		}
	}
	return ledger, nil
}

func (l *ResourceLedger) SetQuota(quota ResourceQuota) error {
	quota = quota.normalized()
	if quota.UpdatedAt.IsZero() {
		quota.UpdatedAt = l.clock.Now().UTC()
	}
	if err := quota.validate(); err != nil {
		return err
	}
	return l.mutate(func() error {
		l.quotas[quota.Scope.quotaKey()] = quota
		return nil
	})
}

func (l *ResourceLedger) DeleteQuota(scope ResourceScope) error {
	scope = scope.normalized()
	if err := scope.validateHierarchy(); err != nil {
		return err
	}
	return l.mutate(func() error {
		if _, ok := l.quotas[scope.quotaKey()]; !ok {
			return ErrResourceNotFound
		}
		delete(l.quotas, scope.quotaKey())
		return nil
	})
}

func (l *ResourceLedger) ListQuotas() ([]ResourceQuota, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return nil, err
	}
	result := make([]ResourceQuota, 0, len(l.quotas))
	for _, quota := range l.quotas {
		result = append(result, quota)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Scope.quotaKey() < result[j].Scope.quotaKey() })
	return result, nil
}

// CheckReservation returns every applicable tenant/workspace/agent limit in
// deterministic hierarchy order without mutating the ledger.
func (l *ResourceLedger) CheckReservation(spec ResourceReservationSpec) ([]ResourceLimitReport, error) {
	spec.Scope = spec.Scope.normalized()
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
func (l *ResourceLedger) Reserve(spec ResourceReservationSpec) (ResourceReservation, bool, []ResourceLimitReport, error) {
	spec.Scope = spec.Scope.normalized()
	spec.ParentID = strings.TrimSpace(spec.ParentID)
	spec.IdempotencyKey = strings.TrimSpace(spec.IdempotencyKey)
	if err := validateReservationSpec(spec); err != nil {
		return ResourceReservation{}, false, nil, err
	}
	digest, err := reservationDigest(spec)
	if err != nil {
		return ResourceReservation{}, false, nil, err
	}
	var result ResourceReservation
	var reports []ResourceLimitReport
	created := false
	err = l.mutate(func() error {
		if prior, ok := l.findReservationByKeyLocked(spec.IdempotencyKey); ok {
			if prior.PayloadDigest != digest {
				return ErrResourceConflict
			}
			result = cloneReservation(prior)
			reports, _ = l.limitReportsLocked(spec.Scope, ResourceVector{})
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
			return ErrResourceConflict
		}
		now := spec.CreatedAt.UTC()
		if now.IsZero() {
			now = l.clock.Now().UTC()
		}
		reservation := ResourceReservation{
			ID: id, IdempotencyKey: spec.IdempotencyKey, PayloadDigest: digest,
			Scope: spec.Scope, ParentID: spec.ParentID,
			State:  ResourceState{Requested: spec.Requested, Reserved: spec.Requested},
			Status: ResourceReserved, ExpiresAt: spec.ExpiresAt.UTC(), CreatedAt: now, UpdatedAt: now,
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

func validateReservationSpec(spec ResourceReservationSpec) error {
	if strings.TrimSpace(spec.IdempotencyKey) == "" {
		return errors.New("resource reservation idempotency_key is required")
	}
	if err := spec.Scope.validateHierarchy(); err != nil {
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
// errors.Is(err, ErrResourceHardLimit).
type ResourceLimitError struct {
	Report ResourceLimitReport
}

func (e *ResourceLimitError) Error() string {
	return fmt.Sprintf("%v: %s", ErrResourceHardLimit, e.Report.Explanation)
}

func (e *ResourceLimitError) Unwrap() error { return ErrResourceHardLimit }

func (l *ResourceLedger) GetReservation(id string) (ResourceReservation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return ResourceReservation{}, err
	}
	reservation, ok := l.reservations[strings.TrimSpace(id)]
	if !ok {
		return ResourceReservation{}, ErrResourceNotFound
	}
	return cloneReservation(reservation), nil
}

func (l *ResourceLedger) ListReservations(scope ResourceScope, includeTerminal bool) ([]ResourceReservation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return nil, err
	}
	scope = scope.normalized()
	result := make([]ResourceReservation, 0, len(l.reservations))
	for _, reservation := range l.reservations {
		if scope.TenantID != "" && !scope.matches(reservation.Scope) {
			continue
		}
		if !includeTerminal && !reservation.active() {
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
func (l *ResourceLedger) GetUsage(id string) (UsageRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return UsageRecord{}, err
	}
	record, ok := l.usage[strings.TrimSpace(id)]
	if !ok {
		return UsageRecord{}, ErrResourceNotFound
	}
	return cloneUsage(record), nil
}

func (l *ResourceLedger) RecordUsage(record UsageRecord) (UsageRecord, bool, []ResourceLimitReport, error) {
	if err := validateUsageRecord(record); err != nil {
		return UsageRecord{}, false, nil, err
	}
	var result UsageRecord
	var reports []ResourceLimitReport
	created := false
	err := l.mutate(func() error {
		if prior, ok := l.usage[record.ID]; ok {
			if prior.PayloadDigest != record.PayloadDigest {
				return ErrResourceConflict
			}
			result = cloneUsage(prior)
			return nil
		}
		reservation, ok := l.reservations[record.ReservationID]
		if !ok {
			return ErrResourceNotFound
		}
		if !sameAccountingScope(reservation.Scope, record.Scope) {
			return errors.New("usage scope does not match reservation")
		}
		if !reservation.active() && !record.DelayedBilling {
			return ErrResourceReservationTerminal
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
		reports, checkErr = l.limitReportsLocked(record.Scope, ResourceVector{})
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

func validateUsageRecord(record UsageRecord) error {
	if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.ReservationID) == "" || strings.TrimSpace(record.PayloadDigest) == "" {
		return errors.New("usage id, reservation_id and payload_digest are required")
	}
	if err := record.Scope.validateUsageAttribution(); err != nil {
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

func sameAccountingScope(reservation, usage ResourceScope) bool {
	reservation, usage = reservation.normalized(), usage.normalized()
	return reservation.TenantID == usage.TenantID && reservation.WorkspaceID == usage.WorkspaceID && reservation.AgentID == usage.AgentID
}

// Settle closes a reservation after all children have settled or released.
// Usage can still receive an explicitly delayed billing adjustment later.
func (l *ResourceLedger) Settle(id, idempotencyKey, reason string, at time.Time) (ResourceReservation, error) {
	return l.finishReservation(id, idempotencyKey, reason, at, ResourceSettled)
}

func (l *ResourceLedger) Release(id, idempotencyKey, reason string, at time.Time) (ResourceReservation, error) {
	return l.finishReservation(id, idempotencyKey, reason, at, ResourceReleased)
}

func (l *ResourceLedger) finishReservation(id, idempotencyKey, reason string, at time.Time, status ResourceReservationStatus) (ResourceReservation, error) {
	id, idempotencyKey, reason = strings.TrimSpace(id), strings.TrimSpace(idempotencyKey), strings.TrimSpace(reason)
	if id == "" || idempotencyKey == "" {
		return ResourceReservation{}, errors.New("reservation id and operation idempotency key are required")
	}
	if at.IsZero() {
		at = l.clock.Now()
	}
	at = at.UTC()
	operationDigest, err := coreencoding.Digest(struct {
		ID     string                    `json:"id"`
		Status ResourceReservationStatus `json:"status"`
		Reason string                    `json:"reason"`
	}{id, status, reason})
	if err != nil {
		return ResourceReservation{}, err
	}
	var result ResourceReservation
	err = l.mutate(func() error {
		if priorDigest, ok := l.operations[idempotencyKey]; ok {
			if priorDigest != operationDigest {
				return ErrResourceConflict
			}
			prior, ok := l.reservations[id]
			if !ok {
				return ErrResourceNotFound
			}
			result = cloneReservation(prior)
			return nil
		}
		reservation, ok := l.reservations[id]
		if !ok {
			return ErrResourceNotFound
		}
		if !reservation.active() {
			return ErrResourceReservationTerminal
		}
		if l.hasActiveChildrenLocked(id) {
			return ErrResourceActiveChildren
		}
		reservation.Status = status
		reservation.TerminalAt, reservation.UpdatedAt = at, at
		if reason == "" {
			if status == ResourceSettled {
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
func (l *ResourceLedger) ReapOrphans(now time.Time) ([]ResourceReservation, error) {
	if now.IsZero() {
		now = l.clock.Now()
	}
	now = now.UTC()
	var expired []ResourceReservation
	err := l.mutate(func() error {
		ids := make([]string, 0)
		for id, reservation := range l.reservations {
			if reservation.active() && !reservation.ExpiresAt.IsZero() && !reservation.ExpiresAt.After(now) {
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
			if !reservation.active() || l.hasActiveChildrenLocked(id) {
				continue
			}
			reservation.Status = ResourceExpired
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

func (l *ResourceLedger) checkParentLocked(spec ResourceReservationSpec) error {
	if spec.ParentID == "" {
		return nil
	}
	parent, ok := l.reservations[spec.ParentID]
	if !ok {
		return ErrResourceNotFound
	}
	if !parent.active() {
		return ErrResourceReservationTerminal
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
		allocated, err = allocated.Add(child.liability())
		if err != nil {
			return err
		}
	}
	projected, err := allocated.Add(spec.Requested)
	if err != nil {
		return err
	}
	if excess := projected.Excess(parent.State.Reserved); !excess.IsZero() {
		return fmt.Errorf("%w: parent=%s excess=%+v", ErrResourceParentExhausted, parent.ID, excess)
	}
	return nil
}

func (l *ResourceLedger) addConsumptionLocked(id string, delta ResourceVector) error {
	currentID := id
	visited := map[string]bool{}
	for currentID != "" {
		if visited[currentID] {
			return errors.New("resource reservation parent cycle")
		}
		visited[currentID] = true
		reservation, ok := l.reservations[currentID]
		if !ok {
			return ErrResourceNotFound
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

func (l *ResourceLedger) recomputeTerminalStateLocked(reservation *ResourceReservation) {
	if reservation == nil || reservation.active() {
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
		if reservation.ParentID == parentID && reservation.active() {
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

func (l *ResourceLedger) findReservationByKeyLocked(key string) (ResourceReservation, bool) {
	for _, reservation := range l.reservations {
		if reservation.IdempotencyKey == key {
			return reservation, true
		}
	}
	return ResourceReservation{}, false
}

func (l *ResourceLedger) limitReportsLocked(scope ResourceScope, requested ResourceVector) ([]ResourceLimitReport, error) {
	quotas := make([]ResourceQuota, 0, 3)
	for _, quota := range l.quotas {
		if quota.Scope.matches(scope) {
			quotas = append(quotas, quota)
		}
	}
	sort.Slice(quotas, func(i, j int) bool {
		return quotaSpecificity(quotas[i].Scope) < quotaSpecificity(quotas[j].Scope)
	})
	reports := make([]ResourceLimitReport, 0, len(quotas))
	for _, quota := range quotas {
		current, err := l.exposureLocked(quota.Scope)
		if err != nil {
			return nil, err
		}
		projected, err := current.Add(requested)
		if err != nil {
			return nil, err
		}
		report := ResourceLimitReport{
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

func quotaSpecificity(scope ResourceScope) int {
	if scope.AgentID != "" {
		return 2
	}
	if scope.WorkspaceID != "" {
		return 1
	}
	return 0
}

func (l *ResourceLedger) exposureLocked(scope ResourceScope) (ResourceVector, error) {
	var total ResourceVector
	for _, reservation := range l.reservations {
		if !scope.matches(reservation.Scope) || l.hasMatchingAncestorLocked(reservation, scope) {
			continue
		}
		var err error
		total, err = total.Add(reservation.liability())
		if err != nil {
			return ResourceVector{}, err
		}
	}
	return total, nil
}

func (l *ResourceLedger) hasMatchingAncestorLocked(reservation ResourceReservation, scope ResourceScope) bool {
	seen := map[string]bool{}
	parentID := reservation.ParentID
	for parentID != "" && !seen[parentID] {
		seen[parentID] = true
		parent, ok := l.reservations[parentID]
		if !ok {
			return false
		}
		if scope.matches(parent.Scope) {
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
	Scope                ResourceScope             `json:"scope"`
	Exposure             ResourceVector            `json:"exposure"`
	Consumed             ResourceVector            `json:"consumed"`
	ActiveReservations   []ResourceReservation     `json:"active_reservations,omitempty"`
	RecentUsage          []UsageRecord             `json:"recent_usage,omitempty"`
	OverageReservations  []string                  `json:"overage_reservation_ids,omitempty"`
	MissingProviderUsage int                       `json:"missing_provider_usage"`
	DelayedBills         int                       `json:"delayed_bills"`
	ChildAgentUsage      map[string]ResourceVector `json:"child_agent_usage,omitempty"`
	GeneratedAt          time.Time                 `json:"generated_at"`
}

func (l *ResourceLedger) Dashboard(scope ResourceScope, usageLimit int) (ResourceDashboard, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.loadLocked(); err != nil {
		return ResourceDashboard{}, err
	}
	scope = scope.normalized()
	if err := scope.validateHierarchy(); err != nil {
		return ResourceDashboard{}, err
	}
	if usageLimit <= 0 || usageLimit > 1000 {
		usageLimit = 100
	}
	exposure, err := l.exposureLocked(scope)
	if err != nil {
		return ResourceDashboard{}, err
	}
	dashboard := ResourceDashboard{Scope: scope, Exposure: exposure, ChildAgentUsage: map[string]ResourceVector{}, GeneratedAt: l.clock.Now().UTC()}
	for _, reservation := range l.reservations {
		if !scope.matches(reservation.Scope) {
			continue
		}
		if reservation.active() {
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
	usage := make([]UsageRecord, 0)
	for _, record := range l.usage {
		if !scope.matches(record.Scope) {
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
		l.quotas = map[string]ResourceQuota{}
		l.reservations = map[string]ResourceReservation{}
		l.usage = map[string]UsageRecord{}
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
		if quota.Scope.quotaKey() != key {
			return fmt.Errorf("resource quota key mismatch %q", key)
		}
		if err := quota.validate(); err != nil {
			return fmt.Errorf("resource quota %q: %w", key, err)
		}
	}
	for id, reservation := range state.Reservations {
		if reservation.ID != id || reservation.IdempotencyKey == "" || reservation.PayloadDigest == "" {
			return fmt.Errorf("invalid resource reservation %q", id)
		}
		if err := reservation.Scope.validateHierarchy(); err != nil {
			return fmt.Errorf("resource reservation %q: %w", id, err)
		}
		for _, vector := range []ResourceVector{reservation.State.Requested, reservation.State.Reserved, reservation.State.Consumed, reservation.State.Released, reservation.State.Overage, reservation.OwnConsumed} {
			if err := vector.Validate(); err != nil {
				return fmt.Errorf("resource reservation %q: %w", id, err)
			}
		}
		switch reservation.Status {
		case ResourceReserved, ResourceSettled, ResourceReleased, ResourceExpired:
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

func cloneReservation(value ResourceReservation) ResourceReservation {
	value.SoftActions = append([]string(nil), value.SoftActions...)
	return value
}

func cloneReservations(values map[string]ResourceReservation) map[string]ResourceReservation {
	result := make(map[string]ResourceReservation, len(values))
	for key, value := range values {
		result[key] = cloneReservation(value)
	}
	return result
}

func cloneUsage(value UsageRecord) UsageRecord {
	value.RawProvider = append(json.RawMessage(nil), value.RawProvider...)
	return value
}

func cloneUsageMap(values map[string]UsageRecord) map[string]UsageRecord {
	result := make(map[string]UsageRecord, len(values))
	for key, value := range values {
		result[key] = cloneUsage(value)
	}
	return result
}

func cloneQuotas(values map[string]ResourceQuota) map[string]ResourceQuota {
	result := make(map[string]ResourceQuota, len(values))
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
