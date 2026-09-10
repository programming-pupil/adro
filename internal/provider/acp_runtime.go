package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/internal/telemetry"
)

type acpRuntimeSpec struct {
	ResumeMethod       string
	SetModel           bool
	ThinkingConfigID   string
	ThinkingOnCommand  bool
	PromptContentAlias bool
	Authenticate       bool
	ProjectMeta        bool
	BareResume         bool
	RequireResumeCap   bool
	RequiredConfig     map[string]string
}

var acpRuntimeSpecs = map[string]acpRuntimeSpec{
	"hermes":     {ResumeMethod: "session/resume", SetModel: true},
	"kimi":       {ResumeMethod: "session/resume", SetModel: true, ThinkingConfigID: "thinking"},
	"kiro-cli":   {ResumeMethod: "session/load", SetModel: true, PromptContentAlias: true},
	"qodercli":   {ResumeMethod: "session/resume", SetModel: true},
	"qoderclicn": {ResumeMethod: "session/resume", SetModel: true},
	"traecli":    {ResumeMethod: "session/load", SetModel: true},
	"grok":       {ResumeMethod: "session/load", SetModel: true, ThinkingOnCommand: true, Authenticate: true},
	"qwenpaw":    {ResumeMethod: "session/load", ProjectMeta: true},
	"reasonix":   {ResumeMethod: "session/resume", SetModel: true},
	"mcode":      {ResumeMethod: "session/load"},
	"dim": {
		ResumeMethod: "session/load", SetModel: true, ThinkingConfigID: "thought_level",
		RequiredConfig: map[string]string{"mode": "agent", "permission": "full-access"},
	},
	"zeroclaw": {ResumeMethod: "session/resume", BareResume: true, RequireResumeCap: true},
}

func isACPRuntime(kind string) bool {
	_, ok := acpRuntimeSpecs[kind]
	return ok
}

func acpRuntimeLaunchArgs(kind, thinkingLevel string, customArgs []string) []string {
	customArgs = acpForwardedCustomArgs(kind, customArgs)
	var args []string
	switch kind {
	case "kiro-cli":
		args = []string{"acp", "--trust-all-tools"}
	case "qodercli", "qoderclicn":
		args = []string{"--yolo", "--acp"}
	case "traecli":
		args = []string{"acp", "serve", "--yolo"}
	case "grok":
		args = []string{"--no-auto-update", "agent", "--always-approve"}
		if strings.TrimSpace(thinkingLevel) != "" {
			args = append(args, "--effort", strings.TrimSpace(thinkingLevel))
		}
		args = append(args, customArgs...)
		return append(args, "stdio")
	case "reasonix":
		args = []string{"acp", "--profile", "balanced", "--planner", "auto", "--sandbox-network", "auto", "--sandbox-bash", "auto", "--workspace-only"}
	default:
		args = []string{"acp"}
	}
	return append(args, customArgs...)
}

func acpForwardedCustomArgs(kind string, args []string) []string {
	if kind != "zeroclaw" {
		return append([]string(nil), args...)
	}
	result := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--agent" || arg == "--agent-alias" {
			if index+1 < len(args) {
				index++
			}
			continue
		}
		if strings.HasPrefix(arg, "--agent=") || strings.HasPrefix(arg, "--agent-alias=") {
			continue
		}
		result = append(result, arg)
	}
	return result
}

func acpAgentAlias(args []string) string {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if (arg == "--agent" || arg == "--agent-alias") && index+1 < len(args) {
			return strings.TrimSpace(args[index+1])
		}
		for _, prefix := range []string{"--agent=", "--agent-alias="} {
			if strings.HasPrefix(arg, prefix) {
				return strings.TrimSpace(strings.TrimPrefix(arg, prefix))
			}
		}
	}
	return ""
}

type acpTranscript struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (o *acpTranscript) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.data.Write(data)
}

func (o *acpTranscript) appendJSON(value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = o.Write(data)
}

func (o *acpTranscript) Bytes() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]byte(nil), o.data.Bytes()...)
}

type acpRPCResult struct {
	result json.RawMessage
	err    error
}

type acpRuntimeError struct {
	method  string
	code    int
	message string
	data    string
}

func (e *acpRuntimeError) Error() string {
	if e.data != "" {
		return fmt.Sprintf("%s: %s (code=%d, data=%s)", e.method, e.message, e.code, e.data)
	}
	return fmt.Sprintf("%s: %s (code=%d)", e.method, e.message, e.code)
}

