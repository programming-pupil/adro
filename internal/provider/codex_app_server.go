package provider

// Codex app-server is a long-lived JSON-RPC process. A local run therefore
// needs the same lifecycle as Multica: initialize, start/resume a native
// thread, start one turn, and wait for turn/completed.

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
	"regexp"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/telemetry"
)

var codexNativeThreadPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z_-]{15,127}$`)

type codexRPCError struct {
	Code    int
	Message string
}

func (e *codexRPCError) Error() string {
	return fmt.Sprintf("codex app-server RPC error %d: %s", e.Code, e.Message)
}

// executeCodexAppServer puts the thread proof obtained from thread/start or
// thread/resume into the provider's existing JSONL evidence stream. It is
// derived from the RPC result, never from model text or a fixture.
func executeCodexAppServer(ctx context.Context, path string, args []string, input, workDir, priorSession string, resumed bool) (int, []byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	configureLocalCommand(cmd)
	cmd.Cancel = func() error { return cancelLocalCommand(cmd) }
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Dir = workDir
	cmd.Env = traceEnvironment(os.Environ(), telemetry.Environment(ctx))

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 0, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return 0, nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return 0, nil, err
	}

	pid := cmd.Process.Pid
	reader := bufio.NewReaderSize(stdout, 256*1024)
	var evidence bytes.Buffer
	nextID := 1

	send := func(method string, params any) (int, error) {
		id := nextID
		nextID++
		message := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
		if params != nil {
			message["params"] = params
		}
		data, err := json.Marshal(message)
		if err != nil {
			return 0, err
		}
		data = append(data, '\n')
		if _, err := stdin.Write(data); err != nil {
			return 0, fmt.Errorf("write %s: %w", method, err)
		}
		return id, nil
	}

	respond := func(id json.RawMessage, result any) error {
		message := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result}
		data, err := json.Marshal(message)
		if err != nil {
			return err
		}
		data = append(data, '\n')
		_, err = stdin.Write(data)
		return err
	}

	readMessage := func() (map[string]json.RawMessage, error) {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimSpace(line)
			if len(line) > 0 {
				var raw map[string]json.RawMessage
				if json.Unmarshal(line, &raw) == nil {
					appendCodexEvidence(&evidence, raw)
					return raw, nil
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		if err != nil {
			return nil, err
		}
		return nil, errors.New("codex app-server emitted invalid JSON-RPC")
	}

	approval := func(method string) map[string]any {
		switch method {
		case "item/commandExecution/requestApproval", "execCommandApproval", "item/fileChange/requestApproval", "applyPatchApproval":
			return map[string]any{"decision": "accept"}
		case "item/permissions/requestApproval":
			return map[string]any{"permissions": map[string]any{}, "scope": "turn"}
		case "mcpServer/elicitation/request":
			return map[string]any{"action": "accept", "content": nil, "_meta": nil}
		default:
			return map[string]any{}
		}
	}

	waitResponse := func(id int) (json.RawMessage, error) {
		for {
			raw, err := readMessage()
			if err != nil {
				return nil, err
			}
			if methodBytes, ok := raw["method"]; ok {
				var method string
				_ = json.Unmarshal(methodBytes, &method)
				if requestID, ok := raw["id"]; ok {
					if err := respond(requestID, approval(method)); err != nil {
						return nil, err
					}
				}
				continue
			}
			var responseID int
			if responseIDBytes, ok := raw["id"]; !ok || json.Unmarshal(responseIDBytes, &responseID) != nil || responseID != id {
				continue
			}
			if errorBytes, ok := raw["error"]; ok {
				var rpcErr struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				}
				_ = json.Unmarshal(errorBytes, &rpcErr)
				return nil, &codexRPCError{Code: rpcErr.Code, Message: rpcErr.Message}
			}
			return raw["result"], nil
		}
	}

	waitNotification := func() (string, map[string]any, error) {
		for {
			raw, err := readMessage()
			if err != nil {
				return "", nil, err
			}
			methodBytes, ok := raw["method"]
			if !ok {
				continue
			}
			var method string
			_ = json.Unmarshal(methodBytes, &method)
			if id, ok := raw["id"]; ok {
				if err := respond(id, approval(method)); err != nil {
					return "", nil, err
				}
				continue
			}
			params := map[string]any{}
			if paramsBytes, ok := raw["params"]; ok {
				_ = json.Unmarshal(paramsBytes, &params)
			}
			return method, params, nil
		}
	}

	finish := func(runErr error) (int, []byte, error) {
		_ = terminateLocalCommand(cmd)
		_ = stdin.Close()
		_ = cmd.Wait()
		return pid, appendEvidenceStderr(evidence.Bytes(), stderr.Bytes()), runErr
	}

	initializeID, err := send("initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "adro-local-provider", "title": "ADRO Local Provider", "version": "1.0.0"},
		"capabilities": map[string]any{"experimentalApi": true},
	})
	if err != nil {
		return finish(err)
	}
	if _, err := waitResponse(initializeID); err != nil {
		return finish(fmt.Errorf("codex initialize failed: %w", err))
	}
	if _, err := stdin.Write([]byte("{\"jsonrpc\":\"2.0\",\"method\":\"initialized\"}\n")); err != nil {
		return finish(fmt.Errorf("write initialized: %w", err))
	}

	threadID := ""
	if resumed && priorSession != "" {
		resumeID, err := send("thread/resume", map[string]any{"threadId": priorSession, "cwd": workDir, "model": nil, "developerInstructions": nil})
		if err != nil {
			return finish(fmt.Errorf("codex thread/resume failed: %w", err))
		}
		result, responseErr := waitResponse(resumeID)
		if responseErr == nil {
			threadID = codexThreadID(result)
		} else if !isRecoverableCodexResumeError(responseErr) {
			return finish(fmt.Errorf("codex thread/resume failed: %w", responseErr))
		}
	}
	if threadID == "" {
		startID, err := send("thread/start", map[string]any{"cwd": workDir, "developerInstructions": nil, "persistExtendedHistory": true})
		if err != nil {
			return finish(err)
		}
		result, err := waitResponse(startID)
		if err != nil {
			return finish(fmt.Errorf("codex thread/start failed: %w", err))
		}
		threadID = codexThreadID(result)
		if threadID == "" {
			return finish(errors.New("codex thread/start returned no thread ID"))
		}
	}

	evidence.WriteString(fmt.Sprintf("{\"type\":\"thread.started\",\"thread_id\":%q}\n", threadID))
	turnID, err := send("turn/start", map[string]any{
		"threadId": threadID,
		"input":    []map[string]any{{"type": "text", "text": input}},
	})
	if err != nil {
		return finish(err)
	}
	if _, err := waitResponse(turnID); err != nil {
		return finish(fmt.Errorf("codex turn/start failed: %w", err))
	}

	for {
		method, params, err := waitNotification()
		if err != nil {
			return finish(err)
		}
		if method != "turn/completed" {
			continue
		}
		if notifiedThread, _ := params["threadId"].(string); notifiedThread != "" && notifiedThread != threadID {
			continue
		}
		if turn, ok := params["turn"].(map[string]any); ok {
			status, _ := turn["status"].(string)
			switch status {
			case "failed":
				return finish(errors.New("codex turn failed"))
			case "cancelled", "canceled", "aborted", "interrupted":
				return finish(fmt.Errorf("codex turn %s", status))
			}
		}
		return finish(nil)
	}
}

// Multica retries a resume only when Codex explicitly rejects the protocol
// request (for example, an unknown thread or an incompatible schema). A
// broken stdio transport, EOF, or context cancellation leaves the native
// session state uncertain and must fail closed instead of silently starting a
// new conversation.
func isRecoverableCodexResumeError(err error) bool {
	var rpcErr *codexRPCError
	return errors.As(err, &rpcErr)
}

// App-server emits token deltas, reasoning summaries, status updates and
// usage heartbeats in addition to the durable run evidence. Recording every
// delta can evict the final ADRO_RESULT_JSON marker from ADRO's bounded
// snapshot. Keep the same actionable evidence Multica keeps: tool lifecycle,
// completed agent messages, terminal turn state and protocol errors.
func appendCodexEvidence(dst *bytes.Buffer, raw map[string]json.RawMessage) {
	method := ""
	_ = json.Unmarshal(raw["method"], &method)
	keep := method == "thread/started" || method == "turn/completed" || method == "error"
	if strings.HasPrefix(method, "item/") {
		var params struct {
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		_ = json.Unmarshal(raw["params"], &params)
		keep = params.Item.Type == "commandExecution" || params.Item.Type == "fileChange" || params.Item.Type == "mcpToolCall" || (method == "item/completed" && params.Item.Type == "agentMessage")
		if method == "item/completed" && params.Item.Type == "agentMessage" && strings.Contains(params.Item.Text, "ADRO_RESULT_JSON") {
			// Keep a plain derived copy as a compatibility projection. Older ADRO
			// pipeline readers only inspect non-JSON lines for the marker, while
			// the source remains the real app-server item/completed event above.
			keep = true
		}
	}
	if !keep {
		return
	}
	data, err := json.Marshal(raw)
	if err == nil {
		dst.Write(data)
		dst.WriteByte('\n')
		var params struct {
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		_ = json.Unmarshal(raw["params"], &params)
		if method == "item/completed" && params.Item.Type == "agentMessage" && strings.Contains(params.Item.Text, "ADRO_RESULT_JSON") {
			dst.WriteString(params.Item.Text)
			dst.WriteByte('\n')
		}
	}
}

func codexThreadID(result json.RawMessage) string {
	var payload map[string]any
	if json.Unmarshal(result, &payload) != nil {
		return ""
	}
	thread, _ := payload["thread"].(map[string]any)
	id, _ := thread["id"].(string)
	id = strings.TrimSpace(id)
	if !codexNativeThreadPattern.MatchString(id) {
		return ""
	}
	return id
}

func appendEvidenceStderr(stdout, stderr []byte) []byte {
	if len(stderr) == 0 {
		return stdout
	}
	return append(append([]byte(nil), stdout...), stderr...)
}
