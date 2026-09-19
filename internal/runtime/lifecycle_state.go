package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	coreencoding "github.com/adro-project/adro/core/encoding"
)

const (
	EventSessionStarted    = "session.started"
	EventSessionSuspended  = "session.suspended"
	EventSessionResumed    = "session.resumed"
	EventSessionCancelling = "session.cancelling"
	EventSessionCancelled  = "session.cancelled"
	EventSessionCompleted  = "session.completed"
	EventSessionFailed     = "session.failed"

	EventTurnCreated         = "turn.created"
	EventTurnContextFrozen   = "turn.context_frozen"
	EventTurnResumed         = "turn.resumed"
	EventTurnWaitingApproval = "turn.waiting_approval"
	EventTurnWaitingInput    = "turn.waiting_input"
	EventTurnSuspended       = "turn.suspended"
	EventTurnCheckpointing   = "turn.checkpointing"
	EventTurnFailed          = "turn.failed"
	EventTurnCancelled       = "turn.cancelled"

	EventStepCreated             = "step.created"
	EventStepContextFrozen       = "step.context_frozen"
	EventStepModelCommitted      = "step.model_request_committed"
	EventStepModelStreaming      = "step.model_streaming"
	EventStepToolRequestsReady   = "step.tool_requests_ready"
	EventStepEffectsRunning      = "step.effects_running"
	EventStepResultAssembled     = "step.result_assembled"
	EventStepCheckpointed        = "step.checkpointed"
	EventStepCompleted           = "step.completed"
	EventStepModelOutcomeUnknown = "step.model_outcome_unknown"
	EventStepEffectUnknown       = "step.effect_outcome_unknown"
	EventStepCancelling          = "step.cancelling"
	EventStepCancelled           = "step.cancelled"
	EventStepFailed              = "step.failed"
)

const (
	SessionNew        = "new"
	SessionActive     = "active"
	SessionSuspended  = "suspended"
	SessionCancelling = "cancelling"
	SessionCancelled  = "cancelled"
	SessionCompleted  = "completed"
	SessionFailed     = "failed"

	TurnCreated         = "created"
	TurnContextFrozen   = "context_frozen"
	TurnRunning         = "running"
	TurnWaitingApproval = "waiting_approval"
	TurnWaitingInput    = "waiting_input"
	TurnSuspended       = "suspended"
	TurnCheckpointing   = "checkpointing"
	TurnCompleted       = "completed"
	TurnFailed          = "failed"
	TurnCancelled       = "cancelled"

	StepPending              = "pending"
	StepContextFrozen        = "context_frozen"
	StepModelCommitted       = "model_request_committed"
	StepModelStreaming       = "model_streaming"
	StepToolRequestsReady    = "tool_requests_ready"
	StepEffectsRunning       = "effects_running"
	StepResultAssembled      = "result_assembled"
	StepCheckpointed         = "checkpointed"
	StepCompleted            = "completed"
	StepModelOutcomeUnknown  = "model_outcome_unknown"
	StepEffectOutcomeUnknown = "effect_outcome_unknown"
	StepCancelling           = "cancelling"
	StepCancelled            = "cancelled"
	StepFailed               = "failed"
)

type StopReason string

const (
	StopCompleted              StopReason = "completed"
	StopMaxSteps               StopReason = "max_steps"
	StopBudgetExhausted        StopReason = "budget_exhausted"
	StopDeadlineExceeded       StopReason = "deadline_exceeded"
	StopCancelledByUser        StopReason = "cancelled_by_user"
	StopCancelledByParent      StopReason = "cancelled_by_parent"
	StopPolicyDenied           StopReason = "policy_denied"
	StopApprovalDenied         StopReason = "approval_denied"
	StopProviderFailed         StopReason = "provider_failed"
	StopProviderOutcomeUnknown StopReason = "provider_outcome_unknown"
	StopEffectOutcomeUnknown   StopReason = "effect_outcome_unknown"
	StopContextOverflow        StopReason = "context_overflow"
	StopSandboxUnavailable     StopReason = "sandbox_unavailable"
	StopStorageConflict        StopReason = "storage_conflict"
	StopRuntimeInvariantFailed StopReason = "runtime_invariant_failed"
)

func (r StopReason) Valid() bool {
	switch r {
	case StopCompleted, StopMaxSteps, StopBudgetExhausted, StopDeadlineExceeded,
		StopCancelledByUser, StopCancelledByParent, StopPolicyDenied,
		StopApprovalDenied, StopProviderFailed, StopProviderOutcomeUnknown,
		StopEffectOutcomeUnknown, StopContextOverflow, StopSandboxUnavailable,
		StopStorageConflict, StopRuntimeInvariantFailed:
		return true
	default:
		return false
	}
}

var (
	ErrSessionTransition     = errors.New("runtime session transition is invalid")
	ErrTurnTransition        = errors.New("runtime turn transition is invalid")
	ErrStepTransition        = errors.New("runtime step transition is invalid")
	ErrConfigSnapshotInvalid = errors.New("runtime config snapshot is invalid")
	ErrStepContextInvalid    = errors.New("runtime step context snapshot is invalid")
	ErrStopReasonInvalid     = errors.New("runtime stop reason is invalid")
	ErrLifecycleInputInvalid = errors.New("runtime lifecycle input is invalid")
)