type acpRuntimeClient struct {
	stdin      io.Writer
	transcript *acpTranscript
	writeMu    sync.Mutex
	mu         sync.Mutex
	nextID     int
	pending    map[string]chan acpRPCResult
	protocol   error
	activity   chan struct{}
}

func newACPRuntimeClient(stdin io.Writer, transcript *acpTranscript) *acpRuntimeClient {
	return &acpRuntimeClient{
		stdin: stdin, transcript: transcript, nextID: 1,
		pending: map[string]chan acpRPCResult{}, activity: make(chan struct{}, 1),
	}
}

func (c *acpRuntimeClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	key := fmt.Sprintf("%d", id)
	response := make(chan acpRPCResult, 1)
	c.pending[key] = response
	c.mu.Unlock()

	frame := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	if err := c.write(frame); err != nil {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return nil, fmt.Errorf("write %s: %w", method, err)
	}
	select {
	case result := <-response:
		return result.result, result.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *acpRuntimeClient) write(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(data)
	return err
}

func (c *acpRuntimeClient) handleLine(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	_, _ = c.transcript.Write(append(append([]byte(nil), line...), '\n'))
	select {
	case c.activity <- struct{}{}:
	default:
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		c.fail(fmt.Errorf("ACP stdout contained malformed JSON: %w", err))
		return
	}
	id := canonicalACPID(raw["id"])
	if id != "" && (raw["result"] != nil || raw["error"] != nil) {
		c.handleResponse(id, raw)
		return
	}
	if id != "" && raw["method"] != nil {
		c.handleRequest(id, raw)
	}
}

func canonicalACPID(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return string(raw)
}

func (c *acpRuntimeClient) handleResponse(id string, raw map[string]json.RawMessage) {
	c.mu.Lock()
	pending := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if pending == nil {
		return
	}
	if rawError := raw["error"]; len(rawError) > 0 && !bytes.Equal(bytes.TrimSpace(rawError), []byte("null")) {
		var rpcErr struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(rawError, &rpcErr); err != nil {
			pending <- acpRPCResult{err: fmt.Errorf("invalid ACP error response: %w", err)}
			return
		}
		method := "ACP request"
		pending <- acpRPCResult{err: &acpRuntimeError{method: method, code: rpcErr.Code, message: rpcErr.Message, data: strings.TrimSpace(string(rpcErr.Data))}}
		return
	}
	result, ok := raw["result"]
	if !ok {
		pending <- acpRPCResult{err: errors.New("ACP response omitted result")}
		return
	}
	pending <- acpRPCResult{result: result}
}

type acpPermissionOption struct {
	ID   string
	Kind string
}

func (c *acpRuntimeClient) handleRequest(id string, raw map[string]json.RawMessage) {
	var method string
	if json.Unmarshal(raw["method"], &method) != nil {
		return
	}
	if method != "session/request_permission" {
		_ = c.write(map[string]any{
			"jsonrpc": "2.0", "id": json.RawMessage(raw["id"]),
			"error": map[string]any{"code": -32601, "message": "method not found: " + method},
		})
		return
	}
	optionID, ok := selectACPPermission(raw["params"])
	if !ok {
		_ = c.write(map[string]any{
			"jsonrpc": "2.0", "id": json.RawMessage(raw["id"]),
			"error": map[string]any{"code": -32603, "message": "no safe permission option was offered"},
		})
		return
	}
	_ = c.write(map[string]any{
		"jsonrpc": "2.0", "id": json.RawMessage(raw["id"]),
		"result": map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": optionID}},
	})
}

func selectACPPermission(raw json.RawMessage) (string, bool) {
	var params struct {
		Options []struct {
			OptionID      string `json:"optionId"`
			OptionIDSnake string `json:"option_id"`
			Kind          string `json:"kind"`
		} `json:"options"`
	}
	if json.Unmarshal(raw, &params) != nil {
		return "", false
	}
	options := make([]acpPermissionOption, 0, len(params.Options))
	for _, option := range params.Options {
		id := strings.TrimSpace(option.OptionID)
		if id == "" {
			id = strings.TrimSpace(option.OptionIDSnake)
		}
		options = append(options, acpPermissionOption{ID: id, Kind: strings.ToLower(strings.TrimSpace(option.Kind))})
	}
	for _, preferred := range []string{"allow_session", "approve_for_session"} {
		for _, option := range options {
			if option.ID == preferred && (option.Kind == "allow_once" || option.Kind == "allow_always") {
				return option.ID, true
			}
		}
	}
	for _, option := range options {
		if option.ID != "" && option.Kind == "allow_once" {
			return option.ID, true
		}
	}
	for _, option := range options {
		if option.ID != "" && option.Kind == "reject_once" {
			return option.ID, true
		}
	}
	return "", false
}

func (c *acpRuntimeClient) fail(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	if c.protocol == nil {
		c.protocol = err
	}
	for id, pending := range c.pending {
		pending <- acpRPCResult{err: err}
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

func (c *acpRuntimeClient) protocolError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.protocol
}

type acpRuntimeProcess struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	client     *acpRuntimeClient
	transcript *acpTranscript
	readerDone chan struct{}
	stderrDone chan struct{}
	closeOnce  sync.Once
	waitErr    error
}

func startACPRuntimeProcess(ctx context.Context, path string, args []string, workDir, kind string, onStart func(int)) (*acpRuntimeProcess, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	configureLocalCommand(cmd)
	cmd.Cancel = func() error { return cancelLocalCommand(cmd) }
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Dir = workDir
	cmd.Env = traceEnvironment(os.Environ(), telemetry.Environment(ctx))
	if kind == "hermes" {
		cmd.Env = replaceEnvironmentValue(cmd.Env, "HERMES_YOLO_MODE", "1")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ACP stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("ACP stderr pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("ACP stdin pipe: %w", err)
	}
	transcript := &acpTranscript{}
	client := newACPRuntimeClient(stdin, transcript)
	process := &acpRuntimeProcess{
		cmd: cmd, stdin: stdin, client: client, transcript: transcript,
		readerDone: make(chan struct{}), stderrDone: make(chan struct{}),
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	if onStart != nil {
		onStart(cmd.Process.Pid)
	}
	go func() {
		defer close(process.readerDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 8<<20)
		for scanner.Scan() {
			client.handleLine(scanner.Bytes())
		}
		if err := scanner.Err(); err != nil {
			client.fail(fmt.Errorf("ACP stdout: %w", err))
		} else {
			client.fail(errors.New("ACP process closed stdout"))
		}
	}()
	go func() {
		defer close(process.stderrDone)
		_, _ = io.Copy(transcript, stderr)
	}()
	return process, nil
}

func (p *acpRuntimeProcess) close() error {
	p.closeOnce.Do(func() {
		_ = p.stdin.Close()
		done := make(chan error, 1)
		go func() { done <- p.cmd.Wait() }()
		select {
		case p.waitErr = <-done:
		case <-time.After(time.Second):
			_ = terminateLocalCommand(p.cmd)
			select {
			case p.waitErr = <-done:
			case <-time.After(250 * time.Millisecond):
				p.waitErr = errors.New("ACP process did not exit after termination")
			}
		}
		select {
		case <-p.readerDone:
		case <-time.After(250 * time.Millisecond):
		}
		select {
		case <-p.stderrDone:
		case <-time.After(250 * time.Millisecond):
		}
	})
	return p.waitErr
}

func executeACPRuntime(
	ctx context.Context,
	path string,
	args []string,
	input, workDir, sessionID string,
	resumed bool,
	model, thinkingLevel, kind string,
	customArgs []string,
	onStart func(int),
) (pid int, output []byte, runErr error) {
	spec, ok := acpRuntimeSpecs[kind]
	if !ok {
		return 0, nil, fmt.Errorf("unsupported ACP runtime %q", kind)
	}
	process, err := startACPRuntimeProcess(ctx, path, args, workDir, kind, func(startedPID int) {
		pid = startedPID
		if onStart != nil {
			onStart(startedPID)
		}
	})
	if err != nil {
		return 0, nil, err
	}
	defer func() {
		protocolErr := process.client.protocolError()
		_ = process.close()
		output = process.transcript.Bytes()
		if runErr == nil && ctx.Err() != nil {
			runErr = ctx.Err()
		} else if runErr == nil && protocolErr != nil {
			runErr = protocolErr
		}
	}()

	client := process.client
	initResult, err := client.request(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientInfo":         map[string]any{"name": "adro", "version": "1"},
		"clientCapabilities": map[string]any{},
	})
	if err != nil {
		return pid, nil, fmt.Errorf("%s initialize failed: %w", kind, err)
	}
	if spec.Authenticate {
		method, err := selectACPAuthMethod(initResult, strings.TrimSpace(os.Getenv("XAI_API_KEY")) != "")
		if err != nil {
			return pid, nil, fmt.Errorf("%s authentication setup failed: %w", kind, err)
		}
		if _, err := client.request(ctx, "authenticate", map[string]any{"methodId": method, "_meta": map[string]any{"headless": true}}); err != nil {
			return pid, nil, fmt.Errorf("%s authenticate failed: %w", kind, err)
		}
	}
	if resumed && spec.RequireResumeCap && !acpResumeCapability(initResult) {
		return pid, nil, fmt.Errorf("%s resume is not advertised by the installed runtime", kind)
	}

	cwd := workDir
	if cwd == "" {
		cwd = "."
	} else if absolute, err := filepath.Abs(cwd); err == nil {
		cwd = absolute
	}
	params := map[string]any{"cwd": cwd, "mcpServers": []any{}}
	if spec.ProjectMeta && cwd != "." {
		params["_meta"] = map[string]any{"qwenpaw.coding_project_dir": cwd}
	}
	if kind == "zeroclaw" {
		if alias := acpAgentAlias(customArgs); alias != "" {
			params["agentAlias"] = alias
		}
	}
	method := "session/new"
	if resumed {
		method = spec.ResumeMethod
		params["sessionId"] = sessionID
		if spec.BareResume {
			params = map[string]any{"sessionId": sessionID}
		}
	}
	sessionResult, err := client.request(ctx, method, params)
	if err != nil {
		return pid, nil, fmt.Errorf("%s %s failed: %w", kind, method, err)
	}
	actualSessionID := acpSessionID(sessionResult)
	if resumed && actualSessionID == "" {
		actualSessionID = sessionID
	}
	if !validProviderSessionID(actualSessionID) {
		return pid, nil, fmt.Errorf("%s %s returned no valid session ID", kind, method)
	}
	if resumed && actualSessionID != sessionID {
		return pid, nil, fmt.Errorf("%s continuation opened session %s, expected %s", kind, actualSessionID, sessionID)
	}
	process.transcript.appendJSON(map[string]any{"type": "session.start", "runtime": kind, "session_id": actualSessionID})

	configIDs := make([]string, 0, len(spec.RequiredConfig))
	for id := range spec.RequiredConfig {
		configIDs = append(configIDs, id)
	}
	sort.Strings(configIDs)
	for _, id := range configIDs {
		if _, err := client.request(ctx, "session/set_config_option", map[string]any{
			"sessionId": actualSessionID, "configId": id, "value": spec.RequiredConfig[id],
		}); err != nil {
			return pid, nil, fmt.Errorf("%s could not set %s=%s: %w", kind, id, spec.RequiredConfig[id], err)
		}
	}
	if strings.TrimSpace(model) != "" {
		if !spec.SetModel {
			return pid, nil, fmt.Errorf("%s does not support session-scoped model selection", kind)
		}
		if _, err := client.request(ctx, "session/set_model", map[string]any{
			"sessionId": actualSessionID, "modelId": strings.TrimSpace(model),
		}); err != nil {
			return pid, nil, fmt.Errorf("%s could not switch to model %q: %w", kind, model, err)
		}
	}
	if level := strings.TrimSpace(thinkingLevel); level != "" && !spec.ThinkingOnCommand {
		configID := spec.ThinkingConfigID
		if configID == "" {
			configID = acpThinkingConfigID(sessionResult)
		}
		if configID == "" {
			return pid, nil, fmt.Errorf("%s did not advertise a session-scoped thinking option", kind)
		}
		if _, err := client.request(ctx, "session/set_config_option", map[string]any{
			"sessionId": actualSessionID, "configId": configID, "value": level,
		}); err != nil {
			return pid, nil, fmt.Errorf("%s could not set thinking level %q: %w", kind, level, err)
		}
	}

	blocks := []map[string]any{{"type": "text", "text": input}}
	promptParams := map[string]any{"sessionId": actualSessionID, "prompt": blocks}
	if spec.PromptContentAlias {
		promptParams["content"] = blocks
	}
	promptResult, err := client.request(ctx, "session/prompt", promptParams)
	if err != nil {
		return pid, nil, fmt.Errorf("%s session/prompt failed: %w", kind, err)
	}
	stopReason := acpStopReason(promptResult)
	if stopReason == "" {
		return pid, nil, fmt.Errorf("%s session/prompt returned no stop reason", kind)
	}
	switch strings.ToLower(stopReason) {
	case "end_turn", "completed", "complete", "success":
	case "cancelled", "canceled":
		return pid, nil, fmt.Errorf("%s cancelled the prompt", kind)
	default:
		return pid, nil, fmt.Errorf("%s stopped with reason %q", kind, stopReason)
	}
	waitForACPQuiescence(ctx, client.activity, 75*time.Millisecond, 2*time.Second)
	if err := client.protocolError(); err != nil {
		return pid, nil, err
	}
	terminal := map[string]any{"type": "result", "runtime": kind, "session_id": actualSessionID, "stop_reason": stopReason}
	if usage := acpUsageValue(promptResult); usage != nil {
		terminal["usage"] = usage
	}
	process.transcript.appendJSON(terminal)
	return pid, nil, nil
}

func waitForACPQuiescence(ctx context.Context, activity <-chan struct{}, quiet, maximum time.Duration) {
	for {
		select {
		case <-activity:
		default:
			goto drained
		}
	}
drained:
	quietTimer := time.NewTimer(quiet)
	defer quietTimer.Stop()
	maximumTimer := time.NewTimer(maximum)
	defer maximumTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-maximumTimer.C:
			return
		case <-quietTimer.C:
			return
		case <-activity:
			if !quietTimer.Stop() {
				select {
				case <-quietTimer.C:
				default:
				}
			}
			quietTimer.Reset(quiet)
		}
	}
}

