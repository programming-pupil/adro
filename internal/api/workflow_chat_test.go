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
	"github.com/adro-project/adro/internal/orchestration"
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

func TestLegacyDesignApprovalRejectionIsTerminalAndReplayable(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "optional")
	control := store.NewMemory()
	requirement, err := control.CreateRequirement(domain.Requirement{WorkspaceID: "w", Title: "reject design", Description: "fail closed", AcceptanceCriteria: []string{"rejected"}, AssigneeMemberIDs: []string{"member"}})
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	fs, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	local := provider.NewLocalProvider("/usr/bin/true", nil, t.TempDir(), bus)
	server := New(control, local, fs, bus, slog.New(slog.NewTextHandler(io.Discard, nil)))
	templateResponse := request(t, server.Routes(), http.MethodPost, "/api/v1/workflow-templates", mustJSONWorkflow(map[string]any{"workspace_id": "w", "name": "reject-gate", "mode": "design_approval", "steps": []map[string]any{{"id": "design", "stage": 1, "agent_id": "designer", "required": true}, {"id": "report", "stage": 7, "agent_id": "reporter", "required": true}}}), map[string]string{"X-Workspace-ID": "w"})
	var template domain.WorkflowTemplate
	if templateResponse.Code != http.StatusCreated || json.Unmarshal(templateResponse.Body.Bytes(), &template) != nil {
		t.Fatalf("template status=%d body=%s", templateResponse.Code, templateResponse.Body.String())
	}
	created := request(t, server.Routes(), http.MethodPost, "/api/v1/pipelines", mustJSONWorkflow(map[string]any{"requirement_id": requirement.ID, "workflow_template_id": template.ID, "max_retries": 2, "coverage_threshold": 80}), map[string]string{"X-Workspace-ID": "w"})
	var run domain.PipelineRun
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &run) != nil {
		t.Fatalf("pipeline status=%d body=%s", created.Code, created.Body.String())
	}
	afterDesign := request(t, server.Routes(), http.MethodPost, "/api/v1/pipelines/"+run.ID+"/results", mustJSONWorkflow(map[string]any{"stage": 1, "agent_id": "designer", "outcome": "pass", "design_doc": "reject this"}), map[string]string{"X-Workspace-ID": "w"})
	if afterDesign.Code != http.StatusOK || json.Unmarshal(afterDesign.Body.Bytes(), &run) != nil || run.Status != domain.PipelineWaitingApproval {
		t.Fatalf("design status=%d body=%s", afterDesign.Code, afterDesign.Body.String())
	}
	rejected := request(t, server.Routes(), http.MethodPost, "/api/v1/approvals/"+run.DesignApprovalID+"/decide", `{"decision":"rejected","reason":"unsafe design"}`, map[string]string{"X-Workspace-ID": "w", "X-Member-ID": "reviewer"})
	if rejected.Code != http.StatusOK || !strings.Contains(rejected.Body.String(), "unsafe design") {
		t.Fatalf("reject status=%d body=%s", rejected.Code, rejected.Body.String())
	}
	projection, err := server.Orchestration.GetProjection(run.ExecutionPlanID)
	if err != nil {
		t.Fatal(err)
	}
	attempt := projection.Attempts[run.ActiveGraphAttemptID]
	if attempt.Status != orchestration.AttemptFailed || attempt.FailureReason == nil || attempt.FailureReason.Code != "legacy_pipeline_approval_denied" {
		t.Fatalf("rejected projection=%+v", projection)
	}
	plan, err := server.Orchestration.GetPlan(run.WorkspaceID, run.ExecutionPlanID)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := orchestration.ReplayProjection(plan, server.Orchestration.ListEvents(plan.ID, 0))
	if err != nil || replayed.Attempts[attempt.ID].Status != orchestration.AttemptFailed || replayed.Attempts[attempt.ID].FailureReason == nil {
		t.Fatalf("replayed rejection=%+v err=%v", replayed, err)
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
	if !strings.Contains(message.Body.String(), `"role":"assistant"`) || !strings.Contains(message.Body.String(), `"continuity":"compiled_context"`) {
		t.Fatalf("chat response did not include the durable assistant turn: %s", message.Body.String())
	}
	command, ok := server.Provider.(*provider.MockProvider).LastCommand()
	if !ok || command.ContextEnvelope.Manifest.SessionID != chat.HarnessSessionID || command.ContextEnvelope.ReplayKey == "" {
		t.Fatalf("chat provider command lost the compiled Harness envelope: %+v", command)
	}
	if !strings.Contains(command.Input, "retain this context") {
		t.Fatalf("fresh provider dispatch did not receive the rendered durable context: %q", command.Input)
	}
	replay := request(t, server.Routes(), http.MethodPost, "/api/v1/chats/"+chat.ID+"/messages", `{"content":"retain this context"}`, map[string]string{"X-Workspace-ID": "w", "Idempotency-Key": "m1"})
	if replay.Code != http.StatusCreated {
		t.Fatalf("chat replay=%d %s", replay.Code, replay.Body.String())
	}
	followUp := request(t, server.Routes(), http.MethodPost, "/api/v1/chats/"+chat.ID+"/messages", `{"content":"what did I ask before?"}`, map[string]string{"X-Workspace-ID": "w", "Idempotency-Key": "m2"})
	if followUp.Code != http.StatusCreated {
		t.Fatalf("chat follow-up=%d %s", followUp.Code, followUp.Body.String())
	}
	command, ok = server.Provider.(*provider.MockProvider).LastCommand()
	if !ok || !strings.Contains(command.Input, "retain this context") || !strings.Contains(command.Input, "what did I ask before?") {
		t.Fatalf("chat follow-up lost earlier durable context: %+v", command)
	}
	read := request(t, server.Routes(), http.MethodGet, "/api/v1/chats/"+chat.ID, "", map[string]string{"X-Workspace-ID": "w"})
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), "retain this context") || !strings.Contains(read.Body.String(), "what did I ask before?") || !strings.Contains(read.Body.String(), "transcript_durable") || !strings.Contains(read.Body.String(), "without returning assistant text") {
		t.Fatalf("read=%d %s", read.Code, read.Body.String())
	}
	var detail struct {
		Messages []domain.ChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &detail); err != nil || len(detail.Messages) != 4 {
		t.Fatalf("chat replay duplicated projections: messages=%d err=%v", len(detail.Messages), err)
	}
	foreign := request(t, server.Routes(), http.MethodGet, "/api/v1/chats/"+chat.ID, "", map[string]string{"X-Workspace-ID": "other"})
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign chat status=%d", foreign.Code)
	}
}