// ConfigSnapshot freezes all behavior-affecting configuration at a safe
// runtime boundary. Its digest excludes no semantic field, including capture
// time, so a replay can prove it used the original snapshot.
type ConfigSnapshot struct {
	Version          string            `json:"version"`
	Digest           string            `json:"digest"`
	FeatureGates     map[string]bool   `json:"feature_gates,omitempty"`
	AdapterVersions  map[string]string `json:"adapter_versions,omitempty"`
	ProtocolVersions map[string]string `json:"protocol_versions,omitempty"`
	PolicyBundle     string            `json:"policy_bundle"`
	TokenizerID      string            `json:"tokenizer_id"`
	CapturedAt       time.Time         `json:"captured_at"`
}

func FreezeConfigSnapshot(snapshot ConfigSnapshot) (ConfigSnapshot, error) {
	snapshot.Version = strings.TrimSpace(snapshot.Version)
	snapshot.PolicyBundle = strings.TrimSpace(snapshot.PolicyBundle)
	snapshot.TokenizerID = strings.TrimSpace(snapshot.TokenizerID)
	snapshot.CapturedAt = snapshot.CapturedAt.UTC().Truncate(time.Microsecond)
	if snapshot.Version == "" || snapshot.PolicyBundle == "" || snapshot.TokenizerID == "" || snapshot.CapturedAt.IsZero() {
		return ConfigSnapshot{}, ErrConfigSnapshotInvalid
	}
	if err := validateStringMap(snapshot.AdapterVersions); err != nil {
		return ConfigSnapshot{}, fmt.Errorf("%w: adapter_versions: %v", ErrConfigSnapshotInvalid, err)
	}
	if err := validateStringMap(snapshot.ProtocolVersions); err != nil {
		return ConfigSnapshot{}, fmt.Errorf("%w: protocol_versions: %v", ErrConfigSnapshotInvalid, err)
	}
	if err := validateBoolMap(snapshot.FeatureGates); err != nil {
		return ConfigSnapshot{}, fmt.Errorf("%w: feature_gates: %v", ErrConfigSnapshotInvalid, err)
	}
	snapshot.FeatureGates = cloneBoolMap(snapshot.FeatureGates)
	snapshot.AdapterVersions = cloneStringMap(snapshot.AdapterVersions)
	snapshot.ProtocolVersions = cloneStringMap(snapshot.ProtocolVersions)
	snapshot.Digest = ""
	digest, err := coreencoding.Digest(snapshot)
	if err != nil {
		return ConfigSnapshot{}, fmt.Errorf("%w: %v", ErrConfigSnapshotInvalid, err)
	}
	snapshot.Digest = digest
	return snapshot, nil
}

func (s ConfigSnapshot) Validate() error {
	frozen, err := FreezeConfigSnapshot(s)
	if err != nil {
		return err
	}
	if frozen.Digest != s.Digest {
		return fmt.Errorf("%w: digest mismatch", ErrConfigSnapshotInvalid)
	}
	return nil
}

type StepContextSnapshot struct {
	ContextManifestDigest string         `json:"context_manifest_digest"`
	ToolCatalogDigest     string         `json:"tool_catalog_digest"`
	PolicySnapshotDigest  string         `json:"policy_snapshot_digest"`
	Config                ConfigSnapshot `json:"config"`
	FrozenAt              time.Time      `json:"frozen_at"`
	Digest                string         `json:"digest"`
}

func FreezeStepContext(snapshot StepContextSnapshot) (StepContextSnapshot, error) {
	snapshot.ContextManifestDigest = strings.TrimSpace(snapshot.ContextManifestDigest)
	snapshot.ToolCatalogDigest = strings.TrimSpace(snapshot.ToolCatalogDigest)
	snapshot.PolicySnapshotDigest = strings.TrimSpace(snapshot.PolicySnapshotDigest)
	snapshot.FrozenAt = snapshot.FrozenAt.UTC().Truncate(time.Microsecond)
	if snapshot.ContextManifestDigest == "" || snapshot.ToolCatalogDigest == "" || snapshot.PolicySnapshotDigest == "" || snapshot.FrozenAt.IsZero() {
		return StepContextSnapshot{}, ErrStepContextInvalid
	}
	if err := snapshot.Config.Validate(); err != nil {
		return StepContextSnapshot{}, fmt.Errorf("%w: config: %v", ErrStepContextInvalid, err)
	}
	snapshot.Digest = ""
	digest, err := coreencoding.Digest(snapshot)
	if err != nil {
		return StepContextSnapshot{}, fmt.Errorf("%w: %v", ErrStepContextInvalid, err)
	}
	snapshot.Digest = digest
	return snapshot, nil
}

func (s StepContextSnapshot) Validate() error {
	frozen, err := FreezeStepContext(s)
	if err != nil {
		return err
	}
	if frozen.Digest != s.Digest {
		return fmt.Errorf("%w: digest mismatch", ErrStepContextInvalid)
	}
	return nil
}

type SessionState struct {
	Scope       Scope      `json:"scope"`
	SessionID   string     `json:"session_id"`
	Status      string     `json:"status"`
	StopReason  StopReason `json:"stop_reason,omitempty"`
	LastEventID string     `json:"last_event_id,omitempty"`
}

