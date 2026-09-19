// Package extensions owns the narrow process boundary for third-party
// adapters. It deliberately does not expose EventStore, database, or secret
// handles: an extension receives only a bounded JSON-RPC stream over a
// sandbox.ExecutionStream.
package extensions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	corepolicy "github.com/adro-project/adro/core/policy"
	extensionport "github.com/adro-project/adro/ports/extensions"
	policyport "github.com/adro-project/adro/ports/policy"
	"github.com/adro-project/adro/ports/sandbox"
	"github.com/adro-project/adro/ports/secretstore"
)

const (
	ProtocolVersion       = "adro.extension.v1"
	DefaultMaxRestarts    = 3
	DefaultInitialBackoff = 25 * time.Millisecond
	DefaultMaxBackoff     = 2 * time.Second
	defaultCallTimeout    = 30 * time.Second
	maxErrorMessageBytes  = 1024
)

var (
	ErrInvalidRequest       = errors.New("invalid extension request")
	ErrUnauthorized         = errors.New("extension execution is not authorized")
	ErrUnavailable          = errors.New("extension is unavailable")
	ErrStopped              = errors.New("extension is stopped")
	ErrQuarantined          = errors.New("extension is quarantined")
	ErrProtocol             = errors.New("extension protocol violation")
	ErrProtocolMismatch     = errors.New("extension handshake mismatch")
	ErrMessageTooLarge      = errors.New("extension message exceeds negotiated limit")
	ErrRestartLimit         = errors.New("extension restart limit exceeded")
	ErrSecretsUnavailable   = errors.New("extension secret injection is unavailable")
	ErrUnsupportedExecution = errors.New("extension execution mode is unsupported")
	ErrAdapterPanic         = errors.New("in-process extension panicked")
	ErrPolicyDenied         = errors.New("extension data egress is denied")
	ErrPolicyUnavailable    = errors.New("extension policy decision cannot be persisted")
)

type State string

const (
	StateStarting    State = "starting"
	StateRunning     State = "running"
	StateRestarting  State = "restarting"
	StateStopped     State = "stopped"
	StateQuarantined State = "quarantined"
)

// Handshake is exchanged before any adapter method is accepted. The
// supervisor sends the manifest's declared values; the adapter must echo only
// values it actually supports and may request a subset of declared privileges.
type Handshake struct {
	ProtocolVersion     string   `json:"protocol_version"`
	AdapterVersion      string   `json:"adapter_version"`
	SchemaDigest        string   `json:"schema_digest"`
	Capabilities        []string `json:"capabilities"`
	RequiredPermissions []string `json:"required_permissions"`
	MaxMessageBytes     int64    `json:"max_message_bytes"`
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// RPCError is a bounded adapter error. The supervisor never copies arbitrary
// extension output into audit records.
type RPCError struct {
	Code    int
	Message string
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("extension rpc error %d: %s", e.Code, e.Message)
}

type AuditEvent struct {
	At           time.Time
	ExtensionID  string
	Version      string
	Action       string
	State        State
	RestartCount int
	Reason       string
}

type AuditFunc func(AuditEvent)

type InProcessAdapter interface {
	Handshake(context.Context, Handshake) (Handshake, error)
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
	Close(context.Context) error
}

type InProcessFactory func(context.Context, extensionport.Installation) (InProcessAdapter, error)

type StartRequest struct {
	Installation  extensionport.Installation
	Command       []string
	WorkingDir    string
	TenantID      string
	SessionID     string
	EffectID      string
	RequiredLevel sandbox.EnforcementLevel
	Limits        sandbox.ResourceBudget
	Environment   map[string]string
	SecretRefs    []secretstore.SecretRef
}

// EgressRequest classifies one outbound adapter call. The runtime records the
// resulting decision before it sends any payload to the extension process.
type EgressRequest struct {
	TenantID    string
	WorkspaceID string
	ActorID     string
	Destination string
	Purpose     string
	Sensitivity corepolicy.Sensitivity
}

type Config struct {
	Broker           sandbox.SandboxBroker
	Authorizer       extensionport.Authorizer
	InProcessFactory InProcessFactory
	Quarantiner      extensionport.Quarantiner
	Audit            AuditFunc
	Now              func() time.Time
	Sleep            func(context.Context, time.Duration) error
	MaxRestarts      int
	InitialBackoff   time.Duration
	MaxBackoff       time.Duration
	CallTimeout      time.Duration
	PolicyEvaluator  corepolicy.Evaluator
	PolicyRecorder   policyport.DecisionRecorder
	PolicyTimeout    time.Duration
}

type Supervisor struct {
	cfg Config
}

func NewSupervisor(cfg Config) (*Supervisor, error) {
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepContext
	}
	if cfg.MaxRestarts < 0 {
		return nil, fmt.Errorf("%w: max restarts cannot be negative", ErrInvalidRequest)
	}
	if cfg.MaxRestarts == 0 {
		cfg.MaxRestarts = DefaultMaxRestarts
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = DefaultInitialBackoff
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = DefaultMaxBackoff
		if cfg.MaxBackoff < cfg.InitialBackoff {
			cfg.MaxBackoff = cfg.InitialBackoff
		}
	}
	if cfg.MaxBackoff < cfg.InitialBackoff {
		return nil, fmt.Errorf("%w: max backoff cannot be below initial backoff", ErrInvalidRequest)
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = defaultCallTimeout
	}
	if cfg.PolicyEvaluator == nil {
		cfg.PolicyEvaluator = corepolicy.BuiltinEvaluator{}
	}
	if cfg.PolicyTimeout <= 0 {
		cfg.PolicyTimeout = cfg.CallTimeout
	}
	if cfg.Broker == nil && cfg.InProcessFactory == nil {
		return nil, fmt.Errorf("%w: sandbox broker or in-process factory is required", ErrInvalidRequest)
	}
	return &Supervisor{cfg: cfg}, nil
}

