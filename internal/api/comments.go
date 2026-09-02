package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/harness"
	"github.com/adro-project/adro/internal/mentions"
	"github.com/adro-project/adro/internal/provider"
	"github.com/adro-project/adro/internal/store"
)

type commentMutation struct {
	Content        string   `json:"content"`
	ParentID       string   `json:"parent_id,omitempty"`
	AuthorID       string   `json:"author_id,omitempty"`
	AuthorType     string   `json:"author_type,omitempty"`
	Mentions       []string `json:"mentions,omitempty"`
	Dispatch       *bool    `json:"dispatch,omitempty"`
	AgentBindingID string   `json:"agent_binding_id,omitempty"`
}

// commentRoute implements the provider-neutral discussion contract for a
// requirement or bug. Replies remain flat records with parent/root pointers;
// clients can render a tree and paginate deterministically by cursor.
func (s *Server) commentRoute(w http.ResponseWriter, r *http.Request, targetType, targetID, workspaceID string) {
	if r.Method == http.MethodGet {
		items, next := s.Store.ListComments(workspaceID, targetType, targetID, r.URL.Query().Get("cursor"), queryInt(r, "limit", 50))
		s.writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
		return
	}
	if r.Method != http.MethodPost {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	var input commentMutation
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	authorID := strings.TrimSpace(r.Header.Get("X-Member-ID"))
	authorType := "member"
	if authorID == "" {
		authorID = strings.TrimSpace(r.Header.Get("X-Agent-ID"))
		if authorID != "" {
			authorType = "agent"
		}
	}
	if authorID == "" {
		authorID = strings.TrimSpace(input.AuthorID)
	}
	if authorID == "" {
		authorID = "local-user"
		authorType = "system"
	}
	if authorType == "member" && strings.TrimSpace(input.AuthorType) != "" && r.Header.Get("X-Member-ID") == "" {
		authorType = strings.TrimSpace(input.AuthorType)
	}
	mentionValues := append([]string(nil), input.Mentions...)
	mentionValues = append(mentionValues, commentMentions(input.Content)...)
	structuredMentionCount := 0
	// Structured URI mentions are parsed independently of display names. Keep
	// the stable target IDs in the legacy comment projection so old clients can
	// render them, while trigger decisions use the mentions package AST.
	if parsed, parseErr := mentions.Parse(input.Content); parseErr == nil {
		structuredMentionCount = len(parsed.Targets())
		for _, target := range parsed.Targets() {
			mentionValues = appendUniqueString(mentionValues, target.TargetID)
		}
	}
	comment, err := s.Store.CreateComment(domain.Comment{WorkspaceID: workspaceID, TargetType: targetType, TargetID: targetID, ParentID: input.ParentID, AuthorID: authorID, AuthorType: authorType, Content: input.Content, Mentions: mentionValues})
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrNotFound) {
			status = http.StatusConflict
		}
		s.problem(w, r, status, "comment_create_failed", err.Error(), nil)
		return
	}
	s.recordAudit(r, workspaceID, "comment.created", comment.ID, map[string]any{"target_type": targetType, "target_id": targetID, "parent_id": comment.ParentID, "root_id": comment.RootID, "mentions": comment.Mentions})
	if s.Events != nil {
		_ = s.Events.Publish(r.Context(), events.New("comment.created.v1", "comment", comment.ID, tenant(r), workspaceID, 1, map[string]any{"target_type": targetType, "target_id": targetID, "parent_id": comment.ParentID, "root_id": comment.RootID, "content_sha256": hashBytes([]byte(comment.Content)), "content_bytes": len(comment.Content), "mentions": comment.Mentions}))
	}
	followUp := map[string]any{"requested": false, "status": "not_requested"}
	// Plain display-name tokens are render-only. Explicit structured URI
	// mentions (or the legacy JSON mentions field) are the only implicit
	// dispatch request; callers can still opt in explicitly via dispatch=true.
	dispatch := structuredMentionCount > 0 || len(input.Mentions) > 0
	if input.Dispatch != nil {
		dispatch = *input.Dispatch
	}
	var triggerOutcomes []mentions.TriggerOutcome
	if structuredMentionCount > 0 {
		if plan, triggerErr := mentions.ComputeTriggers(r.Context(), mentions.TriggerInput{
			WorkspaceID: workspaceID, CommentID: comment.ID, CommentRevision: 1,
			Content: input.Content, RuntimeHealthy: false, UserCanInvoke: true,
		}); triggerErr == nil {
			triggerOutcomes = plan.Outcomes
		}
	}
	if dispatch && structuredMentionCount == 0 {
		followUp = s.queueCommentFollowUp(r, comment, input.AgentBindingID)
	}
	response := map[string]any{"comment": comment, "follow_up": followUp}
	if triggerOutcomes != nil {
		response["trigger_outcomes"] = triggerOutcomes
	}
	s.writeJSON(w, http.StatusCreated, response)
}

