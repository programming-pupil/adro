// Package runner contains the control-plane side of Runner registration and
// capacity management. Execution remains behind the provider SPI.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/internal/domain"
)

type Status string

const (
	Registered  Status = "REGISTERED"
	Healthy     Status = "HEALTHY"
	Draining    Status = "DRAINING"
	Offline     Status = "OFFLINE"
	Quarantined Status = "QUARANTINED"
)

type Runner struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	WorkspaceID    string    `json:"workspace_id"`
	TenantID       string    `json:"tenant_id"`
	Provider       string    `json:"provider"`
	Version        string    `json:"version"`
	Capabilities   []string  `json:"capabilities"`
	SecurityDomain string    `json:"security_domain"`
	CPUCores       int       `json:"cpu_cores"`
	MemoryBytes    int64     `json:"memory_bytes"`
	DiskBytes      int64     `json:"disk_bytes"`
	Concurrency    int       `json:"concurrency"`
	ActiveRuns     int       `json:"active_runs"`
	Status         Status    `json:"status"`
	LastHeartbeat  time.Time `json:"last_heartbeat"`
	WorkspaceRoot  string    `json:"workspace_root"`
}

type ExecuteRequest struct {
	RunnerID string            `json:"runner_id"`
	WorkDir  string            `json:"work_dir"`
	Command  []string          `json:"command"`
	Env      map[string]string `json:"env,omitempty"`
	Timeout  time.Duration     `json:"timeout_ms,omitempty"`
}

