package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/adro-project/adro/internal/artifact"
	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/provider"
	"github.com/adro-project/adro/internal/store"
)

func TestParseAgentDraftValidatesStructuredConfiguration(t *testing.T) {
	output := `analysis omitted
<agent_draft>{"name":"Release reviewer","description":"Reviews release readiness.","role":"reviewer","instructions":"Review evidence before approving.","conversation_starters":[{"label":"Review","prompt":"Review this release."}],"access_policy":{"mode":"private"},"network_access":false,"max_concurrent_tasks":2,"token_budget":50000,"tool_call_budget":100}</agent_draft>`
	draft, err := parseAgentDraft(output)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Name != "Release reviewer" || draft.MaxConcurrentTasks != 2 || len(draft.ConversationStarters) != 1 {
		t.Fatalf("draft=%+v", draft)
	}

	bad := strings.Replace(output, `"max_concurrent_tasks":2`, `"max_concurrent_tasks":0`, 1)
	if _, err := parseAgentDraft(bad); err == nil {
		t.Fatal("invalid execution budget accepted")
	}
	unknown := strings.Replace(output, `"network_access":false`, `"secret":"value","network_access":false`, 1)
	if _, err := parseAgentDraft(unknown); err == nil {
		t.Fatal("unknown draft field accepted")
	}
}

func TestComposeAgentDraftUsesSelectedRuntimeAndReturnsEvidence(t *testing.T) {
	bus := events.NewBus()
	fs, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload := `<agent_draft>{"name":"Test designer","description":"Designs focused tests.","role":"tester","instructions":"Design and verify tests.","conversation_starters":[],"access_policy":{"mode":"private"},"network_access":false,"max_concurrent_tasks":1,"token_budget":120000,"tool_call_budget":200}</agent_draft>`
	executor := provider.NewLocalProvider("/usr/bin/printf", []string{payload}, t.TempDir(), bus)
	s := New(store.NewMemory(), executor, fs, bus, nil)

	response := request(t, s.Routes(), http.MethodPost, "/api/v1/workspaces/local/agents/compose", `{"prompt":"Create a test designer","runtime_id":"local"}`, map[string]string{"X-Workspace-ID": "local", "Idempotency-Key": "compose-one"})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"Test designer"`) || !strings.Contains(response.Body.String(), `"output_sha256"`) {
		t.Fatalf("compose status=%d body=%s", response.Code, response.Body.String())
	}
	if got := s.Orchestration.ListAgents("local", ""); len(got) != 0 {
		t.Fatalf("compose unexpectedly persisted %d Agents", len(got))
	}
}

func TestComposeAgentDraftRejectsInvalidRequests(t *testing.T) {
	s := testServer(t)
	for _, body := range []string{
		`{"prompt":"","runtime_id":"local"}`,
		`{"prompt":"create one","runtime_id":"missing"}`,
	} {
		response := request(t, s.Routes(), http.MethodPost, "/api/v1/workspaces/local/agents/compose", body, map[string]string{"X-Workspace-ID": "local"})
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("body=%s status=%d response=%s", body, response.Code, response.Body.String())
		}
	}
}

func TestAgentBuilderContextFiltersUnavailableResourcesAndSensitiveRuntimeData(t *testing.T) {
	s := testServer(t)
	for _, skill := range []domain.Skill{
		{ID: "skill-active", WorkspaceID: "w1", Name: "Active", Version: "1", Status: "active"},
		{ID: "skill-disabled", WorkspaceID: "w1", Name: "Disabled", Version: "1", Status: "disabled"},
	} {
		if _, err := s.Store.UpsertSkill(skill); err != nil {
			t.Fatal(err)
		}
	}
	for _, server := range []domain.MCPServer{
		{ID: "mcp-active", WorkspaceID: "w1", Name: "Active MCP", Protocol: "http", Endpoint: "https://mcp.example.test", SecretRef: "env:MCP_TOKEN", Status: "configured"},
		{ID: "mcp-disabled", WorkspaceID: "w1", Name: "Disabled MCP", Protocol: "http", Endpoint: "https://disabled.example.test", Status: "disabled"},
	} {
		if _, err := s.Store.UpsertMCPServer(server); err != nil {
			t.Fatal(err)
		}
	}

	context := s.agentBuilderContext("w1", map[string]any{
		"name":           "Release reviewer",
		"runtime_config": map[string]any{"api_key": "MUST_NOT_LEAK"},
		"environment":    []any{map[string]any{"name": "TOKEN", "secret_ref": "env:SECRET"}},
		"custom_args":    []any{"--token", "MUST_NOT_LEAK"},
		"password":       "MUST_NOT_LEAK",
	})
	for _, key := range []string{"runtime_config", "environment", "custom_args", "password"} {
		if _, exists := context[key]; exists {
			t.Fatalf("agent builder context retained sensitive field %q: %#v", key, context)
		}
	}
	prompt, err := agentBuilderPrompt("Create a reviewer", context)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"MUST_NOT_LEAK", "skill-disabled", "mcp-disabled", "env:MCP_TOKEN", "mcp.example.test"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("agent builder prompt leaked %q: %s", forbidden, prompt)
		}
	}
	for _, required := range []string{"Release reviewer", "skill-active", "mcp-active"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("agent builder prompt omitted %q: %s", required, prompt)
		}
	}
}
