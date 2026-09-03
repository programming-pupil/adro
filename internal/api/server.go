// Package api exposes the versioned control-plane HTTP API.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/internal/artifact"
	"github.com/adro-project/adro/internal/audit"
	adroauth "github.com/adro-project/adro/internal/auth"
	"github.com/adro-project/adro/internal/compat"
	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/harness"
	mcpclient "github.com/adro-project/adro/internal/mcp"
	"github.com/adro-project/adro/internal/memory"
	"github.com/adro-project/adro/internal/mentions"
	"github.com/adro-project/adro/internal/orchestration"
	"github.com/adro-project/adro/internal/plugins"
	"github.com/adro-project/adro/internal/provider"
	"github.com/adro-project/adro/internal/runner"
	"github.com/adro-project/adro/internal/store"
	"github.com/adro-project/adro/internal/telemetry"
	"github.com/adro-project/adro/internal/workflow"
	"github.com/gorilla/websocket"
)

type Server struct {
	Store           *store.Memory
	Provider        provider.ExecutionProvider
	Artifacts       artifact.Store
	Events          *events.Bus
	Runners         *runner.Supervisor
	Audit           *audit.Ledger
	Harness         *harness.Store
	Plugins         *plugins.Registry
	Logger          *slog.Logger
	Router          *provider.AgentRouteResolver
	Auth            *adroauth.Service
	Orchestration   orchestration.ControlRepository
	Memory          *memory.Repository
	uploadMu        sync.Mutex
	materializeMu   sync.Mutex
	idempotencyMu   sync.Mutex
	watchMu         sync.Mutex
	triggerMu       sync.RWMutex
	recoveryMu      sync.Mutex
	recoveryStarted bool
	uploads         map[string]*upload
	watchedRuns     map[string]struct{}
	triggerOutcomes map[string][]mentions.TriggerOutcome
}

// authenticatedWorkspaceKey marks the workspace selected by the interactive
// identity at the HTTP boundary. Routes use the request header as the
// authoritative workspace for machine calls and this context value for
// browser sessions, so JSON bodies cannot move a resource across tenants.
type authenticatedWorkspaceKey struct{}
type authenticatedTenantKey struct{}
type upload struct {
	tenant, artifactID string
	version            int64
	opts               artifact.PutOptions
	parts              map[int][]byte
}

type idempotencyResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}

type idempotencyRecord struct {
	RequestSHA256 string              `json:"request_sha256"`
	Response      idempotencyResponse `json:"response"`
}

type bufferedResponseWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{header: make(http.Header)}
}
func (w *bufferedResponseWriter) Header() http.Header { return w.header }
func (w *bufferedResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *bufferedResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}
func (w *bufferedResponseWriter) response() idempotencyResponse {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	headers := make(map[string][]string, len(w.header))
	for key, values := range w.header {
		headers[key] = append([]string(nil), values...)
	}
	return idempotencyResponse{Status: w.status, Headers: headers, Body: append([]byte(nil), w.body.Bytes()...)}
}
func writeBufferedResponse(dst http.ResponseWriter, response idempotencyResponse) {
	for key, values := range response.Headers {
		if (strings.EqualFold(key, "X-Request-ID") || strings.EqualFold(key, "X-Trace-ID") || strings.EqualFold(key, telemetry.TraceParentHeader) || strings.EqualFold(key, telemetry.TraceStateHeader)) && dst.Header().Get(key) != "" {
			continue
		}
		// The buffer starts with a copy of the response headers that were
		// already installed on the real writer (CORS, cache and trace metadata).
		// Replace those values instead of appending them a second time: browsers
		// reject duplicate Access-Control-Allow-Origin values even when the two
		// values are byte-for-byte identical.
		dst.Header().Del(key)
		for _, value := range values {
			dst.Header().Add(key, value)
		}
	}
	status := response.Status
	if status == 0 {
		status = http.StatusOK
	}
	dst.WriteHeader(status)
	_, _ = dst.Write(response.Body)
}

func New(s *store.Memory, p provider.ExecutionProvider, a artifact.Store, b *events.Bus, logger *slog.Logger) *Server {
	legacyID := os.Getenv("ADRO_DEFAULT_AGENT_ID")
	return NewWithRouting(s, p, a, b, logger, provider.NewAgentRouteResolver(provider.AgentRouteConfig{}, legacyID))
}

func NewWithRouting(s *store.Memory, p provider.ExecutionProvider, a artifact.Store, b *events.Bus, logger *slog.Logger, router *provider.AgentRouteResolver) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if router == nil {
		router = provider.NewAgentRouteResolver(provider.AgentRouteConfig{}, "")
	}
	authService, err := adroauth.NewService(os.Getenv("ADRO_AUTH_STATE_FILE"), os.Getenv("ADRO_ADMIN_USERNAME"), os.Getenv("ADRO_ADMIN_PASSWORD"))
	if err != nil {
		logger.Error("load authentication state", "error", err)
		authService, _ = adroauth.NewService("", os.Getenv("ADRO_ADMIN_USERNAME"), os.Getenv("ADRO_ADMIN_PASSWORD"))
	}
	harnessStore, harnessErr := harness.New("")
	if harnessErr != nil {
		logger.Error("initialize harness store", "error", harnessErr)
		harnessStore, _ = harness.New("")
	}
	pluginRegistry, pluginErr := plugins.New("")
	if pluginErr != nil {
		logger.Error("initialize plugin registry", "error", pluginErr)
		pluginRegistry, _ = plugins.New("")
	}
	var orchestrationRepo *orchestration.MemoryRepository
	if path := strings.TrimSpace(os.Getenv("ADRO_ORCHESTRATION_STATE_FILE")); path != "" {
		if loaded, loadErr := orchestration.NewPersistentRepository(path); loadErr == nil {
			orchestrationRepo = loaded
		} else {
			logger.Error("load orchestration state", "error", loadErr, "path", path)
		}
	}
	if orchestrationRepo == nil && strings.TrimSpace(os.Getenv("ADRO_ORCHESTRATION_STATE_FILE")) == "" {
		orchestrationRepo = orchestration.NewMemoryRepository()
	}
	memoryRepo := memory.NewRepository()
	if path := strings.TrimSpace(os.Getenv("ADRO_MEMORY_STATE_FILE")); path != "" {
		if loaded, loadErr := memory.NewPersistentRepository(path); loadErr == nil {
			memoryRepo = loaded
		} else {
			logger.Error("load memory state", "error", loadErr, "path", path)
		}
	}
	return &Server{Store: s, Provider: p, Artifacts: a, Events: b, Runners: runner.NewSupervisor(), Audit: audit.NewLedger(), Harness: harnessStore, Plugins: pluginRegistry, Logger: logger, Router: router, Auth: authService, Orchestration: orchestrationRepo, Memory: memoryRepo, uploads: map[string]*upload{}, watchedRuns: map[string]struct{}{}, triggerOutcomes: map[string][]mentions.TriggerOutcome{}}
}

// NewWithRoutingAndOrchestration is the production injection seam for SQL,
// queue-backed, or other ControlRepository implementations. The default
// constructor remains the local file/memory profile for backwards compatibility.
func NewWithRoutingAndOrchestration(s *store.Memory, p provider.ExecutionProvider, a artifact.Store, b *events.Bus, logger *slog.Logger, router *provider.AgentRouteResolver, repo orchestration.ControlRepository) *Server {
	srv := NewWithRouting(s, p, a, b, logger, router)
	if repo != nil {
		srv.Orchestration = repo
	}
	return srv
}

