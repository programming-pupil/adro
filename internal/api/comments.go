package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/harness"
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
	mentions := append([]string(nil), input.Mentions...)
	mentions = append(mentions, commentMentions(input.Content)...)
	comment, err := s.Store.CreateComment(domain.Comment{WorkspaceID: workspaceID, TargetType: targetType, TargetID: targetID, ParentID: input.ParentID, AuthorID: authorID, AuthorType: authorType, Content: input.Content, Mentions: mentions})
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
	dispatch := len(comment.Mentions) > 0
	if input.Dispatch != nil {
		dispatch = *input.Dispatch
	}
	if dispatch {
		followUp = s.queueCommentFollowUp(r, comment, input.AgentBindingID)
	}
	s.writeJSON(w, http.StatusCreated, map[string]any{"comment": comment, "follow_up": followUp})
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
	if s.Harness == nil || s.Provider == nil || s.Store == nil {
		result["reason"] = "execution dependencies are unavailable"
		return result
	}
	workItem, run, agentID := s.commentExecutionTarget(comment)
	if agentID == "" {
		if requestedBinding != "" {
			agentID = strings.TrimSpace(requestedBinding)
		}
	}
	if requestedBinding != "" && workItem.DeveloperAgentBindingID != "" && strings.TrimSpace(requestedBinding) != workItem.DeveloperAgentBindingID {
		result["reason"] = "agent binding does not match the target work item"
		return result
	}
	if agentID == "" {
		result["reason"] = "no agent binding is available for this target"
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
			return result
		}
	} else if err != nil {
		result["reason"] = err.Error()
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
	prompt := fmt.Sprintf("Follow-up on %s %s, comment %s (thread %s):\n\n%s", comment.TargetType, comment.TargetID, comment.ID, comment.RootID, comment.Content)
	turn, err := s.Harness.AppendTurn(sessionID, harness.Turn{Role: harness.RoleUser, Content: prompt, IdempotencyKey: key, Metadata: map[string]string{"comment_id": comment.ID, "target_type": comment.TargetType, "target_id": comment.TargetID}})
	if err != nil {
		result["reason"] = err.Error()
		return result
	}
	if refreshed, refreshErr := s.Harness.GetSession(sessionID); refreshErr == nil && refreshed.ContextVersion > contextVersion {
		contextVersion = refreshed.ContextVersion
	}
	if err := s.saveHarnessCheckpoint(sessionID, harness.CheckpointEffectBefore, turn.Hash, contextVersion, nil, nil, "comment follow-up pending"); err != nil {
		result["reason"] = err.Error()
		return result
	}
	workItemID := workItem.ID
	if workItemID == "" {
		workItemID = "comment-" + comment.ID
	}
	issueID := workItem.ProviderIssueID
	command := provider.StartRunCommand{WorkItemID: workItemID, ProviderIssueID: issueID, AgentBindingID: agentID, Input: prompt, SessionID: sessionID, ContextID: "context-" + workItemID, ContextVersion: contextVersion, IdempotencyKey: key}
	intent := providerDispatchIntent{Kind: "comment", CommentID: comment.ID, WorkspaceID: comment.WorkspaceID, WorkItemID: workItem.ID, RequirementID: commentRequirementID(s.Store, comment), BugID: commentBugID(s.Store, comment), AgentID: agentID, ProviderIssueID: issueID, HarnessSessionID: sessionID, ContextID: command.ContextID, ContextVersion: contextVersion, TurnHash: turn.Hash, Command: command}
	if workItem.ID != "" {
		if provenance, found := s.Store.FindProvenance(workItem.ID); found && workItem.ProviderIssueID != "" && provenance.ProviderSessionID != "" && provenance.ProviderWorkDir != "" {
			intent.ProviderIssueID = workItem.ProviderIssueID
			intent.Continuation = &provider.ContinuationCommand{IssueID: workItem.ProviderIssueID, AgentID: agentID, Input: prompt, ExpectedSessionID: provenance.ProviderSessionID, ExpectedWorkDir: provenance.ProviderWorkDir, IdempotencyKey: key}
		}
	}
	event, claimed, err := s.Harness.EnqueueAndClaimOutbox(sessionID, key, intent, providerDispatchOwner, dispatchLeaseTTL(), time.Now().UTC())
	if err != nil {
		result["reason"] = err.Error()
		return result
	}
	result["outbox_id"] = event.ID
	if !claimed {
		result["status"] = "queued"
		return result
	}
	if err := s.processCommentDispatchIntent(r.Context(), event, intent); err != nil {
		_ = s.Harness.NackOutbox(sessionID, event.ID, providerDispatchOwner, time.Now().UTC().Add(time.Second))
		result["status"] = "retrying"
		result["reason"] = err.Error()
		return result
	}
	if err := s.Harness.AckOutbox(sessionID, event.ID, providerDispatchOwner, time.Now().UTC()); err != nil {
		result["status"] = "retrying"
		result["reason"] = err.Error()
		return result
	}
	result["status"] = "started"
	if provenance, found := s.Store.FindProvenance(workItemID); found {
		result["run_id"] = provenance.ProviderTaskID
		result["session_id"] = provenance.ProviderSessionID
		result["session_reused"] = intent.Continuation != nil
	}
	return result
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