func TestChatAgentBindingPinsRuntimeConfiguration(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "optional")
	bus := events.NewBus()
	fs, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mock := provider.NewMockProvider(bus)
	server := New(store.NewMemory(), mock, fs, bus, slog.New(slog.NewTextHandler(io.Discard, nil)))
	agentID := orchestration.NewID()
	if err := server.Orchestration.SaveAgent(orchestration.AgentDefinition{
		ID: agentID, WorkspaceID: "w", Revision: 1, Name: "Chat reviewer", Status: orchestration.AgentActive,
		Instructions:    "Review the durable conversation.",
		ExecutorBinding: orchestration.ExecutorBinding{ProviderID: "mock", RuntimeID: "local", Model: "chat-model", RuntimeConfig: map[string]string{"mode": "local"}},
		InputSchema:     orchestration.SchemaRef{ID: "input"}, OutputSchema: orchestration.SchemaRef{ID: "output"},
		ConcurrencyBudget: orchestration.Budget{Tokens: 50000, ToolCalls: 25, Concurrent: 2},
	}, 0); err != nil {
		t.Fatal(err)
	}
	created := request(t, server.Routes(), http.MethodPost, "/api/v1/chats", `{"workspace_id":"w","agent_id":"`+agentID+`","title":"Agent chat"}`, map[string]string{"X-Workspace-ID": "w"})
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"agent_id":"`+agentID+`"`) || !strings.Contains(created.Body.String(), `"model":"chat-model"`) {
		t.Fatalf("created chat did not retain Agent binding: %d %s", created.Code, created.Body.String())
	}
	var chat domain.ChatSession
	if err := json.Unmarshal(created.Body.Bytes(), &chat); err != nil || chat.AgentRevision != 1 {
		t.Fatalf("created chat did not pin Agent revision: %+v err=%v", chat, err)
	}
	updatedAgent := orchestration.AgentDefinition{
		ID: agentID, WorkspaceID: "w", Revision: 2, Name: "Chat reviewer v2", Status: orchestration.AgentActive,
		Instructions:    "Use the newer configuration.",
		ExecutorBinding: orchestration.ExecutorBinding{ProviderID: "mock", RuntimeID: "local", Model: "new-chat-model", RuntimeConfig: map[string]string{"mode": "new"}},
		InputSchema:     orchestration.SchemaRef{ID: "input"}, OutputSchema: orchestration.SchemaRef{ID: "output"},
		ConcurrencyBudget: orchestration.Budget{Tokens: 50000, ToolCalls: 25, Concurrent: 2},
	}
	if err := server.Orchestration.SaveAgent(updatedAgent, 1); err != nil {
		t.Fatal(err)
	}
	_, selection, _, err := server.chatProviderSelection(chat)
	if err != nil || selection.Model != "chat-model" || selection.RuntimeConfig["mode"] != "local" {
		t.Fatalf("chat did not retain pinned Agent configuration: selection=%+v err=%v", selection, err)
	}
}

func TestChatAssistantTextExtractsDSHResultFrame(t *testing.T) {
	output := "{\"v\":1,\"type\":\"ready\"}\n" +
		"{\"v\":1,\"type\":\"result\",\"status\":\"completed\",\"output\":\"historical answer\"}\n"
	if got := chatAssistantText(output); got != "historical answer" {
		t.Fatalf("chatAssistantText=%q", got)
	}
}

func TestChatRunRoutesRespectChatWorkspaceOwnership(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "optional")
	bus := events.NewBus()
	fs, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := New(store.NewMemory(), provider.NewMockProvider(bus), fs, bus, slog.New(slog.NewTextHandler(io.Discard, nil)))
	created := request(t, server.Routes(), http.MethodPost, "/api/v1/chats", `{"workspace_id":"chat-workspace","title":"run ownership"}`, map[string]string{"X-Workspace-ID": "chat-workspace"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", created.Code, created.Body.String())
	}
	var chat domain.ChatSession
	if err := json.Unmarshal(created.Body.Bytes(), &chat); err != nil {
		t.Fatal(err)
	}
	message := request(t, server.Routes(), http.MethodPost, "/api/v1/chats/"+chat.ID+"/messages", `{"content":"expose this run"}`, map[string]string{"X-Workspace-ID": "chat-workspace", "Idempotency-Key": "chat-run-ownership"})
	if message.Code != http.StatusCreated {
		t.Fatalf("message=%d %s", message.Code, message.Body.String())
	}
	var response struct {
		Run provider.RunSnapshot `json:"run"`
	}
	if err := json.Unmarshal(message.Body.Bytes(), &response); err != nil || response.Run.ID == "" {
		t.Fatalf("run=%+v body=%s err=%v", response.Run, message.Body.String(), err)
	}
	owned := request(t, server.Routes(), http.MethodGet, "/api/v1/runs/"+response.Run.ID, "", map[string]string{"X-Workspace-ID": "chat-workspace"})
	if owned.Code != http.StatusOK {
		t.Fatalf("owned run=%d %s", owned.Code, owned.Body.String())
	}
	foreign := request(t, server.Routes(), http.MethodGet, "/api/v1/runs/"+response.Run.ID, "", map[string]string{"X-Workspace-ID": "other-workspace"})
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign run=%d %s", foreign.Code, foreign.Body.String())
	}
}

func TestChatAssistantTextExtractsCodexJSONRPCAgentMessage(t *testing.T) {
	output := `{"jsonrpc":"2.0","method":"item/completed","params":{"item":{"type":"agentMessage","text":"nested assistant answer"}}}` + "\n" +
		`{"jsonrpc":"2.0","method":"turn/completed","params":{"turn":{"status":"completed"}}}` + "\n"
	if got := chatAssistantText(output); got != "nested assistant answer" {
		t.Fatalf("chatAssistantText=%q", got)
	}
}

func TestChatAssistantTextExtractsCodexAgentMessageContentParts(t *testing.T) {
	output := `{"type":"item.completed","item":{"type":"agent_message","content":[{"type":"output_text","text":"part one"},{"type":"output_text","text":"part two"}]}}`
	if got := chatAssistantText(output); got != "part onepart two" {
		t.Fatalf("chatAssistantText=%q", got)
	}
}

func mustJSONWorkflow(value any) string { data, _ := json.Marshal(value); return string(data) }
