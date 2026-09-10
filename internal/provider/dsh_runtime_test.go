package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
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
		"run-1", "task", t.TempDir(), "dsh-session", "provider/model", "high", resumed, nil,
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
