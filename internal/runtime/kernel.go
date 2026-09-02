// Package runtime contains ADRO-owned execution primitives.  Providers are
// adapters around this package: they may supply model output, but they do not
// own the durable record of what ADRO authorized, started, or committed.
package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/durable"
)

const (
	SchemaVersion = 1

	StatusPending         = "pending"
	StatusCommitted       = "committed"
	StatusRejected        = "rejected"
	StatusRecoveryNeeded  = "recovery_required"
	EventTurnStarted      = "turn.started"
	EventTurnFinished     = "turn.finished"
	EventTurnCheckpointed = "turn.checkpointed"
	EventToolAuthorized   = "tool.authorized"
	EventToolStarted      = "tool.started"
	EventToolApproved     = "tool.approved"
	EventToolFinished     = "tool.finished"
	EventInteraction      = "interaction.accepted"
	EventUsage            = "usage.recorded"
	EventEffectFenced     = "effect.fenced"
)

var (
	ErrCorrupt             = errors.New("runtime journal is corrupt")
	ErrConflict            = errors.New("runtime journal conflict")
	ErrIdempotencyConflict = errors.New("runtime idempotency key conflict")
	ErrLeaseBusy           = errors.New("runtime lease is held by another owner")
	ErrLeaseLost           = errors.New("runtime lease is no longer owned")
	ErrUnauthorized        = errors.New("runtime tool is not authorized")
)

// Scope is copied into every journal record.  It is deliberately explicit so
// a cursor or replay from one tenant/session cannot be applied to another.
type Scope struct {
	TenantID    string `json:"tenant_id"`
	WorkspaceID string `json:"workspace_id"`
	SessionID   string `json:"session_id"`
	RunID       string `json:"run_id"`
}

func (s Scope) valid() bool {
	return strings.TrimSpace(s.TenantID) != "" && strings.TrimSpace(s.WorkspaceID) != "" &&
		strings.TrimSpace(s.SessionID) != "" && strings.TrimSpace(s.RunID) != ""
}

// Event is the single durable execution envelope. PayloadHash authenticates
// the canonical payload and EnvelopeHash authenticates every other field,
// including scope, sequence, writer and fencing token.
type Event struct {
	EventID       string `json:"event_id"`
	SchemaVersion int    `json:"schema_version"`
	Sequence      int64  `json:"sequence"`
	EventType     string `json:"event_type"`
	AggregateType string `json:"aggregate_type"`
	AggregateID   string `json:"aggregate_id"`
	Scope
	CorrelationID  string          `json:"correlation_id"`
	CausationID    string          `json:"causation_id,omitempty"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	WriterID       string          `json:"writer_id"`
	FencingToken   int64           `json:"fencing_token"`
	Status         string          `json:"status"`
	Payload        json.RawMessage `json:"payload"`
	PayloadHash    string          `json:"payload_hash"`
	PreviousHash   string          `json:"previous_hash,omitempty"`
	EnvelopeHash   string          `json:"envelope_hash"`
	CreatedAt      time.Time       `json:"created_at"`
	CommittedAt    time.Time       `json:"committed_at"`
}

// Input is the mutation form accepted by Append. Payload is encoded once and
// stored as immutable JSON so retries can compare the exact request bytes.
type Input struct {
	EventType      string
	AggregateType  string
	AggregateID    string
	Scope          Scope
	CorrelationID  string
	CausationID    string
	IdempotencyKey string
	WriterID       string
	FencingToken   int64
	Status         string
	Payload        any
}

type Lease struct {
	Key          string    `json:"key"`
	Owner        string    `json:"owner"`
	FencingToken int64     `json:"fencing_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type journalState struct {
	Version  int               `json:"version"`
	Revision int64             `json:"revision"`
	Events   []Event           `json:"events"`
	Leases   map[string]Lease  `json:"leases,omitempty"`
	Effects  map[string]string `json:"effects,omitempty"`
}

type Journal struct {
	mu       sync.RWMutex
	path     string
	revision int64
	events   []Event
	leases   map[string]Lease
	effects  map[string]string
}

