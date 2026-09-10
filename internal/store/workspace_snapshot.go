package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/adro-project/adro/internal/domain"
)

const WorkspaceSnapshotFormat = "adro.workspace-control.v1"

// WorkspaceSnapshot is the portable control-plane subset. Execution leases,
// provider sessions, credentials, approval tokens, and idempotency responses
// are intentionally absent: they are machine-local authority, not user data.
type WorkspaceSnapshot struct {
	Format           string                              `json:"format"`
	SourceWorkspace  string                              `json:"source_workspace_id"`
	Requirements     []domain.Requirement                `json:"requirements,omitempty"`
	Bugs             []domain.Bug                        `json:"bugs,omitempty"`
	Attachments      []domain.EntityAttachment           `json:"attachments,omitempty"`
	Comments         []domain.Comment                    `json:"comments,omitempty"`
	CommentRevisions map[string][]domain.CommentRevision `json:"comment_revisions,omitempty"`
	CommentFollowUps []domain.CommentFollowUp            `json:"comment_follow_ups,omitempty"`
	Repositories     []domain.Repository                 `json:"repositories,omitempty"`
	TeamWorkspaces   []domain.TeamWorkspace              `json:"team_workspaces,omitempty"`
	MCPServers       []domain.MCPServer                  `json:"mcp_servers,omitempty"`
	Skills           []domain.Skill                      `json:"skills,omitempty"`
	Automations      []domain.Automation                 `json:"automations,omitempty"`
	Bindings         []domain.CapabilityBinding          `json:"bindings,omitempty"`
	ChatSessions     []domain.ChatSession                `json:"chat_sessions,omitempty"`
	ChatMessages     []domain.ChatMessage                `json:"chat_messages,omitempty"`
}

type WorkspaceImportReport struct {
	Digest  string         `json:"digest"`
	DryRun  bool           `json:"dry_run"`
	Replay  bool           `json:"replay"`
	Created map[string]int `json:"created"`
	Skipped map[string]int `json:"skipped"`
}

// ExportWorkspace returns an isolated, deterministic snapshot of durable user
// data. Arbitrary automation payloads are sanitized recursively before they
// cross the portability boundary.
func (m *Memory) ExportWorkspace(workspaceID string) (WorkspaceSnapshot, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return WorkspaceSnapshot{}, errors.New("workspace_id is required")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := WorkspaceSnapshot{Format: WorkspaceSnapshotFormat, SourceWorkspace: workspaceID, CommentRevisions: map[string][]domain.CommentRevision{}}
	requirementIDs, bugIDs, commentIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, item := range m.requirements {
		if item.WorkspaceID == workspaceID {
			out.Requirements = append(out.Requirements, item)
			requirementIDs[item.ID] = true
		}
	}
	for _, item := range m.bugs {
		if item.WorkspaceID == workspaceID {
			out.Bugs = append(out.Bugs, item)
			bugIDs[item.ID] = true
		}
	}
	for _, item := range m.comments {
		if item.WorkspaceID == workspaceID {
			out.Comments = append(out.Comments, cloneComment(item))
			commentIDs[item.ID] = true
		}
	}
	for id := range commentIDs {
		for _, revision := range m.commentRevisions[id] {
			out.CommentRevisions[id] = append(out.CommentRevisions[id], cloneCommentRevision(revision))
		}
	}
	for _, item := range m.commentFollowUps {
		if commentIDs[item.CommentID] {
			out.CommentFollowUps = append(out.CommentFollowUps, item)
		}
	}
	for _, item := range m.attachments {
		if item.WorkspaceID == workspaceID {
			out.Attachments = append(out.Attachments, item)
		}
	}
	for _, item := range m.repositories {
		if item.WorkspaceID == workspaceID {
			item.Metadata = sanitizePortableMap(item.Metadata)
			out.Repositories = append(out.Repositories, item)
		}
	}
	for _, item := range m.teamWorkspaces {
		if item.WorkspaceID == workspaceID {
			item.Policy = sanitizePortableMap(item.Policy)
			out.TeamWorkspaces = append(out.TeamWorkspaces, item)
		}
	}
	for _, item := range m.mcpServers {
		if item.WorkspaceID == workspaceID {
			out.MCPServers = append(out.MCPServers, portableMCPServer(item))
		}
	}
	for _, item := range m.skills {
		if item.WorkspaceID == workspaceID {
			item.Contract = sanitizePortableMap(item.Contract)
			out.Skills = append(out.Skills, item)
		}
	}
	for _, item := range m.automations {
		if item.WorkspaceID == workspaceID {
			item.Trigger = sanitizePortableMap(item.Trigger)
			for i := range item.Nodes {
				item.Nodes[i] = sanitizePortableMap(item.Nodes[i])
			}
			out.Automations = append(out.Automations, item)
		}
	}
	for _, item := range m.bindings {
		if item.WorkspaceID == workspaceID {
			out.Bindings = append(out.Bindings, item)
		}
	}
	for _, item := range m.chatSessions {
		if item.WorkspaceID == workspaceID {
			item.HarnessSessionID = "portable-chat-" + item.ID
			out.ChatSessions = append(out.ChatSessions, item)
		}
	}
	for sessionID, messages := range m.chatMessages {
		if m.chatSessions[sessionID].WorkspaceID != workspaceID {
			continue
		}
		out.ChatMessages = append(out.ChatMessages, messages...)
	}
	_ = requirementIDs
	_ = bugIDs
	sortWorkspaceSnapshot(&out)
	return cloneWorkspaceSnapshot(out), nil
}