func (s *Server) Routes() http.Handler { return http.HandlerFunc(s.ServeHTTP) }
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	traceCtx, serverSpan, traceErr := telemetry.StartRemoteSpan(r.Context(), r.Header.Get(telemetry.TraceParentHeader), r.Header.Get(telemetry.TraceStateHeader))
	r = r.WithContext(traceCtx)
	w.Header().Set(telemetry.TraceParentHeader, serverSpan.TraceParent())
	if serverSpan.TraceState != "" {
		w.Header().Set(telemetry.TraceStateHeader, serverSpan.TraceState)
	}
	w.Header().Set("X-Trace-ID", serverSpan.TraceID)
	if traceErr != nil && s.Logger != nil {
		s.Logger.Warn("ignored invalid incoming trace context", "request_path", r.URL.Path)
	}
	// Mutating store methods persist their own snapshots. Flushing after every
	// read (including CORS preflights and the long-lived WebSocket route) turns
	// a burst of dashboard reads into serialized fsyncs and can stall requests
	// for minutes on a busy or slow filesystem.
	flushState := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions &&
		r.URL.Path != "/api/v1/auth/login" && r.URL.Path != "/api/v1/auth/logout"
	// The local repository is a durable snapshot when configured. Flush after
	// each mutating request so a clean process restart cannot lose a completed
	// mutation that touched more than one component.
	defer func() {
		if !flushState {
			return
		}
		if s.Store != nil {
			if err := s.Store.Flush(); err != nil && s.Logger != nil {
				s.Logger.Error("persist control-plane state", "error", err)
			}
		}
		if s.Events != nil {
			if err := s.Events.Flush(); err != nil && s.Logger != nil {
				s.Logger.Error("persist event state", "error", err)
			}
		}
		if s.Audit != nil {
			if err := s.Audit.Flush(); err != nil && s.Logger != nil {
				s.Logger.Error("persist audit state", "error", err)
			}
		}
		if s.Runners != nil {
			if err := s.Runners.Flush(); err != nil && s.Logger != nil {
				s.Logger.Error("persist runner state", "error", err)
			}
		}
		if s.Harness != nil {
			if err := s.Harness.Flush(); err != nil && s.Logger != nil {
				s.Logger.Error("persist harness state", "error", err)
			}
		}
		if s.Plugins != nil {
			if err := s.Plugins.Flush(); err != nil && s.Logger != nil {
				s.Logger.Error("persist plugin registry", "error", err)
			}
		}
		if s.Orchestration != nil {
			if err := s.Orchestration.Flush(); err != nil && s.Logger != nil {
				s.Logger.Error("persist orchestration state", "error", err)
			}
		}
		if s.Memory != nil {
			if err := s.Memory.Flush(); err != nil && s.Logger != nil {
				s.Logger.Error("persist evidence memory", "error", err)
			}
		}
	}()
	if origin := r.Header.Get("Origin"); origin != "" && allowedOrigin(origin) {
		// The local profile is intentionally permissive for a separately served
		// static workbench. Production deployments should set an allow-list at
		// the edge gateway instead of reflecting arbitrary origins.
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, traceparent, tracestate, X-Request-ID, X-Trace-ID, X-Tenant-ID, X-Workspace-ID, X-Member-ID")
		w.Header().Set("Access-Control-Expose-Headers", "traceparent, tracestate, X-Request-ID, X-Trace-ID")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, OPTIONS")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = domain.NewID()
	}
	w.Header().Set("X-Request-ID", requestID)
	traceID := serverSpan.TraceID
	w.Header().Set("Cache-Control", "no-store")
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "" {
		path = "/"
	}
	authMode, authModeErr := configuredAuthMode()
	if authModeErr != nil {
		s.problem(w, r, http.StatusServiceUnavailable, "invalid_auth_mode", authModeErr.Error(), nil)
		return
	}
	if path == "/api/v1/auth/login" {
		s.login(w, r)
		return
	}
	var buffered *bufferedResponseWriter
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idempotencyKey) > 255 {
		s.problem(w, r, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key must be at most 255 bytes", nil)
		return
	}
	mutating := r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch
	if idempotencyKey != "" && mutating && path != "/api/v1/auth/logout" {
		s.idempotencyMu.Lock()
		defer s.idempotencyMu.Unlock()
	}
	if idempotencyKey != "" && mutating && path != "/api/v1/auth/logout" {
		fingerprint, fingerprintErr := mutationFingerprint(r)
		if fingerprintErr != nil {
			s.problem(w, r, http.StatusRequestEntityTooLarge, "idempotency_body_too_large", "idempotent mutation bodies must not exceed 24 MiB", nil)
			return
		}
		key := "http:" + tenant(r) + ":" + r.Method + ":" + path + ":" + idempotencyKey
		if prior, ok := s.Store.Idempotent(key, idempotencyRecord{}); ok {
			if record, valid := prior.(idempotencyRecord); valid {
				if subtle.ConstantTimeCompare([]byte(record.RequestSHA256), []byte(fingerprint)) != 1 {
					s.problem(w, r, http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key was already used with a different request payload or query", nil)
					return
				}
				w.Header().Set("Idempotency-Replayed", "true")
				writeBufferedResponse(w, record.Response)
				return
			}
		}
		originalWriter := w
		buffered = newBufferedResponseWriter()
		for key, values := range originalWriter.Header() {
			buffered.header[key] = append([]string(nil), values...)
		}
		w = buffered
		defer func() {
			response := buffered.response()
			if err := s.Store.RememberIdempotency(key, idempotencyRecord{RequestSHA256: fingerprint, Response: response}); err != nil {
				// A mutation whose replay record was not durably stored must not be
				// acknowledged as successful: a retry could otherwise apply it twice.
				// Keep the storage detail in server logs and return a stable problem.
				if s.Logger != nil {
					s.Logger.Error("persist idempotency record", "error", err, "request_id", requestID)
				}
				body, _ := json.Marshal(map[string]any{
					"type":       "https://adro.dev/problems/idempotency-storage",
					"title":      "Idempotency record unavailable",
					"status":     http.StatusServiceUnavailable,
					"detail":     "the mutation was not acknowledged because its replay record could not be stored",
					"request_id": requestID,
					"trace_id":   traceID,
				})
				response = idempotencyResponse{Status: http.StatusServiceUnavailable, Headers: map[string][]string{"Content-Type": {"application/problem+json"}}, Body: body}
			}
			writeBufferedResponse(originalWriter, response)
		}()
	}
	user, userAuthenticated := s.authenticateUser(r)
	machineAuthenticated := authorizedMachine(r)
	if strings.HasPrefix(path, "/api/") && path != "/api/v1/auth/me" && authMode == "required" && !userAuthenticated && !machineAuthenticated {
		s.problem(w, r, http.StatusUnauthorized, "authentication_required", "sign in with an active ADRO account", nil)
		return
	}
	if userAuthenticated {
		// Interactive identity is authoritative. Never let a browser-supplied
		// header impersonate another member or escape the user's workspace.
		identityTenant := userTenant(user)
		if requestedTenant := strings.TrimSpace(r.Header.Get("X-Tenant-ID")); requestedTenant != "" && requestedTenant != identityTenant {
			s.problem(w, r, http.StatusForbidden, "tenant_access_denied", "the requested tenant is outside your authenticated identity", nil)
			return
		}
		r.Header.Set("X-Member-ID", user.ID)
		r.Header.Set("X-Workspace-ID", user.WorkspaceID)
		r.Header.Set("X-Tenant-ID", identityTenant)
		ctx := context.WithValue(r.Context(), authenticatedWorkspaceKey{}, user.WorkspaceID)
		r = r.WithContext(context.WithValue(ctx, authenticatedTenantKey{}, identityTenant))
		if menu := menuForPath(path); menu != "" && !user.Can(menu) {
			s.problem(w, r, http.StatusForbidden, "menu_access_denied", "your account is not allowed to use this product area", map[string]any{"menu_id": menu})
			return
		}
	}
	// PostgreSQL orchestration adapters apply RLS identity inside each commit
	// transaction. Scope comes only from the authenticated/request boundary, not
	// from a mutable JSON body, and is a no-op for local repositories.
	if scoped, ok := s.Orchestration.(interface{ SetScope(string, string) }); ok {
		scoped.SetScope(tenant(r), requestWorkspace(r, r.URL.Query().Get("workspace_id")))
	}
	switch {
	case path == "/" && r.Method == http.MethodGet:
		capabilities, capabilityErr := s.Provider.Capabilities(r.Context())
		health, healthErr := s.Provider.Health(r.Context())
		providerName := capabilities.Provider
		if providerName == "" {
			providerName = "local"
		}
		response := map[string]any{"name": "ADRO", "version": "0.1.0", "api": "/api/v1", "provider": providerName, "capabilities": capabilities.Features, "provider_reachable": capabilityErr == nil && healthErr == nil && health.Healthy}
		if capabilityErr != nil {
			response["provider_error"] = string(provider.ErrorCodeOf(capabilityErr))
		} else if healthErr != nil {
			response["provider_error"] = string(provider.ErrorCodeOf(healthErr))
		}
		s.writeJSON(w, 200, response)
	case path == "/healthz" && r.Method == http.MethodGet:
		s.writeJSON(w, 200, map[string]any{"status": "ok"})
	case path == "/readyz" && r.Method == http.MethodGet:
		s.ready(w, r)
	case path == "/metrics" && r.Method == http.MethodGet:
		s.metrics(w, r)
	case path == "/api/v1/auth/me":
		s.authMe(w, r, user, userAuthenticated, machineAuthenticated)
	case path == "/api/v1/auth/logout":
		s.logout(w, r)
	case path == "/api/v1/users" || strings.HasPrefix(path, "/api/v1/users/"):
		s.userRoute(w, r, strings.TrimPrefix(path, "/api/v1/users"), user, userAuthenticated)
	case path == "/api/v1/directory":
		s.directory(w, r, user, userAuthenticated, machineAuthenticated)
	case path == "/api/v1/provider/diagnostics" && r.Method == http.MethodGet:
		s.providerDiagnostics(w, r)
	case path == "/api/v1/system/diagnostics" && r.Method == http.MethodGet:
		s.systemDiagnostics(w, r)
	case path == "/api/v1/audit" && r.Method == http.MethodGet:
		items := s.Audit.List()
		if workspaceID := requestWorkspace(r, ""); workspaceID != "" {
			filtered := items[:0]
			for _, item := range items {
				if item.WorkspaceID == workspaceID {
					filtered = append(filtered, item)
				}
			}
			items = filtered
		}
		s.writeJSON(w, 200, map[string]any{"items": items, "chain_valid": s.Audit.Verify() == nil})
	case path == "/api/v1/requirements":
		s.requirements(w, r)
	case path == "/api/v1/pipelines" || strings.HasPrefix(path, "/api/v1/pipelines/"):
		s.pipelineRoute(w, r, strings.TrimPrefix(path, "/api/v1/pipelines"))
	case path == "/api/v1/execution-plans" || path == "/api/v1/execution-plans/validate":
		s.orchestrationRoute(w, r, strings.TrimPrefix(path, "/api/v1"))
	case strings.HasPrefix(path, "/api/v1/execution-plans/"):
		s.orchestrationRoute(w, r, strings.TrimPrefix(path, "/api/v1"))
	case strings.HasPrefix(path, "/api/v1/plans/") && strings.HasSuffix(path, "/timeline"):
		s.planTimeline(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/plans/"), "/timeline"))
	case strings.HasPrefix(path, "/api/v1/runs/") && strings.HasSuffix(path, "/replay"):
		s.runReplay(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/runs/"), "/replay"))
	case strings.HasPrefix(path, "/api/v1/runs/") && strings.HasSuffix(path, "/diagnostics"):
		s.runDiagnostics(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/runs/"), "/diagnostics"))
	case strings.HasPrefix(path, "/api/v1/workspaces/") && (strings.Contains(path, "/agents") || strings.Contains(path, "/squads")):
		rest := strings.TrimPrefix(path, "/api/v1/workspaces/")
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) < 2 || (parts[1] != "agents" && parts[1] != "squads") {
			s.problem(w, r, http.StatusNotFound, "not_found", "route not found", nil)
			return
		}
		tail := ""
		if len(parts) == 3 {
			tail = parts[2]
		}
		s.orchestrationWorkspaceRoute(w, r, parts[0], parts[1], tail)
	case path == "/api/v1/agents":
		// Keep the historical provider-binding collection endpoint stable. The
		// revisioned orchestration collection lives under /workspaces/{id}/agents
		// and the singular /agents/{id} resource below.
		s.agentRoute(w, r)
	case strings.HasPrefix(path, "/api/v1/agents/") && !strings.Contains(path, "/mcp-bindings") && !strings.Contains(path, "/skill-bindings"):
		rest := strings.TrimPrefix(path, "/api/v1/agents")
		rest = strings.TrimPrefix(rest, "/")
		workspaceID := requestWorkspace(r, r.URL.Query().Get("workspace_id"))
		if workspaceID == "" {
			workspaceID = "local"
		}
		if rest == "" {
			if r.Method == http.MethodGet && s.Orchestration != nil {
				agents := s.Orchestration.ListAgents(workspaceID, orchestration.AgentStatus(r.URL.Query().Get("status")))
				if capability := strings.TrimSpace(r.URL.Query().Get("capability")); capability != "" {
					agents = filterAgentsByCapability(agents, capability)
				}
				s.writeJSON(w, http.StatusOK, map[string]any{"items": agents})
				return
			}
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		s.orchestrationAgentResource(w, r, rest, workspaceID)
	case path == "/api/v1/squads" || strings.HasPrefix(path, "/api/v1/squads/"):
		rest := strings.TrimPrefix(path, "/api/v1/squads")
		rest = strings.TrimPrefix(rest, "/")
		workspaceID := requestWorkspace(r, r.URL.Query().Get("workspace_id"))
		if workspaceID == "" {
			workspaceID = "local"
		}
		s.orchestrationWorkspaceRoute(w, r, workspaceID, "squads", rest)
	case strings.HasPrefix(path, "/api/v1/requirements/") && strings.Contains(path, "/execution-plan"):
		rest := strings.TrimPrefix(path, "/api/v1/requirements/")
		parts := strings.SplitN(rest, "/execution-plan", 2)
		tail := strings.TrimPrefix(parts[1], "/")
		s.executionPlanRequirementRoute(w, r, parts[0], tail)
	case strings.HasPrefix(path, "/api/v1/requirements/") && strings.HasSuffix(path, "/comments/trigger-preview"):
		s.mentionPreviewRoute(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/requirements/"), "/comments/trigger-preview"))
	case path == "/api/v1/workflow-templates" || strings.HasPrefix(path, "/api/v1/workflow-templates/"):
		s.workflowTemplateRoute(w, r, strings.TrimPrefix(path, "/api/v1/workflow-templates"))
	case path == "/api/v1/chats" || strings.HasPrefix(path, "/api/v1/chats/"):
		s.chatRoute(w, r, strings.TrimPrefix(path, "/api/v1/chats"))
	case path == "/api/v1/sessions" || strings.HasPrefix(path, "/api/v1/sessions/"):
		s.sessionRoute(w, r, strings.TrimPrefix(path, "/api/v1/sessions"))
	case strings.HasPrefix(path, "/api/v1/comments/") && strings.HasSuffix(path, "/follow-up"):
		s.commentFollowUpRoute(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/comments/"), "/follow-up"))
	case strings.HasPrefix(path, "/api/v1/comments/") && strings.HasSuffix(path, "/revisions"):
		s.commentRevisionsRoute(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/comments/"), "/revisions"))
	case strings.HasPrefix(path, "/api/v1/comments/") && r.Method == http.MethodPatch:
		s.commentEditRoute(w, r, strings.TrimPrefix(path, "/api/v1/comments/"))
	case strings.HasPrefix(path, "/api/v1/comments/") && strings.HasSuffix(path, "/trigger-outcomes"):
		s.commentTriggerOutcomesRoute(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/comments/"), "/trigger-outcomes"))
	case strings.HasPrefix(path, "/api/v1/comments/") && strings.HasSuffix(path, "/trigger-retry"):
		s.commentTriggerRetryRoute(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/comments/"), "/trigger-retry"))
	case path == "/api/v1/plugins" || strings.HasPrefix(path, "/api/v1/plugins/"):
		s.pluginRoute(w, r, strings.TrimPrefix(path, "/api/v1/plugins"))
	case strings.HasPrefix(path, "/api/v1/requirements/"):
		s.requirement(w, r, strings.TrimPrefix(path, "/api/v1/requirements/"))
	case path == "/api/v1/bugs":
		s.bugs(w, r)
	case strings.HasPrefix(path, "/api/v1/bugs/"):
		s.bug(w, r, strings.TrimPrefix(path, "/api/v1/bugs/"))
	case path == "/api/v1/repositories" || strings.HasPrefix(path, "/api/v1/repositories/"):
		s.repositoryRoute(w, r, strings.TrimPrefix(path, "/api/v1/repositories"))
	case path == "/api/v1/team-workspaces" || strings.HasPrefix(path, "/api/v1/team-workspaces/"):
		s.teamWorkspaceRoute(w, r, strings.TrimPrefix(path, "/api/v1/team-workspaces"))
	case path == "/api/v1/mcp" || strings.HasPrefix(path, "/api/v1/mcp/"):
		s.mcpRoute(w, r, strings.TrimPrefix(path, "/api/v1/mcp"))
	case path == "/api/v1/skills" || strings.HasPrefix(path, "/api/v1/skills/"):
		s.skillRoute(w, r, strings.TrimPrefix(path, "/api/v1/skills"))
	case path == "/api/v1/automations" || strings.HasPrefix(path, "/api/v1/automations/"):
		s.automationRoute(w, r, strings.TrimPrefix(path, "/api/v1/automations"))
	case strings.HasPrefix(path, "/api/v1/automation-runs/"):
		s.automationRunRoute(w, r, strings.TrimPrefix(path, "/api/v1/automation-runs/"))
	case path == "/api/v1/developer-profiles" || strings.HasPrefix(path, "/api/v1/developer-profiles/"):
		s.profileRoute(w, r, strings.TrimPrefix(path, "/api/v1/developer-profiles"))
	case path == "/api/v1/approvals" || strings.HasPrefix(path, "/api/v1/approvals/"):
		s.approvalRoute(w, r, strings.TrimPrefix(path, "/api/v1/approvals"))
	case path == "/api/v1/evidence" || strings.HasPrefix(path, "/api/v1/evidence/"):
		s.evidenceRoute(w, r, strings.TrimPrefix(path, "/api/v1/evidence"))
	case path == "/api/v1/artifacts/uploads":
		s.createUpload(w, r)
	case path == "/api/v1/attachments":
		s.attachmentRoute(w, r, user, userAuthenticated, machineAuthenticated)
	case path == "/api/v1/screenshots" && r.Method == http.MethodPost:
		s.createScreenshot(w, r)
	case path == "/api/v1/artifact-migrations" || strings.HasPrefix(path, "/api/v1/artifact-migrations/"):
		s.artifactMigrationRoute(w, r, strings.TrimPrefix(path, "/api/v1/artifact-migrations"))
	case strings.HasPrefix(path, "/api/v1/artifacts/uploads/"):
		s.uploadRoute(w, r, strings.TrimPrefix(path, "/api/v1/artifacts/uploads/"))
	case strings.HasPrefix(path, "/api/v1/artifacts/"):
		s.artifactRoute(w, r, strings.TrimPrefix(path, "/api/v1/artifacts/"))
	case strings.HasPrefix(path, "/api/v1/runs/"):
		s.runRoute(w, r, strings.TrimPrefix(path, "/api/v1/runs/"))
	case path == "/api/v1/work-items" && r.Method == http.MethodGet:
		s.listWorkItems(w, r)
	case strings.HasPrefix(path, "/api/v1/work-items/"):
		s.workItemRoute(w, r, strings.TrimPrefix(path, "/api/v1/work-items/"))
	case path == "/api/v1/repository-graph" && r.Method == http.MethodGet:
		s.repositoryGraph(w, r)
	case strings.HasPrefix(path, "/api/v1/agents/"):
		s.agentBindingRoute(w, r, strings.TrimPrefix(path, "/api/v1/agents/"))
	case path == "/api/v1/runners" || strings.HasPrefix(path, "/api/v1/runners/"):
		s.runnerRoute(w, r, strings.TrimPrefix(path, "/api/v1/runners"))
	case strings.HasPrefix(path, "/api/v1/streams/workspaces/"):
		s.streamRoute(w, r, strings.TrimPrefix(path, "/api/v1/streams/workspaces/"))
	default:
		s.problem(w, r, http.StatusNotFound, "not_found", "route not found", nil)
	}
}

func (s *Server) agentRoute(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.Header.Get("X-Workspace-ID"))
	if r.Method == http.MethodGet {
		profiles := s.Store.ListDeveloperProfiles(workspaceID)
		s.writeJSON(w, http.StatusOK, map[string]any{"items": profiles})
		return
	}
	var input struct {
		WorkspaceID  string `json:"workspace_id"`
		MemberID     string `json:"member_id"`
		Name         string `json:"name"`
		Instructions string `json:"instructions,omitempty"`
		Role         string `json:"role,omitempty"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	input.WorkspaceID = requestWorkspace(r, input.WorkspaceID)
	if strings.TrimSpace(input.MemberID) == "" || strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.WorkspaceID) == "" {
		s.problem(w, r, http.StatusUnprocessableEntity, "validation_error", "workspace_id, member_id and name are required", nil)
		return
	}
	created, err := s.Provider.EnsureAgent(r.Context(), provider.AgentSpec{WorkspaceID: input.WorkspaceID, Name: strings.TrimSpace(input.Name), Instructions: strings.TrimSpace(input.Instructions)})
	if err != nil {
		s.problem(w, r, http.StatusBadGateway, "provider_agent_create_failed", providerSafeError(err), nil)
		return
	}
	nativeID := strings.TrimSpace(created.ProviderAgentID)
	if nativeID == "" {
		nativeID = strings.TrimSpace(created.ID)
	}
	if nativeID == "" {
		s.problem(w, r, http.StatusBadGateway, "provider_agent_create_failed", "provider returned no agent identity", nil)
		return
	}
	providerName := "provider"
	providerName = "local"
	binding := provider.NewProviderBinding(providerName, input.WorkspaceID, "agent", nativeID, "configured", "webui", "ui")
	if _, err := s.Store.SaveProviderBinding(binding); err != nil {
		s.problem(w, r, http.StatusInternalServerError, "binding_persist_failed", err.Error(), nil)
		return
	}
	profile, err := s.Store.UpsertDeveloperProfile(domain.DeveloperProfile{WorkspaceID: input.WorkspaceID, MemberID: strings.TrimSpace(input.MemberID), DefaultAgentBindingID: binding.ID, DefaultRole: strings.TrimSpace(input.Role), Status: "active"})
	if err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "validation_error", err.Error(), nil)
		return
	}
	s.recordAudit(r, profile.WorkspaceID, "agent.created", profile.ID, map[string]any{"member_id": profile.MemberID, "role": profile.DefaultRole, "provider": providerName})
	s.writeJSON(w, http.StatusCreated, map[string]any{"profile": profile, "binding": map[string]any{"id": binding.ID, "provider": binding.Provider, "status": binding.Status}})
}

func (s *Server) listWorkItems(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.Header.Get("X-Workspace-ID"))
	items := s.Store.ListWorkItems("")
	if workspaceID != "" {
		filtered := items[:0]
		for _, item := range items {
			requirement, err := s.Store.GetRequirement(item.RequirementID)
			if err == nil && requirement.WorkspaceID == workspaceID {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	authMode, authModeErr := configuredAuthMode()
	if authModeErr != nil {
		s.problem(w, r, http.StatusServiceUnavailable, "invalid_auth_mode", authModeErr.Error(), nil)
		return
	}
	if authMode == "required" && localAuthBackend() && (s.Auth == nil || len(s.Auth.ListUsers("")) == 0) {
		s.problem(w, r, http.StatusServiceUnavailable, "auth_not_configured", "set ADRO_ADMIN_PASSWORD or provide a persisted local auth state before enabling required authentication", nil)
		return
	}
	if err := s.Artifacts.Health(ctx); err != nil {
		s.problem(w, r, 503, "artifact_store_unavailable", "artifact store is not ready", nil)
		return
	}
	capabilities, capabilitiesErr := s.Provider.Capabilities(ctx)
	if capabilitiesErr != nil {
		s.problem(w, r, http.StatusServiceUnavailable, "provider_unavailable", "provider capabilities are unavailable", map[string]any{"error_code": provider.ErrorCodeOf(capabilitiesErr)})
		return
	}
	health, err := s.Provider.Health(ctx)
	if err != nil || !health.Healthy {
		if health.Message == "" {
			health.Message = "provider health check failed"
		}
		if err != nil {
			health.Message = string(provider.ErrorCodeOf(err))
		}
		s.problem(w, r, 503, "provider_unavailable", health.Message, nil)
		return
	}
	s.writeJSON(w, 200, map[string]any{"status": "ready", "provider": health, "capabilities": capabilities})
}

// providerDiagnostics is a read-only, secret-free probe for the WebUI and
// operators. It reports the selected execution boundary and its capabilities.
func (s *Server) providerDiagnostics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	caps, capsErr := s.Provider.Capabilities(ctx)
	health, healthErr := s.Provider.Health(ctx)
	providerName := caps.Provider
	if providerName == "" {
		providerName = "local"
	}
	workspaceID := strings.TrimSpace(r.Header.Get("X-Workspace-ID"))
	routeDiagnostics := provider.RouteDiagnostics{}
	if s.Router != nil && workspaceID != "" {
		routeDiagnostics = s.Router.Diagnostics(workspaceID)
	}
	reachable := capsErr == nil && healthErr == nil
	authenticationState := "not_required"
	configurationState := "configured"
	if capsErr != nil || healthErr != nil {
		configurationState = "unconfigured"
	}
	routingState := "unknown"
	if workspaceID != "" {
		routingState = "unconfigured"
	}
	if workspaceID != "" && routeDiagnostics.Configured {
		routingState = "configured_unverified"
	}
	errorCodes := make([]string, 0, 2)
	for _, err := range []error{capsErr, healthErr} {
		if err == nil {
			continue
		}
		code := string(provider.ErrorCodeOf(err))
		if !containsString(errorCodes, code) {
			errorCodes = append(errorCodes, code)
		}
	}
	result := map[string]any{
		"provider":                      providerName,
		"adapter_version":               caps.AdapterVersion,
		"server_version":                caps.ServerVersion,
		"capabilities":                  caps.Features,
		"capabilities_reachable":        capsErr == nil,
		"health_reachable":              healthErr == nil,
		"healthy":                       reachable && health.Healthy,
		"configuration_state":           configurationState,
		"reachability_state":            map[bool]string{true: "reachable", false: "unreachable"}[reachable],
		"authentication_state":          authenticationState,
		"routing_state":                 routingState,
		"executor_configured":           configurationState == "configured",
		"verified_binding_count":        0,
		"error_codes":                   errorCodes,
		"attachment_delivery_supported": false,
		"checked_at":                    time.Now().UTC(),
	}
	if workspaceID != "" {
		result["default_agent_configured"] = routeDiagnostics.DefaultAgentConfigured
		result["member_route_count"] = routeDiagnostics.MemberRouteCount
		result["role_route_count"] = routeDiagnostics.RoleRouteCount
	}
	if health.Message != "" {
		result["health_message"] = health.Message
	}
	if capsErr != nil {
		result["capabilities_error"] = string(provider.ErrorCodeOf(capsErr))
	}
	if healthErr != nil {
		result["health_error"] = string(provider.ErrorCodeOf(healthErr))
	}
	if _, ok := s.Provider.(provider.AttachmentPublisher); ok {
		result["attachment_delivery_supported"] = caps.Supports("attachment.v1")
	}
	s.writeJSON(w, http.StatusOK, result)
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	requirements, _ := s.Store.ListRequirements("", "", "", 250)
	bugs := s.Store.ListBugs("", "")
	runners := s.Runners.List()
	eventsPage, _ := s.Events.List("", "", 250)
	counts := map[domain.RequirementStatus]int{}
	for _, req := range requirements {
		counts[req.Status]++
	}
	openBugs := 0
	for _, bug := range bugs {
		if bug.Status == domain.BugOpen || bug.Status == domain.BugRepairing {
			openBugs++
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# HELP adro_requirements_total Requirements retained by the control plane.\n# TYPE adro_requirements_total gauge\nadro_requirements_total %d\n", len(requirements))
	for status, count := range counts {
		fmt.Fprintf(w, "adro_requirements_by_status{status=%q} %d\n", status, count)
	}
	fmt.Fprintf(w, "# HELP adro_open_bugs_total Open or repairing bugs.\n# TYPE adro_open_bugs_total gauge\nadro_open_bugs_total %d\n", openBugs)
	fmt.Fprintf(w, "# HELP adro_runners_total Registered runners.\n# TYPE adro_runners_total gauge\nadro_runners_total %d\n", len(runners))
	fmt.Fprintf(w, "# HELP adro_events_total Events retained for replay.\n# TYPE adro_events_total gauge\nadro_events_total %d\n", len(eventsPage))
	ready, running, waiting, blocked, feedback, retries := 0, 0, 0, 0, 0, 0
	transitionCount, loopExhausted, contextOverflow, toolDenials, leaseConflicts := 0, 0, 0, 0, 0
	coalesced, triggerBlocked, eventGaps := 0, 0, 0
	var transitionSeconds float64
	var tokenUsage, costCents int64
	toolCalls := 0
	if s.Orchestration != nil {
		for _, plan := range s.Orchestration.ListPlans("") {
			projection, err := s.Orchestration.GetProjection(plan.ID)
			if err != nil {
				continue
			}
			for _, node := range projection.Nodes {
				switch node.Status {
				case orchestration.AttemptReady:
					ready++
				case orchestration.AttemptRunning:
					running++
				case orchestration.AttemptWaiting:
					waiting++
				}
				if node.RetryCount > 0 {
					retries += node.RetryCount
				}
			}
			for _, attempt := range projection.Attempts {
				if attempt.StartedAt != nil && attempt.FinishedAt != nil && !attempt.FinishedAt.Before(*attempt.StartedAt) {
					transitionCount++
					transitionSeconds += attempt.FinishedAt.Sub(*attempt.StartedAt).Seconds()
				}
				if attempt.FailureReason == nil {
					continue
				}
				code := strings.ToLower(attempt.FailureReason.Code)
				if !attempt.FailureReason.Retryable {
					blocked++
				}
				if strings.Contains(code, "loop") {
					loopExhausted++
				}
				if strings.Contains(code, "context") && strings.Contains(code, "overflow") {
					contextOverflow++
				}
				if strings.Contains(code, "denial") || strings.Contains(code, "denied") {
					toolDenials++
				}
				if strings.Contains(code, "lease") || strings.Contains(code, "fencing") || strings.Contains(code, "stale_attempt") {
					leaseConflicts++
				}
			}
			feedback += len(projection.Decisions)
			tokenUsage += projection.TokenUsage
			toolCalls += projection.ToolCalls
			costCents += projection.CostCents
		}
	}
	comments, cursor := s.Store.ListComments("", "", "", "", 250)
	for {
		for _, comment := range comments {
			for _, outcome := range comment.TriggerOutcomes {
				switch outcome.Status {
				case string(mentions.StatusCoalesced):
					coalesced++
				case string(mentions.StatusBlocked):
					triggerBlocked++
				}
			}
		}
		if cursor == "" {
			break
		}
		comments, cursor = s.Store.ListComments("", "", "", cursor, 250)
	}
	for _, event := range eventsPage {
		if event.EventType == "stream.gap.v1" {
			eventGaps++
		}
	}
	fmt.Fprintf(w, "# HELP adro_orchestration_nodes_total Nodes by scheduler state.\n# TYPE adro_orchestration_nodes_total gauge\nadro_orchestration_nodes_total{state=\"ready\"} %d\nadro_orchestration_nodes_total{state=\"running\"} %d\nadro_orchestration_nodes_total{state=\"waiting\"} %d\nadro_orchestration_nodes_total{state=\"blocked\"} %d\n", ready, running, waiting, blocked)
	fmt.Fprintf(w, "# HELP adro_orchestration_feedback_total Feedback edge decisions.\n# TYPE adro_orchestration_feedback_total counter\nadro_orchestration_feedback_total %d\n", feedback)
	fmt.Fprintf(w, "# HELP adro_orchestration_retry_total Node retry requests.\n# TYPE adro_orchestration_retry_total counter\nadro_orchestration_retry_total %d\n", retries)
	fmt.Fprintf(w, "# HELP adro_orchestration_transition_latency_seconds Total and count of completed attempt latency.\n# TYPE adro_orchestration_transition_latency_seconds summary\nadro_orchestration_transition_latency_seconds_sum %g\nadro_orchestration_transition_latency_seconds_count %d\n", transitionSeconds, transitionCount)
	fmt.Fprintf(w, "# HELP adro_orchestration_failures_total Bounded orchestration failure signals.\n# TYPE adro_orchestration_failures_total counter\nadro_orchestration_failures_total{reason=\"loop_exhausted\"} %d\nadro_orchestration_failures_total{reason=\"context_overflow\"} %d\nadro_orchestration_failures_total{reason=\"tool_denial\"} %d\nadro_orchestration_failures_total{reason=\"lease_conflict\"} %d\n", loopExhausted, contextOverflow, toolDenials, leaseConflicts)
	fmt.Fprintf(w, "# HELP adro_comment_triggers_total Structured comment trigger outcomes.\n# TYPE adro_comment_triggers_total counter\nadro_comment_triggers_total{status=\"coalesced\"} %d\nadro_comment_triggers_total{status=\"blocked\"} %d\n", coalesced, triggerBlocked)
	fmt.Fprintf(w, "# HELP adro_event_gaps_total Stream gaps surfaced to consumers.\n# TYPE adro_event_gaps_total counter\nadro_event_gaps_total %d\n", eventGaps)
	fmt.Fprintf(w, "# HELP adro_orchestration_usage_total Aggregated plan usage by unit.\n# TYPE adro_orchestration_usage_total counter\nadro_orchestration_usage_total{unit=\"tokens\"} %d\nadro_orchestration_usage_total{unit=\"tool_calls\"} %d\nadro_orchestration_usage_total{unit=\"cost_cents\"} %d\n", tokenUsage, toolCalls, costCents)
}

func (s *Server) requirements(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		items, next := s.Store.ListRequirements(r.Header.Get("X-Workspace-ID"), r.URL.Query().Get("status"), r.URL.Query().Get("cursor"), queryInt(r, "limit", 50))
		s.writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
		return
	}
	if r.Method != http.MethodPost {
		s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	var in struct {
		WorkspaceID        string   `json:"workspace_id"`
		Title              string   `json:"title"`
		Description        string   `json:"description"`
		AcceptanceCriteria []string `json:"acceptance_criteria"`
		Priority           string   `json:"priority"`
		CreatedBy          string   `json:"created_by"`
		AssigneeMemberIDs  []string `json:"assignee_member_ids"`
		RepositoryIDs      []string `json:"repository_ids"`
		WorkflowTemplateID string   `json:"workflow_template_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		s.problem(w, r, 400, "invalid_json", err.Error(), nil)
		return
	}
	if workspaceID := r.Header.Get("X-Workspace-ID"); workspaceID != "" {
		in.WorkspaceID = workspaceID
	}
	if memberID := r.Header.Get("X-Member-ID"); memberID != "" {
		in.CreatedBy = memberID
	}
	if err := s.validateRequirementRepositoryRelations(in.WorkspaceID, in.RepositoryIDs); err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "invalid_repository_relation", err.Error(), nil)
		return
	}
	req := domain.Requirement{WorkspaceID: in.WorkspaceID, Title: in.Title, Description: in.Description, AcceptanceCriteria: in.AcceptanceCriteria, Priority: in.Priority, CreatedBy: in.CreatedBy, AssigneeMemberIDs: in.AssigneeMemberIDs, RepositoryIDs: in.RepositoryIDs, WorkflowTemplateID: in.WorkflowTemplateID}
	created, err := s.Store.CreateRequirement(req)
	if err != nil {
		s.problem(w, r, 422, "validation_error", err.Error(), nil)
		return
	}
	s.recordAudit(r, created.WorkspaceID, "requirement.created", created.ID, map[string]any{"key": created.Key})
	_ = s.Events.Publish(r.Context(), events.NewWithContext(r.Context(), "requirement.created.v1", "requirement", created.ID, tenant(r), created.WorkspaceID, created.Version, map[string]any{"key": created.Key, "title": created.Title}))
	s.writeJSON(w, 201, created)
}

func (s *Server) requirement(w http.ResponseWriter, r *http.Request, id string) {
	parts := strings.Split(id, "/")
	id = parts[0]
	req, err := s.Store.GetRequirement(id)
	if err != nil {
		s.problem(w, r, 404, "not_found", "requirement not found", nil)
		return
	}
	if workspaceID := r.Header.Get("X-Workspace-ID"); workspaceID != "" && req.WorkspaceID != workspaceID {
		s.problem(w, r, 404, "not_found", "requirement not found", nil)
		return
	}
	if len(parts) > 1 {
		switch parts[1] {
		case "start":
			if r.Method != http.MethodPost {
				s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
				return
			}
			updated, e := s.Store.TransitionRequirement(id, domain.RequirementTriaged, 0)
			if e != nil && updated.ID == "" {
				s.problem(w, r, 409, "invalid_transition", e.Error(), nil)
				return
			}
			if e == nil {
				_ = s.Events.Publish(r.Context(), events.NewWithContext(r.Context(), "requirement.status.changed.v1", "requirement", id, tenant(r), req.WorkspaceID, updated.Version, map[string]any{"from": req.Status, "to": updated.Status}))
				if err := s.materializeWorkItems(r.Context(), updated); err != nil {
					s.problem(w, r, 502, "work_item_creation_failed", providerSafeError(err), nil)
					return
				}
			}
			s.writeJSON(w, 200, updated)
			return
		case "approve":
			if r.Method != http.MethodPost {
				s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
				return
			}
			updated, e := s.Store.TransitionRequirement(id, domain.RequirementDeveloping, 0)
			if e != nil {
				s.problem(w, r, 409, "invalid_transition", e.Error(), nil)
				return
			}
			s.writeJSON(w, 200, updated)
			return
		case "pause":
			if r.Method != http.MethodPost {
				s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
				return
			}
			updated, e := s.Store.TransitionRequirement(id, domain.RequirementHumanApprovalNeeded, 0)
			if e != nil {
				s.problem(w, r, 409, "invalid_transition", e.Error(), nil)
				return
			}
			s.writeJSON(w, 200, updated)
			return
		case "resume":
			if r.Method != http.MethodPost {
				s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
				return
			}
			updated, e := s.Store.TransitionRequirement(id, domain.RequirementDeveloping, 0)
			if e != nil {
				s.problem(w, r, 409, "invalid_transition", e.Error(), nil)
				return
			}
			s.writeJSON(w, 200, updated)
			return
		case "transition":
			if r.Method != http.MethodPost {
				s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
				return
			}
			var input struct {
				Status  domain.RequirementStatus `json:"status"`
				Version int64                    `json:"version"`
				Reason  string                   `json:"reason"`
			}
			if err := decodeJSON(r, &input); err != nil {
				s.problem(w, r, 400, "invalid_json", err.Error(), nil)
				return
			}
			updated, e := s.Store.TransitionRequirement(id, input.Status, input.Version)
			if e != nil {
				code := 409
				if errors.Is(e, store.ErrNotFound) {
					code = 404
				}
				s.problem(w, r, code, "invalid_transition", e.Error(), nil)
				return
			}
			s.recordAudit(r, updated.WorkspaceID, "requirement.status.changed", id, map[string]any{"from": req.Status, "to": updated.Status, "reason": input.Reason})
			_ = s.Events.Publish(r.Context(), events.NewWithContext(r.Context(), "requirement.status.changed.v1", "requirement", id, tenant(r), updated.WorkspaceID, updated.Version, map[string]any{"from": req.Status, "to": updated.Status, "reason": input.Reason}))
			s.writeJSON(w, 200, updated)
			return
		case "confirm-assignees":
			if r.Method != http.MethodPost {
				s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
				return
			}
			updated, e := s.Store.TransitionRequirement(id, domain.RequirementAssigneesConfirmed, parseIfMatch(r.Header.Get("If-Match")))
			if e != nil {
				s.problem(w, r, 409, "invalid_transition", e.Error(), nil)
				return
			}
			s.writeJSON(w, 200, updated)
			return
		case "begin-design":
			if r.Method != http.MethodPost {
				s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
				return
			}
			updated, e := s.Store.TransitionRequirement(id, domain.RequirementDesigning, parseIfMatch(r.Header.Get("If-Match")))
			if e != nil {
				s.problem(w, r, 409, "invalid_transition", e.Error(), nil)
				return
			}
			s.writeJSON(w, 200, updated)
			return
		case "gates":
			if r.Method != http.MethodPost {
				s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
				return
			}
			s.applyGate(w, r, req)
			return
		case "work-items":
			if r.Method == http.MethodGet {
				s.writeJSON(w, 200, map[string]any{"items": s.Store.ListWorkItems(id)})
				return
			}
		case "comments":
			if len(parts) == 2 {
				s.commentRoute(w, r, "requirement", req.ID, req.WorkspaceID)
				return
			}
		case "assignees", "repositories":
			if r.Method == http.MethodPost {
				s.addRelation(w, r, req, parts[1])
				return
			}
		case "impact-reports":
			if len(parts) == 2 && r.Method == http.MethodPost {
				s.createImpactReport(w, r, req)
				return
			}
			if len(parts) == 4 && parts[3] == "confirm" && r.Method == http.MethodPost {
				version, parseErr := strconv.ParseInt(parts[2], 10, 64)
				if parseErr != nil {
					s.problem(w, r, 400, "invalid_version", "impact report version must be an integer", nil)
					return
				}
				var input struct {
					RepositoryIDs []string `json:"repository_ids"`
				}
				if err := decodeJSON(r, &input); err != nil {
					s.problem(w, r, 400, "invalid_json", err.Error(), nil)
					return
				}
				report, err := s.Store.ConfirmImpactReport(req.ID, version, input.RepositoryIDs)
				if err != nil {
					code := 409
					if errors.Is(err, store.ErrNotFound) {
						code = 404
					}
					s.problem(w, r, code, "impact_confirmation_failed", err.Error(), nil)
					return
				}
				s.writeJSON(w, 200, report)
				return
			}
		}
	}
	if r.Method == http.MethodGet {
		comments, _ := s.Store.ListComments(req.WorkspaceID, "requirement", req.ID, "", 250)
		s.writeJSON(w, 200, map[string]any{"requirement": req, "work_items": s.Store.ListWorkItems(id), "events": s.eventList(id, r), "comments": comments, "attachments": s.Store.ListAttachments(req.WorkspaceID, "requirement", req.ID)})
		return
	}
	if r.Method != http.MethodPatch {
		s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	var patch struct {
		Title              *string                   `json:"title"`
		Description        *string                   `json:"description"`
		Priority           *string                   `json:"priority"`
		Status             *domain.RequirementStatus `json:"status"`
		AcceptanceCriteria []string                  `json:"acceptance_criteria"`
		AssigneeMemberIDs  []string                  `json:"assignee_member_ids"`
		RepositoryIDs      []string                  `json:"repository_ids"`
		Version            int64                     `json:"version"`
	}
	if err := decodeJSON(r, &patch); err != nil {
		s.problem(w, r, 400, "invalid_json", err.Error(), nil)
		return
	}
	if patch.Title != nil {
		req.Title = *patch.Title
	}
	if patch.Description != nil {
		req.Description = *patch.Description
	}
	if patch.Priority != nil {
		req.Priority = *patch.Priority
	}
	if patch.Status != nil {
		req.Status = *patch.Status
	}
	if patch.AcceptanceCriteria != nil {
		req.AcceptanceCriteria = patch.AcceptanceCriteria
	}
	if patch.AssigneeMemberIDs != nil {
		req.AssigneeMemberIDs = patch.AssigneeMemberIDs
	}
	if patch.RepositoryIDs != nil {
		req.RepositoryIDs = patch.RepositoryIDs
	}
	if err := s.validateRequirementRepositoryRelations(req.WorkspaceID, req.RepositoryIDs); err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "invalid_repository_relation", err.Error(), nil)
		return
	}
	expected := patch.Version
	if expected == 0 {
		expected = parseIfMatch(r.Header.Get("If-Match"))
	}
	updated, e := s.Store.UpdateRequirement(req, expected)
	if e != nil {
		code := 409
		if errors.Is(e, store.ErrNotFound) {
			code = 404
		}
		s.problem(w, r, code, "update_failed", e.Error(), nil)
		return
	}
	s.writeJSON(w, 200, updated)
}

func (s *Server) addRelation(w http.ResponseWriter, r *http.Request, req domain.Requirement, kind string) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if err := decodeJSON(r, &in); err != nil {
		s.problem(w, r, 400, "invalid_json", err.Error(), nil)
		return
	}
	if kind == "assignees" {
		req.AssigneeMemberIDs = appendUnique(req.AssigneeMemberIDs, in.IDs...)
	} else {
		req.RepositoryIDs = appendUnique(req.RepositoryIDs, in.IDs...)
		if err := s.validateRequirementRepositoryRelations(req.WorkspaceID, req.RepositoryIDs); err != nil {
			s.problem(w, r, http.StatusUnprocessableEntity, "invalid_repository_relation", err.Error(), nil)
			return
		}
	}
	updated, e := s.Store.UpdateRequirement(req, req.Version)
	if e != nil {
		s.problem(w, r, 409, "update_failed", e.Error(), nil)
		return
	}
	s.writeJSON(w, 200, updated)
}

func (s *Server) createImpactReport(w http.ResponseWriter, r *http.Request, req domain.Requirement) {
	var input struct {
		InputSnapshot map[string]any           `json:"input_snapshot"`
		Candidates    []domain.ImpactCandidate `json:"candidate_repositories"`
		Risks         []string                 `json:"unresolved_risks"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, 400, "invalid_json", err.Error(), nil)
		return
	}
	if len(input.Candidates) == 0 {
		for _, repositoryID := range req.RepositoryIDs {
			input.Candidates = append(input.Candidates, domain.ImpactCandidate{RepositoryID: repositoryID, Relation: "explicit", Confidence: 1, RecommendedAction: domain.ImpactMustChange, EvidenceRefs: []string{"user:explicit"}})
		}
	}
	report, err := s.Store.CreateImpactReport(domain.ImpactReport{RequirementID: req.ID, InputSnapshot: input.InputSnapshot, Candidates: input.Candidates, UnresolvedRisks: input.Risks})
	if err != nil {
		s.problem(w, r, 422, "validation_error", err.Error(), nil)
		return
	}
	_ = s.Events.Publish(r.Context(), events.NewWithContext(r.Context(), "impact.report.generated.v1", "impact_report", report.ID, tenant(r), req.WorkspaceID, report.Version, map[string]any{"requirement_id": req.ID, "version": report.Version}))
	s.writeJSON(w, 201, report)
}

func (s *Server) applyGate(w http.ResponseWriter, r *http.Request, req domain.Requirement) {
	var input struct {
		Name           string      `json:"gate"`
		Decision       string      `json:"decision"`
		RepairAttempts int         `json:"repair_attempts"`
		Version        int64       `json:"version"`
		EvidenceIDs    []string    `json:"evidence_ids"`
		Reason         string      `json:"reason"`
		Bug            *domain.Bug `json:"bug,omitempty"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, 400, "invalid_json", err.Error(), nil)
		return
	}
	to, err := workflow.NewEngine(3).ApplyGate(req.Status, workflow.GateInput{Name: input.Name, Decision: input.Decision, RepairAttempts: input.RepairAttempts})
	if err != nil {
		s.problem(w, r, 409, "gate_rejected", err.Error(), nil)
		return
	}
	updated, err := s.Store.TransitionRequirement(req.ID, to, input.Version)
	if err != nil {
		code := 409
		if errors.Is(err, store.ErrNotFound) {
			code = 404
		}
		s.problem(w, r, code, "gate_transition_failed", err.Error(), nil)
		return
	}
	result := domain.GateResult{Gate: input.Name, Decision: input.Decision, EvidenceIDs: append([]string(nil), input.EvidenceIDs...), Checks: []domain.GateCheck{{Name: "status", Actual: req.Status, Expected: to}}}
	var bug domain.Bug
	if input.Decision == "fail" && input.Bug != nil {
		bug = *input.Bug
		bug.RequirementID = req.ID
		bug.WorkspaceID = req.WorkspaceID
		if bug.Fingerprint == "" {
			bug.Fingerprint = fingerprint(bug)
		}
		bug, _, err = s.Store.UpsertBug(bug)
		if err != nil {
			s.problem(w, r, 422, "bug_creation_failed", err.Error(), nil)
			return
		}
		result.Checks = append(result.Checks, domain.GateCheck{Name: "bug_id", Actual: bug.ID, Expected: "unique"})
	}
	s.recordAudit(r, updated.WorkspaceID, "requirement.gate.applied", req.ID, map[string]any{"gate": input.Name, "decision": input.Decision, "from": req.Status, "to": to, "evidence_ids": input.EvidenceIDs, "reason": input.Reason})
	_ = s.Events.Publish(r.Context(), events.NewWithContext(r.Context(), "workflow.gate.completed.v1", "requirement", req.ID, tenant(r), req.WorkspaceID, updated.Version, map[string]any{"gate": input.Name, "decision": input.Decision, "status": to, "evidence_ids": input.EvidenceIDs, "bug_id": bug.ID}))
	s.writeJSON(w, 200, map[string]any{"requirement": updated, "gate_result": result, "bug": bug})
}

func (s *Server) bugs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.writeJSON(w, 200, map[string]any{"items": s.Store.ListBugs(r.Header.Get("X-Workspace-ID"), r.URL.Query().Get("status"))})
		return
	}
	if r.Method != http.MethodPost {
		s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	var in domain.Bug
	if err := decodeJSON(r, &in); err != nil {
		s.problem(w, r, 400, "invalid_json", err.Error(), nil)
		return
	}
	if workspaceID := r.Header.Get("X-Workspace-ID"); workspaceID != "" {
		in.WorkspaceID = workspaceID
	}
	if in.Fingerprint == "" {
		in.Fingerprint = fingerprint(in)
	}
	if in.RequirementID != "" {
		requirement, err := s.Store.GetRequirement(in.RequirementID)
		if err != nil || (in.WorkspaceID != "" && requirement.WorkspaceID != in.WorkspaceID) {
			s.problem(w, r, http.StatusUnprocessableEntity, "invalid_requirement_relation", "related requirement does not exist in this workspace", nil)
			return
		}
	}
	b, duplicate, e := s.Store.UpsertBug(in)
	if e != nil {
		s.problem(w, r, 422, "validation_error", e.Error(), nil)
		return
	}
	if !duplicate {
		s.recordAudit(r, b.WorkspaceID, "bug.detected", b.ID, map[string]any{"fingerprint": b.Fingerprint})
	}
	status := 201
	if duplicate {
		status = 200
	} else {
		_ = s.Events.Publish(r.Context(), events.NewWithContext(r.Context(), "bug.detected.v1", "bug", b.ID, tenant(r), b.WorkspaceID, 1, map[string]any{"fingerprint": b.Fingerprint}))
	}
	s.writeJSON(w, status, b)
}

func (s *Server) bug(w http.ResponseWriter, r *http.Request, id string) {
	parts := strings.Split(id, "/")
	id = parts[0]
	b, e := s.Store.GetBug(id)
	if e != nil {
		s.problem(w, r, 404, "not_found", "bug not found", nil)
		return
	}
	if workspaceID := r.Header.Get("X-Workspace-ID"); workspaceID != "" && b.WorkspaceID != workspaceID {
		s.problem(w, r, 404, "not_found", "bug not found", nil)
		return
	}
	if len(parts) > 1 {
		switch parts[1] {
		case "triage":
			if r.Method != http.MethodPost {
				s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
				return
			}
			b.Status = domain.BugRepairing
			if e = s.Store.UpdateBug(b); e != nil {
				s.problem(w, r, 409, "update_failed", e.Error(), nil)
				return
			}
			s.writeJSON(w, 200, b)
			return
		case "repair":
			if r.Method != http.MethodPost {
				s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
				return
			}
			if b.AttemptCount >= 3 {
				b.Status = domain.BugEscalated
				_ = s.Store.UpdateBug(b)
				s.problem(w, r, 409, "repair_limit_reached", "automatic repair limit reached", map[string]any{"attempt_count": b.AttemptCount})
				return
			}
			b.AttemptCount++
			b.Status = domain.BugRepairing
			if e = s.Store.UpdateBug(b); e != nil {
				s.problem(w, r, 409, "update_failed", e.Error(), nil)
				return
			}
			var run provider.RunBinding
			contextID, contextVersion := "", int64(0)
			priorProvenance := domain.Provenance{}
			var workItem domain.WorkItem
			provenanceWorkItemID := b.WorkItemID
			if provenanceWorkItemID == "" {
				provenanceWorkItemID = "bug-" + b.ID
			}
			contextID = "context-" + provenanceWorkItemID
			contextVersion = 1
			if b.WorkItemID != "" {
				workItem, _ = s.Store.GetWorkItem(b.WorkItemID)
				contextID = "context-" + b.WorkItemID
				if manifest, manifestErr := s.Store.GetContextManifest(contextID, 0); manifestErr == nil {
					contextVersion = manifest.Version
				}
			}
			if saved, ok := s.Store.FindProvenance(provenanceWorkItemID); ok {
				priorProvenance = saved
				if saved.ProviderTaskID != "" {
					if snapshot, snapshotErr := s.Provider.GetRun(r.Context(), saved.ProviderTaskID); snapshotErr == nil {
						if strings.TrimSpace(snapshot.SessionID) != "" {
							priorProvenance.ProviderSessionID = snapshot.SessionID
						}
						if strings.TrimSpace(snapshot.WorkDir) != "" {
							priorProvenance.ProviderWorkDir = snapshot.WorkDir
						}
					}
				}
			}
			if priorProvenance.AgentBindingID == "" {
				priorProvenance.AgentBindingID = workItem.DeveloperAgentBindingID
			}
			providerWorkItemID := provenanceWorkItemID
			cmd := provider.StartRunCommand{WorkItemID: providerWorkItemID, AgentBindingID: priorProvenance.AgentBindingID, ProviderIssueID: workItem.ProviderIssueID, Input: repairBrief(b), SessionID: priorProvenance.ProviderSessionID, ContextID: contextID, ContextVersion: contextVersion, RepairAttempt: b.AttemptCount, LegacyAdapterVersion: "bug-repair-v1", IdempotencyKey: fmt.Sprintf("bug:%s:attempt:%d", b.ID, b.AttemptCount)}
			graphScope, scopeErr := compat.BugDispatchScope(b.ID, cmd.IdempotencyKey)
			if scopeErr != nil {
				s.problem(w, r, http.StatusUnprocessableEntity, "dispatch_scope_invalid", scopeErr.Error(), nil)
				return
			}
			cmd.PlanID, cmd.NodeID, cmd.AttemptID = graphScope.PlanID, graphScope.NodeID, graphScope.AttemptID
			if binding, bindingErr := s.Store.GetProviderBinding(cmd.AgentBindingID); bindingErr == nil {
				cmd.ProviderAssigneeID = binding.ProviderObjectID
			}
			var continuation *provider.ContinuationCommand
			if cmd.ProviderIssueID != "" && priorProvenance.ProviderSessionID != "" && priorProvenance.ProviderWorkDir != "" {
				if _, supported := s.Provider.(provider.ContinuityProvider); supported {
					continuation = &provider.ContinuationCommand{
						IssueID: cmd.ProviderIssueID, AgentID: cmd.AgentBindingID, Input: cmd.Input,
						ExpectedSessionID: priorProvenance.ProviderSessionID, ExpectedWorkDir: priorProvenance.ProviderWorkDir,
						ContextEnvelope: cmd.ContextEnvelope, IdempotencyKey: cmd.IdempotencyKey,
						LegacyAdapterVersion: cmd.LegacyAdapterVersion,
					}
				}
			}
			harnessSessionID := "session-bug-" + b.ID
			if b.WorkItemID != "" {
				harnessSessionID = "session-" + b.WorkItemID
			}
			// A new graph dispatch uses the durable harness session. Provider-native
			// continuation identity lives only on ContinuationCommand.
			cmd.SessionID = harnessSessionID
			var dispatchEvent harness.OutboxEvent
			dispatchClaimed := true
			var repairTurnHash string
			if s.Harness != nil {
				workspaceID := requestWorkspace(r, b.WorkspaceID)
				if workspaceID == "" {
					workspaceID = "local"
				}
				if _, e = s.Harness.EnsureSession(harness.Session{ID: harnessSessionID, TenantID: tenant(r), WorkspaceID: workspaceID, BudgetTokens: harnessSessionBudget()}); e != nil {
					s.problem(w, r, http.StatusServiceUnavailable, "harness_unavailable", e.Error(), nil)
					return
				}
				contextEnvelope, envelopeErr := s.compiledHarnessEnvelope(harnessSessionID)
				if envelopeErr != nil {
					s.problem(w, r, http.StatusServiceUnavailable, "context_envelope_failed", envelopeErr.Error(), nil)
					return
				}
				cmd.ContextEnvelope = contextEnvelope
				if continuation != nil {
					continuation.PlanID, continuation.NodeID, continuation.AttemptID = graphScope.PlanID, graphScope.NodeID, graphScope.AttemptID
					continuation.ContextEnvelope = contextEnvelope
				}
				turn, turnErr := s.Harness.AppendTurn(harnessSessionID, harness.Turn{Role: harness.RoleUser, Content: repairBrief(b), IdempotencyKey: cmd.IdempotencyKey, Metadata: map[string]string{"bug_id": b.ID, "attempt": strconv.Itoa(b.AttemptCount)}})
				if turnErr != nil {
					s.problem(w, r, http.StatusServiceUnavailable, "harness_unavailable", turnErr.Error(), nil)
					return
				}
				repairTurnHash = turn.Hash
				intent := providerDispatchIntent{Type: providerDispatchIntentType, Kind: "bug", BugID: b.ID, WorkItemID: providerWorkItemID, RequirementID: b.RequirementID, ProviderIssueID: cmd.ProviderIssueID, AgentID: cmd.AgentBindingID, HarnessSessionID: harnessSessionID, ContextID: contextID, ContextVersion: contextVersion, RepairAttempt: b.AttemptCount, TurnHash: turn.Hash, ContextEnvelope: contextEnvelope, Command: cmd, Continuation: continuation}
				dispatchEvent, dispatchClaimed, e = s.Harness.EnqueueAndClaimOutbox(harnessSessionID, cmd.IdempotencyKey, intent, providerDispatchOwner, dispatchLeaseTTL(), time.Now().UTC())
				if e != nil {
					if errors.Is(e, harness.ErrLeaseBusy) {
						s.problem(w, r, http.StatusConflict, "dispatch_in_progress", e.Error(), nil)
						return
					}
					s.problem(w, r, http.StatusServiceUnavailable, "outbox_enqueue_failed", e.Error(), nil)
					return
				}
				if !dispatchClaimed {
					s.problem(w, r, http.StatusConflict, "dispatch_in_progress", "provider dispatch intent is already in progress", nil)
					return
				}
				if e = s.saveHarnessCheckpoint(harnessSessionID, harness.CheckpointEffectBefore, repairTurnHash, contextVersion, []string{dispatchEvent.ID}, nil, "bug repair provider run pending"); e != nil {
					s.problem(w, r, http.StatusServiceUnavailable, "checkpoint_save_failed", e.Error(), nil)
					return
				}
			}
			if continuation != nil {
				*continuation = continuation.WithTraceContext(r.Context())
				continuity, supported := s.Provider.(provider.ContinuityProvider)
				if !supported {
					e = errors.New("provider cannot continue the original bug repair session")
				} else {
					run, e = continuity.ContinueWorkItem(r.Context(), *continuation)
					if e == nil && (!run.SessionReused || run.SessionID != continuation.ExpectedSessionID || filepathClean(run.WorkDir) != filepathClean(continuation.ExpectedWorkDir)) {
						e = errors.New("provider did not confirm the original bug repair session and workdir")
					}
				}
			} else {
				cmd = cmd.WithTraceContext(r.Context())
				run, e = s.Provider.StartRun(r.Context(), cmd)
			}
			if e != nil {
				if dispatchEvent.ID != "" {
					_ = s.Harness.NackOutbox(harnessSessionID, dispatchEvent.ID, providerDispatchOwner, time.Now().UTC().Add(time.Second))
				}
				s.problem(w, r, 502, "provider_run_failed", string(provider.ErrorCodeOf(e)), nil)
				return
			}
			if strings.TrimSpace(run.ProviderRunID) == "" {
				if dispatchEvent.ID != "" {
					_ = s.Harness.NackOutbox(harnessSessionID, dispatchEvent.ID, providerDispatchOwner, time.Now().UTC().Add(time.Second))
				}
				s.problem(w, r, 502, "provider_run_failed", "provider returned an empty run binding", nil)
				return
			}
			if providerWorkItemID != "" {
				attempt := domain.RepairAttempt{BugID: b.ID, WorkItemID: providerWorkItemID, Attempt: b.AttemptCount, ContextID: contextID, ContextVersion: contextVersion, ProviderIssueID: cmd.ProviderIssueID, ProviderSessionID: run.SessionID, ProviderWorkDir: run.WorkDir, ProviderTaskID: run.ProviderRunID, Status: "started", Brief: domain.RepairBrief{BugID: b.ID, Fingerprint: b.Fingerprint, StableSummary: b.Title, FailedEvidence: []string{b.LogExcerpt}, Attempt: b.AttemptCount}}
				if _, e = s.Store.SaveRepairAttempt(attempt); e != nil {
					if dispatchEvent.ID != "" {
						_ = s.Harness.NackOutbox(harnessSessionID, dispatchEvent.ID, providerDispatchOwner, time.Now().UTC().Add(time.Second))
					}
					s.problem(w, r, http.StatusServiceUnavailable, "repair_attempt_persist_failed", e.Error(), nil)
					return
				}
				providerName := priorProvenance.Provider
				if providerName == "" {
					providerName = "local"
				}
				if e = s.Store.SaveProvenance(domain.Provenance{WorkItemID: providerWorkItemID, RequirementID: b.RequirementID, BugID: b.ID, AgentBindingID: cmd.AgentBindingID, Provider: providerName, ProviderTaskID: run.ProviderRunID, ProviderSessionID: run.SessionID, ProviderWorkDir: run.WorkDir, ProviderIdempotencyKey: cmd.IdempotencyKey, RepositoryID: b.RepositoryID, ContextVersion: contextVersion}); e != nil {
					if dispatchEvent.ID != "" {
						_ = s.Harness.NackOutbox(harnessSessionID, dispatchEvent.ID, providerDispatchOwner, time.Now().UTC().Add(time.Second))
					}
					s.problem(w, r, http.StatusServiceUnavailable, "provenance_save_failed", e.Error(), nil)
					return
				}
			}
			if dispatchEvent.ID != "" {
				if e = s.saveHarnessCheckpoint(harnessSessionID, harness.CheckpointEffectAfter, repairTurnHash, contextVersion, []string{dispatchEvent.ID}, nil, "bug repair provider run recorded"); e != nil {
					s.problem(w, r, http.StatusServiceUnavailable, "checkpoint_save_failed", e.Error(), nil)
					return
				}
				if e = s.Harness.AckOutbox(harnessSessionID, dispatchEvent.ID, providerDispatchOwner, time.Now().UTC()); e != nil {
					s.problem(w, r, http.StatusServiceUnavailable, "outbox_ack_failed", e.Error(), nil)
					return
				}
			}
			s.writeJSON(w, 202, map[string]any{"bug": b, "run": run, "repair_brief": repairBrief(b), "context_id": contextID, "context_version": contextVersion, "context_available": contextID != "", "session_reused": run.SessionReused})
			return
		case "verify":
			if r.Method != http.MethodPost {
				s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
				return
			}
			b.Status = domain.BugVerified
			if e = s.Store.UpdateBug(b); e != nil {
				s.problem(w, r, 409, "update_failed", e.Error(), nil)
				return
			}
			s.writeJSON(w, 200, b)
			return
		case "comments":
			if len(parts) == 2 {
				s.commentRoute(w, r, "bug", b.ID, b.WorkspaceID)
				return
			}
		}
	}
	if r.Method == http.MethodGet {
		comments, _ := s.Store.ListComments(b.WorkspaceID, "bug", b.ID, "", 250)
		s.writeJSON(w, 200, map[string]any{"bug": b, "comments": comments, "attachments": s.Store.ListAttachments(b.WorkspaceID, "bug", b.ID)})
		return
	}
	s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
}

const maxAttachmentBytes = 20 << 20

func (s *Server) attachmentRoute(w http.ResponseWriter, r *http.Request, user adroauth.User, userAuthenticated, machineAuthenticated bool) {
	ownerType := strings.TrimSpace(r.URL.Query().Get("owner_type"))
	ownerID := strings.TrimSpace(r.URL.Query().Get("owner_id"))
	if r.Method == http.MethodGet {
		if ownerType == "" || ownerID == "" {
			s.problem(w, r, http.StatusBadRequest, "attachment_owner_required", "owner_type and owner_id are required", nil)
			return
		}
		if !s.canUseAttachmentOwner(user, userAuthenticated, machineAuthenticated, ownerType) {
			s.problem(w, r, http.StatusForbidden, "menu_access_denied", "your account is not allowed to access this entity", nil)
			return
		}
		workspaceID, ok := s.attachmentOwnerWorkspace(ownerType, ownerID)
		if !ok || (userAuthenticated && user.WorkspaceID != workspaceID) {
			s.problem(w, r, http.StatusNotFound, "not_found", "attachment owner not found", nil)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"items": s.Store.ListAttachments(workspaceID, ownerType, ownerID)})
		return
	}
	if r.Method != http.MethodPost {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentBytes+(1<<20))
	if err := r.ParseMultipartForm(maxAttachmentBytes + (1 << 20)); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_multipart", "attachment upload is invalid or too large", nil)
		return
	}
	ownerType = strings.TrimSpace(r.FormValue("owner_type"))
	ownerID = strings.TrimSpace(r.FormValue("owner_id"))
	if !s.canUseAttachmentOwner(user, userAuthenticated, machineAuthenticated, ownerType) {
		s.problem(w, r, http.StatusForbidden, "menu_access_denied", "your account is not allowed to attach files to this entity", nil)
		return
	}
	workspaceID, ok := s.attachmentOwnerWorkspace(ownerType, ownerID)
	if !ok || (userAuthenticated && user.WorkspaceID != workspaceID) {
		s.problem(w, r, http.StatusUnprocessableEntity, "invalid_attachment_owner", "attachment owner does not exist in this workspace", nil)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, "file_required", "an attachment file is required", nil)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxAttachmentBytes+1))
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, "upload_failed", err.Error(), nil)
		return
	}
	if len(data) == 0 {
		s.problem(w, r, http.StatusUnprocessableEntity, "empty_upload", "attachment content is empty", nil)
		return
	}
	if len(data) > maxAttachmentBytes {
		s.problem(w, r, http.StatusRequestEntityTooLarge, "attachment_too_large", "attachment exceeds the 20 MiB limit", nil)
		return
	}
	mediaType := header.Header.Get("Content-Type")
	if mediaType == "" || mediaType == "application/octet-stream" {
		mediaType = http.DetectContentType(data)
	}
	filename := providerSafeFilename(header.Filename)
	artifactID := "attachment-" + domain.NewID()
	meta, err := s.Artifacts.Put(r.Context(), artifact.Key{TenantID: tenant(r), ArtifactID: artifactID, Version: 1}, bytes.NewReader(data), artifact.PutOptions{MediaType: mediaType, Classification: "entity:" + ownerType, Immutable: true})
	if err != nil {
		s.problem(w, r, http.StatusInternalServerError, "artifact_publish_failed", err.Error(), nil)
		return
	}
	createdBy := r.Header.Get("X-Member-ID")
	if userAuthenticated {
		createdBy = user.ID
	}
	item, err := s.Store.SaveAttachment(domain.EntityAttachment{WorkspaceID: workspaceID, OwnerType: ownerType, OwnerID: ownerID, Filename: filename, MediaType: mediaType, SizeBytes: meta.SizeBytes, ArtifactURI: meta.Key.URI(), CreatedBy: createdBy})
	if err != nil {
		s.problem(w, r, http.StatusInternalServerError, "attachment_record_failed", err.Error(), nil)
		return
	}
	s.recordAudit(r, workspaceID, "attachment.created", item.ID, map[string]any{"owner_type": ownerType, "owner_id": ownerID, "filename": filename, "size_bytes": meta.SizeBytes})
	s.writeJSON(w, http.StatusCreated, item)
}

