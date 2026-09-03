package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/harness"
	"github.com/adro-project/adro/internal/orchestration"
)

// orchestrationRoute exposes the plan/graph contracts without coupling the
// legacy pipeline handler to numeric stages. The in-memory repository is a
// deliberate local profile; production wiring can replace it via Server's
// repository seam later.
func (s *Server) orchestrationRoute(w http.ResponseWriter, r *http.Request, path string) {
	if path == "/execution-plans" {
		if s.Orchestration == nil {
			s.problem(w, r, http.StatusServiceUnavailable, "orchestration_unavailable", "orchestration repository is unavailable", nil)
			return
		}
		if r.Method != http.MethodGet {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"items": s.Orchestration.ListPlans(requestWorkspace(r, r.URL.Query().Get("workspace_id")))})
		return
	}
	if strings.HasPrefix(path, "/execution-plans/") && path != "/execution-plans/validate" {
		id := strings.TrimPrefix(path, "/execution-plans/")
		if s.Orchestration == nil {
			s.problem(w, r, http.StatusServiceUnavailable, "orchestration_unavailable", "orchestration repository is unavailable", nil)
			return
		}
		workspace := requestWorkspace(r, "")
		if strings.HasSuffix(id, "/timeline") {
			realID := strings.TrimSuffix(id, "/timeline")
			plan, err := s.getOrchestrationPlan(workspace, realID)
			if err != nil {
				s.problem(w, r, http.StatusNotFound, "plan_not_found", err.Error(), nil)
				return
			}
			s.writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "events": redactOrchestrationEvents(s.Orchestration.ListEvents(realID, int64(queryInt(r, "after", 0))))})
			return
		}
		if strings.HasSuffix(id, "/replay") {
			s.planReplay(w, r, strings.TrimSuffix(id, "/replay"), workspace)
			return
		}
		if strings.HasSuffix(id, "/tick") {
			s.executionPlanTick(w, r, strings.TrimSuffix(id, "/tick"), workspace)
			return
		}
		for _, action := range []string{"cancel", "retry", "takeover", "resume"} {
			if strings.HasSuffix(id, "/"+action) {
				s.executionPlanAction(w, r, strings.TrimSuffix(id, "/"+action), action, workspace)
				return
			}
		}
		if strings.Contains(id, "/nodes/") {
			parts := strings.SplitN(id, "/nodes/", 2)
			if len(parts) == 2 {
				nodeParts := strings.SplitN(parts[1], "/", 2)
				if len(nodeParts) == 2 && (nodeParts[1] == "approve" || nodeParts[1] == "deny") {
					s.executionPlanApproval(w, r, parts[0], nodeParts[0], nodeParts[1], workspace)
					return
				}
			}
		}
		plan, err := s.getOrchestrationPlan(workspace, id)
		if err != nil {
			s.problem(w, r, http.StatusNotFound, "plan_not_found", err.Error(), nil)
			return
		}
		if r.Method != http.MethodGet {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		s.writeJSON(w, http.StatusOK, plan)
		return
	}
	if r.Method != http.MethodPost || path != "/execution-plans/validate" {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "only POST /execution-plans/validate is supported", nil)
		return
	}
	var input struct {
		Graph orchestration.WorkflowGraph             `json:"graph"`
		Plan  *orchestration.RequirementExecutionPlan `json:"plan,omitempty"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	if err := orchestration.ValidateGraph(input.Graph); err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "graph_validation_failed", err.Error(), map[string]any{"graph_id": input.Graph.ID})
		return
	}
	hash, err := input.Graph.CanonicalHash()
	if err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "graph_hash_failed", err.Error(), nil)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"valid": true, "validation_digest": hash, "graph": input.Graph})
}

// executionPlanAction applies explicit lifecycle controls through the same
// reducer and event store as provider completions. The request carries the
// expected revision and, for attempt actions, the fencing token so stale
// operators cannot mutate a newer worker.
func (s *Server) executionPlanAction(w http.ResponseWriter, r *http.Request, planID, action, workspace string) {
	if r.Method != http.MethodPost {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "POST is required", nil)
		return
	}
	if !s.requireOrchestrationPermission(w, r) {
		return
	}
	plan, err := s.getOrchestrationPlan(workspace, planID)
	if err != nil {
		s.problem(w, r, http.StatusNotFound, "plan_not_found", err.Error(), nil)
		return
	}
	var input struct {
		PlanRevision int64                    `json:"expected_plan_revision"`
		AttemptID    string                   `json:"attempt_id,omitempty"`
		NodeID       string                   `json:"node_id,omitempty"`
		LeaseToken   int64                    `json:"lease_token,omitempty"`
		Owner        string                   `json:"owner,omitempty"`
		Reason       string                   `json:"reason,omitempty"`
		Context      *harness.ContextEnvelope `json:"context_envelope,omitempty"`
		WorkItemID   string                   `json:"work_item_id,omitempty"`
		AgentBinding string                   `json:"agent_binding_id,omitempty"`
	}
	if err := decodeJSON(r, &input); err != nil && !errors.Is(err, io.EOF) {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	if input.PlanRevision == 0 {
		input.PlanRevision = plan.Revision
	}
	if input.PlanRevision != plan.Revision {
		s.problem(w, r, http.StatusConflict, "stale_plan", "expected_plan_revision does not match the frozen plan", nil)
		return
	}
	if action == "resume" {
		if input.Context == nil {
			s.problem(w, r, http.StatusUnprocessableEntity, "context_envelope_required", "context_envelope is required to resume graph execution", nil)
			return
		}
		projection, projectionErr := s.Orchestration.GetProjection(plan.ID)
		if errors.Is(projectionErr, orchestration.ErrNotFound) {
			projection, projectionErr = orchestration.NewProjection(plan)
		}
		if projectionErr != nil {
			s.problem(w, r, http.StatusConflict, "projection_unavailable", projectionErr.Error(), nil)
			return
		}
		report, tickErr := (orchestration.Scheduler{Repository: s.Orchestration, Executor: orchestration.Executor{Provider: s.Provider, Events: s.Orchestration, Owner: r.Header.Get("X-Member-ID")}, Config: orchestration.SchedulerConfig{MaxConcurrent: queryInt(r, "max_concurrent", 0)}}).Tick(r.Context(), plan, &projection, *input.Context, input.WorkItemID, input.AgentBinding)
		if tickErr != nil && !errors.Is(tickErr, orchestration.ErrDeadlineExceeded) {
			s.problem(w, r, http.StatusConflict, "plan_resume_failed", tickErr.Error(), map[string]any{"report": report})
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "projection": projection, "report": report})
		return
	}
	projection, err := s.Orchestration.GetProjection(plan.ID)
	if err != nil {
		s.problem(w, r, http.StatusConflict, "projection_unavailable", err.Error(), nil)
		return
	}
	if input.AttemptID == "" && input.NodeID != "" {
		if node, ok := projection.Nodes[input.NodeID]; ok {
			input.AttemptID = node.CurrentAttempt
		}
	}
	if action == "retry" {
		if input.NodeID == "" {
			s.problem(w, r, http.StatusBadRequest, "node_id_required", "node_id is required for retry", nil)
			return
		}
		if err := projection.Retry(plan, input.NodeID); err != nil {
			s.problem(w, r, http.StatusConflict, "retry_failed", err.Error(), nil)
			return
		}
		if err := s.commitPlanActionEvent(r.Context(), plan, &projection, "node.retry_requested", input.NodeID, input.Reason, r.Header.Get("Idempotency-Key")); err != nil {
			s.problem(w, r, http.StatusServiceUnavailable, "retry_commit_failed", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusAccepted, map[string]any{"plan": plan, "projection": projection, "node_id": input.NodeID, "status": "ready"})
		return
	}
	if input.AttemptID == "" {
		s.problem(w, r, http.StatusBadRequest, "attempt_id_required", "attempt_id or node_id is required", nil)
		return
	}
	attempt, ok := projection.Attempts[input.AttemptID]
	if !ok {
		s.problem(w, r, http.StatusNotFound, "attempt_not_found", "attempt not found", nil)
		return
	}
	if input.LeaseToken == 0 {
		input.LeaseToken = attempt.Lease.FencingToken
	}
	executor := orchestration.Executor{Events: s.Orchestration, Owner: r.Header.Get("X-Member-ID")}
	if action == "takeover" {
		owner := strings.TrimSpace(input.Owner)
		if owner == "" {
			owner = strings.TrimSpace(r.Header.Get("X-Member-ID"))
		}
		if owner == "" {
			s.problem(w, r, http.StatusBadRequest, "owner_required", "owner is required for takeover", nil)
			return
		}
		if err := projection.TakeOver(plan, input.AttemptID, owner, time.Now().UTC()); err != nil {
			s.problem(w, r, http.StatusConflict, "takeover_failed", err.Error(), nil)
			return
		}
		taken := projection.Attempts[input.AttemptID]
		if err := s.commitAttemptFinishedEvent(r.Context(), plan, &projection, taken, "timeout", taken.Result, taken.FailureReason, r.Header.Get("Idempotency-Key")); err != nil {
			s.problem(w, r, http.StatusServiceUnavailable, "takeover_commit_failed", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"attempt": taken, "projection": projection, "status": "taken_over"})
		return
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = action
	}
	finished, err := executor.FinishAttempt(r.Context(), plan, &projection, input.AttemptID, orchestration.TransitionInput{PlanRevision: plan.Revision, AttemptID: input.AttemptID, LeaseToken: input.LeaseToken, Event: "cancel", Result: orchestration.StructuredResult{Outcome: "cancelled", Summary: reason, EvidenceIDs: []string{"operator:" + input.AttemptID}}, Failure: &orchestration.FailureReason{Code: "cancelled", Message: reason}, IdempotencyKey: r.Header.Get("Idempotency-Key"), Now: time.Now().UTC()})
	if err != nil {
		s.problem(w, r, http.StatusConflict, "cancel_failed", err.Error(), nil)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"attempt": finished, "projection": projection, "status": "cancelled"})
}

func (s *Server) commitPlanActionEvent(ctx context.Context, plan orchestration.RequirementExecutionPlan, projection *orchestration.PlanProjection, typ, nodeID, reason, key string) error {
	if strings.TrimSpace(key) == "" {
		key = plan.ID + ":" + typ + ":" + nodeID
	}
	items := s.Orchestration.ListEvents(plan.ID, 0)
	var previous *orchestration.Event
	if len(items) > 0 {
		tail := items[len(items)-1]
		previous = &tail
	}
	event, err := orchestration.NewEventWithContext(ctx, previous, plan.ID, plan.WorkspaceID, typ, key, map[string]any{"node_id": nodeID, "reason": reason})
	if err != nil {
		return err
	}
	return s.Orchestration.CommitEventProjection(event, *projection)
}

func (s *Server) commitAttemptFinishedEvent(ctx context.Context, plan orchestration.RequirementExecutionPlan, projection *orchestration.PlanProjection, attempt orchestration.NodeAttempt, transition string, result orchestration.StructuredResult, failure *orchestration.FailureReason, key string) error {
	if strings.TrimSpace(key) == "" {
		key = plan.ID + ":" + attempt.ID + ":" + transition
	}
	items := s.Orchestration.ListEvents(plan.ID, 0)
	var previous *orchestration.Event
	if len(items) > 0 {
		tail := items[len(items)-1]
		previous = &tail
	}
	now := time.Now().UTC()
	if attempt.FinishedAt != nil {
		now = attempt.FinishedAt.UTC()
	}
	event, err := orchestration.NewEventWithContext(ctx, previous, plan.ID, plan.WorkspaceID, "attempt.finished", key+":finished", map[string]any{"attempt_id": attempt.ID, "event": transition, "result": result, "failure": failure, "transition_idempotency_key": key, "transition_at": now})
	if err != nil {
		return err
	}
	event.AttemptID, event.NodeID, event.FencingToken = attempt.ID, attempt.NodeID, attempt.Lease.FencingToken
	event.Seal()
	if err := s.Orchestration.CommitEventProjection(event, *projection); err != nil {
		return err
	}
	return nil
}

func (s *Server) getOrchestrationPlan(workspace, id string) (orchestration.RequirementExecutionPlan, error) {
	plan, err := s.Orchestration.GetPlan(workspace, id)
	if err == nil || workspace != "" {
		return plan, err
	}
	for _, candidate := range s.Orchestration.ListPlans("") {
		if candidate.ID == id {
			return candidate, nil
		}
	}
	return orchestration.RequirementExecutionPlan{}, err
}

func (s *Server) executionPlanTick(w http.ResponseWriter, r *http.Request, planID, workspace string) {
	if r.Method != http.MethodPost {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "POST is required", nil)
		return
	}
	if !s.requireOrchestrationPermission(w, r) {
		return
	}
	plan, err := s.getOrchestrationPlan(workspace, planID)
	if err != nil {
		s.problem(w, r, http.StatusNotFound, "plan_not_found", err.Error(), nil)
		return
	}
	var input struct {
		Envelope       *harness.ContextEnvelope `json:"context_envelope"`
		WorkItemID     string                   `json:"work_item_id"`
		AgentBindingID string                   `json:"agent_binding_id"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	if input.Envelope == nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "context_envelope_required", "context_envelope is required for graph execution", nil)
		return
	}
	if strings.TrimSpace(input.WorkItemID) == "" {
		input.WorkItemID = "plan-" + plan.ID
	}
	projection, err := s.Orchestration.GetProjection(plan.ID)
	if errors.Is(err, orchestration.ErrNotFound) {
		projection, err = orchestration.NewProjection(plan)
		if err == nil {
			err = s.Orchestration.SaveProjection(projection)
		}
	}
	if err != nil {
		s.problem(w, r, http.StatusConflict, "projection_unavailable", err.Error(), nil)
		return
	}
	executor := orchestration.Executor{Provider: s.Provider, Events: s.Orchestration, Owner: r.Header.Get("X-Member-ID")}
	report, tickErr := (orchestration.Scheduler{Repository: s.Orchestration, Executor: executor, Config: orchestration.SchedulerConfig{MaxConcurrent: queryInt(r, "max_concurrent", 0)}}).Tick(context.Background(), plan, &projection, *input.Envelope, input.WorkItemID, input.AgentBindingID)
	if tickErr != nil && !errors.Is(tickErr, orchestration.ErrDeadlineExceeded) {
		s.problem(w, r, http.StatusConflict, "plan_tick_failed", tickErr.Error(), map[string]any{"report": report})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "projection": projection, "report": report})
}