// ImportWorkspace validates and stages every collection before one durable
// snapshot replacement. The caller performs cross-store rollback if another
// component fails after this commit.
func (m *Memory) ImportWorkspace(targetWorkspace, digest, policy string, snapshot WorkspaceSnapshot, dryRun bool) (WorkspaceImportReport, error) {
	targetWorkspace = strings.TrimSpace(targetWorkspace)
	digest = strings.TrimSpace(digest)
	policy = strings.ToLower(strings.TrimSpace(policy))
	if targetWorkspace == "" || digest == "" {
		return WorkspaceImportReport{}, errors.New("target workspace and digest are required")
	}
	if snapshot.Format != WorkspaceSnapshotFormat {
		return WorkspaceImportReport{}, fmt.Errorf("unsupported control snapshot format %q", snapshot.Format)
	}
	if policy != "fail" && policy != "skip" && policy != "rename" {
		return WorkspaceImportReport{}, fmt.Errorf("unsupported conflict policy %q", policy)
	}
	report := WorkspaceImportReport{Digest: digest, DryRun: dryRun, Created: map[string]int{}, Skipped: map[string]int{}}
	key := "workspace-import:" + targetWorkspace + ":" + digest
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.idempotency[key]; exists {
		report.Replay = true
		return report, nil
	}
	old := m.snapshotLocked()
	candidate := clonePersistedState(old)
	if err := mergeWorkspaceSnapshot(&candidate, targetWorkspace, policy, snapshot, &report); err != nil {
		return WorkspaceImportReport{}, err
	}
	if dryRun {
		return report, nil
	}
	candidate.Idempotency[key] = json.RawMessage(`{"imported":true}`)
	m.applySnapshotLocked(candidate)
	if err := m.persistLocked(); err != nil {
		m.applySnapshotLocked(old)
		return WorkspaceImportReport{}, fmt.Errorf("persist workspace import: %w", err)
	}
	return report, nil
}

func (m *Memory) Backup(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("backup path is required")
	}
	m.mu.RLock()
	state := m.snapshotLocked()
	m.mu.RUnlock()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (m *Memory) Restore(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode control-plane backup: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.snapshotLocked()
	state.Revision = m.revision
	m.applySnapshotLocked(clonePersistedState(state))
	if err := m.persistLocked(); err != nil {
		m.applySnapshotLocked(old)
		return fmt.Errorf("persist restored control-plane state: %w", err)
	}
	return nil
}