func (s *Server) attachmentOwnerWorkspace(ownerType, ownerID string) (string, bool) {
	switch ownerType {
	case "requirement":
		item, err := s.Store.GetRequirement(ownerID)
		return item.WorkspaceID, err == nil
	case "bug":
		item, err := s.Store.GetBug(ownerID)
		return item.WorkspaceID, err == nil
	case "chat_session":
		item, err := s.Store.GetChatSession(ownerID)
		return item.WorkspaceID, err == nil
	case "comment":
		item, err := s.Store.GetComment(ownerID)
		return item.WorkspaceID, err == nil
	default:
		return "", false
	}
}

func (s *Server) canUseAttachmentOwner(user adroauth.User, authenticated, machine bool, ownerType string) bool {
	if machine || !authenticated && !authRequired() {
		return ownerType == "requirement" || ownerType == "bug" || ownerType == "chat_session" || ownerType == "comment"
	}
	if !authenticated {
		return false
	}
	return ownerType == "requirement" && user.Can("requirements") || ownerType == "bug" && user.Can("bugs") || ownerType == "chat_session" && user.Can("executions") || ownerType == "comment" && (user.Can("requirements") || user.Can("bugs"))
}

func (s *Server) createUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	var in struct {
		ArtifactID     string `json:"artifact_id"`
		Version        int64  `json:"version"`
		MediaType      string `json:"media_type"`
		Classification string `json:"classification"`
		Immutable      bool   `json:"immutable"`
	}
	if err := decodeJSON(r, &in); err != nil {
		s.problem(w, r, 400, "invalid_json", err.Error(), nil)
		return
	}
	if in.ArtifactID == "" {
		in.ArtifactID = domain.NewID()
	}
	if in.Version < 1 {
		in.Version = 1
	}
	id := domain.NewID()
	s.uploadMu.Lock()
	s.uploads[id] = &upload{tenant: tenant(r), artifactID: in.ArtifactID, version: in.Version, opts: artifact.PutOptions{MediaType: in.MediaType, Classification: in.Classification, Immutable: in.Immutable}, parts: map[int][]byte{}}
	s.uploadMu.Unlock()
	s.writeJSON(w, 201, map[string]any{"upload_id": id, "artifact_id": in.ArtifactID, "version": in.Version})
}

