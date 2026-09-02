package provider

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adro-project/adro/internal/durable"
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

func TestLocalProviderStartRunIsIdempotentByHarnessKey(t *testing.T) {
	p := NewLocalProvider("/usr/bin/printf", []string{"{input}"}, t.TempDir(), newTestBus())
	item, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "idempotent-item", Title: "idempotent"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, Input: "once", IdempotencyKey: "pipeline:1:stage:1"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, Input: "once", IdempotencyKey: "pipeline:1:stage:1"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.ProviderRunID != second.ProviderRunID {
		t.Fatalf("duplicate provider run first=%+v second=%+v", first, second)
	}
	if _, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, Input: "different", IdempotencyKey: "pipeline:1:stage:1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("different input should fail closed, got %v", err)
	}
}

func TestExtractToolEventsFromCodexJSONL(t *testing.T) {
	output := []byte("{\"type\":\"item.started\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call-1\",\"name\":\"shell\",\"arguments\":\"go test ./...\"}}\n" +
		"{\"type\":\"item.completed\",\"item\":{\"type\":\"function_call_output\",\"call_id\":\"call-1\",\"output\":\"ok\"}}\n")
	events := extractToolEvents(output, "codex")
	if len(events) != 2 || events[0].CallID != "call-1" || events[0].Phase != "before" || events[1].Phase != "after" {
		t.Fatalf("tool events=%+v", events)
	}
	if got := extractToolEvents(output, "other"); len(got) != 0 {
		t.Fatalf("unknown provider emitted tool events=%+v", got)
	}
}

func TestLocalProviderConcurrentStartRunUsesOneIdempotentReservation(t *testing.T) {
	p := NewLocalProvider("/bin/sleep", []string{"0.05"}, t.TempDir(), newTestBus())
	item, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "concurrent-item", Title: "concurrent"})
	if err != nil {
		t.Fatal(err)
	}
	const callers = 16
	bindings := make([]RunBinding, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			bindings[index], errs[index] = p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, Input: "same", IdempotencyKey: "concurrent:key"})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
		if bindings[i].ID != bindings[0].ID {
			t.Fatalf("caller %d created duplicate binding %q vs %q", i, bindings[i].ID, bindings[0].ID)
		}
	}
}

