// Package local provides a developer-only SandboxBroker. It uses host process
// controls and, where available, an operating-system policy backend. It never
// advertises secure tenant isolation and refuses requirements it cannot enforce.
package local

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	coreencoding "github.com/adro-project/adro/core/encoding"
	"github.com/adro-project/adro/ports/sandbox"
	"github.com/adro-project/adro/ports/secretstore"
)

const (
	defaultTimeout     = 5 * time.Minute
	defaultOutputLimit = 4 << 20
	maxOutputLimit     = 64 << 20
)

type preparedState uint8

const (
	statePrepared preparedState = iota
	stateStarting
	stateRunning
	stateFinished
	stateCancelled
)

type Broker struct {
	mu      sync.Mutex
	root    string
	cap     sandbox.SandboxCapabilities
	handles map[string]*prepared
	clock   func() time.Time
	newID   func() (string, error)
}

type prepared struct {
	handle   sandbox.SandboxHandle
	request  sandbox.SandboxRequest
	command  []string
	profile  string
	state    preparedState
	stream   *executionStream
	cancel   context.CancelFunc
	cmd      *exec.Cmd
	disposed bool
}

// New detects only capabilities available on this host. root must be an
// existing directory because all path grants are checked against it.
func New(root string) (*Broker, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("%w: workspace root is required", sandbox.ErrInvalidRequest)
	}
	canonical, err := canonicalRoot(root)
	if err != nil {
		return nil, err
	}
	return &Broker{
		root: canonical, cap: detectCapabilities(), handles: make(map[string]*prepared),
		clock: func() time.Time { return time.Now().UTC() }, newID: newID,
	}, nil
}

// NewWithCapabilities is intended for deterministic tests. Production code
// should use New so capabilities describe the real host.
func NewWithCapabilities(root string, capabilities sandbox.SandboxCapabilities) (*Broker, error) {
	broker, err := New(root)
	if err != nil {
		return nil, err
	}
	if err := capabilities.Validate(); err != nil {
		return nil, err
	}
	if err := validateCapabilityReduction(broker.cap, capabilities); err != nil {
		return nil, err
	}
	broker.cap = cloneCapabilities(capabilities)
	return broker, nil
}

