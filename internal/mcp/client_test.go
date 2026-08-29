package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adro-project/adro/internal/domain"
)

func TestHTTPJSONRPCInvokeAndDiscovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["method"] == "tools/list" {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"tools": []any{map[string]any{"name": "search"}}}})
			return
		}
		params := request["params"].(map[string]any)
		if params["name"] != "search" {
			t.Errorf("params=%v", params)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}})
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
	if _, err := (Client{}).Invoke(context.Background(), domain.MCPServer{Endpoint: "http://127.0.0.1", Protocol: "stdio"}, "x", nil); err != ErrUnsupportedProtocol {
		t.Fatalf("err=%v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"error\":{\"message\":\"denied\"}}\n\n"))
	}))
	defer server.Close()
	_, err := (Client{}).Invoke(context.Background(), domain.MCPServer{Endpoint: server.URL, Protocol: "http"}, "x", nil)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("err=%v", err)
	}
}
