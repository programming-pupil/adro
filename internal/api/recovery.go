package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/compat"
	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/harness"
	"github.com/adro-project/adro/internal/provider"
	"github.com/adro-project/adro/internal/store"
)

const providerDispatchIntentType = "provider.dispatch.v1"
const providerDispatchOwner = "adro-api-dispatch"

// providerDispatchIntent is the durable handoff between the control plane and
// an execution provider. The command is persisted before the provider is
// called, so a lost response can be replayed with the same idempotency key.
type providerDispatchIntent struct {
	Type               string                        `json:"type"`
	Kind               string                        `json:"kind,omitempty"`
	PipelineID         string                        `json:"pipeline_id"`
	ExpectedVersion    int64                         `json:"expected_version"`
	Stage              domain.PipelineStage          `json:"stage"`
	AgentID            string                        `json:"agent_id"`
	TurnHash           string                        `json:"turn_hash"`
	PipelineWorkItemID string                        `json:"pipeline_work_item_id,omitempty"`
	ProviderIssueID    string                        `json:"provider_issue_id,omitempty"`
	RepositoryID       string                        `json:"repository_id,omitempty"`
	WorkItemID         string                        `json:"work_item_id,omitempty"`
	RequirementID      string                        `json:"requirement_id,omitempty"`
	BugID              string                        `json:"bug_id,omitempty"`
	CommentID          string                        `json:"comment_id,omitempty"`
	WorkspaceID        string                        `json:"workspace_id,omitempty"`
	DispatchTargetType string                        `json:"dispatch_target_type,omitempty"`
	DispatchTargetID   string                        `json:"dispatch_target_id,omitempty"`
	DedupeKey          string                        `json:"dedupe_key,omitempty"`
	HarnessSessionID   string                        `json:"harness_session_id,omitempty"`
	ContextEnvelope    harness.ContextEnvelope       `json:"context_envelope,omitempty"`
	ContextID          string                        `json:"context_id,omitempty"`
	ContextVersion     int64                         `json:"context_version,omitempty"`
	RepairAttempt      int                           `json:"repair_attempt,omitempty"`
	Command            provider.StartRunCommand      `json:"command,omitempty"`
	Continuation       *provider.ContinuationCommand `json:"continuation,omitempty"`
}

// enqueueProviderDispatch records the side effect before it is attempted.
// The idempotency key is also the turn key, tying the intent to the immutable
// transcript entry that caused it.
func (s *Server) enqueueProviderDispatch(run domain.PipelineRun, key string, intent providerDispatchIntent) (harness.OutboxEvent, error) {
	if s.Harness == nil {
		return harness.OutboxEvent{}, errors.New("session harness is not configured")
	}
	intent.Type = providerDispatchIntentType
	return s.Harness.EnqueueOutbox(run.SessionID, key, intent)
}

func (s *Server) enqueueAndClaimProviderDispatch(run domain.PipelineRun, key string, intent providerDispatchIntent) (harness.OutboxEvent, bool, error) {
	if s.Harness == nil {
		return harness.OutboxEvent{}, false, errors.New("session harness is not configured")
	}
	intent.Type = providerDispatchIntentType
	return s.Harness.EnqueueAndClaimOutbox(run.SessionID, key, intent, providerDispatchOwner, dispatchLeaseTTL(), time.Now().UTC())
}

func (s *Server) enqueueExecutionDispatch(sessionID, key string, intent providerDispatchIntent) (harness.OutboxEvent, error) {
	if s.Harness == nil {
		return harness.OutboxEvent{}, errors.New("session harness is not configured")
	}
	intent.Type = providerDispatchIntentType
	intent.Kind = "work_item"
	intent.HarnessSessionID = sessionID
	return s.Harness.EnqueueOutbox(sessionID, key, intent)
}

