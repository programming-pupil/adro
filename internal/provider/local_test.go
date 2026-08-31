package provider

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adro-project/adro/internal/events"
)

func TestLocalProviderRunsRealProcessAndCapturesSnapshot(t *testing.T) {
	bus := newTestBus()
	root := t.TempDir()
	p := NewLocalProvider("/usr/bin/printf", []string{"{input}"}, root, bus)
	item, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "work-1", Title: "local task"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, ProviderIssueID: item.ProviderIssueID, Input: "READY"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, p, binding.ID)
	if snapshot.Status != "completed" || snapshot.SessionID != binding.SessionID || snapshot.WorkDir != binding.WorkDir {
		t.Fatalf("snapshot=%+v binding=%+v", snapshot, binding)
	}
	if snapshot.Usage.DurationMS < 0 || snapshot.FinishedAt == nil {
		t.Fatalf("snapshot usage=%+v", snapshot.Usage)
	}
	if filepath.Clean(snapshot.WorkDir) != filepath.Clean(root+"/work-1/"+binding.SessionID) {
		t.Fatalf("unexpected workdir=%q", snapshot.WorkDir)
	}
}

func TestLocalProviderRepairReusesSessionAndWorkdir(t *testing.T) {
	p := NewLocalProvider("/usr/bin/printf", []string{"{input}"}, t.TempDir(), newTestBus())
	item, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "work-2", Title: "repairable task"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, ProviderIssueID: item.ProviderIssueID, Input: "first"})
	if err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, p, first.ID)
	second, err := p.ContinueWorkItem(context.Background(), ContinuationCommand{IssueID: item.ProviderIssueID, AgentID: "agent", Input: "repair", ExpectedSessionID: first.SessionID, ExpectedWorkDir: first.WorkDir})
	if err != nil {
		t.Fatal(err)
	}
	if !second.SessionReused || second.SessionID != first.SessionID || second.WorkDir != first.WorkDir {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestLocalProviderPersistsRunSnapshotAndMarksInterruptedRun(t *testing.T) {
	root, statePath := t.TempDir(), filepath.Join(t.TempDir(), "runs.json")
	p, err := NewPersistentLocalProvider("/usr/bin/printf", []string{"{input}"}, root, statePath, newTestBus())
	if err != nil {
		t.Fatal(err)
	}
	item, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "persisted-item", Title: "persisted"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, Input: "done"})
	if err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, p, binding.ID)
	restored, err := NewPersistentLocalProvider("/usr/bin/printf", []string{"{input}"}, root, statePath, newTestBus())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := restored.GetRun(context.Background(), binding.ID)
	if err != nil || snapshot.Status != "completed" || snapshot.Output != "done" {
		t.Fatalf("restored snapshot=%+v err=%v", snapshot, err)
	}

	// Simulate a process that was running when the API terminated.
	restored.Executable, restored.Args = "/bin/sleep", []string{"2"}
	second, err := restored.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, Input: "long-running"})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPersistentLocalProvider("/usr/bin/printf", []string{"{input}"}, root, statePath, newTestBus())
	if err != nil {
		t.Fatal(err)
	}
	interrupted, err := restarted.GetRun(context.Background(), second.ID)
	if err != nil || interrupted.Status != "failed" || !strings.Contains(interrupted.Error, "API restart") {
		t.Fatalf("interrupted snapshot=%+v err=%v", interrupted, err)
	}
}

func TestLocalProviderClonesConfiguredRepository(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(source, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", source}, {"-C", source, "config", "user.email", "adro@example.test"}, {"-C", source, "config", "user.name", "ADRO Test"}} {
		if err := exec.Command("git", args...).Run(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("source"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", source, "add", "README.md").Run(); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", source, "commit", "-qm", "initial").Run(); err != nil {
		t.Fatal(err)
	}
	p := NewLocalProvider("/usr/bin/printf", []string{"{input}"}, filepath.Join(t.TempDir(), "workspaces"), newTestBus())
	item, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "clone-item", Title: "clone", RepositoryPath: source})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, Input: "checked out"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, p, binding.ID)
	if snapshot.BaselineCommit == "" || snapshot.HeadCommit == "" {
		t.Fatalf("git provenance missing: %+v", snapshot)
	}
	if _, err := os.Stat(filepath.Join(binding.WorkDir, "README.md")); err != nil {
		t.Fatalf("repository was not materialized: %v", err)
	}
}