func appendUniqueString(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		if value != "" {
			if _, ok := seen[value]; !ok {
				values = append(values, value)
				seen[value] = struct{}{}
			}
		}
	}
	return values
}

// commentFollowUpRoute exposes status polling and an explicit retry/dispatch
// command. The comment itself remains immutable; execution state lives in the
// durable receipt returned by this endpoint.
func (s *Server) commentFollowUpRoute(w http.ResponseWriter, r *http.Request, commentID string) {
	comment, err := s.Store.GetComment(commentID)
	if err != nil {
		s.problem(w, r, http.StatusNotFound, "comment_not_found", "comment not found", nil)
		return
	}
	if workspaceID := requestWorkspace(r, ""); workspaceID != "" && workspaceID != comment.WorkspaceID {
		s.problem(w, r, http.StatusNotFound, "comment_not_found", "comment not found", nil)
		return
	}
	if r.Method == http.MethodGet {
		followUp, getErr := s.Store.GetCommentFollowUp(comment.ID)
		if errors.Is(getErr, store.ErrNotFound) {
			s.writeJSON(w, http.StatusOK, map[string]any{"comment_id": comment.ID, "status": "not_requested"})
			return
		}
		if getErr != nil {
			s.problem(w, r, http.StatusInternalServerError, "follow_up_status_failed", getErr.Error(), nil)
			return
		}
		followUp = s.refreshCommentFollowUp(r, followUp)
		s.writeJSON(w, http.StatusOK, followUp)
		return
	}
	if r.Method != http.MethodPost {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	var input struct {
		AgentBindingID string `json:"agent_binding_id,omitempty"`
	}
	if err := decodeJSON(r, &input); err != nil && !errors.Is(err, io.EOF) {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	result := s.queueCommentFollowUp(r, comment, input.AgentBindingID)
	s.writeJSON(w, http.StatusAccepted, map[string]any{"comment_id": comment.ID, "follow_up": result})
}

func commentMentions(content string) []string {
	seen := map[string]struct{}{}
	mentions := make([]string, 0)
	for _, raw := range strings.Fields(content) {
		if !strings.HasPrefix(raw, "@") {
			continue
		}
		mention := strings.Trim(raw[1:], " ,.;:!?()[]{}")
		if mention == "" {
			continue
		}
		if _, exists := seen[mention]; exists {
			continue
		}
		seen[mention] = struct{}{}
		mentions = append(mentions, mention)
		if len(mentions) == 32 {
			break
		}
	}
	return mentions
}

func (s *Server) queueCommentFollowUp(r *http.Request, comment domain.Comment, requestedBinding string) map[string]any {
	result := map[string]any{"requested": true, "status": "unavailable"}
	saveReceipt := func(receipt domain.CommentFollowUp) {
		if s.Store == nil {
			return
		}
		if _, err := s.Store.SaveCommentFollowUp(receipt); err != nil && s.Logger != nil {
			s.Logger.Error("persist comment follow-up receipt", "error", err, "comment_id", comment.ID)
		}
	}
	if existing, err := s.Store.GetCommentFollowUp(comment.ID); err == nil && (existing.Status == "started" || existing.Status == "running" || existing.Status == "completed") {
		result["status"] = existing.Status
		result["run_id"] = existing.ProviderRunID
		result["session_id"] = existing.ProviderSessionID
		result["session_reused"] = existing.Mode == "continuation"
		return result
	}
	if s.Harness == nil || s.Provider == nil || s.Store == nil {
		result["reason"] = "execution dependencies are unavailable"
		saveReceipt(domain.CommentFollowUp{CommentID: comment.ID, WorkspaceID: comment.WorkspaceID, TargetType: comment.TargetType, TargetID: comment.TargetID, Status: "unavailable", Reason: result["reason"].(string)})
		return result
	}
	workItem, run, agentID := s.commentExecutionTarget(comment)
	if requestedBinding != "" {
		agentID = strings.TrimSpace(requestedBinding)
	} else if agentID == "" {
		for _, mention := range comment.Mentions {
			if binding, bindingErr := s.Store.GetProviderBinding(strings.TrimSpace(mention)); bindingErr == nil && binding.WorkspaceID == comment.WorkspaceID {
				agentID = binding.ID
				break
			}
		}
	}
	if requestedBinding != "" && workItem.DeveloperAgentBindingID != "" && strings.TrimSpace(requestedBinding) != workItem.DeveloperAgentBindingID {
		result["reason"] = "agent binding does not match the target work item"
		saveReceipt(domain.CommentFollowUp{CommentID: comment.ID, WorkspaceID: comment.WorkspaceID, TargetType: comment.TargetType, TargetID: comment.TargetID, AgentBindingID: strings.TrimSpace(requestedBinding), Status: "rejected", Reason: result["reason"].(string)})
		return result
	}
	if agentID == "" {
		result["reason"] = "no agent binding is available for this target"
		saveReceipt(domain.CommentFollowUp{CommentID: comment.ID, WorkspaceID: comment.WorkspaceID, TargetType: comment.TargetType, TargetID: comment.TargetID, Status: "unavailable", Reason: result["reason"].(string)})
		return result
	}
	sessionID := ""
	if run.ID != "" {
		sessionID = run.SessionID
	}
	if sessionID == "" && workItem.ID != "" {
		sessionID = "session-" + workItem.ID
	}
	if sessionID == "" {
		sessionID = "session-comment-" + comment.TargetID
	}
	workspaceID := comment.WorkspaceID
	if _, err := s.Harness.GetSession(sessionID); errors.Is(err, harness.ErrNotFound) {
		if _, err := s.Harness.EnsureSession(harness.Session{ID: sessionID, TenantID: tenant(r), WorkspaceID: workspaceID}); err != nil {
			result["reason"] = err.Error()
			saveReceipt(domain.CommentFollowUp{CommentID: comment.ID, WorkspaceID: comment.WorkspaceID, TargetType: comment.TargetType, TargetID: comment.TargetID, AgentBindingID: agentID, HarnessSessionID: sessionID, Status: "retrying", Reason: result["reason"].(string)})
			return result
		}
	} else if err != nil {
		result["reason"] = err.Error()
		saveReceipt(domain.CommentFollowUp{CommentID: comment.ID, WorkspaceID: comment.WorkspaceID, TargetType: comment.TargetType, TargetID: comment.TargetID, AgentBindingID: agentID, HarnessSessionID: sessionID, Status: "retrying", Reason: result["reason"].(string)})
		return result
	}
	session, err := s.Harness.GetSession(sessionID)
	if err != nil {
		result["reason"] = err.Error()
		return result
	}
	contextVersion := session.ContextVersion
	if contextVersion < 1 {
		contextVersion = 1
	}
	key := "comment:" + comment.ID
	prompt := s.commentFollowUpPrompt(comment)
	turn, err := s.Harness.AppendTurn(sessionID, harness.Turn{Role: harness.RoleUser, Content: prompt, IdempotencyKey: key, Metadata: map[string]string{"comment_id": comment.ID, "target_type": comment.TargetType, "target_id": comment.TargetID}})
	if err != nil {
		result["reason"] = err.Error()
		saveReceipt(domain.CommentFollowUp{CommentID: comment.ID, WorkspaceID: comment.WorkspaceID, TargetType: comment.TargetType, TargetID: comment.TargetID, AgentBindingID: agentID, HarnessSessionID: sessionID, Status: "retrying", Reason: result["reason"].(string)})
		return result
	}
	if refreshed, refreshErr := s.Harness.GetSession(sessionID); refreshErr == nil && refreshed.ContextVersion > contextVersion {
		contextVersion = refreshed.ContextVersion
	}
	dispatchPrompt, compileErr := s.compiledHarnessPrompt(sessionID, prompt)
	if compileErr != nil {
		result["reason"] = compileErr.Error()
		saveReceipt(domain.CommentFollowUp{CommentID: comment.ID, WorkspaceID: comment.WorkspaceID, TargetType: comment.TargetType, TargetID: comment.TargetID, AgentBindingID: agentID, HarnessSessionID: sessionID, ContextVersion: contextVersion, TurnID: turn.ID, TurnHash: turn.Hash, Status: "retrying", Reason: result["reason"].(string)})
		return result
	}
	if err := s.saveHarnessCheckpoint(sessionID, harness.CheckpointEffectBefore, turn.Hash, contextVersion, nil, nil, "comment follow-up pending"); err != nil {
		result["reason"] = err.Error()
		saveReceipt(domain.CommentFollowUp{CommentID: comment.ID, WorkspaceID: comment.WorkspaceID, TargetType: comment.TargetType, TargetID: comment.TargetID, AgentBindingID: agentID, HarnessSessionID: sessionID, ContextVersion: contextVersion, TurnID: turn.ID, TurnHash: turn.Hash, Status: "retrying", Reason: result["reason"].(string)})
		return result
	}
	workItemID := workItem.ID
	if workItemID == "" {
		workItemID = "comment-" + comment.ID
	}
	issueID := workItem.ProviderIssueID
	command := provider.StartRunCommand{WorkItemID: workItemID, ProviderIssueID: issueID, AgentBindingID: agentID, Input: dispatchPrompt, SessionID: sessionID, ContextID: "context-" + workItemID, ContextVersion: contextVersion, IdempotencyKey: key}
	intent := providerDispatchIntent{Kind: "comment", CommentID: comment.ID, WorkspaceID: comment.WorkspaceID, WorkItemID: workItem.ID, RequirementID: commentRequirementID(s.Store, comment), BugID: commentBugID(s.Store, comment), AgentID: agentID, ProviderIssueID: issueID, HarnessSessionID: sessionID, ContextID: command.ContextID, ContextVersion: contextVersion, TurnHash: turn.Hash, Command: command}
	if workItem.ID != "" {
		if provenance, found := s.Store.FindProvenance(workItem.ID); found && workItem.ProviderIssueID != "" && provenance.ProviderSessionID != "" && provenance.ProviderWorkDir != "" {
			intent.ProviderIssueID = workItem.ProviderIssueID
			intent.Continuation = &provider.ContinuationCommand{IssueID: workItem.ProviderIssueID, AgentID: agentID, Input: dispatchPrompt, ExpectedSessionID: provenance.ProviderSessionID, ExpectedWorkDir: provenance.ProviderWorkDir, IdempotencyKey: key}
		}
	}
	event, claimed, err := s.Harness.EnqueueAndClaimOutbox(sessionID, key, intent, providerDispatchOwner, dispatchLeaseTTL(), time.Now().UTC())
	if err != nil {
		result["reason"] = err.Error()
		saveReceipt(domain.CommentFollowUp{CommentID: comment.ID, WorkspaceID: comment.WorkspaceID, TargetType: comment.TargetType, TargetID: comment.TargetID, AgentBindingID: agentID, HarnessSessionID: sessionID, ContextVersion: contextVersion, TurnID: turn.ID, TurnHash: turn.Hash, Status: "retrying", Reason: result["reason"].(string)})
		return result
	}
	result["outbox_id"] = event.ID
	receipt := domain.CommentFollowUp{CommentID: comment.ID, WorkspaceID: comment.WorkspaceID, TargetType: comment.TargetType, TargetID: comment.TargetID, AgentBindingID: agentID, HarnessSessionID: sessionID, ContextVersion: contextVersion, TurnID: turn.ID, TurnHash: turn.Hash, OutboxID: event.ID, Status: "dispatching", Mode: "continuation"}
	if intent.Continuation == nil {
		receipt.Mode = "new_run"
	}
	saveReceipt(receipt)
	if !claimed {
		result["status"] = "queued"
		return result
	}
	if err := s.processCommentDispatchIntent(r.Context(), event, intent); err != nil {
		_ = s.Harness.NackOutbox(sessionID, event.ID, providerDispatchOwner, time.Now().UTC().Add(time.Second))
		result["status"] = "retrying"
		result["reason"] = err.Error()
		receipt.Status, receipt.Reason, receipt.Attempts = "retrying", err.Error(), receipt.Attempts+1
		saveReceipt(receipt)
		return result
	}
	if err := s.Harness.AckOutbox(sessionID, event.ID, providerDispatchOwner, time.Now().UTC()); err != nil {
		result["status"] = "retrying"
		result["reason"] = err.Error()
		receipt.Status, receipt.Reason, receipt.Attempts = "retrying", err.Error(), receipt.Attempts+1
		saveReceipt(receipt)
		return result
	}
	result["status"] = "started"
	if provenance, found := s.Store.FindProvenance(workItemID); found {
		result["run_id"] = provenance.ProviderTaskID
		result["session_id"] = provenance.ProviderSessionID
		result["session_reused"] = intent.Continuation != nil
		receipt.Status, receipt.ProviderRunID, receipt.ProviderSessionID, receipt.ProviderWorkDir, receipt.Attempts = "started", provenance.ProviderTaskID, provenance.ProviderSessionID, provenance.ProviderWorkDir, receipt.Attempts+1
		saveReceipt(receipt)
	}
	return result
}

func (s *Server) commentFollowUpPrompt(comment domain.Comment) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Follow-up on %s %s, comment %s (thread %s).\n\nThread:\n", comment.TargetType, comment.TargetID, comment.ID, comment.RootID)
	cursor := ""
	for {
		items, next := s.Store.ListComments(comment.WorkspaceID, comment.TargetType, comment.TargetID, cursor, 250)
		for _, item := range items {
			if item.RootID == comment.RootID {
				fmt.Fprintf(&builder, "- [%s:%s] %s\n", item.AuthorType, item.AuthorID, item.Content)
			}
		}
		if next == "" || next == cursor {
			break
		}
		cursor = next
	}
	return strings.TrimSpace(builder.String())
}

func (s *Server) refreshCommentFollowUp(r *http.Request, followUp domain.CommentFollowUp) domain.CommentFollowUp {
	if s == nil || s.Provider == nil || strings.TrimSpace(followUp.ProviderRunID) == "" {
		return followUp
	}
	snapshot, err := s.Provider.GetRun(r.Context(), followUp.ProviderRunID)
	if err != nil {
		return followUp
	}
	status := followUp.Status
	switch snapshot.Status {
	case "completed":
		status = "completed"
	case "failed":
		status = "failed"
	case "cancelled":
		status = "cancelled"
	case "timed_out":
		status = "timed_out"
	}
	if status == followUp.Status && snapshot.Error == "" {
		return followUp
	}
	followUp.Status = status
	if snapshot.Error != "" {
		followUp.Reason = snapshot.Error
	}
	if saved, saveErr := s.Store.SaveCommentFollowUp(followUp); saveErr == nil {
		return saved
	}
	return followUp
}

func (s *Server) commentExecutionTarget(comment domain.Comment) (domain.WorkItem, domain.PipelineRun, string) {
	var empty domain.WorkItem
	var run domain.PipelineRun
	agentID := ""
	if comment.TargetType == "requirement" {
		pipelines := s.Store.ListPipelines(comment.WorkspaceID, comment.TargetID)
		for i := len(pipelines) - 1; i >= 0; i-- {
			candidate := pipelines[i]
			if candidate.Status != domain.PipelineCompleted && candidate.Status != domain.PipelineFailed {
				run = candidate
				break
			}
		}
		if run.ID != "" {
			agentID = run.ActiveAgentID
			if run.PipelineWorkItemID != "" {
				if item, err := s.Store.GetWorkItem(run.PipelineWorkItemID); err == nil {
					return item, run, firstNonEmpty(agentID, item.DeveloperAgentBindingID)
				}
			}
		}
		items := s.Store.ListWorkItems(comment.TargetID)
		for _, item := range items {
			if item.Role == "developer" || item.DeveloperAgentBindingID != "" {
				return item, run, firstNonEmpty(agentID, item.DeveloperAgentBindingID)
			}
		}
	} else if comment.TargetType == "bug" {
		if bug, err := s.Store.GetBug(comment.TargetID); err == nil {
			agentID = bug.AssigneeMemberID
			if bug.WorkItemID != "" {
				if item, itemErr := s.Store.GetWorkItem(bug.WorkItemID); itemErr == nil {
					return item, run, firstNonEmpty(item.DeveloperAgentBindingID, agentID)
				}
			}
		}
	}
	return empty, run, agentID
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func commentRequirementID(s *store.Memory, comment domain.Comment) string {
	if comment.TargetType == "requirement" {
		return comment.TargetID
	}
	if bug, err := s.GetBug(comment.TargetID); err == nil {
		return bug.RequirementID
	}
	return ""
}

func commentBugID(s *store.Memory, comment domain.Comment) string {
	if comment.TargetType == "bug" {
		return comment.TargetID
	}
	return ""
}
