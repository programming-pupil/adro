package provider

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDSHRuntimeHelperProcess(t *testing.T) {
	if os.Getenv("ADRO_TEST_DSH_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		os.Exit(2)
	}
	requestLine := scanner.Text()
	if logPath := os.Getenv("ADRO_TEST_DSH_LOG"); logPath != "" {
		file, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if file != nil {
			_, _ = fmt.Fprintln(file, requestLine)
			_ = file.Close()
		}
	}
	var request map[string]any
	if json.Unmarshal([]byte(requestLine), &request) != nil {
		os.Exit(3)
	}
	requestID, _ := request["request_id"].(string)
	mode := os.Getenv("ADRO_TEST_DSH_MODE")
	if mode == "cancel" {
		if scanner.Scan() {
			if logPath := os.Getenv("ADRO_TEST_DSH_LOG"); logPath != "" {
				file, _ := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
				if file != nil {
					_, _ = fmt.Fprintln(file, scanner.Text())
					_ = file.Close()
				}
			}
		}
		time.Sleep(time.Hour)
	}
	version := 1
	if mode == "version" {
		version = 2
	}
	encoder := json.NewEncoder(os.Stdout)
	_ = encoder.Encode(map[string]any{"v": version, "type": "ready", "runtime": "dsh", "protocol_version": version})
	sessionID := "dsh-session"
	if mode == "session-mismatch" {
		sessionID = "different-session"
	}
	_ = encoder.Encode(map[string]any{"v": version, "type": "session", "request_id": requestID, "session_id": sessionID})
	_ = encoder.Encode(map[string]any{"v": version, "type": "tool_call", "request_id": requestID, "call_id": "tool-1", "name": "shell", "arguments": `{"command":"pwd"}`})
	_ = encoder.Encode(map[string]any{"v": version, "type": "tool_result", "request_id": requestID, "call_id": "tool-1", "name": "shell", "output": "ok"})
	_ = encoder.Encode(map[string]any{"v": version, "type": "usage", "request_id": requestID, "provider": "provider", "model": "model", "input_tokens": 13, "output_tokens": 6})
	_ = encoder.Encode(map[string]any{"v": version, "type": "result", "request_id": requestID, "session_id": sessionID, "status": "completed", "output": "done", "resume_rejected": mode == "resume-rejected"})
	if mode == "linger" {
		time.Sleep(time.Hour)
	}
	if mode == "exit-after-result" {
		os.Exit(1)
	}
	os.Exit(0)
}

func runDSHHelper(t *testing.T, mode string, timeout time.Duration) ([]byte, string, error) {
	return runDSHHelperRequest(t, mode, timeout, false)
}

func runDSHHelperRequest(t *testing.T, mode string, timeout time.Duration, resumed bool) ([]byte, string, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := t.TempDir() + "/requests.jsonl"
	t.Setenv("ADRO_TEST_DSH_HELPER", "1")
	t.Setenv("ADRO_TEST_DSH_MODE", mode)
	t.Setenv("ADRO_TEST_DSH_LOG", logPath)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, output, runErr := executeDSHRuntime(
		ctx, executable, []string{"-test.run=^TestDSHRuntimeHelperProcess$"},
		"run-1", "task", t.TempDir(), "dsh-session", "provider/model", "high", resumed, nil, nil,
	)
	requests, _ := os.ReadFile(logPath)
	return output, string(requests), runErr
}

func TestExecuteDSHRuntimeBoundsProcessExitAfterResult(t *testing.T) {
	started := time.Now()
	output, _, err := runDSHHelper(t, "linger", 4*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatalf("terminal result took %s to return", time.Since(started))
	}
	if got := providerSessionID(output, "dsh"); got != "dsh-session" {
		t.Fatalf("session=%q output=%s", got, output)
	}
}

