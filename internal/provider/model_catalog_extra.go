package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"sort"
	"strings"
	"time"
)

func runModelCommand(parent context.Context, path string, args ...string) []byte {
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	output, _ := exec.CommandContext(ctx, path, args...).CombinedOutput()
	return output
}

func discoverCursorCatalog(ctx context.Context, path string) RuntimeModelCatalog {
	models := parseCursorModels(runModelCommand(ctx, path, "--list-models"))
	if len(models) == 0 {
		return RuntimeModelCatalog{RuntimeID: "cursor", Models: []RuntimeModel{{ID: "auto", Label: "Auto", Default: true}}, Fallback: true}
	}
	return RuntimeModelCatalog{RuntimeID: "cursor", Models: models, Dynamic: true}
}

func parseCursorModels(output []byte) []RuntimeModel {
	models := []RuntimeModel{}
	seen := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		index := strings.Index(line, " - ")
		if index <= 0 {
			continue
		}
		id := strings.TrimSpace(line[:index])
		label := strings.TrimSpace(line[index+3:])
		if !safeCatalogID(id) || seen[id] {
			continue
		}
		seen[id] = true
		isDefault := strings.Contains(strings.ToLower(label), "default")
		if suffix := strings.Index(label, "("); suffix > 0 {
			label = strings.TrimSpace(label[:suffix])
		}
		models = append(models, RuntimeModel{ID: id, Label: label, Default: isDefault})
	}
	return models
}

func discoverOpenCodeFamilyCatalog(ctx context.Context, runtimeID, path string) RuntimeModelCatalog {
	models := parseOpenCodeFamilyModels(runModelCommand(ctx, path, "models", "--verbose"))
	if len(models) == 0 {
		models = parseOpenCodeFamilyModels(runModelCommand(ctx, path, "models"))
	}
	return RuntimeModelCatalog{RuntimeID: runtimeID, Models: models, Dynamic: len(models) > 0, Fallback: len(models) == 0}
}

func parseOpenCodeFamilyModels(output []byte) []RuntimeModel {
	lines := strings.Split(string(output), "\n")
	models := []RuntimeModel{}
	indexByID := map[string]int{}
	for index := 0; index < len(lines); index++ {
		id := openCodeModelID(lines[index])
		if id == "" {
			continue
		}
		modelIndex, exists := indexByID[id]
		if !exists {
			modelIndex = len(models)
			indexByID[id] = modelIndex
			models = append(models, RuntimeModel{ID: id, Label: id})
		}
		next := index + 1
		for next < len(lines) && strings.TrimSpace(lines[next]) == "" {
			next++
		}
		if next >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[next]), "{") {
			continue
		}
		raw, resumeAt := collectModelMetadata(lines, next)
		annotateOpenCodeModel(&models[modelIndex], raw)
		index = resumeAt - 1
	}
	return models
}

func openCodeModelID(line string) string {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 || !strings.Contains(fields[0], "/") || !safeCatalogID(fields[0]) || fields[0] == strings.ToUpper(fields[0]) {
		return ""
	}
	return fields[0]
}

func collectModelMetadata(lines []string, start int) ([]byte, int) {
	var output strings.Builder
	for index := start; index < len(lines); index++ {
		if index > start && openCodeModelID(lines[index]) != "" {
			return []byte(output.String()), index
		}
		if output.Len() > 0 {
			output.WriteByte('\n')
		}
		output.WriteString(lines[index])
		if json.Valid([]byte(output.String())) {
			return []byte(output.String()), index + 1
		}
	}
	return []byte(output.String()), len(lines)
}

func annotateOpenCodeModel(model *RuntimeModel, raw []byte) {
	var metadata struct {
		Reasoning bool `json:"reasoning"`
		Variants  map[string]struct {
			Disabled        bool            `json:"disabled"`
			ReasoningEffort string          `json:"reasoningEffort"`
			Thinking        json.RawMessage `json:"thinking"`
		} `json:"variants"`
	}
	if json.Unmarshal(raw, &metadata) != nil || len(metadata.Variants) == 0 {
		return
	}
	reasoning := metadata.Reasoning
	for name, variant := range metadata.Variants {
		if _, known := runtimeThinkingOrder[name]; known || variant.ReasoningEffort != "" || len(variant.Thinking) > 0 {
			reasoning = true
		}
	}
	if !reasoning {
		return
	}
	levels := []RuntimeLevel{}
	for name, variant := range metadata.Variants {
		if name != "" && !variant.Disabled {
			levels = append(levels, RuntimeLevel{Value: name, Label: levelLabel(name)})
		}
	}
	sort.Slice(levels, func(i, j int) bool {
		left, leftKnown := runtimeThinkingOrder[levels[i].Value]
		right, rightKnown := runtimeThinkingOrder[levels[j].Value]
		if leftKnown != rightKnown {
			return leftKnown
		}
		if leftKnown {
			return left < right
		}
		return levels[i].Value < levels[j].Value
	})
	if len(levels) > 0 {
		model.Thinking = &RuntimeThinking{SupportedLevels: levels}
	}
}

var runtimeThinkingOrder = map[string]int{"none": 0, "minimal": 1, "low": 2, "medium": 3, "high": 4, "xhigh": 5, "max": 6}

func discoverAntigravityCatalog(ctx context.Context, path string) RuntimeModelCatalog {
	models := []RuntimeModel{}
	seen := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(runModelCommand(ctx, path, "models")))
	for scanner.Scan() {
		line := scanner.Text()
		id, label := strings.TrimSpace(line), strings.TrimSpace(line)
		if before, after, found := strings.Cut(line, "\t"); found {
			id = strings.TrimSpace(before)
			label = strings.TrimSpace(strings.SplitN(after, "\t", 2)[0])
		}
		if id == "" || !safeCatalogValue(id) || seen[id] {
			continue
		}
		if label == "" {
			label = id
		}
		seen[id] = true
		models = append(models, RuntimeModel{ID: id, Label: label})
	}
	return RuntimeModelCatalog{RuntimeID: "antigravity", Models: models, Dynamic: len(models) > 0, Fallback: len(models) == 0}
}

func discoverOpenClawCatalog(ctx context.Context, path string) RuntimeModelCatalog {
	for _, args := range [][]string{{"agents", "list", "--json"}, {"agents", "list", "--output", "json"}, {"agents", "list", "-o", "json"}} {
		if models, ok := parseOpenClawModels(runModelCommand(ctx, path, args...)); ok {
			return RuntimeModelCatalog{RuntimeID: "openclaw", Models: models, Dynamic: true}
		}
	}
	return RuntimeModelCatalog{RuntimeID: "openclaw", Models: []RuntimeModel{}, Fallback: true}
}

func parseOpenClawModels(output []byte) ([]RuntimeModel, bool) {
	type entry struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Model string `json:"model"`
	}
	var entries []entry
	if err := json.Unmarshal(bytes.TrimSpace(output), &entries); err != nil {
		var wrapped struct {
			Agents []entry `json:"agents"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(output), &wrapped); err != nil || wrapped.Agents == nil {
			return nil, false
		}
		entries = wrapped.Agents
	}
	models := []RuntimeModel{}
	seen := map[string]bool{}
	for _, item := range entries {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = strings.TrimSpace(item.Name)
		}
		if !safeCatalogID(id) || seen[id] {
			continue
		}
		label := strings.TrimSpace(item.Name)
		if label == "" {
			label = id
		}
		if item.Model != "" {
			label += " (" + item.Model + ")"
		}
		seen[id] = true
		models = append(models, RuntimeModel{ID: id, Label: label})
	}
	return models, true
}

func fallbackCopilotCatalog() RuntimeModelCatalog {
	return RuntimeModelCatalog{RuntimeID: "copilot", Fallback: true, Models: []RuntimeModel{
		{ID: "gpt-5.5", Label: "GPT-5.5"},
		{ID: "gpt-5.4", Label: "GPT-5.4"},
		{ID: "gpt-5.4-mini", Label: "GPT-5.4 mini"},
		{ID: "gpt-5.3-codex", Label: "GPT-5.3 Codex"},
		{ID: "gpt-5.2", Label: "GPT-5.2"},
		{ID: "gpt-4.1", Label: "GPT-4.1"},
		{ID: "claude-opus-4.7", Label: "Claude Opus 4.7"},
		{ID: "claude-sonnet-4.6", Label: "Claude Sonnet 4.6"},
		{ID: "claude-haiku-4.5", Label: "Claude Haiku 4.5"},
	}}
}

func fallbackCodeBuddyCatalog() RuntimeModelCatalog {
	levels := []RuntimeLevel{{Value: "minimal", Label: "Minimal"}, {Value: "low", Label: "Low"}, {Value: "medium", Label: "Medium"}, {Value: "high", Label: "High"}, {Value: "xhigh", Label: "Extra high"}, {Value: "max", Label: "Max"}}
	models := []RuntimeModel{
		{ID: "claude-sonnet-4.6", Label: "Claude Sonnet 4.6", Default: true},
		{ID: "claude-opus-4.7", Label: "Claude Opus 4.7"},
		{ID: "gemini-3.1-pro", Label: "Gemini 3.1 Pro"},
		{ID: "gpt-5.5", Label: "GPT-5.5"},
		{ID: "deepseek-v3-2-volc-ioa", Label: "DeepSeek V3.2"},
	}
	for index := range models {
		models[index].Thinking = &RuntimeThinking{SupportedLevels: levels}
	}
	return RuntimeModelCatalog{RuntimeID: "codebuddy", Models: models, Fallback: true}
}

func safeCatalogID(value string) bool {
	if value == "" || len(value) > 256 || strings.HasSuffix(value, ":") {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("-._/", r) {
			continue
		}
		return false
	}
	return true
}

func safeCatalogValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
