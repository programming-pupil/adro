package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestACPRuntimeHelperProcess(t *testing.T) {
	if os.Getenv("ADRO_TEST_ACP_HELPER") != "1" {
		return
	}
	mode := os.Getenv("ADRO_TEST_ACP_MODE")
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	logPath := os.Getenv("ADRO_TEST_ACP_LOG")
	writeLog := func(line string) {
		if logPath == "" {
			return
		}
		file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintln(file, line)
			_ = file.Close()
		}
	}
	for scanner.Scan() {
		line := scanner.Text()
		writeLog(line)
		var request map[string]any
		if json.Unmarshal([]byte(line), &request) != nil {
			continue
		}
		method, _ := request["method"].(string)
		id := request["id"]
		switch method {
		case "initialize":
			if mode == "malformed" {
				fmt.Fprintln(os.Stdout, "not-json")
				continue
			}
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]any{
					"protocolVersion":   1,
					"agentCapabilities": map[string]any{"sessionCapabilities": map[string]any{"resume": true}},
					"authMethods":       []map[string]any{{"id": "cached_token"}},
				},
			})
		case "authenticate", "session/set_config_option":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		case "session/new":
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]any{
					"sessionId":     "native-session",
					"configOptions": []map[string]any{{"id": "reasoning_effort", "category": "thought"}},
				},
			})
		case "session/load", "session/resume":
			sessionID := "resume-session"
			if mode == "resume-mismatch" {
				sessionID = "different-session"
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"sessionId": sessionID}})
		case "session/set_model":
			if mode == "model-error" {
				_ = encoder.Encode(map[string]any{
					"jsonrpc": "2.0", "id": id,
					"error": map[string]any{"code": -32602, "message": "model unavailable"},
				})
				continue
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		case "session/prompt":
			if mode == "hang" {
				select {}
			}
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0", "method": "session/update",
				"params": map[string]any{
					"sessionId": "native-session",
					"update":    map[string]any{"sessionUpdate": "tool_call", "toolCallId": "tool-1", "title": "write_file", "rawInput": map[string]any{"path": "result.txt"}},
				},
			})
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0", "id": "permission-1", "method": "session/request_permission",
				"params": map[string]any{"options": []map[string]any{{"optionId": "permanent", "kind": "allow_always"}, {"optionId": "once", "kind": "allow_once"}}},
			})
			if !scanner.Scan() {
				os.Exit(3)
			}
			permission := scanner.Text()
			writeLog(permission)
			if !strings.Contains(permission, `"optionId":"once"`) {
				os.Exit(4)
			}
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0", "method": "session/update",
				"params": map[string]any{
					"sessionId": "native-session",
					"update":    map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "tool-1", "status": "completed", "content": []map[string]any{{"type": "content", "text": "done"}}},
				},
			})
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]any{
					"stopReason": "end_turn",
					"usage":      map[string]any{"inputTokens": 9, "outputTokens": 4, "cachedReadTokens": 2},
				},
			})
			if mode == "linger-after-result" {
				time.Sleep(time.Hour)
			}
		}
	}
	os.Exit(0)
}

func runACPHelper(t *testing.T, kind string, resumed bool, model, thinking, mode string) ([]byte, string, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := t.TempDir() + "/requests.jsonl"
	t.Setenv("ADRO_TEST_ACP_HELPER", "1")
	t.Setenv("ADRO_TEST_ACP_MODE", mode)
	t.Setenv("ADRO_TEST_ACP_LOG", logPath)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	args := []string{"-test.run=^TestACPRuntimeHelperProcess$"}
	_, output, runErr := executeACPRuntime(
		ctx, executable, args, "task", t.TempDir(), "resume-session", resumed,
		model, thinking, kind, nil, nil, nil,
	)
	requests, _ := os.ReadFile(logPath)
	return output, string(requests), runErr
}

