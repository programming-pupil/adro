package api

// This file is the deliberately small compatibility adapter for historical
// seven-stage pipeline clients.  The old PipelineRun remains a response/view
// contract, but every dispatch/result is also recorded against an immutable
// graph plan, node and attempt.  New orchestration code never consumes the
// numeric stage; it consumes these typed identities and the graph projection.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/compat"
	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/harness"
	"github.com/adro-project/adro/internal/orchestration"
)

func (s *Server) startLegacyGraphAttempt(run domain.PipelineRun, scope compat.DispatchScope, envelope harness.ContextEnvelope, now time.Time) error {
	if s == nil || s.Orchestration == nil {
		return nil
	}
	s.legacyGraphMu.Lock()
	defer s.legacyGraphMu.Unlock()
	plan, err := s.Orchestration.GetPlan(run.WorkspaceID, scope.PlanID)
	if err != nil {
		return fmt.Errorf("load legacy graph plan: %w", err)
	}
	projection, err := s.Orchestration.GetProjection(plan.ID)
	if errors.Is(err, orchestration.ErrNotFound) {
		projection, err = orchestration.NewProjection(plan)
		if err == nil {
			err = s.Orchestration.SaveProjection(projection)
		}
	}
	if err != nil {
		return fmt.Errorf("load legacy graph projection: %w", err)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	previous := projection.Attempts[scope.AttemptID]
	if previous.ID == scope.AttemptID {
		return nil
	}
	step := run.StepFor(run.PipelineStage)
	attemptNo := projection.Nodes[scope.NodeID].AttemptNo + 1
	lease := orchestration.Lease{Key: plan.ID + ":" + scope.NodeID, Owner: "legacy-adapter", FencingToken: now.UnixNano(), ExpiresAt: now.Add(30 * time.Minute)}
	payloadHash := legacyGraphPayloadHash(scope, envelope)
	attempt, err := projection.StartAttempt(plan, scope.NodeID, scope.AttemptID, attemptNo, lease, envelope, orchestration.TransitionInput{PlanRevision: plan.Revision, AttemptID: scope.AttemptID, LeaseToken: lease.FencingToken, IdempotencyKey: "legacy:" + scope.AttemptID, PayloadHash: payloadHash, Now: now})
	if err != nil {
		return fmt.Errorf("start legacy graph attempt for %s: %w", step.Stage, err)
	}
	return s.commitLegacyGraphEvent(plan, projection, attempt, "attempt.started", "legacy:"+scope.AttemptID, map[string]any{
		"legacy_adapter_version": compat.LegacyAdapterVersion,
		"stage":                  int(run.PipelineStage),
		"stage_name":             run.PipelineStage.String(),
		"attempt_id":             attempt.ID,
		"attempt_no":             attempt.AttemptNo,
		"node_id":                attempt.NodeID,
		"lease":                  lease,
		"context":                envelope,
		"dispatch_payload_hash":  payloadHash,
		"started_at":             now,
	}, lease.FencingToken)
}

func (s *Server) bindLegacyGraphAttempt(run domain.PipelineRun, attemptID string, bindingID, sessionID, workDir string) error {
	if s == nil || s.Orchestration == nil || strings.TrimSpace(attemptID) == "" {
		return nil
	}
	s.legacyGraphMu.Lock()
	defer s.legacyGraphMu.Unlock()
	planID := run.ExecutionPlanID
	if planID == "" {
		planID = "legacy-plan-" + run.ID
	}
	plan, err := s.Orchestration.GetPlan(run.WorkspaceID, planID)
	if err != nil {
		return err
	}
	projection, err := s.Orchestration.GetProjection(plan.ID)
	if err != nil {
		return err
	}
	attempt, ok := projection.Attempts[attemptID]
	if !ok {
		return orchestration.ErrStaleAttempt
	}
	if strings.TrimSpace(bindingID) == "" || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(workDir) == "" {
		return errors.New("legacy graph provider binding requires run_id, session_id and workdir")
	}
	if attempt.RunID != "" && attempt.RunID != bindingID {
		return fmt.Errorf("legacy graph attempt %s binding is immutable", attemptID)
	}
	attempt.RunID, attempt.SessionID, attempt.WorkDir = bindingID, sessionID, workDir
	projection.Attempts[attemptID] = attempt
	return s.commitLegacyGraphEvent(plan, projection, attempt, "attempt.bound", "legacy:"+attemptID+":bound", map[string]any{"attempt_id": attemptID, "run_id": bindingID, "session_id": sessionID, "workdir": workDir}, attempt.Lease.FencingToken)
}

func (s *Server) finishLegacyGraphAttempt(run, next domain.PipelineRun, result domain.PipelineStepResult) error {
	if s == nil || s.Orchestration == nil || strings.TrimSpace(run.ExecutionPlanID) == "" || strings.TrimSpace(run.ActiveGraphAttemptID) == "" {
		return nil
	}
	s.legacyGraphMu.Lock()
	defer s.legacyGraphMu.Unlock()
	plan, err := s.Orchestration.GetPlan(run.WorkspaceID, run.ExecutionPlanID)
	if err != nil {
		return err
	}
	projection, err := s.Orchestration.GetProjection(plan.ID)
	if err != nil {
		return err
	}
	attempt, ok := projection.Attempts[run.ActiveGraphAttemptID]
	if !ok {
		return orchestration.ErrStaleAttempt
	}
	event := "failure"
	outcome := strings.ToLower(strings.TrimSpace(result.Outcome))
	if next.Status == domain.PipelineWaitingApproval {
		event = "approval"
	}
	switch outcome {
	case "pass", "passed", "success", "succeeded", "completed":
		if event != "approval" {
			event = "success"
		}
	case "bug":
		event = "bug"
	case "timeout", "timed_out":
		event = "timeout"
	case "cancel", "cancelled", "canceled":
		event = "cancel"
	}
	evidence := []string{result.ProviderTaskID, result.ProviderIssueID, result.CodeVersion}
	filtered := evidence[:0]
	for _, item := range evidence {
		if strings.TrimSpace(item) != "" {
			filtered = append(filtered, item)
		}
	}
	if len(filtered) == 0 {
		// The legacy result schema has no evidence_ids field. Preserve a stable
		// receipt derived from the structured result so graph terminal gates do
		// not silently accept an empty result while old clients migrate.
		digest := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s\x00%s", result.Stage, result.AgentID, result.Outcome, result.Summary)))
		filtered = []string{"legacy-result:" + hex.EncodeToString(digest[:])}
	}
	structured := orchestration.StructuredResult{Outcome: outcome, Summary: result.Summary, EvidenceIDs: filtered, Fields: map[string]any{
		"coverage":          result.Coverage,
		"provider_task_id":  result.ProviderTaskID,
		"provider_issue_id": result.ProviderIssueID,
		"next_node_id":      compat.PipelineNodeID(next.PipelineStage),
		"next_stage":        int(next.PipelineStage),
		"continue":          next.Status == domain.PipelineRunning,
		"pipeline_status":   next.Status,
	}}
	var failure *orchestration.FailureReason
	if event != "success" && event != "approval" {
		failure = &orchestration.FailureReason{Code: "legacy_pipeline_" + event, Message: result.Summary, Retryable: event == "failure" || event == "bug"}
	}
	now := time.Now().UTC()
	transitionKey := "legacy:" + attempt.ID + ":finish"
	finished, err := projection.FinishAttempt(plan, attempt.ID, orchestration.TransitionInput{PlanRevision: plan.Revision, AttemptID: attempt.ID, LeaseToken: attempt.Lease.FencingToken, Event: event, Result: structured, Failure: failure, IdempotencyKey: transitionKey, Now: now})
	if err != nil {
		return err
	}
	return s.commitLegacyGraphEvent(plan, projection, finished, "attempt.finished", transitionKey, map[string]any{
		"attempt_id":                 attempt.ID,
		"event":                      event,
		"result":                     structured,
		"failure":                    failure,
		"transition_idempotency_key": transitionKey,
		"transition_at":              now,
	}, finished.Lease.FencingToken)
}

func (s *Server) failLegacyGraphDispatch(run domain.PipelineRun, cause error) error {
	if strings.TrimSpace(run.ActiveGraphAttemptID) == "" {
		return nil
	}
	next := run
	next.Status = domain.PipelineSuspended
	summary := "legacy provider dispatch failed"
	if cause != nil {
		summary = providerSafeError(cause)
	}
	return s.finishLegacyGraphAttempt(run, next, domain.PipelineStepResult{
		Stage: run.PipelineStage, AgentID: run.AgentFor(run.PipelineStage), Outcome: "fail", Summary: summary, ErrorLog: summary,
	})
}

func (s *Server) resolveLegacyGraphApproval(run, next domain.PipelineRun, approval domain.Approval) error {
	if s == nil || s.Orchestration == nil || strings.TrimSpace(run.ExecutionPlanID) == "" || strings.TrimSpace(run.ActiveGraphAttemptID) == "" {
		return nil
	}
	s.legacyGraphMu.Lock()
	defer s.legacyGraphMu.Unlock()
	plan, err := s.Orchestration.GetPlan(run.WorkspaceID, run.ExecutionPlanID)
	if err != nil {
		return err
	}
	projection, err := s.Orchestration.GetProjection(plan.ID)
	if err != nil {
		return err
	}
	attempt, ok := projection.Attempts[run.ActiveGraphAttemptID]
	if !ok || attempt.Status != orchestration.AttemptWaiting {
		return orchestration.ErrStaleAttempt
	}
	transition := "approval_granted"
	outcome := "approved"
	var failure *orchestration.FailureReason
	if approval.Decision == "rejected" {
		transition = "approval_denied"
		outcome = "rejected"
		failure = &orchestration.FailureReason{Code: "legacy_pipeline_approval_denied", Message: approval.Reason, Retryable: false}
	}
	result := orchestration.StructuredResult{
		Outcome:     outcome,
		Summary:     approval.Reason,
		EvidenceIDs: []string{"approval:" + approval.ID},
		Fields: map[string]any{
			"approval_id":     approval.ID,
			"next_node_id":    compat.PipelineNodeID(next.PipelineStage),
			"next_stage":      int(next.PipelineStage),
			"continue":        next.Status == domain.PipelineRunning,
			"pipeline_status": next.Status,
		},
	}
	now := time.Now().UTC()
	key := "legacy:" + attempt.ID + ":approval:" + approval.ID
	finished, err := projection.FinishAttempt(plan, attempt.ID, orchestration.TransitionInput{
		PlanRevision: plan.Revision, AttemptID: attempt.ID, LeaseToken: attempt.Lease.FencingToken,
		Event: transition, Result: result, Failure: failure, IdempotencyKey: key, Now: now,
	})
	if err != nil {
		return err
	}
	return s.commitLegacyGraphEvent(plan, projection, finished, "attempt.finished", key, map[string]any{
		"attempt_id": attempt.ID, "event": transition, "result": result, "failure": failure,
		"transition_idempotency_key": key, "transition_at": now,
	}, finished.Lease.FencingToken)
}

func (s *Server) commitLegacyGraphEvent(plan orchestration.RequirementExecutionPlan, projection orchestration.PlanProjection, attempt orchestration.NodeAttempt, eventType, key string, payload map[string]any, fencing int64) error {
	items := s.Orchestration.ListEvents(plan.ID, 0)
	var previous *orchestration.Event
	if len(items) > 0 {
		tail := items[len(items)-1]
		previous = &tail
	}
	event, err := orchestration.NewEvent(previous, plan.ID, plan.WorkspaceID, eventType, key, payload)
	if err != nil {
		return err
	}
	event.AttemptID, event.NodeID, event.FencingToken = attempt.ID, attempt.NodeID, fencing
	event.Seal()
	return s.Orchestration.CommitEventProjection(event, projection)
}

func legacyGraphPayloadHash(scope compat.DispatchScope, envelope harness.ContextEnvelope) string {
	digest := sha256.Sum256([]byte(scope.PlanID + "\x00" + scope.NodeID + "\x00" + scope.AttemptID + "\x00" + envelope.ReplayKey + "\x00" + envelope.SelectionDigest))
	return hex.EncodeToString(digest[:])
}
