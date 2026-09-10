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

func TestAgentHumanConfigurationValidation(t *testing.T) {
	base := AgentDefinition{
		ID: "agent", WorkspaceID: "workspace", Revision: 1, Name: "Agent", Description: "A useful Agent.", Status: AgentDraft,
		ConversationStarters: []ConversationStarter{{Label: "Review", Prompt: "Review this change."}},
		AccessPolicy:         AgentAccessPolicy{Mode: "members", MemberIDs: []string{"member-1"}},
		ConcurrencyBudget:    Budget{Tokens: 1000, ToolCalls: 20, Concurrent: 2},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid human configuration rejected: %v", err)
	}

	tooMany := base
	tooMany.ConversationStarters = append(append([]ConversationStarter(nil), base.ConversationStarters...), ConversationStarter{Label: "Two", Prompt: "2"}, ConversationStarter{Label: "Three", Prompt: "3"}, ConversationStarter{Label: "Four", Prompt: "4"})
	if err := tooMany.Validate(); err == nil {
		t.Fatal("too many conversation starters accepted")
	}

	missingMember := base
	missingMember.AccessPolicy.MemberIDs = nil
	if err := missingMember.Validate(); err == nil {
		t.Fatal("member access without members accepted")
	}

	privateMembers := base
	privateMembers.AccessPolicy.Mode = "private"
	if err := privateMembers.Validate(); err == nil {
		t.Fatal("private access with member allowlist accepted")
	}
}

func TestAgentAccessPolicyIsEnforcedBeforeInvocation(t *testing.T) {
	agent := AgentDefinition{OwnerID: "owner", AccessPolicy: AgentAccessPolicy{Mode: "private"}}
	if !agent.CanInvoke("owner") || agent.CanInvoke("member") || agent.CanInvoke("") {
		t.Fatal("private Agent invocation policy was not enforced")
	}
	agent.AccessPolicy = AgentAccessPolicy{Mode: "workspace"}
	if !agent.CanInvoke("member") || agent.CanInvoke("") {
		t.Fatal("workspace Agent invocation policy was not enforced")
	}
	agent.AccessPolicy = AgentAccessPolicy{Mode: "members", MemberIDs: []string{"allowed"}}
	if !agent.CanInvoke("allowed") || agent.CanInvoke("denied") {
		t.Fatal("member allowlist was not enforced")
	}
}

func TestAgentEnvironmentRequiresSecretReferences(t *testing.T) {
	agent := AgentDefinition{
		ID: "agent", WorkspaceID: "workspace", Revision: 1, Name: "Agent", Status: AgentDraft,
		ExecutorBinding: ExecutorBinding{Environment: []EnvironmentReference{{Name: "TOKEN", SecretRef: "env:ADRO_TOKEN"}}},
	}
	if err := agent.Validate(); err != nil {
		t.Fatalf("valid environment reference rejected: %v", err)
	}
	agent.ExecutorBinding.Environment[0].SecretRef = "plaintext-value"
	if err := agent.Validate(); err == nil {
		t.Fatal("plaintext environment value was accepted")
	}
}
