// Package runtime contains ADRO-owned execution primitives.  Providers are
// adapters around this package: they may supply model output, but they do not
// own the durable record of what ADRO authorized, started, or committed.
package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	coreencoding "github.com/adro-project/adro/core/encoding"
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
	EventToolFailed       = "tool.failed"
	EventToolCancelled    = "tool.cancelled"
	EventToolRetried      = "tool.retried"
	EventToolNotStarted   = "tool.not_started"
	EventInteraction      = "interaction.accepted"
	EventUsage            = "usage.recorded"
	EventEffectIntent     = "effect.intent_committed"
	EventEffectPrepared   = "effect.dispatch_prepared"
	EventEffectDispatched = "effect.dispatched"
	EventEffectReceipted  = "effect.receipted"
	EventEffectUnknown    = "effect.outcome_unknown"
	EventEffectReconciled = "effect.reconciled"
	EventModelRequested   = "model.requested"
	EventModelPrepared    = "model.dispatch_prepared"
	EventModelDispatched  = "model.dispatched"
	EventModelStreamed    = "model.stream_event"
	EventModelCompleted   = "model.completed"
	EventModelUnknown     = "model.outcome_unknown"
	EventModelCancelled   = "model.cancelled"
)

var (
	ErrCorrupt              = errors.New("runtime journal is corrupt")
	ErrConflict             = errors.New("runtime journal conflict")
	ErrIdempotencyConflict  = errors.New("runtime idempotency key conflict")
	ErrLeaseBusy            = errors.New("runtime lease is held by another owner")
	ErrLeaseLost            = errors.New("runtime lease is no longer owned")
	ErrUnauthorized         = errors.New("runtime tool is not authorized")
	ErrApprovalRequired     = errors.New("runtime tool approval is required")
	ErrToolExecution        = errors.New("runtime tool execution failed")
	ErrEffectOutcomeUnknown = errors.New("runtime effect outcome is unknown")
)

type EffectClass string

const (
	EffectReadOnly          EffectClass = "read_only"
	EffectIdempotentWrite   EffectClass = "idempotent_write"
	EffectReconcilableWrite EffectClass = "reconcilable_write"
	EffectNonRetriableWrite EffectClass = "non_retriable_write"
)

func (c EffectClass) valid() bool {
	switch c {
	case EffectReadOnly, EffectIdempotentWrite, EffectReconcilableWrite, EffectNonRetriableWrite:
		return true
	default:
		return false
	}
}

// ReconcilePolicy describes the only permitted resolution path after an
// external effect has an unknown outcome. It is part of the frozen tool
// contract, not an operator-side guess made after dispatch.
type ReconcilePolicy string

const (
	ReconcileNone          ReconcilePolicy = "none"
	ReconcileQuery         ReconcilePolicy = "query"
	ReconcileCompensate    ReconcilePolicy = "compensate"
	ReconcileHuman         ReconcilePolicy = "human_decision"
	ReconcileUnrecoverable ReconcilePolicy = "unrecoverable"
)

func (p ReconcilePolicy) valid() bool {
	switch p {
	case ReconcileNone, ReconcileQuery, ReconcileCompensate, ReconcileHuman, ReconcileUnrecoverable:
		return true
	default:
		return false
	}
}

func reconcilePolicyForClass(class EffectClass) ReconcilePolicy {
	if class == EffectReadOnly {
		return ReconcileNone
	}
	// Compatibility callers that predate explicit contracts are kept
	// fail-closed: they can only reach a human resolution path.
	return ReconcileHuman
}

func validateReconcilePolicy(class EffectClass, policy ReconcilePolicy) error {
	if class == EffectReadOnly && policy == "" {
		return nil
	}
	if !policy.valid() {
		return errors.New("valid reconcile_policy is required")
	}
	if class == EffectReadOnly && policy != ReconcileNone {
		return errors.New("read_only effects must use reconcile_policy none")
	}
	if class != EffectReadOnly && policy == ReconcileNone {
		return errors.New("write effects require an explicit reconcile_policy")
	}
	return nil
}

func reconcileDecisionAllowed(policy ReconcilePolicy, decision string) bool {
	switch policy {
	case ReconcileQuery:
		return decision == "confirmed" || decision == "not_found"
	case ReconcileCompensate:
		return decision == "compensated" || decision == "not_found"
	case ReconcileHuman:
		return decision == "human_resolved"
	case ReconcileUnrecoverable:
		return decision == "unrecoverable"
	default:
		return false
	}
}

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