func (b *Broker) Capabilities(ctx context.Context) (sandbox.SandboxCapabilities, error) {
	if err := contextErr(ctx); err != nil {
		return sandbox.SandboxCapabilities{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return cloneCapabilities(b.cap), nil
}

func (b *Broker) Prepare(ctx context.Context, request sandbox.SandboxRequest) (sandbox.SandboxHandle, error) {
	if err := contextErr(ctx); err != nil {
		return sandbox.SandboxHandle{}, err
	}
	now := b.clock().UTC()
	if err := request.Validate(now); err != nil {
		return sandbox.SandboxHandle{}, err
	}
	capabilities, err := b.Capabilities(ctx)
	if err != nil {
		return sandbox.SandboxHandle{}, err
	}
	if !capabilities.Supports(request.RequiredLevel) {
		return sandbox.SandboxHandle{}, fmt.Errorf("%w: required=%s backend=%s level=%s", sandbox.ErrUnsupportedEnforcement, request.RequiredLevel, capabilities.Backend, capabilities.Level)
	}
	if !capabilities.WallTimeoutEnforcement || !capabilities.OutputLimitEnforcement {
		return sandbox.SandboxHandle{}, fmt.Errorf("%w: backend cannot enforce required wall/output limits", sandbox.ErrUnsupportedEnforcement)
	}
	if len(request.NetworkGrants) > 0 && !capabilities.NetworkEnforcement {
		return sandbox.SandboxHandle{}, fmt.Errorf("%w: backend cannot enforce network grants", sandbox.ErrNetworkDenied)
	}
	if len(request.FileGrants) > 0 && !capabilities.FilesystemEnforcement {
		return sandbox.SandboxHandle{}, fmt.Errorf("%w: backend cannot enforce file grants", sandbox.ErrPermissionDenied)
	}
	if len(request.SecretRefs) > 0 && !capabilities.SecretInjection {
		return sandbox.SandboxHandle{}, fmt.Errorf("%w: backend has no secret injection channel", sandbox.ErrPermissionDenied)
	}
	if request.Limits.CPUTime > 0 && !capabilities.CPUEnforcement {
		return sandbox.SandboxHandle{}, fmt.Errorf("%w: CPU limit is unsupported", sandbox.ErrUnsupportedEnforcement)
	}
	if request.Limits.MemoryBytes > 0 && !capabilities.MemoryEnforcement {
		return sandbox.SandboxHandle{}, fmt.Errorf("%w: memory limit is unsupported", sandbox.ErrUnsupportedEnforcement)
	}
	if request.Limits.DiskBytes > 0 && !capabilities.DiskEnforcement {
		return sandbox.SandboxHandle{}, fmt.Errorf("%w: disk limit is unsupported", sandbox.ErrUnsupportedEnforcement)
	}
	if request.Limits.MaxOutputBytes > capabilities.MaxOutputBytes {
		return sandbox.SandboxHandle{}, fmt.Errorf("%w: output limit exceeds backend maximum", sandbox.ErrInvalidRequest)
	}

	workingDir, err := b.validatePath(request.WorkingDir, true)
	if err != nil {
		return sandbox.SandboxHandle{}, err
	}
	workingInfo, err := os.Stat(workingDir)
	if err != nil || !workingInfo.IsDir() {
		return sandbox.SandboxHandle{}, fmt.Errorf("%w: working directory is not a directory", sandbox.ErrInvalidRequest)
	}
	request.WorkingDir = workingDir
	for i := range request.FileGrants {
		path, pathErr := b.validatePath(request.FileGrants[i].Path, !request.FileGrants[i].Create)
		if pathErr != nil {
			return sandbox.SandboxHandle{}, pathErr
		}
		request.FileGrants[i].Path = path
	}
	command, err := resolveCommand(request.Command, workingDir)
	if err != nil {
		return sandbox.SandboxHandle{}, err
	}
	request.Command = append([]string(nil), command...)

	profile, err := b.buildProfile(request, capabilities)
	if err != nil {
		return sandbox.SandboxHandle{}, err
	}
	digest, err := coreencoding.Digest(request)
	if err != nil {
		return sandbox.SandboxHandle{}, fmt.Errorf("digest sandbox request: %w", err)
	}
	id, err := b.newID()
	if err != nil {
		return sandbox.SandboxHandle{}, err
	}
	ttl := request.Limits.WallTimeout
	if ttl <= 0 {
		ttl = defaultTimeout
	}
	if ttl > 24*time.Hour {
		return sandbox.SandboxHandle{}, fmt.Errorf("%w: wall timeout exceeds 24h", sandbox.ErrInvalidRequest)
	}
	handle := sandbox.SandboxHandle{
		ID: id, TenantID: request.TenantID, SessionID: request.SessionID, EffectID: request.EffectID,
		Backend: capabilities.Backend, Enforcement: capabilities.Level, RequestDigest: digest,
		ExpiresAt: now.Add(ttl),
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handles[id] = &prepared{
		handle: handle, request: cloneRequest(request), command: append([]string(nil), command...),
		profile: profile, state: statePrepared,
	}
	return handle, nil
}

func (b *Broker) Execute(ctx context.Context, handle sandbox.SandboxHandle) (sandbox.ExecutionStream, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	b.mu.Lock()
	item, err := b.lookupLocked(handle)
	if err != nil {
		b.mu.Unlock()
		return nil, err
	}
	if item.disposed {
		b.mu.Unlock()
		return nil, sandbox.ErrInvalidHandle
	}
	if item.state == stateCancelled {
		b.mu.Unlock()
		return nil, sandbox.ErrExecutionCancelled
	}
	if !b.clock().Before(item.handle.ExpiresAt) {
		b.mu.Unlock()
		return nil, sandbox.ErrHandleExpired
	}
	if item.state != statePrepared {
		stream := item.stream
		b.mu.Unlock()
		if stream == nil {
			return nil, sandbox.ErrBackendUnavailable
		}
		return stream, nil
	}

	runCtx, cancel := context.WithDeadline(ctx, item.handle.ExpiresAt)
	stream := newExecutionStream(item.request.Limits.MaxOutputBytes, b.clock)
	stream.setCancel(cancel)
	item.state = stateStarting
	item.stream = stream
	item.cancel = cancel
	command := append([]string(nil), item.command...)
	workingDir := item.request.WorkingDir
	environment := restrictedEnvironment(item.request.Environment)
	profile := item.profile
	b.mu.Unlock()

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = workingDir
	cmd.Env = environment
	configureCommand(cmd)
	if profile != "" {
		cmd = wrapCommand(cmd, profile, command)
		configureCommand(cmd)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return b.failStart(handle, stream, cancel, fmt.Errorf("sandbox stdout pipe: %w", err))
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return b.failStart(handle, stream, cancel, fmt.Errorf("sandbox stderr pipe: %w", err))
	}

	b.mu.Lock()
	item, lookupErr := b.lookupLocked(handle)
	if lookupErr != nil || item.disposed {
		b.mu.Unlock()
		cancel()
		stream.finish(sandbox.ExecutionResult{}, sandbox.ErrExecutionCancelled)
		return stream, sandbox.ErrExecutionCancelled
	}
	item.cmd = cmd
	b.mu.Unlock()
	if err := contextErr(runCtx); err != nil {
		return b.failStart(handle, stream, cancel, sandbox.ErrExecutionCancelled)
	}
	if err := cmd.Start(); err != nil {
		return b.failStart(handle, stream, cancel, fmt.Errorf("start sandbox command: %w", err))
	}
	stream.markStarted(b.clock().UTC())

	b.mu.Lock()
	if current, ok := b.handles[handle.ID]; ok && current == item {
		item.state = stateRunning
	}
	b.mu.Unlock()
	go b.watchProcess(runCtx, handle.ID, item, stdout, stderr, stream)
	return stream, nil
}

func (b *Broker) failStart(handle sandbox.SandboxHandle, stream *executionStream, cancel context.CancelFunc, err error) (sandbox.ExecutionStream, error) {
	cancel()
	b.mu.Lock()
	if item, ok := b.handles[handle.ID]; ok {
		item.state = stateFinished
	}
	b.mu.Unlock()
	stream.finish(sandbox.ExecutionResult{ExitCode: -1}, err)
	return stream, err
}

func (b *Broker) watchProcess(ctx context.Context, handleID string, item *prepared, stdout, stderr io.ReadCloser, stream *executionStream) {
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		stream.read("stdout", stdout)
	}()
	go func() {
		defer readers.Done()
		stream.read("stderr", stderr)
	}()

	waitResult := make(chan error, 1)
	go func() { waitResult <- item.cmd.Wait() }()

	var waitErr error
	var timedOut, cancelled bool
	select {
	case waitErr = <-waitResult:
	case <-stream.limitExceeded():
		_ = cancelCommand(item.cmd)
		waitErr = <-waitResult
	case <-ctx.Done():
		timedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
		cancelled = !timedOut
		_ = cancelCommand(item.cmd)
		waitErr = <-waitResult
	}
	readers.Wait()

	result := resultFrom(item.cmd, timedOut, cancelled)
	var resultErr error
	switch {
	case stream.exceededLimit():
		resultErr = sandbox.ErrOutputLimit
	case timedOut:
		resultErr = sandbox.ErrExecutionTimeout
	case cancelled:
		resultErr = sandbox.ErrExecutionCancelled
	case waitErr != nil:
		resultErr = waitErr
	}
	stream.finish(result, resultErr)
	item.cancel()

	b.mu.Lock()
	if current, ok := b.handles[handleID]; ok && current == item {
		item.state = stateFinished
	}
	b.mu.Unlock()
}

func (b *Broker) Cancel(ctx context.Context, handle sandbox.SandboxHandle) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	item, err := b.lookupLocked(handle)
	if err != nil || item.disposed {
		b.mu.Unlock()
		if err != nil {
			return err
		}
		return sandbox.ErrInvalidHandle
	}
	if item.state == statePrepared {
		item.state = stateCancelled
	}
	cancel, cmd := item.cancel, item.cmd
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if cmd != nil {
		return cancelCommand(cmd)
	}
	return nil
}

func (b *Broker) Dispose(ctx context.Context, handle sandbox.SandboxHandle) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	item, err := b.lookupLocked(handle)
	if err != nil || item.disposed {
		b.mu.Unlock()
		if err != nil {
			return err
		}
		return sandbox.ErrInvalidHandle
	}
	item.disposed = true
	cancel, cmd := item.cancel, item.cmd
	delete(b.handles, handle.ID)
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if cmd != nil {
		return cancelCommand(cmd)
	}
	return nil
}

