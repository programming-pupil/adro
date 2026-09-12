package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// RuntimeDescriptor is the single registry entry shared by startup discovery,
// setup, Agent configuration, and execution routing.
type RuntimeDescriptor struct {
	ID                        string `json:"id"`
	Name                      string `json:"name"`
	Command                   string `json:"command"`
	ProtocolFamily            string `json:"protocol_family"`
	AdapterAvailable          bool   `json:"adapter_available"`
	ModelSelectionUnsupported bool   `json:"model_selection_unsupported,omitempty"`
}

type DiscoveredRuntime struct {
	RuntimeDescriptor
	Installed         bool   `json:"installed"`
	ExecutablePath    string `json:"executable_path,omitempty"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

// RuntimeRegistry is the source of truth shared by discovery and execution.
// AdapterAvailable remains false until the corresponding protocol has an
// executable, tested launch contract.
var RuntimeRegistry = []RuntimeDescriptor{
	{ID: "claude", Name: "Claude Code", Command: "claude", ProtocolFamily: "claude", AdapterAvailable: true},
	{ID: "codex", Name: "OpenAI Codex", Command: "codex", ProtocolFamily: "codex", AdapterAvailable: true},
	{ID: "cursor", Name: "Cursor Agent", Command: "cursor-agent", ProtocolFamily: "cursor", AdapterAvailable: true},
	{ID: "copilot", Name: "GitHub Copilot CLI", Command: "copilot", ProtocolFamily: "copilot", AdapterAvailable: true},
	{ID: "opencode", Name: "OpenCode", Command: "opencode", ProtocolFamily: "opencode", AdapterAvailable: true},
	{ID: "openclaw", Name: "OpenClaw", Command: "openclaw", ProtocolFamily: "openclaw", AdapterAvailable: true},
	{ID: "hermes", Name: "Hermes", Command: "hermes", ProtocolFamily: "hermes", AdapterAvailable: true},
	{ID: "pi", Name: "Pi", Command: "pi", ProtocolFamily: "pi", AdapterAvailable: true},
	{ID: "antigravity", Name: "Antigravity", Command: "agy", ProtocolFamily: "antigravity", AdapterAvailable: true},
	{ID: "codebuddy", Name: "CodeBuddy", Command: "codebuddy", ProtocolFamily: "codebuddy", AdapterAvailable: true},
	{ID: "deveco", Name: "DevEco Code", Command: "deveco", ProtocolFamily: "deveco", AdapterAvailable: true},
	{ID: "grok", Name: "Grok", Command: "grok", ProtocolFamily: "grok", AdapterAvailable: true},
	{ID: "kimi", Name: "Kimi", Command: "kimi", ProtocolFamily: "kimi", AdapterAvailable: true},
	{ID: "kiro", Name: "Kiro CLI", Command: "kiro-cli", ProtocolFamily: "kiro", AdapterAvailable: true},
	{ID: "qoder", Name: "Qoder CLI", Command: "qodercli", ProtocolFamily: "qoder", AdapterAvailable: true},
	{ID: "qoderclicn", Name: "Qoder CN", Command: "qoderclicn", ProtocolFamily: "qoderclicn", AdapterAvailable: true},
	{ID: "qwen", Name: "Qwen Code", Command: "qwen", ProtocolFamily: "qwen", AdapterAvailable: true},
	{ID: "qwenpaw", Name: "QwenPaw", Command: "qwenpaw", ProtocolFamily: "qwenpaw", AdapterAvailable: true, ModelSelectionUnsupported: true},
	{ID: "reasonix", Name: "Reasonix", Command: "reasonix", ProtocolFamily: "reasonix", AdapterAvailable: true},
	{ID: "traecli", Name: "Trae CLI", Command: "traecli", ProtocolFamily: "traecli", AdapterAvailable: true},
	{ID: "dsh", Name: "DeepSeek Harness", Command: "dsh", ProtocolFamily: "dsh", AdapterAvailable: true},
	{ID: "omp", Name: "Oh-My-Pi", Command: "omp", ProtocolFamily: "pi", AdapterAvailable: true},
	{ID: "mcode", Name: "MiniMax Code", Command: "mcode", ProtocolFamily: "mcode", AdapterAvailable: true, ModelSelectionUnsupported: true},
	{ID: "dim", Name: "Dim", Command: "dim", ProtocolFamily: "dim", AdapterAvailable: true},
	{ID: "zeroclaw", Name: "ZeroClaw", Command: "zeroclaw", ProtocolFamily: "zeroclaw", AdapterAvailable: true, ModelSelectionUnsupported: true},
}

func DiscoverLocalRuntimes() []DiscoveredRuntime {
	items := make([]DiscoveredRuntime, 0, len(RuntimeRegistry))
	if explicit := os.Getenv("ADRO_EXECUTOR"); explicit != "" {
		if path, err := exec.LookPath(explicit); err == nil {
			items = append(items, DiscoveredRuntime{RuntimeDescriptor: RuntimeDescriptor{ID: "local", Name: "Configured local executor", Command: explicit, ProtocolFamily: "local", AdapterAvailable: true}, Installed: true, ExecutablePath: path})
		}
	}
	unresolved := map[string][]int{}
	for _, descriptor := range RuntimeRegistry {
		item := DiscoveredRuntime{RuntimeDescriptor: descriptor}
		configuredPath, pinned := os.LookupEnv(runtimePathEnvironment(descriptor.ID))
		pinned = pinned && strings.TrimSpace(configuredPath) != ""
		command := descriptor.Command
		if pinned {
			command = strings.TrimSpace(configuredPath)
		}
		if path, err := executablePath(command); err == nil {
			item.Installed = true
			item.ExecutablePath = path
		} else if pinned {
			item.UnavailableReason = "configured executable is unavailable"
		} else {
			unresolved[descriptor.Command] = append(unresolved[descriptor.Command], len(items))
		}
		items = append(items, item)
	}
	if len(unresolved) > 0 {
		for command, path := range cachedLoginShellExecutables() {
			for _, index := range unresolved[command] {
				items[index].Installed = true
				items[index].ExecutablePath = path
			}
		}
	}
	for index := range items {
		if items[index].ID == "codex" && !items[index].Installed && items[index].UnavailableReason == "" {
			if path := codexDesktopExecutable(); path != "" {
				items[index].Installed = true
				items[index].ExecutablePath = path
			}
		}
		if items[index].ID == "dsh" && items[index].Installed && !dshProfileAvailable(items[index].ExecutablePath) {
			items[index].Installed = false
			items[index].ExecutablePath = ""
			items[index].UnavailableReason = "required execution profile is unavailable"
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Installed != items[j].Installed {
			return items[i].Installed
		}
		return items[i].Name < items[j].Name
	})
	return items
}

func runtimePathEnvironment(runtimeID string) string {
	return "ADRO_" + strings.ToUpper(strings.ReplaceAll(runtimeID, "-", "_")) + "_PATH"
}

func executablePath(command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", exec.ErrNotFound
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return path, nil
}

var loginShellExecutableCache struct {
	sync.Mutex
	key       string
	items     map[string]string
	expiresAt time.Time
}

func cachedLoginShellExecutables() map[string]string {
	key := strings.Join([]string{os.Getenv("PATH"), os.Getenv("SHELL"), os.Getenv("HOME")}, "\x00")
	loginShellExecutableCache.Lock()
	defer loginShellExecutableCache.Unlock()
	if loginShellExecutableCache.key == key && time.Now().Before(loginShellExecutableCache.expiresAt) {
		return loginShellExecutableCache.items
	}
	items := resolveLoginShellExecutables()
	loginShellExecutableCache.key = key
	loginShellExecutableCache.items = items
	loginShellExecutableCache.expiresAt = time.Now().Add(10 * time.Minute)
	return items
}

func resolveLoginShellExecutables() map[string]string {
	items := map[string]string{}
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		return items
	}
	if _, supported := map[string]bool{"bash": true, "dash": true, "ksh": true, "sh": true, "zsh": true}[filepath.Base(shell)]; !supported {
		return items
	}
	commands := make([]string, 0, len(RuntimeRegistry))
	for _, descriptor := range RuntimeRegistry {
		commands = append(commands, descriptor.Command)
	}
	sort.Strings(commands)
	var script strings.Builder
	for _, command := range commands {
		if !safeShellCommandName(command) {
			continue
		}
		fmtLine := "if p=$(command -v " + command + " 2>/dev/null); then printf '" + command + "\\t%s\\n' \"$p\"; fi\n"
		script.WriteString(fmtLine)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-ilc", script.String())
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil {
		return items
	}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		command, candidate, ok := strings.Cut(scanner.Text(), "\t")
		if !ok || !safeShellCommandName(command) || !filepath.IsAbs(candidate) {
			continue
		}
		if path, err := executablePath(candidate); err == nil {
			items[command] = path
		}
	}
	return items
}

func safeShellCommandName(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("._-", character) {
			continue
		}
		return false
	}
	return true
}

func codexDesktopExecutable() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	// An explicitly empty override is useful for deterministic local tests and
	// for operators who want PATH/login-shell discovery only.
	if configured, ok := os.LookupEnv("ADRO_CODEX_PATH"); ok && strings.TrimSpace(configured) == "" {
		return ""
	}
	paths := []string{
		"/Applications/ChatGPT.app/Contents/Resources/codex",
		"/Applications/Codex.app/Contents/Resources/codex",
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths,
			filepath.Join(home, "Applications", "ChatGPT.app", "Contents", "Resources", "codex"),
			filepath.Join(home, "Applications", "Codex.app", "Contents", "Resources", "codex"),
		)
	}
	for _, candidate := range paths {
		if path, err := executablePath(candidate); err == nil {
			return path
		}
	}
	return ""
}

func dshProfileAvailable(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--profile", dshProfile, "--probe")
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(output), "\n") {
		var frame struct {
			Version         int    `json:"v"`
			Type            string `json:"type"`
			Runtime         string `json:"runtime"`
			ProtocolVersion int    `json:"protocol_version"`
		}
		if json.Unmarshal([]byte(line), &frame) == nil && frame.Version == 1 && frame.Type == "probe" && frame.Runtime == "dsh" && frame.ProtocolVersion == dshProtocolVersion {
			return true
		}
	}
	return false
}
