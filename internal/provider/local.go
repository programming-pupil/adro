package provider

// LocalProvider is ADRO's native execution boundary. It launches an installed
// coding client as a child process, records an auditable run snapshot, and
// reuses the same checkout/session for repair attempts. There is no remote
// control-plane protocol hidden behind this type: the only external contract
// is the executable selected by the operator.
import (
	"bufio"
	"bytes"
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/durable"
	"github.com/adro-project/adro/internal/events"
	runtimekernel "github.com/adro-project/adro/internal/runtime"
)

type localRun struct {
	snapshot     RunSnapshot
	cancel       context.CancelFunc
	input        string
	stdin        io.WriteCloser
	pending      []Interaction
	inputMu      sync.Mutex
	started      chan struct{}
	fencingToken int64
}

type localWorkItem struct {
	RepositoryPath string `json:"repository_path,omitempty"`
	CloneURL       string `json:"clone_url,omitempty"`
	DefaultBranch  string `json:"default_branch,omitempty"`
}

type localState struct {
	Revision int64                    `json:"revision"`
	Runs     map[string]RunSnapshot   `json:"runs"`
	RunKeys  map[string]string        `json:"run_keys,omitempty"`
	Workdirs map[string]string        `json:"workdirs"`
	Issues   map[string]string        `json:"issues"`
	Items    map[string]localWorkItem `json:"items"`
}

type LocalProvider struct {
	Executable string
	Args       []string
	WorkRoot   string
	Bus        *events.Bus
	StatePath  string

	mu       sync.RWMutex
	startMu  sync.Mutex
	runs     map[string]*localRun
	workdirs map[string]string
	issues   map[string]string
	items    map[string]localWorkItem
	runKeys  map[string]string
	revision int64
	runtime  *runtimekernel.Journal
}

func NewLocalProvider(executable string, args []string, workRoot string, bus *events.Bus) *LocalProvider {
	if bus == nil {
		bus = events.NewBus()
	}
	if strings.TrimSpace(workRoot) == "" {
		workRoot = filepath.Join("var", "workspaces")
	}
	return &LocalProvider{
		Executable: strings.TrimSpace(executable), Args: append([]string(nil), args...),
		WorkRoot: workRoot, Bus: bus, runs: map[string]*localRun{}, workdirs: map[string]string{}, issues: map[string]string{}, items: map[string]localWorkItem{}, runKeys: map[string]string{},
	}
}

// NewPersistentLocalProvider restores run/workspace provenance from an
// operator-owned file. A process cannot resume a child process after a crash,
// so runs that were active at restart are marked failed with a durable reason.
// Their session and workdir remain available for a deliberate repair attempt.
func NewPersistentLocalProvider(executable string, args []string, workRoot, statePath string, bus *events.Bus) (*LocalProvider, error) {
	p := NewLocalProvider(executable, args, workRoot, bus)
	p.StatePath = strings.TrimSpace(statePath)
	if p.StatePath != "" && strings.TrimSpace(os.Getenv("ADRO_RUNTIME_JOURNAL")) != "" {
		journalPath := strings.TrimSpace(os.Getenv("ADRO_RUNTIME_JOURNAL"))
		if journalPath == "true" {
			journalPath = p.StatePath + ".runtime.json"
		}
		journal, err := runtimekernel.NewJournal(journalPath)
		if err != nil {
			return nil, fmt.Errorf("load runtime journal: %w", err)
		}
		p.runtime = journal
	}
	if p.StatePath == "" {
		return p, nil
	}
	if err := p.loadState(); err != nil {
		return nil, err
	}
	return p, nil
}

// DiscoverLocalProvider scans the operator's PATH in a stable order. An
// explicit ADRO_EXECUTOR path always wins; ADRO_EXECUTOR_COMMAND may provide
// extra argv (the first token is the executable).
func DiscoverLocalProvider(workRoot string, bus *events.Bus) (*LocalProvider, error) {
	command := strings.TrimSpace(os.Getenv("ADRO_EXECUTOR_COMMAND"))
	var executable string
	var args []string
	if command != "" {
		parts := strings.Fields(command)
		if len(parts) > 0 {
			executable, args = parts[0], parts[1:]
		}
	}
	if explicit := strings.TrimSpace(os.Getenv("ADRO_EXECUTOR")); explicit != "" {
		executable = explicit
	}
	if executable == "" {
		for _, candidate := range []string{"claude", "codex", "claude-code"} {
			if path, err := exec.LookPath(candidate); err == nil {
				executable = path
				break
			}
		}
	}
	if executable == "" {
		return nil, errors.New("no supported coding client found; install claude, codex, or claude-code, or set ADRO_EXECUTOR")
	}
	if path, err := exec.LookPath(executable); err == nil {
		executable = path
	}
	return NewLocalProvider(executable, args, workRoot, bus), nil
}

func (p *LocalProvider) providerName() string { return "local" }

func (p *LocalProvider) Capabilities(context.Context) (Capabilities, error) {
	if _, err := p.executablePath(); err != nil {
		return Capabilities{}, err
	}
	return Capabilities{
		Provider: p.providerName(), AdapterVersion: "local-exec-v1", ServerVersion: "process",
		Features: []string{"agent.v1", "project.resources.v1", "issue.child.v1", "run.snapshot.v1", "run.messages.v1", "runtime.ledger.v1", "runtime.worktree.v1", "usage.tokens.v1", "attachment.v1", "run.repair.v1", "tool.checkpoint.v1", "context.manifest.v1"},
	}, nil
}

func (p *LocalProvider) EnsureAgent(_ context.Context, s AgentSpec) (AgentBinding, error) {
	if strings.TrimSpace(s.Name) == "" {
		return AgentBinding{}, errors.New("agent name is required")
	}
	if s.ID == "" {
		s.ID = domain.NewID()
	}
	return AgentBinding{ID: s.ID, Provider: p.providerName(), ProviderAgentID: "local-agent-" + s.ID, AgentSpec: s}, nil
}

func (p *LocalProvider) EnsureTeamWorkspace(_ context.Context, s WorkspaceSpec) (WorkspaceBinding, error) {
	if strings.TrimSpace(s.ID) == "" {
		return WorkspaceBinding{}, errors.New("workspace id is required")
	}
	return WorkspaceBinding{ID: s.ID, Provider: p.providerName(), ProviderWorkspaceID: s.ID}, nil
}

