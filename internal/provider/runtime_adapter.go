package provider

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var antigravityConversationPattern = regexp.MustCompile(`conversation=([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})(?:,|\s|$)`)

var runtimeOwnedArgs = map[string][]string{
	"cursor":      {"-p", "--output-format", "--yolo", "--workspace", "--model", "--resume", "--acp"},
	"copilot":     {"-p", "--prompt", "--output-format", "--allow-all", "--no-ask-user", "--model", "--resume", "--acp"},
	"opencode":    {"--format", "--dir", "--variant", "--model", "--session", "--dangerously-skip-permissions"},
	"deveco":      {"--format", "--dir", "--variant", "--model", "--session", "--dangerously-skip-permissions"},
	"openclaw":    {"--local", "--json", "--session-id", "--message", "--model", "--system-prompt", "--agent"},
	"antigravity": {"-p", "--dangerously-skip-permissions", "--model", "--conversation", "--add-dir", "--print-timeout", "--log-file"},
	"codebuddy":   {"-p", "--output-format", "--input-format", "--permission-mode", "--model", "--resume", "--effort", "--disallowedTools", "--acp"},
	"qwen":        {"-p", "--prompt", "-i", "--prompt-interactive", "-o", "--output-format", "-m", "--model", "-r", "--resume", "-c", "--continue", "--yolo", "-y", "--approval-mode", "--core-tools"},
}

func validateRuntimeCustomArgs(runtimeID string, args []string) error {
	owned := runtimeOwnedArgs[runtimeID]
	for _, arg := range args {
		for _, flag := range owned {
			if arg == flag || strings.HasPrefix(arg, flag+"=") {
				return fmt.Errorf("custom argument %q is managed by the %s adapter", arg, runtimeID)
			}
		}
	}
	return nil
}

func (p *LocalProvider) oneShotExecution(args []string) bool {
	if p.executorKind() == "codex" {
		return codexExecMode(args)
	}
	if len(p.Args) > 0 {
		return false
	}
	switch p.executorKind() {
	case "cursor-agent", "copilot", "opencode", "deveco", "openclaw", "agy", "codebuddy", "qwen":
		return true
	default:
		return false
	}
}

func (p *LocalProvider) initialInputPayload(input string, args []string) []byte {
	if p.executorKind() == "codex" && codexExecMode(args) {
		return []byte(input)
	}
	if len(p.Args) > 0 {
		return nil
	}
	switch p.executorKind() {
	case "cursor-agent", "opencode", "qwen":
		return []byte(input)
	case "codebuddy":
		payload, _ := json.Marshal(map[string]any{
			"type": "user",
			"message": map[string]any{
				"role":    "user",
				"content": []map[string]string{{"type": "text", "text": input}},
			},
		})
		return append(payload, '\n')
	default:
		return nil
	}
}

func runtimeRequiresSessionProof(kind string) bool {
	switch kind {
	case "codex", "cursor-agent", "copilot", "opencode", "deveco", "openclaw", "agy", "codebuddy", "qwen":
		return true
	default:
		return false
	}
}

func sessionIDFromAntigravityLog(data []byte) string {
	matches := antigravityConversationPattern.FindAllSubmatch(data, -1)
	if len(matches) == 0 {
		return ""
	}
	return string(matches[len(matches)-1][1])
}

func replaceEnvironmentValue(environment []string, name, value string) []string {
	prefix := strings.ToUpper(name) + "="
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if strings.HasPrefix(strings.ToUpper(item), prefix) {
			continue
		}
		result = append(result, item)
	}
	return append(result, name+"="+value)
}

func runtimeProtocolError(output []byte, kind string) error {
	switch kind {
	case "cursor-agent", "copilot", "opencode", "deveco", "openclaw", "codebuddy", "qwen":
	default:
		return nil
	}
	var failure string
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	for scanner.Scan() {
		var event map[string]any
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		typ := strings.ToLower(stringField(event, "type"))
		phase := strings.ToLower(stringField(event, "phase"))
		isError, _ := event["is_error"].(bool)
		exitCode, hasExitCode := event["exitCode"].(float64)
		terminalFailure := isError || typ == "error" || (typ == "lifecycle" && (phase == "error" || phase == "failed" || phase == "cancelled")) || (kind == "copilot" && typ == "result" && hasExitCode && exitCode != 0)
		if !terminalFailure {
			continue
		}
		failure = runtimeErrorMessage(event)
		if failure == "" {
			failure = typ
		}
	}
	if failure == "" {
		return nil
	}
	return errors.New("runtime reported failure: " + failure)
}

func runtimeTerminalOutputError(output []byte, kind string) error {
	if kind == "openclaw" || kind == "agy" {
		if len(bytes.TrimSpace(output)) == 0 {
			return errors.New("runtime emitted no result")
		}
		return nil
	}
	var terminalTypes map[string]bool
	switch kind {
	case "cursor-agent", "copilot", "codebuddy", "qwen":
		terminalTypes = map[string]bool{"result": true}
	case "opencode", "deveco":
		terminalTypes = map[string]bool{"step_finish": true}
	default:
		return nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	for scanner.Scan() {
		var event map[string]any
		if json.Unmarshal(scanner.Bytes(), &event) == nil && terminalTypes[stringField(event, "type")] {
			return nil
		}
	}
	return errors.New("runtime stream ended without a terminal result")
}

func runtimeErrorMessage(event map[string]any) string {
	for _, key := range []string{"error", "message", "detail", "result"} {
		switch value := event[key].(type) {
		case string:
			if strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		case map[string]any:
			for _, childKey := range []string{"message", "detail", "name"} {
				if child := stringField(value, childKey); child != "" {
					return child
				}
			}
		}
	}
	return ""
}

func stringField(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return strings.TrimSpace(text)
}