func TestLocalProviderRecordsUncommittedChanges(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(source, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", source}, {"-C", source, "config", "user.email", "adro@example.test"}, {"-C", source, "config", "user.name", "ADRO Test"}} {
		if err := exec.Command("git", args...).Run(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("source"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", source, "add", "README.md").Run(); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", source, "commit", "-qm", "initial").Run(); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "write-change.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf changed > changed.txt\n"), 0o750); err != nil {
		t.Fatal(err)
	}
	p := NewLocalProvider(script, nil, filepath.Join(t.TempDir(), "workspaces"), newTestBus())
	item, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "dirty-item", Title: "dirty", RepositoryPath: source})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, Input: "change"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, p, binding.ID)
	if !snapshot.WorkspaceDirty || len(snapshot.ChangedFiles) != 1 || snapshot.ChangedFiles[0] != "changed.txt" {
		t.Fatalf("uncommitted changes missing: %+v", snapshot)
	}
}

func TestLocalProviderStreamFiltersOtherRuns(t *testing.T) {
	p := NewLocalProvider("/usr/bin/printf", []string{"{input}"}, t.TempDir(), newTestBus())
	first, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "stream-one", Title: "one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "stream-two", Title: "two"})
	if err != nil {
		t.Fatal(err)
	}
	one, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: first.ID, Input: "one"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := p.StreamEvents(ctx, one.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: second.ID, Input: "two"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	waitSnapshot(t, p, one.ID)
	waitSnapshot(t, p, other.ID)
	for {
		select {
		case event := <-stream.Events:
			if event.AggregateID != one.ID && event.Payload["run_id"] != one.ID {
				t.Fatalf("stream leaked event=%+v", event)
			}
		case <-time.After(25 * time.Millisecond):
			return
		}
	}
}

func TestUsageFromOutputParsesClaudeResult(t *testing.T) {
	usage := usageFromOutput([]byte(`{"usage":{"input_tokens":12,"output_tokens":7,"cache_read_input_tokens":5,"cache_creation_input_tokens":3},"total_cost_usd":0.0042}`))
	if usage.InputTokens != 12 || usage.OutputTokens != 7 || usage.CacheReadTokens != 5 || usage.CacheWriteTokens != 3 || usage.EstimatedCost != 0.0042 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestLocalProviderClaudeSessionArguments(t *testing.T) {
	p := NewLocalProvider("claude", nil, t.TempDir(), newTestBus())
	sessionID := "11111111-1111-4111-8111-111111111111"
	initial := strings.Join(p.commandArgs("prompt", sessionID, false), " ")
	if !strings.Contains(initial, "--session-id "+sessionID) || strings.Contains(initial, "--resume") {
		t.Fatalf("initial args=%q", initial)
	}
	continued := strings.Join(p.commandArgs("repair", sessionID, true), " ")
	if !strings.Contains(continued, "--resume "+sessionID) || strings.Contains(continued, "--session-id") {
		t.Fatalf("continued args=%q", continued)
	}

	custom := NewLocalProvider("claude", []string{"--model", "sonnet", "-p", "{input}"}, t.TempDir(), newTestBus())
	customInitial := strings.Join(custom.commandArgs("prompt", sessionID, false), " ")
	if !strings.HasSuffix(customInitial, "--session-id "+sessionID) {
		t.Fatalf("custom initial args=%q", customInitial)
	}
	customContinued := strings.Join(custom.commandArgs("repair", sessionID, true), " ")
	if !strings.HasSuffix(customContinued, "--resume "+sessionID) {
		t.Fatalf("custom continued args=%q", customContinued)
	}

	withExplicit := NewLocalProvider("claude", []string{"-p", "{input}", "--resume", sessionID}, t.TempDir(), newTestBus())
	explicitArgs := strings.Join(withExplicit.commandArgs("repair", sessionID, true), " ")
	if strings.Count(explicitArgs, "--resume") != 1 {
		t.Fatalf("explicit session flag duplicated: %q", explicitArgs)
	}
}

func TestLocalProviderCodexSessionArguments(t *testing.T) {
	sessionID := "11111111-1111-4111-8111-111111111111"
	p := NewLocalProvider("codex", nil, t.TempDir(), newTestBus())
	initial := strings.Join(p.commandArgs("prompt", sessionID, false), " ")
	if initial != "exec --json prompt" {
		t.Fatalf("initial args=%q", initial)
	}
	continued := strings.Join(p.commandArgs("repair", sessionID, true), " ")
	if continued != "exec resume --json "+sessionID+" repair" {
		t.Fatalf("continued args=%q", continued)
	}

	custom := NewLocalProvider("npx", []string{"--yes", "@openai/codex@0.151.0", "exec", "--json", "{input}"}, t.TempDir(), newTestBus())
	customInitial := strings.Join(custom.commandArgs("prompt", sessionID, false), " ")
	if customInitial != "--yes @openai/codex@0.151.0 exec --json prompt" {
		t.Fatalf("custom initial args=%q", customInitial)
	}
	customContinued := strings.Join(custom.commandArgs("repair", sessionID, true), " ")
	want := "--yes @openai/codex@0.151.0 exec resume --json " + sessionID + " repair"
	if customContinued != want {
		t.Fatalf("custom continued args=%q want=%q", customContinued, want)
	}

	withoutJSON := NewLocalProvider("codex", []string{"exec", "{input}"}, t.TempDir(), newTestBus())
	if got := strings.Join(withoutJSON.commandArgs("repair", sessionID, true), " "); got != "exec resume --json "+sessionID+" repair" {
		t.Fatalf("custom command did not gain JSON/session args: %q", got)
	}

	ephemeral := NewLocalProvider("codex", []string{"exec", "--json", "--ephemeral", "{input}"}, t.TempDir(), newTestBus())
	if got := strings.Join(ephemeral.commandArgs("repair", sessionID, true), " "); strings.Contains(got, "--ephemeral") {
		t.Fatalf("resume retained ephemeral flag: %q", got)
	}
}

func TestProviderSessionIDExtractsCodexThread(t *testing.T) {
	want := "22222222-2222-4222-8222-222222222222"
	output := []byte("warning\n{\"type\":\"thread.started\",\"thread_id\":\"" + want + "\"}\n")
	if got := providerSessionID(output, "codex"); got != want {
		t.Fatalf("thread id=%q want=%q", got, want)
	}
	if got := providerSessionID([]byte(`{"type":"thread.started","thread_id":"`+want+`"}`), "claude"); got != "" {
		t.Fatalf("claude output unexpectedly changed session: %q", got)
	}
}

func TestLocalProviderStoresNativeCodexThreadID(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "codex")
	want := "33333333-3333-4333-8333-333333333333"
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\""+want+"\"}'\n"), 0o750); err != nil {
		t.Fatal(err)
	}
	p := NewLocalProvider(executable, nil, filepath.Join(root, "workspaces"), newTestBus())
	item, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "codex-thread", Title: "codex thread"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, Input: "prompt"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, p, binding.ID)
	if snapshot.SessionID != want {
		t.Fatalf("snapshot session=%q want=%q binding=%q", snapshot.SessionID, want, binding.SessionID)
	}
}

func waitSnapshot(t *testing.T, p *LocalProvider, id string) RunSnapshot {
	t.Helper()
	for i := 0; i < 1000; i++ {
		snapshot, err := p.GetRun(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Status != "running" {
			return snapshot
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("local process did not finish")
	return RunSnapshot{}
}

func newTestBus() *events.Bus {
	return events.NewBus()
}