func (p *LocalProvider) CreateWorkItem(_ context.Context, s WorkItemSpec) (ProviderWorkItem, error) {
	if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.Title) == "" {
		return ProviderWorkItem{}, errors.New("work item id and title are required")
	}
	issueID := "local-item-" + s.ID
	p.mu.Lock()
	previousIssue, issueExisted := p.issues[issueID]
	previousItem, itemExisted := p.items[s.ID]
	p.issues[issueID] = s.ID
	p.items[s.ID] = localWorkItem{RepositoryPath: s.RepositoryPath, CloneURL: s.CloneURL, DefaultBranch: s.DefaultBranch}
	if err := p.persistLocked(); err != nil {
		if issueExisted {
			p.issues[issueID] = previousIssue
		} else {
			delete(p.issues, issueID)
		}
		if itemExisted {
			p.items[s.ID] = previousItem
		} else {
			delete(p.items, s.ID)
		}
		p.mu.Unlock()
		return ProviderWorkItem{}, fmt.Errorf("persist local work item: %w", err)
	}
	p.mu.Unlock()
	return ProviderWorkItem{ID: s.ID, ProviderIssueID: issueID}, nil
}

func (p *LocalProvider) StartRun(ctx context.Context, command StartRunCommand) (RunBinding, error) {
	// Serialize the idempotency lookup and durable run reservation. Without this
	// narrow gate two concurrent retries can both observe a missing key and
	// launch duplicate child processes before either one records the key.
	p.startMu.Lock()
	defer p.startMu.Unlock()
	if strings.TrimSpace(command.WorkItemID) == "" {
		return RunBinding{}, errors.New("work item id is required")
	}
	sessionID := strings.TrimSpace(command.SessionID)
	if sessionID == "" {
		sessionID = newLocalSessionID()
	}
	workDir, err := p.workDir(command.WorkItemID, sessionID)
	if err != nil {
		return RunBinding{}, err
	}
	p.mu.RLock()
	item := p.items[command.WorkItemID]
	p.mu.RUnlock()
	if err := p.prepareWorkDir(ctx, workDir, item); err != nil {
		return RunBinding{}, err
	}
	if key := strings.TrimSpace(command.IdempotencyKey); key != "" {
		p.mu.RLock()
		existingID := p.runKeys[command.WorkItemID+"\x00"+key]
		existing := p.runs[existingID]
		p.mu.RUnlock()
		if existing != nil {
			if existing.snapshot.InputHash != "" && existing.snapshot.InputHash != sha256Hex(command.Input) {
				return RunBinding{}, fmt.Errorf("%w: idempotency key maps to different input", ErrConflict)
			}
			return bindingFromSnapshot(existing.snapshot, command.ContextID, command.ContextVersion, command.SessionID != ""), nil
		}
	}
	return p.start(ctx, command.WorkItemID, command.ProviderIssueID, command.Input, sessionID, workDir, command.ContextID, command.ContextVersion, command.SessionID != "", command.IdempotencyKey)
}

func (p *LocalProvider) ContinueWorkItem(ctx context.Context, command ContinuationCommand) (RunBinding, error) {
	p.startMu.Lock()
	defer p.startMu.Unlock()
	if command.IssueID == "" || command.Input == "" || command.ExpectedSessionID == "" || command.ExpectedWorkDir == "" {
		return RunBinding{}, errors.New("continuation requires issue, input, session and workdir")
	}
	p.mu.RLock()
	workItemID := command.IssueID
	workDir := p.workdirs[workItemID]
	item := p.items[workItemID]
	if workDir == "" {
		if mapped := p.issues[command.IssueID]; mapped != "" {
			workItemID = mapped
			workDir = p.workdirs[workItemID]
			item = p.items[workItemID]
		}
	}
	p.mu.RUnlock()
	if workDir == "" || filepath.Clean(workDir) != filepath.Clean(command.ExpectedWorkDir) {
		return RunBinding{}, errors.New("continuation workdir does not match the original run")
	}
	if p.executorKind() == "codex" {
		p.mu.RLock()
		proven := p.hasProvenSessionLocked(workItemID, command.ExpectedSessionID)
		p.mu.RUnlock()
		if !proven {
			return RunBinding{}, errors.New("codex continuation requires a proven thread.started session")
		}
	}
	if err := p.prepareWorkDir(ctx, workDir, item); err != nil {
		return RunBinding{}, err
	}
	if key := strings.TrimSpace(command.IdempotencyKey); key != "" {
		p.mu.RLock()
		existingID := p.runKeys[workItemID+"\x00"+key]
		existing := p.runs[existingID]
		p.mu.RUnlock()
		if existing != nil {
			if existing.snapshot.SessionID != command.ExpectedSessionID || filepath.Clean(existing.snapshot.WorkDir) != filepath.Clean(command.ExpectedWorkDir) {
				return RunBinding{}, errors.New("continuation idempotency key maps to a different session or workdir")
			}
			return bindingFromSnapshot(existing.snapshot, "", 0, true), nil
		}
	}
	return p.start(ctx, workItemID, command.IssueID, command.Input, command.ExpectedSessionID, workDir, "", 0, true, command.IdempotencyKey)
}

func (p *LocalProvider) hasProvenSessionLocked(workItemID, sessionID string) bool {
	for _, run := range p.runs {
		if run == nil {
			continue
		}
		snapshot := run.snapshot
		if snapshot.WorkItemID == workItemID && snapshot.SessionID == sessionID && snapshot.SessionContinuity == "proven" {
			return true
		}
	}
	return false
}

func (p *LocalProvider) start(ctx context.Context, workItemID, issueID, input, sessionID, workDir, contextID string, contextVersion int64, reused bool, idempotencyKey string) (RunBinding, error) {
	if err := os.MkdirAll(workDir, 0o750); err != nil {
		return RunBinding{}, fmt.Errorf("create execution workdir: %w", err)
	}
	id := domain.NewID()
	now := time.Now().UTC()
	if err := ctx.Err(); err != nil {
		return RunBinding{}, err
	}
	// A run outlives the HTTP request that created it. The cancel handle is
	// retained in the run record and is exercised by CancelRun or the explicit
	// executor deadline, whichever comes first.
	runCtx, cancel := localExecutionContext()
	snapshot := RunSnapshot{ID: id, WorkItemID: workItemID, ProviderIssueID: issueID, InputHash: sha256Hex(input), Status: "running", SessionID: sessionID, SessionContinuity: "unproven", WorkDir: workDir, StartedAt: &now}
	fencingToken := int64(0)
	var runtimeScope runtimekernel.Scope
	if p.runtime != nil {
		runtimeScope = runtimekernel.Scope{TenantID: "local", WorkspaceID: "local", SessionID: sessionID, RunID: id}
		lease, leaseErr := p.runtime.AcquireLease(runtimeScope, "local", 24*time.Hour, now)
		if leaseErr != nil {
			cancel()
			return RunBinding{}, fmt.Errorf("acquire runtime lease: %w", leaseErr)
		}
		fencingToken = lease.FencingToken
	}
	p.mu.Lock()
	run := &localRun{snapshot: snapshot, cancel: cancel, input: input, started: make(chan struct{}), fencingToken: fencingToken}
	p.runs[id] = run
	appendRuntimeEventLocked(run, "run.started", map[string]any{"work_item_id": workItemID, "session_id": sessionID, "work_dir": workDir, "input_sha256": sha256Hex(input)})
	runKey := strings.TrimSpace(idempotencyKey)
	previousRunKey := ""
	runKeyExisted := false
	if runKey != "" {
		mapKey := workItemID + "\x00" + runKey
		previousRunKey, runKeyExisted = p.runKeys[mapKey]
		p.runKeys[mapKey] = id
	}
	previousWorkDir, workDirExisted := p.workdirs[workItemID]
	p.workdirs[workItemID] = workDir
	if err := p.persistLocked(); err != nil {
		delete(p.runs, id)
		if workDirExisted {
			p.workdirs[workItemID] = previousWorkDir
		} else {
			delete(p.workdirs, workItemID)
		}
		if runKey != "" {
			mapKey := workItemID + "\x00" + runKey
			if runKeyExisted {
				p.runKeys[mapKey] = previousRunKey
			} else {
				delete(p.runKeys, mapKey)
			}
		}
		p.mu.Unlock()
		cancel()
		return RunBinding{}, fmt.Errorf("persist local run: %w", err)
	}
	p.mu.Unlock()
	if p.runtime != nil {
		_, _ = p.runtime.Append(runtimekernel.Input{EventType: runtimekernel.EventTurnStarted, AggregateType: "run", AggregateID: id, Scope: runtimeScope, CorrelationID: id, IdempotencyKey: "run:" + id + ":start", WriterID: "local", FencingToken: fencingToken, Payload: map[string]any{"work_item_id": workItemID, "input_sha256": sha256Hex(input)}})
	}
	_ = p.Bus.Publish(runCtx, events.New("execution.started.v1", "execution_run", id, "", "", 1, map[string]any{"work_item_id": workItemID, "input_sha256": sha256Hex(input), "session_id": sessionID, "work_dir": workDir}))
	go p.execute(runCtx, id, input, workDir, sessionID, reused)
	return RunBinding{ID: id, ProviderRunID: id, SessionID: sessionID, WorkDir: workDir, ContextID: contextID, ContextVersion: contextVersion, SessionReused: reused, StartedAt: now}, nil
}

func bindingFromSnapshot(snapshot RunSnapshot, contextID string, contextVersion int64, reused bool) RunBinding {
	now := time.Now().UTC()
	if snapshot.StartedAt != nil {
		now = *snapshot.StartedAt
	}
	return RunBinding{ID: snapshot.ID, ProviderRunID: snapshot.ID, SessionID: snapshot.SessionID, WorkDir: snapshot.WorkDir, ContextID: contextID, ContextVersion: contextVersion, SessionReused: reused, StartedAt: now}
}

func localExecutionContext() (context.Context, context.CancelFunc) {
	value := strings.TrimSpace(os.Getenv("ADRO_EXECUTOR_TIMEOUT"))
	if value == "" {
		return context.WithCancel(context.Background())
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), timeout)
}

