package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/compat"
	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/harness"
	"github.com/adro-project/adro/internal/mentions"
	"github.com/adro-project/adro/internal/orchestration"
	"github.com/adro-project/adro/internal/provider"
	"github.com/adro-project/adro/internal/store"
)

type commentMutation struct {
	Content          string    `json:"content"`
	ExpectedRevision int64     `json:"expected_revision,omitempty"`
	ParentID         string    `json:"parent_id,omitempty"`
	AuthorID         string    `json:"author_id,omitempty"`
	AuthorType       string    `json:"author_type,omitempty"`
	Mentions         []string  `json:"mentions,omitempty"`
	AttachmentIDs    *[]string `json:"attachment_ids,omitempty"`
	Dispatch         *bool     `json:"dispatch,omitempty"`
	AgentBindingID   string    `json:"agent_binding_id,omitempty"`
}

// computeCommentTriggers resolves the structured AST against the current
// workspace roster before applying the pure trigger calculator. This keeps
// preview/create/edit/retry on one authorization and routing path and avoids
// treating an arbitrary UUID in comment text as an invokable agent.
func (s *Server) computeCommentTriggers(r *http.Request, comment domain.Comment) (mentions.TriggerPlan, error) {
	parsed, err := mentions.Parse(comment.Content)
	if err != nil {
		return mentions.TriggerPlan{}, err
	}
	targets := make([]mentions.Target, 0, len(parsed.Targets()))
	for _, m := range parsed.Targets() {
		if m.TargetType == mentions.TargetAll || m.TargetType == mentions.TargetMember || m.TargetType == mentions.TargetIssue {
			continue
		}
		if s.Orchestration == nil {
			targets = append(targets, mentions.Target{Type: m.TargetType, ID: m.TargetID, WorkspaceID: comment.WorkspaceID})
			continue
		}
		switch m.TargetType {
		case mentions.TargetAgent:
			a, getErr := s.Orchestration.GetAgent(comment.WorkspaceID, m.TargetID, 0)
			if getErr == nil {
				targets = append(targets, mentions.Target{Type: m.TargetType, ID: m.TargetID, WorkspaceID: a.WorkspaceID, Active: a.Status == orchestration.AgentActive, Version: a.Revision, CanInvoke: a.Status == orchestration.AgentActive})
			}
		case mentions.TargetSquad:
			sq, getErr := s.Orchestration.GetSquad(comment.WorkspaceID, m.TargetID, 0)
			if getErr == nil {
				leader := ""
				for _, member := range sq.Members {
					if member.Leader {
						leader = member.AgentID
						break
					}
				}
				targets = append(targets, mentions.Target{Type: m.TargetType, ID: m.TargetID, WorkspaceID: sq.WorkspaceID, Active: sq.Status == orchestration.SquadPublished, Version: sq.PublishedVersion, LeaderID: leader, CanInvoke: sq.Status == orchestration.SquadPublished && leader != ""})
			}
		}
	}
	runtimeHealthy := false
	if s.Provider != nil {
		if health, healthErr := s.Provider.Health(r.Context()); healthErr == nil && health.Healthy {
			runtimeHealthy = true
		}
	}
	userCanInvoke := !authRequired() || authorizedMachine(r)
	if user, ok := s.authenticateUser(r); ok {
		userCanInvoke = user.Can("agents") || user.Can("executions")
	}
	pending := make([]mentions.PendingTask, 0)
	for _, followUp := range s.Store.ListCommentFollowUps(comment.ID) {
		if followUp.Status == "queued" || followUp.Status == "running" || followUp.Status == "pending" {
			pending = append(pending, mentions.PendingTask{DedupeKey: followUp.DedupeKey, TargetType: mentions.TargetType(followUp.DispatchTargetType), TargetID: followUp.DispatchTargetID})
		}
	}
	return mentions.ComputeTriggers(r.Context(), mentions.TriggerInput{WorkspaceID: comment.WorkspaceID, RequirementID: comment.TargetID, CommentID: comment.ID, CommentRevision: comment.Revision, Content: comment.Content, ParentThreadID: comment.RootID, EditingCommentID: comment.ID, Targets: targets, Pending: pending, UserCanInvoke: userCanInvoke, RuntimeHealthy: runtimeHealthy})
}

func (s *Server) commentEditRoute(w http.ResponseWriter, r *http.Request, commentID string) {
	if r.Method != http.MethodPatch {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "PATCH is required", nil)
		return
	}
	var in commentMutation
	if err := decodeJSON(r, &in); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	old, err := s.Store.GetComment(commentID)
	if err != nil {
		s.problem(w, r, http.StatusNotFound, "comment_not_found", err.Error(), nil)
		return
	}
	if ws := requestWorkspace(r, ""); ws != "" && ws != old.WorkspaceID {
		s.problem(w, r, http.StatusNotFound, "comment_not_found", "comment not found", nil)
		return
	}
	oldFollowUps := s.Store.ListCommentFollowUps(old.ID)
	parsed, parseErr := mentions.Parse(in.Content)
	mentionsList := append([]string(nil), in.Mentions...)
	if parseErr == nil {
		for _, target := range parsed.Targets() {
			mentionsList = appendUniqueString(mentionsList, target.TargetID)
		}
	}
	editorID, editorType := commentActor(r)
	updated, err := s.Store.UpdateComment(commentID, in.ExpectedRevision, in.Content, mentionsList, in.AttachmentIDs, editorID, editorType)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		s.problem(w, r, status, "comment_edit_failed", err.Error(), nil)
		return
	}
	// Attachment uploads are committed after the comment exists because the
	// attachment owner must already be a persisted comment. They create a new
	// immutable revision, but must not cancel and redispatch an unchanged body.
	triggerMaterialChanged := old.Content != updated.Content
	if triggerMaterialChanged {
		// Editing a comment invalidates all trigger receipts derived from the
		// previous content. Cancel provider runs before recomputing the new AST;
		// a provider that already reached a terminal state remains immutable and
		// its receipt merge rules preserve that terminal result.
		for _, followUp := range oldFollowUps {
			cancelErr := error(nil)
			if s.Provider != nil && strings.TrimSpace(followUp.ProviderRunID) != "" {
				cancelErr = s.Provider.CancelRun(r.Context(), followUp.ProviderRunID)
			}
			if cancelErr == nil {
				followUp.Status = "cancelled"
				followUp.Reason = "comment content edited"
			} else {
				followUp.Status = "cancel_pending"
				followUp.Reason = "comment content edited; provider cancellation pending: " + cancelErr.Error()
			}
			_, _ = s.Store.SaveCommentFollowUp(followUp)
		}
	}
	var mentionFollowUps []map[string]any
	if plan, calcErr := s.computeCommentTriggers(r, updated); calcErr == nil {
		s.triggerMu.Lock()
		s.triggerOutcomes[updated.ID] = append([]mentions.TriggerOutcome(nil), plan.Outcomes...)
		s.triggerMu.Unlock()
		updated.TriggerOutcomes = make([]domain.CommentTriggerOutcome, 0, len(plan.Outcomes))
		for _, outcome := range plan.Outcomes {
			updated.TriggerOutcomes = append(updated.TriggerOutcomes, domain.CommentTriggerOutcome{TargetType: string(outcome.TargetType), TargetID: outcome.TargetID, Status: string(outcome.Status), ReasonCode: outcome.ReasonCode, Reason: outcome.Reason, AuthoritySnapshot: outcome.AuthoritySnapshot, DedupeKey: outcome.DedupeKey, SourceCommentID: outcome.SourceCommentID, ParentTaskID: outcome.ParentTaskID})
		}
		if saved, saveErr := s.Store.SetCommentTriggerOutcomes(updated.ID, updated.Revision, updated.TriggerOutcomes); saveErr == nil {
			updated = saved
		}
		if triggerMaterialChanged {
			mentionFollowUps = s.dispatchStructuredMentions(r, updated, plan.Outcomes)
		}
	} else if parseErr != nil {
		outcome := invalidMentionOutcome(updated, parseErr)
		if saved, saveErr := s.Store.SetCommentTriggerOutcomes(updated.ID, updated.Revision, []domain.CommentTriggerOutcome{outcome}); saveErr == nil {
			updated = saved
		}
	}
	s.recordAudit(r, updated.WorkspaceID, "comment.edited", updated.ID, map[string]any{"previous_revision": old.Revision, "revision": updated.Revision, "attachment_ids": updated.AttachmentIDs})
	if s.Events != nil {
		_ = s.Events.Publish(r.Context(), events.NewWithContext(r.Context(), "comment.edited.v1", "comment", updated.ID, tenant(r), updated.WorkspaceID, updated.Revision, map[string]any{
			"previous_revision": old.Revision, "revision": updated.Revision,
			"content_sha256": hashBytes([]byte(updated.Content)), "content_bytes": len(updated.Content),
			"attachment_ids": updated.AttachmentIDs, "trigger_material_changed": triggerMaterialChanged,
		}))
	}
	response := map[string]any{"comment": updated, "previous_revision": old.Revision, "mention_follow_ups": mentionFollowUps}
	if parseErr != nil && len(updated.TriggerOutcomes) > 0 {
		response["trigger_outcomes"] = []mentions.TriggerOutcome{toMentionOutcome(updated.TriggerOutcomes[0])}
	}
	s.writeJSON(w, http.StatusOK, response)
}