func NewJournal(path string) (*Journal, error) {
	j := &Journal{path: strings.TrimSpace(path), leases: map[string]Lease{}, effects: map[string]string{}}
	if j.path == "" {
		return j, nil
	}
	data, err := os.ReadFile(j.path)
	if errors.Is(err, os.ErrNotExist) {
		return j, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read runtime journal: %w", err)
	}
	var state journalState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode runtime journal: %w", err)
	}
	if err := validateState(state); err != nil {
		return nil, err
	}
	j.revision, j.events = state.Revision, append([]Event(nil), state.Events...)
	if state.Leases != nil {
		j.leases = state.Leases
	}
	if state.Effects != nil {
		j.effects = state.Effects
	}
	return j, nil
}

func (j *Journal) List(scope Scope) []Event {
	j.mu.RLock()
	defer j.mu.RUnlock()
	result := make([]Event, 0)
	for _, event := range j.events {
		if scope.valid() && event.Scope != scope {
			continue
		}
		result = append(result, cloneEvent(event))
	}
	return result
}

func (j *Journal) Append(input Input) (Event, error) {
	return j.AppendBatch([]Input{input})
}

// AppendBatch gives a turn/tool transition one commit boundary. It validates
// all records before mutating memory or disk, so a partial tool transaction is
// never presented as committed after a crash.
func (j *Journal) AppendBatch(inputs []Input) (Event, error) {
	if len(inputs) == 0 {
		return Event{}, errors.New("at least one runtime event is required")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	candidate := append([]Event(nil), j.events...)
	for _, input := range inputs {
		event, err := j.prepareLocked(candidate, input)
		if err != nil {
			return Event{}, err
		}
		// An idempotent retry returns the original event. Do not append that
		// event a second time when it appears in the same batch or history.
		duplicate := false
		for _, prior := range candidate {
			if prior.EventID == event.EventID {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		candidate = append(candidate, event)
	}
	if err := j.persistCandidateLocked(candidate, j.leases, j.effects); err != nil {
		return Event{}, err
	}
	if j.path != "" {
		if err := j.reloadFromDiskLocked(); err != nil {
			return Event{}, err
		}
	} else {
		j.events = candidate
	}
	if len(candidate) == 0 {
		return Event{}, ErrConflict
	}
	if len(j.events) == 0 {
		return Event{}, ErrConflict
	}
	return cloneEvent(j.events[len(j.events)-1]), nil
}

func (j *Journal) prepareLocked(existing []Event, input Input) (Event, error) {
	if !input.Scope.valid() {
		return Event{}, errors.New("tenant_id, workspace_id, session_id and run_id are required")
	}
	input.EventType = strings.TrimSpace(input.EventType)
	input.AggregateType = strings.TrimSpace(input.AggregateType)
	input.AggregateID = strings.TrimSpace(input.AggregateID)
	if input.EventType == "" || input.AggregateType == "" || input.AggregateID == "" {
		return Event{}, errors.New("event_type, aggregate_type and aggregate_id are required")
	}
	if input.WriterID == "" {
		input.WriterID = "local"
	}
	if input.Status == "" {
		input.Status = StatusCommitted
	}
	if input.Status != StatusPending && input.Status != StatusCommitted && input.Status != StatusRejected && input.Status != StatusRecoveryNeeded {
		return Event{}, errors.New("invalid runtime event status")
	}
	payload, err := json.Marshal(input.Payload)
	if err != nil {
		return Event{}, fmt.Errorf("encode runtime payload: %w", err)
	}
	payload = append([]byte(nil), payload...)
	ph := payloadDigest(payload)
	key := scopedKey(input.Scope, input.IdempotencyKey)
	if input.IdempotencyKey != "" {
		for _, prior := range existing {
			if prior.IdempotencyKey != input.IdempotencyKey || prior.Scope != input.Scope {
				continue
			}
			if prior.PayloadHash != ph || prior.EventType != input.EventType || prior.AggregateID != input.AggregateID {
				return Event{}, ErrIdempotencyConflict
			}
			return prior, nil
		}
		_ = key // key documents the scope tuple used by callers and tests.
	}
	if input.FencingToken > 0 {
		lease, ok := j.leases[input.Scope.TenantID+"\x00"+input.Scope.WorkspaceID+"\x00"+input.Scope.RunID]
		if ok && (lease.Owner != input.WriterID || lease.FencingToken != input.FencingToken || !lease.ExpiresAt.After(time.Now().UTC())) {
			return Event{}, ErrLeaseLost
		}
		if !ok {
			return Event{}, ErrLeaseLost
		}
	}
	previous := ""
	if len(existing) > 0 {
		previous = existing[len(existing)-1].EnvelopeHash
	}
	event := Event{EventID: domain.NewID(), SchemaVersion: SchemaVersion, Sequence: int64(len(existing) + 1), EventType: input.EventType, AggregateType: input.AggregateType, AggregateID: input.AggregateID, Scope: input.Scope, CorrelationID: input.CorrelationID, CausationID: input.CausationID, IdempotencyKey: input.IdempotencyKey, WriterID: input.WriterID, FencingToken: input.FencingToken, Status: input.Status, Payload: payload, PayloadHash: ph, PreviousHash: previous, CreatedAt: time.Now().UTC()}
	event.CommittedAt = event.CreatedAt
	event.EnvelopeHash = envelopeDigest(event)
	return event, nil
}

func scopedKey(scope Scope, key string) string {
	return strings.Join([]string{scope.TenantID, scope.WorkspaceID, scope.SessionID, scope.RunID, key}, "\x00")
}

func (j *Journal) AcquireLease(scope Scope, owner string, ttl time.Duration, now time.Time) (Lease, error) {
	if !scope.valid() || strings.TrimSpace(owner) == "" || ttl <= 0 {
		return Lease{}, errors.New("scope, owner and positive ttl are required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	key := scope.TenantID + "\x00" + scope.WorkspaceID + "\x00" + scope.RunID
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Lease{}, err
	}
	lease, exists := j.leases[key]
	if exists && lease.ExpiresAt.After(now) && lease.Owner != owner {
		return Lease{}, ErrLeaseBusy
	}
	lease.Key, lease.Owner = key, owner
	lease.FencingToken++
	lease.ExpiresAt, lease.UpdatedAt = now.Add(ttl), now
	leases := cloneLeases(j.leases)
	leases[key] = lease
	if err := j.persistCandidateLocked(j.events, leases, j.effects); err != nil {
		return Lease{}, err
	}
	j.leases = leases
	if j.path != "" {
		if err := j.reloadFromDiskLocked(); err != nil {
			return Lease{}, err
		}
	}
	return lease, nil
}

func (j *Journal) ReleaseLease(scope Scope, owner string, fencingToken int64) error {
	key := scope.TenantID + "\x00" + scope.WorkspaceID + "\x00" + scope.RunID
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return err
	}
	lease, ok := j.leases[key]
	if !ok || lease.Owner != owner || lease.FencingToken != fencingToken || !lease.ExpiresAt.After(time.Now().UTC()) {
		return ErrLeaseLost
	}
	leases := cloneLeases(j.leases)
	delete(leases, key)
	if err := j.persistCandidateLocked(j.events, leases, j.effects); err != nil {
		return err
	}
	j.leases = leases
	return nil
}

// FenceEffect records exactly one committed side effect for an idempotency
// key. A retry returns the original receipt and never executes twice.
func (j *Journal) FenceEffect(scope Scope, key, owner string, fencingToken int64, payload any) (Event, bool, error) {
	if strings.TrimSpace(key) == "" {
		return Event{}, false, errors.New("effect key is required")
	}
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, false, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, false, err
	}
	effectKey := scopedKey(scope, key)
	if id := j.effects[effectKey]; id != "" {
		for _, event := range j.events {
			if event.EventID == id {
				return cloneEvent(event), false, nil
			}
		}
		return Event{}, false, ErrCorrupt
	}
	input := Input{EventType: EventEffectFenced, AggregateType: "run", AggregateID: scope.RunID, Scope: scope, IdempotencyKey: key, WriterID: owner, FencingToken: fencingToken, Payload: payload}
	event, err := j.prepareLocked(j.events, input)
	if err != nil {
		return Event{}, false, err
	}
	effects := cloneEffects(j.effects)
	effects[effectKey] = event.EventID
	candidate := append(append([]Event(nil), j.events...), event)
	if err := j.persistCandidateLocked(candidate, j.leases, effects); err != nil {
		return Event{}, false, err
	}
	j.events, j.effects = candidate, effects
	if j.path != "" {
		if err := j.reloadFromDiskLocked(); err != nil {
			return Event{}, false, err
		}
	}
	return cloneEvent(event), true, nil
}

// AuthorizeTool records the policy decision before a tool can start. An empty
// allow-list is intentionally deny-by-default; callers must make the policy
// explicit and the decision is replayable from the journal.
func (j *Journal) AuthorizeTool(scope Scope, callID, name, owner string, fencingToken int64, allowed []string) (Event, error) {
	callID, name = strings.TrimSpace(callID), strings.TrimSpace(name)
	if callID == "" || name == "" {
		return Event{}, errors.New("tool call id and name are required")
	}
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	ok := false
	for _, candidate := range allowed {
		if strings.TrimSpace(candidate) == name {
			ok = true
			break
		}
	}
	if !ok {
		return Event{}, ErrUnauthorized
	}
	return j.Append(Input{EventType: EventToolAuthorized, AggregateType: "tool", AggregateID: callID, Scope: scope, IdempotencyKey: "tool:" + callID + ":authorize", WriterID: owner, FencingToken: fencingToken, Payload: map[string]any{"call_id": callID, "name": name, "allowed": true}})
}

func (j *Journal) toolHas(scope Scope, callID, eventType string) bool {
	for _, event := range j.events {
		if event.Scope == scope && event.AggregateID == callID && event.EventType == eventType {
			return true
		}
	}
	return false
}

// StartTool and FinishTool enforce the durable authorize → start → finish
// sequence. Providers may execute between these calls, but cannot claim a
// completed effect without the corresponding journal facts.
func (j *Journal) StartTool(scope Scope, callID, name, owner string, fencingToken int64, payload any) (Event, error) {
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.RLock()
	authorized := j.toolHas(scope, callID, EventToolAuthorized)
	j.mu.RUnlock()
	if !authorized {
		return Event{}, ErrUnauthorized
	}
	return j.Append(Input{EventType: EventToolStarted, AggregateType: "tool", AggregateID: callID, Scope: scope, IdempotencyKey: "tool:" + callID + ":start", WriterID: owner, FencingToken: fencingToken, Status: StatusPending, Payload: payload})
}

func (j *Journal) ApproveTool(scope Scope, callID, owner string, fencingToken int64, decision string) (Event, error) {
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	decision = strings.ToLower(strings.TrimSpace(decision))
	if decision != "approved" && decision != "denied" {
		return Event{}, errors.New("tool approval must be approved or denied")
	}
	return j.Append(Input{EventType: EventToolApproved, AggregateType: "tool", AggregateID: callID, Scope: scope, IdempotencyKey: "tool:" + callID + ":approval", WriterID: owner, FencingToken: fencingToken, Status: map[bool]string{true: StatusCommitted, false: StatusRejected}[decision == "approved"], Payload: map[string]any{"decision": decision}})
}

func (j *Journal) FinishTool(scope Scope, callID, owner string, fencingToken int64, output any) (Event, error) {
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.RLock()
	started := j.toolHas(scope, callID, EventToolStarted)
	denied := false
	for _, event := range j.events {
		if event.Scope == scope && event.AggregateID == callID && event.EventType == EventToolApproved && event.Status == StatusRejected {
			denied = true
		}
	}
	j.mu.RUnlock()
	if !started {
		return Event{}, ErrConflict
	}
	if denied {
		return Event{}, ErrUnauthorized
	}
	return j.Append(Input{EventType: EventToolFinished, AggregateType: "tool", AggregateID: callID, Scope: scope, IdempotencyKey: "tool:" + callID + ":finish", WriterID: owner, FencingToken: fencingToken, Status: StatusCommitted, Payload: output})
}

func (j *Journal) AppendInteraction(scope Scope, input, key, owner string, fencingToken int64) (Event, error) {
	if strings.TrimSpace(input) == "" {
		return Event{}, errors.New("interaction input is required")
	}
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	return j.Append(Input{EventType: EventInteraction, AggregateType: "run", AggregateID: scope.RunID, Scope: scope, IdempotencyKey: key, WriterID: owner, FencingToken: fencingToken, Payload: map[string]any{"input": input}})
}

func (j *Journal) RecordUsage(scope Scope, usage any, owner string, fencingToken int64) (Event, error) {
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	return j.Append(Input{EventType: EventUsage, AggregateType: "run", AggregateID: scope.RunID, Scope: scope, WriterID: owner, FencingToken: fencingToken, Payload: usage})
}

// FinishTurn commits the terminal turn and its checkpoint in one batch. A
// crash before the snapshot swap leaves neither record visible, while a retry
// with the same idempotency keys converges on the original pair.
func (j *Journal) FinishTurn(scope Scope, turn, checkpoint any, key, owner string, fencingToken int64) (Event, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return Event{}, errors.New("turn idempotency key is required")
	}
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	return j.AppendBatch([]Input{
		{EventType: EventTurnFinished, AggregateType: "run", AggregateID: scope.RunID, Scope: scope, IdempotencyKey: key + ":finish", WriterID: owner, FencingToken: fencingToken, Status: StatusCommitted, Payload: turn},
		{EventType: EventTurnCheckpointed, AggregateType: "run", AggregateID: scope.RunID, Scope: scope, IdempotencyKey: key + ":checkpoint", WriterID: owner, FencingToken: fencingToken, Status: StatusCommitted, Payload: checkpoint},
	})
}

func (j *Journal) Verify() error {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return validateEvents(j.events)
}

func (j *Journal) reloadLocked() error {
	if j.path == "" {
		return nil
	}
	data, err := os.ReadFile(j.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var state journalState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode runtime journal: %w", err)
	}
	if err := validateState(state); err != nil {
		return err
	}
	if state.Revision <= j.revision {
		return nil
	}
	j.revision = state.Revision
	j.events = append([]Event(nil), state.Events...)
	if state.Leases != nil {
		j.leases = cloneLeases(state.Leases)
	}
	if state.Effects != nil {
		j.effects = cloneEffects(state.Effects)
	}
	return nil
}

func (j *Journal) reloadFromDiskLocked() error {
	if j.path == "" {
		return nil
	}
	data, err := os.ReadFile(j.path)
	if err != nil {
		return err
	}
	var state journalState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if err := validateState(state); err != nil {
		return err
	}
	j.revision = state.Revision
	j.events = append([]Event(nil), state.Events...)
	j.leases = cloneLeases(state.Leases)
	j.effects = cloneEffects(state.Effects)
	return nil
}

func (j *Journal) persistCandidateLocked(events []Event, leases map[string]Lease, effects map[string]string) error {
	if j.path == "" {
		j.revision++
		return nil
	}
	return durable.WithExclusive(j.path, func() error {
		diskRevision, err := readRevision(j.path)
		if err != nil {
			return err
		}
		if diskRevision != j.revision {
			// A peer may have advanced only lease/effect metadata while this
			// writer was preparing an event. Reloading is safe when the event
			// history is unchanged; a divergent history remains fail-closed.
			data, readErr := os.ReadFile(j.path)
			if readErr != nil {
				return readErr
			}
			var disk journalState
			if readErr = json.Unmarshal(data, &disk); readErr != nil {
				return readErr
			}
			if len(disk.Events) < len(j.events) {
				return fmt.Errorf("%w: expected revision %d, found %d", ErrConflict, j.revision, diskRevision)
			}
			for i := range j.events {
				if disk.Events[i].EventID != j.events[i].EventID || disk.Events[i].EnvelopeHash != j.events[i].EnvelopeHash {
					return fmt.Errorf("%w: peer event history diverged", ErrConflict)
				}
			}
			if len(disk.Events) > len(j.events) {
				// Keep peer events and append only the candidate suffix. This is
				// the common case when an executor finishes between reload and
				// another process renewing its lease.
				suffix := append([]Event(nil), events[len(j.events):]...)
				previous := disk.Events[len(disk.Events)-1].EnvelopeHash
				for i := range suffix {
					suffix[i].Sequence = int64(len(disk.Events) + i + 1)
					suffix[i].PreviousHash = previous
					suffix[i].EnvelopeHash = envelopeDigest(suffix[i])
					previous = suffix[i].EnvelopeHash
				}
				events = append(append([]Event(nil), disk.Events...), suffix...)
				j.events = append([]Event(nil), disk.Events...)
			}
			if disk.Leases != nil {
				mergedLeases := cloneLeases(disk.Leases)
				for key, lease := range leases {
					mergedLeases[key] = lease
				}
				leases = mergedLeases
			}
			if disk.Effects != nil {
				mergedEffects := cloneEffects(disk.Effects)
				for key, effect := range effects {
					mergedEffects[key] = effect
				}
				effects = mergedEffects
			}
			j.revision = diskRevision
		}
		state := journalState{Version: SchemaVersion, Revision: j.revision + 1, Events: events, Leases: leases, Effects: effects}
		data, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return err
		}
		dir := filepath.Dir(j.path)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(dir, ".adro-runtime-*")
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
		if err := os.Rename(name, j.path); err != nil {
			return err
		}
		j.revision = state.Revision
		return nil
	})
}