func (p *LocalProvider) execute(ctx context.Context, runID, input, workDir, sessionID string, resumed bool) {
	started := time.Now()
	baseline := gitRevision(workDir)
	args := p.commandArgs(input, sessionID, resumed)
	path, pathErr := p.executablePath()
	var output []byte
	var runErr error
	if pathErr == nil {
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Dir = workDir
		stdin, stdinErr := cmd.StdinPipe()
		if stdinErr != nil {
			runErr = stdinErr
		} else {
			var outputBuffer bytes.Buffer
			cmd.Stdout = &outputBuffer
			cmd.Stderr = &outputBuffer
			p.mu.Lock()
			run := p.runs[runID]
			var pending []Interaction
			if run != nil {
				run.stdin = stdin
				pending = append(pending, run.pending...)
				run.pending = nil
			}
			p.mu.Unlock()
			startErr := cmd.Start()
			if startErr != nil {
				runErr = startErr
			} else {
				if run != nil {
					close(run.started)
					run.inputMu.Lock()
					for _, interaction := range pending {
						_, writeErr := io.WriteString(stdin, interaction.Input+"\n")
						p.mu.Lock()
						if current := p.runs[runID]; current != nil {
							status, eventType := "sent", "interaction.sent"
							if writeErr != nil {
								status, eventType = "failed", "interaction.failed"
							}
							previous := current.snapshot
							if updateInteractionLocked(current, interaction.ID, status) {
								appendRuntimeEventLocked(current, eventType, map[string]any{"interaction_id": interaction.ID})
								if err := p.persistLocked(); err != nil {
									current.snapshot = previous
								}
							}
						}
						p.mu.Unlock()
					}
					run.inputMu.Unlock()
				}
				runErr = cmd.Wait()
			}
			if startErr != nil && run != nil {
				close(run.started)
			}
			_ = stdin.Close()
			output = outputBuffer.Bytes()
		}
	} else {
		runErr = pathErr
		p.mu.Lock()
		if run := p.runs[runID]; run != nil {
			close(run.started)
		}
		p.mu.Unlock()
	}
	status := "completed"
	if runErr != nil {
		status = "failed"
	}
	ctxErr := ctx.Err()
	switch {
	case errors.Is(ctxErr, context.DeadlineExceeded):
		// CommandContext returns an implementation-specific kill error when its
		// deadline terminates a child. Expose a stable provider status and reason
		// so the pipeline can distinguish an executor deadline from a real
		// process failure and operators can retry with the same evidence.
		status = "timed_out"
	case errors.Is(ctxErr, context.Canceled):
		status = "cancelled"
	}
	head := gitRevision(workDir)
	dirty, changedFiles := gitChanges(workDir)
	continuity := "unproven"
	discovered := providerSessionID(output, p.executorKind())
	if discovered != "" {
		if p.executorKind() == "codex" && resumed && discovered != sessionID {
			// A resume that opens a different native thread is a new conversation,
			// not a valid continuation. Keep the original session in the snapshot
			// and fail closed so the pipeline cannot silently lose context.
			runErr = fmt.Errorf("codex continuation opened thread %s, expected %s", discovered, sessionID)
		} else {
			sessionID = discovered
			continuity = "proven"
		}
	} else if p.executorKind() == "codex" && resumed {
		// Codex must emit a real thread.started record on every resumed process;
		// without it there is no evidence that the native conversation continued.
		runErr = errors.New("codex continuation did not prove thread.started session")
	}
	if runErr != nil && status == "completed" {
		status = "failed"
	}
	done := time.Now().UTC()
	usage := usageFromOutput(output)
	usage.DurationMS = time.Since(started).Milliseconds()
	toolEvents := extractToolEvents(output, p.executorKind())
	p.mu.Lock()
	run := p.runs[runID]
	persistenceFailure := ""
	if run != nil && run.snapshot.Status == "running" {
		previous := run.snapshot
		run.snapshot.Status = status
		run.snapshot.SessionID = sessionID
		run.snapshot.SessionContinuity = continuity
		run.snapshot.BaselineCommit = baseline
		run.snapshot.HeadCommit = head
		run.snapshot.FinishedAt = &done
		run.snapshot.Usage = usage
		run.snapshot.ToolEvents = append([]ToolEvent(nil), toolEvents...)
		run.snapshot.Output = truncateOutput(output)
		if status == "timed_out" {
			run.snapshot.Error = "executor deadline exceeded"
		} else if runErr != nil {
			run.snapshot.Error = runErr.Error()
		}
		run.snapshot.WorkspaceDirty = dirty
		run.snapshot.ChangedFiles = changedFiles
		run.snapshot.LastEventID = domain.NewID()
		appendRuntimeEventLocked(run, "run.finished", map[string]any{"status": status, "session_id": sessionID, "session_continuity": continuity, "error": run.snapshot.Error})
		if err := p.persistLocked(); err != nil {
			// The process result is not acknowledged as completed when its
			// durable snapshot cannot be written. Preserve the evidence in the
			// live record, but fail closed so callers do not advance on a result
			// that would disappear after restart.
			run.snapshot = previous
			run.snapshot.Status = "failed"
			run.snapshot.SessionID = sessionID
			run.snapshot.SessionContinuity = continuity
			run.snapshot.FinishedAt = &done
			run.snapshot.Usage = usage
			run.snapshot.Output = truncateOutput(output)
			run.snapshot.Error = "durable run snapshot unavailable"
			run.snapshot.WorkspaceDirty = dirty
			run.snapshot.ChangedFiles = changedFiles
			persistenceFailure = err.Error()
			status = "failed"
		}
	}
	p.mu.Unlock()
	if p.runtime != nil {
		scope := runtimekernel.Scope{TenantID: "local", WorkspaceID: "local", SessionID: sessionID, RunID: runID}
		fencingToken := int64(0)
		p.mu.RLock()
		if current := p.runs[runID]; current != nil {
			fencingToken = current.fencingToken
		}
		p.mu.RUnlock()
		_, _ = p.runtime.Append(runtimekernel.Input{EventType: runtimekernel.EventUsage, AggregateType: "run", AggregateID: runID, Scope: scope, CorrelationID: runID, IdempotencyKey: "run:" + runID + ":usage", WriterID: "local", FencingToken: fencingToken, Payload: usage})
		_, _ = p.runtime.FinishTurn(scope, map[string]any{"status": status, "output_sha256": sha256Hex(truncateOutput(output))}, map[string]any{"context_version": 0, "recovery_state": runErr == nil}, "run:"+runID, "local", fencingToken)
	}
	payload := map[string]any{"run_id": runID, "status": status, "output": truncateOutput(output), "duration_ms": time.Since(started).Milliseconds()}
	if runErr != nil {
		payload["error"] = runErr.Error()
	}
	if persistenceFailure != "" {
		payload["error"] = "durable run snapshot unavailable"
	}
	_ = p.Bus.Publish(context.Background(), events.New("execution."+status+".v1", "execution_run", runID, "", "", 2, payload))
}

// extractToolEvents accepts the JSONL shapes emitted by Codex and Claude
// without trusting free-form model text. Unknown records are ignored. The
// resulting sequence is stable and can be replayed into harness checkpoints.
func extractToolEvents(output []byte, kind string) []ToolEvent {
	if kind != "codex" && kind != "claude" {
		return nil
	}
	events := make([]ToolEvent, 0)
	sequence := 0
	for _, line := range bytes.Split(output, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var value map[string]any
		if json.Unmarshal(line, &value) != nil {
			continue
		}
		collectToolValue(value, &events, &sequence)
	}
	return events
}

func collectToolValue(raw any, events *[]ToolEvent, sequence *int) {
	switch value := raw.(type) {
	case []any:
		for _, child := range value {
			collectToolValue(child, events, sequence)
		}
	case map[string]any:
		collectToolEvent(value, events, sequence)
	}
}

func collectToolEvent(value map[string]any, events *[]ToolEvent, sequence *int) {
	if value == nil {
		return
	}
	typ, _ := value["type"].(string)
	item, _ := value["item"].(map[string]any)
	if item == nil {
		item = value
	}
	itemType, _ := item["type"].(string)
	callID := firstString(item, "call_id", "tool_call_id", "id")
	name := firstString(item, "name", "tool_name")
	phase := ""
	lower := strings.ToLower(typ + " " + itemType)
	switch {
	case strings.Contains(lower, "completed") || strings.Contains(lower, "complete") || strings.Contains(lower, "tool_result") || strings.Contains(lower, "result"):
		phase = "after"
	case strings.Contains(lower, "started") || strings.Contains(lower, "start") || strings.Contains(lower, "tool_use") || strings.Contains(lower, "function_call"):
		phase = "before"
	}
	if callID != "" && phase != "" && (strings.Contains(lower, "tool") || strings.Contains(lower, "function") || itemType == "command_execution") {
		payload := firstString(item, "arguments", "input", "output", "aggregated_output", "content")
		*sequence++
		*events = append(*events, ToolEvent{CallID: callID, Name: name, Phase: phase, Payload: payload, Sequence: *sequence})
	}
	for _, key := range []string{"tool", "content_block", "data"} {
		if child := value[key]; child != nil {
			collectToolValue(child, events, sequence)
		}
	}
	if child := value["content"]; child != nil {
		collectToolValue(child, events, sequence)
	}
}

