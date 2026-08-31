package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adro-project/adro/internal/artifact"
	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/provider"
	"github.com/adro-project/adro/internal/store"
)

func TestPipelineAPIUsesNativeLocalExecutor(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "optional")
	control := store.NewMemory()
	requirement, err := control.CreateRequirement(domain.Requirement{
		WorkspaceID: "workspace", Title: "ship", Description: "local pipeline",
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
	local := provider.NewLocalProvider("/usr/bin/true", nil, t.TempDir(), bus)
	server := New(control, local, fs, bus, slog.New(slog.NewTextHandler(io.Discard, nil)))

	create := pipelineRequest(t, server, http.MethodPost, "/api/v1/pipelines", map[string]any{
		"requirement_id": requirement.ID,
		"roles":          map[string]any{"designer_agent_id": "11111111-1111-1111-1111-111111111111", "developer_agent_id": "22222222-2222-2222-2222-222222222222", "tester_agent_id": "33333333-3333-3333-3333-333333333333", "arbitrator_agent_id": "44444444-4444-4444-4444-444444444444"},
		"max_retries":    3, "coverage_threshold": 80,
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", create.Code, create.Body.String())
	}
	var run domain.PipelineRun
	if err := json.Unmarshal(create.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.PipelineStage != domain.PipelineDesign || run.Status != domain.PipelineWaiting || run.ActiveProviderIssueID == "" {
		t.Fatalf("created=%+v", run)
	}
	initialSnapshot, err := local.GetRun(context.Background(), run.ActiveProviderTaskID)
	if err != nil || initialSnapshot.SessionID == "" {
		t.Fatalf("initial provider session=%+v err=%v pipeline=%+v", initialSnapshot, err, run)
	}
	run = pipelineResult(t, server, run.ID, map[string]any{"stage": 1, "agent_id": "11111111-1111-1111-1111-111111111111", "outcome": "pass", "design_doc": "design"}, http.StatusOK)
	run = pipelineResult(t, server, run.ID, map[string]any{"stage": 2, "agent_id": "22222222-2222-2222-2222-222222222222", "outcome": "pass", "code_version": "a1", "provider_session_id": "session-original", "provider_work_dir": t.TempDir()}, http.StatusOK)
	if run.PipelineStage != domain.PipelineUnitTest || run.ParentSessionID != "session-original" {
		t.Fatalf("development result=%+v", run)
	}

	workItem, err := control.CreateWorkItem(domain.WorkItem{ID: "work", RequirementID: requirement.ID, RepositoryID: "repo", MemberID: "member"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := local.StartRun(context.Background(), provider.StartRunCommand{WorkItemID: workItem.ID, Input: "real local command"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := local.GetRun(context.Background(), binding.ID)
	if err != nil || snapshot.SessionID == "" || snapshot.WorkDir == "" || !localCapabilities(t, local).Supports("run.snapshot.v1") {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
}

func TestPipelineResultRetryIsIdempotentAfterDurableAdvance(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "optional")
	control := store.NewMemory()
	requirement, err := control.CreateRequirement(domain.Requirement{
		WorkspaceID: "workspace", Title: "idempotent result", Description: "retry a lost callback",
		AcceptanceCriteria: []string{"duplicate result is harmless"}, AssigneeMemberIDs: []string{"member"},
	})
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
	create := pipelineRequest(t, server, http.MethodPost, "/api/v1/pipelines", map[string]any{
		"requirement_id": requirement.ID,
		"roles": map[string]any{
			"designer_agent_id":   "11111111-1111-1111-1111-111111111111",
			"developer_agent_id":  "22222222-2222-2222-2222-222222222222",
			"tester_agent_id":     "33333333-3333-3333-3333-333333333333",
			"arbitrator_agent_id": "44444444-4444-4444-4444-444444444444",
		},
		"max_retries": 2, "coverage_threshold": 80,
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", create.Code, create.Body.String())
	}
	var initial domain.PipelineRun
	if err := json.Unmarshal(create.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	result := map[string]any{
		"stage": 1, "agent_id": initial.Roles.Designer, "outcome": "pass",
		"design_doc": "design", "provider_issue_id": initial.ActiveProviderIssueID,
		"provider_task_id": initial.ActiveProviderTaskID,
	}
	advanced := pipelineResult(t, server, initial.ID, result, http.StatusOK)
	if advanced.PipelineStage != domain.PipelineDevelopment {
		t.Fatalf("first result did not advance: %+v", advanced)
	}
	retried := pipelineRequest(t, server, http.MethodPost, "/api/v1/pipelines/"+initial.ID+"/results", result)
	if retried.Code != http.StatusOK {
		t.Fatalf("duplicate result was rejected: status=%d body=%s", retried.Code, retried.Body.String())
	}
	var replayed domain.PipelineRun
	if err := json.Unmarshal(retried.Body.Bytes(), &replayed); err != nil {
		t.Fatal(err)
	}
	if replayed.Version != advanced.Version || replayed.PipelineStage != advanced.PipelineStage {
		t.Fatalf("duplicate result changed pipeline: first=%+v replay=%+v", advanced, replayed)
	}
}

func TestPipelineLocalCollectorCompletesRealProcessRepairLoop(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "optional")
	root := t.TempDir()
	script := filepath.Join(root, "adro-stage-executor")
	counter := filepath.Join(root, "integration-attempt")
	program := strings.Join([]string{
		"#!/bin/sh",
		"set -eu",
		"input=\"$1\"",
		"stage=\"$(printf '%s' \"$input\" | sed -n 's/.*pipeline_stage: \\([0-9][0-9]*\\).*/\\1/p')\"",
		"case \"$stage\" in",
		"  1) printf '%s\\n' 'ADRO_RESULT_JSON={\"outcome\":\"pass\",\"design_doc\":\"design from the real process\"}' ;;",
		"  2) printf '%s\\n' 'ADRO_RESULT_JSON={\"outcome\":\"pass\",\"code_version\":\"working\"}' ;;",
		"  3) printf '%s\\n' 'ADRO_RESULT_JSON={\"outcome\":\"pass\",\"coverage\":100,\"passed_tests\":[\"unit\"]}' ;;",
		"  4)",
		"    if [ ! -f \"$ADRO_TEST_COUNTER\" ]; then",
		"      : > \"$ADRO_TEST_COUNTER\"",
		"      printf '%s\\n' 'ADRO_RESULT_JSON={\"outcome\":\"fail\",\"failed_tests\":[\"integration\"],\"error_log\":\"intentional integration failure\"}'",
		"    else",
		"      printf '%s\\n' 'ADRO_RESULT_JSON={\"outcome\":\"pass\",\"passed_tests\":[\"integration\"]}'",
		"    fi",
		"    ;;",
		"  5) printf '%s\\n' 'ADRO_RESULT_JSON={\"outcome\":\"pass\",\"repair_note\":\"repair approved\"}' ;;",
		"  6) printf '%s\\n' 'ADRO_RESULT_JSON={\"outcome\":\"pass\",\"passed_tests\":[\"revalidation\"]}' ;;",
		"  7) printf '%s\\n' 'ADRO_RESULT_JSON={\"outcome\":\"pass\",\"report\":\"final report\"}' ;;",
		"  *) printf '%s\\n' 'ADRO_RESULT_JSON={\"outcome\":\"fail\",\"error_log\":\"unknown stage\"}' ; exit 1 ;;",
		"esac",
		"",
	}, "\n")
	if err := os.WriteFile(script, []byte(program), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ADRO_TEST_COUNTER", counter)
	control := store.NewMemory()
	requirement, err := control.CreateRequirement(domain.Requirement{
		WorkspaceID: "workspace", Title: "real collector", Description: "run every stage",
		AcceptanceCriteria: []string{"the repair loop completes"}, AssigneeMemberIDs: []string{"member"},
	})
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	fs, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	local := provider.NewLocalProvider(script, []string{"{input}"}, filepath.Join(root, "work"), bus)
	server := New(control, local, fs, bus, slog.New(slog.NewTextHandler(io.Discard, nil)))
	create := pipelineRequest(t, server, http.MethodPost, "/api/v1/pipelines", map[string]any{
		"requirement_id": requirement.ID,
		"roles": map[string]any{
			"designer_agent_id":   "11111111-1111-1111-1111-111111111111",
			"developer_agent_id":  "22222222-2222-2222-2222-222222222222",
			"tester_agent_id":     "33333333-3333-3333-3333-333333333333",
			"arbitrator_agent_id": "44444444-4444-4444-4444-444444444444",
		},
		"max_retries": 2, "coverage_threshold": 80,
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", create.Code, create.Body.String())
	}
	var initial domain.PipelineRun
	if err := json.Unmarshal(create.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	var final domain.PipelineRun
	for time.Now().Before(deadline) {
		var getErr error
		final, getErr = control.GetPipeline(initial.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if final.Status == domain.PipelineCompleted || final.Status == domain.PipelineSuspended {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if final.Status != domain.PipelineCompleted {
		t.Fatalf("pipeline did not complete: status=%s stage=%d reason=%s history=%d output=%s", final.Status, final.PipelineStage, final.SuspendReason, len(final.History), strings.TrimSpace(final.FinalReport))
	}
	if final.RetryCount != 1 || final.ParentSessionID == "" || final.ProviderWorkDir == "" {
		t.Fatalf("repair continuity missing: retry=%d session=%q workdir=%q", final.RetryCount, final.ParentSessionID, final.ProviderWorkDir)
	}
	if final.PipelineWorkItemID == "" || final.BugID == "" {
		t.Fatalf("pipeline did not persist logical work item and generated bug: work_item=%q bug=%q", final.PipelineWorkItemID, final.BugID)
	}
	bug, bugErr := control.GetBug(final.BugID)
	if bugErr != nil || bug.WorkItemID != final.PipelineWorkItemID || bug.RequirementID != requirement.ID {
		t.Fatalf("pipeline bug relation=%+v err=%v", bug, bugErr)
	}
	if len(final.Context.ErrorLogs) != 1 || len(final.Context.RepairNotes) != 1 || final.FinalReport != "final report" {
		t.Fatalf("pipeline evidence incomplete: %+v", final.Context)
	}
	if len(final.History) != 10 {
		t.Fatalf("expected ten stage transitions including repair; got %d", len(final.History))
	}
	repairResponse := pipelineRequest(t, server, http.MethodPost, "/api/v1/bugs/"+final.BugID+"/repair", map[string]any{})
	if repairResponse.Code != http.StatusAccepted {
		t.Fatalf("repair=%d %s", repairResponse.Code, repairResponse.Body.String())
	}
	var repairPayload struct {
		Run provider.RunBinding `json:"run"`
	}
	if err := json.Unmarshal(repairResponse.Body.Bytes(), &repairPayload); err != nil {
		t.Fatal(err)
	}
	if !repairPayload.Run.SessionReused || repairPayload.Run.SessionID != final.ParentSessionID || repairPayload.Run.WorkDir != final.ProviderWorkDir {
		t.Fatalf("bug repair lost pipeline session: %+v final=%+v", repairPayload.Run, final)
	}
}

func TestPipelineResultFromSnapshotTreatsMissingMarkerAsFailure(t *testing.T) {
	run := domain.PipelineRun{
		PipelineStage: domain.PipelineUnitTest,
		Roles:         domain.PipelineAgentRoles{Tester: "tester"},
	}
	result, ok := pipelineResultFromSnapshot(run, provider.RunSnapshot{
		ID:     "provider-run",
		Status: "completed",
		Output: `{"type":"result","is_error":true,"result":"authentication error"}`,
	})
	if !ok || result.Outcome != "fail" || !strings.Contains(result.ErrorLog, "ADRO_RESULT_JSON") {
		t.Fatalf("missing marker was not converted to auditable failure: ok=%v result=%+v", ok, result)
	}
}

func TestPipelineResultFromSnapshotCannotOverrideProcessFailure(t *testing.T) {
	run := domain.PipelineRun{
		PipelineStage: domain.PipelineUnitTest,
		Roles:         domain.PipelineAgentRoles{Tester: "tester"},
	}
	result, ok := pipelineResultFromSnapshot(run, provider.RunSnapshot{
		ID:     "provider-run",
		Status: "failed",
		Error:  "exit status 1",
		Output: `{"result":"ADRO_RESULT_JSON={\"outcome\":\"pass\",\"coverage\":100}"}`,
	})
	if !ok || result.Outcome != "fail" || !strings.Contains(result.ErrorLog, "exit status 1") {
		t.Fatalf("process failure was overridden by success marker: ok=%v result=%+v", ok, result)
	}
}

func TestPipelineResultFromSnapshotTreatsCancellationAsFailure(t *testing.T) {
	run := domain.PipelineRun{
		PipelineStage: domain.PipelineUnitTest,
		Roles:         domain.PipelineAgentRoles{Tester: "tester"},
	}
	result, ok := pipelineResultFromSnapshot(run, provider.RunSnapshot{
		ID:     "provider-run",
		Status: "cancelled",
	})
	if !ok || result.Outcome != "fail" || !strings.Contains(result.ErrorLog, "cancelled") {
		t.Fatalf("cancelled process was left unobserved: ok=%v result=%+v", ok, result)
	}
}

func TestPipelineResultFromSnapshotTreatsTimeoutAsFailure(t *testing.T) {
	run := domain.PipelineRun{
		PipelineStage: domain.PipelineUnitTest,
		Roles:         domain.PipelineAgentRoles{Tester: "tester"},
	}
	result, ok := pipelineResultFromSnapshot(run, provider.RunSnapshot{
		ID: "provider-run", Status: "timed_out", Error: "executor deadline exceeded",
	})
	if !ok || result.Outcome != "fail" || !strings.Contains(result.ErrorLog, "executor deadline exceeded") {
		t.Fatalf("timed out process was not converted to auditable failure: ok=%v result=%+v", ok, result)
	}
}

func TestPipelineWatchDeadlineCancelsProviderAndSuspends(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "optional")
	t.Setenv("ADRO_PIPELINE_WATCH_TIMEOUT", "20ms")
	control := store.NewMemory()
	requirement, err := control.CreateRequirement(domain.Requirement{
		WorkspaceID: "workspace", Title: "watchdog", Description: "stop stale local execution",
		AcceptanceCriteria: []string{"stale waits are visible"}, AssigneeMemberIDs: []string{"member"},
	})
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	fs, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	local := provider.NewLocalProvider("/bin/sleep", []string{"1"}, t.TempDir(), bus)
	server := New(control, local, fs, bus, slog.New(slog.NewTextHandler(io.Discard, nil)))
	create := pipelineRequest(t, server, http.MethodPost, "/api/v1/pipelines", map[string]any{
		"requirement_id": requirement.ID,
		"roles": map[string]any{
			"designer_agent_id":   "11111111-1111-1111-1111-111111111111",
			"developer_agent_id":  "22222222-2222-2222-2222-222222222222",
			"tester_agent_id":     "33333333-3333-3333-3333-333333333333",
			"arbitrator_agent_id": "44444444-4444-4444-4444-444444444444",
		},
		"max_retries": 2, "coverage_threshold": 80,
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", create.Code, create.Body.String())
	}
	var initial domain.PipelineRun
	if err := json.Unmarshal(create.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var final domain.PipelineRun
	for time.Now().Before(deadline) {
		final, err = control.GetPipeline(initial.ID)
		if err != nil {
			t.Fatal(err)
		}
		if final.Status == domain.PipelineSuspended {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if final.Status != domain.PipelineSuspended || !strings.Contains(final.SuspendReason, "watcher deadline exceeded") {
		t.Fatalf("stale pipeline was not suspended: status=%s reason=%q", final.Status, final.SuspendReason)
	}
	snapshot, err := local.GetRun(context.Background(), initial.ActiveProviderTaskID)
	if err != nil || snapshot.Status != "cancelled" {
		t.Fatalf("provider was not cancelled: snapshot=%+v err=%v", snapshot, err)
	}
}

func TestPipelineResultFromSnapshotParsesCodexJSONLMarker(t *testing.T) {
	run := domain.PipelineRun{
		PipelineStage: domain.PipelineDesign,
		Roles:         domain.PipelineAgentRoles{Designer: "designer"},
	}
	output := `{"type":"item.completed","item":{"type":"agent_message","text":"ADRO_RESULT_JSON={\"stage\":1,\"outcome\":\"success\",\"design_doc\":\"codex design\"}"}}`
	result, ok := pipelineResultFromSnapshot(run, provider.RunSnapshot{ID: "provider-run", Status: "completed", Output: output})
	if !ok || result.Outcome != "pass" || result.DesignDoc != "codex design" {
		t.Fatalf("codex marker was not parsed: ok=%v result=%+v", ok, result)
	}
}

func TestPipelineResultFromSnapshotParsesCurrentCodexAgentMessageEnvelope(t *testing.T) {
	run := domain.PipelineRun{
		PipelineStage: domain.PipelineReport,
		Roles:         domain.PipelineAgentRoles{Tester: "tester"},
	}
	output := `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":[{"type":"Text","text":"ADRO_RESULT_JSON={\"stage\":7,\"outcome\":\"pass\",\"final_report\":\"current Codex report\"}"}]}}}`
	result, ok := pipelineResultFromSnapshot(run, provider.RunSnapshot{ID: "provider-run", Status: "completed", Output: output})
	if !ok || result.Outcome != "pass" || result.Report != "current Codex report" {
		t.Fatalf("current Codex envelope was not parsed: ok=%v result=%+v", ok, result)
	}
}

func TestPipelineResultFromSnapshotAcceptsStructuredCoverageAndErrorLog(t *testing.T) {
	run := domain.PipelineRun{
		PipelineStage: domain.PipelineReport,
		Roles:         domain.PipelineAgentRoles{Tester: "tester"},
	}
	output := `{"type":"item.completed","item":{"type":"agent_message","text":"ADRO_RESULT_JSON={\"stage\":7,\"outcome\":\"success\",\"coverage\":{\"percent\":100.0},\"error_log\":[\"first failure\",\"rerun passed\"],\"final_report\":\"structured report\"}"}}`
	result, ok := pipelineResultFromSnapshot(run, provider.RunSnapshot{ID: "provider-run", Status: "completed", Output: output})
	if !ok || result.Outcome != "pass" || result.Coverage != 100 || result.ErrorLog != "first failure; rerun passed" || result.Report != "structured report" {
		t.Fatalf("structured marker fields were not parsed: ok=%v result=%+v", ok, result)
	}
}

func TestPipelineResultFromSnapshotAcceptsHumanReadableCoverageAndTestsAlias(t *testing.T) {
	run := domain.PipelineRun{
		PipelineStage: domain.PipelineUnitTest,
		Roles:         domain.PipelineAgentRoles{Tester: "tester"},
	}
	output := `{"type":"item.completed","item":{"type":"agent_message","text":"ADRO_RESULT_JSON={\"stage\":3,\"outcome\":\"success\",\"coverage\":\"100.0% of statements\",\"tests\":[\"go test ./...\",\"go test -cover ./...\"]}"}}`
	result, ok := pipelineResultFromSnapshot(run, provider.RunSnapshot{ID: "provider-run", Status: "completed", Output: output})
	if !ok || result.Outcome != "pass" || result.Coverage != 100 || len(result.PassedTests) != 2 {
		t.Fatalf("human-readable coverage or tests alias was not parsed: ok=%v result=%+v", ok, result)
	}
}

func TestPipelineResultFromSnapshotAcceptsFinalReportAlias(t *testing.T) {
	run := domain.PipelineRun{
		PipelineStage: domain.PipelineReport,
		Roles:         domain.PipelineAgentRoles{Tester: "tester"},
	}
	output := `{"type":"item.completed","item":{"type":"agent_message","text":"ADRO_RESULT_JSON={\"stage\":7,\"agent_id\":\"tester\",\"outcome\":\"success\",\"final_report\":\"short report\"}"}}`
	result, ok := pipelineResultFromSnapshot(run, provider.RunSnapshot{ID: "provider-run", Status: "completed", Output: output})
	if !ok || result.Report != "short report" {
		t.Fatalf("final_report alias was not preserved: ok=%v result=%+v", ok, result)
	}
}

func TestPipelineResultFromSnapshotIgnoresMarkersInCodexCommandOutput(t *testing.T) {
	run := domain.PipelineRun{
		PipelineStage: domain.PipelineUnitTest,
		Roles:         domain.PipelineAgentRoles{Tester: "tester"},
	}
	output := strings.Join([]string{
		"Reading additional input from stdin...",
		`{"type":"item.completed","item":{"type":"command_execution","aggregated_output":"old ADRO_RESULT_JSON={\"outcome\":\"pass\",\"coverage\":100}"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"ADRO_RESULT_JSON={\"stage\":3,\"agent_id\":\"tester\",\"outcome\":\"success\",\"coverage\":85}"}}`,
	}, "\n")
	result, ok := pipelineResultFromSnapshot(run, provider.RunSnapshot{ID: "provider-run", Status: "completed", Output: output})
	if !ok || result.Outcome != "pass" || result.Coverage != 85 {
		t.Fatalf("command-output marker was trusted or agent marker was missed: ok=%v result=%+v", ok, result)
	}
}

func TestProviderNarrativeUsesCodexAgentMessages(t *testing.T) {
	output := strings.Join([]string{
		`{"type":"item.completed","item":{"type":"command_execution","aggregated_output":"state dump ADRO_RESULT_JSON={\"outcome\":\"pass\"}"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"Plan: implement the change."}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"ADRO_RESULT_JSON={\"stage\":1,\"outcome\":\"success\"}"}}`,
	}, "\n")
	narrative := providerNarrative(output)
	if narrative != "Plan: implement the change." {
		t.Fatalf("unexpected narrative=%q", narrative)
	}
}

func TestPipelinePromptMakesIntegrationCounterExplicit(t *testing.T) {
	run := domain.PipelineRun{
		ID: "pipeline", SessionID: "session", PipelineStage: domain.PipelineIntegration,
		Roles: domain.PipelineAgentRoles{Tester: "tester"}, MaxRetries: 2,
		Context: domain.PipelineContext{RequirementText: "run integration"},
	}
	prompt, err := pipelinePrompt(run)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "ADRO_E2E_INTEGRATION_COUNTER=.adro-e2e-integration-counter ./integration-check.sh exactly once") {
		t.Fatalf("integration counter contract missing from prompt: %s", prompt)
	}
}

func localCapabilities(t *testing.T, p provider.ExecutionProvider) provider.Capabilities {
	t.Helper()
	caps, err := p.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return caps
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
