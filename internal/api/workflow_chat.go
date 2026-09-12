package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/harness"
	"github.com/adro-project/adro/internal/orchestration"
	"github.com/adro-project/adro/internal/provider"
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
	if len(parts) == 1 && r.Method == http.MethodPatch {
		s.updateChatSession(w, r, session)
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

func (s *Server) updateChatSession(w http.ResponseWriter, r *http.Request, session domain.ChatSession) {
	var patch map[string]json.RawMessage
	if err := decodeJSON(r, &patch); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	updated := session
	changedBinding := false
	for key, raw := range patch {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			s.problem(w, r, http.StatusBadRequest, "invalid_chat_patch", fmt.Sprintf("field %q must be a string", key), nil)
			return
		}
		value = strings.TrimSpace(value)
		switch key {
		case "title":
			if value != "" {
				updated.Title = value
			}
		case "project_id":
			updated.ProjectID = value
		case "agent_id":
			updated.AgentID = value
			changedBinding = true
		case "runtime_id":
			updated.RuntimeID = value
			changedBinding = true
		case "model":
			updated.Model = value
			changedBinding = true
		case "thinking_level":
			updated.ThinkingLevel = value
			changedBinding = true
		case "service_tier":
			updated.ServiceTier = value
			changedBinding = true
		default:
			s.problem(w, r, http.StatusBadRequest, "invalid_chat_patch", fmt.Sprintf("field %q cannot be updated", key), nil)
			return
		}
	}
	if updated.Title == "" {
		s.problem(w, r, http.StatusUnprocessableEntity, "chat_invalid", "title is required", nil)
		return
	}
	if changedBinding {
		if strings.TrimSpace(updated.AgentID) != "" {
			agent, agentErr := s.chatAgentForSession(updated, 0)
			if agentErr != nil {
				s.problem(w, r, http.StatusUnprocessableEntity, "chat_runtime_invalid", agentErr.Error(), nil)
				return
			}
			updated.AgentRevision = agent.Revision
		} else {
			updated.AgentRevision = 0
		}
		_, selection, key, err := s.chatProviderSelection(updated)
		if err != nil {
			s.problem(w, r, http.StatusUnprocessableEntity, "chat_runtime_invalid", err.Error(), nil)
			return
		}
		updated.RuntimeID = selection.RuntimeID
		updated.Model = selection.Model
		updated.ThinkingLevel = selection.ThinkingLevel
		updated.ServiceTier = selection.ServiceTier
		updated.ProviderRuntimeKey = key
		// A changed Agent/runtime must not reuse a provider-native session that
		// was created under the previous instructions or relay. The next turn
		// carries the complete ADRO context and can start a fresh native thread.
		updated.ProviderSessionID = ""
		updated.ProviderWorkDir = ""
		updated.ProviderRunID = ""
		updated.ContinuityMode = "compiled_context"
	}
	updated.UpdatedAt = time.Now().UTC()
	saved, err := s.Store.UpdateChatSession(updated)
	if err != nil {
		s.problem(w, r, http.StatusConflict, "chat_update_failed", err.Error(), nil)
		return
	}
	s.writeJSON(w, http.StatusOK, saved)
}