func firstString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if stringValue, ok := value[key].(string); ok {
			return stringValue
		}
		if value[key] != nil {
			data, _ := json.Marshal(value[key])
			if len(data) > 0 {
				return string(data)
			}
		}
	}
	return ""
}

func (p *LocalProvider) commandArgs(input, sessionID string, resumed bool) []string {
	if len(p.Args) > 0 {
		args := make([]string, len(p.Args))
		for i, arg := range p.Args {
			args[i] = arg
		}
		if p.executorKind() == "codex" {
			return p.withCodexSessionArgs(args, input, sessionID, resumed)
		}
		for i, arg := range args {
			args[i] = strings.ReplaceAll(arg, "{input}", input)
		}
		return p.withClaudeSessionArgs(args, sessionID, resumed)
	}
	name := p.executorKind()
	switch name {
	case "claude", "claude-code":
		args := []string{"-p", input, "--output-format", "json", "--permission-mode", "acceptEdits"}
		if uuidPattern.MatchString(sessionID) {
			if resumed {
				args = append(args, "--resume", sessionID)
			} else {
				args = append(args, "--session-id", sessionID)
			}
		}
		return args
	case "codex":
		if resumed && uuidPattern.MatchString(sessionID) {
			return []string{"exec", "resume", "--json", sessionID, input}
		}
		return []string{"exec", "--json", input}
	default:
		return []string{input}
	}
}

func (p *LocalProvider) executorKind() string {
	name := strings.ToLower(filepath.Base(p.Executable))
	switch name {
	case "claude", "claude-code":
		return "claude"
	case "codex":
		return "codex"
	}
	// Package runners such as npx/bunx are supported when their argv names
	// the official Codex package. This keeps ADRO_EXECUTOR_COMMAND useful in
	// environments where the package is installed without a global symlink.
	for _, arg := range p.Args {
		if strings.Contains(strings.ToLower(arg), "@openai/codex") {
			return "codex"
		}
	}
	return name
}

func (p *LocalProvider) withCodexSessionArgs(args []string, input, sessionID string, resumed bool) []string {
	expanded := make([]string, 0, len(args)+3)
	promptIndex := -1
	for _, arg := range args {
		if resumed && arg == "--ephemeral" {
			// An ephemeral thread cannot be resumed after the first process exits.
			continue
		}
		if strings.Contains(arg, "{input}") {
			promptIndex = len(expanded)
		}
		expanded = append(expanded, strings.ReplaceAll(arg, "{input}", input))
	}
	if promptIndex < 0 {
		expanded = append(expanded, input)
		promptIndex = len(expanded) - 1
	}
	if !resumed || !uuidPattern.MatchString(sessionID) {
		expanded, _ = ensureCodexJSON(expanded, promptIndex, -1, -1)
		return expanded
	}

	resumeIndex := -1
	execIndex := -1
	for index, arg := range expanded {
		switch arg {
		case "resume":
			if resumeIndex < 0 {
				resumeIndex = index
			}
		case "exec":
			if execIndex < 0 {
				execIndex = index
			}
		}
	}
	if resumeIndex < 0 {
		if execIndex >= 0 {
			expanded = append(expanded[:execIndex+1], append([]string{"resume"}, expanded[execIndex+1:]...)...)
			resumeIndex = execIndex + 1
			promptIndex++
		} else {
			expanded = append([]string{"exec", "resume"}, expanded...)
			resumeIndex = 1
			promptIndex += 2
		}
	}
	expanded, promptIndex = ensureCodexJSON(expanded, promptIndex, resumeIndex, execIndex)

	for index := resumeIndex + 1; index < promptIndex; index++ {
		if uuidPattern.MatchString(expanded[index]) {
			expanded[index] = sessionID
			return expanded
		}
	}
	expanded = append(expanded[:promptIndex], append([]string{sessionID}, expanded[promptIndex:]...)...)
	return expanded
}

func ensureCodexJSON(args []string, promptIndex, resumeIndex, execIndex int) ([]string, int) {
	for _, arg := range args {
		if arg == "--json" {
			return args, promptIndex
		}
	}
	insertion := promptIndex
	if resumeIndex >= 0 {
		insertion = resumeIndex + 1
	} else if execIndex >= 0 {
		insertion = execIndex + 1
	}
	if insertion > promptIndex {
		insertion = promptIndex
	}
	args = append(args[:insertion], append([]string{"--json"}, args[insertion:]...)...)
	if insertion <= promptIndex {
		promptIndex++
	}
	return args, promptIndex
}

// withClaudeSessionArgs keeps custom Claude commands on the same native
// conversation contract as the built-in command. Operators may provide the
// flags themselves; otherwise ADRO appends the appropriate initial or resume
// flag after replacing {input}.
func (p *LocalProvider) withClaudeSessionArgs(args []string, sessionID string, resumed bool) []string {
	if p.executorKind() != "claude" || !uuidPattern.MatchString(sessionID) {
		return args
	}
	for _, arg := range args {
		if arg == "--resume" || strings.HasPrefix(arg, "--resume=") || arg == "--session-id" || strings.HasPrefix(arg, "--session-id=") {
			return args
		}
	}
	flag := "--session-id"
	if resumed {
		flag = "--resume"
	}
	return append(args, flag, sessionID)
}

func providerSessionID(output []byte, kind string) string {
	if kind != "codex" {
		return ""
	}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	for scanner.Scan() {
		var event struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Type != "thread.started" {
			continue
		}
		if uuidPattern.MatchString(event.ThreadID) {
			return event.ThreadID
		}
	}
	return ""
}

func newLocalSessionID() string {
	var raw [16]byte
	if _, err := crand.Read(raw[:]); err != nil {
		return formatSessionID(domain.NewID())
	}
	// RFC 4122 version 4 / variant 1 keeps the ID accepted by Claude Code.
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

func formatSessionID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 32 {
		return value[:8] + "-" + value[8:12] + "-4" + value[13:16] + "-8" + value[17:20] + "-" + value[20:]
	}
	return value
}

