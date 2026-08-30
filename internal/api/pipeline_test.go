package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/adro-project/adro/internal/artifact"
	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/provider"
	"github.com/adro-project/adro/internal/store"
)

const (
	designAgent = "11111111-1111-1111-1111-111111111111"
	devAgent    = "22222222-2222-2222-2222-222222222222"
	testAgent   = "33333333-3333-3333-3333-333333333333"
	arbAgent    = "44444444-4444-4444-4444-444444444444"
)

func TestPipelineAPIUsesRealMulticaIssuesAndCommentContinuity(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "optional")
	var mu sync.Mutex
	issueCount := 0
	comments := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer real-token" {
			t.Errorf("missing Multica bearer")
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/issues":
			mu.Lock()
			issueCount++
			id := "issue-" + string(rune('0'+issueCount))
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments"):
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			mu.Lock()
			comments = append(comments, payload["content"].(string))
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "continuation-comment"})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/task-runs"):
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id": "continuation-task", "agent_id": devAgent,
				"trigger_comment_id": "continuation-comment", "status": "queued",
				"prior_session_id": "session-original", "prior_work_dir": "/repo",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	control := store.NewMemory()
	requirement, err := control.CreateRequirement(domain.Requirement{
		WorkspaceID: "workspace", Title: "ship", Description: "real pipeline",
		AcceptanceCriteria: []string{"all tests pass"}, AssigneeMemberIDs: []string{"member"},
	})
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	fs, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	multica := provider.NewMulticaProvider(upstream.URL, "real-token")
	multica.DefaultWorkspaceID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	server := New(control, multica, fs, bus, slog.New(slog.NewTextHandler(io.Discard, nil)))

	create := pipelineRequest(t, server, http.MethodPost, "/api/v1/pipelines", map[string]any{
		"requirement_id": requirement.ID,
		"roles":          map[string]any{"designer_agent_id": designAgent, "developer_agent_id": devAgent, "tester_agent_id": testAgent, "arbitrator_agent_id": arbAgent},
		"max_retries":    3, "coverage_threshold": 80,
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", create.Code, create.Body.String())
	}
	var run domain.PipelineRun
	if err := json.Unmarshal(create.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.PipelineStage != 1 || run.Status != domain.PipelineWaiting || run.SessionID == "" {
		t.Fatalf("created=%+v", run)
	}

	run = pipelineResult(t, server, run.ID, map[string]any{"stage": 1, "agent_id": designAgent, "outcome": "pass", "design_doc": "design"}, http.StatusOK)
	devIssue := run.ActiveProviderIssueID
	run = pipelineResult(t, server, run.ID, map[string]any{"stage": 2, "agent_id": devAgent, "outcome": "pass", "code_version": "a1", "provider_session_id": "session-original", "provider_work_dir": "/repo"}, http.StatusOK)
	run = pipelineResult(t, server, run.ID, map[string]any{"stage": 3, "agent_id": testAgent, "outcome": "pass", "coverage": 91}, http.StatusOK)
	run = pipelineResult(t, server, run.ID, map[string]any{"stage": 4, "agent_id": testAgent, "outcome": "fail", "failed_tests": []string{"integration"}, "error_log": "boom"}, http.StatusOK)
	run = pipelineResult(t, server, run.ID, map[string]any{"stage": 5, "agent_id": arbAgent, "outcome": "pass", "repair_note": "incremental fix"}, http.StatusOK)
	if run.PipelineStage != 2 || run.ActiveProviderIssueID != devIssue || run.RetryCount != 1 {
		t.Fatalf("continuation=%+v", run)
	}
	mu.Lock()
	commentText := strings.Join(comments, "\n")
	createdIssues := issueCount
	mu.Unlock()
	if !strings.Contains(commentText, "mention://agent/"+devAgent) || createdIssues != 5 {
		t.Fatalf("repair did not use same-issue comment: issues=%d comments=%q", createdIssues, commentText)
	}

	rejected := pipelineRequest(t, server, http.MethodPost, "/api/v1/pipelines/"+run.ID+"/results", map[string]any{
		"stage": 2, "agent_id": devAgent, "outcome": "pass", "code_version": "a2", "provider_session_id": "fresh-session", "provider_work_dir": "/repo",
	})
	if rejected.Code != http.StatusConflict {
		t.Fatalf("fresh session accepted=%d %s", rejected.Code, rejected.Body.String())
	}
	run = pipelineResult(t, server, run.ID, map[string]any{"stage": 2, "agent_id": devAgent, "outcome": "pass", "code_version": "a2", "provider_session_id": "session-original", "provider_work_dir": "/repo"}, http.StatusOK)
	if run.PipelineStage != 3 || run.ParentSessionID != "session-original" {
		t.Fatalf("same session repair=%+v", run)
	}
}

func pipelineRequest(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func pipelineResult(t *testing.T, handler http.Handler, id string, body any, status int) domain.PipelineRun {
	t.Helper()
	rec := pipelineRequest(t, handler, http.MethodPost, "/api/v1/pipelines/"+id+"/results", body)
	if rec.Code != status {
		t.Fatalf("result=%d %s", rec.Code, rec.Body.String())
	}
	var run domain.PipelineRun
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	return run
}
