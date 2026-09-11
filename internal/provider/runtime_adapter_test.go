package provider

import (
	"strings"
	"testing"
)

func TestRuntimeProtocolErrorPromotesStructuredFailures(t *testing.T) {
	for _, test := range []struct {
		kind, output string
	}{
		{"cursor-agent", `{"type":"result","is_error":true,"error":"denied"}`},
		{"copilot", `{"type":"result","exitCode":2,"message":"failed"}`},
		{"opencode", `{"type":"error","error":{"message":"offline"}}`},
		{"openclaw", `{"type":"lifecycle","phase":"failed","detail":"provider unavailable"}`},
		{"qwen", `{"type":"error","message":"quota"}`},
	} {
		if err := runtimeProtocolError([]byte(test.output), test.kind); err == nil {
			t.Fatalf("%s structured failure was accepted", test.kind)
		}
	}
	if err := runtimeProtocolError([]byte(`{"type":"result","is_error":false}`), "qwen"); err != nil {
		t.Fatalf("successful result rejected: %v", err)
	}
	if err := runtimeTerminalOutputError([]byte(`{"type":"assistant"}`), "qwen"); err == nil {
		t.Fatal("unterminated stream was accepted")
	}
	if err := runtimeTerminalOutputError([]byte(`{"type":"result"}`), "qwen"); err != nil {
		t.Fatalf("terminal stream rejected: %v", err)
	}
}

func TestReplaceEnvironmentValueRemovesDuplicates(t *testing.T) {
	result := replaceEnvironmentValue([]string{"PATH=/bin", "PWD=/old", "pwd=/older"}, "PWD", "/work")
	if strings.Join(result, "|") != "PATH=/bin|PWD=/work" {
		t.Fatalf("environment=%v", result)
	}
}

func TestExecutorKindNormalizesLauncherSuffix(t *testing.T) {
	for _, executable := range []string{"cursor-agent.exe", "qwen.cmd", "codebuddy.ps1"} {
		provider := NewLocalProvider(executable, nil, t.TempDir(), nil)
		if strings.Contains(provider.executorKind(), ".") {
			t.Fatalf("launcher suffix not removed: %q", provider.executorKind())
		}
	}
}