func TestExecuteDSHRuntimeAcceptsValidatedResultBeforeNonzeroExit(t *testing.T) {
	output, _, err := runDSHHelper(t, "exit-after-result", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got := providerSessionID(output, "dsh"); got != "dsh-session" {
		t.Fatalf("session=%q output=%s", got, output)
	}
}

func TestExecuteDSHRuntimeLifecycle(t *testing.T) {
	output, requests, err := runDSHHelper(t, "", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"v":1`, `"type":"execute"`, `"request_id":"run-1"`, `"provider":"provider"`, `"id":"model"`, `"reasoning_effort":"high"`} {
		if !strings.Contains(requests, value) {
			t.Fatalf("request missing %s: %s", value, requests)
		}
	}
	if got := providerSessionID(output, "dsh"); got != "dsh-session" {
		t.Fatalf("session=%q output=%s", got, output)
	}
	if err := runtimeTerminalOutputError(output, "dsh"); err != nil {
		t.Fatal(err)
	}
	events := extractToolEvents(output, "dsh")
	if len(events) != 2 || events[0].Phase != "before" || events[1].Phase != "after" {
		t.Fatalf("tool events=%+v", events)
	}
	usage := usageFromOutput(output)
	if usage.InputTokens != 13 || usage.OutputTokens != 6 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestDSHRealRuntimeSmoke(t *testing.T) {
	if os.Getenv("ADRO_RUN_REAL_DSH") != "1" {
		t.Skip("set ADRO_RUN_REAL_DSH=1 with a configured DSH profile to run the real smoke test")
	}
	if strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")) == "" {
		t.Fatal("DEEPSEEK_API_KEY is required for the real DSH smoke test")
	}
	executable := strings.TrimSpace(os.Getenv("ADRO_REAL_DSH_EXECUTOR"))
	if executable == "" {
		var err error
		executable, err = exec.LookPath("dsh")
		if err != nil {
			t.Fatal(err)
		}
	}
	workDir := t.TempDir()
	provider := NewLocalProvider(executable, nil, workDir, nil).
		WithExecutionConfig("deepseek-official/deepseek-v4-flash", "high", "", nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = provider.Shutdown(ctx)
	})

	first, err := provider.StartRun(context.Background(), StartRunCommand{
		WorkItemID: "dsh-real-smoke", WorkDir: workDir, IdempotencyKey: "dsh-real-first",
		Input: "Use the shell tool to write exactly dsh-real-first followed by a newline to dsh-proof.txt in the current directory, then read the file. Finish with exactly ADRO_RESULT_JSON={\"outcome\":\"pass\",\"reason_code\":\"dsh_real_first\",\"summary\":\"DSH first turn passed\",\"evidence_ids\":[\"dsh-proof.txt\"]}.",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstSnapshot := waitForRealDSHRun(t, provider, first.ID)
	if firstSnapshot.Status != "completed" || firstSnapshot.SessionContinuity != "proven" || !hasMatchedToolPair(firstSnapshot.ToolEvents) {
		t.Fatalf("first DSH run did not prove execution and continuity: status=%s continuity=%s error=%s tools=%d output=%s", firstSnapshot.Status, firstSnapshot.SessionContinuity, firstSnapshot.Error, len(firstSnapshot.ToolEvents), firstSnapshot.Output)
	}
	content, err := os.ReadFile(filepath.Join(workDir, "dsh-proof.txt"))
	if err != nil || string(content) != "dsh-real-first\n" {
		t.Fatalf("first DSH file evidence=%q err=%v", content, err)
	}

	second, err := provider.ContinueWorkItem(context.Background(), ContinuationCommand{
		IssueID: "dsh-real-smoke", AgentID: "dsh-real-agent", ExpectedSessionID: firstSnapshot.SessionID,
		ExpectedWorkDir: workDir, IdempotencyKey: "dsh-real-second",
		Input: "Read dsh-proof.txt with the shell tool, append exactly dsh-real-second followed by a newline, then read it again. Finish with exactly ADRO_RESULT_JSON={\"outcome\":\"pass\",\"reason_code\":\"dsh_real_resume\",\"summary\":\"DSH resumed turn passed\",\"evidence_ids\":[\"dsh-proof.txt\"]}.",
	})
	if err != nil {
		t.Fatal(err)
	}
	secondSnapshot := waitForRealDSHRun(t, provider, second.ID)
	if secondSnapshot.Status != "completed" || !second.SessionReused || secondSnapshot.SessionID != firstSnapshot.SessionID || secondSnapshot.SessionContinuity != "proven" || !hasMatchedToolPair(secondSnapshot.ToolEvents) {
		t.Fatalf("resumed DSH run did not preserve execution continuity: status=%s reused=%v session_match=%v continuity=%s error=%s tools=%d output=%s", secondSnapshot.Status, second.SessionReused, secondSnapshot.SessionID == firstSnapshot.SessionID, secondSnapshot.SessionContinuity, secondSnapshot.Error, len(secondSnapshot.ToolEvents), secondSnapshot.Output)
	}
	content, err = os.ReadFile(filepath.Join(workDir, "dsh-proof.txt"))
	if err != nil || string(content) != "dsh-real-first\ndsh-real-second\n" {
		t.Fatalf("resumed DSH file evidence=%q err=%v", content, err)
	}
	if evidencePath := strings.TrimSpace(os.Getenv("ADRO_DSH_REAL_EVIDENCE_PATH")); evidencePath != "" {
		writeDSHRealEvidence(t, evidencePath, workDir, content, firstSnapshot, secondSnapshot)
	}
}

func writeDSHRealEvidence(t *testing.T, path, workDir string, artifact []byte, first, second RunSnapshot) {
	t.Helper()
	artifactDigest := sha256.Sum256(artifact)
	report := map[string]any{
		"runtime":        "dsh",
		"model":          "deepseek-official/deepseek-v4-flash",
		"work_dir":       workDir,
		"session_id":     first.SessionID,
		"session_reused": second.SessionID == first.SessionID,
		"first": map[string]any{
			"run_id": first.ID, "status": first.Status, "executor_pid": first.ExecutorPID,
			"work_dir": first.WorkDir, "session_id": first.SessionID, "session_continuity": first.SessionContinuity,
			"event_cursor": first.LastEventID, "stdout_sha256": first.OutputSHA256, "stderr_sha256": first.OutputSHA256,
			"tool_events_sha256": first.ToolEventsSHA256,
		},
		"second": map[string]any{
			"run_id": second.ID, "status": second.Status, "executor_pid": second.ExecutorPID,
			"work_dir": second.WorkDir, "session_id": second.SessionID, "session_continuity": second.SessionContinuity,
			"event_cursor": second.LastEventID, "stdout_sha256": second.OutputSHA256, "stderr_sha256": second.OutputSHA256,
			"tool_events_sha256": second.ToolEventsSHA256,
		},
		"artifact_sha256":  hex.EncodeToString(artifactDigest[:]),
		"stream_capture":   "provider transcript combines DSH stdout and stderr; process-level stdout/stderr are hashed by the wrapper script",
		"secret_redaction": map[string]any{"api_keys_written": false},
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitForRealDSHRun(t *testing.T, runtime *LocalProvider, runID string) RunSnapshot {
	t.Helper()
	deadline := time.Now().Add(15 * time.Minute)
	for {
		snapshot, err := runtime.GetRun(context.Background(), runID)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Status != "running" {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatal("real DSH run did not finish before the deadline")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func hasMatchedToolPair(events []ToolEvent) bool {
	pairs := map[string]map[string]bool{}
	for _, event := range events {
		if strings.TrimSpace(event.CallID) == "" {
			continue
		}
		if pairs[event.CallID] == nil {
			pairs[event.CallID] = map[string]bool{}
		}
		pairs[event.CallID][event.Phase] = true
	}
	for _, phases := range pairs {
		if phases["before"] && phases["after"] {
			return true
		}
	}
	return false
}

func TestExecuteDSHRuntimeFailsOnProtocolVersion(t *testing.T) {
	if _, _, err := runDSHHelper(t, "version", 3*time.Second); err == nil || !strings.Contains(err.Error(), "unsupported protocol version") {
		t.Fatalf("error=%v", err)
	}
}

func TestExecuteDSHRuntimeFailsClosedOnRejectedOrChangedContinuation(t *testing.T) {
	for _, test := range []struct {
		mode string
		want string
	}{{"resume-rejected", "rejected"}, {"session-mismatch", "different-session"}} {
		t.Run(test.mode, func(t *testing.T) {
			_, _, err := runDSHHelperRequest(t, test.mode, 3*time.Second, true)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestExecuteDSHRuntimeSendsCancelBeforeKill(t *testing.T) {
	started := time.Now()
	_, requests, err := runDSHHelper(t, "cancel", 150*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("error=%v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatalf("cancellation took %s", time.Since(started))
	}
	if !strings.Contains(requests, `"type":"cancel"`) || !strings.Contains(requests, `"request_id":"run-1"`) {
		t.Fatalf("cancel request missing: %s", requests)
	}
}

func TestParseDSHModel(t *testing.T) {
	model, err := parseDSHModel("gateway%2Fteam/model%2Fversion")
	if err != nil || model.Provider != "gateway/team" || model.ID != "model/version" {
		t.Fatalf("model=%+v err=%v", model, err)
	}
	if _, err := parseDSHModel("unqualified"); err == nil {
		t.Fatal("unqualified model was accepted")
	}
}

func TestParseDSHModels(t *testing.T) {
	models := parseDSHModels([]byte(`noise
{"v":1,"type":"models","models":[{"id":"model-a","provider":"provider","label":"Model A","default":true,"thinking":{"supported_levels":[{"value":"high","label":""},{"value":"high","label":"duplicate"},{"value":"bad\u0001"}],"default_level":"high"}},{"id":"provider/model-a","label":"duplicate"},{"id":"bad\u0001","label":"invalid"}]}
`))
	if len(models) != 1 || models[0].ID != "provider/model-a" || !models[0].Default {
		t.Fatalf("models=%+v", models)
	}
	if models[0].Thinking == nil || models[0].Thinking.DefaultLevel != "high" || len(models[0].Thinking.SupportedLevels) != 1 || models[0].Thinking.SupportedLevels[0].Label != "High" {
		t.Fatalf("thinking=%+v", models[0].Thinking)
	}
}
