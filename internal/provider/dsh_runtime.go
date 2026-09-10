package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/internal/telemetry"
)

const (
	dshProtocolVersion  = 1
	dshProfile          = "adro"
	dshProcessExitGrace = time.Second
	dshStreamDrainGrace = 250 * time.Millisecond
)

type dshModelSelection struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

type dshExecuteRequest struct {
	Version         int                `json:"v"`
	Type            string             `json:"type"`
	RequestID       string             `json:"request_id"`
	Cwd             string             `json:"cwd"`
	Prompt          string             `json:"prompt"`
	ResumeSessionID string             `json:"resume_session_id,omitempty"`
	Model           *dshModelSelection `json:"model,omitempty"`
	ReasoningEffort string             `json:"reasoning_effort,omitempty"`
	MCPServers      []any              `json:"mcp_servers"`
}

type dshCancelRequest struct {
	Version   int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
}

type dshWireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type dshRuntimeFrame struct {
	Version         int             `json:"v"`
	Type            string          `json:"type"`
	Runtime         string          `json:"runtime,omitempty"`
	ProtocolVersion int             `json:"protocol_version,omitempty"`
	RequestID       string          `json:"request_id,omitempty"`
	SessionID       string          `json:"session_id,omitempty"`
	Status          string          `json:"status,omitempty"`
	Output          string          `json:"output,omitempty"`
	ResumeRejected  bool            `json:"resume_rejected,omitempty"`
	Error           *dshWireError   `json:"error,omitempty"`
	Code            string          `json:"code,omitempty"`
	Message         string          `json:"message,omitempty"`
	Models          []dshModelFrame `json:"models,omitempty"`
}

type dshModelFrame struct {
	ID       string            `json:"id"`
	Label    string            `json:"label"`
	Provider string            `json:"provider,omitempty"`
	Default  bool              `json:"default,omitempty"`
	Thinking *dshThinkingFrame `json:"thinking,omitempty"`
}

type dshThinkingFrame struct {
	SupportedLevels []RuntimeLevel `json:"supported_levels"`
	DefaultLevel    string         `json:"default_level,omitempty"`
}

func dshLaunchArgs(customArgs []string) []string {
	args := []string{"--profile", dshProfile, "--stdio"}
	return append(args, customArgs...)
}

func parseDSHModel(value string) (*dshModelSelection, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	providerPart, modelPart, ok := strings.Cut(value, "/")
	if !ok || providerPart == "" || modelPart == "" {
		return nil, errors.New("DSH model must use a provider/model identifier")
	}
	provider, err := url.PathUnescape(providerPart)
	if err != nil || strings.TrimSpace(provider) == "" {
		return nil, errors.New("DSH model provider is invalid")
	}
	model, err := url.PathUnescape(modelPart)
	if err != nil || strings.TrimSpace(model) == "" {
		return nil, errors.New("DSH model ID is invalid")
	}
	return &dshModelSelection{Provider: provider, ID: model}, nil
}

