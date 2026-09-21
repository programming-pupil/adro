package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	coreencoding "github.com/adro-project/adro/core/encoding"
	coreevent "github.com/adro-project/adro/core/event"
	"github.com/adro-project/adro/ports/eventstore"
)

const defaultShadowTimeout = 2 * time.Second

const (
	shadowFailureThreshold = 3
	shadowRetryBase        = 100 * time.Millisecond
	shadowRetryMaximum     = 5 * time.Second
)

var ErrShadowDiverged = errors.New("runtime EventStore shadow projection diverged")

// ShadowReport compares the legacy runtime journal with its EventStore shadow.
// It is diagnostic evidence only and never authorizes execution or effects.
type ShadowReport struct {
	Scope         Scope     `json:"scope"`
	StreamID      string    `json:"stream_id"`
	TargetCount   int64     `json:"target_count,omitempty"`
	LegacyCount   int64     `json:"legacy_count"`
	ShadowCount   int64     `json:"shadow_count"`
	LegacyDigest  string    `json:"legacy_digest,omitempty"`
	ShadowDigest  string    `json:"shadow_digest,omitempty"`
	Diverged      bool      `json:"diverged"`
	DivergenceAt  int64     `json:"divergence_at,omitempty"`
	Pending       bool      `json:"pending,omitempty"`
	Attempts      int       `json:"attempts,omitempty"`
	Degraded      bool      `json:"degraded,omitempty"`
	NextAttemptAt time.Time `json:"next_attempt_at,omitempty"`
	Error         string    `json:"error,omitempty"`
	CheckedAt     time.Time `json:"checked_at"`
}

func (r ShadowReport) Matched() bool {
	return !r.Pending && !r.Degraded && r.Error == "" && !r.Diverged &&
		r.LegacyCount == r.ShadowCount && r.LegacyDigest == r.ShadowDigest
}

