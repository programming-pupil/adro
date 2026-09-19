package extensions

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adro-project/adro/adapters/sandbox/local"
	extensionport "github.com/adro-project/adro/ports/extensions"
	"github.com/adro-project/adro/ports/sandbox"
)

func TestExtensionHelperProcess(t *testing.T) {
	if os.Getenv("ADRO_EXTENSION_HELPER") != "1" {
		return
	}
	// Keep the Go test runner summary off the protocol stream. The helper
	// writes framed JSON to the saved stdout file while the runner uses the
	// reassigned stdout for PASS/FAIL text.
	protocolOut := os.Stdout
	os.Stdout = os.Stderr
	mode := "normal"
	for index, arg := range os.Args {
		if arg == "--extension-mode" && index+1 < len(os.Args) {
			mode = os.Args[index+1]
		}
	}
	if mode == "flood" {
		_, _ = io.WriteString(protocolOut, strings.Repeat("x", 1<<20)+"\n")
		os.Exit(0)
	}
	decoder := json.NewDecoder(bufio.NewReader(os.Stdin))
	encoder := json.NewEncoder(protocolOut)
	for {
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      string          `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := decoder.Decode(&request); err != nil {
			os.Exit(0)
		}
		switch request.Method {
		case "adro.handshake":
			if mode == "silent" {
				continue
			}
			var expected Handshake
			if err := json.Unmarshal(request.Params, &expected); err != nil {
				os.Exit(0)
			}
			if mode == "mismatch" {
				expected.AdapterVersion = "wrong"
			}
			if mode == "overclaim" {
				expected.Capabilities = append(expected.Capabilities, "undeclared")
			}
			if mode == "small-bound" {
				expected.MaxMessageBytes = 1024
			}
			responseID := request.ID
			if mode == "wrong-id" {
				responseID = "forged"
			}
			_ = encoder.Encode(rpcResponse{JSONRPC: "2.0", ID: responseID, Result: mustJSON(expected)})
			if mode == "crash" {
				os.Exit(42)
			}
		case "adro.health":
			_ = encoder.Encode(rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: mustJSON(map[string]any{"healthy": true})})
		case "echo":
			_ = encoder.Encode(rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: request.Params})
		default:
			_ = encoder.Encode(rpcResponse{JSONRPC: "2.0", ID: request.ID, Error: &rpcError{Code: -32601, Message: "method not found"}})
		}
	}
}

// Threat ID: TM-EXT-002
func TestSupervisorHandshakeCallHealthAndStop(t *testing.T) {
	broker, request := testSupervisorRequest(t, "normal")
	supervisor, err := NewSupervisor(Config{
		Broker: broker,
		Authorizer: extensionport.AuthorizeFunc(func(_ context.Context, item extensionport.Installation) (extensionport.Installation, error) {
			if item.State != "active" {
				return extensionport.Installation{}, ErrUnauthorized
			}
			return item, nil
		}),
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := supervisor.Start(context.Background(), request)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if got := instance.State(); got != StateRunning {
		t.Fatalf("state=%s, want running", got)
	}
	result, err := instance.Call(context.Background(), "echo", map[string]string{"value": "bounded"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := string(result); got != `{"value":"bounded"}` {
		t.Fatalf("result=%s", got)
	}
	if err := instance.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
	if err := instance.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := instance.Wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if got := instance.State(); got != StateStopped {
		t.Fatalf("state=%s, want stopped", got)
	}
}

// Threat IDs: TM-EXT-003, TM-SUPPLY-003
func TestSupervisorRejectsHandshakeMismatchAndPermissionOverclaim(t *testing.T) {
	for _, mode := range []string{"mismatch", "overclaim"} {
		t.Run(mode, func(t *testing.T) {
			broker, request := testSupervisorRequest(t, mode)
			supervisor, err := NewSupervisor(Config{
				Broker: broker, MaxRestarts: 1,
				Authorizer: extensionport.AuthorizeFunc(func(_ context.Context, item extensionport.Installation) (extensionport.Installation, error) {
					return item, nil
				}),
				Sleep: func(context.Context, time.Duration) error { return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = supervisor.Start(context.Background(), request)
			if !errors.Is(err, ErrRestartLimit) && !errors.Is(err, ErrProtocolMismatch) {
				t.Fatalf("start error=%v", err)
			}
		})
	}
}

// Threat ID: TM-EXT-004
func TestSupervisorCrashRestartQuarantinesAfterBoundedAttempts(t *testing.T) {
	broker, request := testSupervisorRequest(t, "crash")
	var mu sync.Mutex
	var audits []AuditEvent
	quarantined := make(chan string, 1)
	supervisor, err := NewSupervisor(Config{
		Broker: broker, MaxRestarts: 2,
		Authorizer: extensionport.AuthorizeFunc(func(_ context.Context, item extensionport.Installation) (extensionport.Installation, error) {
			return item, nil
		}),
		Sleep: func(context.Context, time.Duration) error { return nil },
		Audit: func(event AuditEvent) { mu.Lock(); audits = append(audits, event); mu.Unlock() },
		Quarantiner: extensionport.QuarantineFunc(func(_ context.Context, _ extensionport.Installation, reason string) error {
			quarantined <- reason
			return nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := supervisor.Start(context.Background(), request)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := instance.Wait(waitCtx); !errors.Is(err, ErrQuarantined) && !errors.Is(err, ErrRestartLimit) {
		t.Fatalf("wait error=%v state=%s", err, instance.State())
	}
	if got := instance.State(); got != StateQuarantined {
		t.Fatalf("state=%s, want quarantined", got)
	}
	select {
	case reason := <-quarantined:
		if reason == "" {
			t.Fatal("quarantine reason is empty")
		}
	case <-waitCtx.Done():
		t.Fatal("quarantine callback was not called")
	}
	if instance.RestartCount() > 2 {
		t.Fatalf("restart count=%d exceeds cap", instance.RestartCount())
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, event := range audits {
		if event.Action == "quarantined" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("audit events=%+v", audits)
	}
}

// Threat IDs: TM-EXT-006, TM-SECRET-003
func TestSupervisorRejectsEnvironmentInjectionAndSecretWithoutLease(t *testing.T) {
	broker, request := testSupervisorRequest(t, "normal")
	request.Environment = map[string]string{"LD_PRELOAD": "evil"}
	supervisor, err := NewSupervisor(Config{Broker: broker, Authorizer: extensionport.AuthorizeFunc(func(_ context.Context, item extensionport.Installation) (extensionport.Installation, error) {
		return item, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.Start(context.Background(), request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("environment error=%v", err)
	}
	request.Environment = nil
	request.Installation.Manifest.SecretPermissions = []extensionport.SecretPermission{{Name: "token", Destination: "ext", Purpose: "auth"}}
	if _, err := supervisor.Start(context.Background(), request); !errors.Is(err, ErrSecretsUnavailable) {
		t.Fatalf("secret error=%v", err)
	}
}

// Threat ID: TM-EXT-005
func TestSupervisorBoundsProtocolFlood(t *testing.T) {
	broker, request := testSupervisorRequest(t, "flood")
	request.Installation.Manifest.MaxMessageBytes = 1024
	supervisor, err := NewSupervisor(Config{
		Broker: broker, MaxRestarts: 0,
		Authorizer: extensionport.AuthorizeFunc(func(_ context.Context, item extensionport.Installation) (extensionport.Installation, error) {
			return item, nil
		}),
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = supervisor.Start(context.Background(), request)
	if !errors.Is(err, ErrRestartLimit) && !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("flood error=%v", err)
	}
}

// Threat ID: TM-EXT-005
func TestSupervisorEnforcesNegotiatedFrameSize(t *testing.T) {
	broker, request := testSupervisorRequest(t, "small-bound")
	supervisor, err := NewSupervisor(Config{Broker: broker, Authorizer: allowAuthorizer()})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := supervisor.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = instance.Stop(context.Background()) }()
	if _, err := instance.Call(context.Background(), "echo", map[string]any{"value": "before"}); err != nil {
		t.Fatalf("small call before rejection: %v state=%s last=%v", err, instance.State(), instance.LastError())
	}
	if _, err := instance.Call(context.Background(), "echo", map[string]any{"value": strings.Repeat("a", 1100)}); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("call above negotiated frame limit returned %v", err)
	}
	if _, err := instance.Call(context.Background(), "echo", map[string]any{"value": "ok"}); err != nil {
		t.Fatalf("small call after rejection: %v state=%s last=%v", err, instance.State(), instance.LastError())
	}
}

// Threat ID: TM-EXT-001
func TestSupervisorInProcessRequiresExplicitFactoryAndHandshake(t *testing.T) {
	manifest := testManifest()
	manifest.ExecutionMode = extensionport.ExecutionInProcess
	request := extensionport.Installation{Manifest: manifest, State: "active", TenantID: "tenant", WorkspaceID: "workspace", Digest: "digest", Signature: "sig", KeyID: "key"}
	factoryCalled := false
	supervisor, err := NewSupervisor(Config{
		InProcessFactory: func(context.Context, extensionport.Installation) (InProcessAdapter, error) {
			factoryCalled = true
			return fakeAdapter{}, nil
		},
		Authorizer: extensionport.AuthorizeFunc(func(_ context.Context, item extensionport.Installation) (extensionport.Installation, error) {
			return item, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := supervisor.Start(context.Background(), StartRequest{Installation: request})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !factoryCalled || instance.State() != StateRunning {
		t.Fatalf("factoryCalled=%v state=%s", factoryCalled, instance.State())
	}
	result, err := instance.Call(context.Background(), "echo", json.RawMessage(`{"ok":true}`))
	if err != nil || string(result) != `{"ok":true}` {
		t.Fatalf("call result=%s err=%v", result, err)
	}
	if err := instance.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Threat IDs: TM-EXT-003, TM-EXT-007
func TestSupervisorRejectsChangedAuthorizationIdentityUndeclaredCallsAndWASI(t *testing.T) {
	broker, request := testSupervisorRequest(t, "normal")
	changed, err := NewSupervisor(Config{
		Broker: broker,
		Authorizer: extensionport.AuthorizeFunc(func(_ context.Context, item extensionport.Installation) (extensionport.Installation, error) {
			item.WorkspaceID = "other-workspace"
			return item, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := changed.Start(context.Background(), request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("changed authorization identity returned %v", err)
	}

	allowed, err := NewSupervisor(Config{Broker: broker, Authorizer: allowAuthorizer()})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := allowed.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := instance.Call(context.Background(), "host.admin", map[string]any{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("undeclared method returned %v", err)
	}
	if _, err := instance.Call(context.Background(), "adro.handshake", map[string]any{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("repeated handshake returned %v", err)
	}
	if err := instance.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	request.Installation.Manifest.ExecutionMode = extensionport.ExecutionWASI
	if _, err := allowed.Start(context.Background(), request); !errors.Is(err, ErrUnsupportedExecution) {
		t.Fatalf("WASI without dedicated runtime returned %v", err)
	}
}

// Threat IDs: TM-EXT-004, TM-EXT-005
func TestSupervisorBoundsSilentAndForgedProtocolResponses(t *testing.T) {
	for _, mode := range []string{"silent", "wrong-id"} {
		t.Run(mode, func(t *testing.T) {
			broker, request := testSupervisorRequest(t, mode)
			supervisor, err := NewSupervisor(Config{
				Broker: broker, Authorizer: allowAuthorizer(), MaxRestarts: 1,
				CallTimeout: 25 * time.Millisecond,
				Sleep:       func(context.Context, time.Duration) error { return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = supervisor.Start(context.Background(), request)
			if !errors.Is(err, ErrRestartLimit) {
				t.Fatalf("mode %s returned %v", mode, err)
			}
		})
	}
}

// Threat ID: TM-EXT-008
func TestSupervisorContainsInProcessPanicAndInvalidResponse(t *testing.T) {
	manifest := testManifest()
	manifest.ExecutionMode = extensionport.ExecutionInProcess
	manifest.MaxMessageBytes = 1024
	request := StartRequest{Installation: extensionport.Installation{
		Manifest: manifest, State: "active", TenantID: "tenant", WorkspaceID: "workspace",
		Digest: "digest", Signature: "signature", KeyID: "key",
	}}
	for name, adapter := range map[string]InProcessAdapter{
		"panic":    hostileAdapter{callPanic: true},
		"oversize": hostileAdapter{result: json.RawMessage(`"` + strings.Repeat("x", 2048) + `"`)},
		"invalid":  hostileAdapter{result: json.RawMessage(`not-json`)},
	} {
		t.Run(name, func(t *testing.T) {
			supervisor, err := NewSupervisor(Config{
				Authorizer: allowAuthorizer(),
				InProcessFactory: func(context.Context, extensionport.Installation) (InProcessAdapter, error) {
					return adapter, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			instance, err := supervisor.Start(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			_, err = instance.Call(context.Background(), "echo", map[string]any{"ok": true})
			if err == nil || instance.State() != StateQuarantined {
				t.Fatalf("call error=%v state=%s", err, instance.State())
			}
			if waitErr := instance.Wait(context.Background()); !errors.Is(waitErr, ErrQuarantined) {
				t.Fatalf("wait returned %v", waitErr)
			}
		})
	}
}

func TestProcessSessionCleanupIsIdempotentAcrossConcurrentOwners(t *testing.T) {
	broker := &countingCleanupBroker{}
	stream := &countingCleanupStream{}
	session := &processSession{
		handle: sandbox.SandboxHandle{ID: "handle"},
		stream: stream,
	}

	const callers = 16
	var wait sync.WaitGroup
	wait.Add(callers)
	for range callers {
		go func() {
			defer wait.Done()
			if err := session.cleanup(broker); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}()
	}
	wait.Wait()

	if got := stream.closeInputCalls.Load(); got != 1 {
		t.Fatalf("close input calls=%d, want 1", got)
	}
	if got := stream.closeCalls.Load(); got != 1 {
		t.Fatalf("close calls=%d, want 1", got)
	}
	if got := broker.cancelCalls.Load(); got != 1 {
		t.Fatalf("cancel calls=%d, want 1", got)
	}
	if got := broker.disposeCalls.Load(); got != 1 {
		t.Fatalf("dispose calls=%d, want 1", got)
	}
}

type countingCleanupStream struct {
	closeInputCalls atomic.Int32
	closeCalls      atomic.Int32
}

func (s *countingCleanupStream) Events() <-chan sandbox.ExecutionEvent { return nil }
func (s *countingCleanupStream) Errors() <-chan error                  { return nil }
func (s *countingCleanupStream) Send(context.Context, []byte) error    { return nil }
func (s *countingCleanupStream) CloseInput() error {
	s.closeInputCalls.Add(1)
	return nil
}
func (*countingCleanupStream) Wait(context.Context) (sandbox.ExecutionResult, error) {
	return sandbox.ExecutionResult{}, nil
}
func (s *countingCleanupStream) Close() error {
	s.closeCalls.Add(1)
	return nil
}

type countingCleanupBroker struct {
	cancelCalls  atomic.Int32
	disposeCalls atomic.Int32
}

func (*countingCleanupBroker) Capabilities(context.Context) (sandbox.SandboxCapabilities, error) {
	return sandbox.SandboxCapabilities{}, nil
}
func (*countingCleanupBroker) Prepare(context.Context, sandbox.SandboxRequest) (sandbox.SandboxHandle, error) {
	return sandbox.SandboxHandle{}, nil
}
func (*countingCleanupBroker) Execute(context.Context, sandbox.SandboxHandle) (sandbox.ExecutionStream, error) {
	return nil, nil
}
func (b *countingCleanupBroker) Cancel(context.Context, sandbox.SandboxHandle) error {
	b.cancelCalls.Add(1)
	return nil
}
func (b *countingCleanupBroker) Dispose(context.Context, sandbox.SandboxHandle) error {
	b.disposeCalls.Add(1)
	return nil
}

type hostileAdapter struct {
	callPanic bool
	result    json.RawMessage
}

func (h hostileAdapter) Handshake(_ context.Context, expected Handshake) (Handshake, error) {
	return expected, nil
}
func (h hostileAdapter) Call(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	if h.callPanic {
		panic("hostile adapter")
	}
	return append(json.RawMessage(nil), h.result...), nil
}
func (hostileAdapter) Close(context.Context) error { return nil }

func allowAuthorizer() extensionport.Authorizer {
	return extensionport.AuthorizeFunc(func(_ context.Context, item extensionport.Installation) (extensionport.Installation, error) {
		return item, nil
	})
}

type fakeAdapter struct{}

func (fakeAdapter) Handshake(_ context.Context, expected Handshake) (Handshake, error) {
	return expected, nil
}
func (fakeAdapter) Call(_ context.Context, _ string, params json.RawMessage) (json.RawMessage, error) {
	return append(json.RawMessage(nil), params...), nil
}
func (fakeAdapter) Close(context.Context) error { return nil }

func testManifest() extensionport.Manifest {
	return extensionport.Manifest{
		ID: "test.extension", Name: "Test Extension", Publisher: "test.publisher", Version: "1.0.0",
		ProtocolVersion: ProtocolVersion, AdapterVersion: "1.0.0",
		SchemaDigest: "sha256:" + strings.Repeat("1", 64), ArtifactDigest: "sha256:" + strings.Repeat("2", 64),
		ExecutionMode: extensionport.ExecutionOutOfProcess, MaxMessageBytes: 64 << 10,
		Capabilities: []string{"echo", "health"}, Permissions: []string{"events:read"},
	}
}

func testSupervisorRequest(t *testing.T, mode string) (*local.Broker, StartRequest) {
	t.Helper()
	broker, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := []string{executable, "-test.run=^TestExtensionHelperProcess$", "--", "--extension-mode", mode}
	manifest := testManifest()
	manifest.ExecutionMode = extensionport.ExecutionOutOfProcess
	return broker, StartRequest{
		Installation: extensionport.Installation{Manifest: manifest, State: "active", TenantID: "tenant", WorkspaceID: "workspace", Digest: "digest", Signature: "signature", KeyID: "key"},
		Command:      command, WorkingDir: ".", RequiredLevel: sandbox.EnforcementProcess,
		Environment: map[string]string{"ADRO_EXTENSION_HELPER": "1"},
		Limits:      sandbox.ResourceBudget{WallTimeout: 3 * time.Second, MaxOutputBytes: 1 << 20},
	}
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal test JSON: %v", err))
	}
	return data
}