func executeDSHRuntime(
	ctx context.Context,
	path string,
	args []string,
	runID, input, workDir, sessionID, model, thinkingLevel string,
	resumed bool,
	environment map[string]string,
	onStart func(int),
) (pid int, output []byte, runErr error) {
	selection, err := parseDSHModel(model)
	if err != nil {
		return 0, nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	configureLocalCommand(cmd)
	cmd.Dir = workDir
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Env = applyRuntimeEnvironment(traceEnvironment(os.Environ(), telemetry.Environment(ctx)), environment)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, nil, fmt.Errorf("DSH stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 0, nil, fmt.Errorf("DSH stderr pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 0, nil, fmt.Errorf("DSH stdin pipe: %w", err)
	}
	transcript := &acpTranscript{}
	var writeMu sync.Mutex
	writeFrame := func(value any) error {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		data = append(data, '\n')
		writeMu.Lock()
		defer writeMu.Unlock()
		_, err = stdin.Write(data)
		return err
	}
	cmd.Cancel = func() error {
		_ = writeFrame(dshCancelRequest{Version: dshProtocolVersion, Type: "cancel", RequestID: runID})
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		<-timer.C
		return cancelLocalCommand(cmd)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return 0, nil, err
	}
	pid = cmd.Process.Pid
	if onStart != nil {
		onStart(pid)
	}
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(transcript, stderr)
	}()
	terminal := false
	defer func() {
		_ = stdin.Close()
		waitDone := make(chan error, 1)
		go func() { waitDone <- cmd.Wait() }()
		var waitErr error
		select {
		case waitErr = <-waitDone:
		case <-time.After(dshProcessExitGrace):
			_ = terminateLocalCommand(cmd)
			select {
			case waitErr = <-waitDone:
			case <-time.After(dshStreamDrainGrace):
				waitErr = errors.New("process did not exit after terminal result")
			}
		}
		// A validated terminal frame owns the protocol outcome. Some profile
		// versions fail during stdin-close cleanup after reporting completion;
		// their later process status cannot revoke the already committed result.
		if terminal {
			waitErr = nil
		}
		select {
		case <-stderrDone:
		case <-time.After(dshStreamDrainGrace):
		}
		output = transcript.Bytes()
		if runErr == nil && ctx.Err() != nil {
			runErr = ctx.Err()
		} else if runErr == nil && waitErr != nil {
			runErr = fmt.Errorf("DSH process failed: %w", waitErr)
		}
	}()

	request := dshExecuteRequest{
		Version: dshProtocolVersion, Type: "execute", RequestID: runID,
		Cwd: workDir, Prompt: input, Model: selection,
		ReasoningEffort: strings.TrimSpace(thinkingLevel), MCPServers: []any{},
	}
	if resumed {
		request.ResumeSessionID = sessionID
	}
	if err := writeFrame(request); err != nil {
		return pid, nil, fmt.Errorf("send DSH execute request: %w", err)
	}

	ready := false
	nativeSessionID := ""
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		_, _ = transcript.Write(append(append([]byte(nil), line...), '\n'))
		var frame dshRuntimeFrame
		if err := json.Unmarshal(line, &frame); err != nil {
			return pid, nil, fmt.Errorf("DSH emitted malformed JSON: %w", err)
		}
		if frame.Version != dshProtocolVersion {
			return pid, nil, fmt.Errorf("DSH returned unsupported protocol version %d", frame.Version)
		}
		if frame.RequestID != "" && frame.RequestID != runID {
			continue
		}
		switch frame.Type {
		case "ready":
			if frame.Runtime != "dsh" || (frame.ProtocolVersion != 0 && frame.ProtocolVersion != dshProtocolVersion) {
				return pid, nil, errors.New("DSH ready frame did not identify the expected protocol")
			}
			ready = true
		case "session":
			if !validProviderSessionID(frame.SessionID) {
				return pid, nil, errors.New("DSH returned an invalid session ID")
			}
			if resumed && frame.SessionID != sessionID {
				return pid, nil, fmt.Errorf("DSH continuation opened session %s, expected %s", frame.SessionID, sessionID)
			}
			nativeSessionID = frame.SessionID
		case "protocol_error":
			return pid, nil, fmt.Errorf("DSH protocol error: %s", strings.TrimSpace(frame.Code+": "+frame.Message))
		case "result":
			terminal = true
			if resumed && frame.ResumeRejected {
				return pid, nil, errors.New("DSH rejected the requested session continuation")
			}
			if frame.SessionID != "" {
				nativeSessionID = frame.SessionID
			}
			if resumed && nativeSessionID != sessionID {
				return pid, nil, fmt.Errorf("DSH continuation completed in session %s, expected %s", nativeSessionID, sessionID)
			}
			if frame.Status != "completed" {
				message := "runtime returned status " + frame.Status
				if frame.Error != nil {
					message = strings.TrimSpace(frame.Error.Code + ": " + frame.Error.Message)
				}
				return pid, nil, errors.New(message)
			}
			goto complete
		}
	}
	if err := scanner.Err(); err != nil {
		return pid, nil, fmt.Errorf("read DSH event stream: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return pid, nil, err
	}

complete:
	if !ready {
		return pid, nil, errors.New("DSH exited before the runtime protocol became ready")
	}
	if !terminal {
		return pid, nil, errors.New("DSH exited without a terminal result")
	}
	if !validProviderSessionID(nativeSessionID) {
		return pid, nil, errors.New("DSH terminal result did not prove a native session")
	}
	transcript.appendJSON(map[string]any{"type": "session.start", "runtime": "dsh", "session_id": nativeSessionID})
	return pid, nil, nil
}

func discoverDSHRuntimeCatalog(parent context.Context, path string) RuntimeModelCatalog {
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	output := runModelCommand(ctx, path, "--profile", dshProfile, "--list-models")
	models := parseDSHModels(output)
	return RuntimeModelCatalog{RuntimeID: "dsh", Models: models, Dynamic: len(models) > 0, Fallback: len(models) == 0}
}

func parseDSHModels(output []byte) []RuntimeModel {
	models := []RuntimeModel{}
	seen := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	for scanner.Scan() {
		var frame dshRuntimeFrame
		if json.Unmarshal(scanner.Bytes(), &frame) != nil || frame.Version != dshProtocolVersion || frame.Type != "models" {
			continue
		}
		for _, item := range frame.Models {
			id := strings.TrimSpace(item.ID)
			if item.Provider != "" && !strings.Contains(id, "/") {
				id = strings.TrimSpace(item.Provider) + "/" + id
			}
			if !safeCatalogValue(id) || seen[id] {
				continue
			}
			label := strings.TrimSpace(item.Label)
			if label == "" {
				label = id
			}
			model := RuntimeModel{ID: id, Label: label, Default: item.Default}
			if item.Thinking != nil && len(item.Thinking.SupportedLevels) > 0 {
				levels := make([]RuntimeLevel, 0, len(item.Thinking.SupportedLevels))
				levelSeen := map[string]bool{}
				for _, level := range item.Thinking.SupportedLevels {
					level.Value = strings.TrimSpace(level.Value)
					if !safeCatalogValue(level.Value) || levelSeen[level.Value] {
						continue
					}
					level.Label = strings.TrimSpace(level.Label)
					if level.Label == "" {
						level.Label = levelLabel(level.Value)
					}
					levelSeen[level.Value] = true
					levels = append(levels, level)
				}
				if len(levels) > 0 {
					defaultLevel := strings.TrimSpace(item.Thinking.DefaultLevel)
					if !levelSeen[defaultLevel] {
						defaultLevel = ""
					}
					model.Thinking = &RuntimeThinking{DefaultLevel: defaultLevel, SupportedLevels: levels}
				}
			}
			seen[id] = true
			models = append(models, model)
		}
	}
	return models
}
