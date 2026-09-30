package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
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
		return Key3(copy.Agents[i].WorkspaceID, copy.Agents[i].ID, copy.Agents[i].Revision) < Key3(copy.Agents[j].WorkspaceID, copy.Agents[j].ID, copy.Agents[j].Revision)
	})
	sort.Slice(copy.Squads, func(i, j int) bool {
		return Key3(copy.Squads[i].WorkspaceID, copy.Squads[i].ID, copy.Squads[i].Revision) < Key3(copy.Squads[j].WorkspaceID, copy.Squads[j].ID, copy.Squads[j].Revision)
	})
	data, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func ValidatePortableExecutorBinding(binding ExecutorBinding) error {
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

func Key3(ws, id string, rev int64) string { return fmt.Sprintf("%s:%s:%d", ws, id, rev) }