func (s *Supervisor) Start(ctx context.Context, request StartRequest) (*Instance, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	authorized, err := s.authorize(ctx, request.Installation)
	if err != nil {
		return nil, err
	}
	request.Installation = authorized
	if request.RequiredLevel == "" {
		request.RequiredLevel = sandbox.EnforcementProcess
	}
	if err := s.validateStart(request); err != nil {
		return nil, err
	}
	policyBundle, err := extensionPolicyBundle(request.Installation)
	if err != nil {
		return nil, err
	}
	instance := &Instance{
		supervisor:   s,
		request:      cloneStartRequest(request),
		state:        StateStarting,
		done:         make(chan struct{}),
		policyBundle: policyBundle,
	}
	instance.lifecycleCtx, instance.cancel = context.WithCancel(context.Background())
	instance.audit("start", StateStarting, "")
	if request.Installation.Manifest.ExecutionMode == extensionport.ExecutionInProcess {
		if err := instance.startInProcess(ctx); err != nil {
			instance.quarantine(err)
			instance.finishDone()
			return nil, err
		}
		return instance, nil
	}
	if err := instance.startExternalWithRetries(ctx, false); err != nil {
		instance.quarantine(err)
		instance.finishDone()
		return nil, err
	}
	go instance.monitor()
	return instance, nil
}

func (s *Supervisor) validateStart(request StartRequest) error {
	item := request.Installation
	manifest := item.Manifest
	if strings.TrimSpace(item.Manifest.ID) == "" || strings.TrimSpace(item.Manifest.Version) == "" || item.State == "" {
		return fmt.Errorf("%w: installation identity and state are required", ErrInvalidRequest)
	}
	if item.State != "active" {
		return fmt.Errorf("%w: installation state is %s", ErrUnauthorized, item.State)
	}
	if request.TenantID != "" && request.TenantID != item.TenantID {
		return fmt.Errorf("%w: sandbox tenant differs from signed installation scope", ErrUnauthorized)
	}
	if manifest.ExecutionMode == extensionport.ExecutionInProcess {
		if s.cfg.InProcessFactory == nil {
			return fmt.Errorf("%w: in-process adapter factory is unavailable", ErrUnsupportedExecution)
		}
	} else if manifest.ExecutionMode == extensionport.ExecutionWASI {
		return fmt.Errorf("%w: WASI requires a dedicated runtime that is not configured", ErrUnsupportedExecution)
	} else if manifest.ExecutionMode != extensionport.ExecutionOutOfProcess {
		return fmt.Errorf("%w: %s", ErrUnsupportedExecution, manifest.ExecutionMode)
	}
	if manifest.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("%w: manifest protocol %q", ErrProtocolMismatch, manifest.ProtocolVersion)
	}
	if manifest.MaxMessageBytes < 1024 {
		return fmt.Errorf("%w: manifest max message size is too small", ErrInvalidRequest)
	}
	if manifest.MaxMessageBytes > 16<<20 {
		return fmt.Errorf("%w: manifest max message size is too large", ErrInvalidRequest)
	}
	if manifest.ExecutionMode != extensionport.ExecutionInProcess {
		if s.cfg.Broker == nil {
			return fmt.Errorf("%w: out-of-process execution requires a sandbox broker", ErrInvalidRequest)
		}
		if len(request.Command) == 0 || strings.TrimSpace(request.Command[0]) == "" {
			return fmt.Errorf("%w: extension command is required", ErrInvalidRequest)
		}
		if strings.TrimSpace(request.WorkingDir) == "" {
			return fmt.Errorf("%w: extension working directory is required", ErrInvalidRequest)
		}
	}
	if request.RequiredLevel == "" {
		request.RequiredLevel = sandbox.EnforcementProcess
	}
	if !request.RequiredLevel.Valid() {
		return fmt.Errorf("%w: invalid sandbox enforcement level", ErrInvalidRequest)
	}
	if err := request.Limits.Validate(); err != nil {
		return err
	}
	for _, ref := range request.SecretRefs {
		if !ref.Valid() {
			return fmt.Errorf("%w: invalid secret reference", ErrInvalidRequest)
		}
	}
	if len(manifest.SecretPermissions) > 0 && len(request.SecretRefs) == 0 {
		return ErrSecretsUnavailable
	}
	if _, err := sanitizeEnvironment(request.Environment); err != nil {
		return err
	}
	if err := validateEgressNetworkBinding(manifest); err != nil {
		return err
	}
	return nil
}

