package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/harness"
)

func (s *Server) workflowTemplateRoute(w http.ResponseWriter, r *http.Request, path string) {
	path = strings.Trim(path, "/")
	if path == "" {
		switch r.Method {
		case http.MethodGet:
			s.writeJSON(w, http.StatusOK, map[string]any{"items": s.Store.ListWorkflowTemplates(requestWorkspace(r, ""))})
		case http.MethodPost:
			var input domain.WorkflowTemplate
			if err := decodeJSON(r, &input); err != nil {
				s.problem(w, r, 400, "invalid_json", err.Error(), nil)
				return
			}
			input.WorkspaceID = requestWorkspace(r, input.WorkspaceID)
			saved, err := s.Store.UpsertWorkflowTemplate(input)
			if err != nil {
				s.problem(w, r, 422, "workflow_invalid", err.Error(), nil)
				return
			}
			s.recordAudit(r, saved.WorkspaceID, "workflow-template.created", saved.ID, map[string]any{"mode": saved.Mode, "step_count": len(saved.Steps)})
			s.writeJSON(w, http.StatusCreated, saved)
		default:
			s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
		}
		return
	}
	template, err := s.Store.GetWorkflowTemplate(strings.Split(path, "/")[0])
	if err != nil || !workspaceMatchesRequest(r, template.WorkspaceID) {
		s.problem(w, r, 404, "workflow_template_not_found", "workflow template not found", nil)
		return
	}
	if path == template.ID && r.Method == http.MethodGet {
		s.writeJSON(w, 200, template)
		return
	}
	if path == template.ID && r.Method == http.MethodDelete {
		if err := s.Store.DeleteWorkflowTemplate(template.ID); err != nil {
			s.problem(w, r, 409, "workflow_template_delete_failed", err.Error(), nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.problem(w, r, 404, "not_found", "route not found", nil)
}

func (s *Server) resumePipelineAfterApproval(approval domain.Approval) (domain.PipelineRun, error) {
	for _, candidate := range s.Store.ListPipelines(approval.WorkspaceID, "") {
		if candidate.DesignApprovalID != approval.ID {
			continue
		}
		if candidate.Status != domain.PipelineWaitingApproval {
			return candidate, errors.New("pipeline is no longer waiting for design approval")
		}
		waiting := candidate
		if approval.Decision == "rejected" {
			candidate.Status = domain.PipelineSuspended
			candidate.DesignApprovalStatus = "rejected"
			candidate.SuspendReason = "design rejected: " + strings.TrimSpace(approval.Reason)
			candidate.Version++
			candidate.UpdatedAt = time.Now().UTC()
			updated, err := s.Store.UpdatePipeline(candidate, candidate.Version-1)
			if err != nil {
				return domain.PipelineRun{}, err
			}
			if err := s.resolveLegacyGraphApproval(waiting, updated, approval); err != nil {
				return updated, err
			}
			return updated, nil
		}
		candidate.DesignApprovalStatus = "approved"
		candidate.PipelineStage = candidate.NextSelectedStage(domain.PipelineDesign)
		candidate.Status = domain.PipelineRunning
		candidate.SuspendReason = ""
		candidate.ActiveAgentID = candidate.AgentFor(candidate.PipelineStage)
		candidate.Version++
		updated, err := s.Store.UpdatePipeline(candidate, candidate.Version-1)
		if err != nil {
			return domain.PipelineRun{}, err
		}
		if err := s.resolveLegacyGraphApproval(waiting, updated, approval); err != nil {
			updated.Status = domain.PipelineSuspended
			updated.SuspendReason = "graph projection rejected approval: " + err.Error()
			updated.Version++
			updated.UpdatedAt = time.Now().UTC()
			updated, _ = s.Store.UpdatePipeline(updated, updated.Version-1)
			return updated, err
		}
		return s.dispatchPipeline(updated)
	}
	return domain.PipelineRun{}, nil
}

func (s *Server) chatRoute(w http.ResponseWriter, r *http.Request, path string) {
	path = strings.Trim(path, "/")
	if path == "" {
		switch r.Method {
		case http.MethodGet:
			s.writeJSON(w, 200, map[string]any{"items": s.Store.ListChatSessions(requestWorkspace(r, ""), r.URL.Query().Get("project_id"))})
		case http.MethodPost:
			s.createChatSession(w, r)
		default:
			s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
		}
		return
	}
	parts := strings.Split(path, "/")
	session, err := s.Store.GetChatSession(parts[0])
	if err != nil || !workspaceMatchesRequest(r, session.WorkspaceID) {
		s.problem(w, r, 404, "chat_not_found", "chat session not found", nil)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		messages, _ := s.Store.ListChatMessages(session.ID)
		status, statusErr := s.Harness.ContextStatus(session.HarnessSessionID)
		if statusErr != nil {
			s.problem(w, r, 503, "harness_unavailable", statusErr.Error(), nil)
			return
		}
		s.writeJSON(w, 200, map[string]any{"chat": session, "messages": messages, "context": status})
		return
	}
	if len(parts) == 2 && parts[1] == "messages" && r.Method == http.MethodPost {
		s.sendChatMessage(w, r, session)
		return
	}
	if len(parts) == 2 && parts[1] == "context" && r.Method == http.MethodGet {
		s.sessionRoute(w, r, "/"+session.HarnessSessionID)
		return
	}
	s.problem(w, r, 404, "not_found", "route not found", nil)
}

func (s *Server) createChatSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		WorkspaceID          string  `json:"workspace_id"`
		ProjectID            string  `json:"project_id"`
		Title                string  `json:"title"`
		CreatedBy            string  `json:"created_by"`
		BudgetTokens         int64   `json:"budget_tokens"`
		AutoCompaction       *bool   `json:"auto_compaction"`
		CompactionThreshold  float64 `json:"compaction_threshold"`
		CompactionRetainTail int     `json:"compaction_retain_tail"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, 400, "invalid_json", err.Error(), nil)
		return
	}
	workspaceID := requestWorkspace(r, input.WorkspaceID)
	if workspaceID == "" {
		s.problem(w, r, 422, "validation_error", "workspace_id is required", nil)
		return
	}
	chat := domain.ChatSession{WorkspaceID: workspaceID, ProjectID: strings.TrimSpace(input.ProjectID), Title: strings.TrimSpace(input.Title), CreatedBy: strings.TrimSpace(input.CreatedBy)}
	if userID := strings.TrimSpace(r.Header.Get("X-Member-ID")); userID != "" {
		chat.CreatedBy = userID
	}
	if chat.Title == "" {
		chat.Title = "Untitled conversation"
	}
	chat.ID = domain.NewID()
	chat.HarnessSessionID = "chat-" + chat.ID
	saved, err := s.Store.CreateChatSession(chat)
	if err != nil {
		s.problem(w, r, 422, "chat_invalid", err.Error(), nil)
		return
	}
	auto := true
	if input.AutoCompaction != nil {
		auto = *input.AutoCompaction
	}
	if _, err := s.Harness.CreateSession(harness.Session{ID: saved.HarnessSessionID, TenantID: tenant(r), WorkspaceID: saved.WorkspaceID, ProjectID: saved.ProjectID, BudgetTokens: input.BudgetTokens, AutoCompaction: auto, AutoCompactionSet: true, CompactionThreshold: input.CompactionThreshold, CompactionRetainTail: input.CompactionRetainTail}); err != nil {
		_ = s.Store.DeleteChatSession(saved.ID)
		s.problem(w, r, 503, "harness_unavailable", err.Error(), nil)
		return
	}
	s.recordAudit(r, saved.WorkspaceID, "chat.created", saved.ID, map[string]any{"project_id": saved.ProjectID})
	s.writeJSON(w, 201, saved)
}

func (s *Server) sendChatMessage(w http.ResponseWriter, r *http.Request, session domain.ChatSession) {
	var input struct {
		Content       string   `json:"content"`
		Role          string   `json:"role"`
		AttachmentIDs []string `json:"attachment_ids"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, 400, "invalid_json", err.Error(), nil)
		return
	}
	if strings.TrimSpace(input.Role) == "" {
		input.Role = "user"
	}
	if input.Role != "user" {
		s.problem(w, r, 422, "invalid_role", "chat clients may append only user messages", nil)
		return
	}
	if len(input.AttachmentIDs) > 0 {
		allowed := map[string]bool{}
		for _, item := range s.Store.ListAttachments(session.WorkspaceID, "chat_session", session.ID) {
			allowed[item.ID] = true
		}
		for _, id := range input.AttachmentIDs {
			if !allowed[strings.TrimSpace(id)] {
				s.problem(w, r, 422, "invalid_attachment", "attachment is not owned by this chat session", map[string]any{"attachment_id": id})
				return
			}
		}
	}
	turnKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if turnKey == "" {
		turnKey = domain.NewID()
	}
	turn, err := s.Harness.AppendTurn(session.HarnessSessionID, harness.Turn{Role: harness.RoleUser, Content: strings.TrimSpace(input.Content), IdempotencyKey: "chat:" + session.ID + ":" + turnKey, Metadata: map[string]string{"chat_session_id": session.ID}})
	if err != nil {
		s.problem(w, r, 422, "chat_message_invalid", err.Error(), nil)
		return
	}
	message, err := s.Store.AppendChatMessage(domain.ChatMessage{ChatSessionID: session.ID, WorkspaceID: session.WorkspaceID, Role: "user", Content: input.Content, AttachmentIDs: input.AttachmentIDs, TurnID: turn.ID, TurnHash: turn.Hash})
	if err != nil {
		s.problem(w, r, 500, "chat_message_failed", err.Error(), nil)
		return
	}
	status, _ := s.Harness.ContextStatus(session.HarnessSessionID)
	s.writeJSON(w, http.StatusCreated, map[string]any{"message": message, "context": status})
}
