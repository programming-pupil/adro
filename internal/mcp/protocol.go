package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	// DefaultProtocolVersion is the newest protocol version understood by this
	// adapter. Older versions remain accepted only when the server explicitly
	// negotiates one of SupportedProtocolVersions.
	DefaultProtocolVersion = "2025-06-18"
	clientName             = "adro-runtime"
	clientVersion          = "1.0"
)

var (
	ErrUnsupportedProtocol = errors.New("MCP protocol is unsupported")
	// ErrStdioDisabled aliases ErrUnsupportedProtocol for callers that need to
	// distinguish a rejected local process from a remote transport without
	// breaking the existing API contract.
	ErrStdioDisabled       = ErrUnsupportedProtocol
	ErrProtocolNegotiation = errors.New("MCP capability negotiation failed")
	ErrSchemaDrift         = errors.New("MCP tool schema changed")
	ErrToolUnavailable     = errors.New("MCP tool is unavailable")
	ErrSecretUnavailable   = errors.New("MCP secret reference cannot be resolved")
	ErrSecretInPayload     = errors.New("MCP payload contains secret material")
)

var defaultSupportedProtocolVersions = []string{
	DefaultProtocolVersion,
	"2025-03-26",
	"2024-11-05",
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
	Error   *rpcError      `json:"error,omitempty"`
}

type negotiatedSession struct {
	ProtocolVersion string
	Capabilities    map[string]any
	ServerInfo      map[string]any
}

func newRequest(method string, params any) rpcRequest {
	return rpcRequest{JSONRPC: "2.0", ID: newRequestID(), Method: method, Params: params}
}

func (r rpcRequest) validate() error {
	if r.JSONRPC != "2.0" || strings.TrimSpace(r.Method) == "" || strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("invalid JSON-RPC request")
	}
	return nil
}

func decodeRPCResponse(message map[string]any, requestID string) (map[string]any, error) {
	encoded, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("encode MCP response: %w", err)
	}
	var response rpcResponse
	if err := json.Unmarshal(encoded, &response); err != nil {
		return nil, fmt.Errorf("decode MCP response: %w", err)
	}
	if response.JSONRPC != "2.0" {
		return nil, fmt.Errorf("%w: response is not JSON-RPC 2.0", ErrProtocolNegotiation)
	}
	if response.ID == nil {
		return nil, errors.New("MCP response is missing an id")
	}
	if fmt.Sprint(response.ID) != requestID {
		return nil, fmt.Errorf("MCP response id does not match request")
	}
	if response.Error != nil {
		message := strings.TrimSpace(response.Error.Message)
		if message == "" {
			message = "MCP server returned an error"
		}
		return nil, fmt.Errorf("MCP server error (%d): %s", response.Error.Code, message)
	}
	if response.Result == nil {
		return map[string]any{}, nil
	}
	return response.Result, nil
}

func validateNegotiation(result map[string]any, supported []string) (negotiatedSession, error) {
	version, ok := result["protocolVersion"].(string)
	if !ok || strings.TrimSpace(version) == "" {
		return negotiatedSession{}, fmt.Errorf("%w: server omitted protocolVersion", ErrProtocolNegotiation)
	}
	allowed := false
	for _, candidate := range supported {
		if strings.TrimSpace(candidate) == version {
			allowed = true
			break
		}
	}
	if !allowed {
		return negotiatedSession{}, fmt.Errorf("%w: unsupported protocolVersion %q", ErrProtocolNegotiation, version)
	}
	capabilities, ok := result["capabilities"].(map[string]any)
	if !ok {
		capabilities = map[string]any{}
	}
	serverInfo, ok := result["serverInfo"].(map[string]any)
	if !ok {
		serverInfo = map[string]any{}
	}
	return negotiatedSession{ProtocolVersion: version, Capabilities: capabilities, ServerInfo: serverInfo}, nil
}
