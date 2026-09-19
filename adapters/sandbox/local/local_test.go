package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/adro-project/adro/ports/sandbox"
	"github.com/adro-project/adro/ports/secretstore"
)

func TestSandboxHelperProcess(t *testing.T) {
	if os.Getenv("ADRO_SANDBOX_HELPER") != "1" {
		return
	}
	separator := -1
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		os.Exit(90)
	}
	args := os.Args[separator+1:]
	switch args[0] {
	case "output":
		_, _ = fmt.Fprint(os.Stdout, "stdout-value")
		_, _ = fmt.Fprint(os.Stderr, "stderr-value")
	case "environment":
		_, _ = fmt.Fprint(os.Stdout, os.Getenv("ADRO_TEST_VALUE"))
	case "sleep":
		duration, err := time.ParseDuration(args[1])
		if err != nil {
			os.Exit(91)
		}
		time.Sleep(duration)
	case "flood":
		chunks, err := strconv.Atoi(args[1])
		if err != nil {
			os.Exit(92)
		}
		chunk := bytes.Repeat([]byte("x"), 1024)
		for index := 0; index < chunks; index++ {
			if _, err := os.Stdout.Write(chunk); err != nil {
				os.Exit(0)
			}
		}
	case "write":
		if err := os.WriteFile(args[1], []byte(args[2]), 0o600); err != nil {
			_, _ = fmt.Fprint(os.Stderr, err)
			os.Exit(3)
		}
	case "spawn-child":
		child := exec.Command(os.Args[0], "-test.run=^TestSandboxHelperProcess$", "--", "sleep", "30s")
		child.Env = os.Environ()
		if err := child.Start(); err != nil {
			os.Exit(93)
		}
		if err := os.WriteFile(args[1], []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			os.Exit(94)
		}
		time.Sleep(30 * time.Second)
	case "dial-must-fail":
		connection, err := net.DialTimeout("tcp", args[1], time.Second)
		if err == nil {
			_ = connection.Close()
			os.Exit(95)
		}
		_, _ = fmt.Fprint(os.Stdout, "denied")
	case "exit":
		code, err := strconv.Atoi(args[1])
		if err != nil {
			os.Exit(96)
		}
		os.Exit(code)
	default:
		os.Exit(97)
	}
	os.Exit(0)
}

func TestPrepareRejectsUnsupportedCapabilitiesAndResources(t *testing.T) {
	broker := newTestSandbox(t)
	request := helperRequest(t, broker, "output")
	request.RequiredLevel = sandbox.EnforcementContainer
	if _, err := broker.Prepare(context.Background(), request); !errors.Is(err, sandbox.ErrUnsupportedEnforcement) {
		t.Fatalf("unsupported enforcement returned %v", err)
	}

	for name, mutate := range map[string]func(*sandbox.SandboxRequest){
		"CPU":            func(r *sandbox.SandboxRequest) { r.Limits.CPUTime = time.Second },
		"memory":         func(r *sandbox.SandboxRequest) { r.Limits.MemoryBytes = 1024 },
		"disk":           func(r *sandbox.SandboxRequest) { r.Limits.DiskBytes = 1024 },
		"secrets":        func(r *sandbox.SandboxRequest) { r.SecretRefs = []secretstore.SecretRef{"secret:credential"} },
		"output maximum": func(r *sandbox.SandboxRequest) { r.Limits.MaxOutputBytes = maxOutputLimit + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := helperRequest(t, broker, "output")
			mutate(&candidate)
			if _, err := broker.Prepare(context.Background(), candidate); err == nil {
				t.Fatal("unsupported resource request was accepted")
			}
		})
	}
}

func TestPrepareFreezesRequestAndValidatesHandleIdentity(t *testing.T) {
	broker := newTestSandbox(t)
	request := helperRequest(t, broker, "environment")
	request.Environment["ADRO_TEST_VALUE"] = "frozen"
	handle, err := broker.Prepare(context.Background(), request)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	request.Environment["ADRO_TEST_VALUE"] = "mutated"
	request.Command[len(request.Command)-1] = "output"

	broker.mu.Lock()
	preparedEnvironment := broker.handles[handle.ID].request.Environment["ADRO_TEST_VALUE"]
	broker.mu.Unlock()
	if preparedEnvironment != "frozen" {
		t.Fatalf("prepared environment = %q, want frozen", preparedEnvironment)
	}

	for name, forge := range map[string]func(*sandbox.SandboxHandle){
		"ID":      func(h *sandbox.SandboxHandle) { h.ID += "-forged" },
		"tenant":  func(h *sandbox.SandboxHandle) { h.TenantID = "other" },
		"session": func(h *sandbox.SandboxHandle) { h.SessionID = "other" },
		"effect":  func(h *sandbox.SandboxHandle) { h.EffectID = "other" },
		"backend": func(h *sandbox.SandboxHandle) { h.Backend = "other" },
		"digest":  func(h *sandbox.SandboxHandle) { h.RequestDigest = strings.Repeat("0", len(h.RequestDigest)) },
	} {
		t.Run(name, func(t *testing.T) {
			forged := handle
			forge(&forged)
			if _, err := broker.Execute(context.Background(), forged); !errors.Is(err, sandbox.ErrInvalidHandle) {
				t.Fatalf("forged handle returned %v", err)
			}
		})
	}
}