// ToolContract is the provider-neutral policy checked at the tool boundary.
// Contracts are data, so authorization decisions and retries can be replayed
// without executing an external tool.
type ToolContract struct {
	SchemaVersion    int               `json:"schema_version,omitempty"`
	Name             string            `json:"name"`
	InputSchema      string            `json:"input_schema,omitempty"`
	OutputSchema     string            `json:"output_schema,omitempty"`
	SideEffectClass  EffectClass       `json:"side_effect_class"`
	ReconcilePolicy  ReconcilePolicy   `json:"reconcile_policy,omitempty"`
	ConcurrencyMode  string            `json:"concurrency_mode,omitempty"`
	MaxInputBytes    int               `json:"max_input_bytes,omitempty"`
	MaxOutputBytes   int               `json:"max_output_bytes,omitempty"`
	FieldClasses     map[string]string `json:"field_classes,omitempty"`
	ContractDigest   string            `json:"contract_digest,omitempty"`
	Risk             string            `json:"risk,omitempty"`
	Capabilities     []string          `json:"capabilities,omitempty"`
	SecretScopes     []string          `json:"secret_scopes,omitempty"`
	Network          bool              `json:"network,omitempty"`
	Filesystem       bool              `json:"filesystem,omitempty"`
	Timeout          time.Duration     `json:"timeout,omitempty"`
	MaxRetries       int               `json:"max_retries,omitempty"`
	RequiresApproval bool              `json:"requires_approval,omitempty"`
	Compensation     string            `json:"compensation,omitempty"`
	EvidenceRequired bool              `json:"evidence_required,omitempty"`
}

type ToolState struct {
	Scope            Scope    `json:"scope"`
	CallID           string   `json:"call_id"`
	Name             string   `json:"name"`
	Capabilities     []string `json:"capabilities,omitempty"`
	Authorized       bool     `json:"authorized"`
	RequiresApproval bool     `json:"requires_approval"`
	Started          bool     `json:"started"`
	Approved         *bool    `json:"approved,omitempty"`
	Finished         bool     `json:"finished"`
	Failed           bool     `json:"failed"`
	Cancelled        bool     `json:"cancelled"`
	NotStarted       bool     `json:"not_started"`
	Attempts         int      `json:"attempts"`
	ContractDigest   string   `json:"contract_digest,omitempty"`
	LastEventID      string   `json:"last_event_id,omitempty"`
	ReasonCode       string   `json:"reason_code,omitempty"`
}

type ModelState struct {
	Scope            Scope  `json:"scope"`
	RequestID        string `json:"request_id"`
	Requested        bool   `json:"requested"`
	DispatchPrepared bool   `json:"dispatch_prepared"`
	Dispatched       bool   `json:"dispatched"`
	StreamSequence   int64  `json:"stream_sequence"`
	Completed        bool   `json:"completed"`
	OutcomeUnknown   bool   `json:"outcome_unknown"`
	Cancelled        bool   `json:"cancelled"`
	FinishReason     string `json:"finish_reason,omitempty"`
	LastEventID      string `json:"last_event_id,omitempty"`
}

type EffectState struct {
	Scope             Scope           `json:"scope"`
	EffectID          string          `json:"effect_id"`
	ToolCallID        string          `json:"tool_call_id,omitempty"`
	Class             EffectClass     `json:"effect_class,omitempty"`
	ReconcilePolicy   ReconcilePolicy `json:"reconcile_policy,omitempty"`
	IntentCommitted   bool            `json:"intent_committed"`
	DispatchPrepared  bool            `json:"dispatch_prepared"`
	Dispatched        bool            `json:"dispatched"`
	Receipted         bool            `json:"receipted"`
	OutcomeUnknown    bool            `json:"outcome_unknown"`
	Reconciled        bool            `json:"reconciled"`
	ReconcileDecision string          `json:"reconcile_decision,omitempty"`
	ReconcileOutput   any             `json:"reconcile_output,omitempty"`
	LastEventID       string          `json:"last_event_id,omitempty"`
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
	mu            sync.RWMutex
	path          string
	revision      int64
	events        []Event
	leases        map[string]Lease
	effects       map[string]string
	shadow        EventShadow
	shadowTimeout time.Duration
	shadowReports map[string]ShadowReport
}