func (s *Supervisor) authorize(ctx context.Context, item extensionport.Installation) (extensionport.Installation, error) {
	if s.cfg.Authorizer == nil {
		return extensionport.Installation{}, ErrUnauthorized
	}
	authorized, err := s.cfg.Authorizer.AuthorizeExecution(ctx, cloneInstallation(item))
	if err != nil {
		return extensionport.Installation{}, fmt.Errorf("%w: %v", ErrUnauthorized, err)
	}
	if !sameInstallationIdentity(item, authorized) {
		return extensionport.Installation{}, fmt.Errorf("%w: authorizer changed installation identity", ErrUnauthorized)
	}
	return cloneInstallation(authorized), nil
}

type Instance struct {
	supervisor *Supervisor
	request    StartRequest

	mu             sync.Mutex
	rpcMu          sync.Mutex
	state          State
	restarts       int
	lastErr        error
	session        *processSession
	inProcess      InProcessAdapter
	policyBundle   *corepolicy.FrozenBundle
	lifecycleCtx   context.Context
	cancel         context.CancelFunc
	done           chan struct{}
	closeDoneOnce  sync.Once
	nextID         atomic.Uint64
	quarantineOnce sync.Once
}

func (i *Instance) State() State {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.state
}

func (i *Instance) RestartCount() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.restarts
}

func (i *Instance) LastError() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.lastErr
}

