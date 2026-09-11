// Package workspacebundle implements the portable, provider-neutral workspace
// archive used by the CLI, HTTP API, and first-run migration screen.
package workspacebundle

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/artifact"
	"github.com/adro-project/adro/internal/orchestration"
	"github.com/adro-project/adro/internal/store"
)

const (
	Format            = "adro.workspace.v1"
	manifestPath      = "manifest.json"
	maxArchiveBytes   = 2 << 30
	maxEntryBytes     = 512 << 20
	maxArchiveEntries = 10_000
)

var ErrConflict = errors.New("workspace import conflict")

type ArtifactEntry struct {
	Key           artifact.Key `json:"key"`
	Path          string       `json:"path"`
	MediaType     string       `json:"media_type,omitempty"`
	SizeBytes     int64        `json:"size_bytes"`
	ContentSHA256 string       `json:"content_sha256"`
	Immutable     bool         `json:"immutable"`
}

type Manifest struct {
	Format            string                         `json:"format"`
	Version           int                            `json:"version"`
	CreatedAt         time.Time                      `json:"created_at"`
	SourceWorkspaceID string                         `json:"source_workspace_id"`
	Digest            string                         `json:"digest"`
	Control           store.WorkspaceSnapshot        `json:"control"`
	Definitions       orchestration.DefinitionBundle `json:"definitions"`
	Artifacts         []ArtifactEntry                `json:"artifacts,omitempty"`
	Excluded          []string                       `json:"excluded"`
}

type Counts struct {
	Agents       int `json:"agents"`
	Squads       int `json:"squads"`
	Requirements int `json:"requirements"`
	Comments     int `json:"comments"`
	Attachments  int `json:"attachments"`
	Repositories int `json:"repositories"`
	Projects     int `json:"projects"`
	Skills       int `json:"skills"`
	MCPServers   int `json:"mcp_servers"`
	Automations  int `json:"automations"`
	ChatSessions int `json:"chat_sessions"`
	ChatMessages int `json:"chat_messages"`
	Artifacts    int `json:"artifact_payloads"`
}

type PreflightReport struct {
	Format            string            `json:"format"`
	Digest            string            `json:"digest"`
	SourceWorkspaceID string            `json:"source_workspace_id"`
	TargetWorkspaceID string            `json:"target_workspace_id,omitempty"`
	ConflictPolicy    string            `json:"conflict_policy"`
	Counts            Counts            `json:"counts"`
	IDRemap           map[string]string `json:"id_remap,omitempty"`
	Excluded          []string          `json:"excluded"`
	Valid             bool              `json:"valid"`
}

type ImportReport struct {
	Preflight     PreflightReport                      `json:"preflight"`
	Control       store.WorkspaceImportReport          `json:"control"`
	Definitions   orchestration.DefinitionImportReport `json:"definitions"`
	ArtifactsPut  int                                  `json:"artifacts_put"`
	ArtifactsSkip int                                  `json:"artifacts_skipped"`
	DryRun        bool                                 `json:"dry_run"`
	Replay        bool                                 `json:"replay"`
}

type DefinitionRepository interface {
	ListAgents(string, orchestration.AgentStatus) []orchestration.AgentDefinition
	ListSquads(string, orchestration.SquadStatus) []orchestration.SquadDefinition
	ImportDefinitionBundle(string, orchestration.DefinitionBundle, bool) (orchestration.DefinitionImportReport, error)
}

type backupRepository interface {
	Backup(string) error
	Restore(string) error
}

type Service struct {
	Control     *store.Memory
	Definitions DefinitionRepository
	Artifacts   artifact.Store
	Now         func() time.Time
}

// Preflight validates both the archive and the actual target stores without
// committing data. This catches destination conflicts before an operator is
// allowed to start an import.
func (s Service) Preflight(ctx context.Context, data []byte, targetWorkspace, policy string) (PreflightReport, error) {
	report, err := s.Import(ctx, data, targetWorkspace, policy, true)
	if err != nil {
		return PreflightReport{}, err
	}
	return report.Preflight, nil
}