func commentActor(r *http.Request) (string, string) {
	if id := strings.TrimSpace(r.Header.Get("X-Member-ID")); id != "" {
		return id, "member"
	}
	if id := strings.TrimSpace(r.Header.Get("X-Agent-ID")); id != "" {
		return id, "agent"
	}
	return "local-user", "system"
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (s *Server) commentRevisionsRoute(w http.ResponseWriter, r *http.Request, commentID string) {
	if r.Method != http.MethodGet {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "GET is required", nil)
		return
	}
	comment, err := s.Store.GetComment(commentID)
	if err != nil || requestWorkspace(r, comment.WorkspaceID) != comment.WorkspaceID {
		s.problem(w, r, http.StatusNotFound, "comment_not_found", "comment not found", nil)
		return
	}
	items, err := s.Store.ListCommentRevisions(commentID)
	if err != nil {
		s.problem(w, r, http.StatusNotFound, "comment_not_found", "comment not found", nil)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"comment_id": commentID, "items": items})
}

func (s *Server) commentTriggerOutcomesRoute(w http.ResponseWriter, r *http.Request, commentID string) {
	if r.Method != http.MethodGet {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "GET is required", nil)
		return
	}
	comment, err := s.Store.GetComment(commentID)
	if err != nil {
		s.problem(w, r, http.StatusNotFound, "comment_not_found", err.Error(), nil)
		return
	}
	if ws := requestWorkspace(r, ""); ws != "" && ws != comment.WorkspaceID {
		s.problem(w, r, http.StatusNotFound, "comment_not_found", "comment not found", nil)
		return
	}
	s.triggerMu.RLock()
	items := append([]mentions.TriggerOutcome(nil), s.triggerOutcomes[commentID]...)
	s.triggerMu.RUnlock()
	if len(items) == 0 {
		for _, outcome := range comment.TriggerOutcomes {
			items = append(items, mentions.TriggerOutcome{TargetType: mentions.TargetType(outcome.TargetType), TargetID: outcome.TargetID, Status: mentions.OutcomeStatus(outcome.Status), ReasonCode: outcome.ReasonCode, Reason: outcome.Reason, AuthoritySnapshot: outcome.AuthoritySnapshot, DedupeKey: outcome.DedupeKey, SourceCommentID: outcome.SourceCommentID, ParentTaskID: outcome.ParentTaskID})
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"comment_id": commentID, "trigger_outcomes": items})
}

