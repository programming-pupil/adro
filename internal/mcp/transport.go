package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/internal/domain"
)

type transport interface {
	Call(context.Context, rpcRequest) (map[string]any, error)
	Notify(context.Context, string, any) error
	SetProtocolVersion(string)
	Close() error
}

type httpTransport struct {
	client          *http.Client
	endpoint        string
	maxResponse     int64
	maxRequest      int64
	secret          string
	mu              sync.Mutex
	sessionID       string
	protocolVersion string
}

func (t *httpTransport) SetProtocolVersion(version string) {
	t.mu.Lock()
	t.protocolVersion = version
	t.mu.Unlock()
}

func (t *httpTransport) Call(ctx context.Context, request rpcRequest) (map[string]any, error) {
	if err := request.validate(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode MCP request: %w", err)
	}
	responseBody, status, err := t.send(ctx, body, true)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("MCP server returned HTTP %d", status)
	}
	message, err := decodeMessage(responseBody, t.maxResponse)
	if err != nil {
		return nil, err
	}
	return decodeRPCResponse(message, request.ID)
}

func (t *httpTransport) Notify(ctx context.Context, method string, params any) error {
	request := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode MCP notification: %w", err)
	}
	responseBody, status, err := t.send(ctx, body, false)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("MCP notification returned HTTP %d", status)
	}
	if len(bytes.TrimSpace(responseBody)) == 0 || status == http.StatusAccepted || status == http.StatusNoContent {
		return nil
	}
	// Servers are allowed to acknowledge a notification with an empty JSON-RPC
	// response. Validate any non-empty response but ignore its result.
	_, err = decodeMessage(responseBody, t.maxResponse)
	return err
}

func (t *httpTransport) send(ctx context.Context, body []byte, expectResponse bool) ([]byte, int, error) {
	if t.maxRequest <= 0 {
		t.maxRequest = 1 << 20
	}
	if int64(len(body)) > t.maxRequest {
		return nil, 0, errors.New("MCP request exceeds the configured size limit")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	t.mu.Lock()
	if t.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	if t.protocolVersion != "" {
		req.Header.Set("MCP-Protocol-Version", t.protocolVersion)
	}
	t.mu.Unlock()
	if t.secret != "" {
		req.Header.Set("Authorization", "Bearer "+t.secret)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if sessionID := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id")); sessionID != "" {
		t.mu.Lock()
		t.sessionID = sessionID
		t.mu.Unlock()
	}
	limit := t.maxResponse
	if limit <= 0 || limit > 16<<20 {
		limit = 16 << 20
	}
	var data []byte
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		data, err = readFirstSSEEvent(resp.Body, limit)
	} else {
		data, err = io.ReadAll(io.LimitReader(resp.Body, limit+1))
	}
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if int64(len(data)) > limit {
		return nil, resp.StatusCode, errors.New("MCP response exceeds the 16 MiB limit")
	}
	if !expectResponse && (resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent) {
		return data, resp.StatusCode, nil
	}
	return data, resp.StatusCode, nil
}

func (t *httpTransport) Close() error { return nil }

type stdioTransport struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    *bufio.Reader
	maxFrame  int64
	closeOnce sync.Once
	waitDone  chan error
	mu        sync.Mutex
	closed    bool
}

func (t *stdioTransport) SetProtocolVersion(string) {}

func (t *stdioTransport) Call(ctx context.Context, request rpcRequest) (map[string]any, error) {
	if err := request.validate(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode MCP request: %w", err)
	}
	if err := t.writeFrame(body); err != nil {
		return nil, err
	}
	frame, err := t.readFrame(ctx)
	if err != nil {
		return nil, err
	}
	message, err := decodeMessage(frame, t.maxFrame)
	if err != nil {
		return nil, err
	}
	return decodeRPCResponse(message, request.ID)
}

func (t *stdioTransport) Notify(ctx context.Context, method string, params any) error {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("encode MCP notification: %w", err)
	}
	return t.writeFrame(body)
}

func (t *stdioTransport) writeFrame(body []byte) error {
	if t.maxFrame <= 0 {
		t.maxFrame = 16 << 20
	}
	if int64(len(body)) > t.maxFrame {
		return errors.New("MCP stdio request exceeds the frame limit")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errors.New("MCP stdio transport is closed")
	}
	if _, err := t.stdin.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("write MCP stdio request: %w", err)
	}
	return nil
}