func (s *Server) createChatSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		WorkspaceID          string  `json:"workspace_id"`
		AgentID              string  `json:"agent_id"`
		ProjectID            string  `json:"project_id"`
		Title                string  `json:"title"`
		CreatedBy            string  `json:"created_by"`
		RuntimeID            string  `json:"runtime_id"`
		Model                string  `json:"model"`
		ThinkingLevel        string  `json:"thinking_level"`
		ServiceTier          string  `json:"service_tier"`
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
	chat := domain.ChatSession{WorkspaceID: workspaceID, AgentID: strings.TrimSpace(input.AgentID), ProjectID: strings.TrimSpace(input.ProjectID), Title: strings.TrimSpace(input.Title), CreatedBy: strings.TrimSpace(input.CreatedBy), RuntimeID: strings.TrimSpace(input.RuntimeID), Model: strings.TrimSpace(input.Model), ThinkingLevel: strings.TrimSpace(input.ThinkingLevel), ServiceTier: strings.TrimSpace(input.ServiceTier)}
	if userID := strings.TrimSpace(r.Header.Get("X-Member-ID")); userID != "" {
		chat.CreatedBy = userID
	}
	if chat.Title == "" {
		chat.Title = "Untitled conversation"
	}
	chat.ID = domain.NewID()
	chat.HarnessSessionID = "chat-" + chat.ID
	if strings.TrimSpace(chat.AgentID) != "" {
		agent, agentErr := s.chatAgentForSession(chat, 0)
		if agentErr != nil {
			s.problem(w, r, http.StatusUnprocessableEntity, "chat_runtime_invalid", agentErr.Error(), nil)
			return
		}
		chat.AgentRevision = agent.Revision
	}
	_, selection, key, selectionErr := s.chatProviderSelection(chat)
	if selectionErr != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "chat_runtime_invalid", selectionErr.Error(), nil)
		return
	}
	chat.RuntimeID = selection.RuntimeID
	chat.Model = selection.Model
	chat.ThinkingLevel = selection.ThinkingLevel
	chat.ServiceTier = selection.ServiceTier
	chat.ProviderRuntimeKey = key
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
	input.Content = strings.TrimSpace(input.Content)
	if input.Content == "" {
		s.problem(w, r, http.StatusUnprocessableEntity, "chat_message_invalid", "content is required", nil)
		return
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
	turn, err := s.Harness.AppendTurn(session.HarnessSessionID, harness.Turn{Role: harness.RoleUser, Content: input.Content, IdempotencyKey: "chat:" + session.ID + ":" + turnKey, Metadata: map[string]string{"chat_session_id": session.ID}})
	if err != nil {
		s.problem(w, r, 422, "chat_message_invalid", err.Error(), nil)
		return
	}
	requestKey := "chat:" + session.ID + ":" + turnKey
	message, err := s.Store.AppendChatMessage(domain.ChatMessage{ChatSessionID: session.ID, WorkspaceID: session.WorkspaceID, Role: "user", Content: input.Content, AttachmentIDs: input.AttachmentIDs, RequestKey: requestKey, TurnID: turn.ID, TurnHash: turn.Hash})
	if err != nil {
		s.problem(w, r, 500, "chat_message_failed", err.Error(), nil)
		return
	}

	// A retried HTTP request may have already completed the provider turn after
	// the client lost its response. The Harness idempotency key gives us the
	// original user turn; reuse its durable assistant projection instead of
	// dispatching a duplicate provider side effect.
	if messages, listErr := s.Store.ListChatMessages(session.ID); listErr == nil {
		for _, existing := range messages {
			if existing.Role == "assistant" && (existing.RequestKey == "chat:"+session.ID+":assistant:"+turn.ID || existing.TurnID == turn.ID) {
				status, _ := s.Harness.ContextStatus(session.HarnessSessionID)
				s.writeJSON(w, http.StatusOK, map[string]any{"message": existing, "user_message": message, "context": status, "replayed": true})
				return
			}
		}
	}

	// Native continuation receives only the new turn because the provider owns
	// the prior transcript. A fresh run must receive the rendered ADRO context;
	// passing the fallback objective here would silently discard earlier turns
	// whenever a relay or provider changes underneath CCSwitch.
	prompt, envelope, err := s.compiledHarnessDispatch(session.HarnessSessionID, "")
	if err != nil {
		s.problem(w, r, http.StatusConflict, "chat_context_unavailable", err.Error(), map[string]any{"user_message_id": message.ID})
		return
	}
	executor, selection, runtimeKey, err := s.chatProviderSelection(session)
	if err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "chat_runtime_invalid", err.Error(), nil)
		return
	}

	runKey := "chat:" + session.ID + ":run:" + turn.ID
	compiledRunKey := runKey + ":compiled_context"
	var binding provider.RunBinding
	continuityMode := "compiled_context"
	if strings.TrimSpace(session.ProviderSessionID) != "" && session.ProviderRuntimeKey == runtimeKey {
		if continuity, ok := executor.(provider.ContinuityProvider); ok {
			binding, err = continuity.ContinueWorkItem(r.Context(), provider.ContinuationCommand{
				IssueID: session.ID, AgentID: session.AgentID, Input: input.Content,
				ExpectedSessionID: session.ProviderSessionID, ExpectedWorkDir: session.ProviderWorkDir,
				ContextEnvelope: envelope, IdempotencyKey: runKey,
			})
			if err == nil {
				continuityMode = "native_session"
			} else {
				// A provider-side validation failure has not created a durable
				// continuation. Start the safe compiled-context path with a
				// distinct key so the failed native attempt can never alias it.
				binding = provider.RunBinding{}
			}
		}
	}
	if binding.ID == "" {
		// Native resume is an optimization. If a relay/provider changed behind
		// the runtime (for example through CCSwitch) and rejects the old native
		// session, start a fresh provider request with the complete ADRO-owned
		// context instead of losing the conversation.
		binding, err = executor.StartRun(r.Context(), provider.StartRunCommand{
			WorkItemID: session.ID, ProviderIssueID: session.ID, Input: prompt,
			ContextEnvelope: envelope, IdempotencyKey: compiledRunKey,
		})
		continuityMode = "compiled_context"
	}
	if err != nil {
		s.problem(w, r, http.StatusBadGateway, "chat_runtime_start_failed", err.Error(), map[string]any{"user_message_id": message.ID})
		return
	}

	// Provider runs outlive an HTTP client disconnect. Waiting on the request
	// context would leave the user turn durable but drop the assistant
	// projection when a browser tab refreshes during a slow real-model turn.
	runCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	snapshot, err := waitForChatRun(runCtx, executor, binding.ID)
	if err != nil && continuityMode == "native_session" && snapshot.RecoveryState == provider.RecoveryNativeContinuationUnavailable {
		// Codex rejected resume before turn/start, so retrying with ADRO's
		// compiled transcript is safe and preserves the same user turn.
		binding, err = executor.StartRun(runCtx, provider.StartRunCommand{
			WorkItemID: session.ID, ProviderIssueID: session.ID, Input: prompt,
			ContextEnvelope: envelope, IdempotencyKey: compiledRunKey,
		})
		continuityMode = "compiled_context"
		if err == nil {
			snapshot, err = waitForChatRun(runCtx, executor, binding.ID)
		}
	}
	if err != nil {
		s.problem(w, r, http.StatusBadGateway, "chat_runtime_failed", err.Error(), map[string]any{"run_id": binding.ID})
		return
	}
	assistantText := chatAssistantText(snapshot.Output)
	if assistantText == "" {
		assistantText = "The selected runtime completed this turn without returning assistant text."
	}
	assistantTurn, err := s.Harness.AppendTurn(session.HarnessSessionID, harness.Turn{Role: harness.RoleAssistant, Content: assistantText, AttemptID: binding.ID, IdempotencyKey: "chat:" + session.ID + ":assistant:" + turn.ID, Metadata: map[string]string{"chat_session_id": session.ID, "runtime_id": selection.RuntimeID, "continuity": continuityMode}})
	if err != nil {
		s.problem(w, r, http.StatusConflict, "chat_context_commit_failed", err.Error(), map[string]any{"run_id": binding.ID})
		return
	}
	assistant, err := s.Store.AppendChatMessage(domain.ChatMessage{ChatSessionID: session.ID, WorkspaceID: session.WorkspaceID, Role: "assistant", Content: assistantText, RequestKey: "chat:" + session.ID + ":assistant:" + turn.ID, TurnID: assistantTurn.ID, TurnHash: assistantTurn.Hash, ProviderRunID: binding.ID})
	if err != nil {
		s.problem(w, r, http.StatusInternalServerError, "chat_response_failed", err.Error(), map[string]any{"run_id": binding.ID})
		return
	}
	session.ProviderRuntimeKey = runtimeKey
	session.ProviderSessionID = snapshot.SessionID
	session.ProviderWorkDir = snapshot.WorkDir
	session.ProviderRunID = binding.ID
	session.ContinuityMode = continuityMode
	session.UpdatedAt = time.Now().UTC()
	if _, updateErr := s.Store.UpdateChatSession(session); updateErr != nil {
		s.problem(w, r, http.StatusConflict, "chat_state_commit_failed", updateErr.Error(), map[string]any{"run_id": binding.ID})
		return
	}
	status, _ := s.Harness.ContextStatus(session.HarnessSessionID)
	s.writeJSON(w, http.StatusCreated, map[string]any{"message": assistant, "user_message": message, "run": snapshot, "context": status, "continuity": continuityMode})
}

