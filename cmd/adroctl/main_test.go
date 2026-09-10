package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/orchestration"
	"github.com/adro-project/adro/internal/store"
)

func TestOrchestrationRequestRoutes(t *testing.T) {
	tests := []struct {
		name         string
		resource     string
		action       string
		opts         apiOptions
		method       string
		path         string
		bodyRequired bool
	}{
		{"agent list", "agent", "list", apiOptions{Workspace: "team/a", Status: "active"}, http.MethodGet, "/api/v1/workspaces/team%2Fa/agents?status=active", false},
		{"agent get", "agent", "get", apiOptions{Workspace: "local", ID: "agent one"}, http.MethodGet, "/api/v1/agents/agent%20one?workspace_id=local", false},
		{"agent create", "agent", "create", apiOptions{Workspace: "local"}, http.MethodPost, "/api/v1/workspaces/local/agents", true},
		{"squad dry run", "squad", "dry-run", apiOptions{Workspace: "local", ID: "squad"}, http.MethodPost, "/api/v1/squads/squad/dry-run?workspace_id=local", false},
		{"plan create", "plan", "create", apiOptions{RequirementID: "req/1"}, http.MethodPost, "/api/v1/requirements/req%2F1/execution-plan", true},
		{"plan publish", "plan", "publish", apiOptions{RequirementID: "req"}, http.MethodPost, "/api/v1/requirements/req/execution-plan/publish", true},
		{"plan timeline", "plan", "timeline", apiOptions{ID: "plan"}, http.MethodGet, "/api/v1/plans/plan/timeline", false},
		{"run replay", "plan", "replay", apiOptions{ID: "run"}, http.MethodGet, "/api/v1/runs/run/replay", false},
		{"run diagnostics", "plan", "diagnostics", apiOptions{ID: "run"}, http.MethodGet, "/api/v1/runs/run/diagnostics", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			method, path, required, err := orchestrationRequest(test.resource, test.action, test.opts)
			if err != nil {
				t.Fatal(err)
			}
			if method != test.method || path != test.path || required != test.bodyRequired {
				t.Fatalf("got method=%s path=%s required=%v, want method=%s path=%s required=%v", method, path, required, test.method, test.path, test.bodyRequired)
			}
		})
	}
}