const maxScreenshotBytes = 20 << 20

// createScreenshot accepts the browser's captured viewport/screen or an image
// selected by the user, stores it in the immutable ArtifactStore, and then
// optionally forwards the same bytes through the provider attachment bridge.
// The response always includes the artifact URI so an external provider can be
// retried without losing the evidence.
func (s *Server) createScreenshot(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxScreenshotBytes+1)
	if err := r.ParseMultipartForm(maxScreenshotBytes + 1); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_multipart", "screenshot upload is invalid or too large", nil)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, "file_required", "a screenshot file is required", nil)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxScreenshotBytes+1))
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, "upload_failed", err.Error(), nil)
		return
	}
	if len(data) == 0 {
		s.problem(w, r, http.StatusUnprocessableEntity, "empty_upload", "screenshot content is empty", nil)
		return
	}
	if len(data) > maxScreenshotBytes {
		s.problem(w, r, http.StatusRequestEntityTooLarge, "screenshot_too_large", "screenshot exceeds the 20 MiB limit", nil)
		return
	}
	targetType := strings.TrimSpace(r.FormValue("target_type"))
	targetID := strings.TrimSpace(r.FormValue("target_id"))
	if targetType != "" && targetType != "issue" && targetType != "comment" && targetType != "run" && targetType != "workspace" {
		s.problem(w, r, http.StatusUnprocessableEntity, "invalid_attachment_target", "target_type must be issue, comment, run, or workspace", nil)
		return
	}
	if targetType != "" && targetID == "" {
		s.problem(w, r, http.StatusUnprocessableEntity, "invalid_attachment_target", "target_id is required when target_type is set", nil)
		return
	}
	mediaType := header.Header.Get("Content-Type")
	if mediaType == "" || mediaType == "application/octet-stream" {
		mediaType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(mediaType, "image/") {
		s.problem(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "screenshots must use an image media type", map[string]any{"media_type": mediaType})
		return
	}
	filename := providerSafeFilename(header.Filename)
	artifactID := "screenshot-" + domain.NewID()
	meta, err := s.Artifacts.Put(r.Context(), artifact.Key{TenantID: tenant(r), ArtifactID: artifactID, Version: 1}, bytes.NewReader(data), artifact.PutOptions{MediaType: mediaType, Classification: "evidence:screenshot", Immutable: true})
	if err != nil {
		s.problem(w, r, http.StatusInternalServerError, "artifact_publish_failed", err.Error(), nil)
		return
	}
	receipt := provider.AttachmentReceipt{Status: "stored", ArtifactURI: meta.Key.URI()}
	delivery := "not_requested"
	if targetType != "" {
		publisher, supported := s.Provider.(provider.AttachmentPublisher)
		if !supported {
			delivery = "provider_unsupported"
		} else {
			receipt, err = publisher.PublishAttachment(r.Context(), provider.AttachmentSpec{TargetType: targetType, TargetID: targetID, Filename: filename, MediaType: mediaType, ArtifactURI: meta.Key.URI(), Content: data})
			if err != nil {
				delivery = "provider_failed"
				receipt = provider.AttachmentReceipt{Status: delivery, ArtifactURI: meta.Key.URI()}
			} else {
				delivery = "delivered"
			}
		}
	}
	_ = s.Events.Publish(r.Context(), events.NewWithContext(r.Context(), "evidence.screenshot.created.v1", "artifact", artifactID, tenant(r), r.Header.Get("X-Workspace-ID"), 1, map[string]any{"uri": meta.Key.URI(), "media_type": mediaType, "target_type": targetType, "target_id": targetID, "delivery": delivery}))
	s.writeJSON(w, http.StatusCreated, map[string]any{"artifact": meta, "uri": meta.Key.URI(), "delivery": delivery, "provider_receipt": receipt})
}

func providerSafeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "screenshot.png"
	}
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	return name
}

