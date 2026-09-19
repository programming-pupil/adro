package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/core"
	coreencoding "github.com/adro-project/adro/core/encoding"
	"github.com/adro-project/adro/internal/durable"
)

const timerSchemaVersion = 1

const (
	TimerPending   = "pending"
	TimerClaimed   = "claimed"
	TimerFired     = "fired"
	TimerCancelled = "cancelled"
	TimerExpired   = "expired"

	TimerCatchUp  = "catch_up"
	TimerCoalesce = "coalesce"
	TimerExpire   = "expire"
)

var (
	ErrTimerNotFound            = errors.New("runtime timer not found")
	ErrTimerConflict            = errors.New("runtime timer conflict")
	ErrTimerLeaseLost           = errors.New("runtime timer lease is no longer owned")
	ErrTimerIdempotencyConflict = errors.New("runtime timer idempotency key conflict")
)

// TimerCommand is the durable command delivered when a timer fires. The
// idempotency key is part of the command contract, not a worker-local value.
type TimerCommand struct {
	Name           string `json:"name"`
	IdempotencyKey string `json:"idempotency_key"`
	Payload        any    `json:"payload,omitempty"`
}

// TimerSpec is the immutable scheduling request. Interval == 0 creates a
// one-shot timer; a positive interval creates a recurring timer.
type TimerSpec struct {
	ID               string
	ScheduleKey      string
	Scope            Scope
	DueAt            time.Time
	Interval         time.Duration
	Command          TimerCommand
	StreamID         string
	ExpectedSequence int64
	Generation       int64
	Policy           string
	MaxCatchUp       int
	MaxLateness      time.Duration
}

