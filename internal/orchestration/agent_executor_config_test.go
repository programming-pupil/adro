package orchestration

import "testing"

func TestAgentExecutorConfigurationValidation(t *testing.T) {
	base := AgentDefinition{
		ID: "agent", WorkspaceID: "workspace", Revision: 1, Name: "Agent", Status: AgentActive,
		ExecutorBinding: ExecutorBinding{ProviderID: "local", RuntimeID: "codex", Model: "gpt-5", ThinkingLevel: "xhigh", ServiceTier: "priority", CustomArgs: []string{"--sandbox", "workspace-write"}},
		InputSchema:     SchemaRef{ID: "input"}, OutputSchema: SchemaRef{ID: "output"},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid executor configuration rejected: %v", err)
	}
	invalidThinking := base
	invalidThinking.ExecutorBinding.ThinkingLevel = "extreme"
	if err := invalidThinking.Validate(); err == nil {
		t.Fatal("invalid thinking level accepted")
	}
	invalidTier := base
	invalidTier.ExecutorBinding.ServiceTier = "turbo"
	if err := invalidTier.Validate(); err == nil {
		t.Fatal("invalid service tier accepted")
	}
}
