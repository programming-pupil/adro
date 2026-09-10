package workspacebundle

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adro-project/adro/internal/artifact"
	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/orchestration"
	"github.com/adro-project/adro/internal/store"
)

func TestBuildPostgresArchiveMapsPortableWorkspace(t *testing.T) {
	uploadRoot := t.TempDir()
	attachmentPath := filepath.Join(uploadRoot, "workspace", "notes.txt")
	if err := os.MkdirAll(filepath.Dir(attachmentPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(attachmentPath, []byte("portable attachment"), 0o600); err != nil {
		t.Fatal(err)
	}

	created := time.Date(2026, 9, 11, 8, 0, 0, 0, time.UTC)
	data := postgresDataset{
		Workspace: sourceRow{"id": "source-workspace", "slug": "source"},
		AgentRuntimes: []sourceRow{
			{"id": "runtime-1", "provider": "codex"},
		},
		Agents: []sourceRow{
			{
				"id": "agent-1", "name": "Builder", "description": "Builds and verifies releases.", "avatar_url": "emoji:rocket", "instructions": "Build and verify",
				"runtime_id": "runtime-1", "model": "gpt-5", "thinking_level": "high", "service_tier": "priority",
				"permission_mode": "public_to", "conversation_starters": []any{map[string]any{"label": "Build", "prompt": "Build this release."}},
				"custom_args": []any{"--api-key", "must-not-leak", "--ephemeral", "--cwd", "/private/worktree"},
				"custom_env":  map[string]any{"API_TOKEN": "must-not-leak"}, "max_concurrent_tasks": float64(3),
				"runtime_config":          map[string]any{"sandbox_mode": "workspace-write", "nested": map[string]any{"enabled": true}, "token": "must-not-leak", "path": "/private/runtime"},
				"disabled_runtime_skills": []any{map[string]any{"runtime_id": "runtime-1", "provider": "codex", "root": "provider", "key": "legacy-review", "name": "Legacy review"}},
				"owner_id":                "member-1", "created_at": created.Format(time.RFC3339), "updated_at": created.Format(time.RFC3339),
			},
		},
		AgentInvocationTargets: []sourceRow{
			{"id": "target-1", "agent_id": "agent-1", "target_type": "member", "target_id": "member-2"},
		},
		Projects: []sourceRow{
			{"id": "project-1", "title": "Release", "description": "Ship it", "status": "in_progress", "created_at": created.Format(time.RFC3339), "updated_at": created.Format(time.RFC3339)},
		},
		ProjectResources: []sourceRow{
			{"id": "repository-1", "project_id": "project-1", "resource_type": "github_repo", "resource_ref": map[string]any{"url": "https://github.com/example/service.git", "ref": "trunk"}, "created_at": created.Format(time.RFC3339)},
			{"id": "local-path", "project_id": "project-1", "resource_type": "local_directory", "resource_ref": map[string]any{"local_path": "/private/repository"}},
		},
		Issues: []sourceRow{
			{
				"id": "issue-1", "number": float64(42), "title": "Portable issue", "description": "Keep history",
				"status": "in_progress", "priority": "high", "assignee_type": "agent", "assignee_id": "agent-1",
				"creator_id": "member-1", "project_id": "project-1", "acceptance_criteria": []any{"imports"},
				"metadata":   map[string]any{"visible": "yes", "access_token": "must-not-leak"},
				"properties": map[string]any{"property-1": "production"}, "created_at": created.Format(time.RFC3339), "updated_at": created.Format(time.RFC3339),
			},
			{"id": "issue-2", "number": float64(41), "title": "Dependency", "status": "done", "created_at": created.Format(time.RFC3339), "updated_at": created.Format(time.RFC3339)},
		},
		IssueLabels: []sourceRow{
			{"issue_id": "issue-1", "id": "label-1", "resource_type": "issue", "name": "release", "color": "#e11d48", "description": "Release work"},
		},
		IssueDependencies: []sourceRow{
			{"id": "dependency-1", "issue_id": "issue-1", "depends_on_issue_id": "issue-2", "type": "blocked_by"},
		},
		PropertyDefinitions: []sourceRow{
			{"id": "property-1", "name": "Environment", "type": "select", "description": "Deployment target", "config": map[string]any{"options": []any{map[string]any{"id": "production", "name": "Production"}}}, "icon": "server"},
		},
		Comments: []sourceRow{
			{"id": "comment-1", "issue_id": "issue-1", "author_type": "member", "author_id": "member-1", "content": "Root", "created_at": created.Format(time.RFC3339), "updated_at": created.Format(time.RFC3339)},
			{"id": "comment-2", "issue_id": "issue-1", "parent_id": "comment-1", "author_type": "agent", "author_id": "agent-1", "content": "Reply", "revision": float64(2), "created_at": created.Format(time.RFC3339), "updated_at": created.Format(time.RFC3339)},
		},
		Skills: []sourceRow{
			{"id": "skill-1", "name": "Release checks", "description": "Verify release", "content": "Run tests", "config": map[string]any{"mode": "strict", "secret": "must-not-leak"}, "created_at": created.Format(time.RFC3339), "updated_at": created.Format(time.RFC3339)},
		},
		SkillFiles: []sourceRow{
			{"id": "skill-file-1", "skill_id": "skill-1", "path": "references/checks.md", "content": "Checklist"},
			{"id": "skill-file-unsafe", "skill_id": "skill-1", "path": "../outside", "content": "ignored"},
		},
		AgentSkills: []sourceRow{
			{"agent_id": "agent-1", "skill_id": "skill-1", "enabled": true, "created_at": created.Format(time.RFC3339)},
		},
		WorkspaceMCPServers: []sourceRow{
			{"id": "mcp-1", "name": "Release tools", "config": map[string]any{"type": "http", "url": "https://mcp.example.test/session/must-not-leak"}, "created_at": created.Format(time.RFC3339), "updated_at": created.Format(time.RFC3339)},
		},
		AgentMCPServers: []sourceRow{
			{"agent_id": "agent-1", "server_id": "mcp-1", "enabled": true, "created_at": created.Format(time.RFC3339)},
		},
		Squads: []sourceRow{
			{"id": "squad-1", "name": "Delivery", "description": "Release team", "leader_id": "agent-1"},
		},
		SquadMembers: []sourceRow{
			{"id": "member-row-1", "squad_id": "squad-1", "member_type": "agent", "member_id": "agent-1", "role": "lead"},
		},
		Automations: []sourceRow{
			{"id": "automation-1", "title": "Nightly", "description": "Run checks", "status": "active", "assignee_type": "agent", "assignee_id": "agent-1", "execution_mode": "create_issue", "project_id": "project-1", "created_at": created.Format(time.RFC3339), "updated_at": created.Format(time.RFC3339)},
		},
		Triggers: []sourceRow{
			{"id": "trigger-1", "autopilot_id": "automation-1", "kind": "webhook", "enabled": true, "webhook_token": "must-not-leak", "label": "release"},
		},
		ChatSessions: []sourceRow{
			{"id": "chat-1", "agent_id": "agent-1", "project_id": "project-1", "creator_id": "member-1", "title": "Release chat", "session_id": "provider-session-must-not-leak", "work_dir": "/private/chat", "status": "active", "created_at": created.Format(time.RFC3339), "updated_at": created.Format(time.RFC3339)},
		},
		ChatMessages: []sourceRow{
			{"id": "message-1", "chat_session_id": "chat-1", "role": "user", "content": "Please release", "task_id": "task-must-not-leak", "created_at": created.Format(time.RFC3339)},
		},
		Attachments: []sourceRow{
			{"id": "attachment-1", "workspace_id": "source-workspace", "comment_id": "comment-2", "filename": "notes.txt", "url": "/uploads/workspace/notes.txt", "content_type": "text/plain", "size_bytes": float64(len("portable attachment")), "uploader_id": "member-1", "created_at": created.Format(time.RFC3339)},
		},
	}

	archive, manifest, err := buildPostgresArchive(context.Background(), data, uploadRoot, created)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Definitions.Agents) != 1 || len(manifest.Definitions.Squads) != 1 {
		t.Fatalf("definitions=%+v", manifest.Definitions)
	}
	agent := manifest.Definitions.Agents[0]
	if agent.ExecutorBinding.RuntimeID != "codex" || agent.ExecutorBinding.Model != "gpt-5" || agent.ExecutorBinding.ThinkingLevel != "high" || agent.ExecutorBinding.ServiceTier != "priority" {
		t.Fatalf("agent executor=%+v", agent.ExecutorBinding)
	}
	if agent.ExecutorBinding.RuntimeConfig["sandbox_mode"] != "workspace-write" || agent.ExecutorBinding.RuntimeConfig["nested.enabled"] != "true" || len(agent.DisabledRuntimeSkills) != 1 || agent.DisabledRuntimeSkills[0].RuntimeID != "codex" {
		t.Fatalf("portable runtime configuration=%+v disabled=%+v", agent.ExecutorBinding.RuntimeConfig, agent.DisabledRuntimeSkills)
	}
	if _, leaked := agent.ExecutorBinding.RuntimeConfig["token"]; leaked {
		t.Fatalf("sensitive runtime config leaked: %+v", agent.ExecutorBinding.RuntimeConfig)
	}
	if agent.OwnerID != "member-1" || agent.Description != "Builds and verifies releases." || agent.Role != "" || agent.AvatarURL != "emoji:rocket" || agent.AccessPolicy.Mode != "members" || len(agent.ConversationStarters) != 1 || len(agent.SkillIDs) != 1 || len(agent.MCPServerIDs) != 1 {
		t.Fatalf("portable Agent fields=%+v", agent)
	}
	if len(agent.ExecutorBinding.CustomArgs) != 0 {
		t.Fatalf("portable args=%q", agent.ExecutorBinding.CustomArgs)
	}
	if len(manifest.Control.Requirements) != 2 || manifest.Control.Requirements[0].AssigneeTargetID != "agent-1" || manifest.Control.Requirements[0].TeamWorkspaceID != "project-1" || len(manifest.Control.Requirements[0].RepositoryIDs) != 1 {
		t.Fatalf("requirements=%+v", manifest.Control.Requirements)
	}
	metadata := manifest.Control.Requirements[0].Metadata
	if len(metadata["labels"].([]any)) != 1 || len(metadata["dependencies"].([]any)) != 1 || len(metadata["property_definitions"].([]any)) != 1 || manifest.Control.Requirements[0].Properties["property-1"] != "production" {
		t.Fatalf("requirement compatibility metadata=%+v properties=%+v", metadata, manifest.Control.Requirements[0].Properties)
	}
	if len(manifest.Control.Comments) != 2 || manifest.Control.Comments[1].RootID != "comment-1" || len(manifest.Control.Comments[1].AttachmentIDs) != 1 {
		t.Fatalf("comments=%+v", manifest.Control.Comments)
	}
	if len(manifest.Control.ChatSessions) != 1 || manifest.Control.ChatSessions[0].HarnessSessionID != "portable-chat-chat-1" || len(manifest.Control.ChatMessages) != 1 {
		t.Fatalf("chat sessions=%+v messages=%+v", manifest.Control.ChatSessions, manifest.Control.ChatMessages)
	}
	if len(manifest.Control.Skills) != 1 || len(manifest.Control.MCPServers) != 1 || len(manifest.Control.Bindings) != 2 || len(manifest.Control.Automations) != 1 || len(manifest.Artifacts) != 1 {
		t.Fatalf("manifest counts=%+v", manifestCounts(manifest))
	}
	if manifest.Control.MCPServers[0].Status != "disabled" || !strings.HasPrefix(manifest.Control.MCPServers[0].Endpoint, "redacted:") {
		t.Fatalf("portable MCP server=%+v", manifest.Control.MCPServers[0])
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"must-not-leak", "provider-session-must-not-leak", "task-must-not-leak", "/private/"} {
		if strings.Contains(string(manifestJSON), forbidden) {
			t.Fatalf("archive contains excluded value %q", forbidden)
		}
	}

	prepared, _, report, err := Preflight(archive, "target-workspace", "rename")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid || prepared.Artifacts[0].Key.TenantID != "target-workspace" || !strings.HasPrefix(prepared.Control.Attachments[0].ArtifactURI, "artifact://target-workspace/") {
		t.Fatalf("preflight=%+v artifact=%+v attachment=%+v", report, prepared.Artifacts[0], prepared.Control.Attachments[0])
	}
	renamedPropertyID := report.IDRemap["property-1"]
	if prepared.Control.Requirements[0].Properties[renamedPropertyID] != "production" {
		t.Fatalf("renamed properties=%+v remap=%+v", prepared.Control.Requirements[0].Properties, report.IDRemap)
	}

	control := store.NewMemory()
	definitions := orchestration.NewMemoryRepository()
	artifactStore, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Control: control, Definitions: definitions, Artifacts: artifactStore}
	imported, err := service.Import(context.Background(), archive, "target-workspace", "rename", false)
	if err != nil {
		t.Fatal(err)
	}
	if imported.ArtifactsPut != 1 || len(definitions.ListAgents("target-workspace", "")) != 1 || len(control.ListChatSessions("target-workspace", "")) != 1 {
		t.Fatalf("import report=%+v", imported)
	}
}