type TurnState struct {
	Scope                Scope      `json:"scope"`
	TurnID               string     `json:"turn_id"`
	SessionID            string     `json:"session_id,omitempty"`
	Status               string     `json:"status"`
	ConfigSnapshotDigest string     `json:"config_snapshot_digest,omitempty"`
	StopReason           StopReason `json:"stop_reason,omitempty"`
	LastEventID          string     `json:"last_event_id,omitempty"`
}

type StepState struct {
	Scope                 Scope      `json:"scope"`
	StepID                string     `json:"step_id"`
	TurnID                string     `json:"turn_id,omitempty"`
	Status                string     `json:"status"`
	ContextSnapshotDigest string     `json:"context_snapshot_digest,omitempty"`
	ModelRequestID        string     `json:"model_request_id,omitempty"`
	StopReason            StopReason `json:"stop_reason,omitempty"`
	LastEventID           string     `json:"last_event_id,omitempty"`
}

func (s SessionState) Terminal() bool {
	return s.Status == SessionCompleted || s.Status == SessionCancelled || s.Status == SessionFailed
}

func (s TurnState) Terminal() bool {
	return s.Status == TurnCompleted || s.Status == TurnCancelled || s.Status == TurnFailed
}

func (s StepState) Terminal() bool {
	return s.Status == StepCompleted || s.Status == StepCancelled || s.Status == StepFailed
}

func (j *Journal) sessionStateLocked(scope Scope) SessionState {
	state := SessionState{Scope: scope, SessionID: scope.SessionID, Status: SessionNew}
	for _, item := range j.events {
		if item.Scope != scope || item.AggregateType != "session" || item.AggregateID != scope.SessionID {
			continue
		}
		state.LastEventID = item.EventID
		switch item.EventType {
		case EventSessionStarted, EventSessionResumed:
			state.Status = SessionActive
		case EventSessionSuspended:
			state.Status = SessionSuspended
		case EventSessionCancelling:
			state.Status, state.StopReason = SessionCancelling, payloadStopReason(item.Payload)
		case EventSessionCancelled:
			state.Status, state.StopReason = SessionCancelled, payloadStopReason(item.Payload)
		case EventSessionCompleted:
			state.Status, state.StopReason = SessionCompleted, payloadStopReason(item.Payload)
		case EventSessionFailed:
			state.Status, state.StopReason = SessionFailed, payloadStopReason(item.Payload)
		}
	}
	return state
}

func (j *Journal) turnStateLocked(scope Scope, turnID string) TurnState {
	state := TurnState{Scope: scope, TurnID: turnID}
	for _, item := range j.events {
		if item.Scope != scope || item.AggregateType != "turn" || item.AggregateID != turnID {
			continue
		}
		state.LastEventID = item.EventID
		switch item.EventType {
		case EventTurnCreated:
			state.Status, state.SessionID = TurnCreated, payloadString(item.Payload, "session_id")
		case EventTurnContextFrozen:
			state.Status = TurnContextFrozen
			state.ConfigSnapshotDigest = payloadString(item.Payload, "config_snapshot_digest")
		case EventTurnStarted, EventTurnResumed:
			state.Status = TurnRunning
		case EventTurnWaitingApproval:
			state.Status = TurnWaitingApproval
		case EventTurnWaitingInput:
			state.Status = TurnWaitingInput
		case EventTurnSuspended:
			state.Status = TurnSuspended
		case EventTurnCheckpointing:
			state.Status = TurnCheckpointing
		case EventTurnFinished:
			state.Status, state.StopReason = TurnCompleted, payloadStopReason(item.Payload)
		case EventTurnFailed:
			state.Status, state.StopReason = TurnFailed, payloadStopReason(item.Payload)
		case EventTurnCancelled:
			state.Status, state.StopReason = TurnCancelled, payloadStopReason(item.Payload)
		}
	}
	return state
}

func (j *Journal) stepStateLocked(scope Scope, stepID string) StepState {
	state := StepState{Scope: scope, StepID: stepID}
	for _, item := range j.events {
		if item.Scope != scope || item.AggregateType != "step" || item.AggregateID != stepID {
			continue
		}
		state.LastEventID = item.EventID
		switch item.EventType {
		case EventStepCreated:
			state.Status, state.TurnID = StepPending, payloadString(item.Payload, "turn_id")
		case EventStepContextFrozen:
			state.Status = StepContextFrozen
			state.ContextSnapshotDigest = payloadString(item.Payload, "context_snapshot_digest")
		case EventStepModelCommitted:
			state.Status = StepModelCommitted
			state.ModelRequestID = payloadString(item.Payload, "model_request_id")
		case EventStepModelStreaming:
			state.Status = StepModelStreaming
		case EventStepToolRequestsReady:
			state.Status = StepToolRequestsReady
		case EventStepEffectsRunning:
			state.Status = StepEffectsRunning
		case EventStepResultAssembled:
			state.Status = StepResultAssembled
		case EventStepCheckpointed:
			state.Status = StepCheckpointed
		case EventStepCompleted:
			state.Status, state.StopReason = StepCompleted, payloadStopReason(item.Payload)
		case EventStepModelOutcomeUnknown:
			state.Status, state.StopReason = StepModelOutcomeUnknown, StopProviderOutcomeUnknown
		case EventStepEffectUnknown:
			state.Status, state.StopReason = StepEffectOutcomeUnknown, StopEffectOutcomeUnknown
		case EventStepCancelling:
			state.Status, state.StopReason = StepCancelling, payloadStopReason(item.Payload)
		case EventStepCancelled:
			state.Status, state.StopReason = StepCancelled, payloadStopReason(item.Payload)
		case EventStepFailed:
			state.Status, state.StopReason = StepFailed, payloadStopReason(item.Payload)
		}
	}
	return state
}