func (s *Server) uploadRoute(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(path, "/")
	id := parts[0]
	s.uploadMu.Lock()
	u, ok := s.uploads[id]
	s.uploadMu.Unlock()
	if !ok {
		s.problem(w, r, 404, "not_found", "upload not found", nil)
		return
	}
	if requestedTenant := r.Header.Get("X-Tenant-ID"); requestedTenant != "" && u.tenant != requestedTenant {
		s.problem(w, r, 404, "not_found", "upload not found", nil)
		return
	}
	if len(parts) >= 3 && parts[1] == "parts" && r.Method == http.MethodPut {
		part, err := strconv.Atoi(parts[2])
		if err != nil || part < 1 {
			s.problem(w, r, 400, "invalid_part", "part number must be positive", nil)
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
		if err != nil {
			s.problem(w, r, 400, "upload_failed", err.Error(), nil)
			return
		}
		s.uploadMu.Lock()
		u.parts[part] = data
		s.uploadMu.Unlock()
		s.writeJSON(w, 200, map[string]any{"part": part, "size": len(data)})
		return
	}
	if len(parts) >= 2 && parts[1] == "complete" && r.Method == http.MethodPost {
		var input struct {
			Parts []struct {
				Part   int    `json:"part"`
				SHA256 string `json:"sha256"`
				Size   int64  `json:"size"`
			} `json:"parts"`
			SHA256 string `json:"sha256"`
		}
		if r.Body != nil && r.ContentLength != 0 {
			if err := decodeJSON(r, &input); err != nil && err != io.EOF {
				s.problem(w, r, 400, "invalid_json", err.Error(), nil)
				return
			}
		}
		s.uploadMu.Lock()
		nums := make([]int, 0, len(u.parts))
		for n := range u.parts {
			nums = append(nums, n)
		}
		sortInts(nums)
		var buf bytes.Buffer
		for _, n := range nums {
			buf.Write(u.parts[n])
		}
		if len(nums) == 0 {
			s.uploadMu.Unlock()
			s.problem(w, r, 422, "empty_upload", "at least one part is required", nil)
			return
		}
		for i, n := range nums {
			if n != i+1 {
				s.uploadMu.Unlock()
				s.problem(w, r, 422, "invalid_parts", "parts must be contiguous starting at 1", nil)
				return
			}
		}
		if len(input.Parts) > 0 {
			if len(input.Parts) != len(nums) {
				s.uploadMu.Unlock()
				s.problem(w, r, 422, "invalid_parts", "declared part count does not match upload", nil)
				return
			}
			for _, declared := range input.Parts {
				data, exists := u.parts[declared.Part]
				if !exists || (declared.Size > 0 && declared.Size != int64(len(data))) || (declared.SHA256 != "" && declared.SHA256 != hashBytes(data)) {
					s.uploadMu.Unlock()
					s.problem(w, r, 422, "part_verification_failed", "part metadata does not match uploaded content", map[string]any{"part": declared.Part})
					return
				}
			}
		}
		if input.SHA256 != "" && input.SHA256 != hashBytes(buf.Bytes()) {
			s.uploadMu.Unlock()
			s.problem(w, r, 422, "hash_mismatch", "declared upload hash does not match content", nil)
			return
		}
		delete(s.uploads, id)
		s.uploadMu.Unlock()
		meta, err := s.Artifacts.Put(r.Context(), artifact.Key{TenantID: u.tenant, ArtifactID: u.artifactID, Version: u.version}, bytes.NewReader(buf.Bytes()), u.opts)
		if err != nil {
			s.problem(w, r, 500, "artifact_publish_failed", err.Error(), nil)
			return
		}
		meta.ContentSHA256 = hashBytes(buf.Bytes())
		s.writeJSON(w, 201, map[string]any{"uri": meta.Key.URI(), "meta": meta})
		return
	}
	s.problem(w, r, 404, "not_found", "upload route not found", nil)
}

func (s *Server) artifactRoute(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(path, "/")
	if len(parts) >= 4 && parts[1] == "versions" {
		version, _ := strconv.ParseInt(parts[2], 10, 64)
		if parts[3] == "content" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			key := artifact.Key{TenantID: tenant(r), ArtifactID: parts[0], Version: version}
			rangeHeader := r.Header.Get("Range")
			br, rangeErr := parseRange(rangeHeader)
			if rangeErr != nil {
				s.problem(w, r, http.StatusRequestedRangeNotSatisfiable, "invalid_range", rangeErr.Error(), nil)
				return
			}
			f, meta, err := s.Artifacts.Open(r.Context(), key, br)
			if err != nil {
				status := http.StatusNotFound
				code := "not_found"
				if rangeHeader != "" {
					status, code = http.StatusRequestedRangeNotSatisfiable, "invalid_range"
				}
				s.problem(w, r, status, code, "artifact content is not available for the requested range", nil)
				return
			}
			defer f.Close()
			contentLength := meta.SizeBytes
			status := http.StatusOK
			if rangeHeader != "" {
				end := br.End
				if end < 0 || end >= meta.SizeBytes {
					end = meta.SizeBytes - 1
				}
				contentLength = end - br.Start + 1
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", br.Start, end, meta.SizeBytes))
				status = http.StatusPartialContent
			}
			if contentLength >= 0 {
				w.Header().Set("Content-Length", strconv.FormatInt(contentLength, 10))
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Accept-Ranges", "bytes")
			if meta.ContentSHA256 != "" {
				w.Header().Set("ETag", fmt.Sprintf("\"%s\"", meta.ContentSHA256))
			}
			w.WriteHeader(status)
			if r.Method != http.MethodHead {
				_, _ = io.Copy(w, f)
			}
			return
		}
		if parts[3] == "" && r.Method == http.MethodGet {
		}
	}
	if len(parts) >= 3 && parts[1] == "versions" && r.Method == http.MethodGet {
		version, _ := strconv.ParseInt(parts[2], 10, 64)
		meta, err := s.Artifacts.Stat(r.Context(), artifact.Key{TenantID: tenant(r), ArtifactID: parts[0], Version: version})
		if err != nil {
			s.problem(w, r, 404, "not_found", "artifact not found", nil)
			return
		}
		s.writeJSON(w, 200, meta)
		return
	}
	s.problem(w, r, 404, "not_found", "artifact route not found", nil)
}

func (s *Server) artifactMigrationRoute(w http.ResponseWriter, r *http.Request, path string) {
	path = strings.Trim(path, "/")
	if path == "" {
		if r.Method != http.MethodPost {
			s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
			return
		}
		var input domain.ArtifactMigration
		if err := decodeJSON(r, &input); err != nil {
			s.problem(w, r, 400, "invalid_json", err.Error(), nil)
			return
		}
		input.WorkspaceID = requestWorkspace(r, input.WorkspaceID)
		migration, err := s.Store.CreateArtifactMigration(input)
		if err != nil {
			s.problem(w, r, 422, "validation_error", err.Error(), nil)
			return
		}
		// The reference profile records the migration contract and marks no object
		// copied until a production driver worker performs the actual copy.
		s.recordAudit(r, migration.WorkspaceID, "artifact.migration.created", migration.ID, map[string]any{"from": migration.FromDriver, "to": migration.ToDriver})
		s.writeJSON(w, http.StatusAccepted, migration)
		return
	}
	parts := strings.Split(path, "/")
	migration, err := s.Store.GetArtifactMigration(parts[0])
	if err != nil {
		s.problem(w, r, 404, "not_found", "artifact migration not found", nil)
		return
	}
	if !workspaceMatchesRequest(r, migration.WorkspaceID) {
		s.problem(w, r, http.StatusNotFound, "not_found", "artifact migration not found", nil)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		s.writeJSON(w, 200, migration)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodPost {
		status := ""
		switch parts[1] {
		case "pause":
			status = "paused"
		case "resume":
			status = "running"
		case "rollback":
			status = "rolled_back"
		}
		if status == "" {
			s.problem(w, r, 404, "not_found", "migration action not found", nil)
			return
		}
		updated, err := s.Store.UpdateArtifactMigration(migration.ID, status)
		if err != nil {
			s.problem(w, r, 409, "migration_update_failed", err.Error(), nil)
			return
		}
		s.recordAudit(r, updated.WorkspaceID, "artifact.migration."+parts[1], updated.ID, nil)
		s.writeJSON(w, 200, updated)
		return
	}
	s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
}

func (s *Server) runRoute(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	id := parts[0]
	if id == "" {
		s.problem(w, r, http.StatusNotFound, "not_found", "run not found", nil)
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	knownAction := (action == "cancel" && r.Method == http.MethodPost) ||
		(action == "messages" && r.Method == http.MethodPost) ||
		(action == "usage" && r.Method == http.MethodGet) ||
		(action == "events" && r.Method == http.MethodGet) ||
		(action == "" && r.Method == http.MethodGet)
	if !knownAction {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	run, err := s.Provider.GetRun(r.Context(), id)
	if err != nil {
		if provider.ErrorCodeOf(err) == provider.ErrorCapability {
			s.problem(w, r, http.StatusNotImplemented, "capability_unavailable", providerSafeError(err), map[string]any{"capability": capabilityName(err)})
		} else {
			s.problem(w, r, http.StatusNotFound, "not_found", providerSafeError(err), nil)
		}
		return
	}
	if workspaceID := requestWorkspace(r, ""); workspaceID != "" && !s.runBelongsToWorkspace(run, workspaceID) {
		s.problem(w, r, http.StatusNotFound, "not_found", "run not found", nil)
		return
	}
	if len(parts) > 1 && parts[1] == "cancel" && r.Method == http.MethodPost {
		if err := s.Provider.CancelRun(r.Context(), id); err != nil {
			if provider.ErrorCodeOf(err) == provider.ErrorCapability {
				s.problem(w, r, http.StatusNotImplemented, "capability_unavailable", providerSafeError(err), map[string]any{"capability": capabilityName(err)})
			} else {
				s.problem(w, r, 404, "not_found", providerSafeError(err), nil)
			}
			return
		}
		s.writeJSON(w, 200, map[string]any{"id": id, "status": "cancelled"})
		return
	}
	if len(parts) > 1 && parts[1] == "messages" && r.Method == http.MethodPost {
		var input struct {
			Input          string `json:"input"`
			IdempotencyKey string `json:"idempotency_key,omitempty"`
		}
		if err := decodeJSON(r, &input); err != nil {
			s.problem(w, r, 400, "invalid_json", err.Error(), nil)
			return
		}
		key := strings.TrimSpace(input.IdempotencyKey)
		if key == "" {
			key = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		}
		var appendErr error
		if keyed, ok := s.Provider.(provider.InputKeyProvider); ok && key != "" {
			appendErr = keyed.AppendInputWithKey(r.Context(), id, input.Input, key)
		} else {
			appendErr = s.Provider.AppendInput(r.Context(), id, input.Input)
		}
		if appendErr != nil {
			err := appendErr
			status, code := http.StatusConflict, "run_input_failed"
			if provider.ErrorCodeOf(err) == provider.ErrorCapability {
				status, code = http.StatusNotImplemented, "capability_unavailable"
			}
			s.problem(w, r, status, code, providerSafeError(err), map[string]any{"capability": capabilityName(err)})
			return
		}
		s.writeJSON(w, 202, map[string]any{"id": id, "accepted": true})
		return
	}
	if len(parts) > 1 && parts[1] == "usage" && r.Method == http.MethodGet {
		usage, err := s.Provider.GetUsage(r.Context(), id)
		if err != nil {
			if provider.ErrorCodeOf(err) == provider.ErrorCapability {
				s.problem(w, r, http.StatusNotImplemented, "capability_unavailable", providerSafeError(err), map[string]any{"capability": capabilityName(err)})
			} else {
				s.problem(w, r, 404, "not_found", providerSafeError(err), nil)
			}
			return
		}
		s.writeJSON(w, 200, usage)
		return
	}
	if len(parts) > 1 && parts[1] == "events" && r.Method == http.MethodGet {
		items, next, err := s.Events.ListChecked(id, r.URL.Query().Get("cursor"), queryInt(r, "limit", 100))
		if errors.Is(err, events.ErrInvalidCursor) {
			s.problem(w, r, http.StatusGone, "invalid_cursor", err.Error(), nil)
			return
		}
		if err != nil {
			s.problem(w, r, http.StatusInternalServerError, "event_stream_unavailable", providerSafeError(err), nil)
			return
		}
		s.writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
		return
	}
	if r.Method == http.MethodGet {
		s.writeJSON(w, 200, run)
		return
	}
	s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
}

func (s *Server) workItemRoute(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(path, "/")
	id := parts[0]
	witem, e := s.Store.GetWorkItem(id)
	if e != nil {
		s.problem(w, r, 404, "not_found", "work item not found", nil)
		return
	}
	if workspaceID := requestWorkspace(r, ""); workspaceID != "" {
		if !s.workItemBelongsToWorkspace(witem, workspaceID) {
			s.problem(w, r, http.StatusNotFound, "not_found", "work item not found", nil)
			return
		}
	}
	if len(parts) == 2 && parts[1] == "context" && r.Method == http.MethodGet {
		contextID := "context-" + id
		version := int64(0)
		if rawVersion := strings.TrimSpace(r.URL.Query().Get("version")); rawVersion != "" {
			parsed, parseErr := strconv.ParseInt(rawVersion, 10, 64)
			if parseErr != nil || parsed < 1 {
				s.problem(w, r, http.StatusBadRequest, "invalid_version", "version must be a positive integer", nil)
				return
			}
			version = parsed
		}
		manifest, manifestErr := s.Store.GetContextManifest(contextID, version)
		if manifestErr != nil {
			s.problem(w, r, http.StatusNotFound, "context_not_found", "work item context manifest not found", nil)
			return
		}
		response := map[string]any{"work_item": witem, "context": manifest, "context_available": true}
		if provenance, ok := s.Store.FindProvenance(id); ok {
			response["provenance"] = provenance
		}
		s.writeJSON(w, http.StatusOK, response)
		return
	}
	if len(parts) == 2 && parts[1] == "repair-attempts" && r.Method == http.MethodGet {
		s.writeJSON(w, http.StatusOK, map[string]any{"work_item": witem, "items": s.Store.ListRepairAttemptsForWorkItem(id)})
		return
	}
	if len(parts) == 2 && parts[1] == "diff" {
		if r.Method == http.MethodGet {
			diff, err := s.Store.GetDiff(id)
			if err != nil {
				s.problem(w, r, 404, "not_found", "diff not found", nil)
				return
			}
			s.writeJSON(w, 200, diff)
			return
		}
		if r.Method == http.MethodPost {
			var input domain.DiffSnapshot
			if err := decodeJSON(r, &input); err != nil {
				s.problem(w, r, 400, "invalid_json", err.Error(), nil)
				return
			}
			input.WorkItemID = id
			if input.RepositoryID == "" {
				input.RepositoryID = witem.RepositoryID
			}
			if input.ContentSHA256 == "" {
				input.ContentSHA256 = hashBytes([]byte(input.Patch))
			}
			diff, err := s.Store.SaveDiff(input)
			if err != nil {
				s.problem(w, r, 422, "validation_error", err.Error(), nil)
				return
			}
			_ = s.Events.Publish(r.Context(), events.NewWithContext(r.Context(), "workspace.diff.updated.v1", "work_item", id, tenant(r), "", 1, map[string]any{"repository_id": diff.RepositoryID, "content_sha256": diff.ContentSHA256}))
			s.writeJSON(w, 201, diff)
			return
		}
		s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	if len(parts) == 2 && parts[1] == "run" && r.Method == http.MethodPost {
		var input struct {
			AgentBindingID string `json:"agent_binding_id"`
			Input          string `json:"input"`
		}
		if err := decodeJSON(r, &input); err != nil {
			s.problem(w, r, 400, "invalid_json", err.Error(), nil)
			return
		}
		if witem.DeveloperAgentBindingID != "" {
			if input.AgentBindingID != "" && input.AgentBindingID != witem.DeveloperAgentBindingID {
				s.problem(w, r, http.StatusConflict, "agent_binding_mismatch", "work item agent binding is immutable", nil)
				return
			}
			input.AgentBindingID = witem.DeveloperAgentBindingID
		}
		contextID := "context-" + id
		sessionID := "session-" + id
		contextVersion := int64(1)
		if manifest, manifestErr := s.Store.GetContextManifest(contextID, 0); manifestErr == nil {
			contextVersion = manifest.Version
		} else {
			manifest, manifestErr := s.Store.SaveContextManifest(domain.ContextManifest{ContextID: contextID, Version: 1, RequirementID: witem.RequirementID, StableSummary: input.Input, Repositories: []domain.ContextRepository{{ID: witem.RepositoryID, Baseline: witem.BaselineCommit, Head: witem.HeadCommit}}, OriginalDeveloperMemberID: witem.MemberID, OriginalAgentBindingID: input.AgentBindingID, TokenBudget: 0})
			if manifestErr != nil {
				s.problem(w, r, http.StatusInternalServerError, "context_manifest_failed", manifestErr.Error(), nil)
				return
			}
			contextVersion = manifest.Version
		}
		cmd := provider.StartRunCommand{WorkItemID: id, AgentBindingID: input.AgentBindingID, Input: input.Input, ProviderIssueID: witem.ProviderIssueID, SessionID: sessionID, ContextID: contextID, ContextVersion: contextVersion, LegacyAdapterVersion: "work-item-v1"}
		if binding, bindingErr := s.Store.GetProviderBinding(input.AgentBindingID); bindingErr == nil {
			cmd.ProviderAssigneeID = binding.ProviderObjectID
		}
		requestKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if requestKey == "" {
			requestKey = strings.TrimSpace(r.Header.Get("X-Request-ID"))
		}
		if requestKey == "" {
			requestKey = domain.NewID()
		}
		turnKey := "work-item:" + id + ":request:" + requestKey
		cmd.IdempotencyKey = turnKey
		graphScope, scopeErr := compat.WorkItemDispatchScope(id, turnKey)
		if scopeErr != nil {
			s.problem(w, r, http.StatusUnprocessableEntity, "dispatch_scope_invalid", scopeErr.Error(), nil)
			return
		}
		cmd.PlanID, cmd.NodeID, cmd.AttemptID = graphScope.PlanID, graphScope.NodeID, graphScope.AttemptID
		var harnessTurnHash string
		var dispatchEvent harness.OutboxEvent
		dispatchClaimed := true
		if s.Harness != nil {
			workspaceID := requestWorkspace(r, requirementWorkspace(s.Store, witem.RequirementID))
			if workspaceID == "" {
				workspaceID = "local"
			}
			if _, harnessErr := s.Harness.EnsureSession(harness.Session{ID: sessionID, TenantID: tenant(r), WorkspaceID: workspaceID, BudgetTokens: harnessSessionBudget()}); harnessErr != nil {
				s.problem(w, r, http.StatusServiceUnavailable, "harness_unavailable", harnessErr.Error(), nil)
				return
			}
			contextEnvelope, envelopeErr := s.compiledHarnessEnvelope(sessionID)
			if envelopeErr != nil {
				s.problem(w, r, http.StatusServiceUnavailable, "context_envelope_failed", envelopeErr.Error(), nil)
				return
			}
			cmd.ContextEnvelope = contextEnvelope
			turn, turnErr := s.Harness.AppendTurn(sessionID, harness.Turn{Role: harness.RoleUser, Content: input.Input, IdempotencyKey: turnKey, Metadata: map[string]string{"work_item_id": id, "agent_id": input.AgentBindingID}})
			if turnErr != nil {
				s.problem(w, r, http.StatusServiceUnavailable, "harness_unavailable", turnErr.Error(), nil)
				return
			}
			harnessTurnHash = turn.Hash
			var enqueueErr error
			var claimedErr error
			dispatchEvent, dispatchClaimed, claimedErr = s.enqueueAndClaimExecutionDispatch(sessionID, turnKey, providerDispatchIntent{WorkItemID: id, RequirementID: witem.RequirementID, AgentID: input.AgentBindingID, RepositoryID: witem.RepositoryID, TurnHash: turn.Hash, Command: cmd})
			enqueueErr = claimedErr
			if enqueueErr != nil {
				if errors.Is(enqueueErr, harness.ErrLeaseBusy) {
					s.problem(w, r, http.StatusConflict, "dispatch_in_progress", enqueueErr.Error(), nil)
					return
				}
				s.problem(w, r, http.StatusServiceUnavailable, "outbox_enqueue_failed", enqueueErr.Error(), nil)
				return
			}
			if !dispatchClaimed {
				s.problem(w, r, http.StatusConflict, "dispatch_in_progress", "provider dispatch intent is already in progress", nil)
				return
			}
			if checkpointErr := s.saveHarnessCheckpoint(sessionID, harness.CheckpointEffectBefore, turn.Hash, contextVersion, []string{dispatchEvent.ID}, nil, "provider run pending"); checkpointErr != nil {
				s.problem(w, r, http.StatusServiceUnavailable, "checkpoint_save_failed", checkpointErr.Error(), nil)
				return
			}
		}
		cmd = cmd.WithTraceContext(r.Context())
		binding, err := s.Provider.StartRun(r.Context(), cmd)
		if err != nil {
			if dispatchEvent.ID != "" {
				_ = s.Harness.NackOutbox(sessionID, dispatchEvent.ID, providerDispatchOwner, time.Now().UTC().Add(time.Second))
			}
			s.problem(w, r, 502, "provider_run_failed", string(provider.ErrorCodeOf(err)), nil)
			return
		}
		if s.Harness != nil {
			if checkpointErr := s.saveHarnessCheckpoint(sessionID, harness.CheckpointEffectAfter, harnessTurnHash, contextVersion, []string{dispatchEvent.ID}, nil, "provider run recorded"); checkpointErr != nil {
				s.problem(w, r, http.StatusServiceUnavailable, "checkpoint_save_failed", checkpointErr.Error(), nil)
				return
			}
		}
		providerName := "local"
		if capabilities, capabilityErr := s.Provider.Capabilities(r.Context()); capabilityErr == nil && capabilities.Provider != "" {
			providerName = capabilities.Provider
		}
		provenance := domain.Provenance{WorkItemID: id, RequirementID: witem.RequirementID, BugID: witem.BugID, AgentBindingID: input.AgentBindingID, Provider: providerName, ProviderTaskID: binding.ProviderRunID, ProviderSessionID: binding.SessionID, ProviderWorkDir: binding.WorkDir, RepositoryID: witem.RepositoryID, ContextVersion: contextVersion}
		if providerBinding, bindingErr := s.Store.GetProviderBinding(input.AgentBindingID); bindingErr == nil {
			provenance.ProviderAgentID = providerBinding.ProviderObjectID
		}
		provenance.ProviderIdempotencyKey = cmd.IdempotencyKey
		if provenanceErr := s.Store.SaveProvenance(provenance); provenanceErr != nil {
			if dispatchEvent.ID != "" {
				_ = s.Harness.NackOutbox(sessionID, dispatchEvent.ID, providerDispatchOwner, time.Now().UTC().Add(time.Second))
			}
			s.problem(w, r, http.StatusServiceUnavailable, "provenance_save_failed", provenanceErr.Error(), nil)
			return
		}
		if dispatchEvent.ID != "" {
			if ackErr := s.Harness.AckOutbox(sessionID, dispatchEvent.ID, providerDispatchOwner, time.Now().UTC()); ackErr != nil {
				s.problem(w, r, http.StatusServiceUnavailable, "outbox_ack_failed", ackErr.Error(), nil)
				return
			}
		}
		_ = s.Events.Publish(r.Context(), events.NewWithContext(r.Context(), "execution.queued.v1", "work_item", id, tenant(r), "", 1, map[string]any{"run_id": binding.ID}))
		s.writeJSON(w, 202, map[string]any{"run": binding, "work_item": witem, "session_id": sessionID, "context_id": contextID, "context_version": contextVersion})
		return
	}
	if r.Method == http.MethodGet {
		s.writeJSON(w, 200, map[string]any{"work_item": witem, "evidence": s.evidenceForRequest(r, s.Store.ListEvidence(id))})
		return
	}
	s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
}
func (s *Server) streamRoute(w http.ResponseWriter, r *http.Request, workspaceID string) {
	parts := strings.Split(strings.Trim(workspaceID, "/"), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		s.problem(w, r, http.StatusNotFound, "not_found", "stream not found", nil)
		return
	}
	workspaceID = strings.TrimSpace(parts[0])
	if !workspaceMatchesRequest(r, workspaceID) {
		s.problem(w, r, http.StatusNotFound, "not_found", "stream not found", nil)
		return
	}
	if len(parts) == 2 && parts[1] == "ack" {
		if r.Method != http.MethodPost {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "POST is required", nil)
			return
		}
		var input struct {
			ConsumerID  string `json:"consumer_id"`
			EventID     string `json:"event_id"`
			AggregateID string `json:"aggregate_id,omitempty"`
		}
		if err := decodeJSON(r, &input); err != nil {
			s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
			return
		}
		tenantID := strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
		if err := s.Events.AckScoped(input.ConsumerID, tenantID, workspaceID, input.AggregateID, input.EventID); err != nil {
			status := http.StatusConflict
			if errors.Is(err, events.ErrInvalidCursor) || strings.Contains(err.Error(), "not retained") {
				status = http.StatusGone
			}
			s.problem(w, r, status, "stream_ack_failed", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"acknowledged": true, "consumer_id": input.ConsumerID, "event_id": input.EventID, "workspace_id": workspaceID})
		return
	}
	if len(parts) == 2 && (parts[1] == "range" || parts[1] == "replay-range") {
		if r.Method != http.MethodGet {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "GET is required", nil)
			return
		}
		from, fromErr := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("from")), 10, 64)
		to, toErr := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("to")), 10, 64)
		if fromErr != nil || toErr != nil || from < 1 || to < from || to-from > 1000 {
			s.problem(w, r, http.StatusBadRequest, "invalid_event_range", "from and to must define a positive range of at most 1001 events", nil)
			return
		}
		tenantID := strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
		items, err := s.Events.ReplayRange(tenantID, workspaceID, r.URL.Query().Get("aggregate_id"), from, to)
		if errors.Is(err, events.ErrInvalidCursor) {
			s.problem(w, r, http.StatusGone, "event_range_unavailable", err.Error(), nil)
			return
		}
		if err != nil {
			s.problem(w, r, http.StatusInternalServerError, "event_range_unavailable", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"items": items, "from_sequence": from, "to_sequence": to, "workspace_id": workspaceID})
		return
	}
	if len(parts) != 1 {
		s.problem(w, r, http.StatusNotFound, "not_found", "stream not found", nil)
		return
	}
	if websocket.IsWebSocketUpgrade(r) {
		s.websocketStream(w, r, workspaceID)
		return
	}
	if r.Method != http.MethodGet {
		s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	tenantID := strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
	consumerID := strings.TrimSpace(r.URL.Query().Get("consumer_id"))
	if consumerID == "" {
		consumerID = "http"
	}
	items, next, err := s.Events.ReplayScoped(consumerID, tenantID, workspaceID, r.URL.Query().Get("aggregate_id"), r.URL.Query().Get("cursor"), queryInt(r, "limit", 100))
	if errors.Is(err, events.ErrInvalidCursor) {
		s.problem(w, r, http.StatusGone, "invalid_cursor", err.Error(), nil)
		return
	}
	if err != nil {
		s.problem(w, r, http.StatusInternalServerError, "event_stream_unavailable", err.Error(), nil)
		return
	}
	s.writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next, "consumer_id": consumerID, "workspace_id": workspaceID})
}