type shadowWork struct {
	Scope         Scope     `json:"scope"`
	TargetCount   int64     `json:"target_count"`
	Attempts      int       `json:"attempts,omitempty"`
	NextAttemptAt time.Time `json:"next_attempt_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

// EventShadow is the migration-only sink used by the legacy journal. Mirror
// implementations must not publish outbox messages or execute side effects.
type EventShadow interface {
	Mirror(context.Context, Scope, []Event) ShadowReport
}

// SetShadowWithError enables migration-only dual writes. It durably enqueues
// every existing scope before starting the worker; external EventStore I/O is
// never performed while the journal mutex is held.
func (j *Journal) SetShadowWithError(shadow EventShadow, timeout time.Duration) error {
	if shadow == nil {
		return errors.New("runtime EventStore shadow is required")
	}
	if timeout <= 0 {
		timeout = defaultShadowTimeout
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.shadowClosing {
		return errors.New("runtime EventStore shadow is shutting down")
	}
	previousShadow, previousTimeout := j.shadow, j.shadowTimeout
	j.shadow, j.shadowTimeout = shadow, timeout
	committed, err := j.persistCandidateLocked(
		j.events, j.leases, j.effects, j.shadowPending, j.shadowReports,
	)
	if err != nil {
		j.shadow, j.shadowTimeout = previousShadow, previousTimeout
		return fmt.Errorf("enqueue runtime EventStore shadow: %w", err)
	}
	j.applyStateLocked(committed)
	if j.shadowDone == nil {
		ctx, cancel := context.WithCancel(context.Background())
		j.shadowNotify = make(chan struct{}, 1)
		j.shadowCancel = cancel
		j.shadowDone = make(chan struct{})
		go j.runShadowWorker(ctx, j.shadowDone)
	}
	j.notifyShadowChangedLocked()
	j.wakeShadowLocked()
	return nil
}

// WaitShadow blocks until every durable shadow work item has been mirrored.
// It is intended for migration gates and tests, not the authoritative append
// path. Failed work remains pending and therefore respects the caller's bound.
func (j *Journal) WaitShadow(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		j.mu.Lock()
		if err := j.reloadLocked(); err != nil {
			j.mu.Unlock()
			return err
		}
		configured := j.shadow != nil
		pending := len(j.shadowPending)
		changed := j.shadowChanged
		closing := j.shadowClosing
		j.mu.Unlock()
		if !configured {
			return errors.New("runtime EventStore shadow is not configured")
		}
		if pending == 0 {
			return nil
		}
		if closing {
			return errors.New("runtime EventStore shadow stopped with pending work")
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return fmt.Errorf("wait for runtime EventStore shadow: %w", ctx.Err())
		}
	}
}

// Shutdown cancels the owned worker and waits for it to stop. It does not
// discard pending work or keep retrying during shutdown; unfinished work
// remains durable for a later Journal instance to resume. The caller's
// context bounds any in-flight EventStore operation that does not honor
// cancellation.
func (j *Journal) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	j.mu.Lock()
	j.shadowClosing = true
	done, cancel := j.shadowDone, j.shadowCancel
	if cancel != nil {
		// Shutdown is a stop operation, rather than another opportunity to
		// retry an unavailable migration target. The durable queue remains in
		// the journal and will be resumed by the next configured worker.
		cancel()
	}
	j.notifyShadowChangedLocked()
	j.wakeShadowLocked()
	j.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		j.mu.RLock()
		pending := len(j.shadowPending)
		j.mu.RUnlock()
		if pending != 0 {
			return fmt.Errorf("shutdown runtime EventStore shadow with %d pending work item(s)", pending)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutdown runtime EventStore shadow: %w", ctx.Err())
	}
}

func (j *Journal) ShadowReports() []ShadowReport {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.shadowReportsLocked()
}

func (j *Journal) signalShadowLocked() {
	j.notifyShadowChangedLocked()
	j.wakeShadowLocked()
}

func (j *Journal) shadowReportsLocked() []ShadowReport {
	keys := make(map[string]struct{}, len(j.shadowReports)+len(j.shadowPending))
	for key := range j.shadowReports {
		keys[key] = struct{}{}
	}
	for key := range j.shadowPending {
		keys[key] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	reports := make([]ShadowReport, 0, len(ordered))
	for _, key := range ordered {
		report := j.shadowReports[key]
		if work, pending := j.shadowPending[key]; pending {
			if !report.Scope.valid() {
				report.Scope = work.Scope
				report.StreamID = ShadowStreamID(work.Scope)
			}
			report.TargetCount = work.TargetCount
			report.Pending = true
			report.Attempts = work.Attempts
			report.Degraded = work.Attempts >= shadowFailureThreshold
			report.NextAttemptAt = work.NextAttemptAt
		}
		reports = append(reports, report)
	}
	return reports
}

func (j *Journal) runShadowWorker(ctx context.Context, done chan struct{}) {
	defer close(done)
	for {
		work, events, shadow, timeout, wait, stop, err := j.nextShadowWork(ctx)
		if err != nil {
			if !waitForShadowWorker(ctx, shadowRetryBase) {
				return
			}
			continue
		}
		if stop {
			return
		}
		if shadow == nil {
			if !j.waitForShadowWake(ctx, wait) {
				return
			}
			continue
		}

		mirrorCtx, cancel := context.WithTimeout(ctx, timeout)
		report := shadow.Mirror(mirrorCtx, work.Scope, events)
		mirrorErr := mirrorCtx.Err()
		cancel()
		if report.CheckedAt.IsZero() {
			report.CheckedAt = time.Now().UTC()
		}
		report.Scope = work.Scope
		if report.StreamID == "" {
			report.StreamID = ShadowStreamID(work.Scope)
		}
		report.TargetCount = int64(len(events))
		if mirrorErr != nil && report.Error == "" {
			report.Error = mirrorErr.Error()
		}
		if !j.finishShadowAttempt(work, report) && !waitForShadowWorker(ctx, shadowRetryBase) {
			return
		}
	}
}

func (j *Journal) nextShadowWork(ctx context.Context) (shadowWork, []Event, EventShadow, time.Duration, time.Duration, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return shadowWork{}, nil, nil, 0, 0, true, nil
	}
	if err := j.reloadLocked(); err != nil {
		return shadowWork{}, nil, nil, 0, 0, false, err
	}
	if len(j.shadowPending) == 0 {
		return shadowWork{}, nil, nil, 0, 0, j.shadowClosing, nil
	}
	now := time.Now().UTC()
	keys := make([]string, 0, len(j.shadowPending))
	for key := range j.shadowPending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var earliest time.Time
	for _, key := range keys {
		work := j.shadowPending[key]
		if !work.NextAttemptAt.IsZero() && work.NextAttemptAt.After(now) {
			if earliest.IsZero() || work.NextAttemptAt.Before(earliest) {
				earliest = work.NextAttemptAt
			}
			continue
		}
		events := eventsForShadowScope(j.events, work.Scope)
		return work, events, j.shadow, j.shadowTimeout, 0, false, nil
	}
	return shadowWork{}, nil, nil, 0, time.Until(earliest), false, nil
}

func (j *Journal) finishShadowAttempt(work shadowWork, report ShadowReport) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return false
	}
	key := shadowScopeKey(work.Scope)
	current, pending := j.shadowPending[key]
	if !pending {
		return true
	}
	now := time.Now().UTC()
	pendingState := cloneShadowWork(j.shadowPending)
	reportState := cloneShadowReports(j.shadowReports)
	matched := report.Error == "" && !report.Diverged &&
		report.LegacyCount == report.ShadowCount && report.LegacyDigest == report.ShadowDigest
	if matched && report.LegacyCount >= current.TargetCount {
		delete(pendingState, key)
		report.Pending = false
		report.Attempts = 0
		report.Degraded = false
		report.NextAttemptAt = time.Time{}
	} else if matched {
		current.Attempts = 0
		current.NextAttemptAt = time.Time{}
		current.UpdatedAt = now
		pendingState[key] = current
		report.Pending = true
		report.TargetCount = current.TargetCount
	} else {
		if report.Error == "" {
			report.Error = ErrShadowDiverged.Error()
		}
		current.Attempts++
		current.NextAttemptAt = now.Add(shadowRetryDelay(current.Attempts))
		current.UpdatedAt = now
		pendingState[key] = current
		report.Pending = true
		report.Attempts = current.Attempts
		report.Degraded = current.Attempts >= shadowFailureThreshold
		report.NextAttemptAt = current.NextAttemptAt
		report.TargetCount = current.TargetCount
	}
	reportState[key] = report
	committed, err := j.persistCandidateLocked(
		j.events, j.leases, j.effects, pendingState, reportState,
	)
	if err != nil {
		return false
	}
	j.applyStateLocked(committed)
	j.notifyShadowChangedLocked()
	j.wakeShadowLocked()
	return true
}

func (j *Journal) waitForShadowWake(ctx context.Context, wait time.Duration) bool {
	j.mu.RLock()
	wake := j.shadowNotify
	j.mu.RUnlock()
	if wait <= 0 {
		// Another process can enqueue work without signalling this instance.
		wait = time.Second
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-wake:
		return true
	case <-timer.C:
		return true
	}
}

func waitForShadowWorker(ctx context.Context, wait time.Duration) bool {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (j *Journal) wakeShadowLocked() {
	if j.shadowNotify == nil {
		return
	}
	select {
	case j.shadowNotify <- struct{}{}:
	default:
	}
}

func (j *Journal) notifyShadowChangedLocked() {
	if j.shadowChanged == nil {
		j.shadowChanged = make(chan struct{})
		return
	}
	close(j.shadowChanged)
	j.shadowChanged = make(chan struct{})
}

func (j *Journal) enqueueShadowScopesLocked(state *journalState) {
	if j.shadow == nil || state == nil {
		return
	}
	if state.ShadowPending == nil {
		state.ShadowPending = make(map[string]shadowWork)
	}
	counts := make(map[string]int64)
	scopes := make(map[string]Scope)
	for _, event := range state.Events {
		key := shadowScopeKey(event.Scope)
		counts[key]++
		scopes[key] = event.Scope
	}
	keys := make([]string, 0, len(scopes))
	for key := range scopes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		scope := scopes[key]
		target := counts[key]
		if report, ok := state.ShadowReports[key]; ok && report.Matched() && report.LegacyCount >= target {
			delete(state.ShadowPending, key)
			continue
		}
		work, exists := state.ShadowPending[key]
		if !exists {
			work = shadowWork{Scope: scope, UpdatedAt: time.Now().UTC()}
		}
		work.Scope = scope
		if target > work.TargetCount {
			work.TargetCount = target
			work.Attempts = 0
			work.NextAttemptAt = time.Time{}
			work.UpdatedAt = time.Now().UTC()
		}
		if report, ok := state.ShadowReports[key]; ok && report.Matched() && report.LegacyCount < target {
			work.Attempts = 0
			work.NextAttemptAt = time.Time{}
			work.UpdatedAt = time.Now().UTC()
		}
		if work.TargetCount > 0 {
			state.ShadowPending[key] = work
		}
	}
}

func eventsForShadowScope(events []Event, scope Scope) []Event {
	result := make([]Event, 0)
	for _, event := range events {
		if event.Scope == scope {
			result = append(result, cloneEvent(event))
		}
	}
	return result
}

func shadowRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		return shadowRetryBase
	}
	delay := shadowRetryBase
	for i := 1; i < attempt && delay < shadowRetryMaximum; i++ {
		delay *= 2
		if delay >= shadowRetryMaximum {
			return shadowRetryMaximum
		}
	}
	return delay
}

func cloneShadowWork(input map[string]shadowWork) map[string]shadowWork {
	output := make(map[string]shadowWork, len(input))
	for key, work := range input {
		output[key] = work
	}
	return output
}

func cloneShadowReports(input map[string]ShadowReport) map[string]ShadowReport {
	output := make(map[string]ShadowReport, len(input))
	for key, report := range input {
		output[key] = report
	}
	return output
}

func mergeShadowWork(base, overlay map[string]shadowWork) map[string]shadowWork {
	output := cloneShadowWork(base)
	for key, candidate := range overlay {
		current, exists := output[key]
		if !exists {
			output[key] = candidate
			continue
		}
		if candidate.TargetCount > current.TargetCount {
			// A new authoritative event must be mirrored even if the other
			// writer has an older failure/backoff for the same scope.
			candidate.Attempts = 0
			candidate.NextAttemptAt = time.Time{}
			candidate.UpdatedAt = maxTime(candidate.UpdatedAt, current.UpdatedAt)
			output[key] = candidate
			continue
		}
		if shadowWorkNewer(candidate, current) {
			candidate.TargetCount = current.TargetCount
			output[key] = candidate
		}
	}
	return output
}

func mergeShadowReports(base, overlay map[string]ShadowReport) map[string]ShadowReport {
	output := cloneShadowReports(base)
	for key, report := range overlay {
		current, exists := output[key]
		if !exists || shadowReportNewer(report, current) {
			output[key] = report
		}
	}
	return output
}

func rebaseShadowWork(base, candidate, disk map[string]shadowWork, candidateReports, diskReports map[string]ShadowReport) map[string]shadowWork {
	merged := cloneShadowWork(disk)
	for key, before := range base {
		after, exists := candidate[key]
		if exists && after == before {
			continue
		}
		if !exists {
			current, pending := merged[key]
			newerObservation := shadowReportNewer(diskReports[key], candidateReports[key])
			if !pending || current.TargetCount <= before.TargetCount && !newerObservation {
				delete(merged, key)
			}
			continue
		}
		if current, pending := merged[key]; pending {
			merged[key] = mergeShadowWork(map[string]shadowWork{key: current}, map[string]shadowWork{key: after})[key]
		} else if report, reported := diskReports[key]; !reported || !report.Matched() || report.LegacyCount < after.TargetCount {
			merged[key] = after
		}
	}
	for key, after := range candidate {
		if _, exists := base[key]; exists {
			continue
		}
		if current, pending := merged[key]; pending {
			merged[key] = mergeShadowWork(map[string]shadowWork{key: current}, map[string]shadowWork{key: after})[key]
		} else if report, reported := diskReports[key]; !reported || !report.Matched() || report.LegacyCount < after.TargetCount {
			merged[key] = after
		}
	}
	return merged
}

func rebaseShadowReports(base, candidate, disk map[string]ShadowReport) map[string]ShadowReport {
	changed := make(map[string]ShadowReport)
	for key, report := range candidate {
		if before, exists := base[key]; !exists || before != report {
			changed[key] = report
		}
	}
	return mergeShadowReports(disk, changed)
}

func shadowWorkNewer(candidate, current shadowWork) bool {
	if !candidate.UpdatedAt.IsZero() || !current.UpdatedAt.IsZero() {
		if candidate.UpdatedAt.IsZero() {
			return false
		}
		if current.UpdatedAt.IsZero() {
			return true
		}
		if candidate.UpdatedAt != current.UpdatedAt {
			return candidate.UpdatedAt.After(current.UpdatedAt)
		}
	}
	if candidate.Attempts != current.Attempts {
		return candidate.Attempts > current.Attempts
	}
	if candidate.NextAttemptAt.IsZero() != current.NextAttemptAt.IsZero() {
		return candidate.NextAttemptAt.IsZero()
	}
	if candidate.NextAttemptAt != current.NextAttemptAt {
		return candidate.NextAttemptAt.Before(current.NextAttemptAt)
	}
	return false
}

func shadowReportNewer(candidate, current ShadowReport) bool {
	if !candidate.CheckedAt.IsZero() || !current.CheckedAt.IsZero() {
		if candidate.CheckedAt.IsZero() {
			return false
		}
		if current.CheckedAt.IsZero() {
			return true
		}
		if candidate.CheckedAt != current.CheckedAt {
			return candidate.CheckedAt.After(current.CheckedAt)
		}
	}
	if candidate.Attempts != current.Attempts {
		return candidate.Attempts > current.Attempts
	}
	if candidate.Pending != current.Pending {
		return !candidate.Pending
	}
	return candidate.Error > current.Error
}

func maxTime(left, right time.Time) time.Time {
	if left.After(right) {
		return left
	}
	return right
}

type EventStoreShadow struct {
	store eventstore.Store
	mu    sync.Mutex
}

func NewEventStoreShadow(store eventstore.Store) (*EventStoreShadow, error) {
	if store == nil {
		return nil, errors.New("runtime EventStore shadow requires a store")
	}
	return &EventStoreShadow{store: store}, nil
}

func (s *EventStoreShadow) Mirror(ctx context.Context, scope Scope, legacy []Event) ShadowReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	report := ShadowReport{
		Scope: scope, StreamID: ShadowStreamID(scope), LegacyCount: int64(len(legacy)), CheckedAt: time.Now().UTC(),
	}
	legacy = cloneEvents(legacy)
	report.LegacyDigest = projectionDigest(legacy, &report)
	if report.Error != "" {
		return report
	}

	head, _, err := s.store.Head(ctx, report.StreamID)
	if err != nil {
		report.Error = fmt.Sprintf("read EventStore shadow head: %v", err)
		return report
	}
	if head > int64(len(legacy)) {
		report.ShadowCount = head
		report.Diverged = true
		report.DivergenceAt = int64(len(legacy)) + 1
		report.Error = ErrShadowDiverged.Error()
		return report
	}
	if head > 0 {
		prefix, err := s.readProjection(ctx, scope, report.StreamID)
		if err != nil {
			report.Error = err.Error()
			return report
		}
		report.ShadowCount = int64(len(prefix))
		report.ShadowDigest = projectionDigest(prefix, &report)
		if report.Error != "" {
			return report
		}
		if divergence := firstDivergence(legacy[:head], prefix); divergence != 0 {
			report.Diverged = true
			report.DivergenceAt = divergence
			report.Error = ErrShadowDiverged.Error()
			return report
		}
	}
	if head < int64(len(legacy)) {
		request := eventstore.AppendRequest{StreamID: report.StreamID, ExpectedSequence: head}
		for _, item := range legacy[head:] {
			mapped, mapErr := mapShadowEvent(report.StreamID, item)
			if mapErr != nil {
				report.Error = mapErr.Error()
				return report
			}
			request.Events = append(request.Events, mapped)
		}
		if _, err := s.store.Append(ctx, request); err != nil {
			report.Error = fmt.Sprintf("append EventStore shadow: %v", err)
			return report
		}
	}

	shadow, err := s.readProjection(ctx, scope, report.StreamID)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	report.ShadowCount = int64(len(shadow))
	report.ShadowDigest = projectionDigest(shadow, &report)
	if report.Error != "" {
		return report
	}
	report.DivergenceAt = firstDivergence(legacy, shadow)
	if report.LegacyCount != report.ShadowCount || report.LegacyDigest != report.ShadowDigest || report.DivergenceAt != 0 {
		report.Diverged = true
		report.Error = ErrShadowDiverged.Error()
	}
	return report
}

func (s *EventStoreShadow) readProjection(ctx context.Context, scope Scope, streamID string) ([]Event, error) {
	const pageSize = 1000
	result := make([]Event, 0)
	for after := int64(0); ; {
		batch, err := s.store.Read(ctx, streamID, after, pageSize)
		if err != nil {
			return nil, fmt.Errorf("read EventStore shadow projection: %w", err)
		}
		if len(batch) == 0 {
			return result, nil
		}
		for _, envelope := range batch {
			if envelope.TenantID != scope.TenantID || envelope.WorkspaceID != scope.WorkspaceID {
				return nil, fmt.Errorf("%w: shadow event scope mismatch at sequence %d", ErrShadowDiverged, envelope.Sequence)
			}
			var item Event
			if err := json.Unmarshal(envelope.Payload, &item); err != nil {
				return nil, fmt.Errorf("decode EventStore shadow payload at sequence %d: %w", envelope.Sequence, err)
			}
			if item.Scope != scope {
				return nil, fmt.Errorf("%w: shadow payload scope mismatch at sequence %d", ErrShadowDiverged, envelope.Sequence)
			}
			result = append(result, item)
			after = envelope.Sequence
		}
		if len(batch) < pageSize {
			return result, nil
		}
	}
}

func mapShadowEvent(streamID string, legacy Event) (coreevent.Uncommitted, error) {
	payload, err := coreencoding.Marshal(legacy)
	if err != nil {
		return coreevent.Uncommitted{}, fmt.Errorf("encode legacy runtime shadow event: %w", err)
	}
	occurredAt := legacy.CreatedAt
	if occurredAt.IsZero() {
		occurredAt = legacy.CommittedAt
	}
	correlationID := legacy.CorrelationID
	if correlationID == "" {
		correlationID = legacy.EventID
	}
	return coreevent.Uncommitted{
		StreamID: streamID, EventType: legacy.EventType,
		TenantID: legacy.TenantID, WorkspaceID: legacy.WorkspaceID,
		Actor:         coreevent.Actor{Type: "migration", ID: "shadow-migrator"},
		CorrelationID: correlationID, CausationID: legacy.CausationID,
		IdempotencyKey: "legacy-event:" + legacy.EventID, FencingToken: legacy.FencingToken,
		OccurredAt: occurredAt, Classification: "internal", Payload: payload,
	}, nil
}

func ShadowStreamID(scope Scope) string {
	digest := sha256.Sum256([]byte(scope.TenantID + "\x00" + scope.WorkspaceID + "\x00" + scope.SessionID + "\x00" + scope.RunID))
	return "runtime-shadow-" + hex.EncodeToString(digest[:16])
}

func projectionDigest(events []Event, report *ShadowReport) string {
	digest, err := coreencoding.Digest(events)
	if err != nil {
		report.Error = fmt.Sprintf("digest runtime shadow projection: %v", err)
		return ""
	}
	return digest
}

func firstDivergence(legacy, shadow []Event) int64 {
	limit := len(legacy)
	if len(shadow) < limit {
		limit = len(shadow)
	}
	for index := 0; index < limit; index++ {
		left, leftErr := coreencoding.Marshal(legacy[index])
		right, rightErr := coreencoding.Marshal(shadow[index])
		if leftErr != nil || rightErr != nil || string(left) != string(right) {
			return int64(index + 1)
		}
	}
	if len(legacy) != len(shadow) {
		return int64(limit + 1)
	}
	return 0
}

func cloneEvents(events []Event) []Event {
	result := make([]Event, len(events))
	for index, item := range events {
		result[index] = cloneEvent(item)
	}
	return result
}

func shadowScopeKey(scope Scope) string {
	return scope.TenantID + "\x00" + scope.WorkspaceID + "\x00" + scope.SessionID + "\x00" + scope.RunID
}