func NewJournal(path string) (*Journal, error) {
	j := &Journal{
		path: strings.TrimSpace(path), leases: map[string]Lease{}, effects: map[string]string{},
		shadowTimeout: defaultShadowTimeout, shadowReports: map[string]ShadowReport{},
	}
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

// SetShadow enables migration-only dual writes. Existing legacy events are
// synchronized immediately, while future shadow failures remain diagnostic and
// never change the result of an authoritative legacy journal commit.
func (j *Journal) SetShadow(shadow EventShadow, timeout time.Duration) []ShadowReport {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.shadow = shadow
	if timeout <= 0 {
		timeout = defaultShadowTimeout
	}
	j.shadowTimeout = timeout
	if j.shadowReports == nil {
		j.shadowReports = map[string]ShadowReport{}
	}
	j.syncShadowScopesLocked(sortedScopes(j.events))
	return j.shadowReportsLocked()
}

func (j *Journal) ShadowReports() []ShadowReport {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.shadowReportsLocked()
}

func (j *Journal) shadowReportsLocked() []ShadowReport {
	keys := make([]string, 0, len(j.shadowReports))
	for key := range j.shadowReports {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	reports := make([]ShadowReport, 0, len(keys))
	for _, key := range keys {
		reports = append(reports, j.shadowReports[key])
	}
	return reports
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
	return j.appendBatchLocked(inputs)
}

// appendBatchLocked appends to a freshly reloaded journal while j.mu is held.
func (j *Journal) appendBatchLocked(inputs []Input) (Event, error) {
	candidate := append([]Event(nil), j.events...)
	var last Event
	for _, input := range inputs {
		event, err := j.prepareLocked(candidate, input)
		if err != nil {
			return Event{}, err
		}
		last = event
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
	scopes := make([]Scope, 0, len(inputs))
	seenScopes := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		key := shadowScopeKey(input.Scope)
		if _, seen := seenScopes[key]; seen {
			continue
		}
		seenScopes[key] = struct{}{}
		scopes = append(scopes, input.Scope)
	}
	j.syncShadowScopesLocked(scopes)
	if last.EventID == "" {
		return Event{}, ErrConflict
	}
	return cloneEvent(last), nil
}

func (j *Journal) syncShadowScopesLocked(scopes []Scope) {
	if j.shadow == nil {
		return
	}
	for _, scope := range scopes {
		events := make([]Event, 0)
		for _, item := range j.events {
			if item.Scope == scope {
				events = append(events, cloneEvent(item))
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), j.shadowTimeout)
		report := j.shadow.Mirror(ctx, scope, events)
		cancel()
		j.shadowReports[shadowScopeKey(scope)] = report
	}
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

// CommitEffectIntent durably records what may be dispatched. The intent is an
// idempotency fence, not proof that an external side effect completed.
func (j *Journal) CommitEffectIntent(scope Scope, effectID, callID, tool string, class EffectClass, input any, owner string, fencingToken int64) (Event, bool, error) {
	return j.CommitEffectIntentWithPolicy(scope, effectID, callID, tool, class, reconcilePolicyForClass(class), input, owner, fencingToken)
}

// CommitEffectIntentWithPolicy records the frozen reconciliation contract
// beside the effect intent. A later worker cannot invent a safer resolution
// path after an external outcome becomes unknown.
func (j *Journal) CommitEffectIntentWithPolicy(scope Scope, effectID, callID, tool string, class EffectClass, policy ReconcilePolicy, input any, owner string, fencingToken int64) (Event, bool, error) {
	effectID, callID, tool = strings.TrimSpace(effectID), strings.TrimSpace(callID), strings.TrimSpace(tool)
	if effectID == "" || callID == "" || tool == "" || !class.valid() {
		return Event{}, false, errors.New("effect_id, call_id, tool and valid effect_class are required")
	}
	if err := validateReconcilePolicy(class, policy); err != nil {
		return Event{}, false, err
	}
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, false, ErrLeaseLost
	}
	inputBytes, err := coreencoding.Marshal(input)
	if err != nil {
		return Event{}, false, fmt.Errorf("encode effect input: %w", err)
	}
	payload := map[string]any{
		"effect_id":        effectID,
		"call_id":          callID,
		"tool":             tool,
		"effect_class":     class,
		"reconcile_policy": policy,
		"input_digest":     canonicalPayloadDigest(inputBytes),
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, false, err
	}
	effectKey := scopedKey(scope, effectID)
	if id := j.effects[effectKey]; id != "" {
		for _, event := range j.events {
			if event.EventID != id {
				continue
			}
			payloadBytes, marshalErr := json.Marshal(payload)
			if marshalErr != nil || payloadDigest(payloadBytes) != event.PayloadHash {
				return Event{}, false, ErrIdempotencyConflict
			}
			return cloneEvent(event), false, nil
		}
		return Event{}, false, ErrCorrupt
	}
	inputEvent := Input{EventType: EventEffectIntent, AggregateType: "effect", AggregateID: effectID, Scope: scope, IdempotencyKey: "effect:" + effectID + ":intent", WriterID: owner, FencingToken: fencingToken, Status: StatusPending, Payload: payload}
	event, err := j.prepareLocked(j.events, inputEvent)
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

func (j *Journal) effectStateLocked(scope Scope, effectID string) EffectState {
	state := EffectState{Scope: scope, EffectID: effectID}
	for _, event := range j.events {
		if event.Scope != scope || event.AggregateType != "effect" || event.AggregateID != effectID {
			continue
		}
		state.LastEventID = event.EventID
		switch event.EventType {
		case EventEffectIntent:
			state.IntentCommitted = true
			var payload struct {
				CallID          string          `json:"call_id"`
				Class           EffectClass     `json:"effect_class"`
				ReconcilePolicy ReconcilePolicy `json:"reconcile_policy"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			state.ToolCallID, state.Class, state.ReconcilePolicy = payload.CallID, payload.Class, payload.ReconcilePolicy
			if state.ReconcilePolicy == "" {
				state.ReconcilePolicy = reconcilePolicyForClass(state.Class)
			}
		case EventEffectPrepared:
			state.DispatchPrepared = true
		case EventEffectDispatched:
			state.Dispatched = true
		case EventEffectReceipted:
			state.Receipted = true
			state.OutcomeUnknown = false
		case EventEffectUnknown:
			state.OutcomeUnknown = true
		case EventEffectReconciled:
			var payload struct {
				Decision string `json:"decision"`
				Output   any    `json:"output"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			state.Reconciled = true
			state.OutcomeUnknown = false
			state.ReconcileDecision = payload.Decision
			state.ReconcileOutput = payload.Output
		}
	}
	return state
}

func (j *Journal) EffectState(scope Scope, effectID string) (EffectState, error) {
	if !scope.valid() || strings.TrimSpace(effectID) == "" {
		return EffectState{}, errors.New("scope and effect_id are required")
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.effectStateLocked(scope, strings.TrimSpace(effectID)), nil
}

func (j *Journal) modelStateLocked(scope Scope, requestID string) ModelState {
	state := ModelState{Scope: scope, RequestID: requestID}
	for _, event := range j.events {
		if event.Scope != scope || event.AggregateType != "model" || event.AggregateID != requestID {
			continue
		}
		state.LastEventID = event.EventID
		switch event.EventType {
		case EventModelRequested:
			state.Requested = true
		case EventModelPrepared:
			state.DispatchPrepared = true
		case EventModelDispatched:
			state.Dispatched = true
		case EventModelStreamed:
			var payload struct {
				Event struct {
					Sequence int64 `json:"sequence"`
				} `json:"event"`
			}
			if json.Unmarshal(event.Payload, &payload) == nil && payload.Event.Sequence > state.StreamSequence {
				state.StreamSequence = payload.Event.Sequence
			}
		case EventModelCompleted:
			state.Completed = true
			var payload struct {
				Reason string `json:"finish_reason"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			state.FinishReason = payload.Reason
		case EventModelUnknown:
			state.OutcomeUnknown = true
		case EventModelCancelled:
			state.Cancelled = true
		}
	}
	return state
}

func (j *Journal) ModelState(scope Scope, requestID string) (ModelState, error) {
	if !scope.valid() || strings.TrimSpace(requestID) == "" {
		return ModelState{}, errors.New("scope and request_id are required")
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.modelStateLocked(scope, strings.TrimSpace(requestID)), nil
}

// CommitModelRequest freezes the exact model request before any adapter
// dispatch. The request idempotency key is the durable duplicate fence.
func (j *Journal) CommitModelRequest(scope Scope, request ModelRequest, owner string, fencingToken int64) (Event, bool, error) {
	if request.Scope != scope {
		return Event{}, false, ErrModelRequestInvalid
	}
	if err := request.Validate(); err != nil {
		return Event{}, false, err
	}
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, false, ErrLeaseLost
	}
	payload := map[string]any{"request": request, "request_digest": request.RequestDigest}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, false, err
	}
	state := j.modelStateLocked(scope, request.RequestID)
	if state.Requested {
		for _, item := range j.events {
			if item.Scope == scope && item.AggregateType == "model" && item.AggregateID == request.RequestID && item.EventType == EventModelRequested {
				var prior struct {
					RequestDigest string `json:"request_digest"`
				}
				if json.Unmarshal(item.Payload, &prior) == nil && prior.RequestDigest == request.RequestDigest {
					return cloneEvent(item), false, nil
				}
				return Event{}, false, ErrModelIdempotencyConflict
			}
		}
		return Event{}, false, ErrCorrupt
	}
	event, err := j.appendBatchLocked([]Input{{EventType: EventModelRequested, AggregateType: "model", AggregateID: request.RequestID, Scope: scope, IdempotencyKey: "model:" + request.IdempotencyKey + ":requested", WriterID: owner, FencingToken: fencingToken, Status: StatusPending, Payload: payload}})
	return event, err == nil, err
}

func (j *Journal) PrepareModelDispatch(scope Scope, requestID, owner string, fencingToken int64) (Event, error) {
	return j.modelTransition(scope, requestID, owner, fencingToken, EventModelPrepared, func(state ModelState) bool {
		return state.Requested && !state.DispatchPrepared && !state.Dispatched && !state.Completed && !state.OutcomeUnknown && !state.Cancelled
	})
}

func (j *Journal) MarkModelDispatched(scope Scope, requestID, owner string, fencingToken int64) (Event, error) {
	return j.modelTransition(scope, requestID, owner, fencingToken, EventModelDispatched, func(state ModelState) bool {
		return state.DispatchPrepared && !state.Dispatched && !state.Completed && !state.OutcomeUnknown && !state.Cancelled
	})
}

func (j *Journal) AppendModelEvent(scope Scope, event ModelEvent, owner string, fencingToken int64) (Event, error) {
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	state := j.modelStateLocked(scope, event.RequestID)
	if !state.Dispatched || state.Completed || state.OutcomeUnknown || state.Cancelled {
		return Event{}, ErrModelTransition
	}
	if err := event.Validate(state.StreamSequence, event.RequestID); err != nil {
		return Event{}, err
	}
	payload := map[string]any{"event": event}
	return j.appendBatchLocked([]Input{{EventType: EventModelStreamed, AggregateType: "model", AggregateID: event.RequestID, Scope: scope, IdempotencyKey: fmt.Sprintf("model:%s:stream:%d", event.RequestID, event.Sequence), WriterID: owner, FencingToken: fencingToken, Status: StatusPending, Payload: payload}})
}

func (j *Journal) CompleteModelCall(scope Scope, event ModelEvent, owner string, fencingToken int64) (Event, error) {
	if event.Type != ModelEventFinish {
		return Event{}, ErrModelTransition
	}
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	state := j.modelStateLocked(scope, event.RequestID)
	if !state.Dispatched || state.Completed || state.OutcomeUnknown || state.Cancelled {
		return Event{}, ErrModelTransition
	}
	if err := event.Validate(state.StreamSequence, event.RequestID); err != nil {
		return Event{}, err
	}
	return j.appendBatchLocked([]Input{
		{EventType: EventModelStreamed, AggregateType: "model", AggregateID: event.RequestID, Scope: scope, IdempotencyKey: fmt.Sprintf("model:%s:stream:%d", event.RequestID, event.Sequence), WriterID: owner, FencingToken: fencingToken, Status: StatusPending, Payload: map[string]any{"event": event}},
		{EventType: EventModelCompleted, AggregateType: "model", AggregateID: event.RequestID, Scope: scope, IdempotencyKey: fmt.Sprintf("model:%s:completed:%d", event.RequestID, event.Sequence), WriterID: owner, FencingToken: fencingToken, Status: StatusCommitted, Payload: map[string]any{"event": event, "finish_reason": event.FinishReason}},
	})
}

func (j *Journal) MarkModelOutcomeUnknown(scope Scope, requestID, reason, owner string, fencingToken int64) (Event, error) {
	if strings.TrimSpace(reason) == "" {
		reason = "dispatch_outcome_unknown"
	}
	return j.modelTransitionWithPayload(scope, requestID, owner, fencingToken, EventModelUnknown, StatusRecoveryNeeded, map[string]any{"request_id": requestID, "reason_code": reason}, func(state ModelState) bool {
		return state.Dispatched && !state.Completed && !state.OutcomeUnknown && !state.Cancelled
	})
}

func (j *Journal) modelTransition(scope Scope, requestID, owner string, fencingToken int64, eventType string, allowed func(ModelState) bool) (Event, error) {
	return j.modelTransitionWithPayload(scope, requestID, owner, fencingToken, eventType, StatusPending, map[string]any{"request_id": requestID}, allowed)
}

func (j *Journal) modelTransitionWithPayload(scope Scope, requestID, owner string, fencingToken int64, eventType, status string, payload map[string]any, allowed func(ModelState) bool) (Event, error) {
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	state := j.modelStateLocked(scope, requestID)
	if !allowed(state) {
		return Event{}, ErrModelTransition
	}
	return j.appendBatchLocked([]Input{{EventType: eventType, AggregateType: "model", AggregateID: requestID, Scope: scope, IdempotencyKey: "model:" + requestID + ":" + strings.TrimPrefix(eventType, "model."), WriterID: owner, FencingToken: fencingToken, Status: status, Payload: payload}})
}

func (j *Journal) PrepareEffectDispatch(scope Scope, effectID, owner string, fencingToken int64) (Event, error) {
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	state := j.effectStateLocked(scope, effectID)
	if !state.IntentCommitted || state.Dispatched || state.Receipted || state.OutcomeUnknown || state.Reconciled {
		return Event{}, ErrConflict
	}
	return j.appendBatchLocked([]Input{{EventType: EventEffectPrepared, AggregateType: "effect", AggregateID: effectID, Scope: scope, IdempotencyKey: "effect:" + effectID + ":prepare", WriterID: owner, FencingToken: fencingToken, Status: StatusPending, Payload: map[string]any{"effect_id": effectID}}})
}

func (j *Journal) MarkEffectDispatched(scope Scope, effectID, owner string, fencingToken int64) (Event, error) {
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	state := j.effectStateLocked(scope, effectID)
	if !state.DispatchPrepared || state.Receipted || state.OutcomeUnknown || state.Reconciled {
		return Event{}, ErrConflict
	}
	if state.Dispatched {
		for _, event := range j.events {
			if event.AggregateType == "effect" && event.AggregateID == effectID && event.EventType == EventEffectDispatched {
				return cloneEvent(event), nil
			}
		}
	}
	return j.appendBatchLocked([]Input{{EventType: EventEffectDispatched, AggregateType: "effect", AggregateID: effectID, Scope: scope, IdempotencyKey: "effect:" + effectID + ":dispatch", WriterID: owner, FencingToken: fencingToken, Status: StatusPending, Payload: map[string]any{"effect_id": effectID}}})
}

func (j *Journal) MarkEffectOutcomeUnknown(scope Scope, effectID, reason, owner string, fencingToken int64) (Event, error) {
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	state := j.effectStateLocked(scope, effectID)
	if !state.Dispatched || state.Receipted || state.Reconciled {
		return Event{}, ErrConflict
	}
	if strings.TrimSpace(reason) == "" {
		reason = "dispatch_outcome_unknown"
	}
	return j.appendBatchLocked([]Input{{EventType: EventEffectUnknown, AggregateType: "effect", AggregateID: effectID, Scope: scope, IdempotencyKey: "effect:" + effectID + ":unknown", WriterID: owner, FencingToken: fencingToken, Status: StatusRecoveryNeeded, Payload: map[string]any{"effect_id": effectID, "reason_code": reason}}})
}

// ReconcileEffect closes an unknown external outcome through the policy frozen
// in the original intent. The reconciliation fact and model-visible tool
// completion share one journal batch; neither can be committed alone.
func (j *Journal) ReconcileEffect(scope Scope, effectID, callID, owner string, fencingToken int64, decision string, output any) (Event, error) {
	effectID, callID, decision = strings.TrimSpace(effectID), strings.TrimSpace(callID), strings.ToLower(strings.TrimSpace(decision))
	if effectID == "" || callID == "" || decision == "" {
		return Event{}, errors.New("effect_id, call_id and reconciliation decision are required")
	}
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	state := j.effectStateLocked(scope, effectID)
	toolState := j.toolStateLocked(scope, callID)
	if state.Reconciled {
		for _, item := range j.events {
			if item.Scope != scope || item.AggregateType != "effect" || item.AggregateID != effectID || item.EventType != EventEffectReconciled {
				continue
			}
			var prior struct {
				Decision string `json:"decision"`
			}
			if json.Unmarshal(item.Payload, &prior) == nil && prior.Decision == decision {
				return cloneEvent(item), nil
			}
			return Event{}, ErrIdempotencyConflict
		}
		return Event{}, ErrCorrupt
	}
	if !state.IntentCommitted || state.ToolCallID != callID || !state.Dispatched || state.Receipted || !toolState.Started || toolState.Finished || toolState.Cancelled {
		return Event{}, ErrConflict
	}
	if !state.OutcomeUnknown {
		return Event{}, ErrConflict
	}
	if !reconcileDecisionAllowed(state.ReconcilePolicy, decision) {
		return Event{}, fmt.Errorf("%w: decision %q is not allowed by policy %q", ErrConflict, decision, state.ReconcilePolicy)
	}
	payload := map[string]any{"effect_id": effectID, "decision": decision, "output": output}
	return j.appendBatchLocked([]Input{
		{EventType: EventEffectReconciled, AggregateType: "effect", AggregateID: effectID, Scope: scope, IdempotencyKey: "effect:" + effectID + ":reconciled", WriterID: owner, FencingToken: fencingToken, Status: StatusCommitted, Payload: payload},
		{EventType: EventToolFinished, AggregateType: "tool", AggregateID: callID, Scope: scope, IdempotencyKey: "tool:" + callID + ":reconciled", WriterID: owner, FencingToken: fencingToken, Status: StatusCommitted, Payload: output},
	})
}

// CompleteToolEffect atomically records the external receipt and the
// model-visible tool completion. A stale worker cannot commit either record.
func (j *Journal) CompleteToolEffect(scope Scope, effectID, callID, owner string, fencingToken int64, output any) (Event, error) {
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	effectState := j.effectStateLocked(scope, effectID)
	toolState := j.toolStateLocked(scope, callID)
	if effectState.ToolCallID != callID || !effectState.Dispatched || effectState.OutcomeUnknown || effectState.Receipted || effectState.Reconciled || !toolState.Started || toolState.Cancelled || toolState.Finished {
		return Event{}, ErrConflict
	}
	return j.appendBatchLocked([]Input{
		{EventType: EventEffectReceipted, AggregateType: "effect", AggregateID: effectID, Scope: scope, IdempotencyKey: "effect:" + effectID + ":receipt", WriterID: owner, FencingToken: fencingToken, Status: StatusCommitted, Payload: output},
		{EventType: EventToolFinished, AggregateType: "tool", AggregateID: callID, Scope: scope, IdempotencyKey: "tool:" + callID + ":finish", WriterID: owner, FencingToken: fencingToken, Status: StatusCommitted, Payload: output},
	})
}

// FailToolEffectOutput records that the external call returned a receipt but
// its output violated the frozen contract. The receipt and terminal failure
// are atomic, preventing a retry from executing the external effect again.
func (j *Journal) FailToolEffectOutput(scope Scope, effectID, callID, owner string, fencingToken int64, outputDigest string) (Event, error) {
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	effectState := j.effectStateLocked(scope, effectID)
	toolState := j.toolStateLocked(scope, callID)
	if effectState.ToolCallID != callID || !effectState.Dispatched || effectState.OutcomeUnknown || effectState.Receipted || effectState.Reconciled || !toolState.Started || toolState.Cancelled || toolState.Finished || toolState.Failed {
		return Event{}, ErrConflict
	}
	payload := map[string]any{"reason_code": "tool_output_schema_invalid", "output_digest": outputDigest}
	return j.appendBatchLocked([]Input{
		{EventType: EventEffectReceipted, AggregateType: "effect", AggregateID: effectID, Scope: scope, IdempotencyKey: "effect:" + effectID + ":receipt", WriterID: owner, FencingToken: fencingToken, Status: StatusCommitted, Payload: map[string]any{"output_digest": outputDigest, "valid": false}},
		{EventType: EventToolFailed, AggregateType: "tool", AggregateID: callID, Scope: scope, IdempotencyKey: "tool:" + callID + ":output-invalid", WriterID: owner, FencingToken: fencingToken, Status: StatusRejected, Payload: payload},
	})
}

// AuthorizeToolContract records a capability decision and the immutable
// contract digest before a tool can start. Tool names are never permissions.
func (j *Journal) AuthorizeToolContract(scope Scope, callID, owner string, fencingToken int64, contract ToolContract, allowedCapabilities []string) (Event, error) {
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	frozen, err := FreezeToolContract(contract)
	if err != nil {
		return Event{}, err
	}
	allowed := make(map[string]struct{}, len(allowedCapabilities))
	for _, capability := range allowedCapabilities {
		if capability = strings.TrimSpace(capability); capability != "" {
			allowed[capability] = struct{}{}
		}
	}
	for _, capability := range frozen.Capabilities {
		if _, ok := allowed[capability]; !ok {
			return Event{}, ErrUnauthorized
		}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	state := j.toolStateLocked(scope, callID)
	if state.Authorized && state.ContractDigest != frozen.ContractDigest {
		return Event{}, ErrIdempotencyConflict
	}
	if state.NotStarted {
		return Event{}, ErrConflict
	}
	payload := map[string]any{
		"call_id": callID, "name": frozen.Name, "capabilities": frozen.Capabilities,
		"allowed": true, "contract_digest": frozen.ContractDigest, "schema_version": frozen.SchemaVersion,
		"requires_approval": frozen.RequiresApproval,
	}
	return j.appendBatchLocked([]Input{{EventType: EventToolAuthorized, AggregateType: "tool", AggregateID: callID, Scope: scope, IdempotencyKey: "tool:" + callID + ":authorize", WriterID: owner, FencingToken: fencingToken, Payload: payload}})
}

func (j *Journal) toolHas(scope Scope, callID, eventType string) bool {
	for _, event := range j.events {
		if event.Scope == scope && event.AggregateID == callID && event.EventType == eventType {
			return true
		}
	}
	return false
}

func (j *Journal) toolEvents(scope Scope, callID string) []Event {
	items := make([]Event, 0)
	for _, event := range j.events {
		if event.Scope == scope && event.AggregateID == callID {
			items = append(items, event)
		}
	}
	return items
}

func (j *Journal) toolStateLocked(scope Scope, callID string) ToolState {
	state := ToolState{Scope: scope, CallID: callID}
	for _, event := range j.toolEvents(scope, callID) {
		state.LastEventID = event.EventID
		switch event.EventType {
		case EventToolAuthorized:
			state.Authorized = event.Status != StatusRejected
			var payload struct {
				Name             string   `json:"name"`
				Capabilities     []string `json:"capabilities"`
				RequiresApproval bool     `json:"requires_approval"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			if payload.Name != "" {
				state.Name = payload.Name
			}
			state.Capabilities = append([]string(nil), payload.Capabilities...)
			state.RequiresApproval = payload.RequiresApproval
			var contractPayload struct {
				ContractDigest string `json:"contract_digest"`
			}
			_ = json.Unmarshal(event.Payload, &contractPayload)
			state.ContractDigest = contractPayload.ContractDigest
		case EventToolStarted:
			state.Started = true
			state.Attempts++
		case EventToolApproved:
			var payload struct {
				Decision string `json:"decision"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			approved := payload.Decision == "approved"
			state.Approved = &approved
		case EventToolFinished:
			state.Finished = true
		case EventToolFailed:
			state.Failed = true
			var payload struct {
				ReasonCode string `json:"reason_code"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			state.ReasonCode = payload.ReasonCode
			if state.ReasonCode == "" {
				state.ReasonCode = "tool_execution_failed"
			}
		case EventToolCancelled:
			state.Cancelled = true
		case EventToolRetried:
			state.Attempts++
		case EventToolNotStarted:
			state.NotStarted = true
			var payload struct {
				ReasonCode string `json:"reason_code"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			state.ReasonCode = payload.ReasonCode
		}
	}
	return state
}

func (j *Journal) ToolState(scope Scope, callID string) (ToolState, error) {
	if !scope.valid() || strings.TrimSpace(callID) == "" {
		return ToolState{}, errors.New("scope and call_id are required")
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.toolStateLocked(scope, strings.TrimSpace(callID)), nil
}

// StartTool and FinishTool enforce the durable authorize → start → finish
// sequence. Providers may execute between these calls, but cannot claim a
// completed effect without the corresponding journal facts.
func (j *Journal) StartTool(scope Scope, callID, name, owner string, fencingToken int64, payload any) (Event, error) {
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	name = strings.TrimSpace(name)
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	state := j.toolStateLocked(scope, callID)
	if !state.Authorized {
		return Event{}, ErrUnauthorized
	}
	if state.Name != name {
		return Event{}, ErrIdempotencyConflict
	}
	if state.Cancelled || state.Finished || state.Failed || state.NotStarted {
		return Event{}, ErrConflict
	}
	if state.RequiresApproval && state.Approved == nil {
		return Event{}, ErrApprovalRequired
	}
	if state.Approved != nil && !*state.Approved {
		return Event{}, ErrUnauthorized
	}
	if state.Started {
		for _, event := range j.events {
			if event.Scope == scope && event.AggregateID == callID && event.EventType == EventToolStarted {
				return cloneEvent(event), nil
			}
		}
	}
	return j.appendBatchLocked([]Input{{EventType: EventToolStarted, AggregateType: "tool", AggregateID: callID, Scope: scope, IdempotencyKey: "tool:" + callID + ":start", WriterID: owner, FencingToken: fencingToken, Status: StatusPending, Payload: payload}})
}

func (j *Journal) StartToolWithContract(scope Scope, callID, owner string, fencingToken int64, contract ToolContract, payload any, allowed []string) (Event, error) {
	frozen, err := FreezeToolContract(contract)
	if err != nil {
		return Event{}, err
	}
	if _, err := validateToolInput(frozen, payload); err != nil {
		return Event{}, err
	}
	if _, err := j.AuthorizeToolContract(scope, callID, owner, fencingToken, frozen, allowed); err != nil {
		return Event{}, err
	}
	if frozen.RequiresApproval {
		j.mu.RLock()
		state := j.toolStateLocked(scope, callID)
		j.mu.RUnlock()
		if state.Approved == nil {
			return Event{}, ErrApprovalRequired
		}
		if !*state.Approved {
			return Event{}, ErrUnauthorized
		}
	}
	return j.StartTool(scope, callID, frozen.Name, owner, fencingToken, payload)
}

func (j *Journal) MarkToolNotStarted(scope Scope, callID, owner string, fencingToken int64, reason string) (Event, error) {
	if strings.TrimSpace(callID) == "" {
		return Event{}, errors.New("call_id is required")
	}
	if strings.TrimSpace(reason) == "" {
		reason = "batch_aborted"
	}
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	state := j.toolStateLocked(scope, callID)
	if state.Started || state.Finished || state.Failed || state.Cancelled {
		return Event{}, ErrConflict
	}
	return j.appendBatchLocked([]Input{{EventType: EventToolNotStarted, AggregateType: "tool", AggregateID: callID, Scope: scope, IdempotencyKey: "tool:" + callID + ":not-started", WriterID: owner, FencingToken: fencingToken, Status: StatusRejected, Payload: map[string]any{"reason_code": reason}}})
}

func (j *Journal) ApproveTool(scope Scope, callID, owner string, fencingToken int64, decision string) (Event, error) {
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	decision = strings.ToLower(strings.TrimSpace(decision))
	if decision != "approved" && decision != "denied" {
		return Event{}, errors.New("tool approval must be approved or denied")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	state := j.toolStateLocked(scope, callID)
	if !state.Authorized {
		return Event{}, ErrUnauthorized
	}
	if state.Approved != nil {
		priorDecision := "denied"
		if *state.Approved {
			priorDecision = "approved"
		}
		for _, event := range j.events {
			if event.Scope == scope && event.AggregateID == callID && event.EventType == EventToolApproved {
				if priorDecision == decision {
					return cloneEvent(event), nil
				}
				return Event{}, ErrIdempotencyConflict
			}
		}
		return Event{}, ErrCorrupt
	}
	if state.Started || state.Finished || state.Failed || state.Cancelled || state.NotStarted {
		return Event{}, ErrConflict
	}
	return j.appendBatchLocked([]Input{{EventType: EventToolApproved, AggregateType: "tool", AggregateID: callID, Scope: scope, IdempotencyKey: "tool:" + callID + ":approval", WriterID: owner, FencingToken: fencingToken, Status: map[bool]string{true: StatusCommitted, false: StatusRejected}[decision == "approved"], Payload: map[string]any{"decision": decision}}})
}

func (j *Journal) FinishTool(scope Scope, callID, owner string, fencingToken int64, output any) (Event, error) {
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	state := j.toolStateLocked(scope, callID)
	denied := false
	for _, event := range j.events {
		if event.Scope == scope && event.AggregateID == callID && event.EventType == EventToolApproved && event.Status == StatusRejected {
			denied = true
		}
	}
	if !state.Started {
		return Event{}, ErrConflict
	}
	if denied || state.Cancelled || state.Failed || state.NotStarted {
		return Event{}, ErrUnauthorized
	}
	if state.Finished {
		return Event{}, ErrConflict
	}
	return j.appendBatchLocked([]Input{{EventType: EventToolFinished, AggregateType: "tool", AggregateID: callID, Scope: scope, IdempotencyKey: "tool:" + callID + ":finish", WriterID: owner, FencingToken: fencingToken, Status: StatusCommitted, Payload: output}})
}

func (j *Journal) CancelTool(scope Scope, callID, owner string, fencingToken int64, reason string) (Event, error) {
	if strings.TrimSpace(reason) == "" {
		reason = "cancelled"
	}
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	state := j.toolStateLocked(scope, callID)
	if !state.Started || state.Finished {
		return Event{}, ErrConflict
	}
	return j.appendBatchLocked([]Input{{EventType: EventToolCancelled, AggregateType: "tool", AggregateID: callID, Scope: scope, IdempotencyKey: "tool:" + callID + ":cancel", WriterID: owner, FencingToken: fencingToken, Status: StatusRejected, Payload: map[string]any{"reason": reason}}})
}

// RetryTool creates a new call lineage. The original call remains immutable;
// retrying with the same lineage and attempt number is idempotent.
func (j *Journal) RetryTool(scope Scope, callID, owner string, fencingToken int64, attempt int, reason string) (string, Event, error) {
	if attempt < 1 {
		attempt = 1
	}
	if strings.TrimSpace(callID) == "" {
		return "", Event{}, errors.New("call_id is required")
	}
	newID := fmt.Sprintf("%s:retry:%d", callID, attempt)
	if strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return "", Event{}, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return "", Event{}, err
	}
	event, err := j.appendBatchLocked([]Input{{EventType: EventToolRetried, AggregateType: "tool", AggregateID: callID, Scope: scope, IdempotencyKey: "tool:" + callID + ":retry:" + fmt.Sprint(attempt), WriterID: owner, FencingToken: fencingToken, Status: StatusCommitted, Payload: map[string]any{"new_call_id": newID, "reason": reason, "attempt": attempt}}})
	return newID, event, err
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
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return ""
	}
	return digest(compact.Bytes())
}

func canonicalPayloadDigest(data []byte) string {
	canonical, err := coreencoding.Canonicalize(data)
	if err != nil {
		return ""
	}
	return digest(canonical)
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