func TestWorkspaceCommandExportsPreflightsAndImportsFreshHome(t *testing.T) {
	sourceHome := filepath.Join(t.TempDir(), "source-home")
	sourceArtifacts := filepath.Join(t.TempDir(), "source-artifacts")
	sourceStore, err := store.NewPersistentMemory(filepath.Join(sourceHome, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sourceStore.CreateRequirement(domain.Requirement{ID: "requirement-cli", WorkspaceID: "source", Title: "CLI migration", Description: "round trip", AcceptanceCriteria: []string{"retained"}, AssigneeMemberIDs: []string{"member"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceStore.UpsertMCPServer(domain.MCPServer{ID: "mcp-cli", WorkspaceID: "source", Name: "CLI MCP", Endpoint: "https://mcp.example.test", Status: "configured"}); err != nil {
		t.Fatal(err)
	}
	sourceDefinitions, err := orchestration.NewPersistentRepository(filepath.Join(sourceHome, "orchestration.json"))
	if err != nil {
		t.Fatal(err)
	}
	agent := orchestration.AgentDefinition{ID: orchestration.NewID(), WorkspaceID: "source", Revision: 1, Name: "CLI agent", Status: orchestration.AgentActive, ExecutorBinding: orchestration.ExecutorBinding{ProviderID: "local", RuntimeID: "codex"}, InputSchema: orchestration.SchemaRef{ID: "input", Version: 1}, OutputSchema: orchestration.SchemaRef{ID: "output", Version: 1}}
	if err := sourceDefinitions.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "workspace.zip")
	var output bytes.Buffer
	if err := workspaceCommand([]string{"export", "--home", sourceHome, "--artifact-root", sourceArtifacts, "--workspace", "source", "--file", bundle}, &output); err != nil {
		t.Fatal(err)
	}
	if output.Len() == 0 {
		t.Fatal("export did not print a receipt")
	}
	output.Reset()
	if err := workspaceCommand([]string{"preflight", "--workspace", "target", "--conflict", "rename", "--file", bundle}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"valid": true`) || !strings.Contains(output.String(), `"requirements": 1`) || !strings.Contains(output.String(), `"mcp_servers": 1`) {
		t.Fatalf("preflight output=%s", output.String())
	}
	targetHome := filepath.Join(t.TempDir(), "target-home")
	targetArtifacts := filepath.Join(t.TempDir(), "target-artifacts")
	output.Reset()
	if err := workspaceCommand([]string{"import", "--home", targetHome, "--artifact-root", targetArtifacts, "--workspace", "target", "--conflict", "rename", "--file", bundle}, &output); err != nil {
		t.Fatal(err)
	}
	importedStore, err := store.NewPersistentMemory(filepath.Join(targetHome, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	requirements, _ := importedStore.ListRequirements("target", "", "", 10)
	importedDefinitions, err := orchestration.NewPersistentRepository(filepath.Join(targetHome, "orchestration.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(requirements) != 1 || requirements[0].Title != "CLI migration" || len(importedDefinitions.ListAgents("target", "")) != 1 {
		t.Fatalf("requirements=%+v agents=%+v output=%s", requirements, importedDefinitions.ListAgents("target", ""), output.String())
	}
}

func TestWorkspaceCommandDefaultsMatchNativeStartupState(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
	t.Setenv("ADRO_HOME", "")
	t.Setenv("ADRO_ARTIFACT_ROOT", "")
	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, ".adro")
	definitions, err := orchestration.NewPersistentRepository(filepath.Join(home, "orchestration.json"))
	if err != nil {
		t.Fatal(err)
	}
	agent := orchestration.AgentDefinition{ID: orchestration.NewID(), WorkspaceID: "local", Revision: 1, Name: "Startup agent", Status: orchestration.AgentActive, ExecutorBinding: orchestration.ExecutorBinding{ProviderID: "local", RuntimeID: "codex"}, InputSchema: orchestration.SchemaRef{ID: "input", Version: 1}, OutputSchema: orchestration.SchemaRef{ID: "output", Version: 1}}
	if err := definitions.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(root, "workspace.zip")
	if err := workspaceCommand([]string{"export", "--workspace", "local", "--file", bundle}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(bundle); err != nil || info.Size() == 0 {
		t.Fatalf("default startup-state export: info=%v err=%v", info, err)
	}
}

func TestWorkspacePostgresCommandsRequireExplicitSource(t *testing.T) {
	for _, action := range []string{"export-postgres", "preflight-postgres", "import-postgres"} {
		t.Run(action, func(t *testing.T) {
			args := []string{action}
			if action == "export-postgres" {
				args = append(args, "--file", filepath.Join(t.TempDir(), "workspace.zip"))
			}
			err := workspaceCommand(args, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "--source-dsn") {
				t.Fatalf("error=%v, want explicit source DSN requirement", err)
			}
		})
	}

	err := workspaceCommand([]string{"export-postgres", "--source-dsn", "redacted", "--source-workspace", "source"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--file") {
		t.Fatalf("error=%v, want output file requirement", err)
	}
}

func TestOrchestrationRequestRejectsMissingIdentity(t *testing.T) {
	for _, test := range []struct{ resource, action string }{{"agent", "get"}, {"squad", "publish"}, {"plan", "timeline"}} {
		if _, _, _, err := orchestrationRequest(test.resource, test.action, apiOptions{}); err == nil || !strings.Contains(err.Error(), "--id") {
			t.Fatalf("%s %s error=%v, want --id requirement", test.resource, test.action, err)
		}
	}
	if _, _, _, err := orchestrationRequest("plan", "create", apiOptions{}); err == nil || !strings.Contains(err.Error(), "--requirement") {
		t.Fatalf("plan create error=%v, want --requirement", err)
	}
}

func TestRequestBodyValidation(t *testing.T) {
	if _, err := requestBody("", true); err == nil {
		t.Fatal("required body was accepted")
	}
	path := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(path, []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := requestBody(path, true)
	if err != nil || string(body) != `{"ok":true}` {
		t.Fatalf("body=%s err=%v", body, err)
	}
	if err := os.WriteFile(path, []byte(`{"broken"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := requestBody(path, true); err == nil {
		t.Fatal("invalid JSON was accepted")
	}
}

func TestDoAPIRequestMapsHeadersAndBody(t *testing.T) {
	var received struct {
		method string
		path   string
		body   string
		header http.Header
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		received.method, received.path, received.body, received.header = r.Method, r.URL.RequestURI(), string(data), r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer server.Close()

	body, err := doAPIRequest(http.MethodPost, "/api/v1/workspaces/local/agents?status=draft", []byte(`{"name":"agent"}`), apiOptions{
		BaseURL: server.URL + "/", Workspace: "local", Tenant: "tenant", Token: "secret-token", IdempotencyKey: "create-agent",
	})
	if err != nil || string(body) != `{"accepted":true}` {
		t.Fatalf("body=%s err=%v", body, err)
	}
	if received.method != http.MethodPost || received.path != "/api/v1/workspaces/local/agents?status=draft" || received.body != `{"name":"agent"}` {
		t.Fatalf("request=%+v", received)
	}
	wants := map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Workspace-ID":  "local",
		"X-Tenant-ID":     "tenant",
		"Authorization":   "Bearer secret-token",
		"Idempotency-Key": "create-agent",
	}
	for key, want := range wants {
		if got := received.header.Get(key); got != want {
			t.Fatalf("%s=%q, want %q", key, got, want)
		}
	}
}

func TestDoAPIRequestReturnsBoundedAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error_code":"idempotency_key_conflict"}`))
	}))
	defer server.Close()
	_, err := doAPIRequest(http.MethodPost, "/api/v1/test", nil, apiOptions{BaseURL: server.URL})
	if err == nil || !strings.Contains(err.Error(), "409 Conflict") || !strings.Contains(err.Error(), "idempotency_key_conflict") {
		t.Fatalf("error=%v", err)
	}
}