func payloadMap(raw []byte) map[string]any {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	return payload
}

func payloadString(raw []byte, key string) string {
	value, _ := payloadMap(raw)[key].(string)
	return value
}

func payloadStopReason(raw []byte) StopReason { return StopReason(payloadString(raw, "stop_reason")) }

func (j *Journal) SessionState(scope Scope) (SessionState, error) {
	if !scope.valid() {
		return SessionState{}, errors.New("scope is required")
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.sessionStateLocked(scope), nil
}

func (j *Journal) TurnState(scope Scope, turnID string) (TurnState, error) {
	if !scope.valid() || strings.TrimSpace(turnID) == "" {
		return TurnState{}, errors.New("scope and turn_id are required")
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.turnStateLocked(scope, strings.TrimSpace(turnID)), nil
}

func (j *Journal) StepState(scope Scope, stepID string) (StepState, error) {
	if !scope.valid() || strings.TrimSpace(stepID) == "" {
		return StepState{}, errors.New("scope and step_id are required")
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.stepStateLocked(scope, strings.TrimSpace(stepID)), nil
}

func (j *Journal) StartSession(scope Scope, owner string, fencingToken int64, payload any) (Event, error) {
	return j.sessionTransition(scope, EventSessionStarted, owner, fencingToken, StatusPending, map[string]any{"session_id": scope.SessionID, "payload": payload}, func(state SessionState) bool {
		return state.Status == SessionNew
	})
}

func (j *Journal) SuspendSession(scope Scope, owner string, fencingToken int64, reason string) (Event, error) {
	return j.sessionTransition(scope, EventSessionSuspended, owner, fencingToken, StatusPending, map[string]any{"session_id": scope.SessionID, "reason_code": normalizedCode(reason, "suspended")}, func(state SessionState) bool {
		return state.Status == SessionActive
	})
}

func (j *Journal) ResumeSession(scope Scope, owner string, fencingToken int64) (Event, error) {
	return j.sessionTransition(scope, EventSessionResumed, owner, fencingToken, StatusPending, map[string]any{"session_id": scope.SessionID}, func(state SessionState) bool {
		return state.Status == SessionSuspended
	})
}

func (j *Journal) BeginSessionCancellation(scope Scope, owner string, fencingToken int64, reason StopReason) (Event, error) {
	if !reason.Valid() || (reason != StopCancelledByUser && reason != StopCancelledByParent) {
		return Event{}, ErrStopReasonInvalid
	}
	return j.sessionTransition(scope, EventSessionCancelling, owner, fencingToken, StatusPending, map[string]any{"session_id": scope.SessionID, "stop_reason": reason}, func(state SessionState) bool {
		return state.Status == SessionActive || state.Status == SessionSuspended
	})
}

func (j *Journal) CancelSession(scope Scope, owner string, fencingToken int64, reason StopReason) (Event, error) {
	if !reason.Valid() || (reason != StopCancelledByUser && reason != StopCancelledByParent) {
		return Event{}, ErrStopReasonInvalid
	}
	return j.sessionTransition(scope, EventSessionCancelled, owner, fencingToken, StatusRejected, map[string]any{"session_id": scope.SessionID, "stop_reason": reason}, func(state SessionState) bool {
		return state.Status == SessionCancelling && !j.hasNonterminalTurnsLocked(scope)
	})
}

func (j *Journal) CompleteSession(scope Scope, owner string, fencingToken int64, reason StopReason) (Event, error) {
	if !reason.Valid() || (reason != StopCompleted && reason != StopMaxSteps && reason != StopBudgetExhausted && reason != StopDeadlineExceeded) {
		return Event{}, ErrStopReasonInvalid
	}
	return j.sessionTransition(scope, EventSessionCompleted, owner, fencingToken, StatusCommitted, map[string]any{"session_id": scope.SessionID, "stop_reason": reason}, func(state SessionState) bool {
		return (state.Status == SessionActive || state.Status == SessionSuspended) && !j.hasNonterminalTurnsLocked(scope)
	})
}

func (j *Journal) FailSession(scope Scope, owner string, fencingToken int64, reason StopReason) (Event, error) {
	if !reason.Valid() || reason == StopCompleted || reason == StopMaxSteps || reason == StopBudgetExhausted || reason == StopCancelledByUser || reason == StopCancelledByParent {
		return Event{}, ErrStopReasonInvalid
	}
	return j.sessionTransition(scope, EventSessionFailed, owner, fencingToken, StatusRejected, map[string]any{"session_id": scope.SessionID, "stop_reason": reason}, func(state SessionState) bool {
		return (state.Status == SessionActive || state.Status == SessionSuspended || state.Status == SessionCancelling) && !j.hasNonterminalTurnsLocked(scope)
	})
}

func (j *Journal) sessionTransition(scope Scope, eventType, owner string, fencingToken int64, status string, payload map[string]any, allowed func(SessionState) bool) (Event, error) {
	if !scope.valid() {
		return Event{}, fmt.Errorf("%w: scope is required", ErrLifecycleInputInvalid)
	}
	owner = strings.TrimSpace(owner)
	if owner == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	if err := j.validateLifecycleLeaseLocked(scope, owner, fencingToken); err != nil {
		return Event{}, err
	}
	state := j.sessionStateLocked(scope)
	input, retry := lifecycleInput(j.events, scope, "session", scope.SessionID, eventType, owner, fencingToken, status, payload, state.LastEventID)
	if retry {
		return j.appendBatchLocked([]Input{input})
	}
	if !allowed(state) {
		return Event{}, ErrSessionTransition
	}
	return j.appendBatchLocked([]Input{input})
}

func (j *Journal) CreateTurn(scope Scope, turnID, owner string, fencingToken int64) (Event, error) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return Event{}, errors.New("turn_id is required")
	}
	return j.turnTransition(scope, turnID, EventTurnCreated, owner, fencingToken, StatusPending, map[string]any{"turn_id": turnID, "session_id": scope.SessionID}, func(session SessionState, state TurnState) bool {
		return session.Status == SessionActive && state.Status == ""
	})
}

func (j *Journal) FreezeTurnContext(scope Scope, turnID, owner string, fencingToken int64, config ConfigSnapshot) (Event, error) {
	frozen, err := FreezeConfigSnapshot(config)
	if err != nil || frozen.Digest != config.Digest {
		if err != nil {
			return Event{}, err
		}
		return Event{}, fmt.Errorf("%w: digest mismatch", ErrConfigSnapshotInvalid)
	}
	return j.turnTransition(scope, strings.TrimSpace(turnID), EventTurnContextFrozen, owner, fencingToken, StatusPending, map[string]any{"turn_id": strings.TrimSpace(turnID), "config_snapshot": frozen, "config_snapshot_digest": frozen.Digest}, func(_ SessionState, state TurnState) bool {
		return state.Status == TurnCreated
	})
}

func (j *Journal) StartTurn(scope Scope, turnID, owner string, fencingToken int64) (Event, error) {
	return j.turnTransition(scope, strings.TrimSpace(turnID), EventTurnStarted, owner, fencingToken, StatusPending, map[string]any{"turn_id": strings.TrimSpace(turnID)}, func(session SessionState, state TurnState) bool {
		return session.Status == SessionActive && state.Status == TurnContextFrozen
	})
}

func (j *Journal) WaitTurnApproval(scope Scope, turnID, owner string, fencingToken int64) (Event, error) {
	return j.turnTransition(scope, strings.TrimSpace(turnID), EventTurnWaitingApproval, owner, fencingToken, StatusPending, map[string]any{"turn_id": strings.TrimSpace(turnID)}, func(_ SessionState, state TurnState) bool { return state.Status == TurnRunning })
}

func (j *Journal) WaitTurnInput(scope Scope, turnID, owner string, fencingToken int64) (Event, error) {
	return j.turnTransition(scope, strings.TrimSpace(turnID), EventTurnWaitingInput, owner, fencingToken, StatusPending, map[string]any{"turn_id": strings.TrimSpace(turnID)}, func(_ SessionState, state TurnState) bool { return state.Status == TurnRunning })
}

func (j *Journal) ResumeTurn(scope Scope, turnID, owner string, fencingToken int64) (Event, error) {
	return j.turnTransition(scope, strings.TrimSpace(turnID), EventTurnResumed, owner, fencingToken, StatusPending, map[string]any{"turn_id": strings.TrimSpace(turnID)}, func(session SessionState, state TurnState) bool {
		return session.Status == SessionActive && (state.Status == TurnWaitingApproval || state.Status == TurnWaitingInput || state.Status == TurnSuspended)
	})
}

func (j *Journal) SuspendTurn(scope Scope, turnID, owner string, fencingToken int64, reason string) (Event, error) {
	return j.turnTransition(scope, strings.TrimSpace(turnID), EventTurnSuspended, owner, fencingToken, StatusRecoveryNeeded, map[string]any{"turn_id": strings.TrimSpace(turnID), "reason_code": normalizedCode(reason, "suspended")}, func(_ SessionState, state TurnState) bool {
		return state.Status == TurnRunning || state.Status == TurnWaitingApproval || state.Status == TurnWaitingInput
	})
}

func (j *Journal) BeginTurnCheckpoint(scope Scope, turnID, owner string, fencingToken int64) (Event, error) {
	turnID = strings.TrimSpace(turnID)
	return j.turnTransition(scope, turnID, EventTurnCheckpointing, owner, fencingToken, StatusPending, map[string]any{"turn_id": turnID}, func(_ SessionState, state TurnState) bool {
		return state.Status == TurnRunning && !j.hasNonterminalStepsLocked(scope, turnID)
	})
}

func (j *Journal) CompleteTurn(scope Scope, turnID, owner string, fencingToken int64, reason StopReason) (Event, error) {
	if !reason.Valid() || (reason != StopCompleted && reason != StopMaxSteps && reason != StopBudgetExhausted && reason != StopDeadlineExceeded) {
		return Event{}, ErrStopReasonInvalid
	}
	return j.turnTransition(scope, strings.TrimSpace(turnID), EventTurnFinished, owner, fencingToken, StatusCommitted, map[string]any{"turn_id": strings.TrimSpace(turnID), "stop_reason": reason}, func(_ SessionState, state TurnState) bool { return state.Status == TurnCheckpointing })
}

func (j *Journal) FailTurn(scope Scope, turnID, owner string, fencingToken int64, reason StopReason) (Event, error) {
	if !reason.Valid() || reason == StopCompleted || reason == StopMaxSteps || reason == StopBudgetExhausted || reason == StopCancelledByUser || reason == StopCancelledByParent {
		return Event{}, ErrStopReasonInvalid
	}
	turnID = strings.TrimSpace(turnID)
	return j.turnTransition(scope, turnID, EventTurnFailed, owner, fencingToken, StatusRejected, map[string]any{"turn_id": turnID, "stop_reason": reason}, func(_ SessionState, state TurnState) bool {
		return state.Status != "" && !state.Terminal() && !j.hasNonterminalStepsLocked(scope, turnID)
	})
}

func (j *Journal) CancelTurn(scope Scope, turnID, owner string, fencingToken int64, reason StopReason) (Event, error) {
	if reason != StopCancelledByUser && reason != StopCancelledByParent {
		return Event{}, ErrStopReasonInvalid
	}
	turnID = strings.TrimSpace(turnID)
	return j.turnTransition(scope, turnID, EventTurnCancelled, owner, fencingToken, StatusRejected, map[string]any{"turn_id": turnID, "stop_reason": reason}, func(_ SessionState, state TurnState) bool {
		return state.Status != "" && !state.Terminal() && !j.hasNonterminalStepsLocked(scope, turnID)
	})
}

func (j *Journal) turnTransition(scope Scope, turnID, eventType, owner string, fencingToken int64, status string, payload map[string]any, allowed func(SessionState, TurnState) bool) (Event, error) {
	if !scope.valid() || turnID == "" {
		return Event{}, fmt.Errorf("%w: scope and turn_id are required", ErrLifecycleInputInvalid)
	}
	owner = strings.TrimSpace(owner)
	if owner == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	if err := j.validateLifecycleLeaseLocked(scope, owner, fencingToken); err != nil {
		return Event{}, err
	}
	state := j.turnStateLocked(scope, turnID)
	input, retry := lifecycleInput(j.events, scope, "turn", turnID, eventType, owner, fencingToken, status, payload, state.LastEventID)
	if retry {
		return j.appendBatchLocked([]Input{input})
	}
	if !allowed(j.sessionStateLocked(scope), state) {
		return Event{}, ErrTurnTransition
	}
	return j.appendBatchLocked([]Input{input})
}

func (j *Journal) CreateStep(scope Scope, stepID, turnID, owner string, fencingToken int64) (Event, error) {
	stepID, turnID = strings.TrimSpace(stepID), strings.TrimSpace(turnID)
	if stepID == "" || turnID == "" {
		return Event{}, errors.New("step_id and turn_id are required")
	}
	return j.stepTransition(scope, stepID, turnID, EventStepCreated, owner, fencingToken, StatusPending, map[string]any{"step_id": stepID, "turn_id": turnID}, func(turn TurnState, state StepState) bool {
		return turn.Status == TurnRunning && state.Status == ""
	})
}

func (j *Journal) FreezeStepContext(scope Scope, stepID, owner string, fencingToken int64, snapshot StepContextSnapshot) (Event, error) {
	frozen, err := FreezeStepContext(snapshot)
	if err != nil || frozen.Digest != snapshot.Digest {
		if err != nil {
			return Event{}, err
		}
		return Event{}, fmt.Errorf("%w: digest mismatch", ErrStepContextInvalid)
	}
	return j.stepTransition(scope, strings.TrimSpace(stepID), "", EventStepContextFrozen, owner, fencingToken, StatusPending, map[string]any{"step_id": strings.TrimSpace(stepID), "context_snapshot": frozen, "context_snapshot_digest": frozen.Digest}, func(turn TurnState, state StepState) bool {
		return turn.Status == TurnRunning && state.Status == StepPending
	})
}

func (j *Journal) CommitStepModelRequest(scope Scope, stepID, requestID, owner string, fencingToken int64) (Event, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return Event{}, errors.New("model request_id is required")
	}
	return j.stepTransition(scope, strings.TrimSpace(stepID), "", EventStepModelCommitted, owner, fencingToken, StatusPending, map[string]any{"step_id": strings.TrimSpace(stepID), "model_request_id": requestID}, func(_ TurnState, state StepState) bool { return state.Status == StepContextFrozen })
}