func (s *Server) commentTriggerRetryRoute(w http.ResponseWriter, r *http.Request, commentID string) {
	if r.Method != http.MethodPost {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "POST is required", nil)
		return
	}
	comment, err := s.Store.GetComment(commentID)
	if err != nil {
		s.problem(w, r, http.StatusNotFound, "comment_not_found", err.Error(), nil)
		return
	}
	if ws := requestWorkspace(r, ""); ws != "" && ws != comment.WorkspaceID {
		s.problem(w, r, http.StatusNotFound, "comment_not_found", "comment not found", nil)
		return
	}
	plan, calcErr := s.computeCommentTriggers(r, comment)
	if calcErr != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "trigger_retry_failed", calcErr.Error(), nil)
		return
	}
	s.triggerMu.Lock()
	s.triggerOutcomes[commentID] = append([]mentions.TriggerOutcome(nil), plan.Outcomes...)
	s.triggerMu.Unlock()
	converted := make([]domain.CommentTriggerOutcome, 0, len(plan.Outcomes))
	for _, outcome := range plan.Outcomes {
		converted = append(converted, domain.CommentTriggerOutcome{TargetType: string(outcome.TargetType), TargetID: outcome.TargetID, Status: string(outcome.Status), ReasonCode: outcome.ReasonCode, Reason: outcome.Reason, AuthoritySnapshot: outcome.AuthoritySnapshot, DedupeKey: outcome.DedupeKey, SourceCommentID: outcome.SourceCommentID, ParentTaskID: outcome.ParentTaskID})
	}
	_, _ = s.Store.SetCommentTriggerOutcomes(commentID, comment.Revision, converted)
	followUps := s.dispatchStructuredMentions(r, comment, plan.Outcomes)
	s.writeJSON(w, http.StatusAccepted, map[string]any{"comment_id": commentID, "trigger_outcomes": plan.Outcomes, "mention_follow_ups": followUps})
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
		// Body identity fields are display-only input. They are never trusted as
		// the actor because a caller could otherwise impersonate an Agent/member
		// and create an audited trigger under another identity. Prefer the
		// authenticated principal when available; the optional local profile uses
		// a stable system actor when no identity source is configured.
		if user, authenticated := s.authenticateUser(r); authenticated {
			authorID = strings.TrimSpace(user.ID)
			if authorID == "" {
				authorID = strings.TrimSpace(user.Username)
			}
		}
	}
	if authorID == "" {
		authorID = "local-user"
		authorType = "system"
	}
	// AuthorType follows the trusted header/principal only. A body value is not
	// allowed to turn a member comment into an Agent or system comment.
	mentionValues := append([]string(nil), input.Mentions...)
	structuredMentionCount := 0
	parsed, parseErr := mentions.Parse(input.Content)
	// Structured URI mentions are parsed independently of display names. Keep
	// the stable target IDs in the legacy comment projection so old clients can
	// render them, while trigger decisions use the mentions package AST.
	if parseErr == nil {
		structuredMentionCount = len(parsed.InvocationTargets())
		for _, target := range parsed.Targets() {
			mentionValues = appendUniqueString(mentionValues, target.TargetID)
		}
	}
	attachmentIDs := []string(nil)
	if input.AttachmentIDs != nil {
		attachmentIDs = append([]string(nil), (*input.AttachmentIDs)...)
	}
	comment, err := s.Store.CreateComment(domain.Comment{WorkspaceID: workspaceID, TargetType: targetType, TargetID: targetID, ParentID: input.ParentID, AuthorID: authorID, AuthorType: authorType, Content: input.Content, Mentions: mentionValues, AttachmentIDs: attachmentIDs})
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
		_ = s.Events.Publish(r.Context(), events.NewWithContext(r.Context(), "comment.created.v1", "comment", comment.ID, tenant(r), workspaceID, 1, map[string]any{"target_type": targetType, "target_id": targetID, "parent_id": comment.ParentID, "root_id": comment.RootID, "content_sha256": hashBytes([]byte(comment.Content)), "content_bytes": len(comment.Content), "mentions": comment.Mentions}))
	}
	followUp := map[string]any{"requested": false, "status": "not_requested"}
	// Plain display-name tokens and the legacy JSON mentions field are render-
	// only. Only structured URI targets implicitly invoke work; callers may use
	// dispatch=true with an explicit agent_binding_id or the target's existing
	// work-item route, but no display name is ever resolved into a route.
	dispatch := structuredMentionCount > 0
	if input.Dispatch != nil {
		dispatch = *input.Dispatch
	}
	if parseErr != nil {
		// An invalid structured URI is retained as a normal comment with a
		// blocked outcome; it must never fall through to the legacy assignee
		// dispatch path.
		dispatch = false
	}
	var triggerOutcomes []mentions.TriggerOutcome
	if parseErr != nil {
		outcome := invalidMentionOutcome(comment, parseErr)
		if saved, saveErr := s.Store.SetCommentTriggerOutcomes(comment.ID, comment.Revision, []domain.CommentTriggerOutcome{outcome}); saveErr == nil {
			comment = saved
		}
		triggerOutcomes = []mentions.TriggerOutcome{toMentionOutcome(outcome)}
	} else if structuredMentionCount > 0 {
		if plan, triggerErr := s.computeCommentTriggers(r, comment); triggerErr == nil {
			triggerOutcomes = plan.Outcomes
			s.triggerMu.Lock()
			s.triggerOutcomes[comment.ID] = append([]mentions.TriggerOutcome(nil), triggerOutcomes...)
			s.triggerMu.Unlock()
			comment.TriggerOutcomes = make([]domain.CommentTriggerOutcome, 0, len(triggerOutcomes))
			for _, outcome := range triggerOutcomes {
				comment.TriggerOutcomes = append(comment.TriggerOutcomes, domain.CommentTriggerOutcome{TargetType: string(outcome.TargetType), TargetID: outcome.TargetID, Status: string(outcome.Status), ReasonCode: outcome.ReasonCode, Reason: outcome.Reason, AuthoritySnapshot: outcome.AuthoritySnapshot, DedupeKey: outcome.DedupeKey, SourceCommentID: outcome.SourceCommentID, ParentTaskID: outcome.ParentTaskID})
			}
			if saved, saveErr := s.Store.SetCommentTriggerOutcomes(comment.ID, comment.Revision, comment.TriggerOutcomes); saveErr == nil {
				comment = saved
			}
			if dispatch {
				followUp["mention_follow_ups"] = s.dispatchStructuredMentions(r, comment, triggerOutcomes)
			}
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

// dispatchStructuredMentions turns queued roster outcomes into the same
// durable follow-up receipt used by legacy comment dispatch. Squad targets are
// routed through their published leader; @all expands to active workspace
// agents. Blocked/deferred/coalesced outcomes remain auditable without a
// provider side effect.
func (s *Server) dispatchStructuredMentions(r *http.Request, comment domain.Comment, outcomes []mentions.TriggerOutcome) []map[string]any {
	result := make([]map[string]any, 0)
	for _, outcome := range outcomes {
		if outcome.Status != mentions.StatusQueued {
			continue
		}
		if outcome.TargetType == mentions.TargetAll {
			// @all is an explicit fan-out. Resolve the current active roster at
			// dispatch time and retain one receipt per expanded target in the
			// response; the original @all outcome remains the audit anchor.
			if s.Orchestration == nil {
				continue
			}
			for _, agent := range s.Orchestration.ListAgents(comment.WorkspaceID, orchestration.AgentActive) {
				followUp := s.queueCommentFollowUpForTarget(r, comment, "agent", agent.ID, outcome.DedupeKey+":"+agent.ID)
				result = append(result, map[string]any{"target_type": string(mentions.TargetAgent), "target_id": agent.ID, "dedupe_key": outcome.DedupeKey + ":" + agent.ID, "follow_up": followUp})
			}
			continue
		}
		targetID := outcome.TargetID
		dispatchType := string(outcome.TargetType)
		bindingID := targetID
		if outcome.TargetType == mentions.TargetSquad && s.Orchestration != nil {
			if squad, err := s.Orchestration.GetSquad(comment.WorkspaceID, targetID, 0); err == nil {
				for _, member := range squad.Members {
					if member.Leader {
						bindingID = member.AgentID
						break
					}
				}
			}
		}
		if outcome.TargetType == mentions.TargetAll && s.Orchestration != nil {
			for _, agent := range s.Orchestration.ListAgents(comment.WorkspaceID, orchestration.AgentActive) {
				result = append(result, s.queueMentionFollowUp(r, comment, agent.ID, outcome.DedupeKey)...)
			}
			continue
		}
		result = append(result, s.queueStructuredTargetFollowUp(r, comment, dispatchType, targetID, bindingID, outcome.DedupeKey)...)
	}
	return result
}

func (s *Server) queueMentionFollowUp(r *http.Request, comment domain.Comment, targetID, dedupeKey string) []map[string]any {
	result := s.queueCommentFollowUpForTargetWithBinding(r, comment, string(mentions.TargetAgent), targetID, targetID, dedupeKey+":"+targetID)
	return []map[string]any{{"target_id": targetID, "dedupe_key": dedupeKey, "follow_up": result}}
}

func (s *Server) queueStructuredTargetFollowUp(r *http.Request, comment domain.Comment, dispatchType, dispatchID, bindingID, dedupeKey string) []map[string]any {
	result := s.queueCommentFollowUpForTargetWithBinding(r, comment, dispatchType, dispatchID, bindingID, dedupeKey+":"+dispatchID)
	return []map[string]any{{"target_type": dispatchType, "target_id": dispatchID, "dedupe_key": dedupeKey, "follow_up": result}}
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

func invalidMentionOutcome(comment domain.Comment, err error) domain.CommentTriggerOutcome {
	return domain.CommentTriggerOutcome{TargetType: "unknown", TargetID: "", Status: string(mentions.StatusBlocked), ReasonCode: "invalid_mention_syntax", Reason: err.Error(), DedupeKey: comment.ID + ":invalid:" + fmt.Sprint(comment.Revision), SourceCommentID: comment.ID}
}

func toMentionOutcome(outcome domain.CommentTriggerOutcome) mentions.TriggerOutcome {
	return mentions.TriggerOutcome{TargetType: mentions.TargetType(outcome.TargetType), TargetID: outcome.TargetID, Status: mentions.OutcomeStatus(outcome.Status), ReasonCode: outcome.ReasonCode, Reason: outcome.Reason, AuthoritySnapshot: outcome.AuthoritySnapshot, DedupeKey: outcome.DedupeKey, SourceCommentID: outcome.SourceCommentID, ParentTaskID: outcome.ParentTaskID}
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
		all := s.Store.ListCommentFollowUps(comment.ID)
		if len(all) > 0 {
			for i := range all {
				all[i] = s.refreshCommentFollowUp(r, all[i])
			}
		}
		followUp, getErr := s.Store.GetCommentFollowUp(comment.ID)
		if errors.Is(getErr, store.ErrNotFound) {
			s.writeJSON(w, http.StatusOK, map[string]any{"comment_id": comment.ID, "status": "not_requested", "follow_ups": all})
			return
		}
		if getErr != nil {
			s.problem(w, r, http.StatusInternalServerError, "follow_up_status_failed", getErr.Error(), nil)
			return
		}
		followUp = s.refreshCommentFollowUp(r, followUp)
		response := map[string]any{"comment_id": comment.ID, "follow_up": followUp, "follow_ups": all}
		s.writeJSON(w, http.StatusOK, response)
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

func (s *Server) queueCommentFollowUp(r *http.Request, comment domain.Comment, requestedBinding string) map[string]any {
	return s.queueCommentFollowUpForTargetWithBinding(r, comment, "agent", requestedBinding, requestedBinding, fmt.Sprintf("%s:legacy:%d", comment.ID, comment.Revision))
}

func (s *Server) queueCommentFollowUpForTarget(r *http.Request, comment domain.Comment, dispatchTargetType, dispatchTargetID, dedupeKey string) map[string]any {
	return s.queueCommentFollowUpForTargetWithBinding(r, comment, dispatchTargetType, dispatchTargetID, dispatchTargetID, dedupeKey)
}

func (s *Server) queueCommentFollowUpForTargetWithBinding(r *http.Request, comment domain.Comment, dispatchTargetType, dispatchTargetID, requestedAgentBinding, dedupeKey string) map[string]any {
	if strings.TrimSpace(dispatchTargetType) == "" {
		dispatchTargetType = "agent"
	}
	requestedBinding := strings.TrimSpace(requestedAgentBinding)
	result := map[string]any{"requested": true, "status": "unavailable"}
	saveReceipt := func(receipt domain.CommentFollowUp) {
		if s.Store == nil {
			return
		}
		receipt.DispatchTargetType = dispatchTargetType
		receipt.DispatchTargetID = dispatchTargetID
		receipt.DedupeKey = dedupeKey
		receipt.CommentRevision = comment.Revision
		if _, err := s.Store.SaveCommentFollowUp(receipt); err != nil && s.Logger != nil {
			s.Logger.Error("persist comment follow-up receipt", "error", err, "comment_id", comment.ID)
		}
	}
	if existing, err := s.Store.GetCommentFollowUpForTarget(comment.ID, dispatchTargetType, dispatchTargetID); err == nil && (existing.Status == "started" || existing.Status == "running" || existing.Status == "completed" || existing.Status == "dispatching" || existing.Status == "queued") {
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
	explicitStructured := strings.Contains(comment.Content, "mention://")
	if requestedBinding != "" {
		agentID = strings.TrimSpace(requestedBinding)
		if s.Orchestration != nil {
			if definition, definitionErr := s.Orchestration.GetAgent(comment.WorkspaceID, agentID, 0); definitionErr == nil {
				if strings.TrimSpace(definition.ExecutorBinding.ProviderID) != "" {
					agentID = definition.ExecutorBinding.ProviderID
				}
			}
		}
	}
	if requestedBinding != "" && !explicitStructured && workItem.DeveloperAgentBindingID != "" && strings.TrimSpace(requestedBinding) != workItem.DeveloperAgentBindingID {
		result["reason"] = "agent binding does not match the target work item"
		saveReceipt(domain.CommentFollowUp{CommentID: comment.ID, WorkspaceID: comment.WorkspaceID, TargetType: comment.TargetType, TargetID: comment.TargetID, AgentBindingID: strings.TrimSpace(requestedBinding), Status: "rejected", Reason: result["reason"].(string)})
		return result
	}
	if agentID == "" {
		result["reason"] = "no agent binding is available for this target"
		saveReceipt(domain.CommentFollowUp{CommentID: comment.ID, WorkspaceID: comment.WorkspaceID, TargetType: comment.TargetType, TargetID: comment.TargetID, Status: "unavailable", Reason: result["reason"].(string)})
		return result
	}
	if strings.TrimSpace(dispatchTargetID) == "" {
		dispatchTargetID = agentID
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
	key := strings.TrimSpace(dedupeKey)
	if key == "" {
		key = fmt.Sprintf("comment:%s:revision:%d:%s:%s", comment.ID, comment.Revision, dispatchTargetType, dispatchTargetID)
	}
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
	contextEnvelope, compileErr := s.compiledHarnessEnvelope(sessionID)
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
	command := provider.StartRunCommand{WorkItemID: workItemID, ProviderIssueID: issueID, AgentBindingID: agentID, Input: dispatchPrompt, SessionID: sessionID, ContextID: "context-" + workItemID, ContextVersion: contextVersion, ContextEnvelope: contextEnvelope, LegacyAdapterVersion: "comment-v1", IdempotencyKey: key}
	graphScope, scopeErr := compat.CommentDispatchScope(comment.ID, dispatchTargetType+":"+dispatchTargetID, key)
	if scopeErr != nil {
		result["reason"] = scopeErr.Error()
		saveReceipt(domain.CommentFollowUp{CommentID: comment.ID, WorkspaceID: comment.WorkspaceID, TargetType: comment.TargetType, TargetID: comment.TargetID, AgentBindingID: agentID, HarnessSessionID: sessionID, ContextVersion: contextVersion, TurnID: turn.ID, TurnHash: turn.Hash, Status: "rejected", Reason: result["reason"].(string)})
		return result
	}
	command.PlanID, command.NodeID, command.AttemptID = graphScope.PlanID, graphScope.NodeID, graphScope.AttemptID
	command = command.WithTraceContext(r.Context())
	intent := providerDispatchIntent{Kind: "comment", CommentID: comment.ID, WorkspaceID: comment.WorkspaceID, DispatchTargetType: dispatchTargetType, DispatchTargetID: dispatchTargetID, DedupeKey: dedupeKey, WorkItemID: workItem.ID, RequirementID: commentRequirementID(s.Store, comment), BugID: commentBugID(s.Store, comment), AgentID: agentID, ProviderIssueID: issueID, HarnessSessionID: sessionID, ContextID: command.ContextID, ContextVersion: contextVersion, TurnHash: turn.Hash, ContextEnvelope: contextEnvelope, Command: command}
	if workItem.ID != "" {
		if provenance, found := s.Store.FindProvenance(workItem.ID); found && workItem.ProviderIssueID != "" && provenance.ProviderSessionID != "" && provenance.ProviderWorkDir != "" {
			intent.ProviderIssueID = workItem.ProviderIssueID
			continuation := &provider.ContinuationCommand{IssueID: workItem.ProviderIssueID, AgentID: agentID, Input: dispatchPrompt, ExpectedSessionID: provenance.ProviderSessionID, ExpectedWorkDir: provenance.ProviderWorkDir, IdempotencyKey: key, ContextEnvelope: contextEnvelope, LegacyAdapterVersion: "comment-v1"}
			continuation.ExpectedRevision = contextVersion
			continuation.PlanID = command.PlanID
			continuation.NodeID = command.NodeID
			continuation.AttemptID = command.AttemptID
			*continuation = continuation.WithTraceContext(r.Context())
			intent.Continuation = continuation
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