func (i *Instance) Wait(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-i.done:
		if err := i.LastError(); err != nil {
			return err
		}
		if state := i.State(); state == StateQuarantined {
			return ErrQuarantined
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (i *Instance) startInProcess(ctx context.Context) error {
	adapter, err := createInProcess(ctx, i.supervisor.cfg.InProcessFactory, i.request.Installation)
	if err != nil {
		return err
	}
	handshakeCtx, cancelHandshake := withDefaultTimeout(ctx, i.supervisor.cfg.CallTimeout)
	response, err := handshakeInProcess(handshakeCtx, adapter, i.expectedHandshake())
	cancelHandshake()
	if err != nil {
		_ = closeInProcess(context.Background(), adapter)
		return fmt.Errorf("%w: %v", ErrProtocolMismatch, err)
	}
	if err := validateHandshake(i.request.Installation.Manifest, response); err != nil {
		_ = closeInProcess(context.Background(), adapter)
		return err
	}
	i.mu.Lock()
	i.inProcess = adapter
	i.state = StateRunning
	i.mu.Unlock()
	i.audit("running", StateRunning, "in-process handshake accepted")
	return nil
}

func (i *Instance) startExternalWithRetries(ctx context.Context, restarting bool) error {
	var last error
	for {
		if restarting {
			restart, ok := i.reserveRestart()
			if !ok {
				if last == nil {
					last = ErrUnavailable
				}
				return fmt.Errorf("%w: %v", ErrRestartLimit, last)
			}
			if err := i.supervisor.cfg.Sleep(ctx, i.backoff(restart-1)); err != nil {
				return err
			}
		}
		if err := i.startExternal(ctx); err == nil {
			return nil
		} else {
			last = err
			i.mu.Lock()
			i.lastErr = err
			state := i.state
			i.mu.Unlock()
			i.audit("start_failed", state, safeReason(err))
			restarting = true
		}
	}
}

func (i *Instance) reserveRestart() (int, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.restarts >= i.supervisor.cfg.MaxRestarts {
		return i.restarts, false
	}
	i.restarts++
	i.state = StateRestarting
	return i.restarts, true
}

func (i *Instance) startExternal(ctx context.Context) error {
	request := i.request
	manifest := request.Installation.Manifest
	now := i.supervisor.cfg.Now().UTC()
	limits := request.Limits
	if limits.MaxOutputBytes == 0 {
		limits.MaxOutputBytes = manifest.MaxMessageBytes * 8
		if limits.MaxOutputBytes < 1<<20 {
			limits.MaxOutputBytes = 1 << 20
		}
	}
	if limits.WallTimeout == 0 {
		limits.WallTimeout = 24 * time.Hour
	}
	fileGrants, networkGrants, err := manifestGrants(manifest, now, limits.WallTimeout)
	if err != nil {
		return err
	}
	environment, err := sanitizeEnvironment(request.Environment)
	if err != nil {
		return err
	}
	tenantID, sessionID, effectID := request.TenantID, request.SessionID, request.EffectID
	if tenantID == "" {
		tenantID = request.Installation.TenantID
	}
	if sessionID == "" {
		sessionID = "extension:" + manifest.ID
	}
	if effectID == "" {
		effectID = "extension-start:" + manifest.ID
	}
	handle, err := i.supervisor.cfg.Broker.Prepare(ctx, sandbox.SandboxRequest{
		TenantID: tenantID, SessionID: sessionID, EffectID: effectID,
		RequiredLevel: request.RequiredLevel, Command: append([]string(nil), request.Command...),
		WorkingDir: request.WorkingDir, FileGrants: fileGrants, NetworkGrants: networkGrants,
		SecretRefs: append([]secretstore.SecretRef(nil), request.SecretRefs...), Limits: limits, Environment: environment,
	})
	if err != nil {
		return err
	}
	stream, err := i.supervisor.cfg.Broker.Execute(ctx, handle)
	if err != nil {
		_ = i.supervisor.cfg.Broker.Dispose(context.Background(), handle)
		return err
	}
	session := newProcessSession(handle, stream, manifest.MaxMessageBytes)
	i.mu.Lock()
	i.session = session
	i.mu.Unlock()
	handshakeCtx, cancelHandshake := withDefaultTimeout(ctx, i.supervisor.cfg.CallTimeout)
	response, err := i.callSession(handshakeCtx, session, "adro.handshake", i.expectedHandshake())
	cancelHandshake()
	if err != nil {
		i.cleanupSession(session)
		i.mu.Lock()
		if i.session == session {
			i.session = nil
		}
		i.mu.Unlock()
		return err
	}
	var handshake Handshake
	if err := json.Unmarshal(response, &handshake); err != nil {
		i.cleanupSession(session)
		return fmt.Errorf("%w: handshake result: %v", ErrProtocol, err)
	}
	if err := validateHandshake(manifest, handshake); err != nil {
		i.cleanupSession(session)
		return err
	}
	// The adapter can negotiate a stricter limit than the signed manifest.
	// The pump and dispatch path may run concurrently, so publish it atomically
	// before making this session available for calls.
	session.maxMessageBytes.Store(handshake.MaxMessageBytes)
	i.mu.Lock()
	if i.session == session {
		i.state = StateRunning
	}
	i.mu.Unlock()
	i.audit("running", StateRunning, "handshake accepted")
	return nil
}

func (i *Instance) expectedHandshake() Handshake {
	manifest := i.request.Installation.Manifest
	return Handshake{
		ProtocolVersion: manifest.ProtocolVersion, AdapterVersion: manifest.AdapterVersion,
		SchemaDigest: manifest.SchemaDigest, Capabilities: cloneStrings(manifest.Capabilities),
		RequiredPermissions: cloneStrings(manifest.Permissions), MaxMessageBytes: manifest.MaxMessageBytes,
	}
}

func (i *Instance) monitor() {
	for {
		i.mu.Lock()
		session := i.session
		ctx := i.lifecycleCtx
		state := i.state
		i.mu.Unlock()
		if session == nil || state == StateStopped || state == StateQuarantined {
			i.finishDone()
			return
		}
		_, waitErr := session.stream.Wait(context.Background())
		if waitErr == nil {
			waitErr = fmt.Errorf("%w: extension process exited", ErrUnavailable)
		}
		if ctx.Err() != nil {
			i.cleanupSession(session)
			i.finishDone()
			return
		}
		i.mu.Lock()
		if i.session == session {
			i.session = nil
			i.lastErr = waitErr
			i.state = StateRestarting
		}
		i.mu.Unlock()
		i.cleanupSession(session)
		i.audit("crash", StateRestarting, safeReason(waitErr))
		if err := i.startExternalWithRetries(ctx, true); err != nil {
			i.quarantine(err)
			i.finishDone()
			return
		}
		// startExternalWithRetries installed a fresh session. Loop and wait for
		// that process. Calls can proceed as soon as its handshake succeeded.
	}
}

func (i *Instance) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return i.call(ctx, method, params, nil)
}

// CallWithEgress binds a classified payload to the signed destination grant.
// The decision record is persisted before the adapter receives the request.
func (i *Instance) CallWithEgress(ctx context.Context, method string, params any, egress EgressRequest) (json.RawMessage, error) {
	return i.call(ctx, method, params, &egress)
}

func (i *Instance) call(ctx context.Context, method string, params any, egress *EgressRequest) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	method = strings.TrimSpace(method)
	if method == "" || strings.ContainsAny(method, "\r\n") {
		return nil, fmt.Errorf("%w: method is invalid", ErrInvalidRequest)
	}
	if method == "adro.handshake" || method != "adro.health" && !contains(i.request.Installation.Manifest.Capabilities, method) {
		return nil, fmt.Errorf("%w: method %q is not declared by the signed manifest", ErrUnauthorized, method)
	}
	callCtx, cancelCall := withDefaultTimeout(ctx, i.supervisor.cfg.CallTimeout)
	defer cancelCall()
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("%w: encode params: %v", ErrInvalidRequest, err)
	}
	payloadJSON := json.RawMessage(payload)
	i.rpcMu.Lock()
	defer i.rpcMu.Unlock()
	i.mu.Lock()
	state, session, adapter := i.state, i.session, i.inProcess
	i.mu.Unlock()
	if state == StateStopped {
		return nil, ErrStopped
	}
	if state == StateQuarantined {
		return nil, ErrQuarantined
	}
	if state != StateRunning {
		return nil, ErrUnavailable
	}
	if method != "adro.health" {
		if err := i.authorizeEgress(callCtx, method, egress); err != nil {
			return nil, err
		}
	}
	if adapter != nil {
		if int64(len(payloadJSON)) > i.request.Installation.Manifest.MaxMessageBytes {
			return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, ErrMessageTooLarge)
		}
		result, err := callInProcess(callCtx, adapter, method, payloadJSON)
		if err == nil && int64(len(result)) > i.request.Installation.Manifest.MaxMessageBytes {
			err = ErrMessageTooLarge
		}
		if err == nil && !json.Valid(result) {
			err = fmt.Errorf("%w: in-process adapter returned invalid JSON", ErrProtocol)
		}
		if err != nil {
			if errors.Is(err, ErrAdapterPanic) || errors.Is(err, ErrProtocol) || errors.Is(err, ErrMessageTooLarge) {
				i.quarantine(err)
				i.finishDone()
			}
			return nil, err
		}
		return append(json.RawMessage(nil), result...), nil
	}
	if session == nil {
		return nil, ErrUnavailable
	}
	result, err := i.callSession(callCtx, session, method, payloadJSON)
	if err != nil && (errors.Is(err, ErrProtocol) || errors.Is(err, ErrMessageTooLarge) && !errors.Is(err, ErrInvalidRequest)) {
		_ = session.stream.Close()
	}
	return result, err
}