type ExecuteResult struct {
	RunnerID   string `json:"runner_id"`
	WorkDir    string `json:"work_dir"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

type Supervisor struct {
	mu      sync.RWMutex
	runners map[string]Runner
	path    string
}

func NewSupervisor() *Supervisor { return &Supervisor{runners: map[string]Runner{}} }

func NewPersistentSupervisor(path string) (*Supervisor, error) {
	s := &Supervisor{runners: map[string]Runner{}, path: path}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.runners); err != nil {
		return nil, err
	}
	// A process restart invalidates in-flight execution counters and the last
	// health assertion. Require a fresh heartbeat before scheduling work again.
	for id, runner := range s.runners {
		runner.ActiveRuns = 0
		if runner.Status == Healthy {
			runner.Status = Offline
		}
		s.runners[id] = runner
	}
	return s, nil
}

func (s *Supervisor) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistLocked()
}

func (s *Supervisor) persistLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.Marshal(s.runners)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".adro-runners-*")
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
	return os.Rename(tmpName, s.path)
}

func (s *Supervisor) Register(r Runner) (Runner, error) {
	if r.Name == "" || r.Provider == "" || r.Version == "" {
		return Runner{}, errors.New("name, provider and version are required")
	}
	if r.ID == "" {
		r.ID = domain.NewID()
	}
	if r.Concurrency < 1 {
		r.Concurrency = 1
	}
	if r.Status == "" {
		r.Status = Registered
	}
	r.LastHeartbeat = time.Now().UTC()
	s.mu.Lock()
	previous, existed := s.runners[r.ID]
	s.runners[r.ID] = r
	if err := s.persistLocked(); err != nil {
		if existed {
			s.runners[r.ID] = previous
		} else {
			delete(s.runners, r.ID)
		}
		s.mu.Unlock()
		return Runner{}, fmt.Errorf("persist runner registration: %w", err)
	}
	s.mu.Unlock()
	return r, nil
}
func (s *Supervisor) Heartbeat(id string, activeRuns int) (Runner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runners[id]
	if !ok {
		return Runner{}, errors.New("runner not found")
	}
	if r.Status == Offline {
		r.Status = Healthy
	}
	if r.Status == Registered {
		r.Status = Healthy
	}
	r.ActiveRuns = activeRuns
	r.LastHeartbeat = time.Now().UTC()
	previous := s.runners[id]
	s.runners[id] = r
	if err := s.persistLocked(); err != nil {
		s.runners[id] = previous
		return Runner{}, fmt.Errorf("persist runner heartbeat: %w", err)
	}
	return r, nil
}
func (s *Supervisor) SetStatus(id string, status Status) (Runner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runners[id]
	if !ok {
		return Runner{}, errors.New("runner not found")
	}
	switch status {
	case Registered, Healthy, Draining, Offline, Quarantined:
	default:
		return Runner{}, errors.New("invalid runner status")
	}
	previous := r
	r.Status = status
	s.runners[id] = r
	if err := s.persistLocked(); err != nil {
		s.runners[id] = previous
		return Runner{}, fmt.Errorf("persist runner status: %w", err)
	}
	return r, nil
}
func (s *Supervisor) List() []Runner {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]Runner, 0, len(s.runners))
	for _, r := range s.runners {
		items = append(items, r)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

// ListForScope returns only runners registered for the requested workspace and
// tenant. An empty scope is intentionally unrestricted for trusted machine
// callers and backwards-compatible control-plane tooling.
func (s *Supervisor) ListForScope(workspaceID, tenantID string) []Runner {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]Runner, 0, len(s.runners))
	for _, r := range s.runners {
		if runnerInScope(r, workspaceID, tenantID) {
			items = append(items, r)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (s *Supervisor) Get(id string) (Runner, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.runners[id]
	if !ok {
		return Runner{}, errors.New("runner not found")
	}
	return r, nil
}

func (s *Supervisor) BelongsToScope(id, workspaceID, tenantID string) bool {
	r, err := s.Get(id)
	return err == nil && runnerInScope(r, workspaceID, tenantID)
}

func runnerInScope(r Runner, workspaceID, tenantID string) bool {
	workspaceID = strings.TrimSpace(workspaceID)
	tenantID = strings.TrimSpace(tenantID)
	if workspaceID != "" && strings.TrimSpace(r.WorkspaceID) != workspaceID {
		return false
	}
	if tenantID != "" && strings.TrimSpace(r.TenantID) != tenantID {
		return false
	}
	return true
}
func (s *Supervisor) Choose(securityDomain string) (Runner, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var selected Runner
	found := false
	for _, r := range s.runners {
		if r.Status != Healthy || r.ActiveRuns >= r.Concurrency {
			continue
		}
		if securityDomain != "" && r.SecurityDomain != securityDomain {
			continue
		}
		if !found || r.ActiveRuns < selected.ActiveRuns {
			selected = r
			found = true
		}
	}
	if !found {
		return Runner{}, errors.New("no runner capacity")
	}
	return selected, nil
}
func (s *Supervisor) Reap(after time.Duration) []Runner {
	cutoff := time.Now().Add(-after)
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := make(map[string]Runner, len(s.runners))
	for id, runner := range s.runners {
		previous[id] = runner
	}
	changed := []Runner{}
	for id, r := range s.runners {
		if r.Status == Healthy && r.LastHeartbeat.Before(cutoff) {
			r.Status = Offline
			s.runners[id] = r
			changed = append(changed, r)
		}
	}
	if len(changed) > 0 {
		if err := s.persistLocked(); err != nil {
			// Reaping is a durable health decision. If it cannot be written,
			// retain the prior state and return no acknowledgements so a caller
			// cannot act on an offline transition that would vanish on restart.
			s.runners = previous
			return nil
		}
	}
	return changed
}

// Execute provides the reference Runner boundary. Commands are argv arrays,
// never shell strings; execution is confined to the registered workspace root,
// inherits no ambient environment, and is capacity-accounted as one run.
// Production deployments should replace this method with a rootless/VM worker
// while retaining the request/result and audit contracts.
func (s *Supervisor) Execute(ctx context.Context, request ExecuteRequest) (result ExecuteResult, err error) {
	if len(request.Command) == 0 || strings.TrimSpace(request.Command[0]) == "" {
		return ExecuteResult{}, errors.New("command is required")
	}
	if len(request.Command) > 64 {
		return ExecuteResult{}, errors.New("command has too many arguments")
	}
	s.mu.Lock()
	r, ok := s.runners[request.RunnerID]
	if !ok {
		s.mu.Unlock()
		return ExecuteResult{}, errors.New("runner not found")
	}
	if r.Status != Healthy {
		s.mu.Unlock()
		return ExecuteResult{}, fmt.Errorf("runner is not healthy")
	}
	if r.ActiveRuns >= r.Concurrency {
		s.mu.Unlock()
		return ExecuteResult{}, errors.New("no runner capacity")
	}
	root, err := filepath.Abs(r.WorkspaceRoot)
	if err != nil || root == "." || root == string(filepath.Separator) || strings.TrimSpace(r.WorkspaceRoot) == "" {
		s.mu.Unlock()
		return ExecuteResult{}, errors.New("runner workspace_root must be an explicit directory")
	}
	rootInfo, rootStatErr := os.Stat(root)
	if rootStatErr != nil || !rootInfo.IsDir() {
		s.mu.Unlock()
		return ExecuteResult{}, errors.New("runner workspace_root is not an existing directory")
	}
	resolvedRoot, resolveErr := filepath.EvalSymlinks(root)
	if resolveErr != nil {
		s.mu.Unlock()
		return ExecuteResult{}, errors.New("runner workspace_root cannot be resolved")
	}
	workDir := request.WorkDir
	if workDir == "" {
		workDir = root
	}
	workDir, err = filepath.Abs(workDir)
	if err != nil {
		s.mu.Unlock()
		return ExecuteResult{}, errors.New("invalid work_dir")
	}
	if info, statErr := os.Stat(workDir); statErr != nil || !info.IsDir() {
		s.mu.Unlock()
		return ExecuteResult{}, errors.New("work_dir is not an existing directory")
	}
	resolvedWorkDir, resolveErr := filepath.EvalSymlinks(workDir)
	if resolveErr != nil {
		s.mu.Unlock()
		return ExecuteResult{}, errors.New("work_dir cannot be resolved")
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedWorkDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		s.mu.Unlock()
		return ExecuteResult{}, errors.New("work_dir is outside runner workspace_root")
	}
	previous := r
	r.ActiveRuns++
	s.runners[r.ID] = r
	if err := s.persistLocked(); err != nil {
		s.runners[r.ID] = previous
		s.mu.Unlock()
		return ExecuteResult{}, fmt.Errorf("persist runner capacity: %w", err)
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if current, exists := s.runners[r.ID]; exists && current.ActiveRuns > 0 {
			previous := current
			current.ActiveRuns--
			s.runners[r.ID] = current
			if persistErr := s.persistLocked(); persistErr != nil {
				// Keep the conservative active-run count when the completion
				// update cannot be made durable; this prevents oversubscription
				// after a restart, and propagates the durability failure to the
				// caller instead of acknowledging the run unconditionally.
				s.runners[r.ID] = previous
				err = errors.Join(err, fmt.Errorf("persist runner completion: %w", persistErr))
			}
		}
		s.mu.Unlock()
	}()
	if request.Timeout <= 0 || request.Timeout > 30*time.Minute {
		request.Timeout = 15 * time.Minute
	}
	commandCtx, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, request.Command[0], request.Command[1:]...)
	cmd.Dir = workDir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + filepath.Join(workDir, ".home"), "LANG=C.UTF-8"}
	for key, value := range request.Env {
		if !validEnvKey(key) || strings.ContainsAny(value, "\x00\r\n") {
			return ExecuteResult{}, fmt.Errorf("invalid environment variable %q", key)
		}
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &limitedWriter{writer: &stdout, limit: 4 << 20}
	cmd.Stderr = &limitedWriter{writer: &stderr, limit: 4 << 20}
	started := time.Now()
	err = cmd.Run()
	result = ExecuteResult{RunnerID: r.ID, WorkDir: workDir, Stdout: stdout.String(), Stderr: stderr.String(), DurationMS: time.Since(started).Milliseconds(), ExitCode: 0}
	if err != nil {
		if commandCtx.Err() != nil {
			return result, commandCtx.Err()
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return result, err
		}
	}
	return result, nil
}

type limitedWriter struct {
	writer  io.Writer
	limit   int64
	written int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.written >= w.limit {
		return len(p), nil
	}
	allowed := int64(len(p))
	if remaining := w.limit - w.written; allowed > remaining {
		allowed = remaining
	}
	n, err := w.writer.Write(p[:allowed])
	w.written += int64(n)
	if allowed < int64(len(p)) {
		return len(p), nil
	}
	return n, err
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