func (t *stdioTransport) readFrame(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	// MCP stdio uses newline-delimited JSON. Accept Content-Length framing as a
	// compatibility boundary, but reject arbitrary stdout text instead of
	// silently treating logs as protocol messages.
	for {
		line, err := readLineLimited(t.stdout, t.maxFrame)
		if err != nil {
			return nil, fmt.Errorf("read MCP stdio response: %w", err)
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(trimmed), "content-length:") {
			length, err := parseContentLength(trimmed)
			if err != nil {
				return nil, err
			}
			for {
				header, err := readLineLimited(t.stdout, t.maxFrame)
				if err != nil {
					return nil, fmt.Errorf("read MCP stdio headers: %w", err)
				}
				if strings.TrimSpace(header) == "" {
					break
				}
			}
			if int64(length) > t.maxFrame {
				return nil, errors.New("MCP stdio response exceeds the frame limit")
			}
			frame := make([]byte, length)
			if _, err := io.ReadFull(t.stdout, frame); err != nil {
				return nil, fmt.Errorf("read MCP stdio content-length frame: %w", err)
			}
			return frame, nil
		}
		if !json.Valid([]byte(trimmed)) {
			return nil, errors.New("MCP stdio emitted non-JSON stdout")
		}
		return []byte(trimmed), nil
	}
}

func (t *stdioTransport) Close() error {
	var closeErr error
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()
		if t.stdin != nil {
			_ = t.stdin.Close()
		}
		if t.cmd != nil && t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		select {
		case err := <-t.waitDone:
			if err != nil {
				// A killed helper is expected during normal teardown.
				if !strings.Contains(strings.ToLower(err.Error()), "signal: killed") {
					closeErr = err
				}
			}
		case <-time.After(2 * time.Second):
			closeErr = errors.New("MCP stdio process did not exit")
		}
	})
	return closeErr
}

func readLineLimited(reader *bufio.Reader, max int64) (string, error) {
	if max <= 0 {
		max = 16 << 20
	}
	var line []byte
	for {
		part, isPrefix, err := reader.ReadLine()
		if err != nil {
			return "", err
		}
		line = append(line, part...)
		if int64(len(line)) > max {
			return "", errors.New("MCP frame exceeds the size limit")
		}
		if !isPrefix {
			return string(line), nil
		}
	}
}

func parseContentLength(line string) (int, error) {
	value := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(line), "content-length:"))
	length, err := strconv.Atoi(value)
	if err != nil || length < 0 {
		return 0, errors.New("invalid MCP Content-Length")
	}
	return length, nil
}

func readFirstSSEEvent(reader io.Reader, max int64) ([]byte, error) {
	if max <= 0 {
		max = 16 << 20
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024), int(max))
	var event []byte
	seenData := false
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.HasPrefix(line, "data:") {
			part := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			event = append(event, part...)
			event = append(event, '\n')
			seenData = true
		} else if line == "" && seenData {
			return event, nil
		}
		if int64(len(event)) > max {
			return nil, errors.New("MCP SSE event exceeds the size limit")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if seenData {
		return event, nil
	}
	return nil, errors.New("MCP SSE response contains no data event")
}

func decodeMessage(body []byte, max int64) (map[string]any, error) {
	if max <= 0 || max > 16<<20 {
		max = 16 << 20
	}
	if int64(len(body)) > max {
		return nil, errors.New("MCP response exceeds the configured size limit")
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, errors.New("MCP response is empty")
	}
	var message map[string]any
	if json.Unmarshal(trimmed, &message) == nil {
		return message, nil
	}
	// Streamable HTTP may return one or more SSE events. The first complete
	// data event is the JSON-RPC response; comments and event names are ignored.
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	scanner.Buffer(make([]byte, 1024), int(max))
	var data []string
	flush := func() bool {
		if len(data) == 0 {
			return false
		}
		candidate := strings.TrimSpace(strings.Join(data, "\n"))
		data = nil
		return json.Unmarshal([]byte(candidate), &message) == nil
	}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		if line == "" && flush() {
			return message, nil
		}
	}
	if scanner.Err() != nil {
		return nil, scanner.Err()
	}
	if flush() {
		return message, nil
	}
	return nil, errors.New("MCP response is not valid JSON-RPC")
}