func (s Service) Export(ctx context.Context, workspaceID string) ([]byte, Manifest, error) {
	if s.Control == nil || s.Definitions == nil || s.Artifacts == nil {
		return nil, Manifest{}, errors.New("workspace migration service is not configured")
	}
	control, err := s.Control.ExportWorkspace(workspaceID)
	if err != nil {
		return nil, Manifest{}, err
	}
	definitions := orchestration.DefinitionBundle{
		Format: orchestration.DefinitionBundleFormat, SourceWorkspaceID: workspaceID,
		Agents: s.Definitions.ListAgents(workspaceID, ""), Squads: s.Definitions.ListSquads(workspaceID, ""),
	}
	for index := range definitions.Agents {
		definitions.Agents[index].ExecutorBinding.ProviderVersion = ""
		definitions.Agents[index].ExecutorBinding.BinaryDigest = ""
		definitions.Agents[index].ExecutorBinding.CustomArgs = portableRuntimeArgs(definitions.Agents[index].ExecutorBinding.RuntimeID, definitions.Agents[index].ExecutorBinding.CustomArgs)
		definitions.Agents[index].ExecutorBinding.RuntimeConfig = portableRuntimeConfig(definitions.Agents[index].ExecutorBinding.RuntimeConfig)
		definitions.Agents[index].ExecutorBinding.Environment = nil
	}
	if len(definitions.Agents) == 0 {
		return nil, Manifest{}, errors.New("workspace has no Agent definition to migrate")
	}
	payloads := map[string][]byte{}
	entries := make([]ArtifactEntry, 0, len(control.Attachments))
	for _, attachment := range control.Attachments {
		key, err := parseArtifactURI(attachment.ArtifactURI)
		if err != nil {
			return nil, Manifest{}, fmt.Errorf("attachment %q: %w", attachment.ID, err)
		}
		reader, meta, err := s.Artifacts.Open(ctx, key, artifact.ByteRange{End: -1})
		if err != nil {
			return nil, Manifest{}, fmt.Errorf("open attachment %q payload: %w", attachment.ID, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, maxEntryBytes+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, Manifest{}, fmt.Errorf("read attachment %q payload: %w", attachment.ID, readErr)
		}
		if closeErr != nil {
			return nil, Manifest{}, fmt.Errorf("close attachment %q payload: %w", attachment.ID, closeErr)
		}
		if len(data) > maxEntryBytes {
			return nil, Manifest{}, fmt.Errorf("attachment %q exceeds the portable object limit", attachment.ID)
		}
		entryPath := artifactPath(key)
		entries = append(entries, ArtifactEntry{Key: key, Path: entryPath, MediaType: meta.MediaType, SizeBytes: meta.SizeBytes, ContentSHA256: meta.ContentSHA256, Immutable: meta.Immutable})
		payloads[entryPath] = data
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	manifest := Manifest{Format: Format, Version: 1, CreatedAt: now, SourceWorkspaceID: workspaceID, Control: control, Definitions: definitions, Artifacts: entries, Excluded: []string{"authentication credentials and environment values", "executor sessions", "task tokens", "leases and queued work", "local absolute paths", "provider observations"}}
	return finalizeArchive(manifest, payloads)
}

func finalizeArchive(manifest Manifest, payloads map[string][]byte) ([]byte, Manifest, error) {
	sortManifest(&manifest)
	digest, err := manifestDigest(manifest)
	if err != nil {
		return nil, Manifest{}, err
	}
	manifest.Digest = digest
	archive, err := writeArchive(manifest, payloads)
	if err != nil {
		return nil, Manifest{}, err
	}
	return archive, manifest, nil
}

func Preflight(data []byte, targetWorkspace, policy string) (Manifest, map[string][]byte, PreflightReport, error) {
	policy = strings.ToLower(strings.TrimSpace(policy))
	if policy == "" {
		policy = "rename"
	}
	if policy != "fail" && policy != "skip" && policy != "rename" {
		return Manifest{}, nil, PreflightReport{}, fmt.Errorf("unsupported conflict policy %q", policy)
	}
	manifest, payloads, err := readArchive(data)
	if err != nil {
		return Manifest{}, nil, PreflightReport{}, err
	}
	want, err := manifestDigest(manifest)
	if err != nil {
		return Manifest{}, nil, PreflightReport{}, err
	}
	if manifest.Digest != want {
		return Manifest{}, nil, PreflightReport{}, errors.New("workspace bundle digest mismatch")
	}
	if strings.TrimSpace(targetWorkspace) == "" {
		targetWorkspace = manifest.SourceWorkspaceID
	}
	prepared, remap, err := prepareManifest(manifest, targetWorkspace, policy)
	if err != nil {
		return Manifest{}, nil, PreflightReport{}, err
	}
	if err := validatePortableReferences(prepared); err != nil {
		return Manifest{}, nil, PreflightReport{}, err
	}
	preparedPayloads := make(map[string][]byte, len(payloads))
	for index, original := range manifest.Artifacts {
		preparedPayloads[prepared.Artifacts[index].Path] = payloads[original.Path]
	}
	report := PreflightReport{Format: Format, Digest: manifest.Digest, SourceWorkspaceID: manifest.SourceWorkspaceID, TargetWorkspaceID: targetWorkspace, ConflictPolicy: policy, Counts: manifestCounts(prepared), IDRemap: remap, Excluded: append([]string(nil), manifest.Excluded...), Valid: true}
	return prepared, preparedPayloads, report, nil
}

func validatePortableReferences(manifest Manifest) error {
	agents := make(map[string]orchestration.AgentDefinition, len(manifest.Definitions.Agents))
	for _, agent := range manifest.Definitions.Agents {
		if current, exists := agents[agent.ID]; !exists || agent.Revision > current.Revision {
			agents[agent.ID] = agent
		}
	}
	squads := make(map[string]orchestration.SquadDefinition, len(manifest.Definitions.Squads))
	for _, squad := range manifest.Definitions.Squads {
		if current, exists := squads[squad.ID]; !exists || squad.Revision > current.Revision {
			squads[squad.ID] = squad
		}
	}
	requirements := make(map[string]bool, len(manifest.Control.Requirements))
	repositories := make(map[string]bool, len(manifest.Control.Repositories))
	projects := make(map[string]bool, len(manifest.Control.TeamWorkspaces))
	skills := make(map[string]bool, len(manifest.Control.Skills))
	mcpServers := make(map[string]bool, len(manifest.Control.MCPServers))
	for _, requirement := range manifest.Control.Requirements {
		requirements[requirement.ID] = true
	}
	for _, repository := range manifest.Control.Repositories {
		repositories[repository.ID] = true
	}
	for _, project := range manifest.Control.TeamWorkspaces {
		projects[project.ID] = true
		for _, repositoryID := range project.RepositoryIDs {
			if !repositories[repositoryID] {
				return fmt.Errorf("project %q references missing repository %q", project.ID, repositoryID)
			}
		}
	}
	for _, skill := range manifest.Control.Skills {
		skills[skill.ID] = true
	}
	for _, server := range manifest.Control.MCPServers {
		mcpServers[server.ID] = true
	}
	for _, agent := range agents {
		for _, skillID := range agent.SkillIDs {
			if !skills[skillID] {
				return fmt.Errorf("Agent %q references missing Skill %q", agent.ID, skillID)
			}
		}
		for _, serverID := range agent.MCPServerIDs {
			if !mcpServers[serverID] {
				return fmt.Errorf("Agent %q references missing MCP server %q", agent.ID, serverID)
			}
		}
	}
	for _, requirement := range manifest.Control.Requirements {
		if requirement.ParentRequirementID != "" && !requirements[requirement.ParentRequirementID] {
			return fmt.Errorf("requirement %q references missing parent %q", requirement.ID, requirement.ParentRequirementID)
		}
		if requirement.TeamWorkspaceID != "" && !projects[requirement.TeamWorkspaceID] {
			return fmt.Errorf("requirement %q references missing project %q", requirement.ID, requirement.TeamWorkspaceID)
		}
		// Unknown requirement repository IDs remain portable because provider-
		// native resources need not be mirrored in the local registry.
		targetType := strings.ToLower(strings.TrimSpace(requirement.AssigneeTargetType))
		targetID := strings.TrimSpace(requirement.AssigneeTargetID)
		if (targetType == "") != (targetID == "") {
			return fmt.Errorf("requirement %q has an incomplete assignee target", requirement.ID)
		}
		switch targetType {
		case "", "member":
		case "agent":
			agent, ok := agents[targetID]
			if !ok || agent.Status != orchestration.AgentActive {
				return fmt.Errorf("requirement %q references missing or inactive Agent %q", requirement.ID, targetID)
			}
		case "squad":
			squad, ok := squads[targetID]
			if !ok || squad.Status != orchestration.SquadPublished {
				return fmt.Errorf("requirement %q references missing or unpublished Squad %q", requirement.ID, targetID)
			}
		default:
			return fmt.Errorf("requirement %q has unsupported assignee target type %q", requirement.ID, targetType)
		}
	}
	for _, binding := range manifest.Control.Bindings {
		if _, ok := agents[binding.AgentID]; !ok {
			return fmt.Errorf("capability binding %q references missing Agent %q", binding.ID, binding.AgentID)
		}
		if binding.Kind == "skill" && !skills[binding.CapabilityID] {
			return fmt.Errorf("capability binding %q references missing Skill %q", binding.ID, binding.CapabilityID)
		}
		if binding.Kind == "mcp" && !mcpServers[binding.CapabilityID] {
			return fmt.Errorf("capability binding %q references missing MCP server %q", binding.ID, binding.CapabilityID)
		}
	}
	for _, session := range manifest.Control.ChatSessions {
		if session.AgentID != "" {
			if _, ok := agents[session.AgentID]; !ok {
				return fmt.Errorf("chat session %q references missing Agent %q", session.ID, session.AgentID)
			}
		}
		if session.ProjectID != "" && !projects[session.ProjectID] {
			return fmt.Errorf("chat session %q references missing project %q", session.ID, session.ProjectID)
		}
	}
	for _, automation := range manifest.Control.Automations {
		for _, node := range automation.Nodes {
			targetType, _ := node["assignee_type"].(string)
			targetID, _ := node["assignee_id"].(string)
			switch targetType {
			case "agent":
				if _, ok := agents[targetID]; !ok {
					return fmt.Errorf("automation %q references missing Agent %q", automation.ID, targetID)
				}
			case "squad":
				if _, ok := squads[targetID]; !ok {
					return fmt.Errorf("automation %q references missing Squad %q", automation.ID, targetID)
				}
			}
			if projectID, _ := node["project_id"].(string); projectID != "" && !projects[projectID] {
				return fmt.Errorf("automation %q references missing project %q", automation.ID, projectID)
			}
		}
	}
	return nil
}

func (s Service) Import(ctx context.Context, data []byte, targetWorkspace, policy string, dryRun bool) (ImportReport, error) {
	if s.Control == nil || s.Definitions == nil || s.Artifacts == nil {
		return ImportReport{}, errors.New("workspace migration service is not configured")
	}
	manifest, payloads, preflight, err := Preflight(data, targetWorkspace, policy)
	if err != nil {
		return ImportReport{}, err
	}
	report := ImportReport{Preflight: preflight, DryRun: dryRun}
	report.Control, err = s.Control.ImportWorkspace(preflight.TargetWorkspaceID, preflight.Digest, preflight.ConflictPolicy, manifest.Control, true)
	if err != nil {
		return ImportReport{}, err
	}
	report.Definitions, err = s.importDefinitions(preflight.TargetWorkspaceID, preflight.ConflictPolicy, manifest.Definitions, true)
	if err != nil {
		return ImportReport{}, err
	}
	if report.Control.Replay {
		report.Replay = true
		return report, nil
	}
	for _, entry := range manifest.Artifacts {
		if _, statErr := s.Artifacts.Stat(ctx, entry.Key); statErr == nil {
			if preflight.ConflictPolicy == "fail" {
				return ImportReport{}, fmt.Errorf("%w: artifact %s already exists", ErrConflict, entry.Key.URI())
			}
			report.ArtifactsSkip++
		} else if !errors.Is(statErr, context.Canceled) && !errors.Is(statErr, context.DeadlineExceeded) && !errors.Is(statErr, io.EOF) {
			// File and object stores use implementation-specific not-found errors.
			if !errors.Is(statErr, os.ErrNotExist) && !strings.Contains(strings.ToLower(statErr.Error()), "not exist") && !strings.Contains(strings.ToLower(statErr.Error()), "not found") && !strings.Contains(strings.ToLower(statErr.Error()), "no such file") {
				return ImportReport{}, fmt.Errorf("stat target artifact: %w", statErr)
			}
		}
	}
	if dryRun {
		return report, nil
	}
	controlBackup, definitionsBackup, cleanup, err := s.backups()
	if err != nil {
		return ImportReport{}, err
	}
	defer cleanup()
	createdArtifacts := make([]artifact.Key, 0, len(manifest.Artifacts))
	rollback := func(cause error) error {
		for i := len(createdArtifacts) - 1; i >= 0; i-- {
			_ = s.Artifacts.Delete(context.Background(), createdArtifacts[i], artifact.DeleteOptions{})
		}
		var failures []string
		if controlBackup != "" {
			if restoreErr := s.Control.Restore(controlBackup); restoreErr != nil {
				failures = append(failures, "control: "+restoreErr.Error())
			}
		}
		if definitionsBackup != "" {
			if restoreErr := s.Definitions.(backupRepository).Restore(definitionsBackup); restoreErr != nil {
				failures = append(failures, "definitions: "+restoreErr.Error())
			}
		}
		if len(failures) > 0 {
			return fmt.Errorf("%w; rollback failed: %s", cause, strings.Join(failures, "; "))
		}
		return cause
	}
	for _, entry := range manifest.Artifacts {
		if _, err := s.Artifacts.Stat(ctx, entry.Key); err == nil {
			continue
		}
		meta, putErr := s.Artifacts.Put(ctx, entry.Key, bytes.NewReader(payloads[entry.Path]), artifact.PutOptions{MediaType: entry.MediaType, Immutable: entry.Immutable})
		if putErr != nil {
			return ImportReport{}, rollback(fmt.Errorf("write artifact %s: %w", entry.Key.URI(), putErr))
		}
		if meta.ContentSHA256 != entry.ContentSHA256 || meta.SizeBytes != entry.SizeBytes {
			return ImportReport{}, rollback(fmt.Errorf("verify imported artifact %s: metadata mismatch (sha256 got %s want %s, size got %d want %d)", entry.Key.URI(), meta.ContentSHA256, entry.ContentSHA256, meta.SizeBytes, entry.SizeBytes))
		}
		createdArtifacts = append(createdArtifacts, entry.Key)
		report.ArtifactsPut++
	}
	report.Control, err = s.Control.ImportWorkspace(preflight.TargetWorkspaceID, preflight.Digest, preflight.ConflictPolicy, manifest.Control, false)
	if err != nil {
		return ImportReport{}, rollback(err)
	}
	report.Definitions, err = s.importDefinitions(preflight.TargetWorkspaceID, preflight.ConflictPolicy, manifest.Definitions, false)
	if err != nil {
		return ImportReport{}, rollback(err)
	}
	return report, nil
}

func (s Service) importDefinitions(workspaceID, policy string, bundle orchestration.DefinitionBundle, dryRun bool) (orchestration.DefinitionImportReport, error) {
	digest, err := bundle.Digest()
	if err != nil {
		return orchestration.DefinitionImportReport{}, fmt.Errorf("digest definition bundle: %w", err)
	}
	report := orchestration.DefinitionImportReport{
		Format: orchestration.DefinitionBundleFormat, Digest: digest,
		AgentCount: len(bundle.Agents), SquadCount: len(bundle.Squads), DryRun: dryRun,
	}
	if policy == "rename" {
		return s.Definitions.ImportDefinitionBundle(workspaceID, bundle, dryRun)
	}

	existingAgents := map[string]bool{}
	for _, value := range s.Definitions.ListAgents(workspaceID, "") {
		existingAgents[value.ID+"\x00"+strconv.FormatInt(value.Revision, 10)] = true
	}
	existingSquads := map[string]bool{}
	for _, value := range s.Definitions.ListSquads(workspaceID, "") {
		existingSquads[value.ID+"\x00"+strconv.FormatInt(value.Revision, 10)] = true
	}
	filtered := bundle
	filtered.Agents = nil
	filtered.Squads = nil
	for _, value := range bundle.Agents {
		key := value.ID + "\x00" + strconv.FormatInt(value.Revision, 10)
		if existingAgents[key] {
			if policy == "fail" {
				return orchestration.DefinitionImportReport{}, fmt.Errorf("%w: Agent revision %q already exists", ErrConflict, value.ID)
			}
			report.SkippedAgents++
			continue
		}
		filtered.Agents = append(filtered.Agents, value)
	}
	for _, value := range bundle.Squads {
		key := value.ID + "\x00" + strconv.FormatInt(value.Revision, 10)
		if existingSquads[key] {
			if policy == "fail" {
				return orchestration.DefinitionImportReport{}, fmt.Errorf("%w: Squad revision %q already exists", ErrConflict, value.ID)
			}
			report.SkippedSquads++
			continue
		}
		filtered.Squads = append(filtered.Squads, value)
	}
	if len(filtered.Agents) == 0 && len(filtered.Squads) == 0 {
		return report, nil
	}
	imported, err := s.Definitions.ImportDefinitionBundle(workspaceID, filtered, dryRun)
	if err != nil {
		return orchestration.DefinitionImportReport{}, err
	}
	report.CreatedAgents = imported.CreatedAgents
	report.CreatedSquads = imported.CreatedSquads
	report.SkippedAgents += imported.SkippedAgents
	report.SkippedSquads += imported.SkippedSquads
	return report, nil
}

func (s Service) backups() (string, string, func(), error) {
	definitionBackup, ok := s.Definitions.(backupRepository)
	if !ok {
		return "", "", func() {}, errors.New("definition repository does not support atomic migration rollback")
	}
	base, err := os.MkdirTemp("", "adro-workspace-import-*")
	if err != nil {
		return "", "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(base) }
	controlPath, definitionPath := filepath.Join(base, "control.json"), filepath.Join(base, "definitions.json")
	if err := s.Control.Backup(controlPath); err != nil {
		cleanup()
		return "", "", func() {}, err
	}
	if err := definitionBackup.Backup(definitionPath); err != nil {
		cleanup()
		return "", "", func() {}, err
	}
	return controlPath, definitionPath, cleanup, nil
}

func manifestCounts(m Manifest) Counts {
	return Counts{
		Agents:       len(m.Definitions.Agents),
		Squads:       len(m.Definitions.Squads),
		Requirements: len(m.Control.Requirements),
		Comments:     len(m.Control.Comments),
		Attachments:  len(m.Control.Attachments),
		Repositories: len(m.Control.Repositories),
		Projects:     len(m.Control.TeamWorkspaces),
		Skills:       len(m.Control.Skills),
		MCPServers:   len(m.Control.MCPServers),
		Automations:  len(m.Control.Automations),
		ChatSessions: len(m.Control.ChatSessions),
		ChatMessages: len(m.Control.ChatMessages),
		Artifacts:    len(m.Artifacts),
	}
}

func writeArchive(manifest Manifest, payloads map[string][]byte) ([]byte, error) {
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	entry, err := writer.CreateHeader(&zip.FileHeader{Name: manifestPath, Method: zip.Deflate})
	if err != nil {
		return nil, err
	}
	if _, err := entry.Write(data); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(payloads))
	for name := range payloads {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	for _, name := range paths {
		file, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			return nil, err
		}
		if _, err := file.Write(payloads[name]); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func readArchive(data []byte) (Manifest, map[string][]byte, error) {
	if len(data) == 0 || int64(len(data)) > maxArchiveBytes {
		return Manifest{}, nil, errors.New("workspace bundle size is invalid")
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Manifest{}, nil, fmt.Errorf("open workspace bundle: %w", err)
	}
	if len(reader.File) == 0 || len(reader.File) > maxArchiveEntries {
		return Manifest{}, nil, errors.New("workspace bundle entry count is invalid")
	}
	files := map[string][]byte{}
	var total uint64
	for _, file := range reader.File {
		name := path.Clean(file.Name)
		if name != file.Name || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") || file.FileInfo().IsDir() {
			return Manifest{}, nil, fmt.Errorf("unsafe workspace bundle entry %q", file.Name)
		}
		if _, duplicate := files[name]; duplicate {
			return Manifest{}, nil, fmt.Errorf("duplicate workspace bundle entry %q", name)
		}
		if file.UncompressedSize64 > maxEntryBytes || total+file.UncompressedSize64 > maxArchiveBytes {
			return Manifest{}, nil, fmt.Errorf("workspace bundle entry %q exceeds size limits", name)
		}
		stream, err := file.Open()
		if err != nil {
			return Manifest{}, nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(stream, maxEntryBytes+1))
		closeErr := stream.Close()
		if readErr != nil {
			return Manifest{}, nil, readErr
		}
		if closeErr != nil {
			return Manifest{}, nil, closeErr
		}
		if len(body) > maxEntryBytes {
			return Manifest{}, nil, errors.New("workspace bundle entry expanded beyond its limit")
		}
		files[name] = body
		total += uint64(len(body))
	}
	manifestData, ok := files[manifestPath]
	if !ok {
		return Manifest{}, nil, errors.New("workspace bundle has no manifest")
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(manifestData))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, nil, fmt.Errorf("decode workspace manifest: %w", err)
	}
	if manifest.Format != Format || manifest.Version != 1 || manifest.Control.Format != store.WorkspaceSnapshotFormat || manifest.Definitions.Format != orchestration.DefinitionBundleFormat {
		return Manifest{}, nil, fmt.Errorf("unsupported workspace bundle format %q version %d", manifest.Format, manifest.Version)
	}
	declared := map[string]bool{manifestPath: true}
	payloads := map[string][]byte{}
	for _, item := range manifest.Artifacts {
		if item.Path != artifactPath(item.Key) || declared[item.Path] {
			return Manifest{}, nil, fmt.Errorf("invalid or duplicate artifact path %q", item.Path)
		}
		declared[item.Path] = true
		payload, exists := files[item.Path]
		if !exists {
			return Manifest{}, nil, fmt.Errorf("artifact payload %q is missing", item.Path)
		}
		hash := sha256.Sum256(payload)
		if int64(len(payload)) != item.SizeBytes || hex.EncodeToString(hash[:]) != item.ContentSHA256 {
			return Manifest{}, nil, fmt.Errorf("artifact payload %q failed size or SHA-256 verification", item.Path)
		}
		payloads[item.Path] = payload
	}
	for name := range files {
		if !declared[name] {
			return Manifest{}, nil, fmt.Errorf("undeclared workspace bundle entry %q", name)
		}
	}
	return manifest, payloads, nil
}

func manifestDigest(m Manifest) (string, error) {
	copy := m
	copy.Digest = ""
	copy.CreatedAt = time.Time{}
	sortManifest(&copy)
	data, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func sortManifest(m *Manifest) {
	sort.Slice(m.Definitions.Agents, func(i, j int) bool {
		a, b := m.Definitions.Agents[i], m.Definitions.Agents[j]
		return a.ID+fmt.Sprint(a.Revision) < b.ID+fmt.Sprint(b.Revision)
	})
	sort.Slice(m.Definitions.Squads, func(i, j int) bool {
		a, b := m.Definitions.Squads[i], m.Definitions.Squads[j]
		return a.ID+fmt.Sprint(a.Revision) < b.ID+fmt.Sprint(b.Revision)
	})
	sort.Slice(m.Artifacts, func(i, j int) bool { return m.Artifacts[i].Path < m.Artifacts[j].Path })
	sort.Strings(m.Excluded)
}

func prepareManifest(m Manifest, target, policy string) (Manifest, map[string]string, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return Manifest{}, nil, err
	}
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return Manifest{}, nil, err
	}
	ids := manifestIDs(m)
	remap := map[string]string{}
	if policy == "rename" {
		for id := range ids {
			remap[id] = deterministicID(m.Digest, target, id)
		}
	}
	rewriteValues(root, m.SourceWorkspaceID, target, remap)
	data, err = json.Marshal(root)
	if err != nil {
		return Manifest{}, nil, err
	}
	var prepared Manifest
	if err := json.Unmarshal(data, &prepared); err != nil {
		return Manifest{}, nil, err
	}
	prepared.Control.SourceWorkspace = target
	prepared.Definitions.SourceWorkspaceID = target
	for i := range prepared.Artifacts {
		prepared.Artifacts[i].Path = artifactPath(prepared.Artifacts[i].Key)
	}
	return prepared, remap, nil
}

func manifestIDs(manifest Manifest) map[string]bool {
	ids := map[string]bool{}
	add := func(id string) {
		if strings.TrimSpace(id) != "" {
			ids[id] = true
		}
	}
	addGraph := func(graph orchestration.WorkflowGraph) {
		add(graph.ID)
		for _, node := range graph.Nodes {
			add(node.ID)
		}
		for _, edge := range graph.Edges {
			add(edge.ID)
		}
	}
	for _, agent := range manifest.Definitions.Agents {
		add(agent.ID)
		addGraph(agent.Graph)
	}
	for _, squad := range manifest.Definitions.Squads {
		add(squad.ID)
		for _, member := range squad.Members {
			add(member.ID)
		}
		addGraph(squad.Graph)
	}
	for _, value := range manifest.Control.Requirements {
		add(value.ID)
		for _, key := range []string{"labels", "dependencies", "property_definitions"} {
			collectCollectionIDs(value.Metadata[key], add)
		}
	}
	for _, value := range manifest.Control.Bugs {
		add(value.ID)
	}
	for _, value := range manifest.Control.Attachments {
		add(value.ID)
	}
	for _, value := range manifest.Control.Comments {
		add(value.ID)
	}
	for _, value := range manifest.Control.CommentFollowUps {
		add(value.ID)
	}
	for _, value := range manifest.Control.Repositories {
		add(value.ID)
	}
	for _, value := range manifest.Control.TeamWorkspaces {
		add(value.ID)
	}
	for _, value := range manifest.Control.Skills {
		add(value.ID)
	}
	for _, value := range manifest.Control.MCPServers {
		add(value.ID)
	}
	for _, value := range manifest.Control.Automations {
		add(value.ID)
		collectCollectionIDs(value.Trigger["sources"], add)
	}
	for _, value := range manifest.Control.Bindings {
		add(value.ID)
	}
	for _, value := range manifest.Control.ChatSessions {
		add(value.ID)
	}
	for _, value := range manifest.Control.ChatMessages {
		add(value.ID)
	}
	for _, value := range manifest.Artifacts {
		add(value.Key.ArtifactID)
	}
	return ids
}

func collectCollectionIDs(value any, add func(string)) {
	items, ok := value.([]any)
	if !ok {
		return
	}
	for _, item := range items {
		if object, ok := item.(map[string]any); ok {
			if id, ok := object["id"].(string); ok {
				add(id)
			}
		}
	}
}

func rewriteValues(value any, sourceWorkspace, targetWorkspace string, remap map[string]string) {
	rewriteJSONValue(value, sourceWorkspace, targetWorkspace, remap, false, false)
}

func rewriteJSONValue(value any, sourceWorkspace, targetWorkspace string, remap map[string]string, rewriteMapKeys, rewriteStrings bool) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child, exists := typed[key]
			if !exists {
				continue
			}
			if key == "workspace_id" || key == "source_workspace_id" || key == "tenant_id" {
				if child == sourceWorkspace {
					typed[key] = targetWorkspace
					child = targetWorkspace
				}
			}
			if text, ok := child.(string); ok {
				if isIDReferenceField(key) || strings.HasPrefix(text, "artifact://") {
					child = rewriteString(text, sourceWorkspace, targetWorkspace, remap)
				} else {
					child = rewriteStructuredMentions(text, remap)
				}
			} else {
				rewriteJSONValue(child, sourceWorkspace, targetWorkspace, remap, key == "comment_revisions" || key == "properties", isIDReferenceField(key))
			}
			mappedKey := ""
			if rewriteMapKeys {
				mappedKey = remap[key]
			}
			if mappedKey == "" || mappedKey == key {
				typed[key] = child
				continue
			}
			delete(typed, key)
			typed[mappedKey] = child
		}
	case []any:
		for index, child := range typed {
			if text, ok := child.(string); ok {
				if rewriteStrings {
					typed[index] = rewriteString(text, sourceWorkspace, targetWorkspace, remap)
				} else {
					typed[index] = rewriteStructuredMentions(text, remap)
				}
			} else {
				rewriteJSONValue(child, sourceWorkspace, targetWorkspace, remap, false, rewriteStrings)
			}
		}
	}
}