func (m *Memory) snapshotLocked() persistedState {
	state := persistedState{Version: 4, Revision: m.revision, Requirements: m.requirements, Bugs: m.bugs, Attachments: m.attachments, Comments: m.comments, CommentRevisions: m.commentRevisions, CommentFollowUps: m.commentFollowUps, WorkItems: m.workItems, Evidence: m.evidence, Provenance: m.provenance, ProviderBindings: m.providerBindings, ImpactReports: m.impactReports, Idempotency: map[string]json.RawMessage{}, Repositories: m.repositories, TeamWorkspaces: m.teamWorkspaces, Profiles: m.profiles, MCPServers: m.mcpServers, Skills: m.skills, Automations: m.automations, Approvals: m.approvals, Diffs: m.diffs, Migrations: m.migrations, Invocations: m.invocations, Bindings: m.bindings, AutomationRuns: m.automationRuns, Contexts: m.contexts, RepairAttempts: m.repairAttempts, Pipelines: m.pipelines, WorkflowTemplates: m.workflowTemplates, ChatSessions: m.chatSessions, ChatMessages: m.chatMessages}
	for key, value := range m.idempotency {
		data, _ := json.Marshal(value)
		if raw, ok := value.(json.RawMessage); ok {
			data = append([]byte(nil), raw...)
		}
		state.Idempotency[key] = data
	}
	return clonePersistedState(state)
}

func (m *Memory) applySnapshotLocked(state persistedState) {
	m.revision, m.requirements, m.bugs, m.attachments = state.Revision, state.Requirements, state.Bugs, state.Attachments
	m.comments, m.commentRevisions, m.commentFollowUps = state.Comments, state.CommentRevisions, state.CommentFollowUps
	m.workItems, m.evidence, m.provenance, m.providerBindings = state.WorkItems, state.Evidence, state.Provenance, state.ProviderBindings
	m.impactReports, m.repositories, m.teamWorkspaces, m.profiles = state.ImpactReports, state.Repositories, state.TeamWorkspaces, state.Profiles
	m.mcpServers, m.skills, m.automations, m.approvals = state.MCPServers, state.Skills, state.Automations, state.Approvals
	m.diffs, m.migrations, m.invocations, m.bindings = state.Diffs, state.Migrations, state.Invocations, state.Bindings
	m.automationRuns, m.contexts, m.repairAttempts, m.pipelines = state.AutomationRuns, state.Contexts, state.RepairAttempts, state.Pipelines
	m.workflowTemplates, m.chatSessions, m.chatMessages = state.WorkflowTemplates, state.ChatSessions, state.ChatMessages
	m.idempotency = map[string]any{}
	for key, raw := range state.Idempotency {
		m.idempotency[key] = append(json.RawMessage(nil), raw...)
	}
}

func clonePersistedState(state persistedState) persistedState {
	data, _ := json.Marshal(state)
	var clone persistedState
	_ = json.Unmarshal(data, &clone)
	initializePersistedState(&clone)
	return clone
}

