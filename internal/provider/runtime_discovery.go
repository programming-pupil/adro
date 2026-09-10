package provider

import (
	"os"
	"os/exec"
	"sort"
)

// RuntimeDescriptor is the single registry entry shared by startup discovery,
// setup, Agent configuration, and execution routing.
type RuntimeDescriptor struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Command          string `json:"command"`
	ProtocolFamily   string `json:"protocol_family"`
	AdapterAvailable bool   `json:"adapter_available"`
}

type DiscoveredRuntime struct {
	RuntimeDescriptor
	Installed      bool   `json:"installed"`
	ExecutablePath string `json:"executable_path,omitempty"`
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
	{ID: "hermes", Name: "Hermes", Command: "hermes", ProtocolFamily: "hermes"},
	{ID: "pi", Name: "Pi", Command: "pi", ProtocolFamily: "pi"},
	{ID: "antigravity", Name: "Antigravity", Command: "agy", ProtocolFamily: "antigravity", AdapterAvailable: true},
	{ID: "codebuddy", Name: "CodeBuddy", Command: "codebuddy", ProtocolFamily: "codebuddy", AdapterAvailable: true},
	{ID: "deveco", Name: "DevEco Code", Command: "deveco", ProtocolFamily: "deveco", AdapterAvailable: true},
	{ID: "grok", Name: "Grok", Command: "grok", ProtocolFamily: "grok"},
	{ID: "kimi", Name: "Kimi", Command: "kimi", ProtocolFamily: "kimi"},
	{ID: "kiro", Name: "Kiro CLI", Command: "kiro-cli", ProtocolFamily: "kiro"},
	{ID: "qoder", Name: "Qoder CLI", Command: "qodercli", ProtocolFamily: "qoder"},
	{ID: "qoderclicn", Name: "Qoder CN", Command: "qoderclicn", ProtocolFamily: "qoderclicn"},
	{ID: "qwen", Name: "Qwen Code", Command: "qwen", ProtocolFamily: "qwen", AdapterAvailable: true},
	{ID: "qwenpaw", Name: "QwenPaw", Command: "qwenpaw", ProtocolFamily: "qwenpaw"},
	{ID: "reasonix", Name: "Reasonix", Command: "reasonix", ProtocolFamily: "reasonix"},
	{ID: "traecli", Name: "Trae CLI", Command: "traecli", ProtocolFamily: "traecli"},
	{ID: "dsh", Name: "DeepSeek Harness", Command: "dsh", ProtocolFamily: "dsh"},
	{ID: "omp", Name: "Oh-My-Pi", Command: "omp", ProtocolFamily: "pi"},
	{ID: "mcode", Name: "MiniMax Code", Command: "mcode", ProtocolFamily: "mcode"},
	{ID: "dim", Name: "Dim", Command: "dim", ProtocolFamily: "dim"},
	{ID: "zeroclaw", Name: "ZeroClaw", Command: "zeroclaw", ProtocolFamily: "zeroclaw"},
}

func DiscoverLocalRuntimes() []DiscoveredRuntime {
	items := make([]DiscoveredRuntime, 0, len(RuntimeRegistry))
	if explicit := os.Getenv("ADRO_EXECUTOR"); explicit != "" {
		if path, err := exec.LookPath(explicit); err == nil {
			items = append(items, DiscoveredRuntime{RuntimeDescriptor: RuntimeDescriptor{ID: "local", Name: "Configured local executor", Command: explicit, ProtocolFamily: "local", AdapterAvailable: true}, Installed: true, ExecutablePath: path})
		}
	}
	for _, descriptor := range RuntimeRegistry {
		item := DiscoveredRuntime{RuntimeDescriptor: descriptor}
		if path, err := exec.LookPath(descriptor.Command); err == nil {
			item.Installed = true
			item.ExecutablePath = path
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Installed != items[j].Installed {
			return items[i].Installed
		}
		return items[i].Name < items[j].Name
	})
	return items
}