var streamUpgrader = websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 8192, CheckOrigin: func(r *http.Request) bool { return allowedOrigin(r.Header.Get("Origin")) }}

func allowedOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	configured := strings.TrimSpace(os.Getenv("ADRO_ALLOWED_ORIGINS"))
	if configured != "" {
		for _, value := range strings.Split(configured, ",") {
			if strings.TrimSpace(value) == origin {
				return true
			}
		}
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return false
	}
	return u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
}

func (s *Server) websocketStream(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspaceMatchesRequest(r, workspaceID) {
		http.NotFound(w, r)
		return
	}
	conn, err := streamUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	// Subscribe before replay so events published immediately after Upgrade are
	// retained in the buffered channel instead of racing the initial snapshot.
	updates, cancel := s.Events.Subscribe(128)
	defer cancel()
	tenantID := strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
	consumerID := strings.TrimSpace(r.URL.Query().Get("consumer_id"))
	if consumerID == "" {
		consumerID = "websocket"
	}
	initial, _, err := s.Events.ReplayScoped(consumerID, tenantID, workspaceID, r.URL.Query().Get("aggregate_id"), r.URL.Query().Get("cursor"), 250)
	if errors.Is(err, events.ErrInvalidCursor) {
		_ = conn.WriteJSON(map[string]any{"type": "error", "code": "invalid_cursor", "message": err.Error()})
		return
	}
	if err != nil {
		return
	}
	for _, event := range initial {
		if err := conn.WriteJSON(event); err != nil {
			return
		}
	}
	done := make(chan struct{})
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				close(done)
				return
			}
		}
	}()
	for {
		select {
		case <-done:
			return
		case <-r.Context().Done():
			return
		case event := <-updates:
			if event.WorkspaceID != workspaceID {
				continue
			}
			if err := conn.WriteJSON(event); err != nil {
				return
			}
		}
	}
}