func TestPathValidationRejectsEscapeAndSymlinkAliases(t *testing.T) {
	broker := newTestSandbox(t)
	if _, err := broker.validatePath(filepath.Join("..", "outside"), false); !errors.Is(err, sandbox.ErrPathEscape) {
		t.Fatalf("path escape returned %v", err)
	}
	outside := t.TempDir()
	leaf := filepath.Join(broker.root, "leaf-link")
	if err := os.Symlink(filepath.Join(outside, "target"), leaf); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := broker.validatePath(leaf, false); !errors.Is(err, sandbox.ErrPathSymlink) {
		t.Fatalf("leaf symlink returned %v", err)
	}
	ancestor := filepath.Join(broker.root, "ancestor-link")
	if err := os.Symlink(outside, ancestor); err != nil {
		t.Fatalf("create ancestor symlink: %v", err)
	}
	if _, err := broker.validatePath(filepath.Join(ancestor, "child"), false); !errors.Is(err, sandbox.ErrPathSymlink) {
		t.Fatalf("ancestor symlink returned %v", err)
	}

	workingFile := filepath.Join(broker.root, "not-a-directory")
	if err := os.WriteFile(workingFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("write working file: %v", err)
	}
	request := helperRequest(t, broker, "output")
	request.WorkingDir = workingFile
	if _, err := broker.Prepare(context.Background(), request); !errors.Is(err, sandbox.ErrInvalidRequest) {
		t.Fatalf("file working directory returned %v", err)
	}
}

func TestExecuteStreamsOutputAndReusesConcurrentStream(t *testing.T) {
	broker := newTestSandbox(t)
	handle := prepareHelper(t, broker, helperRequest(t, broker, "output"))
	stream, err := broker.Execute(context.Background(), handle)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	repeated, err := broker.Execute(context.Background(), handle)
	if err != nil {
		t.Fatalf("repeated execute: %v", err)
	}
	if stream != repeated {
		t.Fatal("repeated execute returned a distinct stream")
	}
	result, err := stream.Wait(context.Background())
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if got, want := string(result.Stdout), "stdout-value"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := string(result.Stderr), "stderr-value"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if result.ExitCode != 0 || result.StartedAt.IsZero() || result.FinishedAt.Before(result.StartedAt) {
		t.Fatalf("invalid execution result: %+v", result)
	}
}

func TestPreparedCancellationPreventsExecution(t *testing.T) {
	broker := newTestSandbox(t)
	handle := prepareHelper(t, broker, helperRequest(t, broker, "output"))
	if err := broker.Cancel(context.Background(), handle); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := broker.Cancel(context.Background(), handle); err != nil {
		t.Fatalf("repeated cancel: %v", err)
	}
	if _, err := broker.Execute(context.Background(), handle); !errors.Is(err, sandbox.ErrExecutionCancelled) {
		t.Fatalf("execute after prepared cancellation returned %v", err)
	}
}

func TestExecutionTimeoutAndCancellation(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		broker := newTestSandbox(t)
		request := helperRequest(t, broker, "sleep", "30s")
		request.Limits.WallTimeout = 150 * time.Millisecond
		handle := prepareHelper(t, broker, request)
		stream, err := broker.Execute(context.Background(), handle)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		result, err := stream.Wait(context.Background())
		if !errors.Is(err, sandbox.ErrExecutionTimeout) || !result.TimedOut {
			t.Fatalf("timeout result=%+v err=%v", result, err)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		broker := newTestSandbox(t)
		handle := prepareHelper(t, broker, helperRequest(t, broker, "sleep", "30s"))
		stream, err := broker.Execute(context.Background(), handle)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if err := broker.Cancel(context.Background(), handle); err != nil {
			t.Fatalf("cancel: %v", err)
		}
		result, err := stream.Wait(context.Background())
		if !errors.Is(err, sandbox.ErrExecutionCancelled) || !result.Cancelled {
			t.Fatalf("cancel result=%+v err=%v", result, err)
		}
	})
}