func (j *Journal) StartStepModelStream(scope Scope, stepID, owner string, fencingToken int64) (Event, error) {
	return j.stepTransition(scope, strings.TrimSpace(stepID), "", EventStepModelStreaming, owner, fencingToken, StatusPending, map[string]any{"step_id": strings.TrimSpace(stepID)}, func(_ TurnState, state StepState) bool { return state.Status == StepModelCommitted })
}

func (j *Journal) MarkStepToolRequestsReady(scope Scope, stepID, owner string, fencingToken int64) (Event, error) {
	return j.stepTransition(scope, strings.TrimSpace(stepID), "", EventStepToolRequestsReady, owner, fencingToken, StatusPending, map[string]any{"step_id": strings.TrimSpace(stepID)}, func(_ TurnState, state StepState) bool { return state.Status == StepModelStreaming })
}

func (j *Journal) StartStepEffects(scope Scope, stepID, owner string, fencingToken int64) (Event, error) {
	return j.stepTransition(scope, strings.TrimSpace(stepID), "", EventStepEffectsRunning, owner, fencingToken, StatusPending, map[string]any{"step_id": strings.TrimSpace(stepID)}, func(_ TurnState, state StepState) bool { return state.Status == StepToolRequestsReady })
}

func (j *Journal) AssembleStepResult(scope Scope, stepID, owner string, fencingToken int64) (Event, error) {
	return j.stepTransition(scope, strings.TrimSpace(stepID), "", EventStepResultAssembled, owner, fencingToken, StatusPending, map[string]any{"step_id": strings.TrimSpace(stepID)}, func(_ TurnState, state StepState) bool {
		return state.Status == StepModelStreaming || state.Status == StepEffectsRunning
	})
}

