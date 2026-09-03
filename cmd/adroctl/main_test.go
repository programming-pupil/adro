package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