func acpSessionID(raw json.RawMessage) string {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return nestedString(value, "sessionId", "session_id")
}

func acpStopReason(raw json.RawMessage) string {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return nestedString(value, "stopReason", "stop_reason")
}

func nestedString(value any, keys ...string) string {
	switch item := value.(type) {
	case map[string]any:
		for _, key := range keys {
			if result, ok := item[key].(string); ok && strings.TrimSpace(result) != "" {
				return strings.TrimSpace(result)
			}
		}
		for _, child := range item {
			if result := nestedString(child, keys...); result != "" {
				return result
			}
		}
	case []any:
		for _, child := range item {
			if result := nestedString(child, keys...); result != "" {
				return result
			}
		}
	}
	return ""
}

func acpThinkingConfigID(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return findACPThinkingConfigID(value)
}

func findACPThinkingConfigID(value any) string {
	switch item := value.(type) {
	case map[string]any:
		id := directString(item, "id", "configId", "config_id")
		category := strings.ToLower(directString(item, "category", "name"))
		lowerID := strings.ToLower(id)
		if id != "" && (strings.Contains(lowerID, "think") || strings.Contains(lowerID, "thought") || strings.Contains(lowerID, "reason") || strings.Contains(lowerID, "effort") || strings.Contains(category, "think") || strings.Contains(category, "reason")) {
			return id
		}
		for _, child := range item {
			if result := findACPThinkingConfigID(child); result != "" {
				return result
			}
		}
	case []any:
		for _, child := range item {
			if result := findACPThinkingConfigID(child); result != "" {
				return result
			}
		}
	}
	return ""
}

