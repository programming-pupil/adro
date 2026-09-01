package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/adro-project/adro/internal/artifact"
	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/provider"
	"github.com/adro-project/adro/internal/store"
)

func TestWorkflowTemplateDesignApprovalAndOptionalStages(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "optional")
	control := store.NewMemory()
	req, err := control.CreateRequirement(domain.Requirement{WorkspaceID: "w", Title: "approval", Description: "design gate", AcceptanceCriteria: []string{"report"}, AssigneeMemberIDs: []string{"member"}})
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	fs, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := New(control, provider.NewLocalProvider("/usr/bin/true", nil, t.TempDir(), bus), fs, bus, slog.New(slog.NewTextHandler(io.Discard, nil)))
	steps := []map[string]any{
		{"id": "design", "stage": 1, "agent_id": "designer", "required": true},
		{"id": "report", "stage": 7, "agent_id": "reporter", "required": true},
	}
	templateResponse := request(t, server.Routes(), http.MethodPost, "/api/v1/workflow-templates", mustJSONWorkflow(map[string]any{"workspace_id": "w", "name": "design-gate", "mode": "design_approval", "steps": steps}), map[string]string{"X-Workspace-ID": "w"})
	if templateResponse.Code != http.StatusCreated {
		t.Fatalf("template=%d %s", templateResponse.Code, templateResponse.Body.String())
	}
	var template domain.WorkflowTemplate
	if err := json.Unmarshal(templateResponse.Body.Bytes(), &template); err != nil {
		t.Fatal(err)
	}
	pipelineResponse := request(t, server.Routes(), http.MethodPost, "/api/v1/pipelines", mustJSONWorkflow(map[string]any{"requirement_id": req.ID, "workflow_template_id": template.ID, "max_retries": 2, "coverage_threshold": 80}), map[string]string{"X-Workspace-ID": "w"})
	if pipelineResponse.Code != http.StatusCreated {
		t.Fatalf("pipeline=%d %s", pipelineResponse.Code, pipelineResponse.Body.String())
	}
	var run domain.PipelineRun
	if err := json.Unmarshal(pipelineResponse.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.PipelineStage != domain.PipelineDesign || run.Status != domain.PipelineWaiting {
		t.Fatalf("initial run=%+v", run)
	}
	result := mustJSONWorkflow(map[string]any{"stage": 1, "agent_id": "designer", "outcome": "pass", "design_doc": "approved design"})
	afterDesign := request(t, server.Routes(), http.MethodPost, "/api/v1/pipelines/"+run.ID+"/results", result, map[string]string{"X-Workspace-ID": "w"})
	if afterDesign.Code != http.StatusOK {
		t.Fatalf("design=%d %s", afterDesign.Code, afterDesign.Body.String())
	}
	if err := json.Unmarshal(afterDesign.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.PipelineWaitingApproval || run.DesignApprovalID == "" {
		t.Fatalf("approval gate missing=%+v", run)
	}
	decision := request(t, server.Routes(), http.MethodPost, "/api/v1/approvals/"+run.DesignApprovalID+"/decide", `{"decision":"approved","reason":"reviewed"}`, map[string]string{"X-Workspace-ID": "w", "X-Member-ID": "reviewer"})
	if decision.Code != http.StatusOK {
		t.Fatalf("decision=%d %s", decision.Code, decision.Body.String())
	}
	var resumed struct {
		Pipeline domain.PipelineRun `json:"pipeline"`
	}
	if err := json.Unmarshal(decision.Body.Bytes(), &resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.Pipeline.PipelineStage != domain.PipelineReport || resumed.Pipeline.Status != domain.PipelineWaiting {
		t.Fatalf("resume=%+v", resumed.Pipeline)
	}
}

func TestStandaloneChatUsesHarnessAndProjectIsolation(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "optional")
	bus := events.NewBus()
	fs, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := New(store.NewMemory(), provider.NewMockProvider(bus), fs, bus, slog.New(slog.NewTextHandler(io.Discard, nil)))
	created := request(t, server.Routes(), http.MethodPost, "/api/v1/chats", `{"workspace_id":"w","project_id":"project-a","title":"Architecture"}`, map[string]string{"X-Workspace-ID": "w"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", created.Code, created.Body.String())
	}
	var chat domain.ChatSession
	if err := json.Unmarshal(created.Body.Bytes(), &chat); err != nil {
		t.Fatal(err)
	}
	message := request(t, server.Routes(), http.MethodPost, "/api/v1/chats/"+chat.ID+"/messages", `{"content":"retain this context"}`, map[string]string{"X-Workspace-ID": "w", "Idempotency-Key": "m1"})
	if message.Code != http.StatusCreated {
		t.Fatalf("message=%d %s", message.Code, message.Body.String())
	}
	read := request(t, server.Routes(), http.MethodGet, "/api/v1/chats/"+chat.ID, "", map[string]string{"X-Workspace-ID": "w"})
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), "retain this context") || !strings.Contains(read.Body.String(), "transcript_durable") {
		t.Fatalf("read=%d %s", read.Code, read.Body.String())
	}
	foreign := request(t, server.Routes(), http.MethodGet, "/api/v1/chats/"+chat.ID, "", map[string]string{"X-Workspace-ID": "other"})
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign chat status=%d", foreign.Code)
	}
}

func mustJSONWorkflow(value any) string { data, _ := json.Marshal(value); return string(data) }