func (i *Instance) authorizeEgress(ctx context.Context, method string, request *EgressRequest) error {
	if i.policyBundle == nil {
		if request != nil {
			return fmt.Errorf("%w: extension has no signed data-egress grant", ErrPolicyDenied)
		}
		return nil
	}
	if request == nil {
		return fmt.Errorf("%w: classified egress metadata is required", ErrPolicyDenied)
	}
	input := corepolicy.Input{
		TenantID: request.TenantID, WorkspaceID: request.WorkspaceID, ActorID: request.ActorID,
		Capability: method, Destination: request.Destination, Purpose: request.Purpose, Sensitivity: request.Sensitivity,
	}
	record, err := corepolicy.EvaluateFailClosed(
		ctx, i.supervisor.cfg.PolicyEvaluator, *i.policyBundle, input,
		i.supervisor.cfg.Now().UTC(), i.supervisor.cfg.PolicyTimeout, corepolicy.EngineVersion,
	)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if i.supervisor.cfg.PolicyRecorder == nil {
		i.audit("policy_unavailable", i.State(), "decision recorder is not configured")
		return ErrPolicyUnavailable
	}
	if err := i.supervisor.cfg.PolicyRecorder.RecordDecision(ctx, record); err != nil {
		i.audit("policy_unavailable", i.State(), "decision record failed")
		return ErrPolicyUnavailable
	}
	if record.Outcome != corepolicy.OutcomeAllow {
		i.audit("policy_denied", i.State(), record.ReasonCode)
		return fmt.Errorf("%w: %s", ErrPolicyDenied, record.ReasonCode)
	}
	return nil
}

func (i *Instance) Health(ctx context.Context) error {
	callCtx := ctx
	if callCtx == nil {
		callCtx = context.Background()
	}
	if _, ok := callCtx.Deadline(); !ok {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(callCtx, i.supervisor.cfg.CallTimeout)
		defer cancel()
	}
	_, err := i.Call(callCtx, "adro.health", map[string]any{})
	return err
}

func (i *Instance) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	i.mu.Lock()
	if i.state == StateStopped {
		i.mu.Unlock()
		return nil
	}
	i.state = StateStopped
	cancel := i.cancel
	session := i.session
	adapter := i.inProcess
	i.session, i.inProcess = nil, nil
	i.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	var result error
	if adapter != nil {
		result = closeInProcess(ctx, adapter)
	}
	if session != nil {
		result = errors.Join(result, i.cleanupSession(session))
	}
	i.audit("stopped", StateStopped, safeReason(result))
	i.finishDone()
	return result
}