func (s *Server) executionPlanApproval(w http.ResponseWriter, r *http.Request, planID, nodeID, action, workspace string) {
	if r.Method != http.MethodPost {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "POST is required", nil)
		return
	}
	if !s.requireOrchestrationPermission(w, r) {
		return
	}
	plan, err := s.getOrchestrationPlan(workspace, planID)
	if err != nil {
		s.problem(w, r, http.StatusNotFound, "plan_not_found", err.Error(), nil)
		return
	}
	projection, err := s.Orchestration.GetProjection(plan.ID)
	if err != nil {
		s.problem(w, r, http.StatusConflict, "projection_unavailable", err.Error(), nil)
		return
	}
	node, ok := projection.Nodes[nodeID]
	if !ok || node.CurrentAttempt == "" {
		s.problem(w, r, http.StatusNotFound, "node_attempt_not_found", "node has no current attempt", nil)
		return
	}
	attempt, ok := projection.Attempts[node.CurrentAttempt]
	if !ok || attempt.Status != orchestration.AttemptWaiting {
		s.problem(w, r, http.StatusConflict, "approval_not_pending", "node is not waiting for approval", nil)
		return
	}
	event := "approval_granted"
	result := orchestration.StructuredResult{Outcome: "approved", Summary: "human approval granted", EvidenceIDs: []string{"human-approval:" + attempt.ID}}
	if action == "deny" {
		event = "approval_denied"
		result = orchestration.StructuredResult{Outcome: "denied", Summary: "human approval denied", EvidenceIDs: []string{"human-denial:" + attempt.ID}}
	}
	executor := orchestration.Executor{Events: s.Orchestration, Owner: r.Header.Get("X-Member-ID")}
	finished, err := executor.FinishAttempt(context.Background(), plan, &projection, attempt.ID, orchestration.TransitionInput{PlanRevision: plan.Revision, AttemptID: attempt.ID, LeaseToken: attempt.Lease.FencingToken, Event: event, Result: result, IdempotencyKey: attempt.IdempotencyKey + ":" + event})
	if err != nil {
		s.problem(w, r, http.StatusConflict, "approval_failed", err.Error(), nil)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"attempt": finished, "projection": projection})
}

