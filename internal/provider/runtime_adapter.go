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
	"hermes":      {"acp"},
	"kimi":        {"acp"},
	"kiro":        {"acp", "-a", "--trust-all-tools", "--trust-tools"},
	"qoder":       {"acp", "--acp", "--yolo"},
	"qoderclicn":  {"acp", "--acp", "--yolo"},
	"traecli":     {"acp", "serve", "-y", "--yolo", "-p", "--print", "--output-format", "--permission-mode"},
	"grok":        {"agent", "stdio", "headless", "serve", "leader", "--always-approve", "--yolo", "--no-auto-update", "--no-alt-screen", "-p", "--print", "--single", "--output-format", "--permission-mode", "-m", "--model", "--reasoning-effort", "--effort", "-r", "--resume", "-c", "--continue", "-s", "--session-id", "--cwd", "-w", "--worktree", "--ref", "--fork-session"},
	"qwenpaw":     {"acp", "--workspace"},
	"reasonix":    {"acp", "--model", "--profile", "--planner", "--sandbox-network", "--sandbox-bash", "--workspace-only"},
	"mcode":       {"acp", "login", "--region", "-h", "--help"},
	"dim":         {"acp", "--auth-setup", "--remote", "-h", "--help"},
	"zeroclaw":    {"acp", "login", "auth", "--login", "--auth", "-h", "--help"},
	"pi":          {"-p", "--print", "--mode", "--session", "--thinking", "--model"},
	"omp":         {"-p", "--print", "--mode", "--session", "--thinking", "--model"},
	"dsh":         {"--profile", "--stdio", "--list-models"},
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
	case "cursor-agent", "copilot", "opencode", "deveco", "openclaw", "agy", "codebuddy", "qwen", "pi", "omp", "dsh":
		return true
	default:
		return isACPRuntime(p.executorKind())
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
	case "cursor-agent", "opencode", "qwen", "pi", "omp":
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
	case "codex", "cursor-agent", "copilot", "opencode", "deveco", "openclaw", "agy", "codebuddy", "qwen", "pi", "omp", "dsh":
		return true
	default:
		return isACPRuntime(kind)
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
	if kind == "pi" || kind == "omp" {
		return piRuntimeOutputError(output, kind)
	}
	switch kind {
	case "cursor-agent", "copilot", "opencode", "deveco", "openclaw", "codebuddy", "qwen":
	default:
		if !isACPRuntime(kind) {
			return nil
		}
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
	case "pi", "omp":
		terminalTypes = map[string]bool{"turn_end": true}
	case "dsh":
		terminalTypes = map[string]bool{"result": true}
	default:
		if isACPRuntime(kind) {
			terminalTypes = map[string]bool{"result": true}
		} else {
			return nil
		}
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

func piRuntimeOutputError(output []byte, kind string) error {
	terminal := false
	lastTurnError := ""
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	for scanner.Scan() {
		var event map[string]any
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		typ := stringField(event, "type")
		switch typ {
		case "turn_start":
			lastTurnError = ""
		case "turn_end":
			terminal = true
			if message, ok := event["message"].(map[string]any); ok && strings.EqualFold(stringField(message, "stopReason"), "error") {
				lastTurnError = stringField(message, "errorMessage")
				if lastTurnError == "" {
					lastTurnError = kind + " ended the turn with an error"
				}
			}
		case "error":
			message := firstString(event, "message", "error")
			if message == "" {
				message = kind + " reported an error"
			}
			return errors.New(message)
		case "auto_retry_end":
			if success, _ := event["success"].(bool); !success {
				message := stringField(event, "finalError")
				if message == "" {
					message = kind + " exhausted automatic retries"
				}
				return errors.New(message)
			}
		}
	}
	if lastTurnError != "" {
		return errors.New(lastTurnError)
	}
	if !terminal {
		return errors.New(kind + " stream ended without a terminal turn")
	}
	return nil
}

var piCustomArgValueModes = map[string]bool{
	"--provider": true, "--api-key": true, "--system-prompt": true, "--append-system-prompt": true,
	"--name": true, "-n": true, "--session-id": true, "--fork": true, "--session-dir": true,
	"--models": true, "--tools": true, "-t": true, "--exclude-tools": true, "-xt": true,
	"--export": true, "--extension": true, "-e": true, "--skill": true, "--prompt-template": true,
	"--theme": true,
}

func piForwardedCustomArgs(args []string) []string {
	result := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if strings.HasPrefix(arg, "@") || !strings.HasPrefix(arg, "-") {
			continue
		}
		result = append(result, arg)
		if strings.Contains(arg, "=") {
			continue
		}
		if piCustomArgValueModes[arg] && index+1 < len(args) {
			result = append(result, args[index+1])
			index++
		} else if strings.HasPrefix(arg, "--") && index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") && !strings.HasPrefix(args[index+1], "@") {
			result = append(result, args[index+1])
			index++
		}
	}
	return result
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