func TestLocalProviderRecordsExecutorTimeout(t *testing.T) {
	t.Setenv("ADRO_EXECUTOR_TIMEOUT", "20ms")
	root := t.TempDir()
	p := NewLocalProvider("/bin/sleep", []string{"1"}, root, newTestBus())
	item, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "timeout-item", Title: "deadline"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, Input: "long-running"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, p, binding.ID)
	if snapshot.Status != "timed_out" {
		t.Fatalf("timeout status=%q snapshot=%+v", snapshot.Status, snapshot)
	}
	if snapshot.Error != "executor deadline exceeded" {
		t.Fatalf("timeout reason=%q snapshot=%+v", snapshot.Error, snapshot)
	}
	if snapshot.FinishedAt == nil || snapshot.SessionID != binding.SessionID || snapshot.WorkDir != binding.WorkDir {
		t.Fatalf("timeout evidence was not retained: %+v binding=%+v", snapshot, binding)
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

func TestLocalProviderAppendInputUsesInteractiveStdinAndPersistsLedger(t *testing.T) {
	root, statePath := t.TempDir(), filepath.Join(t.TempDir(), "runs.json")
	p, err := NewPersistentLocalProvider("/bin/sh", []string{"-c", "read first; read second; printf '%s:%s' \"$first\" \"$second\""}, root, statePath, newTestBus())
	if err != nil {
		t.Fatal(err)
	}
	item, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "interactive-item", Title: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.AppendInput(context.Background(), binding.ID, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := p.AppendInput(context.Background(), binding.ID, "beta"); err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, p, binding.ID)
	if snapshot.Status != "completed" || !strings.Contains(snapshot.Output, "alpha:beta") {
		t.Fatalf("interactive output=%q status=%q", snapshot.Output, snapshot.Status)
	}
	if len(snapshot.Interactions) != 2 || len(snapshot.Ledger) < 4 {
		t.Fatalf("missing interaction/runtime evidence: %+v", snapshot)
	}
	restarted, err := NewPersistentLocalProvider("/bin/sh", nil, root, statePath, newTestBus())
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restarted.GetRun(context.Background(), binding.ID)
	if err != nil || len(restored.Interactions) != 2 {
		t.Fatalf("restored interactions=%+v err=%v", restored.Interactions, err)
	}
}

func TestLocalProviderAppendInputWithKeyIsIdempotent(t *testing.T) {
	root, statePath := t.TempDir(), filepath.Join(t.TempDir(), "runs.json")
	p, err := NewPersistentLocalProvider("/bin/sh", []string{"-c", "read first; read second; printf '%s:%s' \"$first\" \"$second\""}, root, statePath, newTestBus())
	if err != nil {
		t.Fatal(err)
	}
	item, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "interactive-keyed-item", Title: "interactive keyed"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.AppendInputWithKey(context.Background(), binding.ID, "alpha", "turn:1"); err != nil {
		t.Fatal(err)
	}
	// Retrying the same request must reuse the accepted/sent interaction rather
	// than writing a second line to the provider stdin.
	if err := p.AppendInputWithKey(context.Background(), binding.ID, "alpha", "turn:1"); err != nil {
		t.Fatal(err)
	}
	if err := p.AppendInputWithKey(context.Background(), binding.ID, "beta", "turn:2"); err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, p, binding.ID)
	if snapshot.Status != "completed" || !strings.Contains(snapshot.Output, "alpha:beta") {
		t.Fatalf("keyed interactive output=%q status=%q", snapshot.Output, snapshot.Status)
	}
	if len(snapshot.Interactions) != 2 || snapshot.Interactions[0].Status != "sent" || snapshot.Interactions[1].Status != "sent" {
		t.Fatalf("keyed interaction ledger=%+v", snapshot.Interactions)
	}
}

func TestLocalProviderRejectsTamperedRuntimeLedger(t *testing.T) {
	root, statePath := t.TempDir(), filepath.Join(t.TempDir(), "runs.json")
	p, err := NewPersistentLocalProvider("/usr/bin/printf", []string{"{input}"}, root, statePath, newTestBus())
	if err != nil {
		t.Fatal(err)
	}
	item, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "ledger-item", Title: "ledger"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, Input: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, p, binding.ID)
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "run.started", "run.tampered", 1))
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPersistentLocalProvider("/usr/bin/printf", nil, root, statePath, newTestBus()); err == nil {
		t.Fatal("expected tampered ledger to fail closed")
	}
}

func TestLocalProviderFaultBeforeRenameLeavesPreviousSnapshot(t *testing.T) {
	root, statePath := t.TempDir(), filepath.Join(t.TempDir(), "runs.json")
	p, err := NewPersistentLocalProvider("/usr/bin/printf", []string{"{input}"}, root, statePath, newTestBus())
	if err != nil {
		t.Fatal(err)
	}
	item, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "fault-item", Title: "fault"})
	if err != nil {
		t.Fatal(err)
	}
	restore := durable.SetFaultInjector(func(point string) error {
		if point == "provider.snapshot.before_rename" {
			return errors.New("injected crash")
		}
		return nil
	})
	defer restore()
	if _, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, Input: "must-fail"}); err == nil {
		t.Fatal("expected provider persistence fault")
	}
}

