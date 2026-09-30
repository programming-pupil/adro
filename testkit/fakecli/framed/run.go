// Package framed provides a real stdio child fixture for protocol regressions.
// It is test infrastructure and must never be linked into the service binary.
package framed

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

type Options struct {
	Mode      string
	SessionID string
	Runtime   string
}

// Run preserves the framed baseline's lifecycle, cancellation, version,
// terminal, and resume-proof scenarios without an external account or shell.
// A nonzero returned status is an intentional child outcome, not a Go error.
func Run(ctx context.Context, in io.Reader, out io.Writer, opts Options) (int, error) {
	switch opts.Mode {
	case "", "cancel", "version", "session-mismatch", "resume-rejected", "linger", "exit-after-result":
	default:
		return 0, fmt.Errorf("unknown framed fixture mode")
	}
	if opts.SessionID == "" || opts.Runtime == "" {
		return 0, fmt.Errorf("fixture session and runtime are required")
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return 0, err
		}
		return 0, io.ErrUnexpectedEOF
	}
	var request struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
		return 0, fmt.Errorf("decode fixture request: %w", err)
	}
	if request.Type != "execute" || request.RequestID == "" {
		return 0, fmt.Errorf("fixture expects an execute request with an ID")
	}
	if opts.Mode == "cancel" {
		if !scanner.Scan() {
			return 0, fmt.Errorf("fixture did not receive cancellation")
		}
		var cancelRequest struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &cancelRequest); err != nil || cancelRequest.Type != "cancel" || cancelRequest.RequestID != request.RequestID {
			return 0, fmt.Errorf("fixture received invalid cancellation")
		}
		<-ctx.Done()
		return 0, ctx.Err()
	}
	version := 1
	if opts.Mode == "version" {
		version = 2
	}
	sessionID := opts.SessionID
	if opts.Mode == "session-mismatch" {
		sessionID = "different-session"
	}
	frames := []map[string]any{
		{"v": version, "type": "ready", "runtime": opts.Runtime, "protocol_version": version},
		{"v": version, "type": "session", "request_id": request.RequestID, "session_id": sessionID},
		{"v": version, "type": "tool_call", "request_id": request.RequestID, "call_id": "tool-1", "name": "shell", "arguments": `{"command":"pwd"}`},
		{"v": version, "type": "tool_result", "request_id": request.RequestID, "call_id": "tool-1", "name": "shell", "output": "ok"},
		{"v": version, "type": "usage", "request_id": request.RequestID, "provider": "provider", "model": "model", "input_tokens": 13, "output_tokens": 6},
		{"v": version, "type": "result", "request_id": request.RequestID, "session_id": sessionID, "status": "completed", "output": "done", "resume_rejected": opts.Mode == "resume-rejected"},
	}
	encoder := json.NewEncoder(out)
	for _, frame := range frames {
		if err := encoder.Encode(frame); err != nil {
			return 0, fmt.Errorf("write fixture frame: %w", err)
		}
	}
	if opts.Mode == "linger" {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	if opts.Mode == "exit-after-result" {
		return 1, nil
	}
	return 0, nil
}