func readRevision(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var state journalState
	if err := json.Unmarshal(data, &state); err != nil {
		return 0, fmt.Errorf("decode runtime revision: %w", err)
	}
	if err := validateState(state); err != nil {
		return 0, err
	}
	return state.Revision, nil
}

func validateState(state journalState) error {
	if state.Version != 0 && state.Version != SchemaVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrCorrupt, state.Version)
	}
	if err := validateEvents(state.Events); err != nil {
		return err
	}
	return nil
}

func validateEvents(events []Event) error {
	previous := ""
	seen := map[string]struct{}{}
	for i, event := range events {
		if event.EventID == "" || event.SchemaVersion != SchemaVersion || event.Sequence != int64(i+1) || !event.Scope.valid() || event.EventType == "" || event.AggregateType == "" || event.AggregateID == "" || event.PreviousHash != previous || event.PayloadHash != payloadDigest(event.Payload) || event.EnvelopeHash != envelopeDigest(event) {
			return fmt.Errorf("%w: event sequence %d", ErrCorrupt, i+1)
		}
		if _, ok := seen[event.EventID]; ok {
			return fmt.Errorf("%w: duplicate event %s", ErrCorrupt, event.EventID)
		}
		seen[event.EventID] = struct{}{}
		previous = event.EnvelopeHash
	}
	return nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func payloadDigest(data []byte) string {
	var canonical bytes.Buffer
	if err := json.Compact(&canonical, data); err != nil {
		return ""
	}
	return digest(canonical.Bytes())
}

func envelopeDigest(event Event) string {
	copy := event
	copy.EnvelopeHash = ""
	data, _ := json.Marshal(copy)
	return digest(data)
}

func cloneEvent(event Event) Event {
	event.Payload = append([]byte(nil), event.Payload...)
	return event
}
func cloneLeases(input map[string]Lease) map[string]Lease {
	out := make(map[string]Lease, len(input))
	for k, v := range input {
		out[k] = v
	}
	return out
}
func cloneEffects(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for k, v := range input {
		out[k] = v
	}
	return out
}