func (s *Server) chatProviderSelection(session domain.ChatSession) (provider.ExecutionProvider, provider.RuntimeSelection, string, error) {
	selection := provider.RuntimeSelection{RuntimeID: strings.TrimSpace(session.RuntimeID), Model: strings.TrimSpace(session.Model), ThinkingLevel: strings.TrimSpace(session.ThinkingLevel), ServiceTier: strings.TrimSpace(session.ServiceTier)}
	if strings.TrimSpace(session.AgentID) != "" {
		agent, err := s.chatAgentForSession(session, session.AgentRevision)
		if err != nil {
			return nil, provider.RuntimeSelection{}, "", fmt.Errorf("load chat Agent: %w", err)
		}
		selection.RuntimeID = strings.TrimSpace(agent.ExecutorBinding.RuntimeID)
		selection.Model = strings.TrimSpace(agent.ExecutorBinding.Model)
		selection.ThinkingLevel = strings.TrimSpace(agent.ExecutorBinding.ThinkingLevel)
		selection.ServiceTier = strings.TrimSpace(agent.ExecutorBinding.ServiceTier)
		selection.CustomArgs = append([]string(nil), agent.ExecutorBinding.CustomArgs...)
		selection.RuntimeConfig = cloneRuntimeConfig(agent.ExecutorBinding.RuntimeConfig)
		environment, err := resolveAgentEnvironment(agent.ExecutorBinding.Environment)
		if err != nil {
			return nil, provider.RuntimeSelection{}, "", err
		}
		selection.Environment = environment
		selection.MCPServers, err = s.agentRuntimeMCPServers(agent)
		if err != nil {
			return nil, provider.RuntimeSelection{}, "", err
		}
		for _, skill := range agent.DisabledRuntimeSkills {
			selection.DisabledRuntimeSkills = append(selection.DisabledRuntimeSkills, provider.RuntimeSkillRef{RuntimeID: skill.RuntimeID, Provider: skill.Provider, Root: skill.Root, Key: skill.Key, Name: skill.Name, Plugin: skill.Plugin})
		}
	}
	if selection.RuntimeID == "" {
		selection.RuntimeID = "local"
	}
	key := chatRuntimeKey(selection)
	if s.RuntimeProviders != nil {
		executor, err := s.RuntimeProviders.Resolve(selection)
		return executor, selection, key, err
	}
	if s.Provider == nil {
		return nil, selection, key, errors.New("no execution provider is configured")
	}
	return s.Provider, selection, key, nil
}

