package provider

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
		WithExecutionConfig("gpt-5", "xhigh", "fast", []string{"--ephemeral"}).
		WithRuntimeConfig(map[string]string{"sandbox_mode": "workspace-write"}).
		WithMCPServers([]RuntimeMCPServer{{Name: "release tools", Endpoint: "https://mcp.example.test", Protocol: "http", BearerTokenEnvVar: "ADRO_MCP_TOKEN"}})
	args = codex.commandArgs("ship it", "", false)
	for _, value := range []string{"gpt-5", "model_reasoning_effort=xhigh", "service_tier=fast", "sandbox_mode=workspace-write", `mcp_servers."release tools".url="https://mcp.example.test"`, `mcp_servers."release tools".bearer_token_env_var="ADRO_MCP_TOKEN"`, "--ephemeral"} {
		if !slices.Contains(args, value) {
			t.Fatalf("missing %q in %v", value, args)
		}
	}
}

func TestLocalProviderAppliesRuntimeSkillAndGatewayPolicies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	codex := NewLocalProvider("codex", []string{"exec", "{input}"}, t.TempDir(), events.NewBus()).
		WithDisabledRuntimeSkills([]RuntimeSkillRef{{RuntimeID: "codex", Provider: "codex", Root: "provider", Key: "review"}})
	args := codex.commandArgs("ship it", "", false)
	want := `skills.config=[{path="` + filepath.ToSlash(filepath.Join(home, ".codex", "skills", "review", "SKILL.md")) + `",enabled=false}]`
	if !slices.Contains(args, want) {
		t.Fatalf("missing disabled Skill policy %q in %v", want, args)
	}

	claude := NewLocalProvider("claude", nil, t.TempDir(), events.NewBus()).
		WithDisabledRuntimeSkills([]RuntimeSkillRef{{RuntimeID: "claude", Provider: "claude", Root: "provider", Key: "review", Name: "Review"}})
	args = claude.commandArgs("ship it", "11111111-1111-4111-8111-111111111111", false)
	settingsIndex := slices.Index(args, "--settings")
	if settingsIndex < 0 || settingsIndex+1 >= len(args) || !strings.Contains(args[settingsIndex+1], `"Skill(Review)"`) {
		t.Fatalf("Claude Skill settings missing from %v", args)
	}

	openclaw := NewLocalProvider("openclaw", nil, t.TempDir(), events.NewBus()).
		WithRuntimeConfig(map[string]string{"mode": "gateway"})
	args = openclaw.commandArgs("ship it", "session", false)
	if slices.Contains(args, "--local") {
		t.Fatalf("gateway mode retained --local: %v", args)
	}
	if err := validateRuntimeConfig("openclaw", map[string]string{"mode": "gateway", "gateway.port": "70000"}); err == nil {
		t.Fatal("invalid gateway port accepted")
	}
	if err := validateRuntimeConfig("codex", map[string]string{"sandbox_mode": "host", "approval_policy": "always"}); err == nil {
		t.Fatal("invalid Codex execution policy accepted")
	}
}

func TestOpenClawGatewayRuntimeConfigIsEphemeralAndSecretBacked(t *testing.T) {
	root := t.TempDir()
	provider := NewLocalProvider("openclaw", nil, root, events.NewBus()).
		WithRuntimeConfig(map[string]string{"mode": "gateway", "gateway.host": "gateway.internal", "gateway.port": "18789", "gateway.tls": "true", "gateway.auth_env": "TOKEN"}).
		WithRuntimeEnvironment(map[string]string{"TOKEN": "do-not-persist"})
	overlay, cleanup, err := provider.openclawRuntimeEnvironment("run-1")
	if err != nil {
		t.Fatal(err)
	}
	path := overlay["OPENCLAW_CONFIG_PATH"]
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("gateway wrapper permissions=%o", info.Mode().Perm())
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"host":"gateway.internal"`, `"port":18789`, `"tls":true`, `"token":"do-not-persist"`} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("gateway wrapper missing %q: %s", want, payload)
		}
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("gateway wrapper survived cleanup: %v", err)
	}

	provider.WithRuntimeEnvironment(map[string]string{})
	_, _, err = provider.openclawRuntimeEnvironment("run-2")
	if err == nil || strings.Contains(err.Error(), "do-not-persist") {
		t.Fatalf("missing credential error=%v", err)
	}
}

func TestRuntimeProviderPoolRestoresSelectedRuntimeRuns(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "claude")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf 'completed'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	workRoot := filepath.Join(dir, "work")
	statePath := filepath.Join(dir, "runs.json")
	fallback, err := NewPersistentLocalProvider(executable, nil, workRoot, statePath, events.NewBus())
	if err != nil {
		t.Fatal(err)
	}
	pool := NewRuntimeProviderPool(fallback, workRoot, events.NewBus())
	selected, err := pool.Resolve(RuntimeSelection{RuntimeID: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := selected.StartRun(context.Background(), StartRunCommand{WorkItemID: "restore-selected", Input: "run", IdempotencyKey: "restore-selected"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot, snapshotErr := selected.GetRun(context.Background(), binding.ID)
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		if snapshot.Status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("selected runtime did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}

	restartedFallback, err := NewPersistentLocalProvider(executable, nil, workRoot, statePath, events.NewBus())
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewRuntimeProviderPool(restartedFallback, workRoot, events.NewBus())
	if _, err := restarted.Resolve(RuntimeSelection{RuntimeID: "claude"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := restarted.GetRun(context.Background(), binding.ID)
	if err != nil || snapshot.ID != binding.ID || snapshot.Status == "running" {
		t.Fatalf("restored snapshot=%+v err=%v", snapshot, err)
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
		"codex":  "--listen=tcp://127.0.0.1:9999",
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
	if err := validateRuntimeCustomArgs("codex", []string{"--ephemeral"}); err == nil {
		t.Fatal("Codex app-server accepted an exec-only argument")
	}
}

func TestCodexAppServerPlacesGlobalCustomArgsBeforeSubcommand(t *testing.T) {
	provider := NewLocalProvider("codex", nil, t.TempDir(), events.NewBus()).
		WithExecutionConfig("", "", "", []string{"--profile", "research", "--analytics-default-enabled"})
	args := provider.commandArgs("ship it", "", false)
	want := "--profile research app-server --listen stdio:// --analytics-default-enabled"
	if got := strings.Join(args, " "); got != want {
		t.Fatalf("Codex app-server args=%q want=%q", got, want)
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
