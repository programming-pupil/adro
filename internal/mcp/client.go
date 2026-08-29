// Package mcp contains the isolated transport used by the control plane when
// invoking a governed MCP server. It speaks JSON-RPC over HTTP/SSE and keeps
// credentials out of request payloads; stdio is intentionally not executed by
// the API process.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/domain"
)

var ErrUnsupportedProtocol = errors.New("MCP protocol is unsupported by the HTTP gateway")

type Client struct {
	HTTP             *http.Client
	MaxResponseBytes int64
}

func (c Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c Client) Invoke(ctx context.Context, server domain.MCPServer, tool string, request map[string]any) (map[string]any, error) {
	if err := validate(server, tool); err != nil {
		return nil, err
	}
	if request == nil {
		request = map[string]any{}
	}
	payload := map[string]any{"jsonrpc": "2.0", "id": domain.NewID(), "method": "tools/call", "params": map[string]any{"name": tool, "arguments": request}}
	return c.call(ctx, server.Endpoint, payload)
}

func (c Client) Discover(ctx context.Context, server domain.MCPServer) (map[string]any, string, error) {
	if err := validate(server, "discover"); err != nil {
		return nil, "", err
	}
	result, err := c.call(ctx, server.Endpoint, map[string]any{"jsonrpc": "2.0", "id": domain.NewID(), "method": "tools/list", "params": map[string]any{}})
	if err != nil {
		return nil, "", err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return result, hex.EncodeToString(sum[:]), nil
}

func (c Client) Health(ctx context.Context, server domain.MCPServer) error {
	if err := validate(server, "health"); err != nil {
		return err
	}
	// A discovery call is a useful health check for MCP servers because many
	// servers intentionally do not expose a separate /health route.
	_, _, err := c.Discover(ctx, server)
	return err
}

func validate(server domain.MCPServer, operation string) error {
	protocol := strings.ToLower(strings.TrimSpace(server.Protocol))
	if strings.Contains(protocol, "stdio") || strings.HasPrefix(protocol, "command") {
		return ErrUnsupportedProtocol
	}
	parsed, err := url.Parse(strings.TrimSpace(server.Endpoint))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("MCP %s endpoint must be an http or https URL", operation)
	}
	if parsed.User != nil {
		return errors.New("MCP endpoint must not contain credentials")
	}
	return nil
}

func (c Client) call(ctx context.Context, endpoint string, payload map[string]any) (map[string]any, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	limit := c.MaxResponseBytes
	if limit <= 0 || limit > 16<<20 {
		limit = 16 << 20
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("MCP response exceeds the 16 MiB limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("MCP server returned HTTP %d", resp.StatusCode)
	}
	message, err := decodeMessage(body)
	if err != nil {
		return nil, err
	}
	if rpcError, ok := message["error"].(map[string]any); ok {
		return nil, fmt.Errorf("MCP tool call failed: %v", rpcError["message"])
	}
	if result, ok := message["result"].(map[string]any); ok {
		return result, nil
	}
	return message, nil
}

func decodeMessage(body []byte) (map[string]any, error) {
	var message map[string]any
	if json.Unmarshal(body, &message) == nil {
		return message, nil
	}
	// Streamable HTTP and older MCP servers may return SSE frames. Decode the
	// first complete data frame; callers use cursor/event semantics above it.
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &message) == nil {
			return message, nil
		}
	}
	if scanner.Err() != nil {
		return nil, scanner.Err()
	}
	return nil, errors.New("MCP response is not valid JSON-RPC")
}
