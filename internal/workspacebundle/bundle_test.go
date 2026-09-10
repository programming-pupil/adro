package workspacebundle

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adro-project/adro/internal/artifact"
	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/orchestration"
	"github.com/adro-project/adro/internal/store"
)

func TestWorkspaceArchiveRoundTripRedactionRemapAndReplay(t *testing.T) {
	ctx := context.Background()
	sourceStore := store.NewMemory()
	requirement, err := sourceStore.CreateRequirement(domain.Requirement{
		ID: "requirement-1", WorkspaceID: "source", Title: "Portable work", Description: "retain this",
		AcceptanceCriteria: []string{"works"}, AssigneeMemberIDs: []string{"member-1"}, RepositoryIDs: []string{"repository-1"},
		Properties: map[string]any{"property-1": "production"},
		Metadata: map[string]any{"property_definitions": []any{map[string]any{
			"id": "property-1", "name": "Environment", "type": "select",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	comment, err := sourceStore.CreateComment(domain.Comment{
		ID: "comment-1", WorkspaceID: "source", TargetType: "requirement", TargetID: requirement.ID,
		AuthorType: "member", AuthorID: "member-1", Content: "agent-1 is text; [@agent](mention://agent/agent-1)", Mentions: []string{"agent-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceArtifacts, err := artifact.NewFileStore(filepath.Join(t.TempDir(), "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	key := artifact.Key{TenantID: "tenant", ArtifactID: "payload-1", Version: 1}
	meta, err := sourceArtifacts.Put(ctx, key, strings.NewReader("portable payload"), artifact.PutOptions{MediaType: "text/plain", Immutable: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sourceStore.SaveAttachment(domain.EntityAttachment{ID: "attachment-1", WorkspaceID: "source", OwnerType: "comment", OwnerID: comment.ID, Filename: "proof.txt", MediaType: meta.MediaType, SizeBytes: meta.SizeBytes, ArtifactURI: key.URI(), CreatedBy: "member-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceStore.UpsertAutomation(domain.Automation{ID: "automation-1", WorkspaceID: "source", Name: "Daily check", Trigger: map[string]any{"kind": "schedule", "webhook_token": "never-export", "workdir": "/Users/example/private"}, Nodes: []map[string]any{{"kind": "agent", "password": "never-export"}}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceStore.UpsertSkill(domain.Skill{ID: "skill-1", WorkspaceID: "source", Name: "Review", Version: "1", Contract: map[string]any{"mode": "strict"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceStore.UpsertMCPServer(domain.MCPServer{ID: "mcp-1", WorkspaceID: "source", Name: "Review tools", Endpoint: "https://mcp.example.test/session?token=never-export", Protocol: "http", SecretRef: "env:NEVER_EXPORT", Status: "configured", Configuration: map[string]any{"retry": float64(2), "authorization": "never-export"}}); err != nil {
		t.Fatal(err)
	}

	sourceDefinitions := orchestration.NewMemoryRepository()
	agent := orchestration.AgentDefinition{ID: "agent-1", WorkspaceID: "source", Revision: 1, Name: "General", SkillIDs: []string{"skill-1"}, MCPServerIDs: []string{"mcp-1"}, Status: orchestration.AgentActive, ExecutorBinding: orchestration.ExecutorBinding{ProviderID: "local", RuntimeID: "codex", Model: "gpt-5", ThinkingLevel: "high", ProviderVersion: "machine-only", BinaryDigest: "machine-only", CustomArgs: []string{"--ephemeral", "--api-key=never-export"}, RuntimeConfig: map[string]string{"sandbox_mode": "workspace-write"}, Environment: []orchestration.EnvironmentReference{{Name: "TOKEN", SecretRef: "env:NEVER_EXPORT"}}}, InputSchema: orchestration.SchemaRef{ID: "input", Version: 1}, OutputSchema: orchestration.SchemaRef{ID: "output", Version: 1}}
	if err := sourceDefinitions.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}

	archive, manifest, err := (Service{Control: sourceStore, Definitions: sourceDefinitions, Artifacts: sourceArtifacts}).Export(ctx, "source")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"never-export", "/Users/example/private", "machine-only"} {
		if bytes.Contains(archive, []byte(forbidden)) {
			t.Fatalf("archive contains redacted value %q", forbidden)
		}
	}
	if manifest.Digest == "" || len(manifest.Artifacts) != 1 {
		t.Fatalf("manifest=%+v", manifest)
	}
	portableAgent := manifest.Definitions.Agents[0]
	if len(portableAgent.ExecutorBinding.Environment) != 0 || len(portableAgent.ExecutorBinding.RuntimeConfig) != 1 || portableAgent.ExecutorBinding.RuntimeConfig["sandbox_mode"] != "workspace-write" {
		t.Fatalf("portable Agent runtime configuration=%+v", portableAgent.ExecutorBinding)
	}
	if len(manifest.Control.MCPServers) != 1 || manifest.Control.MCPServers[0].SecretRef != "" || manifest.Control.MCPServers[0].Status != "disabled" || !strings.HasPrefix(manifest.Control.MCPServers[0].Endpoint, "redacted:") {
		t.Fatalf("portable MCP server=%+v", manifest.Control.MCPServers)
	}

	prepared, _, preflight, err := Preflight(archive, "target", "rename")
	if err != nil {
		t.Fatal(err)
	}
	if !preflight.Valid || len(preflight.IDRemap) == 0 || prepared.Control.Requirements[0].ID == requirement.ID || prepared.Control.Requirements[0].WorkspaceID != "target" {
		t.Fatalf("preflight=%+v prepared=%+v", preflight, prepared.Control.Requirements)
	}
	if prepared.Control.Attachments[0].ArtifactURI == key.URI() || prepared.Artifacts[0].Key.ArtifactID == key.ArtifactID {
		t.Fatalf("artifact identity was not remapped: %+v %+v", prepared.Control.Attachments[0], prepared.Artifacts[0])
	}
	renamedCommentID := preflight.IDRemap[comment.ID]
	if len(prepared.Control.CommentRevisions[renamedCommentID]) != 1 || prepared.Control.CommentRevisions[renamedCommentID][0].CommentID != renamedCommentID {
		t.Fatalf("comment revision key was not remapped with its comment: %+v", prepared.Control.CommentRevisions)
	}
	renamedPropertyID := preflight.IDRemap["property-1"]
	if renamedPropertyID == "" || prepared.Control.Requirements[0].Properties[renamedPropertyID] != "production" {
		t.Fatalf("custom property key was not remapped: remap=%+v properties=%+v", preflight.IDRemap, prepared.Control.Requirements[0].Properties)
	}
	if _, remapped := preflight.IDRemap["production"]; remapped {
		t.Fatalf("scoped property option was treated as a global entity ID: %+v", preflight.IDRemap)
	}
	expectedContent := "agent-1 is text; [@agent](mention://agent/" + preflight.IDRemap["agent-1"] + ")"
	if prepared.Control.Comments[0].Content != expectedContent || len(prepared.Control.Comments[0].Mentions) != 1 || prepared.Control.Comments[0].Mentions[0] != preflight.IDRemap["agent-1"] {
		t.Fatalf("user-authored content was changed during ID remapping: %q", prepared.Control.Comments[0].Content)
	}

	targetStore, err := store.NewPersistentMemory(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	targetDefinitions, err := orchestration.NewPersistentRepository(filepath.Join(t.TempDir(), "orchestration.json"))
	if err != nil {
		t.Fatal(err)
	}
	targetArtifacts, err := artifact.NewFileStore(filepath.Join(t.TempDir(), "target-artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Control: targetStore, Definitions: targetDefinitions, Artifacts: targetArtifacts}
	report, err := service.Import(ctx, archive, "target", "rename", false)
	if err != nil {
		t.Fatal(err)
	}
	if report.ArtifactsPut != 1 || report.Control.Created["requirements"] != 1 || report.Definitions.CreatedAgents != 1 {
		t.Fatalf("report=%+v", report)
	}
	imported, _ := targetStore.ListRequirements("target", "", "", 20)
	if len(imported) != 1 || imported[0].Title != "Portable work" {
		t.Fatalf("requirements=%+v", imported)
	}
	reader, _, err := targetArtifacts.Open(ctx, prepared.Artifacts[0].Key, artifact.ByteRange{End: -1})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(body) != "portable payload" {
		t.Fatalf("payload=%q", body)
	}
	replay, err := service.Import(ctx, archive, "target", "rename", false)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replay || !replay.Control.Replay || replay.ArtifactsPut != 0 {
		t.Fatalf("replay=%+v", replay)
	}
}

func TestWorkspaceArchiveRejectsTamperedAndUndeclaredEntries(t *testing.T) {
	archive := minimalArchive(t)
	tampered := rewriteArchive(t, archive, func(name string, data []byte) (string, []byte) {
		if strings.HasPrefix(name, "artifacts/") {
			return name, []byte("tampered")
		}
		return name, data
	})
	if _, _, _, err := Preflight(tampered, "target", "rename"); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("tamper error=%v", err)
	}
	undeclared := rewriteArchive(t, archive, func(name string, data []byte) (string, []byte) { return name, data }, archiveEntry{name: "extra.txt", data: []byte("not declared")})
	if _, _, _, err := Preflight(undeclared, "target", "rename"); err == nil || !strings.Contains(err.Error(), "undeclared") {
		t.Fatalf("undeclared error=%v", err)
	}
}

func TestWorkspaceImportRollsBackControlAndArtifacts(t *testing.T) {
	archive := minimalArchive(t)
	targetStore, err := store.NewPersistentMemory(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	base := orchestration.NewMemoryRepository()
	failing := &failingDefinitions{MemoryRepository: base}
	targetArtifacts, err := artifact.NewFileStore(filepath.Join(t.TempDir(), "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = (Service{Control: targetStore, Definitions: failing, Artifacts: targetArtifacts}).Import(context.Background(), archive, "target", "rename", false)
	if err == nil || !strings.Contains(err.Error(), "injected definition commit failure") {
		t.Fatalf("error=%v", err)
	}
	items, _ := targetStore.ListRequirements("target", "", "", 10)
	if len(items) != 0 {
		t.Fatalf("control import survived rollback: %+v", items)
	}
	_, _, preflight, err := Preflight(archive, "target", "rename")
	if err != nil {
		t.Fatal(err)
	}
	_ = preflight
	prepared, _, _, _ := Preflight(archive, "target", "rename")
	if _, err := targetArtifacts.Stat(context.Background(), prepared.Artifacts[0].Key); err == nil {
		t.Fatal("artifact survived rollback")
	}
}

func TestTargetPreflightEnforcesDefinitionConflictPolicies(t *testing.T) {
	agent := orchestration.AgentDefinition{
		ID: "agent-1", WorkspaceID: "source", Revision: 1, Name: "Builder", Status: orchestration.AgentActive,
		ExecutorBinding: orchestration.ExecutorBinding{ProviderID: "local", RuntimeID: "codex"},
		InputSchema:     orchestration.SchemaRef{ID: "input", Version: 1}, OutputSchema: orchestration.SchemaRef{ID: "output", Version: 1},
	}
	squad := orchestration.SquadDefinition{
		ID: "squad-1", WorkspaceID: "source", Revision: 1, Name: "Delivery", Status: orchestration.SquadPublished, PublishedVersion: 1,
		Members: []orchestration.SquadMember{{ID: "member-1", AgentID: agent.ID, Role: "leader", Leader: true, MaxAttempts: 1}},
		Graph: orchestration.WorkflowGraph{
			ID: "graph-1", Version: 1, EntryNodeIDs: []string{"node-1"}, ExitNodeIDs: []string{"node-1"},
			Nodes: []orchestration.WorkflowNode{{ID: "node-1", Kind: orchestration.NodeAgent, AgentRef: &orchestration.VersionedRef{ID: agent.ID, Revision: 1}}},
		},
		Policy: orchestration.SquadPolicy{MaxNestingDepth: 1},
	}
	archive, _, err := finalizeArchive(Manifest{
		Format: Format, Version: 1, SourceWorkspaceID: "source",
		Control:     store.WorkspaceSnapshot{Format: store.WorkspaceSnapshotFormat, SourceWorkspace: "source"},
		Definitions: orchestration.DefinitionBundle{Format: orchestration.DefinitionBundleFormat, SourceWorkspaceID: "source", Agents: []orchestration.AgentDefinition{agent}, Squads: []orchestration.SquadDefinition{squad}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	definitions := orchestration.NewMemoryRepository()
	agent.WorkspaceID = "target"
	if err := definitions.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	artifacts, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Control: store.NewMemory(), Definitions: definitions, Artifacts: artifacts}
	if _, err := service.Preflight(context.Background(), archive, "target", "fail"); !errors.Is(err, ErrConflict) {
		t.Fatalf("fail preflight error=%v, want definition conflict", err)
	}
	report, err := service.Import(context.Background(), archive, "target", "skip", false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Definitions.SkippedAgents != 1 || report.Definitions.CreatedSquads != 1 || len(definitions.ListSquads("target", "")) != 1 {
		t.Fatalf("definition report=%+v", report.Definitions)
	}
}

type failingDefinitions struct {
	*orchestration.MemoryRepository
}

func (f *failingDefinitions) ImportDefinitionBundle(workspace string, bundle orchestration.DefinitionBundle, dry bool) (orchestration.DefinitionImportReport, error) {
	if !dry {
		return orchestration.DefinitionImportReport{}, errors.New("injected definition commit failure")
	}
	return f.MemoryRepository.ImportDefinitionBundle(workspace, bundle, true)
}

func minimalArchive(t *testing.T) []byte {
	t.Helper()
	ctx := context.Background()
	control := store.NewMemory()
	requirement, err := control.CreateRequirement(domain.Requirement{ID: "requirement-1", WorkspaceID: "source", Title: "migrate", Description: "data", AcceptanceCriteria: []string{"ok"}, AssigneeMemberIDs: []string{"member"}, RepositoryIDs: []string{"repo"}})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := artifact.NewFileStore(filepath.Join(t.TempDir(), "source-artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	key := artifact.Key{TenantID: "tenant", ArtifactID: "object", Version: 1}
	meta, err := artifacts.Put(ctx, key, strings.NewReader("payload"), artifact.PutOptions{MediaType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.SaveAttachment(domain.EntityAttachment{ID: "attachment", WorkspaceID: "source", OwnerType: "requirement", OwnerID: requirement.ID, Filename: "data.txt", MediaType: "text/plain", SizeBytes: meta.SizeBytes, ArtifactURI: key.URI()}); err != nil {
		t.Fatal(err)
	}
	definitions := orchestration.NewMemoryRepository()
	agent := orchestration.AgentDefinition{ID: "agent", WorkspaceID: "source", Revision: 1, Name: "General", Status: orchestration.AgentActive, ExecutorBinding: orchestration.ExecutorBinding{ProviderID: "local", RuntimeID: "codex"}, InputSchema: orchestration.SchemaRef{ID: "in", Version: 1}, OutputSchema: orchestration.SchemaRef{ID: "out", Version: 1}}
	if err := definitions.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	data, _, err := (Service{Control: control, Definitions: definitions, Artifacts: artifacts}).Export(ctx, "source")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type archiveEntry struct {
	name string
	data []byte
}

func rewriteArchive(t *testing.T, source []byte, transform func(string, []byte) (string, []byte), extra ...archiveEntry) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, file := range reader.File {
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		name, data := transform(file.Name, data)
		target, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range extra {
		target, err := writer.Create(item.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Write(item.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }
