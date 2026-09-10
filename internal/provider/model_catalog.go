package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type RuntimeModelCatalog struct {
	RuntimeID string         `json:"runtime_id"`
	Models    []RuntimeModel `json:"models"`
	Dynamic   bool           `json:"dynamic"`
	Fallback  bool           `json:"fallback"`
}
type RuntimeModel struct {
	ID           string               `json:"id"`
	Label        string               `json:"label"`
	Default      bool                 `json:"default,omitempty"`
	Thinking     *RuntimeThinking     `json:"thinking,omitempty"`
	ServiceTiers []RuntimeServiceTier `json:"service_tiers,omitempty"`
}
type RuntimeThinking struct {
	DefaultLevel    string         `json:"default_level,omitempty"`
	SupportedLevels []RuntimeLevel `json:"supported_levels"`
}
type RuntimeLevel struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}
type RuntimeServiceTier struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

var claudeEffortPattern = regexp.MustCompile(`--effort\s*(?:<[^>]+>)?[^\n(]*\(([^)]+)\)`)

func DiscoverRuntimeModels(ctx context.Context, runtimeID string) (RuntimeModelCatalog, error) {
	var runtime *DiscoveredRuntime
	for _, item := range DiscoverLocalRuntimes() {
		if item.ID == strings.TrimSpace(runtimeID) {
			copy := item
			runtime = &copy
			break
		}
	}
	if runtime == nil {
		return RuntimeModelCatalog{}, errors.New("unknown runtime")
	}
	if !runtime.Installed {
		return RuntimeModelCatalog{}, errors.New("runtime is not installed")
	}
	switch runtime.ID {
	case "codex":
		return discoverCodexCatalog(ctx, runtime.ExecutablePath), nil
	case "claude":
		return discoverClaudeCatalog(ctx, runtime.ExecutablePath), nil
	case "cursor":
		return discoverCursorCatalog(ctx, runtime.ExecutablePath), nil
	case "copilot":
		return fallbackCopilotCatalog(), nil
	case "opencode", "deveco":
		return discoverOpenCodeFamilyCatalog(ctx, runtime.ID, runtime.ExecutablePath), nil
	case "openclaw":
		return discoverOpenClawCatalog(ctx, runtime.ExecutablePath), nil
	case "antigravity":
		return discoverAntigravityCatalog(ctx, runtime.ExecutablePath), nil
	case "codebuddy":
		return fallbackCodeBuddyCatalog(), nil
	case "qwen":
		return RuntimeModelCatalog{RuntimeID: runtime.ID, Models: []RuntimeModel{}, Fallback: true}, nil
	case "pi", "omp":
		return discoverPiFamilyCatalog(ctx, runtime.ID, runtime.ExecutablePath), nil
	case "dsh":
		return discoverDSHRuntimeCatalog(ctx, runtime.ExecutablePath), nil
	case "hermes", "kimi", "kiro", "qoder", "qoderclicn", "traecli", "grok", "reasonix", "dim":
		return discoverACPRuntimeCatalog(ctx, runtime.ID, runtime.ExecutablePath), nil
	case "qwenpaw", "mcode", "zeroclaw":
		return RuntimeModelCatalog{RuntimeID: runtime.ID, Models: []RuntimeModel{}}, nil
	default:
		return RuntimeModelCatalog{RuntimeID: runtime.ID, Models: []RuntimeModel{}}, nil
	}
}