func (j *Journal) CheckpointStep(scope Scope, stepID, owner string, fencingToken int64, checkpointDigest string) (Event, error) {
	checkpointDigest = strings.TrimSpace(checkpointDigest)
	if checkpointDigest == "" {
		return Event{}, errors.New("checkpoint_digest is required")
	}
	return j.stepTransition(scope, strings.TrimSpace(stepID), "", EventStepCheckpointed, owner, fencingToken, StatusCommitted, map[string]any{"step_id": strings.TrimSpace(stepID), "checkpoint_digest": checkpointDigest}, func(_ TurnState, state StepState) bool { return state.Status == StepResultAssembled })
}

func (j *Journal) CompleteStep(scope Scope, stepID, owner string, fencingToken int64) (Event, error) {
	return j.stepTransition(scope, strings.TrimSpace(stepID), "", EventStepCompleted, owner, fencingToken, StatusCommitted, map[string]any{"step_id": strings.TrimSpace(stepID), "stop_reason": StopCompleted}, func(_ TurnState, state StepState) bool { return state.Status == StepCheckpointed })
}

func (j *Journal) MarkStepModelOutcomeUnknown(scope Scope, stepID, owner string, fencingToken int64) (Event, error) {
	return j.stepTransition(scope, strings.TrimSpace(stepID), "", EventStepModelOutcomeUnknown, owner, fencingToken, StatusRecoveryNeeded, map[string]any{"step_id": strings.TrimSpace(stepID), "stop_reason": StopProviderOutcomeUnknown}, func(_ TurnState, state StepState) bool {
		return state.Status == StepModelCommitted || state.Status == StepModelStreaming
	})
}