func (p *LocalProvider) workDir(workItemID, sessionID string) (string, error) {
	if err := validatePathComponent(workItemID); err != nil {
		return "", err
	}
	p.mu.RLock()
	if existing := p.workdirs[workItemID]; existing != "" {
		p.mu.RUnlock()
		return existing, nil
	}
	p.mu.RUnlock()
	key := sessionID
	if key == "" {
		key = domain.NewID()
	}
	return filepath.Join(p.WorkRoot, workItemID, key), nil
}

func (p *LocalProvider) executablePath() (string, error) {
	if strings.TrimSpace(p.Executable) == "" {
		return "", &UpstreamError{Code: ErrorConfiguration}
	}
	path, err := exec.LookPath(p.Executable)
	if err != nil {
		return "", &UpstreamError{Code: ErrorConfiguration}
	}
	return path, nil
}

func (p *LocalProvider) AppendInput(ctx context.Context, runID, input string) error {
	return p.appendInput(ctx, runID, input, "")
}

func (p *LocalProvider) AppendInputWithKey(ctx context.Context, runID, input, key string) error {
	return p.appendInput(ctx, runID, input, key)
}

func (p *LocalProvider) appendInput(ctx context.Context, runID, input, key string) error {
	input = strings.TrimSpace(input)
	if input == "" {
		return errors.New("input is required")
	}
	p.mu.Lock()
	run := p.runs[runID]
	if run == nil {
		p.mu.Unlock()
		return errors.New("run not found")
	}
	if run.snapshot.Status != "running" {
		p.mu.Unlock()
		return errors.New("run is not running")
	}
	key = strings.TrimSpace(key)
	var interaction Interaction
	if key != "" {
		for _, existing := range run.snapshot.Interactions {
			if existing.IdempotencyKey != key {
				continue
			}
			if existing.Input != input {
				p.mu.Unlock()
				return fmt.Errorf("%w: interaction idempotency key maps to different input", ErrConflict)
			}
			interaction = existing
			if existing.Status == "sent" || run.stdin == nil {
				p.mu.Unlock()
				return nil
			}
			break
		}
	}
	previousSnapshot := run.snapshot
	previousPending := append([]Interaction(nil), run.pending...)
	created := interaction.ID == ""
	if interaction.ID == "" {
		interaction = Interaction{ID: domain.NewID(), RunID: runID, Sequence: int64(len(run.snapshot.Interactions) + 1), Input: input, IdempotencyKey: key, Status: "accepted", CreatedAt: time.Now().UTC()}
		interaction.Hash = hashInteraction(interaction)
		run.snapshot.Interactions = append(run.snapshot.Interactions, interaction)
		appendRuntimeEventLocked(run, "interaction.accepted", map[string]any{"interaction_id": interaction.ID, "sequence": interaction.Sequence, "input_sha256": sha256Hex(input)})
		if run.stdin == nil {
			run.pending = append(run.pending, interaction)
		}
	}
	if created {
		if err := p.persistLocked(); err != nil {
			run.snapshot = previousSnapshot
			run.pending = previousPending
			returnErr := fmt.Errorf("persist interaction: %w", err)
			p.mu.Unlock()
			return returnErr
		}
	}
	stdin := run.stdin
	started := run.started
	p.mu.Unlock()
	if stdin == nil {
		return nil
	}
	select {
	case <-started:
	case <-ctx.Done():
		return ctx.Err()
	}
	run.inputMu.Lock()
	defer run.inputMu.Unlock()
	// Re-check the durable interaction state after serializing writers. A
	// concurrent retry may have delivered this idempotency key while we waited.
	p.mu.RLock()
	current := p.runs[runID]
	status := ""
	if current != nil {
		for _, existing := range current.snapshot.Interactions {
			if existing.ID == interaction.ID {
				status = existing.Status
				break
			}
		}
	}
	p.mu.RUnlock()
	if status == "sent" {
		return nil
	}
	if _, err := io.WriteString(stdin, input+"\n"); err != nil {
		p.mu.Lock()
		if current := p.runs[runID]; current != nil {
			previous := current.snapshot
			if updateInteractionLocked(current, interaction.ID, "failed") {
				appendRuntimeEventLocked(current, "interaction.failed", map[string]any{"interaction_id": interaction.ID})
				if persistErr := p.persistLocked(); persistErr != nil {
					current.snapshot = previous
				}
			}
		}
		p.mu.Unlock()
		return fmt.Errorf("write provider input: %w", err)
	}
	p.mu.Lock()
	if current := p.runs[runID]; current != nil {
		previousSnapshot := current.snapshot
		if updateInteractionLocked(current, interaction.ID, "sent") {
			appendRuntimeEventLocked(current, "interaction.sent", map[string]any{"interaction_id": interaction.ID})
		}
		if err := p.persistLocked(); err != nil {
			current.snapshot = previousSnapshot
			p.mu.Unlock()
			return fmt.Errorf("persist interaction delivery: %w", err)
		}
	}
	p.mu.Unlock()
	return nil
}

func sha256Hex(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func hashInteraction(interaction Interaction) string {
	copy := interaction
	copy.Hash = ""
	data, _ := json.Marshal(copy)
	return sha256Hex(string(data))
}

func updateInteractionLocked(run *localRun, interactionID, status string) bool {
	if run == nil || interactionID == "" {
		return false
	}
	for index := range run.snapshot.Interactions {
		if run.snapshot.Interactions[index].ID == interactionID {
			run.snapshot.Interactions[index].Status = status
			run.snapshot.Interactions[index].Hash = hashInteraction(run.snapshot.Interactions[index])
			return true
		}
	}
	return false
}

func appendRuntimeEventLocked(run *localRun, typ string, payload map[string]any) RuntimeEvent {
	prev := ""
	if n := len(run.snapshot.Ledger); n > 0 {
		prev = run.snapshot.Ledger[n-1].Hash
	}
	event := RuntimeEvent{ID: domain.NewID(), RunID: run.snapshot.ID, Sequence: int64(len(run.snapshot.Ledger) + 1), Type: typ, Payload: payload, PrevHash: prev, CreatedAt: time.Now().UTC()}
	copy := event
	copy.Hash = ""
	data, _ := json.Marshal(copy)
	event.Hash = sha256Hex(string(data))
	run.snapshot.Ledger = append(run.snapshot.Ledger, event)
	return event
}

func (p *LocalProvider) CancelRun(_ context.Context, runID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	run := p.runs[runID]
	if run == nil {
		return errors.New("run not found")
	}
	if run.snapshot.Status != "running" {
		return errors.New("run is not running")
	}
	previous := run.snapshot
	run.cancel()
	run.snapshot.Status = "cancelled"
	now := time.Now().UTC()
	run.snapshot.FinishedAt = &now
	appendRuntimeEventLocked(run, "run.cancelled", map[string]any{"reason": "requested"})
	if err := p.persistLocked(); err != nil {
		run.snapshot = previous
		return fmt.Errorf("persist cancelled local run: %w", err)
	}
	return nil
}

func (p *LocalProvider) GetRun(_ context.Context, runID string) (RunSnapshot, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	run := p.runs[runID]
	if run == nil {
		return RunSnapshot{}, errors.New("run not found")
	}
	return cloneRunSnapshot(run.snapshot), nil
}

func cloneRunSnapshot(snapshot RunSnapshot) RunSnapshot {
	snapshot.ChangedFiles = append([]string(nil), snapshot.ChangedFiles...)
	snapshot.ToolEvents = append([]ToolEvent(nil), snapshot.ToolEvents...)
	snapshot.Interactions = append([]Interaction(nil), snapshot.Interactions...)
	snapshot.Ledger = make([]RuntimeEvent, len(snapshot.Ledger))
	for i, event := range snapshot.Ledger {
		event.Payload = clonePayload(event.Payload)
		snapshot.Ledger[i] = event
	}
	return snapshot
}

func clonePayload(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return input
	}
	var output map[string]any
	if json.Unmarshal(data, &output) != nil {
		return input
	}
	return output
}