func openTransport(ctx context.Context, client Client, server domain.MCPServer) (transport, error) {
	kind, err := classifyProtocol(server.Protocol)
	if err != nil {
		return nil, err
	}
	secret, err := client.resolveSecret(ctx, server.SecretRef)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "http":
		endpoint, err := validateHTTPURL(server.Endpoint)
		if err != nil {
			return nil, err
		}
		return &httpTransport{client: client.httpClient(), endpoint: endpoint, maxResponse: client.maxResponse(), maxRequest: client.maxRequest(), secret: secret}, nil
	case "stdio":
		if !client.AllowStdio {
			return nil, ErrUnsupportedProtocol
		}
		command, args, env, err := parseStdioCommand(server)
		if err != nil {
			return nil, err
		}
		if (len(env) > 0 || secret != "") && !client.AllowStdioEnvironment {
			return nil, errors.New("MCP stdio environment injection is disabled")
		}
		if secret != "" {
			if env == nil {
				env = map[string]string{}
			}
			env["ADRO_MCP_BEARER_TOKEN"] = secret
		}
		cmd := exec.CommandContext(ctx, command, args...)
		if len(env) > 0 {
			cmd.Env = append(os.Environ(), envPairs(env)...)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("open MCP stdio stdout: %w", err)
		}
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("open MCP stdio stdin: %w", err)
		}
		// Always drain stderr. A child that fills stderr must never block the
		// protocol pipe; diagnostic output is intentionally not returned to the
		// caller because it may contain secrets.
		cmd.Stderr = io.Discard
		if err := cmd.Start(); err != nil {
			_ = stdout.Close()
			_ = stdin.Close()
			return nil, fmt.Errorf("start MCP stdio server: %w", err)
		}
		waitDone := make(chan error, 1)
		go func() { waitDone <- cmd.Wait() }()
		return &stdioTransport{cmd: cmd, stdin: stdin, stdout: bufio.NewReaderSize(stdout, 64<<10), maxFrame: client.maxStdioFrame(), waitDone: waitDone}, nil
	default:
		return nil, ErrUnsupportedProtocol
	}
}

func classifyProtocol(protocol string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "http", "https", "sse", "streamable-http", "streamable_http", "remote", "remote-http":
		return "http", nil
	case "stdio", "command", "command-line":
		return "stdio", nil
	default:
		return "", ErrUnsupportedProtocol
	}
}

func validateHTTPURL(raw string) (string, error) {
	parsed, err := urlParse(raw)
	if err != nil || parsed.host == "" || (parsed.scheme != "http" && parsed.scheme != "https") {
		return "", errors.New("MCP endpoint must be an http or https URL")
	}
	if parsed.userinfo {
		return "", errors.New("MCP endpoint must not contain credentials")
	}
	return parsed.raw, nil
}

type parsedURL struct {
	raw      string
	scheme   string
	host     string
	userinfo bool
}

func urlParse(raw string) (parsedURL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return parsedURL{}, err
	}
	return parsedURL{raw: parsed.String(), scheme: strings.ToLower(parsed.Scheme), host: parsed.Host, userinfo: parsed.User != nil}, nil
}

func parseStdioCommand(server domain.MCPServer) (string, []string, map[string]string, error) {
	config := server.Configuration
	command, _ := config["command"].(string)
	command = strings.TrimSpace(command)
	if command == "" {
		endpoint := strings.TrimSpace(server.Endpoint)
		for _, prefix := range []string{"stdio://", "command://"} {
			if strings.HasPrefix(strings.ToLower(endpoint), prefix) {
				endpoint = endpoint[len(prefix):]
				break
			}
		}
		if endpoint != "" && !strings.Contains(endpoint, "://") {
			command = endpoint
		}
	}
	if command == "" || strings.ContainsRune(command, '\x00') {
		return "", nil, nil, errors.New("MCP stdio command is required")
	}
	args, err := stringSlice(config["args"], 32, 4096)
	if err != nil {
		return "", nil, nil, fmt.Errorf("MCP stdio args: %w", err)
	}
	env, err := stringMap(config["env"], 32, 4096)
	if err != nil {
		return "", nil, nil, fmt.Errorf("MCP stdio env: %w", err)
	}
	return command, args, env, nil
}

func stringSlice(value any, maxItems, maxLength int) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]string); ok {
			items = make([]any, len(typed))
			for i := range typed {
				items[i] = typed[i]
			}
		} else {
			return nil, errors.New("must be an array of strings")
		}
	}
	if len(items) > maxItems {
		return nil, errors.New("contains too many items")
	}
	out := make([]string, len(items))
	for i, item := range items {
		text, ok := item.(string)
		if !ok || strings.ContainsRune(text, '\x00') || len(text) > maxLength {
			return nil, errors.New("contains an invalid string")
		}
		out[i] = text
	}
	return out, nil
}

func stringMap(value any, maxItems, maxLength int) (map[string]string, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.(map[string]any)
	if !ok {
		if typed, ok := value.(map[string]string); ok {
			items = make(map[string]any, len(typed))
			for key, value := range typed {
				items[key] = value
			}
		} else {
			return nil, errors.New("must be an object")
		}
	}
	if len(items) > maxItems {
		return nil, errors.New("contains too many entries")
	}
	out := make(map[string]string, len(items))
	for key, item := range items {
		text, ok := item.(string)
		if !ok || strings.ContainsRune(key, '\x00') || strings.ContainsRune(text, '\x00') || len(key) > 256 || len(text) > maxLength {
			return nil, errors.New("contains an invalid entry")
		}
		out[key] = text
	}
	return out, nil
}

func envPairs(values map[string]string) []string {
	pairs := make([]string, 0, len(values))
	for key, value := range values {
		pairs = append(pairs, key+"="+value)
	}
	return pairs
}