func (j *Journal) MarkStepEffectOutcomeUnknown(scope Scope, stepID, owner string, fencingToken int64) (Event, error) {
	return j.stepTransition(scope, strings.TrimSpace(stepID), "", EventStepEffectUnknown, owner, fencingToken, StatusRecoveryNeeded, map[string]any{"step_id": strings.TrimSpace(stepID), "stop_reason": StopEffectOutcomeUnknown}, func(_ TurnState, state StepState) bool { return state.Status == StepEffectsRunning })
}

func (j *Journal) BeginStepCancellation(scope Scope, stepID, owner string, fencingToken int64, reason StopReason) (Event, error) {
	if reason != StopCancelledByUser && reason != StopCancelledByParent {
		return Event{}, ErrStopReasonInvalid
	}
	return j.stepTransition(scope, strings.TrimSpace(stepID), "", EventStepCancelling, owner, fencingToken, StatusPending, map[string]any{"step_id": strings.TrimSpace(stepID), "stop_reason": reason}, func(_ TurnState, state StepState) bool {
		return state.Status != "" && !state.Terminal() && state.Status != StepCancelling
	})
}

func (j *Journal) CancelStep(scope Scope, stepID, owner string, fencingToken int64, reason StopReason) (Event, error) {
	if reason != StopCancelledByUser && reason != StopCancelledByParent {
		return Event{}, ErrStopReasonInvalid
	}
	return j.stepTransition(scope, strings.TrimSpace(stepID), "", EventStepCancelled, owner, fencingToken, StatusRejected, map[string]any{"step_id": strings.TrimSpace(stepID), "stop_reason": reason}, func(_ TurnState, state StepState) bool { return state.Status == StepCancelling })
}

func (j *Journal) FailStep(scope Scope, stepID, owner string, fencingToken int64, reason StopReason) (Event, error) {
	if !reason.Valid() || reason == StopCompleted || reason == StopMaxSteps || reason == StopBudgetExhausted || reason == StopCancelledByUser || reason == StopCancelledByParent {
		return Event{}, ErrStopReasonInvalid
	}
	return j.stepTransition(scope, strings.TrimSpace(stepID), "", EventStepFailed, owner, fencingToken, StatusRejected, map[string]any{"step_id": strings.TrimSpace(stepID), "stop_reason": reason}, func(_ TurnState, state StepState) bool { return state.Status != "" && !state.Terminal() })
}