func (s *Server) chatAgentForSession(session domain.ChatSession, revision int64) (orchestration.AgentDefinition, error) {
	if s.Orchestration == nil {
		return orchestration.AgentDefinition{}, errors.New("chat Agent is unavailable")
	}
	agent, err := s.Orchestration.GetAgent(session.WorkspaceID, session.AgentID, revision)
	if err != nil {
		return orchestration.AgentDefinition{}, err
	}
	if agent.Status != orchestration.AgentActive {
		return orchestration.AgentDefinition{}, errors.New("chat Agent is not active")
	}
	return agent, nil
}

func cloneRuntimeConfig(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func chatRuntimeKey(selection provider.RuntimeSelection) string {
	// Avoid summing attacker-controlled lengths into a capacity calculation.
	keys := make([]string, 0, len(selection.CustomArgs))
	for index, arg := range selection.CustomArgs {
		keys = append(keys, fmt.Sprintf("arg:%d=%s", index, arg))
	}
	for key := range selection.RuntimeConfig {
		keys = append(keys, "config:"+key+"="+selection.RuntimeConfig[key])
	}
	for key, value := range selection.Environment {
		// The secret value is included only in the digest, never in the
		// persisted provider key itself.
		keys = append(keys, "env:"+key+"="+value)
	}
	for _, server := range selection.MCPServers {
		keys = append(keys, "mcp:"+server.Name+":"+server.Endpoint+":"+server.Protocol+":"+server.BearerTokenEnvVar)
	}
	for _, skill := range selection.DisabledRuntimeSkills {
		keys = append(keys, "skill:"+skill.RuntimeID+":"+skill.Provider+":"+skill.Root+":"+skill.Key+":"+skill.Plugin)
	}
	sort.Strings(keys)
	data := strings.Join(append([]string{selection.RuntimeID, selection.Model, selection.ThinkingLevel, selection.ServiceTier}, keys...), "\x00")
	digest := sha256.Sum256([]byte(data))
	return hex.EncodeToString(digest[:])
}

func waitForChatRun(ctx context.Context, executor provider.ExecutionProvider, runID string) (provider.RunSnapshot, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot, err := executor.GetRun(ctx, runID)
		if err != nil {
			return provider.RunSnapshot{}, err
		}
		switch snapshot.Status {
		case "completed":
			return snapshot, nil
		case "failed", "cancelled", "timed_out":
			if snapshot.Error == "" {
				snapshot.Error = "runtime finished with status " + snapshot.Status
			}
			return snapshot, errors.New(snapshot.Error)
		}
		select {
		case <-ctx.Done():
			return provider.RunSnapshot{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func chatAssistantText(output string) string {
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	var last string
	var delta strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var frame map[string]any
		if json.Unmarshal([]byte(line), &frame) != nil {
			continue
		}
		if text := assistantTextFromFrame(frame); text != "" {
			last = text
		}
		if strings.Contains(strings.ToLower(stringValue(frame["method"])), "agentmessage") {
			if params, ok := frame["params"].(map[string]any); ok {
				if piece := strings.TrimSpace(stringValue(params["delta"])); piece != "" {
					delta.WriteString(piece)
				}
			}
		}
	}
	if last != "" {
		return last
	}
	if text := strings.TrimSpace(delta.String()); text != "" {
		return text
	}
	return strings.TrimSpace(providerNarrative(output))
}

func assistantTextFromFrame(frame map[string]any) string {
	if normalizeFrameType(stringValue(frame["type"])) == "result" {
		if result, ok := frame["result"].(map[string]any); ok {
			if text := strings.TrimSpace(stringValue(result["output"])); text != "" {
				return text
			}
		}
		if text := strings.TrimSpace(stringValue(frame["output"])); text != "" {
			return text
		}
		if text := strings.TrimSpace(stringValue(frame["result"])); text != "" {
			return text
		}
	}
	for _, candidate := range []any{frame["item"], frame["params"]} {
		if params, ok := candidate.(map[string]any); ok {
			if item, ok := params["item"].(map[string]any); ok {
				params = item
			}
			itemType := normalizeFrameType(stringValue(params["type"]))
			if itemType != "agentmessage" {
				continue
			}
			if text := strings.TrimSpace(stringValue(params["text"])); text != "" {
				return text
			}
			if content, ok := params["content"].([]any); ok {
				var parts []string
				for _, raw := range content {
					part, _ := raw.(map[string]any)
					if text := strings.TrimSpace(stringValue(part["text"])); text != "" {
						parts = append(parts, text)
					}
				}
				if len(parts) > 0 {
					return strings.Join(parts, "")
				}
			}
		}
	}
	if typ := normalizeFrameType(stringValue(frame["type"])); typ == "assistant" || typ == "message" {
		return strings.TrimSpace(stringValue(frame["text"]))
	}
	return ""
}

func normalizeFrameType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, "-", "")
	return value
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