func (p *LocalProvider) StreamEvents(ctx context.Context, runID, cursor string) (EventStream, error) {
	if _, err := p.GetRun(ctx, runID); err != nil {
		return EventStream{}, err
	}
	streamCtx, cancelStream := context.WithCancel(ctx)
	source, cancelSource := p.Bus.Subscribe(64)
	filtered := make(chan events.Envelope, 64)
	go func() {
		defer close(filtered)
		defer cancelSource()
		seen := make(map[string]struct{})
		send := func(event events.Envelope) bool {
			if event.EventID != "" {
				if _, duplicate := seen[event.EventID]; duplicate {
					return true
				}
				seen[event.EventID] = struct{}{}
			}
			if event.AggregateID != runID && event.Payload["run_id"] != runID {
				return true
			}
			select {
			case filtered <- event:
				return true
			case <-streamCtx.Done():
				return false
			}
		}

		// Subscribe before replay to close the publication race. A provider
		// stream must honor its cursor after a reconnect; replayed events are
		// de-duplicated against the live buffer in case both contain the same
		// envelope.
		replayCursor := cursor
		for {
			page, next, err := p.Bus.ListChecked("", replayCursor, 250)
			if err != nil {
				return
			}
			if len(page) == 0 {
				break
			}
			for _, event := range page {
				if !send(event) {
					return
				}
			}
			if next == "" || next == replayCursor {
				break
			}
			replayCursor = next
		}
		for {
			select {
			case <-streamCtx.Done():
				return
			case event, ok := <-source:
				if !ok {
					return
				}
				if !send(event) {
					return
				}
			}
		}
	}()
	return EventStream{Events: filtered, Close: func() { cancelStream(); cancelSource() }}, nil
}

func (p *LocalProvider) GetUsage(ctx context.Context, runID string) (Usage, error) {
	run, err := p.GetRun(ctx, runID)
	return run.Usage, err
}

func (p *LocalProvider) Health(context.Context) (ProviderHealth, error) {
	if _, err := p.executablePath(); err != nil {
		return ProviderHealth{Healthy: false, Message: "configured executor is unavailable"}, err
	}
	return ProviderHealth{Healthy: true, Message: "local executor ready"}, nil
}

func (p *LocalProvider) PublishAttachment(_ context.Context, spec AttachmentSpec) (AttachmentReceipt, error) {
	if spec.TargetType == "" || spec.TargetID == "" || len(spec.Content) == 0 {
		return AttachmentReceipt{}, errors.New("attachment target and content are required")
	}
	return AttachmentReceipt{ID: domain.NewID(), ProviderAttachmentID: "local-" + domain.NewID(), Status: "accepted", ArtifactURI: spec.ArtifactURI}, nil
}

func gitRevision(workDir string) string {
	cmd := exec.Command("git", "-C", workDir, "rev-parse", "HEAD")
	value, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(value))
}

func gitChanges(workDir string) (bool, []string) {
	cmd := exec.Command("git", "-C", workDir, "status", "--porcelain=v1")
	value, err := cmd.Output()
	if err != nil {
		return false, nil
	}
	lines := strings.Split(strings.TrimSpace(string(value)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return false, nil
	}
	changed := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) > 3 {
			line = line[3:]
		}
		if line != "" {
			changed = append(changed, line)
		}
	}
	return len(changed) > 0, changed
}

func (p *LocalProvider) prepareWorkDir(ctx context.Context, workDir string, item localWorkItem) error {
	if err := os.MkdirAll(filepath.Dir(workDir), 0o750); err != nil {
		return fmt.Errorf("create workspace parent: %w", err)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".git")); err == nil {
		return nil
	}
	if item.RepositoryPath == "" && item.CloneURL == "" {
		return os.MkdirAll(workDir, 0o750)
	}
	if item.RepositoryPath != "" {
		if err := validateExistingDirectory(item.RepositoryPath); err != nil {
			return err
		}
		if err := runGit(ctx, "clone", "--local", "--no-hardlinks", item.RepositoryPath, workDir); err != nil {
			return fmt.Errorf("clone local repository: %w", err)
		}
	} else {
		if err := runGit(ctx, "clone", "--no-hardlinks", item.CloneURL, workDir); err != nil {
			return fmt.Errorf("clone repository: %w", err)
		}
	}
	if item.DefaultBranch != "" {
		if err := runGit(ctx, "-C", workDir, "checkout", item.DefaultBranch); err != nil {
			return fmt.Errorf("checkout default branch: %w", err)
		}
	}
	return nil
}

func validateExistingDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("repository path is unavailable: %w", err)
	}
	if !info.IsDir() {
		return errors.New("repository path is not a directory")
	}
	return nil
}

