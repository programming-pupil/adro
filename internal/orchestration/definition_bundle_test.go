package orchestration

import (
	"path/filepath"
	"testing"
)

func migrationBundle() DefinitionBundle {
	agent := AgentDefinition{
		ID: "agent-1", Revision: 1, Name: "General agent", Description: "Handles general delivery work.", Role: "generalist", Instructions: "Deliver verifiable results.", Status: AgentActive,
		ConversationStarters: []ConversationStarter{{Label: "Plan", Prompt: "Plan this delivery."}},
		AccessPolicy:         AgentAccessPolicy{Mode: "members", MemberIDs: []string{"member-1"}},
		ExecutorBinding:      ExecutorBinding{ProviderID: "local", RuntimeID: "codex", Model: "gpt-5", ThinkingLevel: "high", CustomArgs: []string{"--ephemeral"}},
		ConcurrencyBudget:    Budget{Tokens: 120000, ToolCalls: 200, Concurrent: 2},
		InputSchema:          SchemaRef{ID: "input", Version: 1}, OutputSchema: SchemaRef{ID: "output", Version: 1},
	}
	graph := WorkflowGraph{ID: "squad-graph", Version: 1, EntryNodeIDs: []string{"agent-node"}, ExitNodeIDs: []string{"agent-node"}, Nodes: []WorkflowNode{{ID: "agent-node", Kind: NodeAgent, AgentRef: &VersionedRef{ID: agent.ID, Revision: 1}}}}
	squad := SquadDefinition{ID: "squad-1", Revision: 1, PublishedVersion: 1, Name: "Delivery", Status: SquadPublished, Members: []SquadMember{{ID: "leader", AgentID: agent.ID, Role: "leader", Leader: true}}, Graph: graph}
	return DefinitionBundle{Format: DefinitionBundleFormat, SourceWorkspaceID: "source", Agents: []AgentDefinition{agent}, Squads: []SquadDefinition{squad}}
}

func TestImportDefinitionBundleDryRunCommitAndReplay(t *testing.T) {
	repo := NewMemoryRepository()
	bundle := migrationBundle()
	dry, err := repo.ImportDefinitionBundle("target", bundle, true)
	if err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun || dry.CreatedAgents != 1 || dry.CreatedSquads != 1 {
		t.Fatalf("dry report=%+v", dry)
	}
	if len(repo.ListAgents("target", "")) != 0 || len(repo.ListSquads("target", "")) != 0 {
		t.Fatal("dry-run changed repository")
	}

	created, err := repo.ImportDefinitionBundle("target", bundle, false)
	if err != nil {
		t.Fatal(err)
	}
	if created.Digest != dry.Digest || created.CreatedAgents != 1 || created.CreatedSquads != 1 {
		t.Fatalf("created=%+v dry=%+v", created, dry)
	}
	agent, err := repo.GetAgent("target", "agent-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if agent.ExecutorBinding.RuntimeID != "codex" || agent.ExecutorBinding.ThinkingLevel != "high" || agent.AccessPolicy.Mode != "members" || len(agent.ConversationStarters) != 1 || agent.ConcurrencyBudget.Concurrent != 2 {
		t.Fatalf("agent=%+v", agent)
	}
	squad, err := repo.GetSquad("target", "squad-1", 1)
	if err != nil || squad.Members[0].AgentID != agent.ID {
		t.Fatalf("squad=%+v err=%v", squad, err)
	}

	replayed, err := repo.ImportDefinitionBundle("target", bundle, false)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.CreatedAgents != 0 || replayed.CreatedSquads != 0 || replayed.SkippedAgents != 1 || replayed.SkippedSquads != 1 {
		t.Fatalf("replay=%+v", replayed)
	}
}

func TestImportDefinitionBundleRejectsWholeInvalidBatch(t *testing.T) {
	repo := NewMemoryRepository()
	bundle := migrationBundle()
	bundle.Agents = append(bundle.Agents, bundle.Agents[0])
	if _, err := repo.ImportDefinitionBundle("target", bundle, false); err == nil {
		t.Fatal("duplicate agent revision was accepted")
	}
	if len(repo.ListAgents("target", "")) != 0 {
		t.Fatal("invalid batch partially imported an agent")
	}

	bundle = migrationBundle()
	bundle.Squads[0].Members[0].AgentID = "missing-agent"
	if _, err := repo.ImportDefinitionBundle("target", bundle, false); err == nil {
		t.Fatal("missing squad member was accepted")
	}
	if len(repo.ListAgents("target", "")) != 0 || len(repo.ListSquads("target", "")) != 0 {
		t.Fatal("failed squad validation partially imported data")
	}

	bundle = migrationBundle()
	bundle.Agents[0].ExecutorBinding.CustomArgs = []string{"--api-key=must-not-migrate"}
	if _, err := repo.ImportDefinitionBundle("target", bundle, false); err == nil {
		t.Fatal("credential-bearing custom argument was accepted")
	}
	if len(repo.ListAgents("target", "")) != 0 {
		t.Fatal("credential rejection partially imported data")
	}
}

func TestImportDefinitionBundleRestoresMemoryAndDiskOnPersistenceConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orchestration.json")
	stale, err := NewPersistentRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	baseline := migrationBundle().Agents[0]
	baseline.ID, baseline.WorkspaceID = "baseline", "target"
	if err := stale.SaveAgent(baseline, 0); err != nil {
		t.Fatal(err)
	}

	concurrent, err := NewPersistentRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	other := baseline
	other.ID = "concurrent"
	if err := concurrent.SaveAgent(other, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := stale.ImportDefinitionBundle("target", migrationBundle(), false); err == nil {
		t.Fatal("stale repository import unexpectedly persisted")
	}
	if _, err := stale.GetAgent("target", "agent-1", 0); err == nil {
		t.Fatal("failed import remained visible in memory")
	}

	reloaded, err := NewPersistentRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.GetAgent("target", "agent-1", 0); err == nil {
		t.Fatal("failed import remained visible on disk")
	}
	if _, err := reloaded.GetAgent("target", "concurrent", 0); err != nil {
		t.Fatalf("concurrent durable update was lost: %v", err)
	}
}