func (b *Broker) lookupLocked(handle sandbox.SandboxHandle) (*prepared, error) {
	item, ok := b.handles[handle.ID]
	if !ok || handle.ID == "" || item.handle != handle {
		return nil, sandbox.ErrInvalidHandle
	}
	return item, nil
}

func (b *Broker) validatePath(path string, requireExists bool) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("%w: empty path", sandbox.ErrInvalidRequest)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(b.root, path)
	}
	clean := filepath.Clean(path)
	if !withinRoot(b.root, clean) {
		return "", sandbox.ErrPathEscape
	}
	relative, err := filepath.Rel(b.root, clean)
	if err != nil {
		return "", fmt.Errorf("%w: resolve path: %v", sandbox.ErrInvalidRequest, err)
	}
	current := b.root
	parts := strings.Split(relative, string(os.PathSeparator))
	for index, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return "", sandbox.ErrPathSymlink
			}
			continue
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return "", fmt.Errorf("%w: inspect path: %v", sandbox.ErrInvalidRequest, statErr)
		}
		if requireExists || index < len(parts)-1 {
			return "", fmt.Errorf("%w: path does not exist", sandbox.ErrInvalidRequest)
		}
	}
	if requireExists {
		if _, err := os.Stat(clean); err != nil {
			return "", fmt.Errorf("%w: path does not exist", sandbox.ErrInvalidRequest)
		}
	}
	return clean, nil
}

