package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adro-project/adro/internal/events"
	"github.com/gorilla/websocket"
)

func TestMulticaProviderUsesBearerAndMapsCapabilities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing bearer")
		}
		if r.URL.Path != "/api/capabilities" {
			t.Errorf("path=%s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Capabilities{AdapterVersion: "1", Features: []string{"issue.child.v1"}})
	}))
	defer srv.Close()
	p := NewMulticaProvider(srv.URL, "secret")
	caps, err := p.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if caps.Provider != "multica" || len(caps.Features) != 1 {
		t.Fatalf("caps=%+v", caps)
	}
}

func TestCapabilitiesAcceptFeatureStatusMapShapes(t *testing.T) {
	var caps Capabilities
	if err := json.Unmarshal([]byte(`{"provider":"multica","features":{"run.snapshot.v1":"unsupported","attachment.v1":true},"capabilities":["run.messages.v1"]}`), &caps); err != nil {
		t.Fatal(err)
	}
	if caps.Supports("run.snapshot.v1") {
		t.Fatal("unsupported feature reported as supported")
	}
	if !caps.Supports("attachment.v1") || !caps.Supports("run.messages.v1") {
		t.Fatalf("capabilities=%+v", caps)
	}
}

func TestMulticaProviderFallsBackToVersionedCapabilities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/capabilities" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/api/v1/capabilities" {
			t.Errorf("path=%s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Capabilities{ServerVersion: "0.4.35", Features: []string{"attachment.v1"}})
	}))
	defer srv.Close()
	caps, err := NewMulticaProvider(srv.URL, "").Capabilities(context.Background())
	if err != nil || caps.Provider != "multica" || caps.ServerVersion != "0.4.35" {
		t.Fatalf("caps=%+v err=%v", caps, err)
	}
}

func TestMulticaProviderFallsBackToPublicConfigHandshake(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/capabilities" || r.URL.Path == "/api/v1/capabilities" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/api/config" {
			t.Errorf("path=%s", r.URL.Path)
			return
		}
		_ = json.NewEncoder(w).Encode(multicaConfigResponse{ServerVersion: "0.4.35"})
	}))
	defer srv.Close()
	caps, err := NewMulticaProvider(srv.URL, "").Capabilities(context.Background())
	if err != nil || caps.Provider != "multica" || caps.AdapterVersion != "api-config-v1" || caps.ServerVersion != "0.4.35" {
		t.Fatalf("caps=%+v err=%v", caps, err)
	}
	if len(caps.Features) != 1 || caps.Features[0] != "api.config.v1" {
		t.Fatalf("unexpected conservative features: %+v", caps.Features)
	}
}

func TestMulticaProviderParsesMulticaReadinessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"checks": map[string]string{"db": "ok", "migrations": "ok"},
		})
	}))
	defer srv.Close()
	health, err := NewMulticaProvider(srv.URL, "").Health(context.Background())
	if err != nil || !health.Healthy || health.Message != "" {
		t.Fatalf("health=%+v err=%v", health, err)
	}
}