func TestExecuteACPRuntimeFreshLifecycle(t *testing.T) {
	output, requests, err := runACPHelper(t, "hermes", false, "chosen-model", "high", "")
	if err != nil {
		t.Fatal(err)
	}
	methods := acpRequestMethods(t, requests)
	want := []string{"initialize", "session/new", "session/set_model", "session/set_config_option", "session/prompt"}
	if !reflect.DeepEqual(methods, want) {
		t.Fatalf("methods=%v want=%v", methods, want)
	}
	if got := providerSessionID(output, "hermes"); got != "native-session" {
		t.Fatalf("session=%q", got)
	}
	if err := runtimeTerminalOutputError(output, "hermes"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(requests, `"modelId":"chosen-model"`) || !strings.Contains(requests, `"configId":"reasoning_effort"`) {
		t.Fatalf("configuration requests missing:\n%s", requests)
	}
	events := extractToolEvents(output, "hermes")
	if len(events) != 2 || events[0].CallID != "tool-1" || events[0].Phase != "before" || events[1].Phase != "after" {
		t.Fatalf("tool events=%+v", events)
	}
}

func TestExecuteACPRuntimeUsesProviderResumeMethod(t *testing.T) {
	for _, test := range []struct {
		kind   string
		method string
	}{
		{kind: "kimi", method: "session/resume"},
		{kind: "kiro-cli", method: "session/load"},
		{kind: "qodercli", method: "session/resume"},
		{kind: "traecli", method: "session/load"},
		{kind: "qwenpaw", method: "session/load"},
		{kind: "reasonix", method: "session/resume"},
		{kind: "mcode", method: "session/load"},
		{kind: "dim", method: "session/load"},
		{kind: "zeroclaw", method: "session/resume"},
	} {
		t.Run(test.kind, func(t *testing.T) {
			_, requests, err := runACPHelper(t, test.kind, true, "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			methods := acpRequestMethods(t, requests)
			if !slices.Contains(methods, test.method) || slices.Contains(methods, "session/new") {
				t.Fatalf("methods=%v", methods)
			}
		})
	}
}

func TestExecuteACPRuntimeProviderSpecificConfiguration(t *testing.T) {
	_, kiroRequests, err := runACPHelper(t, "kiro-cli", false, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kiroRequests, `"content":[`) || !strings.Contains(kiroRequests, `"prompt":[`) {
		t.Fatalf("kiro prompt aliases missing:\n%s", kiroRequests)
	}
	_, dimRequests, err := runACPHelper(t, "dim", false, "", "max", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"configId":"mode"`, `"value":"agent"`, `"configId":"permission"`, `"value":"full-access"`, `"configId":"thought_level"`} {
		if !strings.Contains(dimRequests, value) {
			t.Fatalf("dim request missing %s:\n%s", value, dimRequests)
		}
	}
}

func TestExecuteACPRuntimeFailsClosed(t *testing.T) {
	if _, _, err := runACPHelper(t, "hermes", false, "bad-model", "", "model-error"); err == nil || !strings.Contains(err.Error(), "model unavailable") {
		t.Fatalf("model error=%v", err)
	}
	if _, _, err := runACPHelper(t, "hermes", false, "", "", "malformed"); err == nil || !strings.Contains(err.Error(), "malformed JSON") {
		t.Fatalf("malformed error=%v", err)
	}
	if _, _, err := runACPHelper(t, "mcode", false, "unsupported", "", ""); err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("unsupported model error=%v", err)
	}
	if _, _, err := runACPHelper(t, "hermes", true, "", "", "resume-mismatch"); err == nil || !strings.Contains(err.Error(), "different-session") {
		t.Fatalf("resume mismatch error=%v", err)
	}
}

func TestExecuteACPRuntimeBoundsExitAfterTerminalResponse(t *testing.T) {
	started := time.Now()
	if _, _, err := runACPHelper(t, "hermes", false, "", "", "linger-after-result"); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatalf("terminal response took %s to return", time.Since(started))
	}
}

func TestACPRuntimeLaunchContracts(t *testing.T) {
	tests := map[string][]string{
		"hermes":     {"acp"},
		"kimi":       {"acp"},
		"kiro-cli":   {"acp", "--trust-all-tools"},
		"qodercli":   {"--yolo", "--acp"},
		"qoderclicn": {"--yolo", "--acp"},
		"traecli":    {"acp", "serve", "--yolo"},
		"grok":       {"--no-auto-update", "agent", "--always-approve", "--effort", "high", "stdio"},
		"qwenpaw":    {"acp"},
		"reasonix":   {"acp", "--profile", "balanced", "--planner", "auto", "--sandbox-network", "auto", "--sandbox-bash", "auto", "--workspace-only"},
		"mcode":      {"acp"},
		"dim":        {"acp"},
		"zeroclaw":   {"acp"},
	}
	for kind, want := range tests {
		if got := acpRuntimeLaunchArgs(kind, "high", nil); !reflect.DeepEqual(got, want) {
			t.Errorf("%s args=%v want=%v", kind, got, want)
		}
	}
	if got := acpRuntimeLaunchArgs("zeroclaw", "", []string{"--agent", "primary", "--verbose"}); !reflect.DeepEqual(got, []string{"acp", "--verbose"}) {
		t.Fatalf("zeroclaw forwarded args=%v", got)
	}
	if alias := acpAgentAlias([]string{"--agent-alias=primary"}); alias != "primary" {
		t.Fatalf("agent alias=%q", alias)
	}
}

func TestSelectACPPermissionNeverPersistsGrant(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want string
		ok   bool
	}{
		{raw: `{"options":[{"optionId":"always","kind":"allow_always"},{"optionId":"once","kind":"allow_once"}]}`, want: "once", ok: true},
		{raw: `{"options":[{"optionId":"allow_session","kind":"allow_always"}]}`, want: "allow_session", ok: true},
		{raw: `{"options":[{"optionId":"deny","kind":"reject_once"}]}`, want: "deny", ok: true},
		{raw: `{"options":[{"optionId":"always","kind":"allow_always"}]}`, ok: false},
	} {
		got, ok := selectACPPermission(json.RawMessage(test.raw))
		if got != test.want || ok != test.ok {
			t.Errorf("select(%s)=(%q,%v) want=(%q,%v)", test.raw, got, ok, test.want, test.ok)
		}
	}
}

func acpRequestMethods(t *testing.T, raw string) []string {
	t.Helper()
	methods := []string{}
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		var frame map[string]any
		if json.Unmarshal(scanner.Bytes(), &frame) != nil {
			continue
		}
		if method, _ := frame["method"].(string); method != "" {
			methods = append(methods, method)
		}
	}
	return methods
}
