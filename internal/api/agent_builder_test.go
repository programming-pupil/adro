package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

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

	untagged := "Here is the configuration:\n```json\n" + strings.TrimSuffix(strings.TrimPrefix(output[strings.Index(output, "<agent_draft>"):], "<agent_draft>"), "</agent_draft>") + "\n```"
	if draft, err := parseAgentDraft(untagged); err != nil || draft.Name != "Release reviewer" {
		t.Fatalf("untagged draft=%+v err=%v", draft, err)
	}

	appServerEvent, err := json.Marshal(map[string]any{
		"method": "item/completed",
		"params": map[string]any{"item": map[string]any{
			"type": "agentMessage",
			"text": "Draft ready.\n" + output[strings.Index(output, "<agent_draft>"):],
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if draft, err := parseAgentDraft(string(appServerEvent)); err != nil || draft.Name != "Release reviewer" {
		t.Fatalf("app-server draft=%+v err=%v event=%s", draft, err, appServerEvent)
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
	var composed struct {
		Evidence struct {
			RunID string `json:"run_id"`
		} `json:"evidence"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &composed); err != nil || composed.Evidence.RunID == "" {
		t.Fatalf("compose evidence=%+v err=%v", composed.Evidence, err)
	}
	owned := request(t, s.Routes(), http.MethodGet, "/api/v1/runs/"+composed.Evidence.RunID, "", map[string]string{"X-Workspace-ID": "local"})
	if owned.Code != http.StatusOK {
		t.Fatalf("owned compose run=%d %s", owned.Code, owned.Body.String())
	}
	foreign := request(t, s.Routes(), http.MethodGet, "/api/v1/runs/"+composed.Evidence.RunID, "", map[string]string{"X-Workspace-ID": "other"})
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign compose run=%d %s", foreign.Code, foreign.Body.String())
	}
}

func TestComposeAgentDraftAsyncPublishesProgressAndFinalDraft(t *testing.T) {
	bus := events.NewBus()
	fs, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload := `<agent_draft>{"name":"Streaming designer","description":"Designs observable agents.","role":"designer","instructions":"Design and verify agent configurations.","conversation_starters":[],"access_policy":{"mode":"private"},"network_access":false,"max_concurrent_tasks":1,"token_budget":120000,"tool_call_budget":200}</agent_draft>`
	progress := `{"method":"item/completed","params":{"item":{"id":"message-1","type":"agentMessage","text":"Preparing the first configuration draft."}}}`
	finalEvent, err := json.Marshal(map[string]any{
		"method": "item/completed",
		"params": map[string]any{"item": map[string]any{
			"id": "message-2", "type": "agentMessage", "text": "Draft complete.\n" + payload,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	script := "printf '%s\\n' '" + progress + "'; sleep 0.6; printf '%s\\n' '" + string(finalEvent) + "'"
	executor := provider.NewLocalProvider("/bin/sh", []string{"-c", script}, t.TempDir(), bus)
	s := New(store.NewMemory(), executor, fs, bus, nil)

	started := request(t, s.Routes(), http.MethodPost, "/api/v1/workspaces/local/agents/compose?async=true", `{"prompt":"Create an observable designer","runtime_id":"local"}`, map[string]string{"X-Workspace-ID": "local", "Idempotency-Key": "compose-async"})
	if started.Code != http.StatusAccepted {
		t.Fatalf("async compose status=%d body=%s", started.Code, started.Body.String())
	}
	var startBody struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &startBody); err != nil || startBody.RunID == "" {
		t.Fatalf("async compose response=%s err=%v", started.Body.String(), err)
	}

	var progressBody string
	progressDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(progressDeadline) {
		response := request(t, s.Routes(), http.MethodGet, "/api/v1/workspaces/local/agents/compose/"+startBody.RunID, "", map[string]string{"X-Workspace-ID": "local"})
		progressBody = response.Body.String()
		if response.Code == http.StatusOK && strings.Contains(progressBody, "Preparing the first configuration draft") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(progressBody, "Preparing the first configuration draft") {
		t.Fatalf("async compose exposed no live progress: %s", progressBody)
	}

	var finalBody string
	finalDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(finalDeadline) {
		response := request(t, s.Routes(), http.MethodGet, "/api/v1/workspaces/local/agents/compose/"+startBody.RunID+"?runtime_id=forged&model=forged", "", map[string]string{"X-Workspace-ID": "local"})
		finalBody = response.Body.String()
		if strings.Contains(finalBody, `"status":"completed"`) && strings.Contains(finalBody, `"Streaming designer"`) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(finalBody, `"Streaming designer"`) {
		t.Fatalf("async compose did not return final draft: %s", finalBody)
	}
	if strings.Contains(finalBody, "forged") {
		t.Fatalf("async compose trusted client-supplied evidence metadata: %s", finalBody)
	}
	if audits := s.Audit.List(); len(audits) == 0 || audits[len(audits)-1].Action != "agent.draft.compose.started" {
		t.Fatalf("async compose start was not audited: %+v", audits)
	}
}

func TestAgentDraftProgressItemsHideProtocolPayloads(t *testing.T) {
	output := strings.Join([]string{
		`{"method":"item/started","params":{"item":{"id":"tool-1","type":"commandExecution","command":"pwd"}}}`,
		`{"method":"item/completed","params":{"item":{"id":"tool-1","type":"commandExecution","command":"pwd","aggregatedOutput":"/workspace"}}}`,
		`{"method":"item/completed","params":{"item":{"id":"message-1","type":"agentMessage","text":"Draft ready.\n<agent_draft>{\"name\":\"Hidden\"}</agent_draft>\nADRO_RESULT_JSON={\"outcome\":\"pass\"}"}}}`,
	}, "\n")
	items := agentDraftProgressItems(output)
	if len(items) != 3 {
		t.Fatalf("progress items=%+v", items)
	}
	if items[2].Text != "Draft ready." || strings.Contains(items[2].Text, "agent_draft") || strings.Contains(items[2].Text, "ADRO_RESULT_JSON") {
		t.Fatalf("assistant progress leaked protocol payload: %+v", items[2])
	}
}

func TestAgentDraftProgressRequiresManagementPermission(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "required")
	t.Setenv("ADRO_ADMIN_USERNAME", "admin")
	t.Setenv("ADRO_ADMIN_PASSWORD", "AdminPass123!")
	t.Setenv("ADRO_AUTH_STATE_FILE", "")
	s := testServer(t)
	adminToken := loginToken(t, s, "admin", "AdminPass123!")
	created := request(t, s.Routes(), http.MethodPost, "/api/v1/users", `{"username":"delivery.viewer","display_name":"Delivery Viewer","password":"ViewerPass123!","role":"member","status":"active","menu_ids":["delivery"]}`, bearer(adminToken))
	if created.Code != http.StatusCreated {
		t.Fatalf("create viewer status=%d body=%s", created.Code, created.Body.String())
	}
	viewerToken := loginToken(t, s, "delivery.viewer", "ViewerPass123!")
	response := request(t, s.Routes(), http.MethodGet, "/api/v1/workspaces/local/agents/compose/private-run", "", bearer(viewerToken))
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "orchestration_manage_permission_denied") {
		t.Fatalf("progress permission status=%d body=%s", response.Code, response.Body.String())
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