func runGit(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func validatePathComponent(value string) error {
	if strings.TrimSpace(value) == "" || value == "." || value == ".." || filepath.Base(value) != value {
		return errors.New("work item id must be a single safe path component")
	}
	return nil
}

func (p *LocalProvider) persistLocked() error {
	if p.StatePath == "" {
		return nil
	}
	return durable.WithExclusive(p.StatePath, func() error {
		diskRevision, err := localPersistedRevision(p.StatePath)
		if err != nil {
			return err
		}
		if diskRevision != p.revision {
			return fmt.Errorf("stale local provider state: expected revision %d, found %d", p.revision, diskRevision)
		}
		state := localState{Revision: p.revision + 1, Runs: map[string]RunSnapshot{}, RunKeys: map[string]string{}, Workdirs: map[string]string{}, Issues: map[string]string{}, Items: map[string]localWorkItem{}}
		for id, run := range p.runs {
			state.Runs[id] = run.snapshot
		}
		for key, id := range p.runKeys {
			state.RunKeys[key] = id
		}
		for key, value := range p.workdirs {
			state.Workdirs[key] = value
		}
		for key, value := range p.issues {
			state.Issues[key] = value
		}
		for key, value := range p.items {
			state.Items[key] = value
		}
		data, err := json.Marshal(state)
		if err != nil {
			return err
		}
		dir := filepath.Dir(p.StatePath)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(dir, ".adro-runs-*")
		if err != nil {
			return err
		}
		tmpName := tmp.Name()
		defer os.Remove(tmpName)
		if err := tmp.Chmod(0o600); err != nil {
			_ = tmp.Close()
			return err
		}
		if _, err := tmp.Write(data); err != nil {
			_ = tmp.Close()
			return err
		}
		if err := tmp.Sync(); err != nil {
			_ = tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := durable.Inject("provider.snapshot.before_rename"); err != nil {
			return err
		}
		if err := os.Rename(tmpName, p.StatePath); err != nil {
			return err
		}
		if dirFile, openErr := os.Open(dir); openErr == nil {
			if syncErr := dirFile.Sync(); syncErr != nil {
				_ = dirFile.Close()
				return syncErr
			}
			_ = dirFile.Close()
		}
		p.revision = state.Revision
		return nil
	})
}

func localPersistedRevision(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read local provider revision: %w", err)
	}
	var state localState
	if err := json.Unmarshal(data, &state); err != nil {
		return 0, fmt.Errorf("decode local provider revision: %w", err)
	}
	return state.Revision, nil
}

func (p *LocalProvider) loadState() error {
	data, err := os.ReadFile(p.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read local execution state: %w", err)
	}
	var state localState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode local execution state: %w", err)
	}
	p.revision = state.Revision
	for id, snapshot := range state.Runs {
		if err := validateRuntimeSnapshot(snapshot); err != nil {
			return err
		}
		run := &localRun{snapshot: snapshot}
		if snapshot.Status == "running" {
			run.snapshot.Status = "failed"
			run.snapshot.Error = "local executor process was interrupted by an API restart"
			run.snapshot.RecoveryState = "reconcile_required"
			run.snapshot.RecoveryReason = "provider_process_lost_on_restart"
			now := time.Now().UTC()
			run.snapshot.FinishedAt = &now
			appendRuntimeEventLocked(run, "run.recovery_required", map[string]any{
				"reason":     "provider_process_lost_on_restart",
				"continuity": run.snapshot.SessionContinuity,
				"session_id": run.snapshot.SessionID,
			})
		}
		p.runs[id] = run
	}
	for key, id := range state.RunKeys {
		if _, exists := p.runs[id]; exists {
			p.runKeys[key] = id
		}
	}
	for key, value := range state.Workdirs {
		p.workdirs[key] = value
	}
	for key, value := range state.Issues {
		p.issues[key] = value
	}
	for key, value := range state.Items {
		p.items[key] = value
	}
	return p.persistLoadedState()
}

func validateRuntimeSnapshot(snapshot RunSnapshot) error {
	if snapshot.ID == "" {
		return errors.New("local execution state contains a run without id")
	}
	previous := ""
	for index, event := range snapshot.Ledger {
		if event.ID == "" || event.RunID != snapshot.ID || event.Sequence != int64(index+1) || event.PrevHash != previous || event.Hash == "" {
			return fmt.Errorf("local execution ledger is corrupt for run %s", snapshot.ID)
		}
		copy := event
		copy.Hash = ""
		data, _ := json.Marshal(copy)
		if got := sha256Hex(string(data)); got != event.Hash {
			return fmt.Errorf("local execution ledger hash mismatch for run %s", snapshot.ID)
		}
		previous = event.Hash
	}
	for index, interaction := range snapshot.Interactions {
		if interaction.ID == "" || interaction.RunID != snapshot.ID || interaction.Sequence != int64(index+1) || interaction.Hash == "" || hashInteraction(interaction) != interaction.Hash {
			return fmt.Errorf("local interaction ledger is corrupt for run %s", snapshot.ID)
		}
	}
	return nil
}

func (p *LocalProvider) persistLoadedState() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.persistLocked()
}

func truncateOutput(value []byte) string {
	const max = 16 << 10
	if len(value) > max {
		return string(value[:max]) + "..."
	}
	return string(value)
}

// usageFromOutput extracts the stable usage fields emitted by non-interactive
// coding CLIs. Unknown output formats deliberately produce an empty usage
// record; the process result remains valid and the duration is still captured.
func usageFromOutput(output []byte) Usage {
	var result struct {
		Usage struct {
			InputTokens               int64 `json:"input_tokens"`
			OutputTokens              int64 `json:"output_tokens"`
			CacheReadTokens           int64 `json:"cache_read_input_tokens"`
			CacheWriteTokens          int64 `json:"cache_creation_input_tokens"`
			CacheReadTokensAlternate  int64 `json:"cache_read_tokens"`
			CacheWriteTokensAlternate int64 `json:"cache_write_tokens"`
		} `json:"usage"`
		TotalCostUSD float64 `json:"total_cost_usd"`
		CostUSD      float64 `json:"cost_usd"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &result); err != nil {
		return Usage{}
	}
	cacheRead := result.Usage.CacheReadTokens
	if cacheRead == 0 {
		cacheRead = result.Usage.CacheReadTokensAlternate
	}
	cacheWrite := result.Usage.CacheWriteTokens
	if cacheWrite == 0 {
		cacheWrite = result.Usage.CacheWriteTokensAlternate
	}
	cost := result.TotalCostUSD
	if cost == 0 {
		cost = result.CostUSD
	}
	return Usage{InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens, CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite, EstimatedCost: cost}
}
