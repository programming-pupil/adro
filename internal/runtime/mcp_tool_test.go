package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adro-project/adro/internal/domain"
	mcpclient "github.com/adro-project/adro/internal/mcp"
)

func TestMCPToolExecutorUsesDurableToolLoop(t *testing.T) {
	var calls atomic.Int32
	server := newRuntimeMCPTestServer(t, &calls, false)
	defer server.Close()
	journal, err := NewJournal("")
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{TenantID: "tenant", WorkspaceID: "workspace", SessionID: "session", RunID: "run"}
	lease, err := journal.AcquireLease(scope, "worker", time.Minute, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	executor := MCPToolExecutor{
		Client: mcpclient.Client{},
		Server: domain.MCPServer{ID: "mcp-1", Endpoint: server.URL, Protocol: "streamable-http"},
		Loop:   ToolLoop{Journal: journal, Scope: scope, Owner: "worker", FencingToken: lease.FencingToken, AllowedCapabilities: []string{"knowledge.read"}},
	}
	result, err := executor.Run(context.Background(), "call-1", ToolContract{Name: "search", Capabilities: []string{"knowledge.read"}, SideEffectClass: EffectReadOnly}, map[string]any{"query": "adro"})
	if err != nil || result.Status != "finished" || calls.Load() != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls.Load())
	}
	events := journal.List(scope)
	seen := map[string]bool{}
	for _, event := range events {
		seen[event.EventType] = true
	}
	for _, eventType := range []string{EventEffectIntent, EventEffectDispatched, EventEffectReceipted} {
		if !seen[eventType] {
			t.Fatalf("missing durable MCP effect event %q: %v", eventType, seen)
		}
	}
}

func TestMCPToolExecutorHonorsApprovalAndUnknownWriteOutcome(t *testing.T) {
	var calls atomic.Int32
	server := newRuntimeMCPTestServer(t, &calls, true)
	defer server.Close()
	journal, err := NewJournal("")
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{TenantID: "tenant", WorkspaceID: "workspace", SessionID: "session", RunID: "run"}
	lease, err := journal.AcquireLease(scope, "worker", time.Minute, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	contract := ToolContract{Name: "mutate", Capabilities: []string{"record.write"}, SideEffectClass: EffectNonRetriableWrite, ReconcilePolicy: ReconcileHuman, RequiresApproval: true}
	executor := MCPToolExecutor{Client: mcpclient.Client{}, Server: domain.MCPServer{ID: "mcp-1", Endpoint: server.URL, Protocol: "http"}, Loop: ToolLoop{Journal: journal, Scope: scope, Owner: "worker", FencingToken: lease.FencingToken, AllowedCapabilities: []string{"record.write"}}}
	if _, err := executor.Run(context.Background(), "call-write", contract, map[string]any{"value": "x"}); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("approval err=%v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("MCP was called before approval")
	}
	if _, err := journal.ApproveTool(scope, "call-write", "worker", lease.FencingToken, "approved"); err != nil {
		t.Fatal(err)
	}
	result, err := executor.Run(context.Background(), "call-write", contract, map[string]any{"value": "x"})
	if !errors.Is(err, ErrEffectOutcomeUnknown) || result.Status != "outcome_unknown" {
		t.Fatalf("unknown outcome result=%+v err=%v", result, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one non-retried MCP dispatch, got %d", calls.Load())
	}
}

func newRuntimeMCPTestServer(t *testing.T, calls *atomic.Int32, failCall bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		method, _ := request["method"].(string)
		switch method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"protocolVersion": mcpclient.DefaultProtocolVersion, "capabilities": map[string]any{}}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			params := []any{map[string]any{"name": "search"}, map[string]any{"name": "mutate"}}
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"tools": params}})
		case "tools/call":
			calls.Add(1)
			if failCall {
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "error": map[string]any{"code": -32001, "message": "remote failure"}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"ok": true}})
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
		}
	}))
}