func (i *Instance) callSession(ctx context.Context, session *processSession, method string, params any) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	i.nextID.Add(1)
	id := fmt.Sprintf("%d", i.nextID.Load())
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	request := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: payload}
	frame, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if int64(len(frame)+1) > session.maxMessageBytes.Load() {
		// An oversized *caller* frame is not a protocol failure by the
		// extension. Preserve the healthy process for later valid calls.
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, ErrMessageTooLarge)
	}
	frame = append(frame, '\n')
	if err := session.stream.Send(ctx, frame); err != nil {
		return nil, fmt.Errorf("%w: send: %v", ErrUnavailable, err)
	}
	for {
		select {
		case <-ctx.Done():
			_ = session.stream.Close()
			return nil, ctx.Err()
		case err := <-session.failures:
			if err == nil {
				return nil, ErrUnavailable
			}
			return nil, err
		case frame, ok := <-session.frames:
			if !ok {
				return nil, ErrUnavailable
			}
			var response rpcResponse
			if err := json.Unmarshal(frame, &response); err != nil {
				return nil, fmt.Errorf("%w: invalid response JSON", ErrProtocol)
			}
			if response.JSONRPC != "2.0" || response.ID != id {
				return nil, fmt.Errorf("%w: response id/version mismatch", ErrProtocol)
			}
			if response.Error != nil {
				message := response.Error.Message
				if len(message) > maxErrorMessageBytes {
					message = message[:maxErrorMessageBytes]
				}
				return nil, &RPCError{Code: response.Error.Code, Message: message}
			}
			if len(response.Result) == 0 {
				return nil, fmt.Errorf("%w: response omitted result", ErrProtocol)
			}
			return append(json.RawMessage(nil), response.Result...), nil
		}
	}
}

func (i *Instance) cleanupSession(session *processSession) error {
	if session == nil {
		return nil
	}
	return session.cleanup(i.supervisor.cfg.Broker)
}

func (i *Instance) failStartup(err error) {
	i.mu.Lock()
	i.state = StateStopped
	i.lastErr = err
	i.mu.Unlock()
	if i.cancel != nil {
		i.cancel()
	}
	i.finishDone()
}

func (i *Instance) quarantine(err error) {
	i.quarantineOnce.Do(func() {
		i.mu.Lock()
		i.state = StateQuarantined
		i.lastErr = errors.Join(ErrQuarantined, err)
		i.mu.Unlock()
		reason := safeReason(err)
		if i.supervisor.cfg.Quarantiner != nil {
			quarantineCtx, cancel := context.WithTimeout(context.Background(), i.supervisor.cfg.CallTimeout)
			quarantineErr := i.supervisor.cfg.Quarantiner.QuarantineExecution(quarantineCtx, cloneInstallation(i.request.Installation), reason)
			cancel()
			if quarantineErr != nil {
				i.audit("quarantine_persist_failed", StateQuarantined, safeReason(quarantineErr))
			}
		}
		i.audit("quarantined", StateQuarantined, reason)
	})
}

func (i *Instance) finishDone() {
	i.closeDoneOnce.Do(func() { close(i.done) })
}

func (i *Instance) backoff(attempt int) time.Duration {
	backoff := i.supervisor.cfg.InitialBackoff
	for n := 0; n < attempt && backoff < i.supervisor.cfg.MaxBackoff; n++ {
		backoff *= 2
	}
	if backoff > i.supervisor.cfg.MaxBackoff {
		backoff = i.supervisor.cfg.MaxBackoff
	}
	return backoff
}

func (i *Instance) audit(action string, state State, reason string) {
	if i.supervisor.cfg.Audit == nil {
		return
	}
	i.mu.Lock()
	restarts := i.restarts
	i.mu.Unlock()
	i.supervisor.cfg.Audit(AuditEvent{At: i.supervisor.cfg.Now().UTC(), ExtensionID: i.request.Installation.Manifest.ID, Version: i.request.Installation.Manifest.Version, Action: action, State: state, RestartCount: restarts, Reason: reason})
}

type processSession struct {
	handle          sandbox.SandboxHandle
	stream          sandbox.ExecutionStream
	maxMessageBytes atomic.Int64
	frames          chan []byte
	failures        chan error
	failureOnce     sync.Once
	cleanupOnce     sync.Once
	cleanupErr      error
}

func newProcessSession(handle sandbox.SandboxHandle, stream sandbox.ExecutionStream, maxMessageBytes int64) *processSession {
	session := &processSession{handle: handle, stream: stream, frames: make(chan []byte, 16), failures: make(chan error, 1)}
	session.maxMessageBytes.Store(maxMessageBytes)
	go session.pump()
	return session
}

func (s *processSession) pump() {
	defer close(s.frames)
	var pending []byte
	for event := range s.stream.Events() {
		if event.Stream != "stdout" || len(event.Data) == 0 {
			continue
		}
		if int64(len(pending)+len(event.Data)) > s.maxMessageBytes.Load()*2 {
			s.fail(fmt.Errorf("%w: frame buffer exceeded negotiated bound", ErrMessageTooLarge))
			_ = s.stream.Close()
			return
		}
		pending = append(pending, event.Data...)
		for {
			index := bytes.IndexByte(pending, '\n')
			if index < 0 {
				if int64(len(pending)) > s.maxMessageBytes.Load() {
					s.fail(ErrMessageTooLarge)
					_ = s.stream.Close()
					return
				}
				break
			}
			frame := bytes.TrimSuffix(append([]byte(nil), pending[:index]...), []byte{'\r'})
			pending = pending[index+1:]
			if len(frame) == 0 {
				s.fail(fmt.Errorf("%w: empty frame", ErrProtocol))
				_ = s.stream.Close()
				return
			}
			if int64(len(frame)) > s.maxMessageBytes.Load() {
				s.fail(ErrMessageTooLarge)
				_ = s.stream.Close()
				return
			}
			select {
			case s.frames <- frame:
			default:
				s.fail(fmt.Errorf("%w: response queue is full", ErrProtocol))
				_ = s.stream.Close()
				return
			}
		}
	}
	if len(bytes.TrimSpace(pending)) != 0 {
		s.fail(fmt.Errorf("%w: unterminated frame", ErrProtocol))
	}
}