func (j *Journal) stepTransition(scope Scope, stepID, turnID, eventType, owner string, fencingToken int64, status string, payload map[string]any, allowed func(TurnState, StepState) bool) (Event, error) {
	if !scope.valid() || stepID == "" {
		return Event{}, fmt.Errorf("%w: scope and step_id are required", ErrLifecycleInputInvalid)
	}
	owner = strings.TrimSpace(owner)
	if owner == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	if err := j.validateLifecycleLeaseLocked(scope, owner, fencingToken); err != nil {
		return Event{}, err
	}
	state := j.stepStateLocked(scope, stepID)
	if turnID == "" {
		turnID = state.TurnID
	}
	if turnID == "" {
		return Event{}, ErrStepTransition
	}
	payload["turn_id"] = turnID
	input, retry := lifecycleInput(j.events, scope, "step", stepID, eventType, owner, fencingToken, status, payload, state.LastEventID)
	if retry {
		return j.appendBatchLocked([]Input{input})
	}
	turn := j.turnStateLocked(scope, turnID)
	if !allowed(turn, state) || (state.Status != "" && state.TurnID != turnID) || (!stepTransitionAllowedOutsideRunningTurn(eventType) && turn.Status != TurnRunning) {
		return Event{}, ErrStepTransition
	}
	return j.appendBatchLocked([]Input{input})
}

func lifecycleInput(events []Event, scope Scope, aggregateType, aggregateID, eventType, owner string, fencingToken int64, status string, payload any, previousEventID string) (Input, bool) {
	key := lifecycleTransitionKey(aggregateType, aggregateID, eventType, previousEventID)
	retry := false
	if last, ok := lastAggregateEvent(events, scope, aggregateType, aggregateID); ok && last.EventType == eventType {
		key, retry = last.IdempotencyKey, true
	}
	return Input{EventType: eventType, AggregateType: aggregateType, AggregateID: aggregateID, Scope: scope, IdempotencyKey: key, WriterID: owner, FencingToken: fencingToken, Status: status, Payload: payload}, retry
}

func lifecycleTransitionKey(aggregateType, aggregateID, eventType, previousEventID string) string {
	boundary := previousEventID
	if boundary == "" {
		boundary = "initial"
	}
	return aggregateType + ":" + aggregateID + ":" + strings.TrimPrefix(eventType, aggregateType+".") + ":after:" + boundary
}

func lastAggregateEvent(events []Event, scope Scope, aggregateType, aggregateID string) (Event, bool) {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Scope == scope && events[index].AggregateType == aggregateType && events[index].AggregateID == aggregateID {
			return events[index], true
		}
	}
	return Event{}, false
}

func stepTransitionAllowedOutsideRunningTurn(eventType string) bool {
	return eventType == EventStepCancelling || eventType == EventStepCancelled || eventType == EventStepFailed
}

func (j *Journal) validateLifecycleLeaseLocked(scope Scope, owner string, fencingToken int64) error {
	key := scope.TenantID + "\x00" + scope.WorkspaceID + "\x00" + scope.RunID
	lease, ok := j.leases[key]
	if !ok || lease.Owner != owner || lease.FencingToken != fencingToken || !lease.ExpiresAt.After(j.now()) {
		return ErrLeaseLost
	}
	return nil
}

func (j *Journal) hasNonterminalTurnsLocked(scope Scope) bool {
	turns := make(map[string]struct{})
	for _, event := range j.events {
		if event.Scope == scope && event.AggregateType == "turn" && event.EventType == EventTurnCreated {
			turns[event.AggregateID] = struct{}{}
		}
	}
	for turnID := range turns {
		if !j.turnStateLocked(scope, turnID).Terminal() {
			return true
		}
	}
	return false
}

func (j *Journal) hasNonterminalStepsLocked(scope Scope, turnID string) bool {
	steps := make(map[string]struct{})
	for _, event := range j.events {
		if event.Scope != scope || event.AggregateType != "step" || event.EventType != EventStepCreated {
			continue
		}
		if payloadString(event.Payload, "turn_id") == turnID {
			steps[event.AggregateID] = struct{}{}
		}
	}
	for stepID := range steps {
		if !j.stepStateLocked(scope, stepID).Terminal() {
			return true
		}
	}
	return false
}

func normalizedCode(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func validateStringMap(values map[string]string) error {
	for key, value := range values {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" || key != strings.TrimSpace(key) || value != strings.TrimSpace(value) {
			return errors.New("keys and values must be non-empty normalized strings")
		}
	}
	return nil
}

func validateBoolMap(values map[string]bool) error {
	for key := range values {
		if strings.TrimSpace(key) == "" || key != strings.TrimSpace(key) {
			return errors.New("keys must be non-empty normalized strings")
		}
	}
	return nil
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func cloneBoolMap(values map[string]bool) map[string]bool {
	if values == nil {
		return nil
	}
	copy := make(map[string]bool, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}
