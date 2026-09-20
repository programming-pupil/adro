// Package mcp contains the isolated transport used by the control plane when
// invoking a governed MCP server. The protocol boundary is deliberately
// separate from the tool/effect runtime: transport negotiation, framing and
// schema validation happen here, while approval, effect receipts and audit
// remain owned by the runtime caller.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	coreencoding "github.com/adro-project/adro/core/encoding"
	"github.com/adro-project/adro/internal/domain"
)

type Client struct {
	HTTP                      *http.Client
	MaxResponseBytes          int64
	MaxRequestBytes           int64
	MaxStdioFrameBytes        int64
	AllowStdio                bool
	AllowStdioEnvironment     bool
	SecretResolver            SecretResolver
	ClientName                string
	ClientVersion             string
	SupportedProtocolVersions []string
}

// SecretResolver is intentionally tiny so a production Secret Broker can be
// injected without making this package depend on a storage implementation.
// Resolved bytes are used only in transport headers/environment and never in a
// JSON-RPC payload, digest, invocation record or error message.
type SecretResolver interface {
	Resolve(context.Context, string) (string, error)
}

func (c Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c Client) maxResponse() int64 {
	if c.MaxResponseBytes <= 0 || c.MaxResponseBytes > 16<<20 {
		return 16 << 20
	}
	return c.MaxResponseBytes
}

func (c Client) maxRequest() int64 {
	if c.MaxRequestBytes <= 0 || c.MaxRequestBytes > 1<<20 {
		return 1 << 20
	}
	return c.MaxRequestBytes
}

func (c Client) maxStdioFrame() int64 {
	if c.MaxStdioFrameBytes <= 0 || c.MaxStdioFrameBytes > 16<<20 {
		return 16 << 20
	}
	return c.MaxStdioFrameBytes
}

func (c Client) clientName() string {
	if strings.TrimSpace(c.ClientName) == "" {
		return clientName
	}
	return strings.TrimSpace(c.ClientName)
}

func (c Client) clientVersion() string {
	if strings.TrimSpace(c.ClientVersion) == "" {
		return clientVersion
	}
	return strings.TrimSpace(c.ClientVersion)
}

func (c Client) supportedVersions() []string {
	if len(c.SupportedProtocolVersions) == 0 {
		return append([]string(nil), defaultSupportedProtocolVersions...)
	}
	seen := map[string]bool{}
	versions := make([]string, 0, len(c.SupportedProtocolVersions))
	for _, version := range c.SupportedProtocolVersions {
		version = strings.TrimSpace(version)
		if version != "" && !seen[version] {
			seen[version] = true
			versions = append(versions, version)
		}
	}
	if len(versions) == 0 {
		return append([]string(nil), defaultSupportedProtocolVersions...)
	}
	return versions
}

func (c Client) resolveSecret(ctx context.Context, reference string) (string, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return "", nil
	}
	if c.SecretResolver == nil {
		return "", ErrSecretUnavailable
	}
	value, err := c.SecretResolver.Resolve(ctx, reference)
	if err != nil || strings.TrimSpace(value) == "" {
		return "", ErrSecretUnavailable
	}
	return value, nil
}

func (c Client) Invoke(ctx context.Context, server domain.MCPServer, tool string, request map[string]any) (map[string]any, error) {
	if err := validate(server, "invoke"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(tool) == "" {
		return nil, ErrToolUnavailable
	}
	if request == nil {
		request = map[string]any{}
	}
	if containsSecretMaterial(request) {
		return nil, ErrSecretInPayload
	}
	if _, err := coreencoding.Digest(request); err != nil {
		return nil, fmt.Errorf("validate MCP request: %w", err)
	}
	var response map[string]any
	err := c.withSession(ctx, server, func(session *mcpSession) error {
		toolResult, digest, err := session.toolsList(ctx)
		if err != nil {
			return err
		}
		catalog, err := parseToolCatalog(toolResult)
		if err != nil {
			return err
		}
		if server.SchemaDigest != "" && !strings.EqualFold(strings.TrimSpace(server.SchemaDigest), digest) {
			return fmt.Errorf("%w: expected %s, got %s", ErrSchemaDrift, server.SchemaDigest, digest)
		}
		toolDefinition, ok := catalog[tool]
		if !ok {
			return fmt.Errorf("%w: %s", ErrToolUnavailable, tool)
		}
		if err := validateToolArguments(toolDefinition, request); err != nil {
			return err
		}
		result, err := session.call(ctx, "tools/call", map[string]any{"name": tool, "arguments": request})
		if err != nil {
			return err
		}
		response = result
		return nil
	})
	return response, err
}

func (c Client) Discover(ctx context.Context, server domain.MCPServer) (map[string]any, string, error) {
	if err := validate(server, "discover"); err != nil {
		return nil, "", err
	}
	var result map[string]any
	var digest string
	err := c.withSession(ctx, server, func(session *mcpSession) error {
		var err error
		result, digest, err = session.toolsList(ctx)
		return err
	})
	return result, digest, err
}

func (c Client) Health(ctx context.Context, server domain.MCPServer) error {
	if err := validate(server, "health"); err != nil {
		return err
	}
	_, _, err := c.Discover(ctx, server)
	return err
}

type mcpSession struct {
	transport       transport
	negotiated      negotiatedSession
	maxRequestBytes int64
}

func (c Client) withSession(ctx context.Context, server domain.MCPServer, fn func(*mcpSession) error) error {
	tr, err := openTransport(ctx, c, server)
	if err != nil {
		return err
	}
	defer tr.Close()
	params := map[string]any{
		"protocolVersion": c.supportedVersions()[0],
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"clientInfo": map[string]any{"name": c.clientName(), "version": c.clientVersion()},
	}
	request := newRequest("initialize", params)
	response, err := tr.Call(ctx, request)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProtocolNegotiation, err)
	}
	negotiated, err := validateNegotiation(response, c.supportedVersions())
	if err != nil {
		return err
	}
	tr.SetProtocolVersion(negotiated.ProtocolVersion)
	if err := tr.Notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		return fmt.Errorf("%w: initialized notification: %v", ErrProtocolNegotiation, err)
	}
	return fn(&mcpSession{transport: tr, negotiated: negotiated, maxRequestBytes: c.maxRequest()})
}