func (s *processSession) fail(err error) {
	s.failureOnce.Do(func() { s.failures <- err })
}

func (s *processSession) cleanup(broker sandbox.SandboxBroker) error {
	s.cleanupOnce.Do(func() {
		s.cleanupErr = errors.Join(s.stream.CloseInput(), s.stream.Close())
		if broker != nil {
			s.cleanupErr = errors.Join(s.cleanupErr, broker.Cancel(context.Background(), s.handle))
			s.cleanupErr = errors.Join(s.cleanupErr, broker.Dispose(context.Background(), s.handle))
		}
	})
	return s.cleanupErr
}

func validateHandshake(manifest extensionport.Manifest, got Handshake) error {
	if got.ProtocolVersion != manifest.ProtocolVersion || got.AdapterVersion != manifest.AdapterVersion {
		return fmt.Errorf("%w: protocol=%q adapter=%q", ErrProtocolMismatch, got.ProtocolVersion, got.AdapterVersion)
	}
	if got.SchemaDigest != manifest.SchemaDigest && !contains(manifest.CompatibleSchemaDigests, got.SchemaDigest) {
		return fmt.Errorf("%w: schema digest %q", ErrProtocolMismatch, got.SchemaDigest)
	}
	if got.MaxMessageBytes < 1024 || got.MaxMessageBytes > manifest.MaxMessageBytes {
		return fmt.Errorf("%w: max message bytes %d", ErrProtocolMismatch, got.MaxMessageBytes)
	}
	if !subset(got.Capabilities, manifest.Capabilities) || !subset(got.RequiredPermissions, manifest.Permissions) {
		return fmt.Errorf("%w: adapter requested undeclared capability or permission", ErrProtocolMismatch)
	}
	return nil
}

func manifestGrants(manifest extensionport.Manifest, now time.Time, wallTimeout time.Duration) ([]sandbox.FileGrant, []sandbox.NetworkGrant, error) {
	files := make([]sandbox.FileGrant, 0, len(manifest.FilePermissions))
	for _, permission := range manifest.FilePermissions {
		grant := sandbox.FileGrant{Path: permission.Path}
		for _, operation := range permission.Operations {
			switch operation {
			case "read":
				grant.Read = true
			case "write":
				grant.Write = true
			case "create":
				grant.Create = true
			case "delete":
				grant.Delete = true
			case "execute":
				grant.Execute = true
			default:
				return nil, nil, fmt.Errorf("%w: unknown file permission %q", ErrInvalidRequest, operation)
			}
		}
		files = append(files, grant)
	}
	expires := now.Add(wallTimeout)
	if !expires.After(now) {
		expires = now.Add(time.Hour)
	}
	network := make([]sandbox.NetworkGrant, 0, len(manifest.NetworkPermissions))
	for _, permission := range manifest.NetworkPermissions {
		network = append(network, sandbox.NetworkGrant{Domain: permission.Domain, IP: permission.IP, CIDR: permission.CIDR, Ports: append([]int(nil), permission.Ports...), Protocol: permission.Protocol, Purpose: permission.Purpose, ExpiresAt: expires})
	}
	return files, network, nil
}

func sanitizeEnvironment(environment map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(environment))
	for key, value := range environment {
		if key == "" || strings.TrimSpace(key) != key || strings.ContainsAny(key, "=\x00\r\n") {
			return nil, fmt.Errorf("%w: invalid environment key", ErrInvalidRequest)
		}
		upper := strings.ToUpper(key)
		for _, blocked := range []string{"LD_", "DYLD_", "GODEBUG", "GOFLAGS", "NODE_OPTIONS", "PYTHONPATH", "RUBYOPT", "PERL5OPT", "BASH_ENV", "ENV", "IFS", "SHELLOPTS"} {
			if upper == blocked || strings.HasPrefix(upper, blocked) {
				return nil, fmt.Errorf("%w: environment key %q is not permitted", ErrUnauthorized, key)
			}
		}
		if strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("%w: environment value contains NUL", ErrInvalidRequest)
		}
		result[key] = value
	}
	return result, nil
}

func sameInstallationIdentity(requested, authorized extensionport.Installation) bool {
	return requested.Manifest.ID == authorized.Manifest.ID && requested.Manifest.Version == authorized.Manifest.Version &&
		requested.TenantID == authorized.TenantID && requested.WorkspaceID == authorized.WorkspaceID &&
		requested.Digest == authorized.Digest && requested.Signature == authorized.Signature && requested.KeyID == authorized.KeyID
}

func withDefaultTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func createInProcess(ctx context.Context, factory InProcessFactory, item extensionport.Installation) (adapter InProcessAdapter, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			adapter = nil
			err = ErrAdapterPanic
		}
	}()
	return factory(ctx, item)
}

func handshakeInProcess(ctx context.Context, adapter InProcessAdapter, expected Handshake) (response Handshake, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			response = Handshake{}
			err = ErrAdapterPanic
		}
	}()
	return adapter.Handshake(ctx, expected)
}

func callInProcess(ctx context.Context, adapter InProcessAdapter, method string, payload json.RawMessage) (result json.RawMessage, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = nil
			err = ErrAdapterPanic
		}
	}()
	return adapter.Call(ctx, method, payload)
}

func closeInProcess(ctx context.Context, adapter InProcessAdapter) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = ErrAdapterPanic
		}
	}()
	return adapter.Close(ctx)
}

func cloneStartRequest(request StartRequest) StartRequest {
	request.Command = append([]string(nil), request.Command...)
	environment := request.Environment
	request.Environment = make(map[string]string, len(environment))
	for key, value := range environment {
		request.Environment[key] = value
	}
	request.SecretRefs = append([]secretstore.SecretRef(nil), request.SecretRefs...)
	request.Installation = cloneInstallation(request.Installation)
	return request
}

func cloneInstallation(item extensionport.Installation) extensionport.Installation {
	manifest := item.Manifest
	manifest.Capabilities = append([]string(nil), manifest.Capabilities...)
	manifest.Permissions = append([]string(nil), manifest.Permissions...)
	manifest.CompatibleSchemaDigests = append([]string(nil), manifest.CompatibleSchemaDigests...)
	manifest.FilePermissions = append([]extensionport.FilePermission(nil), manifest.FilePermissions...)
	for index := range manifest.FilePermissions {
		manifest.FilePermissions[index].Operations = append([]string(nil), manifest.FilePermissions[index].Operations...)
	}
	manifest.NetworkPermissions = append([]extensionport.NetworkPermission(nil), manifest.NetworkPermissions...)
	for index := range manifest.NetworkPermissions {
		manifest.NetworkPermissions[index].Ports = append([]int(nil), manifest.NetworkPermissions[index].Ports...)
	}
	manifest.SecretPermissions = append([]extensionport.SecretPermission(nil), manifest.SecretPermissions...)
	manifest.DataEgressPermissions = append([]extensionport.DataEgressPermission(nil), manifest.DataEgressPermissions...)
	item.Manifest = manifest
	return item
}

func cloneStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func subset(values, allowed []string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		set[value] = struct{}{}
	}
	for _, value := range values {
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func safeReason(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > maxErrorMessageBytes {
		message = message[:maxErrorMessageBytes]
	}
	return message
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func extensionPolicyBundle(item extensionport.Installation) (*corepolicy.FrozenBundle, error) {
	if len(item.Manifest.DataEgressPermissions) == 0 {
		return nil, nil
	}
	rules := make([]corepolicy.EgressRule, len(item.Manifest.DataEgressPermissions))
	for index, permission := range item.Manifest.DataEgressPermissions {
		rules[index] = corepolicy.EgressRule{
			Destination: permission.Destination, Purpose: permission.Purpose,
			MaxSensitivity: corepolicy.Sensitivity(permission.MaxSensitivity),
		}
	}
	bundle, err := corepolicy.FreezeBundle(corepolicy.Bundle{
		ID: "extension:" + item.Manifest.ID, Version: item.Manifest.Version + "@" + item.Digest,
		TenantID: item.TenantID, WorkspaceID: item.WorkspaceID,
		Capabilities: append([]string(nil), item.Manifest.Capabilities...), Egress: rules,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: signed egress policy: %v", ErrInvalidRequest, err)
	}
	return &bundle, nil
}

func validateEgressNetworkBinding(manifest extensionport.Manifest) error {
	networkRules := make([]corepolicy.NetworkRule, len(manifest.NetworkPermissions))
	for index, permission := range manifest.NetworkPermissions {
		networkRules[index] = corepolicy.NetworkRule{
			Domain: permission.Domain, IP: permission.IP, CIDR: permission.CIDR,
			Ports: append([]int(nil), permission.Ports...), Protocol: permission.Protocol, Purpose: permission.Purpose,
		}
	}
	egressRules := make([]corepolicy.EgressRule, len(manifest.DataEgressPermissions))
	for index, permission := range manifest.DataEgressPermissions {
		egressRules[index] = corepolicy.EgressRule{
			Destination: permission.Destination, Purpose: permission.Purpose,
			MaxSensitivity: corepolicy.Sensitivity(permission.MaxSensitivity),
		}
	}
	if err := corepolicy.ValidateNetworkEgress(networkRules, egressRules); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return nil
}