func TestMulticaProviderPublishesMultipartAttachment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/attachments" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("request path=%s auth=%s", r.URL.Path, r.Header.Get("Authorization"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("target_type") != "comment" || r.FormValue("target_id") != "c1" {
			t.Fatalf("target=%s/%s", r.FormValue("target_type"), r.FormValue("target_id"))
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if header.Filename != "screen.png" {
			t.Fatalf("filename=%s", header.Filename)
		}
		_ = json.NewEncoder(w).Encode(AttachmentReceipt{ID: "provider-attachment-1", Status: "accepted"})
	}))
	defer srv.Close()
	receipt, err := NewMulticaProvider(srv.URL, "secret").PublishAttachment(context.Background(), AttachmentSpec{TargetType: "comment", TargetID: "c1", Filename: "screen.png", MediaType: "image/png", ArtifactURI: "artifact://t/a/1", Content: []byte("png")})
	if err != nil || receipt.Status != "accepted" || receipt.ArtifactURI != "artifact://t/a/1" {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestMulticaProviderCreatesIssueWithRealContract(t *testing.T) {
	workspaceID := "11111111-1111-4111-8111-111111111111"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/issues" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("workspace_id"); got != workspaceID {
			t.Fatalf("workspace_id=%q", got)
		}
		var body multicaCreateIssueRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Title != "REQ-1 / provider" || body.Description != "change contract" || body.Status != "todo" || body.Priority != "none" || body.Stage == nil || *body.Stage != 1 {
			t.Fatalf("issue body=%+v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "provider-issue-1", "identifier": "CTW-12"})
	}))
	defer srv.Close()
	p := NewMulticaProvider(srv.URL, "secret")
	binding, err := p.CreateWorkItem(context.Background(), WorkItemSpec{
		ID: "local-work-item", WorkspaceID: workspaceID, Title: "REQ-1 / provider", Description: "change contract", Stage: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if binding.ID != "local-work-item" || binding.ProviderIssueID != "provider-issue-1" {
		t.Fatalf("binding=%+v", binding)
	}
	if !p.AuthenticationVerified() {
		t.Fatal("successful authenticated issue creation did not verify authentication")
	}
}

func TestMulticaProviderDiscoversWorkspaceAndRuntimeForAgent(t *testing.T) {
	workspaceID := "11111111-1111-4111-8111-111111111111"
	runtimeID := "22222222-2222-4222-8222-222222222222"
	agentID := "33333333-3333-4333-8333-333333333333"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/workspaces":
			_ = json.NewEncoder(w).Encode([]multicaResourceResponse{{ID: workspaceID}})
		case "/api/runtimes":
			if r.URL.Query().Get("workspace_id") != workspaceID {
				t.Fatalf("runtime workspace=%q", r.URL.Query().Get("workspace_id"))
			}
			_ = json.NewEncoder(w).Encode([]multicaResourceResponse{{ID: runtimeID, Status: "online"}})
		case "/api/agents":
			if r.Method != http.MethodPost || r.URL.Query().Get("workspace_id") != workspaceID {
				t.Fatalf("agent request=%s %s", r.Method, r.URL.String())
			}
			var body multicaCreateAgentRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Name != "Delivery Agent" || body.Instructions != "ship with evidence" || body.RuntimeID != runtimeID || body.Visibility != "private" || body.MaxConcurrentTasks != 1 {
				t.Fatalf("agent body=%+v", body)
			}
			_ = json.NewEncoder(w).Encode(multicaResourceResponse{ID: agentID})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := NewMulticaProvider(srv.URL, "secret")
	binding, err := p.EnsureAgent(context.Background(), AgentSpec{WorkspaceID: "local", Name: "Delivery Agent", Instructions: "ship with evidence"})
	if err != nil {
		t.Fatal(err)
	}
	if binding.ProviderAgentID != agentID || binding.Provider != "multica" || !p.AuthenticationVerified() {
		t.Fatalf("binding=%+v authenticated=%v", binding, p.AuthenticationVerified())
	}
}

func TestMulticaProviderRejectsAmbiguousOnlineRuntimeDiscovery(t *testing.T) {
	workspaceID := "11111111-1111-4111-8111-111111111111"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/workspaces":
			_ = json.NewEncoder(w).Encode([]multicaResourceResponse{{ID: workspaceID}})
		case "/api/runtimes":
			_ = json.NewEncoder(w).Encode([]multicaResourceResponse{
				{ID: "22222222-2222-4222-8222-222222222222", Status: "online"},
				{ID: "33333333-3333-4333-8333-333333333333", Status: "online"},
			})
		default:
			t.Fatalf("unexpected request=%s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	p := NewMulticaProvider(srv.URL, "secret")
	_, err := p.EnsureAgent(context.Background(), AgentSpec{WorkspaceID: "local", Name: "Delivery Agent"})
	if err == nil || !strings.Contains(err.Error(), "multiple online Multica runtimes") {
		t.Fatalf("expected ambiguous runtime error, got %v", err)
	}
}

func TestMulticaProviderUsesConfiguredWorkspaceForLocalWorkItem(t *testing.T) {
	workspaceID := "11111111-1111-4111-8111-111111111111"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/issues" || r.URL.Query().Get("workspace_id") != workspaceID {
			t.Fatalf("request=%s", r.URL.String())
		}
		_ = json.NewEncoder(w).Encode(multicaIssueResponse{ID: "provider-issue"})
	}))
	defer srv.Close()
	p := NewMulticaProvider(srv.URL, "secret")
	p.DefaultWorkspaceID = workspaceID
	created, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "work-item", WorkspaceID: "local", Title: "Real issue"})
	if err != nil || created.ProviderIssueID != "provider-issue" {
		t.Fatalf("created=%+v err=%v", created, err)
	}
}

func TestMulticaProviderUsesDefaultProjectForWorkItem(t *testing.T) {
	workspaceID := "11111111-1111-4111-8111-111111111111"
	projectID := "22222222-2222-4222-8222-222222222222"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body multicaCreateIssueRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.ProjectID != projectID {
			t.Fatalf("project_id=%q", body.ProjectID)
		}
		_ = json.NewEncoder(w).Encode(multicaIssueResponse{ID: "provider-issue"})
	}))
	defer srv.Close()
	p := NewMulticaProvider(srv.URL, "secret")
	p.DefaultWorkspaceID = workspaceID
	p.DefaultProjectID = projectID
	if _, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "work-item", WorkspaceID: "local", Title: "Real issue"}); err != nil {
		t.Fatal(err)
	}
}

func TestMulticaProviderRejectsEmptyWorkItemTitle(t *testing.T) {
	_, err := NewMulticaProvider("http://example.test", "").CreateWorkItem(context.Background(), WorkItemSpec{WorkspaceID: "11111111-1111-4111-8111-111111111111"})
	if err == nil || !strings.Contains(err.Error(), "title is required") {
		t.Fatalf("err=%v", err)
	}
}

func TestMulticaProviderFallsBackToNativeIssueRerun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/runs":
			http.NotFound(w, r)
		case r.URL.Path == "/api/issues/provider-issue-1/rerun":
			if r.Method != http.MethodPost {
				t.Fatalf("method=%s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "task-1", "status": "queued"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	binding, err := NewMulticaProvider(srv.URL, "").StartRun(context.Background(), StartRunCommand{WorkItemID: "work-1", ProviderIssueID: "provider-issue-1", Input: "run"})
	if err != nil {
		t.Fatal(err)
	}
	if binding.ID != "task-1" || binding.ProviderRunID != "task-1" {
		t.Fatalf("binding=%+v", binding)
	}
}

func TestMulticaProviderStreamsWebSocketEvents(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("run_id") != "run-1" || r.URL.Query().Get("cursor") != "cursor-1" {
			t.Fatalf("query=%s", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("missing auth")
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_ = conn.WriteJSON(events.New("execution.completed.v1", "run", "run-1", "t", "w", 1, map[string]any{"ok": true}))
		_, _, _ = conn.ReadMessage()
	}))
	defer srv.Close()
	p := NewMulticaProvider(strings.Replace(srv.URL, "http://", "ws://", 1), "secret")
	p.WebSocketURL = strings.Replace(srv.URL, "http://", "ws://", 1)
	stream, err := p.StreamEvents(context.Background(), "run-1", "cursor-1")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	select {
	case event := <-stream.Events:
		if event.EventType != "execution.completed.v1" {
			t.Fatalf("event=%+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket event")
	}
}