func TestBuildPostgresArchiveRejectsUnsafeAttachmentPath(t *testing.T) {
	data := postgresDataset{
		Workspace: sourceRow{"id": "source-workspace"},
		Agents:    []sourceRow{{"id": "agent-1", "name": "Builder"}},
		Issues:    []sourceRow{{"id": "issue-1", "title": "Issue"}},
		Attachments: []sourceRow{{
			"id": "attachment-1", "issue_id": "issue-1", "url": "/uploads/../outside.txt", "size_bytes": float64(1),
		}},
	}
	_, _, err := buildPostgresArchive(context.Background(), data, t.TempDir(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("error=%v, want unsafe attachment rejection", err)
	}
}

func TestSanitizePortableArgsDropsSeparatedSecretsAndPaths(t *testing.T) {
	got := sanitizePortableArgs([]string{
		"--token", "secret-value", "--model=gpt-5", "--workdir", "/private/work", "relative-input", `C:\\private\\work`,
	})
	if strings.Join(got, "|") != "--model=gpt-5|relative-input" {
		t.Fatalf("sanitized args=%q", got)
	}
}

func TestPortableRuntimeArgsDropsCodexExecOnlyFlags(t *testing.T) {
	got := portableRuntimeArgs("codex", []string{"--profile", "research", "--ephemeral", "--config=search=true"})
	if strings.Join(got, "|") != "--profile|research|--config=search=true" {
		t.Fatalf("portable Codex args=%q", got)
	}
	other := portableRuntimeArgs("other", []string{"--ephemeral"})
	if strings.Join(other, "|") != "--ephemeral" {
		t.Fatalf("non-Codex args changed=%q", other)
	}
}

func TestPreflightRejectsMissingNativeAssignmentTarget(t *testing.T) {
	manifest := Manifest{
		Format: Format, Version: 1, SourceWorkspaceID: "source",
		Control: store.WorkspaceSnapshot{
			Format: store.WorkspaceSnapshotFormat, SourceWorkspace: "source",
			Requirements: []domain.Requirement{{ID: "issue-1", WorkspaceID: "source", Title: "orphan", AssigneeTargetType: "agent", AssigneeTargetID: "missing-agent"}},
		},
		Definitions: orchestration.DefinitionBundle{
			Format: orchestration.DefinitionBundleFormat, SourceWorkspaceID: "source",
			Agents: []orchestration.AgentDefinition{{
				ID: "agent-1", WorkspaceID: "source", Revision: 1, Name: "Builder", Status: orchestration.AgentActive,
				ExecutorBinding: orchestration.ExecutorBinding{ProviderID: "local"}, InputSchema: orchestration.SchemaRef{ID: "input"}, OutputSchema: orchestration.SchemaRef{ID: "output"},
			}},
		},
	}
	archive, _, err := finalizeArchive(manifest, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = Preflight(archive, "target", "rename")
	if err == nil || !strings.Contains(err.Error(), "missing or inactive Agent") {
		t.Fatalf("error=%v", err)
	}
}
