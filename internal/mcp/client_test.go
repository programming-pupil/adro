package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/adro-project/adro/internal/domain"
)

func TestHTTPJSONRPCNegotiationInvokeAndDiscovery(t *testing.T) {
	var sessionID = "session-test-1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		method, _ := request["method"].(string)
		if method == "initialize" {
			w.Header().Set("Mcp-Session-Id", sessionID)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{
					"protocolVersion": DefaultProtocolVersion,
					"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
					"serverInfo":      map[string]any{"name": "test", "version": "1"},
				},
			})
			return
		}
		if method == "notifications/initialized" {
			if got := r.Header.Get("Mcp-Session-Id"); got != sessionID {
				http.Error(w, "missing session", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if got := r.Header.Get("Mcp-Session-Id"); got != sessionID {
			http.Error(w, "missing session", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("MCP-Protocol-Version"); got != DefaultProtocolVersion {
			http.Error(w, "missing protocol version", http.StatusBadRequest)
			return
		}
		switch method {
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{
				"tools": []any{map[string]any{"name": "search", "inputSchema": map[string]any{"type": "object", "required": []any{"query"}}}},
			}})
		case "tools/call":
			params, _ := request["params"].(map[string]any)
			if params["name"] != "search" {
				http.Error(w, "unexpected tool", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}})
		default:
			http.Error(w, "unexpected method", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	mcpServer := domain.MCPServer{WorkspaceID: "w", Name: "tools", Endpoint: server.URL, Protocol: "streamable-http"}
	response, err := (Client{}).Invoke(context.Background(), mcpServer, "search", map[string]any{"query": "adro"})
	if err != nil || response["content"] == nil {
		t.Fatalf("response=%v err=%v", response, err)
	}
	_, digest, err := (Client{}).Discover(context.Background(), mcpServer)
	if err != nil || len(digest) != 64 {
		t.Fatalf("digest=%q err=%v", digest, err)
	}
}

func TestRejectsStdioAndRPCError(t *testing.T) {
	if _, err := (Client{}).Invoke(context.Background(), domain.MCPServer{Endpoint: "http://127.0.0.1", Protocol: "stdio"}, "x", nil); !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf("err=%v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		switch request["method"] {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"protocolVersion": DefaultProtocolVersion}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request["id"], "error": map[string]any{"code": -32000, "message": "denied"}})
			_, _ = w.Write([]byte("data: " + string(data) + "\n\n"))
		}
	}))
	defer server.Close()
	_, _, err := (Client{}).Discover(context.Background(), domain.MCPServer{Endpoint: server.URL, Protocol: "http"})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("err=%v", err)
	}
}

func TestSchemaDriftAndSecretPayloadFailClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		switch request["method"] {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"protocolVersion": DefaultProtocolVersion}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"tools": []any{map[string]any{"name": "search"}}}})
		case "tools/call":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"ok": true}})
		}
	}))
	defer server.Close()
	mcpServer := domain.MCPServer{Endpoint: server.URL, Protocol: "http", SchemaDigest: strings.Repeat("0", 64)}
	_, err := (Client{}).Invoke(context.Background(), mcpServer, "search", map[string]any{})
	if !errors.Is(err, ErrSchemaDrift) {
		t.Fatalf("schema drift err=%v", err)
	}
	_, err = (Client{}).Invoke(context.Background(), domain.MCPServer{Endpoint: server.URL, Protocol: "http"}, "search", map[string]any{"authorization": "Bearer secret"})
	if !errors.Is(err, ErrSecretInPayload) {
		t.Fatalf("secret err=%v", err)
	}
}

func TestStdioNegotiationAndBoundedFraming(t *testing.T) {
	server := domain.MCPServer{
		Endpoint: "stdio://",
		Protocol: "stdio",
		Configuration: map[string]any{
			"command": os.Args[0],
			"args":    []any{"-test.run=TestMCPStdioHelper", "--"},
			"env":     map[string]any{"MCP_STDIO_HELPER": "1"},
		},
	}
	response, err := (Client{AllowStdio: true, AllowStdioEnvironment: true, MaxStdioFrameBytes: 1 << 20}).Invoke(context.Background(), server, "echo", map[string]any{"value": "ok"})
	if err != nil || response["ok"] != true {
		t.Fatalf("response=%v err=%v", response, err)
	}
	if _, err := decodeMessage([]byte("data: {\"jsonrpc\":\"2.0\"}\n\ndata: {\"jsonrpc\":\"2.0\"}\n\n"), 1024); err != nil {
		t.Fatalf("SSE decode err=%v", err)
	}
	if _, err := readLineLimited(bufio.NewReader(strings.NewReader(strings.Repeat("x", 128))), 32); err == nil {
		t.Fatal("expected bounded frame error")
	}
}

func TestMCPStdioHelper(t *testing.T) {
	if os.Getenv("MCP_STDIO_HELPER") != "1" {
		return
	}
	decoder := bufio.NewScanner(os.Stdin)
	for decoder.Scan() {
		var request map[string]any
		if json.Unmarshal(decoder.Bytes(), &request) != nil {
			continue
		}
		method, _ := request["method"].(string)
		if method == "notifications/initialized" {
			continue
		}
		var response map[string]any
		switch method {
		case "initialize":
			response = map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"protocolVersion": DefaultProtocolVersion, "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "stdio", "version": "1"}}}
		case "tools/list":
			response = map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"tools": []any{map[string]any{"name": "echo", "inputSchema": map[string]any{"type": "object", "required": []any{"value"}}}}}}
		case "tools/call":
			response = map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"ok": true}}
		default:
			response = map[string]any{"jsonrpc": "2.0", "id": request["id"], "error": map[string]any{"code": -32601, "message": "method not found"}}
		}
		data, _ := json.Marshal(response)
		_, _ = os.Stdout.Write(append(data, '\n'))
	}
}
