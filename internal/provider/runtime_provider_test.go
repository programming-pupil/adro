package provider

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/adro-project/adro/internal/events"
)

func TestLocalProviderAppliesAgentRuntimeOptions(t *testing.T) {
	claude := NewLocalProvider("claude", nil, t.TempDir(), events.NewBus()).
		WithExecutionConfig("claude-opus", "high", "", []string{"--verbose"})
	args := claude.commandArgs("ship it", "", false)
	for _, pair := range [][]string{{"--model", "claude-opus"}, {"--effort", "high"}} {
		found := false
		for i := 0; i+1 < len(args); i++ {
			if args[i] == pair[0] && args[i+1] == pair[1] {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %v in %v", pair, args)
		}
	}
	if !slices.Contains(args, "--verbose") {
		t.Fatalf("custom args missing from %v", args)
	}

	codex := NewLocalProvider("codex", []string{"exec", "{input}"}, t.TempDir(), events.NewBus()).
		WithExecutionConfig("gpt-5", "xhigh", "fast", []string{"--ephemeral"})
	args = codex.commandArgs("ship it", "", false)
	for _, value := range []string{"gpt-5", "model_reasoning_effort=xhigh", "service_tier=fast", "--ephemeral"} {
		if !slices.Contains(args, value) {
			t.Fatalf("missing %q in %v", value, args)
		}
	}
}

func TestRuntimeProviderPoolRoutesAndFailsClosed(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	fallback := NewMockProvider(events.NewBus())
	pool := NewRuntimeProviderPool(fallback, t.TempDir(), events.NewBus())
	legacy, err := pool.Resolve(RuntimeSelection{})
	if err != nil || legacy != fallback {
		t.Fatalf("legacy resolution = %T, %v", legacy, err)
	}
	claude, err := pool.Resolve(RuntimeSelection{RuntimeID: "claude", Model: "claude-opus-4-8"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := claude.(selectedRuntimeProvider); !ok {
		t.Fatalf("resolved provider = %T", claude)
	}
	if _, err := pool.Resolve(RuntimeSelection{RuntimeID: "cursor"}); err == nil {
		t.Fatal("uninstalled runtime must fail")
	}
	if _, err := pool.Resolve(RuntimeSelection{RuntimeID: "missing"}); err == nil {
		t.Fatal("unknown runtime must fail")
	}
	if _, err := pool.Resolve(RuntimeSelection{RuntimeID: "codex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Resolve(RuntimeSelection{RuntimeID: "claude", Model: "future-model"}); err != nil {
		t.Fatalf("non-authoritative catalog rejected a future model: %v", err)
	}
	if _, err := pool.Resolve(RuntimeSelection{RuntimeID: "claude", Model: "claude-sonnet-4-6", ThinkingLevel: "xhigh"}); err == nil {
		t.Fatal("unsupported per-model thinking level must fail")
	}
	if _, err := claude.Capabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeAdapterCommandContracts(t *testing.T) {
	const session = "11111111-1111-4111-8111-111111111111"
	tests := []struct {
		name       string
		want       []string
		promptArg  bool
		stdinValue string
	}{
		{name: "cursor-agent", want: []string{"-p", "--output-format", "stream-json", "--workspace", "/work", "--model", "chosen", "--resume", session}, stdinValue: "task"},
		{name: "copilot", want: []string{"-p", "task", "--output-format", "json", "--allow-all", "--no-ask-user", "--model", "chosen", "--resume", session}, promptArg: true},
		{name: "opencode", want: []string{"run", "--format", "json", "--dangerously-skip-permissions", "--dir", "/work", "--model", "chosen", "--variant", "high", "--session", session}, stdinValue: "task"},
		{name: "deveco", want: []string{"run", "--format", "json", "--dangerously-skip-permissions", "--dir", "/work", "--model", "chosen", "--variant", "high", "--session", session, "task"}, promptArg: true},
		{name: "openclaw", want: []string{"agent", "--local", "--json", "--session-id", session, "--agent", "chosen", "--message", "task"}, promptArg: true},
		{name: "agy", want: []string{"-p", "task", "--dangerously-skip-permissions", "--print-timeout", "30m", "--model", "chosen", "--conversation", session, "--add-dir", "/work"}, promptArg: true},
		{name: "codebuddy", want: []string{"-p", "--output-format", "stream-json", "--input-format", "stream-json", "--permission-mode", "bypassPermissions", "--model", "chosen", "--effort", "high", "--resume", session}},
		{name: "qwen", want: []string{"--output-format", "stream-json", "--model", "chosen", "--resume", session, "--yolo"}, stdinValue: "task"},
		{name: "pi", want: []string{"-p", "--mode", "json", "--session", session, "--model", "chosen", "--thinking", "high"}, stdinValue: "task"},
		{name: "omp", want: []string{"-p", "--mode", "json", "--session", session, "--model", "chosen", "--thinking", "high"}, stdinValue: "task"},
		{name: "dsh", want: []string{"--profile", "adro", "--stdio"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := NewLocalProvider(test.name, nil, t.TempDir(), events.NewBus()).
				WithExecutionConfig("chosen", "high", "", nil)
			args := provider.commandArgsAt("task", session, true, "/work")
			for _, want := range test.want {
				if !slices.Contains(args, want) {
					t.Fatalf("missing %q in %v", want, args)
				}
			}
			if !test.promptArg && slices.Contains(args, "task") {
				t.Fatalf("prompt leaked into argv: %v", args)
			}
			payload := provider.initialInputPayload("task", args)
			if test.stdinValue != "" && string(payload) != test.stdinValue {
				t.Fatalf("stdin payload=%q want=%q", payload, test.stdinValue)
			}
			if test.name == "codebuddy" && !strings.Contains(string(payload), `"text":"task"`) {
				t.Fatalf("structured stdin payload=%q", payload)
			}
			if !provider.oneShotExecution(args) {
				t.Fatal("runtime was not marked one-shot")
			}
		})
	}
}

func TestRuntimeAdapterRejectsProtocolOverrides(t *testing.T) {
	for runtimeID, arg := range map[string]string{
		"cursor": "--output-format", "copilot": "--allow-all=false", "opencode": "--dir=/tmp",
		"deveco": "--variant", "openclaw": "--message=other", "antigravity": "--log-file",
		"codebuddy": "--input-format=json", "qwen": "--approval-mode=default",
		"pi": "--session=/tmp/other", "omp": "--thinking=off",
		"dsh": "--profile=other",
	} {
		if err := validateRuntimeCustomArgs(runtimeID, []string{arg}); err == nil {
			t.Fatalf("%s accepted managed argument %q", runtimeID, arg)
		}
	}
	if err := validateRuntimeCustomArgs("cursor", []string{"--sandbox", "workspace-write"}); err != nil {
		t.Fatalf("safe custom arguments rejected: %v", err)
	}
}

func TestValidateRuntimeSelectionDistinguishesAuthoritativeCatalogs(t *testing.T) {
	model := RuntimeModel{
		ID:           "known",
		Thinking:     &RuntimeThinking{SupportedLevels: []RuntimeLevel{{Value: "high"}}},
		ServiceTiers: []RuntimeServiceTier{{ID: "fast"}},
	}
	for _, catalog := range []RuntimeModelCatalog{
		{RuntimeID: "dynamic", Models: []RuntimeModel{model}},
		{RuntimeID: "fallback", Models: []RuntimeModel{model}, Fallback: true},
		{RuntimeID: "empty"},
	} {
		err := validateRuntimeSelection(catalog, RuntimeSelection{Model: "future"})
		if catalog.RuntimeID == "dynamic" && err == nil {
			t.Fatal("authoritative catalog accepted an unknown model")
		}
		if catalog.RuntimeID != "dynamic" && err != nil {
			t.Fatalf("%s catalog rejected a future model: %v", catalog.RuntimeID, err)
		}
		if err := validateRuntimeSelection(catalog, RuntimeSelection{Model: "future", ThinkingLevel: "high"}); err == nil {
			t.Fatalf("%s catalog accepted options for an unadvertised model", catalog.RuntimeID)
		}
	}
	if err := validateRuntimeSelection(RuntimeModelCatalog{Models: []RuntimeModel{model}}, RuntimeSelection{Model: "known", ThinkingLevel: "high", ServiceTier: "fast"}); err != nil {
		t.Fatalf("advertised options rejected: %v", err)
	}
}

func TestRuntimeDescriptorsExposeModelSelectionLimits(t *testing.T) {
	unsupported := map[string]bool{"qwenpaw": true, "mcode": true, "zeroclaw": true}
	for _, descriptor := range RuntimeRegistry {
		if descriptor.ModelSelectionUnsupported != unsupported[descriptor.ID] {
			t.Errorf("runtime %s model_selection_unsupported=%v", descriptor.ID, descriptor.ModelSelectionUnsupported)
		}
	}
}