func initializePersistedState(s *persistedState) {
	if s.Requirements == nil {
		s.Requirements = map[string]domain.Requirement{}
	}
	if s.Bugs == nil {
		s.Bugs = map[string]domain.Bug{}
	}
	if s.Attachments == nil {
		s.Attachments = map[string]domain.EntityAttachment{}
	}
	if s.Comments == nil {
		s.Comments = map[string]domain.Comment{}
	}
	if s.CommentRevisions == nil {
		s.CommentRevisions = map[string][]domain.CommentRevision{}
	}
	if s.CommentFollowUps == nil {
		s.CommentFollowUps = map[string]domain.CommentFollowUp{}
	}
	if s.WorkItems == nil {
		s.WorkItems = map[string]domain.WorkItem{}
	}
	if s.Evidence == nil {
		s.Evidence = map[string]domain.EvidenceBundle{}
	}
	if s.Provenance == nil {
		s.Provenance = map[string]domain.Provenance{}
	}
	if s.ProviderBindings == nil {
		s.ProviderBindings = map[string]domain.ProviderBinding{}
	}
	if s.ImpactReports == nil {
		s.ImpactReports = map[string][]domain.ImpactReport{}
	}
	if s.Idempotency == nil {
		s.Idempotency = map[string]json.RawMessage{}
	}
	if s.Repositories == nil {
		s.Repositories = map[string]domain.Repository{}
	}
	if s.TeamWorkspaces == nil {
		s.TeamWorkspaces = map[string]domain.TeamWorkspace{}
	}
	if s.Profiles == nil {
		s.Profiles = map[string]domain.DeveloperProfile{}
	}
	if s.MCPServers == nil {
		s.MCPServers = map[string]domain.MCPServer{}
	}
	if s.Skills == nil {
		s.Skills = map[string]domain.Skill{}
	}
	if s.Automations == nil {
		s.Automations = map[string]domain.Automation{}
	}
	if s.Approvals == nil {
		s.Approvals = map[string]domain.Approval{}
	}
	if s.Diffs == nil {
		s.Diffs = map[string]domain.DiffSnapshot{}
	}
	if s.Migrations == nil {
		s.Migrations = map[string]domain.ArtifactMigration{}
	}
	if s.Invocations == nil {
		s.Invocations = map[string]domain.MCPInvocation{}
	}
	if s.Bindings == nil {
		s.Bindings = map[string]domain.CapabilityBinding{}
	}
	if s.AutomationRuns == nil {
		s.AutomationRuns = map[string]domain.AutomationRun{}
	}
	if s.Contexts == nil {
		s.Contexts = map[string][]domain.ContextManifest{}
	}
	if s.RepairAttempts == nil {
		s.RepairAttempts = map[string][]domain.RepairAttempt{}
	}
	if s.Pipelines == nil {
		s.Pipelines = map[string]domain.PipelineRun{}
	}
	if s.WorkflowTemplates == nil {
		s.WorkflowTemplates = map[string]domain.WorkflowTemplate{}
	}
	if s.ChatSessions == nil {
		s.ChatSessions = map[string]domain.ChatSession{}
	}
	if s.ChatMessages == nil {
		s.ChatMessages = map[string][]domain.ChatMessage{}
	}
}

func mergeWorkspaceSnapshot(state *persistedState, workspace, policy string, in WorkspaceSnapshot, report *WorkspaceImportReport) error {
	merge := func(kind, id string, exists bool, assign func()) error {
		if exists {
			if policy == "fail" {
				return fmt.Errorf("%s %q conflicts with existing data", kind, id)
			}
			report.Skipped[kind]++
			return nil
		}
		assign()
		report.Created[kind]++
		return nil
	}
	for _, value := range in.Requirements {
		value.WorkspaceID = workspace
		v := value
		if err := merge("requirements", v.ID, state.Requirements[v.ID].ID != "", func() { state.Requirements[v.ID] = v }); err != nil {
			return err
		}
	}
	for _, value := range in.Bugs {
		value.WorkspaceID = workspace
		v := value
		if err := merge("bugs", v.ID, state.Bugs[v.ID].ID != "", func() { state.Bugs[v.ID] = v }); err != nil {
			return err
		}
	}
	for _, value := range in.Comments {
		value.WorkspaceID = workspace
		v := cloneComment(value)
		if err := merge("comments", v.ID, state.Comments[v.ID].ID != "", func() { state.Comments[v.ID] = v }); err != nil {
			return err
		}
	}
	for _, value := range in.Attachments {
		value.WorkspaceID = workspace
		v := value
		if err := merge("attachments", v.ID, state.Attachments[v.ID].ID != "", func() { state.Attachments[v.ID] = v }); err != nil {
			return err
		}
	}
	for _, value := range in.CommentFollowUps {
		v := value
		key := commentFollowUpKey(v)
		if err := merge("comment_follow_ups", key, state.CommentFollowUps[key].ID != "", func() { state.CommentFollowUps[key] = v }); err != nil {
			return err
		}
	}
	for id, revisions := range in.CommentRevisions {
		if _, exists := state.CommentRevisions[id]; exists {
			if policy == "fail" {
				return fmt.Errorf("comment revisions %q conflict with existing data", id)
			}
			report.Skipped["comment_revisions"]++
			continue
		}
		state.CommentRevisions[id] = revisions
		report.Created["comment_revisions"]++
	}
	for _, value := range in.Repositories {
		value.WorkspaceID = workspace
		v := value
		if err := merge("repositories", v.ID, state.Repositories[v.ID].ID != "", func() { state.Repositories[v.ID] = v }); err != nil {
			return err
		}
	}
	for _, value := range in.TeamWorkspaces {
		value.WorkspaceID = workspace
		v := value
		if err := merge("team_workspaces", v.ID, state.TeamWorkspaces[v.ID].ID != "", func() { state.TeamWorkspaces[v.ID] = v }); err != nil {
			return err
		}
	}
	for _, value := range in.Skills {
		value.WorkspaceID = workspace
		v := value
		if err := merge("skills", v.ID, state.Skills[v.ID].ID != "", func() { state.Skills[v.ID] = v }); err != nil {
			return err
		}
	}
	for _, value := range in.MCPServers {
		value.WorkspaceID = workspace
		v := portableMCPServer(value)
		if err := merge("mcp_servers", v.ID, state.MCPServers[v.ID].ID != "", func() { state.MCPServers[v.ID] = v }); err != nil {
			return err
		}
	}
	for _, value := range in.Automations {
		value.WorkspaceID = workspace
		v := value
		if err := merge("automations", v.ID, state.Automations[v.ID].ID != "", func() { state.Automations[v.ID] = v }); err != nil {
			return err
		}
	}
	for _, value := range in.Bindings {
		value.WorkspaceID = workspace
		v := value
		if err := merge("bindings", v.ID, state.Bindings[v.ID].ID != "", func() { state.Bindings[v.ID] = v }); err != nil {
			return err
		}
	}
	for _, value := range in.ChatSessions {
		value.WorkspaceID = workspace
		value.HarnessSessionID = "portable-chat-" + value.ID
		v := value
		if err := merge("chat_sessions", v.ID, state.ChatSessions[v.ID].ID != "", func() { state.ChatSessions[v.ID] = v }); err != nil {
			return err
		}
	}
	for _, value := range in.ChatMessages {
		value.WorkspaceID = workspace
		v := value
		exists := false
		for _, existing := range state.ChatMessages[v.ChatSessionID] {
			if existing.ID == v.ID {
				exists = true
				break
			}
		}
		if err := merge("chat_messages", v.ID, exists, func() {
			state.ChatMessages[v.ChatSessionID] = append(state.ChatMessages[v.ChatSessionID], v)
		}); err != nil {
			return err
		}
	}
	return validateWorkspaceSnapshotReferences(state, workspace)
}

func validateWorkspaceSnapshotReferences(state *persistedState, workspace string) error {
	for _, comment := range state.Comments {
		if comment.WorkspaceID != workspace {
			continue
		}
		if comment.TargetType == "requirement" && state.Requirements[comment.TargetID].ID == "" {
			return fmt.Errorf("comment %q references missing requirement %q", comment.ID, comment.TargetID)
		}
		if comment.TargetType == "bug" && state.Bugs[comment.TargetID].ID == "" {
			return fmt.Errorf("comment %q references missing bug %q", comment.ID, comment.TargetID)
		}
		if comment.ParentID != "" && state.Comments[comment.ParentID].ID == "" {
			return fmt.Errorf("comment %q references missing parent %q", comment.ID, comment.ParentID)
		}
	}
	for _, attachment := range state.Attachments {
		if attachment.WorkspaceID != workspace {
			continue
		}
		switch attachment.OwnerType {
		case "requirement":
			if state.Requirements[attachment.OwnerID].ID == "" {
				return fmt.Errorf("attachment %q references missing requirement", attachment.ID)
			}
		case "bug":
			if state.Bugs[attachment.OwnerID].ID == "" {
				return fmt.Errorf("attachment %q references missing bug", attachment.ID)
			}
		case "comment":
			if state.Comments[attachment.OwnerID].ID == "" {
				return fmt.Errorf("attachment %q references missing comment", attachment.ID)
			}
		case "chat_message":
			found := false
			for _, messages := range state.ChatMessages {
				for _, message := range messages {
					if message.ID == attachment.OwnerID && message.WorkspaceID == workspace {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if !found {
				return fmt.Errorf("attachment %q references missing chat message", attachment.ID)
			}
		default:
			return fmt.Errorf("attachment %q has unsupported owner type %q", attachment.ID, attachment.OwnerType)
		}
	}
	for _, session := range state.ChatSessions {
		if session.WorkspaceID != workspace {
			continue
		}
		if session.ProjectID != "" && state.TeamWorkspaces[session.ProjectID].ID == "" {
			return fmt.Errorf("chat session %q references missing project %q", session.ID, session.ProjectID)
		}
	}
	for sessionID, messages := range state.ChatMessages {
		if state.ChatSessions[sessionID].WorkspaceID != workspace {
			continue
		}
		for _, message := range messages {
			if message.ChatSessionID != sessionID || message.WorkspaceID != workspace {
				return fmt.Errorf("chat message %q has inconsistent session or workspace", message.ID)
			}
		}
	}
	return nil
}

func commentFollowUpKey(value domain.CommentFollowUp) string {
	return value.CommentID + ":" + fmt.Sprint(value.CommentRevision) + ":" + value.TargetType + ":" + value.TargetID
}

func sanitizePortableMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		lower := strings.ToLower(key)
		blocked := false
		for _, word := range []string{"password", "passwd", "token", "secret", "credential", "cookie", "authorization", "private_key", "api_key", "apikey"} {
			if strings.Contains(lower, word) {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		switch typed := item.(type) {
		case map[string]any:
			out[key] = sanitizePortableMap(typed)
		case []any:
			items := make([]any, 0, len(typed))
			for _, child := range typed {
				if nested, ok := child.(map[string]any); ok {
					items = append(items, sanitizePortableMap(nested))
				} else if text, ok := child.(string); !ok || !strings.HasPrefix(text, "/") {
					items = append(items, child)
				}
			}
			out[key] = items
		case string:
			if !strings.HasPrefix(typed, "/") {
				out[key] = typed
			}
		default:
			out[key] = item
		}
	}
	return out
}

func portableMCPServer(server domain.MCPServer) domain.MCPServer {
	hadSecretReference := strings.TrimSpace(server.SecretRef) != ""
	server.SecretRef = ""
	server.Configuration = sanitizePortableMap(server.Configuration)
	parsed, err := url.Parse(strings.TrimSpace(server.Endpoint))
	credentialBearingEndpoint := err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != ""
	if credentialBearingEndpoint {
		server.Endpoint = "redacted://configure-after-import"
	}
	if hadSecretReference || credentialBearingEndpoint {
		server.Status = "disabled"
	}
	return server
}

func sortWorkspaceSnapshot(s *WorkspaceSnapshot) {
	sort.Slice(s.Requirements, func(i, j int) bool { return s.Requirements[i].ID < s.Requirements[j].ID })
	sort.Slice(s.Bugs, func(i, j int) bool { return s.Bugs[i].ID < s.Bugs[j].ID })
	sort.Slice(s.Attachments, func(i, j int) bool { return s.Attachments[i].ID < s.Attachments[j].ID })
	sort.Slice(s.Comments, func(i, j int) bool { return s.Comments[i].ID < s.Comments[j].ID })
	sort.Slice(s.Repositories, func(i, j int) bool { return s.Repositories[i].ID < s.Repositories[j].ID })
	sort.Slice(s.TeamWorkspaces, func(i, j int) bool { return s.TeamWorkspaces[i].ID < s.TeamWorkspaces[j].ID })
	sort.Slice(s.MCPServers, func(i, j int) bool { return s.MCPServers[i].ID < s.MCPServers[j].ID })
	sort.Slice(s.Skills, func(i, j int) bool { return s.Skills[i].ID < s.Skills[j].ID })
	sort.Slice(s.Automations, func(i, j int) bool { return s.Automations[i].ID < s.Automations[j].ID })
	sort.Slice(s.Bindings, func(i, j int) bool { return s.Bindings[i].ID < s.Bindings[j].ID })
	sort.Slice(s.ChatSessions, func(i, j int) bool { return s.ChatSessions[i].ID < s.ChatSessions[j].ID })
	sort.Slice(s.ChatMessages, func(i, j int) bool { return s.ChatMessages[i].ID < s.ChatMessages[j].ID })
}

func cloneWorkspaceSnapshot(value WorkspaceSnapshot) WorkspaceSnapshot {
	data, _ := json.Marshal(value)
	var clone WorkspaceSnapshot
	_ = json.Unmarshal(data, &clone)
	return clone
}
