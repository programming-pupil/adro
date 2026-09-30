package orchestration

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	graphmodel "github.com/adro-project/adro/internal/orchestration/graph"
)

// ImportDefinitionBundle validates the complete candidate snapshot before one
// durable write. Any conflict or persistence error restores the old maps.
func (r *MemoryRepository) ImportDefinitionBundle(workspaceID string, bundle graphmodel.DefinitionBundle, dryRun bool) (graphmodel.DefinitionImportReport, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return graphmodel.DefinitionImportReport{}, errors.New("workspace_id is required")
	}
	if bundle.Format != graphmodel.DefinitionBundleFormat {
		return graphmodel.DefinitionImportReport{}, fmt.Errorf("unsupported bundle format %q", bundle.Format)
	}
	if len(bundle.Agents) == 0 && len(bundle.Squads) == 0 {
		return graphmodel.DefinitionImportReport{}, errors.New("bundle must contain at least one definition")
	}
	digest, err := bundle.Digest()
	if err != nil {
		return graphmodel.DefinitionImportReport{}, fmt.Errorf("digest bundle: %w", err)
	}
	report := graphmodel.DefinitionImportReport{Format: graphmodel.DefinitionBundleFormat, Digest: digest, AgentCount: len(bundle.Agents), SquadCount: len(bundle.Squads), DryRun: dryRun}
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
			a.Status = graphmodel.AgentActive
		}
		if a.CreatedAt.IsZero() {
			a.CreatedAt = now
		}
		if a.UpdatedAt.IsZero() {
			a.UpdatedAt = a.CreatedAt
		}
		if err := a.Validate(); err != nil {
			return graphmodel.DefinitionImportReport{}, fmt.Errorf("agents[%d] %q: %w", i, a.ID, err)
		}
		if err := graphmodel.ValidatePortableExecutorBinding(a.ExecutorBinding); err != nil {
			return graphmodel.DefinitionImportReport{}, fmt.Errorf("agents[%d] %q: %w", i, a.ID, err)
		}
		key := graphmodel.Key3(workspaceID, a.ID, a.Revision)
		if _, duplicate := seenAgents[key]; duplicate {
			return graphmodel.DefinitionImportReport{}, fmt.Errorf("agents[%d]: duplicate agent revision %s", i, key)
		}
		seenAgents[key] = struct{}{}
		if existing, exists := candidateAgents[key]; exists {
			a.CreatedAt, a.UpdatedAt = existing.CreatedAt, existing.UpdatedAt
			if !reflect.DeepEqual(existing, a) {
				return graphmodel.DefinitionImportReport{}, fmt.Errorf("agents[%d]: agent revision %s conflicts with existing definition", i, key)
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
			s.Status = graphmodel.SquadPublished
		}
		if err := s.Validate(); err != nil {
			return graphmodel.DefinitionImportReport{}, fmt.Errorf("squads[%d] %q: %w", i, s.ID, err)
		}
		if s.Status == graphmodel.SquadPublished {
			if err := r.validatePublishedSquadLocked(s); err != nil {
				return graphmodel.DefinitionImportReport{}, fmt.Errorf("squads[%d] %q: %w", i, s.ID, err)
			}
		}
		key := graphmodel.Key3(workspaceID, s.ID, s.Revision)
		if _, duplicate := seenSquads[key]; duplicate {
			return graphmodel.DefinitionImportReport{}, fmt.Errorf("squads[%d]: duplicate squad revision %s", i, key)
		}
		seenSquads[key] = struct{}{}
		if existing, exists := candidateSquads[key]; exists {
			if !reflect.DeepEqual(existing, s) {
				return graphmodel.DefinitionImportReport{}, fmt.Errorf("squads[%d]: squad revision %s conflicts with existing definition", i, key)
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
		return graphmodel.DefinitionImportReport{}, fmt.Errorf("persist definition bundle: %w", err)
	}
	oldAgents, oldSquads, oldDirty, oldRevision = r.agents, r.squads, r.dirty, r.revision
	return report, nil
}