func (s *Server) runnerRoute(w http.ResponseWriter, r *http.Request, path string) {
	path = strings.Trim(path, "/")
	workspaceID, tenantID := runnerRequestScope(r)
	if path == "" {
		if r.Method == http.MethodGet {
			items := s.Runners.ListForScope(workspaceID, tenantID)
			if (workspaceID != "" || tenantID != "") && len(items) == 0 && len(s.Runners.List()) > 0 {
				s.problem(w, r, http.StatusNotFound, "not_found", "runner not found", nil)
				return
			}
			s.writeJSON(w, http.StatusOK, map[string]any{"items": items})
			return
		}
		if r.Method != http.MethodPost {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		var input runner.Runner
		if err := decodeJSON(r, &input); err != nil {
			s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
			return
		}
		input.WorkspaceID = requestWorkspace(r, input.WorkspaceID)
		if input.WorkspaceID == "" {
			input.WorkspaceID = "local"
		}
		input.TenantID = strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
		if input.TenantID == "" {
			input.TenantID = input.WorkspaceID
		}
		registered, err := s.Runners.Register(input)
		if err != nil {
			s.problem(w, r, http.StatusUnprocessableEntity, "validation_error", err.Error(), nil)
			return
		}
		s.recordAudit(r, registered.WorkspaceID, "runner.registered", registered.ID, map[string]any{"provider": registered.Provider})
		s.writeJSON(w, http.StatusCreated, registered)
		return
	}
	parts := strings.Split(path, "/")
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		item, err := s.Runners.Get(id)
		if err == nil && runnerInRequestScope(item, workspaceID, tenantID) {
			s.writeJSON(w, http.StatusOK, item)
			return
		}
		s.problem(w, r, http.StatusNotFound, "not_found", "runner not found", nil)
		return
	}
	if len(parts) == 2 && parts[1] == "heartbeat" && r.Method == http.MethodPost {
		if !s.Runners.BelongsToScope(id, workspaceID, tenantID) {
			s.problem(w, r, http.StatusNotFound, "not_found", "runner not found", nil)
			return
		}
		var input struct {
			ActiveRuns int `json:"active_runs"`
		}
		if err := decodeJSON(r, &input); err != nil {
			s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
			return
		}
		updated, err := s.Runners.Heartbeat(id, input.ActiveRuns)
		if err != nil {
			s.problem(w, r, http.StatusNotFound, "not_found", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, updated)
		return
	}
	if len(parts) == 2 && (parts[1] == "drain" || parts[1] == "quarantine") && r.Method == http.MethodPost {
		if !s.Runners.BelongsToScope(id, workspaceID, tenantID) {
			s.problem(w, r, http.StatusNotFound, "not_found", "runner not found", nil)
			return
		}
		status := runner.Draining
		if parts[1] == "quarantine" {
			status = runner.Quarantined
		}
		updated, err := s.Runners.SetStatus(id, status)
		if err != nil {
			s.problem(w, r, http.StatusNotFound, "not_found", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, updated)
		return
	}
	if len(parts) == 2 && parts[1] == "execute" && r.Method == http.MethodPost {
		if !s.Runners.BelongsToScope(id, workspaceID, tenantID) {
			s.problem(w, r, http.StatusNotFound, "not_found", "runner not found", nil)
			return
		}
		var input struct {
			WorkDir   string            `json:"work_dir"`
			Command   []string          `json:"command"`
			Env       map[string]string `json:"env"`
			TimeoutMS int64             `json:"timeout_ms"`
		}
		if err := decodeJSON(r, &input); err != nil {
			s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
			return
		}
		request := runner.ExecuteRequest{RunnerID: id, WorkDir: input.WorkDir, Command: input.Command, Env: input.Env}
		if input.TimeoutMS > 0 {
			request.Timeout = time.Duration(input.TimeoutMS) * time.Millisecond
		}
		result, err := s.Runners.Execute(r.Context(), request)
		commandDigest := hashBytes([]byte(strings.Join(input.Command, "\x00")))
		if err != nil {
			s.recordAudit(r, r.Header.Get("X-Workspace-ID"), "runner.execution.failed", id, map[string]any{"command_sha256": commandDigest, "error": err.Error()})
			s.problem(w, r, http.StatusUnprocessableEntity, "runner_execution_failed", err.Error(), map[string]any{"command_sha256": commandDigest})
			return
		}
		s.recordAudit(r, r.Header.Get("X-Workspace-ID"), "runner.execution.completed", id, map[string]any{"command_sha256": commandDigest, "exit_code": result.ExitCode, "duration_ms": result.DurationMS})
		s.writeJSON(w, http.StatusOK, result)
		return
	}
	s.problem(w, r, http.StatusNotFound, "not_found", "runner route not found", nil)
}

func (s *Server) repositoryRoute(w http.ResponseWriter, r *http.Request, path string) {
	path = strings.Trim(path, "/")
	if path == "" {
		if r.Method == http.MethodGet {
			s.writeJSON(w, http.StatusOK, map[string]any{"items": s.Store.ListRepositories(r.Header.Get("X-Workspace-ID"))})
			return
		}
		if r.Method != http.MethodPost {
			s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
			return
		}
		var input domain.Repository
		if err := decodeJSON(r, &input); err != nil {
			s.problem(w, r, 400, "invalid_json", err.Error(), nil)
			return
		}
		input.WorkspaceID = requestWorkspace(r, input.WorkspaceID)
		saved, err := s.Store.UpsertRepository(input)
		if err != nil {
			s.problem(w, r, 422, "validation_error", err.Error(), nil)
			return
		}
		s.writeJSON(w, 201, saved)
		return
	}
	parts := strings.Split(path, "/")
	id := parts[0]
	if len(parts) == 2 && parts[1] == "index" && r.Method == http.MethodPost {
		var input struct {
			Commit string `json:"commit"`
		}
		if err := decodeJSON(r, &input); err != nil {
			s.problem(w, r, 400, "invalid_json", err.Error(), nil)
			return
		}
		if input.Commit == "" {
			input.Commit = "working-tree"
		}
		repository, err := s.Store.GetRepository(id)
		if err != nil || !workspaceMatchesRequest(r, repository.WorkspaceID) {
			s.problem(w, r, http.StatusNotFound, "not_found", "repository not found", nil)
			return
		}
		saved, err := s.Store.MarkRepositoryIndexed(id, input.Commit)
		if err != nil {
			s.problem(w, r, 404, "not_found", err.Error(), nil)
			return
		}
		_ = s.Events.Publish(r.Context(), events.NewWithContext(r.Context(), "repository.indexed.v1", "repository", id, tenant(r), saved.WorkspaceID, 1, map[string]any{"commit": input.Commit}))
		s.writeJSON(w, 200, saved)
		return
	}
	if len(parts) == 1 && (r.Method == http.MethodPatch || r.Method == http.MethodDelete) {
		saved, err := s.Store.GetRepository(id)
		if err != nil {
			s.problem(w, r, 404, "not_found", "repository not found", nil)
			return
		}
		if !workspaceMatchesRequest(r, saved.WorkspaceID) {
			s.problem(w, r, http.StatusNotFound, "not_found", "repository not found", nil)
			return
		}
		if r.Method == http.MethodDelete {
			if err := s.Store.DeleteRepository(id); err != nil {
				s.problem(w, r, 404, "not_found", err.Error(), nil)
				return
			}
			s.recordAudit(r, saved.WorkspaceID, "repository.deleted", id, nil)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var patch domain.Repository
		if err := decodeJSON(r, &patch); err != nil {
			s.problem(w, r, 400, "invalid_json", err.Error(), nil)
			return
		}
		patch.ID = id
		patch.WorkspaceID = requestWorkspace(r, saved.WorkspaceID)
		if patch.CanonicalName == "" {
			patch.CanonicalName = saved.CanonicalName
		}
		if patch.CloneURL == "" {
			patch.CloneURL = saved.CloneURL
		}
		if patch.Provider == "" {
			patch.Provider = saved.Provider
		}
		if patch.DefaultBranch == "" {
			patch.DefaultBranch = saved.DefaultBranch
		}
		if patch.LanguageSet == nil {
			patch.LanguageSet = saved.LanguageSet
		}
		if patch.Metadata == nil {
			patch.Metadata = saved.Metadata
		}
		if patch.IndexStatus == "" {
			patch.IndexStatus = saved.IndexStatus
		}
		updated, err := s.Store.UpsertRepository(patch)
		if err != nil {
			s.problem(w, r, 422, "validation_error", err.Error(), nil)
			return
		}
		s.writeJSON(w, 200, updated)
		return
	}
	if r.Method == http.MethodGet {
		saved, err := s.Store.GetRepository(id)
		if err != nil {
			s.problem(w, r, 404, "not_found", "repository not found", nil)
			return
		}
		if !workspaceMatchesRequest(r, saved.WorkspaceID) {
			s.problem(w, r, http.StatusNotFound, "not_found", "repository not found", nil)
			return
		}
		s.writeJSON(w, 200, saved)
		return
	}
	s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
}

func (s *Server) repositoryGraph(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.Header.Get("X-Workspace-ID")
	repositories := s.Store.ListRepositories(workspaceID)
	nodes := make([]map[string]any, 0, len(repositories))
	edges := make([]map[string]any, 0)
	for _, repository := range repositories {
		nodes = append(nodes, map[string]any{"id": repository.ID, "name": repository.CanonicalName, "indexed_commit": repository.IndexedCommit, "index_status": repository.IndexStatus})
		for _, key := range []string{"depends_on", "dependencies", "calls"} {
			if raw, ok := repository.Metadata[key].([]any); ok {
				for _, value := range raw {
					if target, ok := value.(string); ok && target != "" {
						edges = append(edges, map[string]any{"from": repository.ID, "to": target, "relation": key})
					}
				}
			}
			if raw, ok := repository.Metadata[key].([]string); ok {
				for _, target := range raw {
					if target != "" {
						edges = append(edges, map[string]any{"from": repository.ID, "to": target, "relation": key})
					}
				}
			}
		}
	}
	s.writeJSON(w, 200, map[string]any{"nodes": nodes, "edges": edges})
}

func (s *Server) teamWorkspaceRoute(w http.ResponseWriter, r *http.Request, path string) {
	path = strings.Trim(path, "/")
	if path != "" {
		if r.Method == http.MethodGet {
			workspace, err := s.Store.GetTeamWorkspace(strings.Split(path, "/")[0])
			if err != nil {
				s.problem(w, r, 404, "not_found", "team workspace not found", nil)
				return
			}
			if !workspaceMatchesRequest(r, workspace.WorkspaceID) {
				s.problem(w, r, http.StatusNotFound, "not_found", "team workspace not found", nil)
				return
			}
			s.writeJSON(w, 200, workspace)
			return
		}
		s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	if r.Method == http.MethodGet {
		s.writeJSON(w, 200, map[string]any{"items": s.Store.ListTeamWorkspaces(r.Header.Get("X-Workspace-ID"))})
		return
	}
	if r.Method != http.MethodPost {
		s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	var input domain.TeamWorkspace
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, 400, "invalid_json", err.Error(), nil)
		return
	}
	input.WorkspaceID = requestWorkspace(r, input.WorkspaceID)
	saved, err := s.Store.UpsertTeamWorkspace(input)
	if err != nil {
		s.problem(w, r, 422, "validation_error", err.Error(), nil)
		return
	}
	s.writeJSON(w, 201, saved)
}

func (s *Server) mcpRoute(w http.ResponseWriter, r *http.Request, path string) {
	path = strings.Trim(path, "/")
	if path == "invocations" {
		if r.Method != http.MethodGet {
			s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
			return
		}
		s.writeJSON(w, 200, map[string]any{"items": s.Store.ListMCPInvocations(r.Header.Get("X-Workspace-ID"))})
		return
	}
	if path == "servers" {
		path = ""
	}
	if strings.HasPrefix(path, "servers/") {
		path = strings.TrimPrefix(path, "servers/")
	}
	if path != "" {
		id := strings.Split(path, "/")[0]
		for _, item := range s.Store.ListMCPServers(r.Header.Get("X-Workspace-ID")) {
			if item.ID != id {
				continue
			}
			parts := strings.Split(path, "/")
			switch {
			case len(parts) == 2 && r.Method == http.MethodPost && (parts[1] == "discover" || parts[1] == "health-check" || parts[1] == "approve"):
				updated := item
				if parts[1] == "discover" {
					_, digest, discoverErr := (mcpclient.Client{}).Discover(r.Context(), item)
					if discoverErr != nil {
						// Discovery is an operator probe. Keep the resource addressable
						// when a remote server is offline, but never fabricate a schema
						// digest or mark it healthy.
						updated.Status = "unreachable"
						saved, saveErr := s.Store.UpsertMCPServer(updated)
						if saveErr != nil {
							s.problem(w, r, http.StatusUnprocessableEntity, "mcp_update_failed", saveErr.Error(), nil)
							return
						}
						s.recordAudit(r, saved.WorkspaceID, "mcp.discover.failed", saved.ID, map[string]any{"error": discoverErr.Error()})
						s.writeJSON(w, http.StatusOK, map[string]any{"server": saved, "reachable": false, "error": "mcp_discovery_failed"})
						return
					}
					updated.SchemaDigest = digest
				}
				if parts[1] == "health-check" {
					if healthErr := (mcpclient.Client{}).Health(r.Context(), item); healthErr != nil {
						updated.Status = "unreachable"
						saved, saveErr := s.Store.UpsertMCPServer(updated)
						if saveErr != nil {
							s.problem(w, r, http.StatusUnprocessableEntity, "mcp_update_failed", saveErr.Error(), nil)
							return
						}
						s.recordAudit(r, saved.WorkspaceID, "mcp.health-check.failed", saved.ID, map[string]any{"error": healthErr.Error()})
						s.writeJSON(w, http.StatusOK, map[string]any{"server": saved, "reachable": false, "error": "mcp_health_check_failed"})
						return
					}
				}
				if parts[1] == "approve" {
					updated.Status = "approved"
				}
				if parts[1] == "health-check" {
					updated.Status = "healthy"
				}
				saved, err := s.Store.UpsertMCPServer(updated)
				if err != nil {
					s.problem(w, r, 422, "mcp_update_failed", err.Error(), nil)
					return
				}
				s.recordAudit(r, saved.WorkspaceID, "mcp."+parts[1], saved.ID, nil)
				s.writeJSON(w, 200, saved)
			case len(parts) == 1 && r.Method == http.MethodPost:
				var input struct {
					Tool    string         `json:"tool"`
					Request map[string]any `json:"request"`
				}
				if err := decodeJSON(r, &input); err != nil {
					s.problem(w, r, 400, "invalid_json", err.Error(), nil)
					return
				}
				if strings.TrimSpace(input.Tool) == "" {
					s.problem(w, r, http.StatusUnprocessableEntity, "tool_required", "tool is required", nil)
					return
				}
				if containsSecret(input.Request) {
					s.problem(w, r, http.StatusUnprocessableEntity, "secret_reference_required", "MCP invocation arguments must not contain credentials", nil)
					return
				}
				started := time.Now()
				response, invokeErr := (mcpclient.Client{}).Invoke(r.Context(), item, input.Tool, input.Request)
				invocationStatus := "completed"
				if invokeErr != nil {
					invocationStatus = "failed"
				}
				if response == nil {
					response = map[string]any{}
				}
				if invokeErr != nil {
					response = map[string]any{"error": invokeErr.Error()}
				}
				invocation, err := s.Store.SaveMCPInvocation(domain.MCPInvocation{WorkspaceID: item.WorkspaceID, ServerID: item.ID, Tool: input.Tool, Request: input.Request, Response: response, Status: invocationStatus, DurationMS: time.Since(started).Milliseconds()})
				if err != nil {
					s.problem(w, r, 422, "invocation_failed", err.Error(), nil)
					return
				}
				auditAction := "mcp.invoked"
				if invokeErr != nil {
					auditAction = "mcp.invocation_failed"
				}
				s.recordAudit(r, item.WorkspaceID, auditAction, invocation.ID, map[string]any{"server_id": item.ID, "tool": input.Tool, "duration_ms": invocation.DurationMS})
				if invokeErr != nil {
					s.problem(w, r, http.StatusBadGateway, "mcp_invocation_failed", invokeErr.Error(), map[string]any{"invocation": invocation})
					return
				}
				s.writeJSON(w, 200, invocation)
			case r.Method == http.MethodGet:
				s.writeJSON(w, 200, item)
			case r.Method == http.MethodDelete:
				if err := s.Store.DeleteMCPServer(id); err != nil {
					s.problem(w, r, 404, "not_found", err.Error(), nil)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			case r.Method == http.MethodPatch:
				var input domain.MCPServer
				if err := decodeJSON(r, &input); err != nil {
					s.problem(w, r, 400, "invalid_json", err.Error(), nil)
					return
				}
				if containsSecret(input.Configuration) {
					s.problem(w, r, 422, "secret_reference_required", "MCP credentials must be stored in secret_ref", nil)
					return
				}
				input.ID = id
				input.WorkspaceID = requestWorkspace(r, item.WorkspaceID)
				if input.Name == "" {
					input.Name = item.Name
				}
				if input.Endpoint == "" {
					input.Endpoint = item.Endpoint
				}
				if input.Protocol == "" {
					input.Protocol = item.Protocol
				}
				if input.Configuration == nil {
					input.Configuration = item.Configuration
				}
				saved, err := s.Store.UpsertMCPServer(input)
				if err != nil {
					s.problem(w, r, 422, "validation_error", err.Error(), nil)
					return
				}
				s.writeJSON(w, 200, saved)
			default:
				s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
			}
			return
		}
		s.problem(w, r, 404, "not_found", "MCP server not found", nil)
		return
	}
	if r.Method == http.MethodGet {
		s.writeJSON(w, 200, map[string]any{"items": s.Store.ListMCPServers(r.Header.Get("X-Workspace-ID"))})
		return
	}
	if r.Method != http.MethodPost {
		s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	var input domain.MCPServer
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, 400, "invalid_json", err.Error(), nil)
		return
	}
	if containsSecret(input.Configuration) {
		s.problem(w, r, 422, "secret_reference_required", "MCP credentials must be stored in secret_ref", nil)
		return
	}
	input.WorkspaceID = requestWorkspace(r, input.WorkspaceID)
	saved, err := s.Store.UpsertMCPServer(input)
	if err != nil {
		s.problem(w, r, 422, "validation_error", err.Error(), nil)
		return
	}
	s.writeJSON(w, 201, saved)
}

func (s *Server) skillRoute(w http.ResponseWriter, r *http.Request, path string) {
	path = strings.Trim(path, "/")
	if path != "" {
		id := strings.Split(path, "/")[0]
		for _, item := range s.Store.ListSkills(r.Header.Get("X-Workspace-ID")) {
			if item.ID != id {
				continue
			}
			parts := strings.Split(path, "/")
			switch {
			case len(parts) == 2 && parts[1] == "versions" && r.Method == http.MethodPost:
				var input domain.Skill
				if err := decodeJSON(r, &input); err != nil {
					s.problem(w, r, 400, "invalid_json", err.Error(), nil)
					return
				}
				input.ID, input.WorkspaceID = id, item.WorkspaceID
				if input.Name == "" {
					input.Name = item.Name
				}
				if input.Kind == "" {
					input.Kind = item.Kind
				}
				saved, err := s.Store.UpsertSkill(input)
				if err != nil {
					s.problem(w, r, 422, "validation_error", err.Error(), nil)
					return
				}
				s.writeJSON(w, 201, saved)
			case len(parts) == 2 && (parts[1] == "publish" || parts[1] == "rollback") && r.Method == http.MethodPost:
				updated := item
				if parts[1] == "publish" {
					updated.Status = "published"
				} else {
					updated.Status = "rolled_back"
				}
				saved, err := s.Store.UpsertSkill(updated)
				if err != nil {
					s.problem(w, r, 422, "skill_update_failed", err.Error(), nil)
					return
				}
				s.recordAudit(r, saved.WorkspaceID, "skill."+parts[1], saved.ID, map[string]any{"version": saved.Version})
				s.writeJSON(w, 200, saved)
			case len(parts) == 1 && r.Method == http.MethodGet:
				s.writeJSON(w, 200, item)
			case len(parts) == 1 && r.Method == http.MethodDelete:
				if err := s.Store.DeleteSkill(id); err != nil {
					s.problem(w, r, 404, "not_found", err.Error(), nil)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			case len(parts) == 1 && r.Method == http.MethodPatch:
				var input domain.Skill
				if err := decodeJSON(r, &input); err != nil {
					s.problem(w, r, 400, "invalid_json", err.Error(), nil)
					return
				}
				input.ID = id
				input.WorkspaceID = requestWorkspace(r, item.WorkspaceID)
				if input.Name == "" {
					input.Name = item.Name
				}
				if input.Version == "" {
					input.Version = item.Version
				}
				saved, err := s.Store.UpsertSkill(input)
				if err != nil {
					s.problem(w, r, 422, "validation_error", err.Error(), nil)
					return
				}
				s.writeJSON(w, 200, saved)
			default:
				s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
			}
			return
		}
		s.problem(w, r, 404, "not_found", "skill not found", nil)
		return
	}
	if r.Method == http.MethodGet {
		s.writeJSON(w, 200, map[string]any{"items": s.Store.ListSkills(r.Header.Get("X-Workspace-ID"))})
		return
	}
	if r.Method != http.MethodPost {
		s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	var input domain.Skill
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, 400, "invalid_json", err.Error(), nil)
		return
	}
	input.WorkspaceID = requestWorkspace(r, input.WorkspaceID)
	saved, err := s.Store.UpsertSkill(input)
	if err != nil {
		s.problem(w, r, 422, "validation_error", err.Error(), nil)
		return
	}
	s.writeJSON(w, 201, saved)
}

func (s *Server) automationRoute(w http.ResponseWriter, r *http.Request, path string) {
	path = strings.Trim(path, "/")
	if path != "" {
		id := strings.Split(path, "/")[0]
		for _, item := range s.Store.ListAutomations(r.Header.Get("X-Workspace-ID")) {
			if item.ID != id {
				continue
			}
			parts := strings.Split(path, "/")
			switch {
			case len(parts) == 2 && parts[1] == "runs" && r.Method == http.MethodGet:
				s.writeJSON(w, 200, map[string]any{"items": s.Store.ListAutomationRuns(id)})
			case len(parts) == 2 && parts[1] == "publish" && r.Method == http.MethodPost:
				updated := item
				updated.Enabled = true
				saved, err := s.Store.UpsertAutomation(updated)
				if err != nil {
					s.problem(w, r, 422, "automation_update_failed", err.Error(), nil)
					return
				}
				s.recordAudit(r, saved.WorkspaceID, "automation.published", saved.ID, nil)
				s.writeJSON(w, 200, saved)
			case len(parts) == 2 && parts[1] == "pause" && r.Method == http.MethodPost:
				updated := item
				updated.Enabled = false
				saved, err := s.Store.UpsertAutomation(updated)
				if err != nil {
					s.problem(w, r, 422, "automation_update_failed", err.Error(), nil)
					return
				}
				s.writeJSON(w, 200, saved)
			case len(parts) == 2 && parts[1] == "trigger" && r.Method == http.MethodPost:
				var input map[string]any
				if err := decodeJSON(r, &input); err != nil && err != io.EOF {
					s.problem(w, r, 400, "invalid_json", err.Error(), nil)
					return
				}
				if !item.Enabled {
					s.problem(w, r, 409, "automation_paused", "automation is paused", nil)
					return
				}
				run, err := s.Store.CreateAutomationRun(domain.AutomationRun{AutomationID: item.ID, WorkspaceID: item.WorkspaceID, Input: input})
				if err != nil {
					s.problem(w, r, 422, "automation_run_failed", err.Error(), nil)
					return
				}
				s.recordAudit(r, item.WorkspaceID, "automation.triggered", run.ID, map[string]any{"automation_id": item.ID})
				s.writeJSON(w, http.StatusAccepted, run)
			case len(parts) == 1 && r.Method == http.MethodGet:
				s.writeJSON(w, 200, item)
			case len(parts) == 1 && r.Method == http.MethodDelete:
				if err := s.Store.DeleteAutomation(id); err != nil {
					s.problem(w, r, 404, "not_found", err.Error(), nil)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			case len(parts) == 1 && r.Method == http.MethodPatch:
				var input domain.Automation
				if err := decodeJSON(r, &input); err != nil {
					s.problem(w, r, 400, "invalid_json", err.Error(), nil)
					return
				}
				input.ID = id
				input.WorkspaceID = requestWorkspace(r, item.WorkspaceID)
				saved, err := s.Store.UpsertAutomation(input)
				if err != nil {
					s.problem(w, r, 422, "validation_error", err.Error(), nil)
					return
				}
				s.writeJSON(w, 200, saved)
			default:
				s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
			}
			return
		}
		s.problem(w, r, 404, "not_found", "automation not found", nil)
		return
	}
	if r.Method == http.MethodGet {
		s.writeJSON(w, 200, map[string]any{"items": s.Store.ListAutomations(r.Header.Get("X-Workspace-ID"))})
		return
	}
	if r.Method != http.MethodPost {
		s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	var input domain.Automation
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, 400, "invalid_json", err.Error(), nil)
		return
	}
	input.WorkspaceID = requestWorkspace(r, input.WorkspaceID)
	saved, err := s.Store.UpsertAutomation(input)
	if err != nil {
		s.problem(w, r, 422, "validation_error", err.Error(), nil)
		return
	}
	s.writeJSON(w, 201, saved)
}

func (s *Server) automationRunRoute(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		s.problem(w, r, 404, "not_found", "automation run not found", nil)
		return
	}
	run, err := s.Store.GetAutomationRun(parts[0])
	if err != nil {
		s.problem(w, r, 404, "not_found", "automation run not found", nil)
		return
	}
	if !workspaceMatchesRequest(r, run.WorkspaceID) {
		s.problem(w, r, http.StatusNotFound, "not_found", "automation run not found", nil)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		s.writeJSON(w, 200, run)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodPost {
		var status, actor string
		switch parts[1] {
		case "cancel":
			status = "cancelled"
		case "takeover":
			status, actor = "running", r.Header.Get("X-Member-ID")
			if actor == "" {
				actor = "local-user"
			}
		}
		if status == "" {
			s.problem(w, r, 404, "not_found", "automation run action not found", nil)
			return
		}
		updated, err := s.Store.UpdateAutomationRun(run.ID, status, actor)
		if err != nil {
			s.problem(w, r, 409, "automation_run_update_failed", err.Error(), nil)
			return
		}
		s.recordAudit(r, updated.WorkspaceID, "automation-run."+parts[1], updated.ID, nil)
		s.writeJSON(w, 200, updated)
		return
	}
	s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
}

func (s *Server) agentBindingRoute(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		s.problem(w, r, 404, "not_found", "agent route not found", nil)
		return
	}
	agentID, kind := parts[0], ""
	switch parts[1] {
	case "mcp-bindings":
		kind = "mcp"
	case "skill-bindings":
		kind = "skill"
	default:
		s.problem(w, r, 404, "not_found", "agent route not found", nil)
		return
	}
	workspaceID := r.Header.Get("X-Workspace-ID")
	if len(parts) == 2 && r.Method == http.MethodGet {
		s.writeJSON(w, 200, map[string]any{"items": s.Store.ListBindings(workspaceID, agentID, kind)})
		return
	}
	if len(parts) == 2 && r.Method == http.MethodPost {
		var input struct {
			WorkspaceID  string `json:"workspace_id"`
			CapabilityID string `json:"capability_id"`
			Enabled      *bool  `json:"enabled"`
		}
		if err := decodeJSON(r, &input); err != nil {
			s.problem(w, r, 400, "invalid_json", err.Error(), nil)
			return
		}
		input.WorkspaceID = requestWorkspace(r, input.WorkspaceID)
		enabled := true
		if input.Enabled != nil {
			enabled = *input.Enabled
		}
		binding, err := s.Store.SaveBinding(domain.CapabilityBinding{WorkspaceID: input.WorkspaceID, AgentID: agentID, CapabilityID: input.CapabilityID, Kind: kind, Enabled: enabled})
		if err != nil {
			s.problem(w, r, 422, "validation_error", err.Error(), nil)
			return
		}
		s.recordAudit(r, binding.WorkspaceID, "agent.binding.created", binding.ID, map[string]any{"kind": kind, "agent_id": agentID})
		s.writeJSON(w, 201, binding)
		return
	}
	if len(parts) == 3 && r.Method == http.MethodDelete {
		found := false
		for _, binding := range s.Store.ListBindings(workspaceID, agentID, kind) {
			if binding.ID == parts[2] {
				found = true
				break
			}
		}
		if !found {
			s.problem(w, r, http.StatusNotFound, "not_found", "binding not found", nil)
			return
		}
		if err := s.Store.DeleteBinding(parts[2]); err != nil {
			s.problem(w, r, 404, "not_found", err.Error(), nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(parts) == 3 && r.Method == http.MethodPatch {
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		if err := decodeJSON(r, &input); err != nil {
			s.problem(w, r, 400, "invalid_json", err.Error(), nil)
			return
		}
		for _, binding := range s.Store.ListBindings(workspaceID, agentID, kind) {
			if binding.ID == parts[2] {
				if input.Enabled != nil {
					binding.Enabled = *input.Enabled
				}
				saved, err := s.Store.SaveBinding(binding)
				if err != nil {
					s.problem(w, r, 422, "binding_update_failed", err.Error(), nil)
					return
				}
				s.writeJSON(w, 200, saved)
				return
			}
		}
		s.problem(w, r, 404, "not_found", "binding not found", nil)
		return
	}
	s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
}

func (s *Server) profileRoute(w http.ResponseWriter, r *http.Request, path string) {
	memberID := strings.Trim(strings.TrimPrefix(path, "/"), "/")
	workspaceID := r.Header.Get("X-Workspace-ID")
	if memberID == "" && r.Method == http.MethodGet {
		s.writeJSON(w, 200, map[string]any{"items": s.Store.ListDeveloperProfiles(workspaceID)})
		return
	}
	if memberID == "" {
		s.problem(w, r, 400, "member_required", "member id is required", nil)
		return
	}
	if r.Method == http.MethodGet {
		profile, err := s.Store.GetDeveloperProfile(workspaceID, memberID)
		if err != nil {
			s.problem(w, r, 404, "not_found", "developer profile not found", nil)
			return
		}
		s.writeJSON(w, 200, profile)
		return
	}
	if r.Method != http.MethodPatch && r.Method != http.MethodPost {
		s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	var profile domain.DeveloperProfile
	if err := decodeJSON(r, &profile); err != nil {
		s.problem(w, r, 400, "invalid_json", err.Error(), nil)
		return
	}
	profile.MemberID = memberID
	profile.WorkspaceID = requestWorkspace(r, profile.WorkspaceID)
	saved, err := s.Store.UpsertDeveloperProfile(profile)
	if err != nil {
		s.problem(w, r, 422, "validation_error", err.Error(), nil)
		return
	}
	s.recordAudit(r, saved.WorkspaceID, "developer-profile.upserted", saved.ID, map[string]any{"member_id": saved.MemberID, "role": saved.DefaultRole})
	s.writeJSON(w, 200, saved)
}

func (s *Server) approvalRoute(w http.ResponseWriter, r *http.Request, path string) {
	path = strings.Trim(path, "/")
	if path != "" {
		parts := strings.Split(path, "/")
		if len(parts) == 2 && parts[1] == "decide" && r.Method == http.MethodPost {
			approval, err := s.Store.GetApproval(parts[0])
			if err != nil || !workspaceMatchesRequest(r, approval.WorkspaceID) {
				s.problem(w, r, http.StatusNotFound, "not_found", "approval not found", nil)
				return
			}
			var input struct {
				Decision string `json:"decision"`
				Reason   string `json:"reason"`
			}
			if err := decodeJSON(r, &input); err != nil {
				s.problem(w, r, 400, "invalid_json", err.Error(), nil)
				return
			}
			if input.Decision != "approved" && input.Decision != "rejected" {
				s.problem(w, r, 422, "invalid_decision", "decision must be approved or rejected", nil)
				return
			}
			saved, err := s.Store.DecideApproval(parts[0], input.Decision, r.Header.Get("X-Member-ID"), input.Reason)
			if err != nil {
				code := 409
				if errors.Is(err, store.ErrNotFound) {
					code = 404
				}
				s.problem(w, r, code, "approval_failed", err.Error(), nil)
				return
			}
			s.recordAudit(r, saved.WorkspaceID, "approval.decided", saved.ID, map[string]any{"decision": saved.Decision})
			if saved.Kind == "design" {
				if resumed, resumeErr := s.resumePipelineAfterApproval(saved); resumeErr != nil {
					s.problem(w, r, http.StatusConflict, "pipeline_resume_failed", resumeErr.Error(), map[string]any{"approval_id": saved.ID})
					return
				} else if resumed.ID != "" {
					s.writeJSON(w, http.StatusOK, map[string]any{"approval": saved, "pipeline": resumed})
					return
				}
			}
			s.writeJSON(w, 200, saved)
			return
		}
		s.problem(w, r, 404, "not_found", "approval not found", nil)
		return
	}
	if r.Method == http.MethodPost {
		var input domain.Approval
		if err := decodeJSON(r, &input); err != nil {
			s.problem(w, r, 400, "invalid_json", err.Error(), nil)
			return
		}
		input.WorkspaceID = requestWorkspace(r, input.WorkspaceID)
		saved, err := s.Store.CreateApproval(input)
		if err != nil {
			s.problem(w, r, 422, "validation_error", err.Error(), nil)
			return
		}
		s.recordAudit(r, saved.WorkspaceID, "approval.requested", saved.ID, map[string]any{"kind": saved.Kind})
		s.writeJSON(w, 201, saved)
		return
	}
	s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
}

func (s *Server) evidenceRoute(w http.ResponseWriter, r *http.Request, path string) {
	path = strings.Trim(path, "/")
	if path != "" {
		s.problem(w, r, 404, "not_found", "evidence route not found", nil)
		return
	}
	if r.Method == http.MethodGet {
		workItemID := r.URL.Query().Get("work_item_id")
		workspaceID := requestWorkspace(r, "")
		if workItemID != "" {
			workItem, err := s.Store.GetWorkItem(workItemID)
			if err != nil || (workspaceID != "" && !s.workItemBelongsToWorkspace(workItem, workspaceID)) {
				s.problem(w, r, http.StatusNotFound, "not_found", "evidence not found", nil)
				return
			}
		}
		s.writeJSON(w, 200, map[string]any{"items": s.evidenceForRequest(r, s.Store.ListEvidence(workItemID))})
		return
	}
	if r.Method != http.MethodPost {
		s.problem(w, r, 405, "method_not_allowed", "method not allowed", nil)
		return
	}
	var input domain.EvidenceBundle
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, 400, "invalid_json", err.Error(), nil)
		return
	}
	if input.ID == "" {
		input.ID = domain.NewID()
	}
	if input.Status == "" {
		input.Status = "created"
	}
	if input.ContentSHA256 == "" {
		input.ContentSHA256 = hashBytes([]byte(mustJSON(input.Summary)))
	}
	input.WorkspaceID = requestWorkspace(r, input.WorkspaceID)
	if input.WorkspaceID == "" {
		s.problem(w, r, http.StatusUnprocessableEntity, "validation_error", "workspace_id is required", nil)
		return
	}
	if input.WorkItemID != "" {
		workItem, err := s.Store.GetWorkItem(input.WorkItemID)
		workspaceID := requestWorkspace(r, "")
		if err != nil || (workspaceID != "" && !s.workItemBelongsToWorkspace(workItem, workspaceID)) {
			s.problem(w, r, http.StatusUnprocessableEntity, "invalid_workspace", "work item does not belong to this workspace", nil)
			return
		}
	}
	if err := s.Store.SaveEvidence(input); err != nil {
		s.problem(w, r, 422, "validation_error", err.Error(), nil)
		return
	}
	s.recordAudit(r, input.WorkspaceID, "evidence.created", input.ID, map[string]any{"kind": input.Kind})
	s.writeJSON(w, 201, input)
}
func mustJSON(value any) string { data, _ := json.Marshal(value); return string(data) }
func (s *Server) eventList(id string, r *http.Request) []events.Envelope {
	items, _ := s.Events.List(id, r.URL.Query().Get("cursor"), 100)
	return items
}

func (s *Server) materializeWorkItems(ctx context.Context, req domain.Requirement) error {
	// The reference store is process-local. Serialize the provider side effect
	// with local persistence so a failed create can be retried without allowing
	// a concurrent request to create the same remote Issue.
	s.materializeMu.Lock()
	defer s.materializeMu.Unlock()
	if len(req.RepositoryIDs) == 0 || len(req.AssigneeMemberIDs) == 0 {
		return nil
	}
	if err := s.validateRequirementRepositoryRelations(req.WorkspaceID, req.RepositoryIDs); err != nil {
		return err
	}
	existing := s.Store.ListWorkItems(req.ID)
	known := make(map[string]domain.WorkItem, len(existing))
	for _, item := range existing {
		known[item.RepositoryID] = item
	}
	persistedDecision := func(item domain.WorkItem) (provider.RouteDecision, error) {
		decision := provider.RouteDecision{Source: item.AgentRouteSource, ConfigRevision: item.RoutingConfigRevision}
		if item.DeveloperAgentBindingID == "" {
			return decision, nil
		}
		binding, bindingErr := s.Store.GetProviderBinding(item.DeveloperAgentBindingID)
		if bindingErr != nil {
			return provider.RouteDecision{}, fmt.Errorf("work item provider binding unavailable")
		}
		if binding.Provider == "" || binding.Kind != "agent" || binding.ProviderObjectID == "" {
			return provider.RouteDecision{}, fmt.Errorf("work item provider binding is invalid")
		}
		decision.Binding = binding
		decision.ProviderAssigneeID = binding.ProviderObjectID
		decision.AssigneeType = "agent"
		if decision.Source == "" {
			decision.Source = binding.Source
		}
		if decision.ConfigRevision == "" {
			decision.ConfigRevision = binding.ConfigRevision
		}
		return decision, nil
	}
	for i, repositoryID := range req.RepositoryIDs {
		item, exists := known[repositoryID]
		if exists && item.ProviderIssueID != "" {
			continue
		}
		memberID := req.AssigneeMemberIDs[i%len(req.AssigneeMemberIDs)]
		decision := provider.RouteDecision{Source: "unassigned"}
		if exists {
			// A previous provider call failed. Retry with the original immutable
			// route instead of resolving against possibly changed configuration.
			memberID = item.MemberID
			var err error
			decision, err = persistedDecision(item)
			if err != nil {
				return err
			}
		} else {
			profile := domain.DeveloperProfile{}
			if storedProfile, profileErr := s.Store.GetDeveloperProfile(req.WorkspaceID, memberID); profileErr == nil {
				profile = storedProfile
			}
			var profileBinding *domain.ProviderBinding
			if profile.DefaultAgentBindingID != "" {
				resolved, bindingErr := s.Store.GetProviderBinding(profile.DefaultAgentBindingID)
				if bindingErr != nil {
					return fmt.Errorf("developer profile binding unavailable")
				}
				profileBinding = &resolved
			}
			if s.Router != nil {
				decision = s.Router.Resolve(req.WorkspaceID, memberID, profile.DefaultRole, profileBinding)
			}
			if decision.Binding.ID != "" {
				if _, err := s.Store.SaveProviderBinding(decision.Binding); err != nil {
					return err
				}
			}
			var err error
			item, _, err = s.Store.CreateWorkItemIfAbsent(domain.WorkItem{
				RequirementID: req.ID, RepositoryID: repositoryID, MemberID: memberID,
				DeveloperAgentBindingID: decision.Binding.ID, Role: profile.DefaultRole,
				AgentRouteSource: decision.Source, RoutingConfigRevision: decision.ConfigRevision, Stage: 1,
			})
			if err != nil {
				return err
			}
			if item.ProviderIssueID != "" {
				known[repositoryID] = item
				continue
			}
			if item.ID != "" && item.DeveloperAgentBindingID != "" && item.DeveloperAgentBindingID != decision.Binding.ID {
				// Another process won the atomic insert. Reconstruct its persisted
				// route before performing the provider side effect.
				decision, err = persistedDecision(item)
				if err != nil {
					return err
				}
			}
		}
		repositoryPath, cloneURL, defaultBranch := "", "", ""
		if repository, repositoryErr := s.Store.GetRepository(repositoryID); repositoryErr == nil {
			cloneURL, defaultBranch = repository.CloneURL, repository.DefaultBranch
			if repository.Metadata != nil {
				if value, ok := repository.Metadata["local_path"].(string); ok {
					repositoryPath = strings.TrimSpace(value)
				}
			}
		}
		binding, err := s.Provider.CreateWorkItem(ctx, provider.WorkItemSpec{
			ID: item.ID, RequirementID: req.ID, RepositoryID: repositoryID, MemberID: memberID,
			WorkspaceID:        req.WorkspaceID,
			Title:              fmt.Sprintf("%s / %s", req.Key, repositoryID),
			Description:        req.Description,
			ProviderAssigneeID: decision.ProviderAssigneeID,
			AssigneeType:       decision.AssigneeType,
			Stage:              1,
			RepositoryPath:     repositoryPath,
			CloneURL:           cloneURL,
			DefaultBranch:      defaultBranch,
		})
		if err != nil {
			return err
		}
		item.ProviderIssueID = binding.ProviderIssueID
		if err := s.Store.UpdateWorkItem(item); err != nil {
			return err
		}
		_ = s.Events.Publish(ctx, events.NewWithContext(ctx, "work_item.created.v1", "work_item", item.ID, "", req.WorkspaceID, 1, map[string]any{"requirement_id": req.ID, "repository_id": repositoryID, "member_id": memberID, "agent_route_source": item.AgentRouteSource, "routing_config_revision": item.RoutingConfigRevision}))
	}
	return nil
}

// validateRequirementRepositoryRelations prevents a requirement from using a
// repository registered in another workspace. Unknown IDs remain valid for
// provider-native resources that are intentionally not mirrored in ADRO's
// repository registry; once an ID is registered locally, its workspace is
// authoritative and must match the requirement.
func (s *Server) validateRequirementRepositoryRelations(workspaceID string, repositoryIDs []string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" || s == nil || s.Store == nil {
		return nil
	}
	for _, repositoryID := range repositoryIDs {
		repositoryID = strings.TrimSpace(repositoryID)
		if repositoryID == "" {
			continue
		}
		repository, err := s.Store.GetRepository(repositoryID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("repository %q is unavailable", repositoryID)
		}
		if strings.TrimSpace(repository.WorkspaceID) != workspaceID {
			return fmt.Errorf("repository %q does not belong to workspace %q", repositoryID, workspaceID)
		}
	}
	return nil
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.Logger.Error("write response", "error", err)
	}
}

func (s *Server) recordAudit(r *http.Request, workspaceID, action, correlationID string, payload map[string]any) {
	if s.Audit == nil {
		return
	}
	actorID, actorType := r.Header.Get("X-Member-ID"), "member"
	if actorID == "" {
		actorID, actorType = "local-user", "system"
	}
	if _, err := s.Audit.Append(audit.Event{TenantID: tenant(r), WorkspaceID: workspaceID, ActorType: actorType, ActorID: actorID, Action: action, CorrelationID: correlationID, Payload: payload}); err != nil {
		s.Logger.Warn("audit append failed", "action", action, "error", err)
	}
}
func (s *Server) problem(w http.ResponseWriter, r *http.Request, status int, code, detail string, extra map[string]any) {
	traceID := w.Header().Get("X-Trace-ID")
	if traceID == "" {
		traceID = r.Header.Get("X-Trace-ID")
	}
	body := map[string]any{"type": "https://adro.dev/problems/" + code, "title": http.StatusText(status), "status": status, "detail": detail, "error_code": code, "request_id": w.Header().Get("X-Request-ID"), "trace_id": traceID}
	for k, v := range extra {
		body[k] = v
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.Logger.Error("write problem response", "error", err)
	}
}

const maxIdempotencyBodyBytes = 24 << 20

func mutationFingerprint(r *http.Request) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxIdempotencyBodyBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxIdempotencyBodyBytes {
		return "", errors.New("request body exceeds idempotency limit")
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	digest := sha256.Sum256([]byte(r.URL.RawQuery + "\x00" + r.Header.Get("Content-Type") + "\x00" + string(data)))
	return hex.EncodeToString(digest[:]), nil
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if len(s.Auth.ListUsers("")) == 0 {
		s.problem(w, r, http.StatusServiceUnavailable, "auth_not_configured", "set ADRO_ADMIN_PASSWORD before enabling local authentication", nil)
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	session, err := s.Auth.Authenticate(input.Username, input.Password)
	if err != nil {
		status := http.StatusUnauthorized
		code := "invalid_credentials"
		if errors.Is(err, adroauth.ErrLocked) {
			status = http.StatusTooManyRequests
			code = "login_temporarily_locked"
		}
		s.problem(w, r, status, code, "username or password is invalid", nil)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "adro_session", Value: session.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, Expires: session.ExpiresAt})
	s.writeJSON(w, http.StatusOK, session)
}

func (s *Server) authMe(w http.ResponseWriter, r *http.Request, user adroauth.User, authenticated, machine bool) {
	if r.Method != http.MethodGet {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if authenticated {
		s.writeJSON(w, http.StatusOK, map[string]any{"user": user, "menus": adroauth.AllMenus})
		return
	}
	if machine {
		s.writeJSON(w, http.StatusOK, map[string]any{"machine": true, "menus": adroauth.AllMenus})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"authenticated": false, "user": nil})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	s.Auth.Logout(bearerToken(r))
	http.SetCookie(w, &http.Cookie{Name: "adro_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) userRoute(w http.ResponseWriter, r *http.Request, path string, actor adroauth.User, authenticated bool) {
	if !authenticated || actor.Role != "admin" {
		s.problem(w, r, http.StatusForbidden, "administrator_required", "administrator access is required", nil)
		return
	}
	path = strings.Trim(path, "/")
	if path == "" {
		switch r.Method {
		case http.MethodGet:
			s.writeJSON(w, http.StatusOK, map[string]any{"items": s.Auth.ListUsers(actor.WorkspaceID), "menus": adroauth.AllMenus})
		case http.MethodPost:
			var input adroauth.User
			if err := decodeJSON(r, &input); err != nil {
				s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
				return
			}
			input.WorkspaceID = actor.WorkspaceID
			created, err := s.Auth.CreateUser(input)
			if err != nil {
				s.problem(w, r, http.StatusUnprocessableEntity, "user_creation_failed", err.Error(), nil)
				return
			}
			s.recordAudit(r, actor.WorkspaceID, "user.created", created.ID, map[string]any{"username": created.Username, "role": created.Role})
			s.writeJSON(w, http.StatusCreated, created)
		default:
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		}
		return
	}
	if r.Method != http.MethodPatch {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	var patch adroauth.User
	if err := decodeJSON(r, &patch); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	updated, err := s.Auth.UpdateUser(path, patch)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, adroauth.ErrNotFound) {
			status = http.StatusNotFound
		}
		s.problem(w, r, status, "user_update_failed", err.Error(), nil)
		return
	}
	s.recordAudit(r, actor.WorkspaceID, "user.updated", updated.ID, map[string]any{"role": updated.Role, "status": updated.Status, "menu_ids": updated.MenuIDs})
	s.writeJSON(w, http.StatusOK, updated)
}

func (s *Server) directory(w http.ResponseWriter, r *http.Request, user adroauth.User, authenticated, machine bool) {
	if r.Method != http.MethodGet {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if authRequired() && !authenticated && !machine {
		s.problem(w, r, http.StatusUnauthorized, "authentication_required", "sign in with an active ADRO account", nil)
		return
	}
	workspaceID := r.Header.Get("X-Workspace-ID")
	if authenticated {
		workspaceID = user.WorkspaceID
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"items": s.Auth.Directory(workspaceID)})
}

func providerSafeError(err error) string {
	if err == nil {
		return ""
	}
	var upstream *provider.UpstreamError
	if errors.As(err, &upstream) {
		return string(provider.ErrorCodeOf(err))
	}
	return err.Error()
}

func capabilityName(err error) string {
	var capability *provider.CapabilityError
	if errors.As(err, &capability) && capability != nil {
		return capability.Capability
	}
	return ""
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 2<<20+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

func configuredAuthMode() (string, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("ADRO_AUTH_MODE")))
	if mode == "" {
		return "optional", nil
	}
	if mode == "optional" || mode == "required" {
		return mode, nil
	}
	return "", fmt.Errorf("ADRO_AUTH_MODE=%q is invalid (allowed: optional, required)", mode)
}

func authRequired() bool {
	mode, err := configuredAuthMode()
	return err == nil && mode == "required"
}

func localAuthBackend() bool {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("ADRO_AUTH_BACKEND")))
	return backend == "" || backend == "local"
}