func TestOutputLimitTerminatesProcess(t *testing.T) {
	broker := newTestSandbox(t)
	request := helperRequest(t, broker, "flood", "1024")
	request.Limits.MaxOutputBytes = 8 * 1024
	handle := prepareHelper(t, broker, request)
	stream, err := broker.Execute(context.Background(), handle)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	result, err := stream.Wait(context.Background())
	if !errors.Is(err, sandbox.ErrOutputLimit) || !result.OutputLimit {
		t.Fatalf("output limit result=%+v err=%v", result, err)
	}
	if got := len(result.Stdout) + len(result.Stderr); got != 8*1024 {
		t.Fatalf("captured %d bytes, want 8192", got)
	}
}

func TestUnconsumedEventStreamDoesNotDeadlock(t *testing.T) {
	broker := newTestSandbox(t)
	request := helperRequest(t, broker, "flood", "2048")
	request.Limits.MaxOutputBytes = 3 * 1024 * 1024
	handle := prepareHelper(t, broker, request)
	stream, err := broker.Execute(context.Background(), handle)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := stream.Wait(ctx)
	if err != nil {
		t.Fatalf("wait without event consumer: %v", err)
	}
	if result.DroppedEvents == 0 {
		t.Fatal("expected bounded event channel to report dropped events")
	}
}

func TestExecutionPreservesFrozenEnvironment(t *testing.T) {
	broker := newTestSandbox(t)
	request := helperRequest(t, broker, "environment")
	request.Environment["ADRO_TEST_VALUE"] = "expected"
	handle := prepareHelper(t, broker, request)
	request.Environment["ADRO_TEST_VALUE"] = "mutated"
	stream, err := broker.Execute(context.Background(), handle)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	result, err := stream.Wait(context.Background())
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if got := string(result.Stdout); got != "expected" {
		t.Fatalf("environment output = %q, want expected", got)
	}
}

func TestNonzeroExitIsReported(t *testing.T) {
	broker := newTestSandbox(t)
	handle := prepareHelper(t, broker, helperRequest(t, broker, "exit", "7"))
	stream, err := broker.Execute(context.Background(), handle)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	result, err := stream.Wait(context.Background())
	if err == nil || result.ExitCode != 7 {
		t.Fatalf("exit result=%+v err=%v", result, err)
	}
}

func TestExpiredAndDisposedHandlesFailClosed(t *testing.T) {
	broker := newTestSandbox(t)
	current := time.Now().UTC()
	broker.clock = func() time.Time { return current }
	handle := prepareHelper(t, broker, helperRequest(t, broker, "output"))
	current = handle.ExpiresAt
	if _, err := broker.Execute(context.Background(), handle); !errors.Is(err, sandbox.ErrHandleExpired) {
		t.Fatalf("expired handle returned %v", err)
	}

	broker = newTestSandbox(t)
	handle = prepareHelper(t, broker, helperRequest(t, broker, "output"))
	if err := broker.Dispose(context.Background(), handle); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	if _, err := broker.Execute(context.Background(), handle); !errors.Is(err, sandbox.ErrInvalidHandle) {
		t.Fatalf("disposed handle returned %v", err)
	}
}

func newTestSandbox(t *testing.T) *Broker {
	t.Helper()
	broker, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new sandbox: %v", err)
	}
	if err := broker.cap.Validate(); err != nil {
		t.Fatalf("detected capabilities invalid: %v", err)
	}
	return broker
}

func helperRequest(t *testing.T, broker *Broker, args ...string) sandbox.SandboxRequest {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("test executable: %v", err)
	}
	command := []string{executable, "-test.run=^TestSandboxHelperProcess$", "--"}
	command = append(command, args...)
	return sandbox.SandboxRequest{
		TenantID: "tenant", SessionID: "session", EffectID: "effect",
		RequiredLevel: sandbox.EnforcementProcess,
		Command:       command, WorkingDir: broker.root,
		Environment: map[string]string{"ADRO_SANDBOX_HELPER": "1"},
		Limits:      sandbox.ResourceBudget{WallTimeout: 5 * time.Second, MaxOutputBytes: 4 << 20},
	}
}

func prepareHelper(t *testing.T, broker *Broker, request sandbox.SandboxRequest) sandbox.SandboxHandle {
	t.Helper()
	handle, err := broker.Prepare(context.Background(), request)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	return handle
}
