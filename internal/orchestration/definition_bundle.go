package orchestration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

const DefinitionBundleFormat = "adro.workspace-definitions.v1"

type DefinitionBundle struct {
	Format            string            `json:"format"`
	SourceWorkspaceID string            `json:"source_workspace_id,omitempty"`
	Agents            []AgentDefinition `json:"agents"`
	Squads            []SquadDefinition `json:"squads,omitempty"`
}

type DefinitionImportReport struct {
	Format        string `json:"format"`
	Digest        string `json:"digest"`
	AgentCount    int    `json:"agent_count"`
	SquadCount    int    `json:"squad_count"`
	CreatedAgents int    `json:"created_agents"`
	CreatedSquads int    `json:"created_squads"`
	SkippedAgents int    `json:"skipped_agents"`
	SkippedSquads int    `json:"skipped_squads"`
	DryRun        bool   `json:"dry_run"`
}

func (b DefinitionBundle) Digest() (string, error) {
	copy := b
	copy.Format = DefinitionBundleFormat
	copy.Agents = append([]AgentDefinition(nil), b.Agents...)
	copy.Squads = append([]SquadDefinition(nil), b.Squads...)
	sort.Slice(copy.Agents, func(i, j int) bool {
		return key3(copy.Agents[i].WorkspaceID, copy.Agents[i].ID, copy.Agents[i].Revision) < key3(copy.Agents[j].WorkspaceID, copy.Agents[j].ID, copy.Agents[j].Revision)
	})
	sort.Slice(copy.Squads, func(i, j int) bool {
		return key3(copy.Squads[i].WorkspaceID, copy.Squads[i].ID, copy.Squads[i].Revision) < key3(copy.Squads[j].WorkspaceID, copy.Squads[j].ID, copy.Squads[j].Revision)
	})
	data, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

// ImportDefinitionBundle validates the complete candidate snapshot before one
// durable write. Any conflict or persistence error restores the old maps.
func (r *MemoryRepository) ImportDefinitionBundle(workspaceID string, bundle DefinitionBundle, dryRun bool) (DefinitionImportReport, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return DefinitionImportReport{}, errors.New("workspace_id is required")
	}
	if bundle.Format != DefinitionBundleFormat {
		return DefinitionImportReport{}, fmt.Errorf("unsupported bundle format %q", bundle.Format)
	}
	if len(bundle.Agents) == 0 && len(bundle.Squads) == 0 {
		return DefinitionImportReport{}, errors.New("bundle must contain at least one definition")
	}
	digest, err := bundle.Digest()
	if err != nil {
		return DefinitionImportReport{}, fmt.Errorf("digest bundle: %w", err)
	}
	report := DefinitionImportReport{Format: DefinitionBundleFormat, Digest: digest, AgentCount: len(bundle.Agents), SquadCount: len(bundle.Squads), DryRun: dryRun}
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	oldAgents, oldSquads := r.agents, r.squads
	oldDirty, oldRevision := r.dirty, r.revision
	candidateAgents, candidateSquads := cloneValue(r.agents), cloneValue(r.squads)
	seenAgents, seenSquads := map[string]struct{}{}, map[string]struct{}{}
	for i := range bundle.Agents {
		a := cloneValue(bundle.Agents[i])
		a.WorkspaceID = workspaceID
		if a.Revision == 0 {
			a.Revision = 1
		}
		if a.Status == "" {
			a.Status = AgentActive
		}
		if a.CreatedAt.IsZero() {
			a.CreatedAt = now
		}
		if a.UpdatedAt.IsZero() {
			a.UpdatedAt = a.CreatedAt
		}
		if err := a.Validate(); err != nil {
			return DefinitionImportReport{}, fmt.Errorf("agents[%d] %q: %w", i, a.ID, err)
		}
		if err := validatePortableExecutorBinding(a.ExecutorBinding); err != nil {
			return DefinitionImportReport{}, fmt.Errorf("agents[%d] %q: %w", i, a.ID, err)
		}
		key := key3(workspaceID, a.ID, a.Revision)
		if _, duplicate := seenAgents[key]; duplicate {
			return DefinitionImportReport{}, fmt.Errorf("agents[%d]: duplicate agent revision %s", i, key)
		}
		seenAgents[key] = struct{}{}
		if existing, exists := candidateAgents[key]; exists {
			a.CreatedAt, a.UpdatedAt = existing.CreatedAt, existing.UpdatedAt
			if !reflect.DeepEqual(existing, a) {
				return DefinitionImportReport{}, fmt.Errorf("agents[%d]: agent revision %s conflicts with existing definition", i, key)
			}
			report.SkippedAgents++
			continue
		}
		candidateAgents[key] = a
		report.CreatedAgents++
	}
	// Published Squad validation resolves members through r.agents, so point it
	// at the fully validated candidate Agent set while checking Squads.
	r.agents = candidateAgents
	defer func() { r.agents, r.squads, r.dirty, r.revision = oldAgents, oldSquads, oldDirty, oldRevision }()
	for i := range bundle.Squads {
		s := cloneValue(bundle.Squads[i])
		s.WorkspaceID = workspaceID
		if s.Revision == 0 {
			s.Revision = 1
		}
		if s.Status == "" {
			s.Status = SquadPublished
		}
		if err := s.Validate(); err != nil {
			return DefinitionImportReport{}, fmt.Errorf("squads[%d] %q: %w", i, s.ID, err)
		}
		if s.Status == SquadPublished {
			if err := r.validatePublishedSquadLocked(s); err != nil {
				return DefinitionImportReport{}, fmt.Errorf("squads[%d] %q: %w", i, s.ID, err)
			}
		}
		key := key3(workspaceID, s.ID, s.Revision)
		if _, duplicate := seenSquads[key]; duplicate {
			return DefinitionImportReport{}, fmt.Errorf("squads[%d]: duplicate squad revision %s", i, key)
		}
		seenSquads[key] = struct{}{}
		if existing, exists := candidateSquads[key]; exists {
			if !reflect.DeepEqual(existing, s) {
				return DefinitionImportReport{}, fmt.Errorf("squads[%d]: squad revision %s conflicts with existing definition", i, key)
			}
			report.SkippedSquads++
			continue
		}
		candidateSquads[key] = s
		report.CreatedSquads++
	}
	if dryRun {
		return report, nil
	}
	r.agents, r.squads, r.dirty = candidateAgents, candidateSquads, true
	if err := r.persistLocked(); err != nil {
		return DefinitionImportReport{}, fmt.Errorf("persist definition bundle: %w", err)
	}
	oldAgents, oldSquads, oldDirty, oldRevision = r.agents, r.squads, r.dirty, r.revision
	return report, nil
}

func validatePortableExecutorBinding(binding ExecutorBinding) error {
	if strings.TrimSpace(binding.ProviderVersion) != "" || strings.TrimSpace(binding.BinaryDigest) != "" {
		return errors.New("provider_version and binary_digest are machine observations and cannot be imported")
	}
	for _, arg := range binding.CustomArgs {
		name := strings.ToLower(strings.TrimSpace(strings.SplitN(arg, "=", 2)[0]))
		for _, fragment := range []string{"token", "secret", "password", "passwd", "cookie", "api-key", "apikey", "credential"} {
			if strings.Contains(name, fragment) {
				return fmt.Errorf("custom argument %q may contain a credential and cannot be imported", name)
			}
		}
	}
	return nil
}
