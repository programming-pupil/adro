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
	"github.com/adro-project/adro/internal/events"
)

type localRun struct {
	snapshot RunSnapshot
	cancel   context.CancelFunc
	input    string
}

type localWorkItem struct {
	RepositoryPath string `json:"repository_path,omitempty"`
	CloneURL       string `json:"clone_url,omitempty"`
	DefaultBranch  string `json:"default_branch,omitempty"`
}

type localState struct {
	Runs     map[string]RunSnapshot   `json:"runs"`
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
	runs     map[string]*localRun
	workdirs map[string]string
	issues   map[string]string
	items    map[string]localWorkItem
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
		WorkRoot: workRoot, Bus: bus, runs: map[string]*localRun{}, workdirs: map[string]string{}, issues: map[string]string{}, items: map[string]localWorkItem{},
	}
}

// NewPersistentLocalProvider restores run/workspace provenance from an
// operator-owned file. A process cannot resume a child process after a crash,
// so runs that were active at restart are marked failed with a durable reason.
// Their session and workdir remain available for a deliberate repair attempt.
func NewPersistentLocalProvider(executable string, args []string, workRoot, statePath string, bus *events.Bus) (*LocalProvider, error) {
	p := NewLocalProvider(executable, args, workRoot, bus)
	p.StatePath = strings.TrimSpace(statePath)
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
		Features: []string{"agent.v1", "project.resources.v1", "issue.child.v1", "run.snapshot.v1", "runtime.worktree.v1", "usage.tokens.v1", "attachment.v1", "run.repair.v1"},
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
	return p.start(ctx, command.WorkItemID, command.ProviderIssueID, command.Input, sessionID, workDir, command.ContextID, command.ContextVersion, command.SessionID != "")
}

func (p *LocalProvider) ContinueWorkItem(ctx context.Context, command ContinuationCommand) (RunBinding, error) {
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
	if err := p.prepareWorkDir(ctx, workDir, item); err != nil {
		return RunBinding{}, err
	}
	return p.start(ctx, workItemID, command.IssueID, command.Input, command.ExpectedSessionID, workDir, "", 0, true)
}

func (p *LocalProvider) start(ctx context.Context, workItemID, issueID, input, sessionID, workDir, contextID string, contextVersion int64, reused bool) (RunBinding, error) {
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
	snapshot := RunSnapshot{ID: id, WorkItemID: workItemID, ProviderIssueID: issueID, Status: "running", SessionID: sessionID, WorkDir: workDir, StartedAt: &now}
	p.mu.Lock()
	p.runs[id] = &localRun{snapshot: snapshot, cancel: cancel, input: input}
	previousWorkDir, workDirExisted := p.workdirs[workItemID]
	p.workdirs[workItemID] = workDir
	if err := p.persistLocked(); err != nil {
		delete(p.runs, id)
		if workDirExisted {
			p.workdirs[workItemID] = previousWorkDir
		} else {
			delete(p.workdirs, workItemID)
		}
		p.mu.Unlock()
		cancel()
		return RunBinding{}, fmt.Errorf("persist local run: %w", err)
	}
	p.mu.Unlock()
	digest := sha256.Sum256([]byte(input))
	_ = p.Bus.Publish(runCtx, events.New("execution.started.v1", "execution_run", id, "", "", 1, map[string]any{"work_item_id": workItemID, "input_sha256": hex.EncodeToString(digest[:]), "session_id": sessionID, "work_dir": workDir}))
	go p.execute(runCtx, id, input, workDir, sessionID, reused)
	return RunBinding{ID: id, ProviderRunID: id, SessionID: sessionID, WorkDir: workDir, ContextID: contextID, ContextVersion: contextVersion, SessionReused: reused, StartedAt: now}, nil
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
		output, runErr = cmd.CombinedOutput()
	} else {
		runErr = pathErr
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
	if discovered := providerSessionID(output, p.executorKind()); discovered != "" {
		sessionID = discovered
	}
	done := time.Now().UTC()
	usage := usageFromOutput(output)
	usage.DurationMS = time.Since(started).Milliseconds()
	p.mu.Lock()
	run := p.runs[runID]
	persistenceFailure := ""
	if run != nil && run.snapshot.Status == "running" {
		previous := run.snapshot
		run.snapshot.Status = status
		run.snapshot.SessionID = sessionID
		run.snapshot.BaselineCommit = baseline
		run.snapshot.HeadCommit = head
		run.snapshot.FinishedAt = &done
		run.snapshot.Usage = usage
		run.snapshot.Output = truncateOutput(output)
		if status == "timed_out" {
			run.snapshot.Error = "executor deadline exceeded"
		} else if runErr != nil {
			run.snapshot.Error = runErr.Error()
		}
		run.snapshot.WorkspaceDirty = dirty
		run.snapshot.ChangedFiles = changedFiles
		run.snapshot.LastEventID = domain.NewID()
		if err := p.persistLocked(); err != nil {
			// The process result is not acknowledged as completed when its
			// durable snapshot cannot be written. Preserve the evidence in the
			// live record, but fail closed so callers do not advance on a result
			// that would disappear after restart.
			run.snapshot = previous
			run.snapshot.Status = "failed"
			run.snapshot.SessionID = sessionID
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
	payload := map[string]any{"run_id": runID, "status": status, "output": truncateOutput(output), "duration_ms": time.Since(started).Milliseconds()}
	if runErr != nil {
		payload["error"] = runErr.Error()
	}
	if persistenceFailure != "" {
		payload["error"] = "durable run snapshot unavailable"
	}
	_ = p.Bus.Publish(context.Background(), events.New("execution."+status+".v1", "execution_run", runID, "", "", 2, payload))
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

func (p *LocalProvider) AppendInput(_ context.Context, runID, input string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	run := p.runs[runID]
	if run == nil {
		return errors.New("run not found")
	}
	if run.snapshot.Status != "running" {
		return errors.New("run is not running")
	}
	_ = input
	return &CapabilityError{Capability: "run.messages.v1", AdapterVersion: "local-exec-v1"}
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
	return run.snapshot, nil
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
			page, next := p.Bus.List("", replayCursor, 250)
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
	state := localState{Runs: map[string]RunSnapshot{}, Workdirs: map[string]string{}, Issues: map[string]string{}, Items: map[string]localWorkItem{}}
	for id, run := range p.runs {
		state.Runs[id] = run.snapshot
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
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, p.StatePath)
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
	for id, snapshot := range state.Runs {
		if snapshot.Status == "running" {
			snapshot.Status = "failed"
			snapshot.Error = "local executor process was interrupted by an API restart"
			now := time.Now().UTC()
			snapshot.FinishedAt = &now
		}
		p.runs[id] = &localRun{snapshot: snapshot}
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
