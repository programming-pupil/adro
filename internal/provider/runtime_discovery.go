package provider

import (
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

// RuntimeRegistry mirrors the runtime identities supported by the reference
// implementation. AdapterAvailable remains false until ADRO can execute that
// protocol; discovery must never imply execution support.
var RuntimeRegistry = []RuntimeDescriptor{
	{ID: "claude", Name: "Claude Code", Command: "claude", ProtocolFamily: "claude", AdapterAvailable: true},
	{ID: "codex", Name: "OpenAI Codex", Command: "codex", ProtocolFamily: "codex", AdapterAvailable: true},
	{ID: "cursor", Name: "Cursor Agent", Command: "cursor-agent", ProtocolFamily: "cursor"},
	{ID: "copilot", Name: "GitHub Copilot CLI", Command: "copilot", ProtocolFamily: "copilot"},
	{ID: "opencode", Name: "OpenCode", Command: "opencode", ProtocolFamily: "opencode"},
	{ID: "openclaw", Name: "OpenClaw", Command: "openclaw", ProtocolFamily: "openclaw"},
	{ID: "hermes", Name: "Hermes", Command: "hermes", ProtocolFamily: "hermes"},
	{ID: "pi", Name: "Pi", Command: "pi", ProtocolFamily: "pi"},
	{ID: "antigravity", Name: "Antigravity", Command: "agy", ProtocolFamily: "antigravity"},
	{ID: "codebuddy", Name: "CodeBuddy", Command: "codebuddy", ProtocolFamily: "codebuddy"},
	{ID: "deveco", Name: "DevEco Code", Command: "deveco", ProtocolFamily: "deveco"},
	{ID: "grok", Name: "Grok", Command: "grok", ProtocolFamily: "grok"},
	{ID: "kimi", Name: "Kimi", Command: "kimi", ProtocolFamily: "kimi"},
	{ID: "kiro", Name: "Kiro CLI", Command: "kiro-cli", ProtocolFamily: "kiro"},
	{ID: "qoder", Name: "Qoder CLI", Command: "qodercli", ProtocolFamily: "qoder"},
	{ID: "qoderclicn", Name: "Qoder CN", Command: "qoderclicn", ProtocolFamily: "qoderclicn"},
	{ID: "qwen", Name: "Qwen Code", Command: "qwen", ProtocolFamily: "qwen"},
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
