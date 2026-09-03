package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/harness"
	"github.com/adro-project/adro/internal/orchestration"
)

func TestExecutionPlanHumanApprovalTimelineReplayAndDiagnostics(t *testing.T) {
	s := testServer(t)
	requirement, err := s.Store.CreateRequirement(domain.Requirement{WorkspaceID: "w1", Title: "human gate", Description: "exercise plan controls", AcceptanceCriteria: []string{"approved"}, AssigneeMemberIDs: []string{"member"}, RepositoryIDs: []string{"repo"}})
	if err != nil {
		t.Fatal(err)
	}
	graph := orchestration.WorkflowGraph{ID: "human-graph", Version: 1, EntryNodeIDs: []string{"review"}, ExitNodeIDs: []string{"review"}, Nodes: []orchestration.WorkflowNode{{ID: "review", Kind: orchestration.NodeHuman}}}
	created := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+requirement.ID+"/execution-plan", mustJSON(map[string]any{"graph": graph, "idempotency_key": "human-plan"}), map[string]string{"X-Workspace-ID": "w1", "Idempotency-Key": "human-plan-http"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var plan orchestration.RequirementExecutionPlan
	if err := json.Unmarshal(created.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	tick := request(t, s.Routes(), http.MethodPost, "/api/v1/execution-plans/"+plan.ID+"/tick", mustJSON(map[string]any{"context_envelope": testContextEnvelope(t)}), map[string]string{"X-Workspace-ID": "w1", "Idempotency-Key": "human-tick"})
	if tick.Code != http.StatusOK || !strings.Contains(tick.Body.String(), "approval_pending") {
		t.Fatalf("tick status=%d body=%s", tick.Code, tick.Body.String())
	}
	projection, err := s.Orchestration.GetProjection(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	attemptID := projection.Nodes["review"].CurrentAttempt
	if attemptID == "" {
		t.Fatalf("human attempt was not created: %+v", projection)
	}
	approve := request(t, s.Routes(), http.MethodPost, "/api/v1/execution-plans/"+plan.ID+"/nodes/review/approve", "{}", map[string]string{"X-Workspace-ID": "w1", "Idempotency-Key": "human-approve"})
	if approve.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approve.Code, approve.Body.String())
	}
	approved, err := s.Orchestration.GetProjection(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != orchestration.PlanTerminal || approved.TerminalOutcome != "succeeded" {
		t.Fatalf("approval did not produce successful terminal projection: %+v", approved)
	}
	timeline := request(t, s.Routes(), http.MethodGet, "/api/v1/plans/"+plan.ID+"/timeline", "", map[string]string{"X-Workspace-ID": "w1"})
	if timeline.Code != http.StatusOK || !strings.Contains(timeline.Body.String(), "attempt.finished") {
		t.Fatalf("timeline status=%d body=%s", timeline.Code, timeline.Body.String())
	}
	planReplay := request(t, s.Routes(), http.MethodGet, "/api/v1/execution-plans/"+plan.ID+"/replay", "", map[string]string{"X-Workspace-ID": "w1"})
	if planReplay.Code != http.StatusOK || !strings.Contains(planReplay.Body.String(), "succeeded") {
		t.Fatalf("plan replay status=%d body=%s", planReplay.Code, planReplay.Body.String())
	}
	replay := request(t, s.Routes(), http.MethodGet, "/api/v1/runs/"+attemptID+"/replay", "", map[string]string{"X-Workspace-ID": "w1"})
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), "succeeded") {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	diagnostics := request(t, s.Routes(), http.MethodGet, "/api/v1/runs/"+attemptID+"/diagnostics", "", map[string]string{"X-Workspace-ID": "w1"})
	if diagnostics.Code != http.StatusOK || strings.Contains(diagnostics.Body.String(), "context_envelope") {
		t.Fatalf("diagnostics status=%d body=%s", diagnostics.Code, diagnostics.Body.String())
	}
	foreign := request(t, s.Routes(), http.MethodGet, "/api/v1/plans/"+plan.ID+"/timeline", "", map[string]string{"X-Workspace-ID": "w2"})
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign timeline status=%d body=%s", foreign.Code, foreign.Body.String())
	}
}