func isIDReferenceField(key string) bool {
	return key == "id" || key == "from" || key == "to" || key == "mentions" || strings.HasSuffix(key, "_id") || strings.HasSuffix(key, "_ids")
}

func rewriteStructuredMentions(value string, remap map[string]string) string {
	for _, kind := range []string{"agent", "squad", "issue"} {
		prefix := "mention://" + kind + "/"
		start := 0
		for {
			index := strings.Index(value[start:], prefix)
			if index < 0 {
				break
			}
			index += start + len(prefix)
			end := strings.IndexByte(value[index:], ')')
			if end < 0 {
				break
			}
			end += index
			if mapped := remap[value[index:end]]; mapped != "" {
				value = value[:index] + mapped + value[end:]
				start = index + len(mapped)
			} else {
				start = end + 1
			}
		}
	}
	return value
}

func rewriteString(value, sourceWorkspace, targetWorkspace string, remap map[string]string) string {
	if mapped := remap[value]; mapped != "" {
		return mapped
	}
	if strings.HasPrefix(value, "artifact://") {
		parts := strings.Split(value, "/")
		if len(parts) == 5 {
			if parts[2] == sourceWorkspace {
				parts[2] = targetWorkspace
			}
			if mapped := remap[parts[3]]; mapped != "" {
				parts[3] = mapped
			}
			return strings.Join(parts, "/")
		}
	}
	return value
}