// Timer is the durable timer record. Claim ownership and fencing fields are
// persisted so a restarted worker cannot silently steal an unexpired claim.
type Timer struct {
	ID               string        `json:"id"`
	ScheduleKey      string        `json:"schedule_key"`
	Scope            Scope         `json:"scope"`
	DueAt            time.Time     `json:"due_at"`
	Interval         time.Duration `json:"interval,omitempty"`
	Command          TimerCommand  `json:"command"`
	CommandDigest    string        `json:"command_digest"`
	StreamID         string        `json:"stream_id,omitempty"`
	ExpectedSequence int64         `json:"expected_sequence"`
	Generation       int64         `json:"generation"`
	Policy           string        `json:"policy"`
	MaxCatchUp       int           `json:"max_catch_up"`
	MaxLateness      time.Duration `json:"max_lateness,omitempty"`
	State            string        `json:"state"`
	Owner            string        `json:"owner,omitempty"`
	LeaseExpiresAt   time.Time     `json:"lease_expires_at,omitempty"`
	FencingToken     int64         `json:"fencing_token"`
	ClaimKey         string        `json:"claim_key,omitempty"`
	ClaimGeneration  int64         `json:"claim_generation,omitempty"`
	ClaimMissed      int           `json:"claim_missed,omitempty"`
	ClaimAdvanceTo   time.Time     `json:"claim_advance_to,omitempty"`
	SuppressedCount  int64         `json:"suppressed_count,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	CancelledAt      time.Time     `json:"cancelled_at,omitempty"`
	ExpiredAt        time.Time     `json:"expired_at,omitempty"`
	TerminalReason   string        `json:"terminal_reason,omitempty"`
}

type TimerExecution struct {
	OccurrenceKey string    `json:"occurrence_key"`
	TimerID       string    `json:"timer_id"`
	Generation    int64     `json:"generation"`
	Status        string    `json:"status"`
	CompletedAt   time.Time `json:"completed_at"`
}

// TimerClaim is the worker capability for one occurrence. The fencing token
// must accompany acknowledgement or release; the occurrence key makes a
// duplicate delivery converge on one durable command result.
type TimerClaim struct {
	Timer         Timer
	OccurrenceKey string
	Missed        int
	Coalesced     bool
}

type TimerExplanation struct {
	Timer         Timer  `json:"timer"`
	Reason        string `json:"reason"`
	NextAction    string `json:"next_action"`
	OccurrenceKey string `json:"occurrence_key,omitempty"`
}

type TimerStoreOptions struct {
	Clock core.Clock
	IDs   core.IDGenerator
}

type timerStoreState struct {
	Version    int                       `json:"version"`
	Revision   int64                     `json:"revision"`
	Timers     map[string]Timer          `json:"timers"`
	Executions map[string]TimerExecution `json:"executions"`
}

// TimerStore is the reference durable timer backend. It uses an atomic JSON
// snapshot plus an inter-process lock, matching the local runtime journal's
// crash boundary. A database adapter can implement the same operations later.
type TimerStore struct {
	mu         sync.RWMutex
	path       string
	revision   int64
	timers     map[string]Timer
	executions map[string]TimerExecution
	clock      core.Clock
	ids        core.IDGenerator
}

func NewTimerStore(path string, options TimerStoreOptions) (*TimerStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("durable timer store path is required")
	}
	if options.Clock == nil {
		options.Clock = core.SystemClock{}
	}
	if options.IDs == nil {
		options.IDs = &core.CryptoIDs{}
	}
	store := &TimerStore{path: path, timers: map[string]Timer{}, executions: map[string]TimerExecution{}, clock: options.Clock, ids: options.IDs}
	if err := store.loadFromDisk(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *TimerStore) Schedule(spec TimerSpec) (Timer, bool, error) {
	if err := validateTimerSpec(spec); err != nil {
		return Timer{}, false, err
	}
	var result Timer
	created := false
	err := s.mutate(func() error {
		if existing, ok := s.findScheduleLocked(spec.Scope, spec.ScheduleKey); ok {
			// Equal schedules are the only idempotent retry. A changed due
			// time, interval, or command must be explicit and cannot silently
			// rewrite a durable timer.
			if existing.CommandDigest != commandDigest(spec.Command) || !existing.DueAt.Equal(spec.DueAt.UTC()) || existing.Interval != spec.Interval {
				return ErrTimerIdempotencyConflict
			}
			result = cloneTimer(existing)
			return nil
		}
		id := strings.TrimSpace(spec.ID)
		if id == "" {
			id = s.ids.NewID("timer")
		}
		if _, ok := s.timers[id]; ok {
			return ErrTimerIdempotencyConflict
		}
		now := s.clock.Now().UTC()
		timer := Timer{
			ID: id, ScheduleKey: strings.TrimSpace(spec.ScheduleKey), Scope: spec.Scope,
			DueAt: spec.DueAt.UTC(), Interval: spec.Interval, Command: cloneCommand(spec.Command),
			CommandDigest: commandDigest(spec.Command), StreamID: strings.TrimSpace(spec.StreamID),
			ExpectedSequence: spec.ExpectedSequence, Generation: spec.Generation, Policy: normalizeTimerPolicy(spec.Policy),
			MaxCatchUp: normalizeMaxCatchUp(spec.MaxCatchUp), MaxLateness: spec.MaxLateness,
			State: TimerPending, CreatedAt: now, UpdatedAt: now,
		}
		if timer.Generation < 0 {
			return errors.New("timer generation cannot be negative")
		}
		s.timers[id] = timer
		result, created = cloneTimer(timer), true
		return nil
	})
	return result, created, err
}

func (s *TimerStore) Get(id string) (Timer, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Timer{}, ErrTimerNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return Timer{}, err
	}
	timer, ok := s.timers[id]
	if !ok {
		return Timer{}, ErrTimerNotFound
	}
	return cloneTimer(timer), nil
}

func (s *TimerStore) List(scope Scope, includeTerminal bool) ([]Timer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return nil, err
	}
	result := make([]Timer, 0, len(s.timers))
	for _, timer := range s.timers {
		if scope.valid() && timer.Scope != scope {
			continue
		}
		if !includeTerminal && (timer.State == TimerFired || timer.State == TimerCancelled || timer.State == TimerExpired) {
			continue
		}
		result = append(result, cloneTimer(timer))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].DueAt.Equal(result[j].DueAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].DueAt.Before(result[j].DueAt)
	})
	return result, nil
}

// ClaimDue atomically claims the oldest due timers. It is deliberately
// bounded by limit, and each expired recurrence is either coalesced or
// suppressed according to its persisted policy, so downtime cannot create an
// unbounded execution burst.
func (s *TimerStore) ClaimDue(now time.Time, owner string, ttl time.Duration, limit int) ([]TimerClaim, error) {
	if strings.TrimSpace(owner) == "" || ttl <= 0 || limit <= 0 {
		return nil, errors.New("timer owner, positive ttl and positive limit are required")
	}
	if now.IsZero() {
		now = s.clock.Now()
	}
	now = now.UTC()
	claims := make([]TimerClaim, 0, limit)
	err := s.mutate(func() error {
		candidates := make([]Timer, 0, len(s.timers))
		for _, timer := range s.timers {
			if timer.State == TimerCancelled || timer.State == TimerFired || timer.State == TimerExpired || timer.DueAt.After(now) {
				continue
			}
			if timer.State == TimerClaimed && timer.LeaseExpiresAt.After(now) {
				continue
			}
			candidates = append(candidates, timer)
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].DueAt.Equal(candidates[j].DueAt) {
				return candidates[i].ID < candidates[j].ID
			}
			return candidates[i].DueAt.Before(candidates[j].DueAt)
		})
		for _, candidate := range candidates {
			if len(claims) >= limit {
				break
			}
			timer := s.timers[candidate.ID]
			missed := missedOccurrences(timer, now)
			if shouldExpire(timer, now, missed) {
				timer.State, timer.ExpiredAt, timer.TerminalReason = TimerExpired, now, "timer_catch_up_expired"
				timer.Owner, timer.ClaimKey = "", ""
				timer.UpdatedAt = now
				s.timers[timer.ID] = timer
				continue
			}
			advanceTo := time.Time{}
			coalesced := false
			suppressed := int64(0)
			if timer.Interval > 0 {
				switch timer.Policy {
				case TimerCoalesce:
					if missed > 0 {
						coalesced, advanceTo = true, now
					}
				case TimerCatchUp:
					if timer.MaxCatchUp > 0 && missed > timer.MaxCatchUp {
						suppressed = int64(missed - timer.MaxCatchUp)
						advanceTo = now
					}
				case TimerExpire:
					// shouldExpire handled the expiry bound above.
				}
			}
			if suppressed > 0 {
				timer.SuppressedCount += suppressed
			}
			timer.State = TimerClaimed
			timer.Owner = owner
			timer.LeaseExpiresAt = now.Add(ttl)
			timer.FencingToken++
			timer.ClaimGeneration = timer.Generation
			timer.ClaimKey = occurrenceKey(timer)
			timer.ClaimMissed = missed
			timer.ClaimAdvanceTo = advanceTo
			timer.UpdatedAt = now
			s.timers[timer.ID] = timer
			claims = append(claims, TimerClaim{Timer: cloneTimer(timer), OccurrenceKey: timer.ClaimKey, Missed: missed, Coalesced: coalesced || advanceTo.After(timer.DueAt)})
		}
		return nil
	})
	return claims, err
}

// Acknowledge marks a claimed occurrence complete and, for recurring timers,
// schedules the next occurrence in the same atomic snapshot.
func (s *TimerStore) Acknowledge(id, owner string, fencingToken int64, occurrenceKey string, now time.Time) (TimerExecution, error) {
	if strings.TrimSpace(occurrenceKey) == "" {
		return TimerExecution{}, errors.New("timer occurrence_key is required")
	}
	return s.finish(id, owner, fencingToken, occurrenceKey, now, "completed")
}

func (s *TimerStore) ReleaseClaim(id, owner string, fencingToken int64, occurrenceKey string, now time.Time) error {
	if strings.TrimSpace(occurrenceKey) == "" {
		return errors.New("timer occurrence_key is required")
	}
	if now.IsZero() {
		now = s.clock.Now()
	}
	now = now.UTC()
	return s.mutate(func() error {
		timer, err := s.claimedTimerLocked(id, owner, fencingToken, occurrenceKey, now)
		if err != nil {
			return err
		}
		timer.State, timer.Owner, timer.ClaimKey = TimerPending, "", ""
		timer.LeaseExpiresAt, timer.ClaimAdvanceTo = time.Time{}, time.Time{}
		timer.UpdatedAt = now
		s.timers[timer.ID] = timer
		return nil
	})
}

func (s *TimerStore) Cancel(id, reason string) (Timer, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Timer{}, ErrTimerNotFound
	}
	if strings.TrimSpace(reason) == "" {
		reason = "cancelled_by_operator"
	}
	var result Timer
	err := s.mutate(func() error {
		timer, ok := s.timers[id]
		if !ok {
			return ErrTimerNotFound
		}
		if timer.State == TimerFired || timer.State == TimerExpired {
			return ErrTimerConflict
		}
		if timer.State == TimerCancelled {
			if timer.TerminalReason != strings.TrimSpace(reason) {
				return ErrTimerConflict
			}
			result = cloneTimer(timer)
			return nil
		}
		timer.State, timer.TerminalReason = TimerCancelled, strings.TrimSpace(reason)
		timer.CancelledAt, timer.UpdatedAt = s.clock.Now().UTC(), s.clock.Now().UTC()
		timer.Owner, timer.ClaimKey, timer.LeaseExpiresAt = "", "", time.Time{}
		s.timers[id] = timer
		result = cloneTimer(timer)
		return nil
	})
	return result, err
}

func (s *TimerStore) Explain(id string) (TimerExplanation, error) {
	timer, err := s.Get(id)
	if err != nil {
		return TimerExplanation{}, err
	}
	explanation := TimerExplanation{Timer: timer, OccurrenceKey: occurrenceKey(timer)}
	switch timer.State {
	case TimerPending:
		explanation.Reason, explanation.NextAction = "waiting_for_due_time", "claim_when_due"
	case TimerClaimed:
		explanation.Reason, explanation.NextAction = "claimed_by_worker", "acknowledge_or_release_claim"
	case TimerFired:
		explanation.Reason, explanation.NextAction = "completed", "none"
	case TimerCancelled:
		explanation.Reason, explanation.NextAction = timer.TerminalReason, "none"
	case TimerExpired:
		explanation.Reason, explanation.NextAction = timer.TerminalReason, "manual_review_or_reschedule"
	default:
		explanation.Reason, explanation.NextAction = "unknown_state", "manual_review"
	}
	return explanation, nil
}

func (s *TimerStore) finish(id, owner string, fencingToken int64, occurrenceKey string, now time.Time, status string) (TimerExecution, error) {
	if now.IsZero() {
		now = s.clock.Now()
	}
	now = now.UTC()
	var result TimerExecution
	err := s.mutate(func() error {
		if prior, ok := s.executions[occurrenceKey]; ok {
			result = prior
			return nil
		}
		timer, err := s.claimedTimerLocked(id, owner, fencingToken, occurrenceKey, now)
		if err != nil {
			return err
		}
		result = TimerExecution{OccurrenceKey: occurrenceKey, TimerID: timer.ID, Generation: timer.ClaimGeneration, Status: status, CompletedAt: now}
		s.executions[occurrenceKey] = result
		if timer.Interval <= 0 {
			timer.State, timer.TerminalReason = TimerFired, "completed"
		} else {
			next := timer.DueAt.Add(timer.Interval)
			if !timer.ClaimAdvanceTo.IsZero() {
				next = timer.ClaimAdvanceTo.Add(timer.Interval)
			}
			timer.DueAt, timer.Generation, timer.State = next, timer.Generation+1, TimerPending
		}
		timer.Owner, timer.ClaimKey = "", ""
		timer.LeaseExpiresAt, timer.ClaimAdvanceTo = time.Time{}, time.Time{}
		timer.UpdatedAt = now
		s.timers[timer.ID] = timer
		return nil
	})
	return result, err
}

func (s *TimerStore) claimedTimerLocked(id, owner string, fencingToken int64, occurrenceKey string, now time.Time) (Timer, error) {
	timer, ok := s.timers[strings.TrimSpace(id)]
	if !ok {
		return Timer{}, ErrTimerNotFound
	}
	if timer.State != TimerClaimed || timer.Owner != strings.TrimSpace(owner) || timer.FencingToken != fencingToken || timer.ClaimKey != occurrenceKey || !timer.LeaseExpiresAt.After(now) {
		return Timer{}, ErrTimerLeaseLost
	}
	return timer, nil
}

func (s *TimerStore) findScheduleLocked(scope Scope, key string) (Timer, bool) {
	for _, timer := range s.timers {
		if timer.Scope == scope && timer.ScheduleKey == key {
			return timer, true
		}
	}
	return Timer{}, false
}

func (s *TimerStore) mutate(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return durable.WithExclusive(s.path, func() error {
		if err := s.loadLocked(); err != nil {
			return err
		}
		beforeRevision := s.revision
		beforeTimers := cloneTimers(s.timers)
		beforeExecutions := cloneExecutions(s.executions)
		rollback := func() {
			s.revision = beforeRevision
			s.timers = beforeTimers
			s.executions = beforeExecutions
		}
		if err := fn(); err != nil {
			rollback()
			return err
		}
		if err := s.persistLocked(); err != nil {
			rollback()
			return err
		}
		return nil
	})
}

func (s *TimerStore) loadFromDisk() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *TimerStore) loadLocked() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.revision, s.timers, s.executions = 0, map[string]Timer{}, map[string]TimerExecution{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read timer store: %w", err)
	}
	var state timerStoreState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode timer store: %w", err)
	}
	if err := validateTimerState(state); err != nil {
		return err
	}
	s.revision = state.Revision
	s.timers, s.executions = cloneTimers(state.Timers), cloneExecutions(state.Executions)
	return nil
}

func (s *TimerStore) persistLocked() error {
	state := timerStoreState{Version: timerSchemaVersion, Revision: s.revision + 1, Timers: cloneTimers(s.timers), Executions: cloneExecutions(s.executions)}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".adro-timer-*")
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
	if err := durable.Inject("timer.persist.before_rename"); err != nil {
		return err
	}
	if err := os.Rename(name, s.path); err != nil {
		return err
	}
	s.revision = state.Revision
	return nil
}

func validateTimerSpec(spec TimerSpec) error {
	if !spec.Scope.valid() {
		return errors.New("timer scope is required")
	}
	if spec.DueAt.IsZero() {
		return errors.New("timer due_at is required")
	}
	if spec.Interval < 0 || spec.MaxLateness < 0 {
		return errors.New("timer interval and max_lateness cannot be negative")
	}
	if strings.TrimSpace(spec.ScheduleKey) == "" {
		return errors.New("timer schedule_key is required")
	}
	if strings.TrimSpace(spec.Command.Name) == "" || strings.TrimSpace(spec.Command.IdempotencyKey) == "" {
		return errors.New("timer command name and idempotency_key are required")
	}
	if _, err := coreencoding.Digest(spec.Command.Payload); err != nil {
		return fmt.Errorf("timer command payload: %w", err)
	}
	if spec.ExpectedSequence < 0 || spec.Generation < 0 {
		return errors.New("timer expected sequence and generation cannot be negative")
	}
	return nil
}

func normalizeTimerPolicy(policy string) string {
	switch strings.TrimSpace(policy) {
	case TimerCoalesce:
		return TimerCoalesce
	case TimerExpire:
		return TimerExpire
	default:
		return TimerCatchUp
	}
}

func normalizeMaxCatchUp(value int) int {
	if value <= 0 {
		return 16
	}
	return value
}

func commandDigest(command TimerCommand) string {
	digest, _ := coreencoding.Digest(struct {
		Name    string `json:"name"`
		Payload any    `json:"payload"`
	}{Name: strings.TrimSpace(command.Name), Payload: command.Payload})
	return digest
}

func missedOccurrences(timer Timer, now time.Time) int {
	if timer.Interval <= 0 || timer.DueAt.After(now) {
		return 0
	}
	return int(now.Sub(timer.DueAt) / timer.Interval)
}

func shouldExpire(timer Timer, now time.Time, missed int) bool {
	if timer.MaxLateness > 0 && now.Sub(timer.DueAt) > timer.MaxLateness {
		return true
	}
	return timer.Policy == TimerExpire && timer.Interval > 0 && timer.MaxCatchUp > 0 && missed > timer.MaxCatchUp
}

func occurrenceKey(timer Timer) string {
	return timer.Command.IdempotencyKey + ":generation:" + strconv.FormatInt(timer.Generation, 10)
}

func validateTimerState(state timerStoreState) error {
	if state.Version != 0 && state.Version != timerSchemaVersion {
		return fmt.Errorf("%w: unsupported timer store version %d", ErrTimerConflict, state.Version)
	}
	for id, timer := range state.Timers {
		if id == "" || timer.ID != id || !timer.Scope.valid() || timer.DueAt.IsZero() || timer.CommandDigest != commandDigest(timer.Command) || timer.State == "" {
			return fmt.Errorf("%w: invalid timer %q", ErrTimerConflict, id)
		}
	}
	for key, execution := range state.Executions {
		if key == "" || execution.OccurrenceKey != key || execution.TimerID == "" || execution.Status == "" {
			return fmt.Errorf("%w: invalid timer execution %q", ErrTimerConflict, key)
		}
	}
	return nil
}

func cloneCommand(command TimerCommand) TimerCommand {
	copy := command
	if command.Payload != nil {
		data, err := json.Marshal(command.Payload)
		if err == nil {
			var payload any
			if json.Unmarshal(data, &payload) == nil {
				copy.Payload = payload
			}
		}
	}
	return copy
}

func cloneTimer(timer Timer) Timer {
	timer.Command = cloneCommand(timer.Command)
	return timer
}

func cloneTimers(input map[string]Timer) map[string]Timer {
	output := make(map[string]Timer, len(input))
	for key, value := range input {
		output[key] = cloneTimer(value)
	}
	return output
}

func cloneExecutions(input map[string]TimerExecution) map[string]TimerExecution {
	output := make(map[string]TimerExecution, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