func discoverCodexCatalog(parent context.Context, path string) RuntimeModelCatalog {
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "debug", "models", "--bundled").Output()
	if err == nil {
		var payload struct {
			Models []struct {
				Slug             string                                 `json:"slug"`
				DisplayName      string                                 `json:"display_name"`
				DefaultReasoning string                                 `json:"default_reasoning_level"`
				Priority         int                                    `json:"priority"`
				Reasoning        []struct{ Effort, Description string } `json:"supported_reasoning_levels"`
				Tiers            []RuntimeServiceTier                   `json:"service_tiers"`
			} `json:"models"`
		}
		if json.Unmarshal(output, &payload) == nil && len(payload.Models) > 0 {
			models := make([]RuntimeModel, 0, len(payload.Models))
			for _, item := range payload.Models {
				if strings.TrimSpace(item.Slug) == "" {
					continue
				}
				levels := make([]RuntimeLevel, 0, len(item.Reasoning))
				for _, level := range item.Reasoning {
					levels = append(levels, RuntimeLevel{Value: level.Effort, Label: levelLabel(level.Effort), Description: level.Description})
				}
				model := RuntimeModel{ID: item.Slug, Label: item.DisplayName, Default: item.Priority == 1, ServiceTiers: item.Tiers}
				if len(levels) > 0 {
					model.Thinking = &RuntimeThinking{DefaultLevel: item.DefaultReasoning, SupportedLevels: levels}
				}
				models = append(models, model)
			}
			if len(models) > 0 {
				return RuntimeModelCatalog{RuntimeID: "codex", Models: models, Dynamic: true}
			}
		}
	}
	levels := []RuntimeLevel{{Value: "low", Label: "Low"}, {Value: "medium", Label: "Medium"}, {Value: "high", Label: "High"}, {Value: "xhigh", Label: "Extra high"}}
	models := []RuntimeModel{{ID: "gpt-5.6-sol", Label: "GPT-5.6 Sol", Default: true}, {ID: "gpt-5.6-terra", Label: "GPT-5.6 Terra"}, {ID: "gpt-5.6-luna", Label: "GPT-5.6 Luna"}, {ID: "gpt-5.5", Label: "GPT-5.5"}, {ID: "gpt-5.4", Label: "GPT-5.4"}, {ID: "gpt-5.4-mini", Label: "GPT-5.4 Mini"}, {ID: "gpt-5.3-codex", Label: "GPT-5.3 Codex"}, {ID: "gpt-5.2", Label: "GPT-5.2"}}
	for i := range models {
		models[i].Thinking = &RuntimeThinking{DefaultLevel: "medium", SupportedLevels: levels}
	}
	return RuntimeModelCatalog{RuntimeID: "codex", Models: models, Fallback: true}
}

func discoverClaudeCatalog(parent context.Context, path string) RuntimeModelCatalog {
	levels := []string{"low", "medium", "high"}
	ctx, cancel := context.WithTimeout(parent, 4*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, path, "--help").CombinedOutput(); err == nil {
		if match := claudeEffortPattern.FindStringSubmatch(string(output)); len(match) == 2 {
			parsed := []string{}
			for _, value := range strings.Split(match[1], ",") {
				if value = strings.TrimSpace(value); value != "" {
					parsed = append(parsed, value)
				}
			}
			if len(parsed) > 0 {
				levels = parsed
			}
		}
	}
	models := []RuntimeModel{{ID: "claude-sonnet-5", Label: "Claude Sonnet 5"}, {ID: "claude-sonnet-4-6", Label: "Claude Sonnet 4.6", Default: true}, {ID: "claude-fable-5", Label: "Claude Fable 5"}, {ID: "claude-opus-5", Label: "Claude Opus 5"}, {ID: "claude-opus-4-8", Label: "Claude Opus 4.8"}, {ID: "claude-opus-4-7", Label: "Claude Opus 4.7"}, {ID: "claude-haiku-4-5-20251001", Label: "Claude Haiku 4.5"}, {ID: "claude-opus-4-6", Label: "Claude Opus 4.6"}, {ID: "claude-sonnet-4-5", Label: "Claude Sonnet 4.5"}}
	for i := range models {
		allowed := make([]RuntimeLevel, 0, len(levels))
		for _, level := range levels {
			if claudeModelSupports(models[i].ID, level) {
				allowed = append(allowed, RuntimeLevel{Value: level, Label: levelLabel(level)})
			}
		}
		models[i].Thinking = &RuntimeThinking{DefaultLevel: "medium", SupportedLevels: allowed}
	}
	// Claude exposes effort levels through --help but does not expose an
	// authoritative model inventory. Keep these entries as editable hints.
	return RuntimeModelCatalog{RuntimeID: "claude", Models: models, Dynamic: true, Fallback: true}
}

func claudeModelSupports(model, level string) bool {
	if level == "xhigh" && !strings.Contains(model, "opus") {
		return false
	}
	if level == "max" && strings.Contains(model, "haiku") {
		return false
	}
	return true
}
func levelLabel(level string) string {
	switch level {
	case "xhigh":
		return "Extra high"
	case "ultra":
		return "Ultra"
	case "max":
		return "Max"
	}
	if level == "" {
		return ""
	}
	return strings.ToUpper(level[:1]) + level[1:]
}