func (b *Broker) buildProfile(request sandbox.SandboxRequest, capabilities sandbox.SandboxCapabilities) (string, error) {
	if capabilities.Backend != "macos-seatbelt" {
		return "", nil
	}
	if len(request.NetworkGrants) > 0 {
		return "", fmt.Errorf("%w: local Seatbelt backend cannot faithfully bind domain/IP/CIDR grants; use a controlled proxy or production backend", sandbox.ErrUnsupportedEnforcement)
	}
	var profile strings.Builder
	profile.WriteString("(version 1)\n(deny default)\n(import \"system.sb\")\n")
	profile.WriteString("(allow process-fork process-info* signal)\n")
	writeSeatbeltRule(&profile, "allow process-exec", request.Command[0], false)
	writeSeatbeltRule(&profile, "allow file-read* file-map-executable", request.Command[0], false)
	profile.WriteString("(allow file-read-metadata file-test-existence)\n")
	for _, systemPath := range []string{"/bin", "/usr/bin", "/usr/lib", "/usr/share", "/System", "/Library/Apple"} {
		writeSeatbeltRule(&profile, "allow file-read* file-map-executable", systemPath, true)
	}
	for _, grant := range request.FileGrants {
		info, err := os.Stat(grant.Path)
		isDirectory := err == nil && info.IsDir()
		if grant.Read {
			writeSeatbeltRule(&profile, "allow file-read-data file-read-metadata file-test-existence", grant.Path, isDirectory)
		}
		if grant.Execute {
			writeSeatbeltRule(&profile, "allow process-exec", grant.Path, isDirectory)
			writeSeatbeltRule(&profile, "allow file-read* file-map-executable", grant.Path, isDirectory)
		}
		if grant.Write {
			writeSeatbeltRule(&profile, "allow file-write-data file-write-xattr file-write-mode", grant.Path, isDirectory)
		}
		if grant.Create {
			writeSeatbeltRule(&profile, "allow file-write-create", grant.Path, isDirectory)
		}
		if grant.Delete {
			writeSeatbeltRule(&profile, "allow file-write-unlink", grant.Path, isDirectory)
		}
	}
	return profile.String(), nil
}

func writeSeatbeltRule(profile *strings.Builder, operation, path string, subpath bool) {
	filter := "literal"
	if subpath {
		filter = "subpath"
	}
	fmt.Fprintf(profile, "(%s (%s \"%s\"))\n", operation, filter, seatbeltQuote(path))
}

func detectCapabilities() sandbox.SandboxCapabilities {
	capabilities := sandbox.SandboxCapabilities{
		Backend: "local-process", Level: sandbox.EnforcementProcess,
		SupportedLevels:       []sandbox.EnforcementLevel{sandbox.EnforcementNone, sandbox.EnforcementProcess},
		SecureTenantIsolation: false, ProcessTreeCancellation: supportsProcessTreeCancellation(),
		WallTimeoutEnforcement: true, OutputLimitEnforcement: true, MaxOutputBytes: maxOutputLimit,
		Limitations: []string{
			"reference-only", "not secure multi-tenant isolation", "filesystem and network are host-accessible",
			"CPU, memory, disk, and secret injection limits are unavailable", "no production container or remote worker",
		},
	}
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("sandbox-exec"); err == nil {
			capabilities.Backend = "macos-seatbelt"
			capabilities.Level = sandbox.EnforcementNetwork
			capabilities.SupportedLevels = []sandbox.EnforcementLevel{
				sandbox.EnforcementNone, sandbox.EnforcementProcess, sandbox.EnforcementFilesystem, sandbox.EnforcementNetwork,
			}
			capabilities.FilesystemEnforcement = true
			capabilities.NetworkEnforcement = true
			capabilities.Limitations = []string{
				"reference-only", "macOS Seatbelt is not secure multi-tenant isolation",
				"outbound network grants require a controlled proxy or production backend",
				"CPU, memory, disk, and secret injection limits are unavailable", "no container, microVM, or remote worker",
			}
		}
	}
	return capabilities
}

