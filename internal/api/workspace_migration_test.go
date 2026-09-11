package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/orchestration"
	"github.com/adro-project/adro/internal/workspacebundle"
)

func TestWorkspaceMigrationHTTPExportPreflightImportAndReplay(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "optional")
	source := testServer(t)
	if _, err := source.Store.CreateRequirement(domain.Requirement{ID: "requirement-http", WorkspaceID: "source", Title: "Migrated requirement", Description: "portable", AcceptanceCriteria: []string{"retained"}, AssigneeMemberIDs: []string{"member"}}); err != nil {
		t.Fatal(err)
	}
	agent := orchestration.AgentDefinition{ID: orchestration.NewID(), WorkspaceID: "source", Revision: 1, Name: "Portable agent", Status: orchestration.AgentActive, ExecutorBinding: orchestration.ExecutorBinding{ProviderID: "local", RuntimeID: "codex"}, InputSchema: orchestration.SchemaRef{ID: "input", Version: 1}, OutputSchema: orchestration.SchemaRef{ID: "output", Version: 1}}
	if err := source.Orchestration.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	exported := requestBytes(t, source.Routes(), http.MethodGet, "/api/v1/workspaces/source/migration/export", nil, map[string]string{"X-Workspace-ID": "source"})
	if exported.Code != http.StatusOK || exported.Header().Get("Content-Type") != "application/vnd.adro.workspace+zip" || exported.Header().Get("Digest") == "" {
		t.Fatalf("export status=%d headers=%v body=%s", exported.Code, exported.Header(), exported.Body.String())
	}

	target := testServer(t)
	preflight := requestBytes(t, target.Routes(), http.MethodPost, "/api/v1/workspaces/target/migration/preflight?conflict=rename", exported.Body.Bytes(), map[string]string{"X-Workspace-ID": "target"})
	if preflight.Code != http.StatusOK {
		t.Fatalf("preflight status=%d body=%s", preflight.Code, preflight.Body.String())
	}
	var checked workspacebundle.PreflightReport
	if err := json.Unmarshal(preflight.Body.Bytes(), &checked); err != nil || !checked.Valid || checked.Counts.Requirements != 1 || checked.Counts.Agents != 1 {
		t.Fatalf("preflight=%+v err=%v", checked, err)
	}
	headers := map[string]string{"X-Workspace-ID": "target", "Idempotency-Key": "workspace-import-http"}
	imported := requestBytes(t, target.Routes(), http.MethodPost, "/api/v1/workspaces/target/migration/import?conflict=rename", exported.Body.Bytes(), headers)
	if imported.Code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", imported.Code, imported.Body.String())
	}
	requirements, _ := target.Store.ListRequirements("target", "", "", 10)
	if len(requirements) != 1 || requirements[0].Title != "Migrated requirement" || len(target.Orchestration.ListAgents("target", "")) != 1 {
		t.Fatalf("requirements=%+v agents=%+v", requirements, target.Orchestration.ListAgents("target", ""))
	}
	replayed := requestBytes(t, target.Routes(), http.MethodPost, "/api/v1/workspaces/target/migration/import?conflict=rename", exported.Body.Bytes(), headers)
	if replayed.Code != imported.Code || replayed.Header().Get("Idempotency-Replayed") != "true" || replayed.Body.String() != imported.Body.String() {
		t.Fatalf("replay status=%d header=%q body=%s", replayed.Code, replayed.Header().Get("Idempotency-Replayed"), replayed.Body.String())
	}
}

func TestWorkspaceMigrationHTTPRejectsWorkspaceEscape(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "optional")
	s := testServer(t)
	response := requestBytes(t, s.Routes(), http.MethodGet, "/api/v1/workspaces/other/migration/export", nil, map[string]string{"X-Workspace-ID": "current"})
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func requestBytes(t *testing.T, handler http.Handler, method, target string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}
