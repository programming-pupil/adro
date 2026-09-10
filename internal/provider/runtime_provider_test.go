package provider

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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
		t.Fatal("runtime without adapter must fail")
	}
	if _, err := pool.Resolve(RuntimeSelection{RuntimeID: "missing"}); err == nil {
		t.Fatal("unknown runtime must fail")
	}
	if _, err := pool.Resolve(RuntimeSelection{RuntimeID: "codex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Resolve(RuntimeSelection{RuntimeID: "claude", Model: "missing-model"}); err == nil {
		t.Fatal("unadvertised model must fail")
	}
	if _, err := pool.Resolve(RuntimeSelection{RuntimeID: "claude", Model: "claude-sonnet-4-6", ThinkingLevel: "xhigh"}); err == nil {
		t.Fatal("unsupported per-model thinking level must fail")
	}
	if _, err := claude.Capabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
}