func TestExecutionPlanApprovalDenialIsFailClosed(t *testing.T) {
	s := testServer(t)
	requirement, err := s.Store.CreateRequirement(domain.Requirement{WorkspaceID: "w1", Title: "deny gate", Description: "exercise denial", AcceptanceCriteria: []string{"denied"}, AssigneeMemberIDs: []string{"member"}, RepositoryIDs: []string{"repo"}})
	if err != nil {
		t.Fatal(err)
	}
	graph := orchestration.WorkflowGraph{ID: "deny-graph", Version: 1, EntryNodeIDs: []string{"review"}, ExitNodeIDs: []string{"review"}, Nodes: []orchestration.WorkflowNode{{ID: "review", Kind: orchestration.NodeHuman}}}
	created := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+requirement.ID+"/execution-plan", mustJSON(map[string]any{"graph": graph}), map[string]string{"X-Workspace-ID": "w1"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var plan orchestration.RequirementExecutionPlan
	if err := json.Unmarshal(created.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	tick := request(t, s.Routes(), http.MethodPost, "/api/v1/execution-plans/"+plan.ID+"/tick", mustJSON(map[string]any{"context_envelope": testContextEnvelope(t)}), map[string]string{"X-Workspace-ID": "w1"})
	if tick.Code != http.StatusOK {
		t.Fatalf("tick status=%d body=%s", tick.Code, tick.Body.String())
	}
	deny := request(t, s.Routes(), http.MethodPost, "/api/v1/execution-plans/"+plan.ID+"/nodes/review/deny", "{}", map[string]string{"X-Workspace-ID": "w1", "Idempotency-Key": "human-deny"})
	if deny.Code != http.StatusOK {
		t.Fatalf("deny status=%d body=%s", deny.Code, deny.Body.String())
	}
	projection, err := s.Orchestration.GetProjection(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Status != orchestration.PlanTerminal || projection.TerminalOutcome != "failed" || projection.Attempts[projection.Nodes["review"].CurrentAttempt].Status != orchestration.AttemptFailed {
		t.Fatalf("denial was not fail-closed: %+v", projection)
	}
}

func TestOrchestrationDiagnosticsAndMetricsExposeBoundedSignals(t *testing.T) {
	s := testServer(t)
	traceParent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	requirement, err := s.Store.CreateRequirement(domain.Requirement{WorkspaceID: "w1", Title: "diagnostics", Description: "metrics", AcceptanceCriteria: []string{"works"}, AssigneeMemberIDs: []string{"member"}, RepositoryIDs: []string{"repo"}})
	if err != nil {
		t.Fatal(err)
	}
	graph := orchestration.WorkflowGraph{ID: "metrics-graph", Version: 1, EntryNodeIDs: []string{"review"}, ExitNodeIDs: []string{"review"}, Nodes: []orchestration.WorkflowNode{{ID: "review", Kind: orchestration.NodeHuman}}}
	created := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+requirement.ID+"/execution-plan", mustJSON(map[string]any{"graph": graph}), map[string]string{"X-Workspace-ID": "w1", "traceparent": traceParent, "tracestate": "vendor=value"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var plan orchestration.RequirementExecutionPlan
	if err := json.Unmarshal(created.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	tick := request(t, s.Routes(), http.MethodPost, "/api/v1/execution-plans/"+plan.ID+"/tick", mustJSON(map[string]any{"context_envelope": testContextEnvelope(t)}), map[string]string{"X-Workspace-ID": "w1"})
	if tick.Code != http.StatusOK {
		t.Fatalf("tick status=%d body=%s", tick.Code, tick.Body.String())
	}
	projection, err := s.Orchestration.GetProjection(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	attemptID := projection.Nodes["review"].CurrentAttempt
	diagnostics := request(t, s.Routes(), http.MethodGet, "/api/v1/runs/"+attemptID+"/diagnostics", "", map[string]string{"X-Workspace-ID": "w1"})
	if diagnostics.Code != http.StatusOK || !strings.Contains(diagnostics.Body.String(), "capabilities") || !strings.Contains(diagnostics.Body.String(), "budget") || !strings.Contains(diagnostics.Body.String(), "4bf92f3577b34da6a3ce929d0e0e4736") || !strings.Contains(diagnostics.Body.String(), `"tracestate":"vendor=value"`) || strings.Contains(diagnostics.Body.String(), "context_envelope") {
		t.Fatalf("diagnostics status=%d body=%s", diagnostics.Code, diagnostics.Body.String())
	}
	metrics := request(t, s.Routes(), http.MethodGet, "/metrics", "", nil)
	requiredMetrics := []string{
		"adro_orchestration_nodes_total",
		"adro_orchestration_feedback_total",
		"adro_orchestration_retry_total",
		"adro_orchestration_transition_latency_seconds_sum",
		"adro_orchestration_transition_latency_seconds_count",
		"adro_orchestration_failures_total{reason=\"loop_exhausted\"}",
		"adro_orchestration_failures_total{reason=\"context_overflow\"}",
		"adro_orchestration_failures_total{reason=\"tool_denial\"}",
		"adro_orchestration_failures_total{reason=\"lease_conflict\"}",
		"adro_comment_triggers_total{status=\"coalesced\"}",
		"adro_comment_triggers_total{status=\"blocked\"}",
		"adro_event_gaps_total",
		"adro_orchestration_usage_total{unit=\"tokens\"}",
		"adro_orchestration_usage_total{unit=\"tool_calls\"}",
		"adro_orchestration_usage_total{unit=\"cost_cents\"}",
	}
	if metrics.Code != http.StatusOK {
		t.Fatalf("metrics status=%d body=%s", metrics.Code, metrics.Body.String())
	}
	for _, metric := range requiredMetrics {
		if !strings.Contains(metrics.Body.String(), metric) {
			t.Fatalf("metrics missing %q body=%s", metric, metrics.Body.String())
		}
	}
}

func testContextEnvelope(t *testing.T) harness.ContextEnvelope {
	t.Helper()
	manifest := harness.ContextManifest{SessionID: "session-composition", Version: 1, TokenBudget: 100, TokenEstimate: 0, Digest: "legacy-manifest"}
	envelope, err := manifest.Envelope()
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}