func deterministicID(digest, target, source string) string {
	sum := sha256.Sum256([]byte(digest + "\x00" + target + "\x00" + source))
	sum[6] = (sum[6] & 0x0f) | 0x40
	sum[8] = (sum[8] & 0x3f) | 0x80
	hexID := hex.EncodeToString(sum[:16])
	return hexID[:8] + "-" + hexID[8:12] + "-" + hexID[12:16] + "-" + hexID[16:20] + "-" + hexID[20:32]
}

func parseArtifactURI(uri string) (artifact.Key, error) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(uri), "artifact://"), "/")
	if !strings.HasPrefix(strings.TrimSpace(uri), "artifact://") || len(parts) != 3 {
		return artifact.Key{}, errors.New("artifact URI is not portable")
	}
	version, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || version < 1 {
		return artifact.Key{}, errors.New("artifact URI has an invalid version")
	}
	key := artifact.Key{TenantID: parts[0], ArtifactID: parts[1], Version: version}
	if artifactPath(key) == "" {
		return artifact.Key{}, errors.New("artifact URI has unsafe identifiers")
	}
	return key, nil
}

func artifactPath(key artifact.Key) string {
	for _, part := range []string{key.TenantID, key.ArtifactID} {
		if part == "" || part != path.Base(part) || strings.Contains(part, "..") || strings.ContainsAny(part, `/\\`) {
			return ""
		}
	}
	if key.Version < 1 {
		return ""
	}
	return path.Join("artifacts", key.TenantID, key.ArtifactID, strconv.FormatInt(key.Version, 10)+".bin")
}

func portableArgs(args []string) []string {
	return sanitizePortableArgs(args)
}

func portableRuntimeArgs(runtimeID string, args []string) []string {
	result := portableArgs(args)
	if runtimeID != "codex" {
		return result
	}
	filtered := make([]string, 0, len(result))
	for _, arg := range result {
		if arg == "--ephemeral" || strings.HasPrefix(arg, "--ephemeral=") {
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered
}

func portableRuntimeConfig(config map[string]string) map[string]string {
	result := make(map[string]string, len(config))
	for key, value := range config {
		if containsSensitiveName(strings.ToLower(key)) || isAbsolutePortablePath(value) {
			continue
		}
		result[key] = value
	}
	return result
}