func (s *Server) mentionPreviewRoute(w http.ResponseWriter, r *http.Request, requirementID string) {
	if r.Method != http.MethodPost {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	var input struct {
		Content     string `json:"content"`
		CommentID   string `json:"comment_id"`
		Revision    int64  `json:"revision"`
		PlanVersion string `json:"plan_version"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	workspace := requestWorkspace(r, "")
	if workspace == "" {
		workspace = "local"
	}
	requirement, requirementErr := s.Store.GetRequirement(requirementID)
	if requirementErr != nil || requirement.WorkspaceID != workspace {
		s.problem(w, r, http.StatusNotFound, "requirement_not_found", "requirement not found", nil)
		return
	}
	commentID := strings.TrimSpace(input.CommentID)
	if commentID == "" {
		commentID = orchestration.NewID()
	}
	revision := input.Revision
	if revision < 1 {
		revision = 1
	}
	// Preview resolves the same persisted roster and runtime health used by
	// create/edit/retry. Client-supplied targets and health are intentionally
	// ignored so a preview cannot claim a route that creation would block.
	plan, err := s.computeCommentTriggers(r, domain.Comment{ID: commentID, WorkspaceID: requirement.WorkspaceID, TargetType: "requirement", TargetID: requirementID, Content: input.Content, Revision: revision})
	if err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "trigger_preview_failed", err.Error(), nil)
		return
	}
	_ = input.PlanVersion // plan revisions are frozen by the persisted roster.
	s.writeJSON(w, http.StatusOK, plan)
}