func TestLocalProviderRejectsUnpersistedMutations(t *testing.T) {
	p := NewLocalProvider("/usr/bin/printf", []string{"{input}"}, t.TempDir(), newTestBus())
	p.StatePath = t.TempDir()
	if _, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "durability-item", Title: "must persist"}); err == nil {
		t.Fatal("expected work item persistence failure")
	}
	if len(p.items) != 0 || len(p.issues) != 0 {
		t.Fatalf("failed work item remained visible: items=%d issues=%d", len(p.items), len(p.issues))
	}

	p.StatePath = ""
	item, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "run-durability-item", Title: "run must persist"})
	if err != nil {
		t.Fatal(err)
	}
	p.StatePath = t.TempDir()
	if _, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, Input: "must fail closed"}); err == nil {
		t.Fatal("expected run persistence failure")
	}
	if len(p.runs) != 0 || len(p.workdirs) != 0 {
		t.Fatalf("failed run remained visible: runs=%d workdirs=%d", len(p.runs), len(p.workdirs))
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

func TestLocalProviderStreamReplaysAfterCursor(t *testing.T) {
	bus := newTestBus()
	p := NewLocalProvider("/usr/bin/printf", []string{"{input}"}, t.TempDir(), bus)
	item, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "stream-cursor", Title: "cursor"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, Input: "done"})
	if err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, p, binding.ID)
	all, _ := bus.List(binding.ID, "", 20)
	if len(all) < 2 {
		t.Fatalf("expected start and completion events, got %d", len(all))
	}
	stream, err := p.StreamEvents(context.Background(), binding.ID, all[0].EventID)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	select {
	case event := <-stream.Events:
		if event.EventID != all[1].EventID {
			t.Fatalf("cursor replay returned %q, want %q", event.EventID, all[1].EventID)
		}
	case <-time.After(time.Second):
		t.Fatal("cursor replay did not return the completion event")
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
	if snapshot.SessionContinuity != "proven" {
		t.Fatalf("snapshot continuity=%q want proven", snapshot.SessionContinuity)
	}
	continued, err := p.ContinueWorkItem(context.Background(), ContinuationCommand{IssueID: item.ProviderIssueID, AgentID: "agent", Input: "repair", ExpectedSessionID: want, ExpectedWorkDir: binding.WorkDir})
	if err != nil {
		t.Fatalf("proven codex continuation rejected: %v", err)
	}
	continuedSnapshot := waitSnapshot(t, p, continued.ID)
	if continuedSnapshot.Status != "completed" || continuedSnapshot.SessionID != want || continuedSnapshot.SessionContinuity != "proven" {
		t.Fatalf("continued snapshot=%+v", continuedSnapshot)
	}
}

func TestLocalProviderRejectsCodexContinuationWithoutThreadProof(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "codex")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf 'plain output\\n'\n"), 0o750); err != nil {
		t.Fatal(err)
	}
	p := NewLocalProvider(executable, nil, filepath.Join(root, "workspaces"), newTestBus())
	item, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "codex-no-proof", Title: "codex no proof"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, Input: "prompt"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, p, binding.ID)
	if snapshot.Status != "completed" || snapshot.SessionContinuity != "unproven" {
		t.Fatalf("initial snapshot=%+v", snapshot)
	}
	if _, err := p.ContinueWorkItem(context.Background(), ContinuationCommand{IssueID: item.ProviderIssueID, AgentID: "agent", Input: "repair", ExpectedSessionID: binding.SessionID, ExpectedWorkDir: binding.WorkDir}); err == nil || !strings.Contains(err.Error(), "proven thread.started") {
		t.Fatalf("expected fail-closed continuation, got %v", err)
	}
}

func waitSnapshot(t *testing.T, p *LocalProvider, id string) RunSnapshot {
	t.Helper()
	// Race/coverage builds can spend tens of seconds compiling and scheduling a
	// short-lived child process on a loaded CI worker. Keep the assertion bounded
	// while leaving enough room for the real-process acceptance path to finish.
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := p.GetRun(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Status != "running" {
			return snapshot
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("local process did not finish within 90s: %s", id)
	return RunSnapshot{}
}

func newTestBus() *events.Bus {
	return events.NewBus()
}