func tenant(r *http.Request) string {
	if value, ok := r.Context().Value(authenticatedTenantKey{}).(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	if v := r.Header.Get("X-Tenant-ID"); v != "" {
		return v
	}
	return "local"
}

func userTenant(user adroauth.User) string {
	if workspace := strings.TrimSpace(user.WorkspaceID); workspace != "" {
		return workspace
	}
	return "local"
}

func runnerRequestScope(r *http.Request) (string, string) {
	return strings.TrimSpace(r.Header.Get("X-Workspace-ID")), strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
}

func runnerInRequestScope(item runner.Runner, workspaceID, tenantID string) bool {
	if workspaceID == "" && tenantID == "" {
		return true
	}
	if workspaceID != "" && strings.TrimSpace(item.WorkspaceID) != workspaceID {
		return false
	}
	if tenantID != "" && strings.TrimSpace(item.TenantID) != tenantID {
		return false
	}
	return true
}

// requestWorkspace returns the effective workspace for a mutation. An
// authenticated identity or explicit machine header is authoritative; a body
// workspace is retained only for unauthenticated/provider bootstrap flows.
func requestWorkspace(r *http.Request, bodyWorkspace string) string {
	if workspace := strings.TrimSpace(r.Header.Get("X-Workspace-ID")); workspace != "" {
		return workspace
	}
	if workspace, ok := r.Context().Value(authenticatedWorkspaceKey{}).(string); ok && strings.TrimSpace(workspace) != "" {
		return strings.TrimSpace(workspace)
	}
	return strings.TrimSpace(bodyWorkspace)
}

func workspaceMatchesRequest(r *http.Request, resourceWorkspace string) bool {
	requested := strings.TrimSpace(r.Header.Get("X-Workspace-ID"))
	return requested == "" || strings.TrimSpace(resourceWorkspace) == requested
}

func requirementWorkspace(s *store.Memory, requirementID string) string {
	if s == nil || strings.TrimSpace(requirementID) == "" {
		return ""
	}
	requirement, err := s.GetRequirement(requirementID)
	if err != nil {
		return ""
	}
	return requirement.WorkspaceID
}

// runBelongsToWorkspace resolves a provider run through ADRO's local work
// item graph before allowing an authenticated workspace to access it. A run
// without a resolvable local owner is intentionally denied because there is no
// trustworthy workspace boundary to apply.
func (s *Server) runBelongsToWorkspace(run provider.RunSnapshot, workspaceID string) bool {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return true
	}
	if s == nil || s.Store == nil || strings.TrimSpace(run.WorkItemID) == "" {
		return false
	}
	item, err := s.Store.GetWorkItem(run.WorkItemID)
	if err != nil {
		return false
	}
	return s.workItemBelongsToWorkspace(item, workspaceID)
}

func (s *Server) workItemBelongsToWorkspace(item domain.WorkItem, workspaceID string) bool {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return true
	}
	resourceWorkspace := s.workItemWorkspace(item)
	return resourceWorkspace != "" && resourceWorkspace == workspaceID
}

func (s *Server) workItemWorkspace(item domain.WorkItem) string {
	if s == nil || s.Store == nil {
		return ""
	}
	if item.RequirementID != "" {
		return strings.TrimSpace(requirementWorkspace(s.Store, item.RequirementID))
	}
	if item.BugID != "" {
		if bug, err := s.Store.GetBug(item.BugID); err == nil {
			return strings.TrimSpace(bug.WorkspaceID)
		}
	}
	return ""
}

func (s *Server) evidenceForRequest(r *http.Request, items []domain.EvidenceBundle) []domain.EvidenceBundle {
	workspaceID := requestWorkspace(r, "")
	if workspaceID == "" {
		return items
	}
	filtered := items[:0]
	for _, item := range items {
		if strings.TrimSpace(item.WorkspaceID) == workspaceID {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func authorizedMachine(r *http.Request) bool {
	expected := os.Getenv("ADRO_API_TOKEN")
	if expected == "" {
		return false
	}
	value := bearerToken(r)
	if value == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(value), []byte(expected)) == 1
}

func (s *Server) authenticateUser(r *http.Request) (adroauth.User, bool) {
	if s.Auth == nil {
		return adroauth.User{}, false
	}
	return s.Auth.AuthenticateToken(bearerToken(r))
}

func bearerToken(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) >= 8 && strings.EqualFold(value[:7], "Bearer ") {
		return strings.TrimSpace(value[7:])
	}
	if cookie, err := r.Cookie("adro_session"); err == nil {
		return strings.TrimSpace(cookie.Value)
	}
	return ""
}

func menuForPath(path string) string {
	routes := []struct {
		prefix string
		menu   string
	}{
		{"/api/v1/users", "admin"}, {"/api/v1/audit", "admin"}, {"/api/v1/plugins", "admin"},
		{"/api/v1/requirements", "requirements"}, {"/api/v1/bugs", "bugs"},
		{"/api/v1/pipelines", "executions"}, {"/api/v1/workflow-templates", "executions"}, {"/api/v1/chats", "executions"},
		{"/api/v1/repositories", "repositories"}, {"/api/v1/repository-graph", "repositories"},
		{"/api/v1/agents", "agents"}, {"/api/v1/developer-profiles", "agents"},
		{"/api/v1/mcp", "mcp"}, {"/api/v1/skills", "skills"},
		{"/api/v1/automations", "automations"}, {"/api/v1/automation-runs", "automations"},
		{"/api/v1/screenshots", "artifacts"}, {"/api/v1/artifacts", "artifacts"},
		{"/api/v1/artifact-migrations", "artifacts"}, {"/api/v1/runners", "runners"},
		{"/api/v1/approvals", "humanQA"}, {"/api/v1/evidence", "testing"},
		{"/api/v1/runs", "executions"}, {"/api/v1/work-items", "executions"}, {"/api/v1/streams", "executions"},
	}
	for _, route := range routes {
		if path == route.prefix || strings.HasPrefix(path, route.prefix+"/") {
			return route.menu
		}
	}
	return ""
}
func queryInt(r *http.Request, k string, def int) int {
	v, e := strconv.Atoi(r.URL.Query().Get(k))
	if e != nil || v < 1 {
		return def
	}
	return v
}
func parseIfMatch(v string) int64 {
	v = strings.Trim(v, "\"")
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}
func appendUnique(base []string, values ...string) []string {
	seen := map[string]bool{}
	for _, v := range base {
		seen[v] = true
	}
	for _, v := range values {
		if v != "" && !seen[v] {
			base = append(base, v)
			seen[v] = true
		}
	}
	return base
}
func fingerprint(b domain.Bug) string {
	h := sha256.Sum256([]byte(strings.Join([]string{b.Title, b.RepositoryID, b.Steps, b.Expected, b.Actual, b.LogExcerpt}, "\x00")))
	return hex.EncodeToString(h[:])
}

func containsSecret(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			key = strings.ToLower(key)
			if strings.Contains(key, "token") || strings.Contains(key, "password") || strings.Contains(key, "secret") || strings.Contains(key, "authorization") || strings.Contains(key, "private_key") {
				return true
			}
			if containsSecret(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsSecret(child) {
				return true
			}
		}
	}
	return false
}
func repairBrief(b domain.Bug) string {
	return fmt.Sprintf("RepairBrief\nBug: %s\nFingerprint: %s\nExpected: %s\nActual: %s\nSteps: %s\nLog: %s\nAttempt: %d", b.Title, b.Fingerprint, b.Expected, b.Actual, b.Steps, b.LogExcerpt, b.AttemptCount)
}
func hashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func parseRange(v string) (artifact.ByteRange, error) {
	if !strings.HasPrefix(v, "bytes=") {
		if v == "" {
			return artifact.ByteRange{Start: 0, End: -1}, nil
		}
		return artifact.ByteRange{}, errors.New("range must use bytes=start-end")
	}
	p := strings.Split(strings.TrimPrefix(v, "bytes="), "-")
	if len(p) != 2 {
		return artifact.ByteRange{}, errors.New("range must use bytes=start-end")
	}
	if p[0] == "" {
		return artifact.ByteRange{}, errors.New("suffix ranges are not supported")
	}
	start, err := strconv.ParseInt(p[0], 10, 64)
	if err != nil || start < 0 {
		return artifact.ByteRange{}, errors.New("invalid range start")
	}
	end := int64(-1)
	if p[1] != "" {
		end, err = strconv.ParseInt(p[1], 10, 64)
		if err != nil || end < start {
			return artifact.ByteRange{}, errors.New("invalid range end")
		}
	}
	return artifact.ByteRange{Start: start, End: end}, nil
}
func sortInts(v []int) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}