func (s *mcpSession) call(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	request := newRequest(method, params)
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > s.maxRequestBytes {
		return nil, errors.New("MCP request exceeds the configured size limit")
	}
	return s.transport.Call(ctx, request)
}

func (s *mcpSession) toolsList(ctx context.Context) (map[string]any, string, error) {
	result, err := s.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, "", err
	}
	if _, err := parseToolCatalog(result); err != nil {
		return nil, "", err
	}
	digest, err := coreencoding.Digest(result)
	if err != nil {
		return nil, "", fmt.Errorf("digest MCP tool schema: %w", err)
	}
	return result, digest, nil
}

func parseToolCatalog(result map[string]any) (map[string]map[string]any, error) {
	value, ok := result["tools"]
	if !ok {
		return nil, fmt.Errorf("%w: tools/list omitted tools", ErrProtocolNegotiation)
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: tools must be an array", ErrProtocolNegotiation)
	}
	catalog := make(map[string]map[string]any, len(items))
	for _, item := range items {
		definition, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: tool definition is not an object", ErrProtocolNegotiation)
		}
		name, ok := definition["name"].(string)
		name = strings.TrimSpace(name)
		if !ok || name == "" || len(name) > 256 {
			return nil, fmt.Errorf("%w: tool name is invalid", ErrProtocolNegotiation)
		}
		if _, exists := catalog[name]; exists {
			return nil, fmt.Errorf("%w: duplicate tool %q", ErrProtocolNegotiation, name)
		}
		if schema, exists := definition["inputSchema"]; exists {
			if _, ok := schema.(map[string]any); !ok {
				return nil, fmt.Errorf("%w: tool %q inputSchema is invalid", ErrProtocolNegotiation, name)
			}
		}
		catalog[name] = definition
	}
	return catalog, nil
}

func validateToolArguments(definition map[string]any, request map[string]any) error {
	schema, ok := definition["inputSchema"].(map[string]any)
	if !ok {
		return nil
	}
	if schemaType, ok := schema["type"].(string); ok && schemaType != "" && schemaType != "object" {
		return fmt.Errorf("%w: tool schema requires unsupported root type %q", ErrProtocolNegotiation, schemaType)
	}
	if required, ok := schema["required"].([]any); ok {
		for _, item := range required {
			name, ok := item.(string)
			if !ok || strings.TrimSpace(name) == "" {
				return fmt.Errorf("%w: tool schema required field is invalid", ErrProtocolNegotiation)
			}
			if _, present := request[name]; !present {
				return fmt.Errorf("MCP tool argument %q is required", name)
			}
		}
	}
	return nil
}

func validate(server domain.MCPServer, operation string) error {
	if strings.TrimSpace(server.Name) == "" && strings.TrimSpace(server.Endpoint) == "" && strings.TrimSpace(server.Protocol) == "" {
		return fmt.Errorf("MCP %s server is empty", operation)
	}
	_, err := classifyProtocol(server.Protocol)
	if err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(server.Protocol), "stdio") || strings.EqualFold(strings.TrimSpace(server.Protocol), "command") || strings.EqualFold(strings.TrimSpace(server.Protocol), "command-line") {
		return nil
	}
	_, err = validateHTTPURL(server.Endpoint)
	return err
}

func containsSecretMaterial(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			key = strings.ToLower(strings.TrimSpace(key))
			if strings.Contains(key, "token") || strings.Contains(key, "password") || strings.Contains(key, "secret") || strings.Contains(key, "authorization") || strings.Contains(key, "private_key") || strings.Contains(key, "credential") {
				return true
			}
			if containsSecretMaterial(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsSecretMaterial(child) {
				return true
			}
		}
	}
	return false
}

func newRequestID() string {
	return fmt.Sprintf("adro-%d", nextRequestID())
}

func nextRequestID() uint64 {
	return atomic.AddUint64(&requestIDCounter, 1)
}

var requestIDCounter uint64