func validateCapabilityReduction(actual, requested sandbox.SandboxCapabilities) error {
	if requested.Backend != actual.Backend || !sandbox.AtLeast(actual.Level, requested.Level) {
		return fmt.Errorf("%w: test capabilities cannot upgrade or replace the detected backend", sandbox.ErrInvalidRequest)
	}
	actualLevels := make(map[sandbox.EnforcementLevel]struct{}, len(actual.SupportedLevels))
	for _, level := range actual.SupportedLevels {
		actualLevels[level] = struct{}{}
	}
	for _, level := range requested.SupportedLevels {
		if _, ok := actualLevels[level]; !ok {
			return fmt.Errorf("%w: unsupported test enforcement level %s", sandbox.ErrInvalidRequest, level)
		}
	}
	if (requested.SecureTenantIsolation && !actual.SecureTenantIsolation) ||
		(requested.NetworkEnforcement && !actual.NetworkEnforcement) ||
		(requested.FilesystemEnforcement && !actual.FilesystemEnforcement) ||
		(requested.ProcessTreeCancellation && !actual.ProcessTreeCancellation) ||
		(requested.WallTimeoutEnforcement && !actual.WallTimeoutEnforcement) ||
		(requested.OutputLimitEnforcement && !actual.OutputLimitEnforcement) ||
		(requested.CPUEnforcement && !actual.CPUEnforcement) ||
		(requested.MemoryEnforcement && !actual.MemoryEnforcement) ||
		(requested.DiskEnforcement && !actual.DiskEnforcement) ||
		(requested.SecretInjection && !actual.SecretInjection) ||
		requested.MaxOutputBytes > actual.MaxOutputBytes {
		return fmt.Errorf("%w: test capabilities cannot advertise unavailable enforcement", sandbox.ErrInvalidRequest)
	}
	return nil
}

func resolveCommand(argv []string, workingDir string) ([]string, error) {
	command := strings.TrimSpace(argv[0])
	var path string
	var err error
	if strings.ContainsRune(command, os.PathSeparator) {
		if !filepath.IsAbs(command) {
			command = filepath.Join(workingDir, command)
		}
		path, err = filepath.Abs(command)
	} else {
		path, err = exec.LookPath(command)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: command executable is unavailable: %v", sandbox.ErrBackendUnavailable, err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve command executable: %v", sandbox.ErrBackendUnavailable, err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("%w: command executable is not an executable regular file", sandbox.ErrBackendUnavailable)
	}
	result := append([]string(nil), argv...)
	result[0] = filepath.Clean(path)
	return result, nil
}

func canonicalRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("%w: %v", sandbox.ErrInvalidRequest, err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: workspace root must be a directory", sandbox.ErrInvalidRequest)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("%w: resolve workspace root: %v", sandbox.ErrInvalidRequest, err)
	}
	return filepath.Clean(real), nil
}

func withinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && !filepath.IsAbs(relative)
}

func restrictedEnvironment(environment map[string]string) []string {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+environment[key])
	}
	return result
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func newID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("sandbox handle id: %w", err)
	}
	return "sandbox:" + hex.EncodeToString(raw[:]), nil
}

func cloneCapabilities(capabilities sandbox.SandboxCapabilities) sandbox.SandboxCapabilities {
	capabilities.SupportedLevels = append([]sandbox.EnforcementLevel(nil), capabilities.SupportedLevels...)
	capabilities.Limitations = append([]string(nil), capabilities.Limitations...)
	return capabilities
}

func cloneRequest(request sandbox.SandboxRequest) sandbox.SandboxRequest {
	request.Command = append([]string(nil), request.Command...)
	request.FileGrants = append([]sandbox.FileGrant(nil), request.FileGrants...)
	request.NetworkGrants = append([]sandbox.NetworkGrant(nil), request.NetworkGrants...)
	for index := range request.NetworkGrants {
		request.NetworkGrants[index].Ports = append([]int(nil), request.NetworkGrants[index].Ports...)
	}
	request.SecretRefs = append([]secretstore.SecretRef(nil), request.SecretRefs...)
	if request.Environment != nil {
		environment := request.Environment
		request.Environment = make(map[string]string, len(environment))
		for key, value := range environment {
			request.Environment[key] = value
		}
	}
	return request
}

func seatbeltQuote(value string) string {
	return strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "", "\r", "").Replace(value)
}

func resultFrom(cmd *exec.Cmd, timedOut, cancelled bool) sandbox.ExecutionResult {
	result := sandbox.ExecutionResult{ExitCode: -1, TimedOut: timedOut, Cancelled: cancelled}
	if cmd != nil && cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	return result
}

var _ sandbox.SandboxBroker = (*Broker)(nil)