func directString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if result, ok := value[key].(string); ok && strings.TrimSpace(result) != "" {
			return strings.TrimSpace(result)
		}
	}
	return ""
}

func acpUsageValue(raw json.RawMessage) any {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	for _, key := range []string{"usage", "tokenUsage", "token_usage"} {
		if usage, ok := value[key].(map[string]any); ok {
			return usage
		}
	}
	return nil
}

func selectACPAuthMethod(raw json.RawMessage, haveAPIKey bool) (string, error) {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return "", errors.New("initialize returned invalid authentication metadata")
	}
	methods := collectACPAuthMethods(value)
	if haveAPIKey && methods["xai.api_key"] {
		return "xai.api_key", nil
	}
	if methods["cached_token"] {
		return "cached_token", nil
	}
	if methods["xai.api_key"] {
		return "", errors.New("XAI_API_KEY is not set")
	}
	return "", errors.New("no supported authentication method was advertised")
}

func collectACPAuthMethods(value any) map[string]bool {
	result := map[string]bool{}
	var walk func(any)
	walk = func(raw any) {
		switch item := raw.(type) {
		case map[string]any:
			for key, child := range item {
				if key == "authMethods" || key == "auth_methods" {
					if values, ok := child.([]any); ok {
						for _, candidate := range values {
							switch method := candidate.(type) {
							case string:
								result[strings.TrimSpace(method)] = true
							case map[string]any:
								id := nestedString(method, "id", "methodId", "method_id")
								if id != "" {
									result[id] = true
								}
							}
						}
					}
					continue
				}
				walk(child)
			}
		case []any:
			for _, child := range item {
				walk(child)
			}
		}
	}
	walk(value)
	return result
}

func acpResumeCapability(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	var find func(any) bool
	find = func(raw any) bool {
		switch item := raw.(type) {
		case map[string]any:
			if resume, ok := item["resume"].(bool); ok && resume {
				return true
			}
			for _, child := range item {
				if find(child) {
					return true
				}
			}
		case []any:
			for _, child := range item {
				if find(child) {
					return true
				}
			}
		}
		return false
	}
	return find(value)
}