func (s *Server) enqueueAndClaimExecutionDispatch(sessionID, key string, intent providerDispatchIntent) (harness.OutboxEvent, bool, error) {
	if s.Harness == nil {
		return harness.OutboxEvent{}, false, errors.New("session harness is not configured")
	}
	intent.Type = providerDispatchIntentType
	intent.Kind = "work_item"
	intent.HarnessSessionID = sessionID
	return s.Harness.EnqueueAndClaimOutbox(sessionID, key, intent, providerDispatchOwner, dispatchLeaseTTL(), time.Now().UTC())
}

func (s *Server) claimProviderDispatch(sessionID string, event harness.OutboxEvent) error {
	if event.State == "published" {
		return nil
	}
	if event.State == "processing" {
		return harness.ErrLeaseBusy
	}
	claimed, err := s.Harness.ClaimOutbox(sessionID, providerDispatchOwner, 1, dispatchLeaseTTL(), time.Now().UTC())
	if err != nil {
		return err
	}
	if len(claimed) != 1 || claimed[0].ID != event.ID {
		return errors.New("provider dispatch intent was claimed by another worker")
	}
	return nil
}

type harnessPublisher struct{ server *Server }

func (p harnessPublisher) Publish(ctx context.Context, event harness.OutboxEvent) error {
	if p.server == nil {
		return errors.New("harness publisher has no server")
	}
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(event.Payload, &envelope); err != nil {
		return fmt.Errorf("decode harness outbox payload: %w", err)
	}
	if envelope.Type == providerDispatchIntentType {
		return p.server.processProviderDispatchIntent(ctx, event)
	}
	// Generic outbox records are still observable through the ADRO event bus;
	// an adapter can replace this publisher with NATS without changing intent
	// durability or acknowledgement ordering.
	if p.server.Events == nil {
		return errors.New("generic harness outbox requires an event bus")
	}
	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("decode generic harness outbox payload: %w", err)
	}
	eventType := "harness.outbox.published.v1"
	if value, ok := payload["event_type"].(string); ok && strings.TrimSpace(value) != "" {
		eventType = value
	}
	return p.server.Events.Publish(ctx, events.NewWithContext(ctx, eventType, "harness_outbox", event.ID, "", event.SessionID, 1, map[string]any{
		"outbox_id": event.ID, "session_id": event.SessionID, "payload": payload,
	}))
}

// processProviderDispatchIntent replays a pending dispatch after a restart or
// a lost control-plane response. It is intentionally idempotent at both
// provider and pipeline boundaries.
func (s *Server) processProviderDispatchIntent(ctx context.Context, event harness.OutboxEvent) error {
	var intent providerDispatchIntent
	if err := json.Unmarshal(event.Payload, &intent); err != nil {
		return fmt.Errorf("decode provider dispatch intent: %w", err)
	}
	if intent.Type != providerDispatchIntentType {
		return errors.New("invalid provider dispatch intent type")
	}
	if intent.Kind == "work_item" {
		return s.processWorkItemDispatchIntent(ctx, event, intent)
	}
	if intent.Kind == "bug" {
		return s.processBugDispatchIntent(ctx, event, intent)
	}
	if intent.Kind == "comment" {
		return s.processCommentDispatchIntent(ctx, event, intent)
	}
	if strings.TrimSpace(intent.PipelineID) == "" || intent.ExpectedVersion <= 0 || !intent.Stage.Valid() || strings.TrimSpace(intent.AgentID) == "" || strings.TrimSpace(intent.TurnHash) == "" {
		return errors.New("invalid provider dispatch intent")
	}
	run, err := s.Store.GetPipeline(intent.PipelineID)
	if err != nil {
		return err
	}
	if intent.ContextEnvelope.Manifest.Version == 0 && intent.Command.ContextEnvelope.Manifest.Version == 0 && intent.Continuation == nil {
		// Older durable intents did not carry the typed envelope. Recompile from
		// the same durable harness session before replaying instead of allowing a
		// provider to run with a silently incomplete context.
		envelope, envelopeErr := s.compiledHarnessEnvelope(run.SessionID)
		if envelopeErr != nil {
			return fmt.Errorf("hydrate provider dispatch context: %w", envelopeErr)
		}
		intent.ContextEnvelope = envelope
		intent.Command.ContextEnvelope = envelope
		intent.Command.ContextVersion = envelope.Manifest.Version
	}
	// A prior attempt may have committed the pipeline update but crashed before
	// acknowledging the outbox. In that case the intent is already complete.
	if run.Version > intent.ExpectedVersion && run.Status == domain.PipelineWaiting && run.ActiveProviderTaskID != "" {
		return nil
	}
	// Do not revive a pipeline that was deliberately suspended or completed.
	// The durable record is acknowledged as a stale intent; an operator can
	// create a new attempt explicitly.
	if run.Status != domain.PipelineRunning || run.Version != intent.ExpectedVersion || run.PipelineStage != intent.Stage {
		if run.Status == domain.PipelineSuspended || run.Status == domain.PipelineCompleted || run.Status == domain.PipelineFailed {
			return nil
		}
		return fmt.Errorf("provider dispatch intent conflicts with pipeline version %d (expected %d): %w", run.Version, intent.ExpectedVersion, store.ErrConflict)
	}
	graphScope := compat.DispatchScope{PlanID: intent.Command.PlanID, NodeID: intent.Command.NodeID, AttemptID: intent.Command.AttemptID}
	legacyVersion := intent.Command.LegacyAdapterVersion
	if intent.Continuation != nil {
		graphScope = compat.DispatchScope{PlanID: intent.Continuation.PlanID, NodeID: intent.Continuation.NodeID, AttemptID: intent.Continuation.AttemptID}
		legacyVersion = intent.Continuation.LegacyAdapterVersion
	}
	if strings.TrimSpace(legacyVersion) != "" {
		if graphScope.PlanID == "" || graphScope.NodeID == "" || graphScope.AttemptID == "" {
			return errors.New("recovered legacy dispatch has incomplete graph scope")
		}
		run.ExecutionPlanID, run.ActiveGraphNodeID, run.ActiveGraphAttemptID = graphScope.PlanID, graphScope.NodeID, graphScope.AttemptID
		run.LegacyAdapterVersion = compat.LegacyAdapterVersion
		if err := s.startLegacyGraphAttempt(run, graphScope, intent.ContextEnvelope, event.CreatedAt); err != nil {
			return fmt.Errorf("recover legacy graph attempt: %w", err)
		}
	}
	contextVersion := dispatchContextVersion(intent)
	if contextVersion < 1 {
		return errors.New("provider dispatch intent has no context version")
	}

	var binding provider.RunBinding
	reusedBinding := false
	if intent.PipelineWorkItemID != "" {
		if provenance, found := s.Store.FindProvenance(intent.PipelineWorkItemID); found && provenance.ProviderIdempotencyKey == event.IdempotencyKey && provenance.ProviderTaskID != "" {
			binding = provider.RunBinding{ProviderRunID: provenance.ProviderTaskID, SessionID: provenance.ProviderSessionID, WorkDir: provenance.ProviderWorkDir, ContextVersion: provenance.ContextVersion, SessionReused: intent.Continuation != nil}
			reusedBinding = true
		}
	}
	if intent.Continuation != nil {
		if !reusedBinding {
			continuity, ok := s.Provider.(provider.ContinuityProvider)
			if !ok {
				return errors.New("provider cannot continue the original development session")
			}
			command := intent.Continuation.WithTraceContext(ctx)
			binding, err = continuity.ContinueWorkItem(ctx, command)
			if err == nil && (!binding.SessionReused || binding.SessionID != intent.Continuation.ExpectedSessionID || filepathClean(binding.WorkDir) != filepathClean(intent.Continuation.ExpectedWorkDir)) {
				err = errors.New("provider did not confirm the original development task session and workdir")
			}
		} else if binding.SessionID != intent.Continuation.ExpectedSessionID || filepathClean(binding.WorkDir) != filepathClean(intent.Continuation.ExpectedWorkDir) {
			err = errors.New("stored provider provenance does not match the original development session")
		}
	} else if !reusedBinding {
		binding, err = s.Provider.StartRun(ctx, intent.Command.WithTraceContext(ctx))
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(binding.ProviderRunID) == "" {
		return errors.New("provider returned an empty run binding")
	}
	if strings.TrimSpace(legacyVersion) != "" {
		if err := s.bindLegacyGraphAttempt(run, graphScope.AttemptID, binding.ProviderRunID, binding.SessionID, binding.WorkDir); err != nil {
			return fmt.Errorf("recover legacy graph binding: %w", err)
		}
	}

	if intent.Stage == domain.PipelineDevelopment && intent.PipelineWorkItemID != "" && !reusedBinding {
		providerName := "local"
		if caps, capsErr := s.Provider.Capabilities(ctx); capsErr == nil && caps.Provider != "" {
			providerName = caps.Provider
		}
		if err := s.Store.SaveProvenance(domain.Provenance{WorkItemID: intent.PipelineWorkItemID, RequirementID: run.RequirementID, AgentBindingID: intent.AgentID, Provider: providerName, ProviderTaskID: binding.ProviderRunID, ProviderSessionID: binding.SessionID, ProviderWorkDir: binding.WorkDir, ProviderIdempotencyKey: event.IdempotencyKey, RepositoryID: intent.RepositoryID, ContextVersion: contextVersion}); err != nil {
			return err
		}
	}
	if err := s.saveHarnessCheckpoint(run.SessionID, harness.CheckpointEffectAfter, intent.TurnHash, contextVersion, []string{event.ID}, nil, "provider dispatch recorded"); err != nil {
		return err
	}
	run.PipelineWorkItemID = intent.PipelineWorkItemID
	run.ActiveProviderIssueID = intent.ProviderIssueID
	run.ActiveProviderTaskID = binding.ProviderRunID
	run.ActiveAgentID = intent.AgentID
	run.Status = domain.PipelineWaiting
	run.Version++
	run.UpdatedAt = time.Now().UTC()
	updated, err := s.Store.UpdatePipeline(run, intent.ExpectedVersion)
	if err != nil {
		// A concurrent request may have committed the exact same binding. Read
		// the winner before deciding whether the intent must be retried.
		if latest, getErr := s.Store.GetPipeline(intent.PipelineID); getErr == nil && latest.Status == domain.PipelineWaiting && latest.ActiveProviderTaskID == binding.ProviderRunID {
			return nil
		}
		return err
	}
	s.watchLocalPipelineRun(updated)
	return nil
}

// dispatchContextVersion is the compatibility reader for durable intents
// created before ContextVersion became an explicit field. New callers always
// populate it, but replay must derive the value from the exact envelope rather
// than from the mutable pipeline revision.
func dispatchContextVersion(intent providerDispatchIntent) int64 {
	if intent.ContextVersion > 0 {
		return intent.ContextVersion
	}
	if intent.Command.ContextVersion > 0 {
		return intent.Command.ContextVersion
	}
	if intent.Command.ContextEnvelope.Manifest.Version > 0 {
		return intent.Command.ContextEnvelope.Manifest.Version
	}
	if intent.ContextEnvelope.Manifest.Version > 0 {
		return intent.ContextEnvelope.Manifest.Version
	}
	if intent.Continuation != nil && intent.Continuation.ContextEnvelope.Manifest.Version > 0 {
		return intent.Continuation.ContextEnvelope.Manifest.Version
	}
	return 0
}

func (s *Server) processWorkItemDispatchIntent(ctx context.Context, event harness.OutboxEvent, intent providerDispatchIntent) error {
	if strings.TrimSpace(intent.WorkItemID) == "" || strings.TrimSpace(intent.RequirementID) == "" || strings.TrimSpace(intent.HarnessSessionID) == "" || strings.TrimSpace(intent.TurnHash) == "" {
		return errors.New("invalid work item dispatch intent")
	}
	item, err := s.Store.GetWorkItem(intent.WorkItemID)
	if err != nil {
		return err
	}
	if provenance, found := s.Store.FindProvenance(item.ID); found && provenance.ProviderIdempotencyKey == event.IdempotencyKey && provenance.ProviderTaskID != "" {
		return s.saveHarnessCheckpoint(intent.HarnessSessionID, harness.CheckpointEffectAfter, intent.TurnHash, intent.Command.ContextVersion, []string{event.ID}, nil, "provider run recorded")
	}
	binding, err := s.Provider.StartRun(ctx, intent.Command.WithTraceContext(ctx))
	if err != nil {
		return err
	}
	if strings.TrimSpace(binding.ProviderRunID) == "" {
		return errors.New("provider returned an empty run binding")
	}
	providerName := "local"
	if caps, capsErr := s.Provider.Capabilities(ctx); capsErr == nil && caps.Provider != "" {
		providerName = caps.Provider
	}
	provenance := domain.Provenance{WorkItemID: item.ID, RequirementID: intent.RequirementID, BugID: item.BugID, AgentBindingID: intent.AgentID, Provider: providerName, ProviderTaskID: binding.ProviderRunID, ProviderSessionID: binding.SessionID, ProviderWorkDir: binding.WorkDir, ProviderIdempotencyKey: event.IdempotencyKey, RepositoryID: item.RepositoryID, ContextVersion: intent.Command.ContextVersion}
	if providerBinding, bindingErr := s.Store.GetProviderBinding(intent.AgentID); bindingErr == nil {
		provenance.ProviderAgentID = providerBinding.ProviderObjectID
	}
	if err := s.Store.SaveProvenance(provenance); err != nil {
		return err
	}
	if err := s.saveHarnessCheckpoint(intent.HarnessSessionID, harness.CheckpointEffectAfter, intent.TurnHash, intent.Command.ContextVersion, []string{event.ID}, nil, "provider run recorded"); err != nil {
		return err
	}
	return nil
}

func (s *Server) processCommentDispatchIntent(ctx context.Context, event harness.OutboxEvent, intent providerDispatchIntent) error {
	if strings.TrimSpace(intent.CommentID) == "" || strings.TrimSpace(intent.HarnessSessionID) == "" || strings.TrimSpace(intent.TurnHash) == "" || strings.TrimSpace(intent.AgentID) == "" {
		return errors.New("invalid comment dispatch intent")
	}
	workItemID := strings.TrimSpace(intent.WorkItemID)
	if workItemID == "" {
		workItemID = "comment-" + intent.CommentID
	}
	if provenance, found := s.Store.FindProvenance(workItemID); found && provenance.ProviderIdempotencyKey == event.IdempotencyKey && provenance.ProviderTaskID != "" {
		s.markCommentFollowUpStarted(intent, provenance.ProviderTaskID, provenance.ProviderSessionID, provenance.ProviderWorkDir)
		return s.saveHarnessCheckpoint(intent.HarnessSessionID, harness.CheckpointEffectAfter, intent.TurnHash, intent.ContextVersion, []string{event.ID}, nil, "comment follow-up dispatched")
	}
	var binding provider.RunBinding
	var err error
	if intent.Continuation != nil {
		continuity, ok := s.Provider.(provider.ContinuityProvider)
		if !ok {
			return errors.New("provider cannot continue the comment thread session")
		}
		command := intent.Continuation.WithTraceContext(ctx)
		binding, err = continuity.ContinueWorkItem(ctx, command)
		if err == nil && (!binding.SessionReused || binding.SessionID != intent.Continuation.ExpectedSessionID || filepathClean(binding.WorkDir) != filepathClean(intent.Continuation.ExpectedWorkDir)) {
			err = errors.New("provider did not confirm the comment thread session and workdir")
		}
	} else {
		binding, err = s.Provider.StartRun(ctx, intent.Command.WithTraceContext(ctx))
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(binding.ProviderRunID) == "" {
		return errors.New("provider returned an empty run binding")
	}
	providerName := "local"
	if caps, capsErr := s.Provider.Capabilities(ctx); capsErr == nil && caps.Provider != "" {
		providerName = caps.Provider
	}
	repositoryID := ""
	if intent.WorkItemID != "" {
		if item, itemErr := s.Store.GetWorkItem(intent.WorkItemID); itemErr == nil {
			repositoryID = item.RepositoryID
		}
	}
	if err := s.Store.SaveProvenance(domain.Provenance{WorkItemID: workItemID, RequirementID: intent.RequirementID, BugID: intent.BugID, AgentBindingID: intent.AgentID, Provider: providerName, ProviderTaskID: binding.ProviderRunID, ProviderSessionID: binding.SessionID, ProviderWorkDir: binding.WorkDir, ProviderIdempotencyKey: event.IdempotencyKey, RepositoryID: repositoryID, ContextVersion: intent.ContextVersion}); err != nil {
		return err
	}
	if err := s.saveHarnessCheckpoint(intent.HarnessSessionID, harness.CheckpointEffectAfter, intent.TurnHash, intent.ContextVersion, []string{event.ID}, nil, "comment follow-up dispatched"); err != nil {
		return err
	}
	s.markCommentFollowUpStarted(intent, binding.ProviderRunID, binding.SessionID, binding.WorkDir)
	if s.Events != nil {
		_ = s.Events.Publish(ctx, events.NewWithContext(ctx, "comment.follow_up.started.v1", "comment", intent.CommentID, "", intent.WorkspaceID, intent.ContextVersion, map[string]any{"comment_id": intent.CommentID, "run_id": binding.ProviderRunID, "session_id": binding.SessionID, "session_reused": binding.SessionReused}))
	}
	return nil
}

func (s *Server) markCommentFollowUpStarted(intent providerDispatchIntent, providerRunID, providerSessionID, providerWorkDir string) {
	if s == nil || s.Store == nil || strings.TrimSpace(intent.CommentID) == "" {
		return
	}
	dispatchType, dispatchID := strings.TrimSpace(intent.DispatchTargetType), strings.TrimSpace(intent.DispatchTargetID)
	if dispatchType == "" {
		dispatchType = "legacy"
	}
	if dispatchID == "" {
		dispatchID = intent.CommentID
	}
	receipt, err := s.Store.GetCommentFollowUpForTarget(intent.CommentID, dispatchType, dispatchID)
	if err != nil {
		targetType, targetID := "requirement", intent.RequirementID
		if intent.BugID != "" {
			targetType, targetID = "bug", intent.BugID
		}
		receipt = domain.CommentFollowUp{CommentID: intent.CommentID, WorkspaceID: intent.WorkspaceID, TargetType: targetType, TargetID: targetID, DispatchTargetType: dispatchType, DispatchTargetID: dispatchID, DedupeKey: intent.DedupeKey, AgentBindingID: intent.AgentID, HarnessSessionID: intent.HarnessSessionID, ContextVersion: intent.ContextVersion, TurnHash: intent.TurnHash, Status: "started", Mode: "continuation"}
	}
	if receipt.Mode == "" {
		receipt.Mode = "continuation"
	}
	receipt.Status = "started"
	receipt.ProviderRunID = providerRunID
	receipt.ProviderSessionID = providerSessionID
	receipt.ProviderWorkDir = providerWorkDir
	receipt.DispatchTargetType = dispatchType
	receipt.DispatchTargetID = dispatchID
	if receipt.DedupeKey == "" {
		receipt.DedupeKey = intent.DedupeKey
	}
	receipt.Attempts++
	_, _ = s.Store.SaveCommentFollowUp(receipt)
}

func (s *Server) processBugDispatchIntent(ctx context.Context, event harness.OutboxEvent, intent providerDispatchIntent) error {
	if strings.TrimSpace(intent.BugID) == "" || strings.TrimSpace(intent.HarnessSessionID) == "" || strings.TrimSpace(intent.TurnHash) == "" || intent.RepairAttempt <= 0 {
		return errors.New("invalid bug dispatch intent")
	}
	bug, err := s.Store.GetBug(intent.BugID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(intent.Command.WorkItemID) == "" {
		// Bugs created without a linked WorkItem still need a stable provider
		// execution identity so recovery can replay the exact same intent.
		intent.Command.WorkItemID = "bug-" + bug.ID
	}
	providerWorkItemID := intent.WorkItemID
	if providerWorkItemID == "" {
		providerWorkItemID = "bug-" + bug.ID
	}
	if provenance, found := s.Store.FindProvenance(providerWorkItemID); found && provenance.ProviderIdempotencyKey == event.IdempotencyKey && provenance.ProviderTaskID != "" {
		return s.saveHarnessCheckpoint(intent.HarnessSessionID, harness.CheckpointEffectAfter, intent.TurnHash, intent.ContextVersion, []string{event.ID}, nil, "bug repair provider run recorded")
	}
	intent.Command.WorkItemID = providerWorkItemID
	var binding provider.RunBinding
	if intent.Continuation != nil {
		continuity, supported := s.Provider.(provider.ContinuityProvider)
		if !supported {
			return errors.New("provider cannot continue the original bug repair session")
		}
		command := intent.Continuation.WithTraceContext(ctx)
		binding, err = continuity.ContinueWorkItem(ctx, command)
		if err == nil && (!binding.SessionReused || binding.SessionID != intent.Continuation.ExpectedSessionID || filepathClean(binding.WorkDir) != filepathClean(intent.Continuation.ExpectedWorkDir)) {
			return errors.New("provider did not confirm the original bug repair session and workdir")
		}
	} else {
		binding, err = s.Provider.StartRun(ctx, intent.Command.WithTraceContext(ctx))
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(binding.ProviderRunID) == "" {
		return errors.New("provider returned an empty run binding")
	}
	providerName := "local"
	if caps, capsErr := s.Provider.Capabilities(ctx); capsErr == nil && caps.Provider != "" {
		providerName = caps.Provider
	}
	if providerWorkItemID != "" {
		attempt := domain.RepairAttempt{BugID: bug.ID, WorkItemID: providerWorkItemID, Attempt: intent.RepairAttempt, ContextID: intent.ContextID, ContextVersion: intent.ContextVersion, ProviderIssueID: intent.ProviderIssueID, ProviderSessionID: binding.SessionID, ProviderWorkDir: binding.WorkDir, ProviderTaskID: binding.ProviderRunID, Status: "started", Brief: domain.RepairBrief{BugID: bug.ID, Fingerprint: bug.Fingerprint, StableSummary: bug.Title, FailedEvidence: []string{bug.LogExcerpt}, Attempt: intent.RepairAttempt}}
		if _, err := s.Store.SaveRepairAttempt(attempt); err != nil {
			return err
		}
		if err := s.Store.SaveProvenance(domain.Provenance{WorkItemID: providerWorkItemID, RequirementID: bug.RequirementID, BugID: bug.ID, AgentBindingID: intent.AgentID, Provider: providerName, ProviderTaskID: binding.ProviderRunID, ProviderSessionID: binding.SessionID, ProviderWorkDir: binding.WorkDir, ProviderIdempotencyKey: event.IdempotencyKey, RepositoryID: bug.RepositoryID, ContextVersion: intent.ContextVersion}); err != nil {
			return err
		}
	}
	return s.saveHarnessCheckpoint(intent.HarnessSessionID, harness.CheckpointEffectAfter, intent.TurnHash, intent.ContextVersion, []string{event.ID}, nil, "bug repair provider run recorded")
}

func filepathClean(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return filepath.Clean(value)
}

// StartRecoveryWorker starts the local supervised recovery loop. It is safe to
// call with an ephemeral harness too; production adapters can keep the same
// lifecycle and replace the store/queue implementation.
func (s *Server) StartRecoveryWorker(ctx context.Context) {
	if s == nil || s.Harness == nil || s.Store == nil || s.Provider == nil {
		return
	}
	s.recoveryMu.Lock()
	if s.recoveryStarted {
		s.recoveryMu.Unlock()
		return
	}
	s.recoveryStarted = true
	s.recoveryMu.Unlock()
	interval := recoveryInterval()
	owner := strings.TrimSpace(os.Getenv("ADRO_HARNESS_WORKER_ID"))
	if owner == "" {
		owner = "adro-api-worker-" + strconv.Itoa(os.Getpid())
	}
	go func() {
		dispatcher := harness.Dispatcher{Store: s.Harness, Publisher: harnessPublisher{server: s}, Owner: owner, LeaseTTL: dispatchLeaseTTL()}
		s.recoverOnce(ctx, dispatcher)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.recoverOnce(ctx, dispatcher)
			}
		}
	}()
}

func (s *Server) recoverOnce(ctx context.Context, dispatcher harness.Dispatcher) {
	if s.Runners != nil {
		if _, err := s.Runners.ReapStale(time.Now().UTC(), runnerHeartbeatMaxAge()); err != nil && s.Logger != nil {
			s.Logger.Error("runner heartbeat recovery failed", "error", err)
		}
	}
	for _, session := range s.Harness.ListSessions() {
		if _, err := s.Harness.Recover(session.ID, time.Now().UTC()); err != nil {
			if s.Logger != nil {
				s.Logger.Error("harness recovery failed", "session_id", session.ID, "error", err)
			}
			continue
		}
		if _, err := dispatcher.DispatchOnce(ctx, session.ID, 25); err != nil && s.Logger != nil {
			s.Logger.Error("harness outbox dispatch failed", "session_id", session.ID, "error", err)
		}
	}
	// Local providers cannot keep a child process alive across an API restart.
	// Reconcile their durable terminal snapshots immediately so a pipeline never
	// remains waiting until the watchdog's long deadline.
	for _, run := range s.Store.ListPipelines("", "") {
		if run.Status != domain.PipelineWaiting || run.ActiveProviderTaskID == "" {
			continue
		}
		snapshot, err := s.Provider.GetRun(ctx, run.ActiveProviderTaskID)
		if err == nil && snapshot.Status != "running" {
			_ = s.recordProviderToolEvents(run, snapshot)
			if result, ok := pipelineResultFromSnapshot(run, snapshot); ok {
				_, _, _ = s.advancePipeline(run, result)
				continue
			}
		}
		s.watchLocalPipelineRun(run)
	}
}

func runnerHeartbeatMaxAge() time.Duration {
	value := strings.TrimSpace(os.Getenv("ADRO_RUNNER_HEARTBEAT_MAX_AGE"))
	if value != "" {
		if age, err := time.ParseDuration(value); err == nil && age > 0 {
			return age
		}
	}
	return 30 * time.Second
}

func recoveryInterval() time.Duration {
	value := strings.TrimSpace(os.Getenv("ADRO_HARNESS_RECOVERY_INTERVAL"))
	if value == "" {
		return time.Second
	}
	interval, err := time.ParseDuration(value)
	if err != nil || interval <= 0 {
		return time.Second
	}
	return interval
}

func dispatchLeaseTTL() time.Duration {
	if value := strings.TrimSpace(os.Getenv("ADRO_HARNESS_DISPATCH_LEASE_TTL")); value != "" {
		if ttl, err := time.ParseDuration(value); err == nil && ttl > 0 {
			return ttl
		}
	}
	// A processing claim must outlive the child executor. Otherwise a healthy
	// long-running agent would be requeued by the recovery worker and started a
	// second time before the first process can acknowledge its intent.
	const fallback = time.Hour
	value := strings.TrimSpace(os.Getenv("ADRO_EXECUTOR_TIMEOUT"))
	if timeout, err := time.ParseDuration(value); err == nil && timeout > 0 {
		return timeout + 5*time.Minute
	}
	return fallback
}
