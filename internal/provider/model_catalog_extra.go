package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
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

func discoverPiFamilyCatalog(ctx context.Context, runtimeID, path string) RuntimeModelCatalog {
	var output []byte
	if runtimeID == "omp" {
		output = runModelCommand(ctx, path, "models", "--json")
		models := parseOMPModels(output)
		return RuntimeModelCatalog{RuntimeID: runtimeID, Models: models, Dynamic: len(models) > 0, Fallback: len(models) == 0}
	}
	output = runModelCommand(ctx, path, "--list-models")
	models := parsePiTableModels(output)
	return RuntimeModelCatalog{RuntimeID: runtimeID, Models: models, Dynamic: len(models) > 0, Fallback: len(models) == 0}
}

func parsePiTableModels(output []byte) []RuntimeModel {
	models := []RuntimeModel{}
	seen := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lower := strings.ToLower(line)
		if line == "" || strings.HasPrefix(lower, "provider") || strings.HasPrefix(lower, "warning:") || strings.HasPrefix(lower, "error:") || strings.HasPrefix(lower, "info:") || strings.Contains(lower, "no models match") || strings.Contains(lower, "unknown flag") || strings.Contains(lower, "unknown command") || strings.Contains(lower, "usage:") || strings.Contains(lower, "--help") || strings.Contains(line, "`") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		id := ""
		if strings.ContainsAny(fields[0], ":/") {
			id = strings.Replace(fields[0], ":", "/", 1)
		} else if len(fields) >= 2 {
			id = fields[0] + "/" + fields[1]
		}
		if slash := strings.Index(id, "/"); slash <= 0 || slash == len(id)-1 || !safeCatalogValue(id) || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, RuntimeModel{ID: id, Label: id})
	}
	return models
}

func parseOMPModels(output []byte) []RuntimeModel {
	var result struct {
		Models []struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
			Selector string `json:"selector"`
			Name     string `json:"name"`
		} `json:"models"`
	}
	if json.Unmarshal(bytes.TrimSpace(output), &result) != nil {
		return nil
	}
	models := []RuntimeModel{}
	seen := map[string]bool{}
	for _, item := range result.Models {
		id := strings.TrimSpace(item.Selector)
		if id == "" && strings.TrimSpace(item.Provider) != "" && strings.TrimSpace(item.ID) != "" {
			id = strings.TrimSpace(item.Provider) + "/" + strings.TrimSpace(item.ID)
		}
		if id == "" {
			id = strings.TrimSpace(item.ID)
		}
		if !safeCatalogValue(id) || seen[id] {
			continue
		}
		label := strings.TrimSpace(item.Name)
		if label == "" {
			label = id
		}
		seen[id] = true
		models = append(models, RuntimeModel{ID: id, Label: label})
	}
	return models
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

func discoverACPRuntimeCatalog(parent context.Context, runtimeID, path string) RuntimeModelCatalog {
	fallback := RuntimeModelCatalog{RuntimeID: runtimeID, Models: []RuntimeModel{}, Fallback: true}
	kind := runtimeID
	switch runtimeID {
	case "kiro":
		kind = "kiro-cli"
	case "qoder":
		kind = "qodercli"
	}
	spec, ok := acpRuntimeSpecs[kind]
	if !ok || !spec.SetModel {
		fallback.Fallback = false
		return fallback
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	workDir, err := os.MkdirTemp("", "adro-acp-models-")
	if err != nil {
		return fallback
	}
	defer os.RemoveAll(workDir)
	process, err := startACPRuntimeProcess(ctx, path, acpRuntimeLaunchArgs(kind, "", nil), workDir, kind, nil)
	if err != nil {
		return fallback
	}
	defer process.close()
	initialize, err := process.client.request(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientInfo":         map[string]any{"name": "adro-model-discovery", "version": "1"},
		"clientCapabilities": map[string]any{},
	})
	if err != nil {
		return fallback
	}
	if spec.Authenticate {
		method, err := selectACPAuthMethod(initialize, strings.TrimSpace(os.Getenv("XAI_API_KEY")) != "")
		if err != nil {
			return fallbackGrokCatalog(runtimeID)
		}
		if _, err := process.client.request(ctx, "authenticate", map[string]any{"methodId": method, "_meta": map[string]any{"headless": true}}); err != nil {
			return fallbackGrokCatalog(runtimeID)
		}
	}
	session, err := process.client.request(ctx, "session/new", map[string]any{"cwd": workDir, "mcpServers": []any{}})
	if err != nil {
		return fallbackGrokCatalog(runtimeID)
	}
	models := parseACPRuntimeModels(session)
	if len(models) == 0 {
		return fallbackGrokCatalog(runtimeID)
	}
	return RuntimeModelCatalog{RuntimeID: runtimeID, Models: models, Dynamic: true}
}

func fallbackGrokCatalog(runtimeID string) RuntimeModelCatalog {
	if runtimeID != "grok" {
		return RuntimeModelCatalog{RuntimeID: runtimeID, Models: []RuntimeModel{}, Fallback: true}
	}
	levels := []RuntimeLevel{{Value: "low", Label: "Low"}, {Value: "medium", Label: "Medium"}, {Value: "high", Label: "High"}, {Value: "xhigh", Label: "Extra high"}}
	models := []RuntimeModel{
		{ID: "grok-4.6", Label: "Grok 4.6", Default: true},
		{ID: "grok-4.5", Label: "Grok 4.5"},
		{ID: "grok-composer-2.5-fast", Label: "Grok Composer 2.5 Fast"},
	}
	for index := range models {
		models[index].Thinking = &RuntimeThinking{SupportedLevels: levels}
	}
	return RuntimeModelCatalog{RuntimeID: runtimeID, Models: models, Fallback: true}
}

func parseACPRuntimeModels(raw json.RawMessage) []RuntimeModel {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	current := nestedString(root, "currentModelId", "current_model_id")
	entries := findACPModelEntries(root)
	models := make([]RuntimeModel, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		id := directString(entry, "modelId", "model_id", "id", "value")
		if !safeCatalogValue(id) || seen[id] {
			continue
		}
		label := directString(entry, "name", "label", "title")
		if label == "" {
			label = id
		}
		seen[id] = true
		models = append(models, RuntimeModel{ID: id, Label: label, Default: id == current})
	}
	levels, defaultLevel := parseACPThinkingLevels(root)
	if len(levels) > 0 {
		for index := range models {
			models[index].Thinking = &RuntimeThinking{DefaultLevel: defaultLevel, SupportedLevels: append([]RuntimeLevel(nil), levels...)}
		}
	}
	return models
}

func findACPModelEntries(value any) []map[string]any {
	entries := []map[string]any{}
	var walk func(any)
	walk = func(raw any) {
		switch item := raw.(type) {
		case map[string]any:
			for key, child := range item {
				lower := strings.ToLower(strings.ReplaceAll(key, "_", ""))
				if lower == "availablemodels" {
					if list, ok := child.([]any); ok {
						for _, candidate := range list {
							if entry, ok := candidate.(map[string]any); ok {
								entries = append(entries, entry)
							}
						}
					}
					continue
				}
				walk(child)
			}
		case []any:
			for _, child := range item {
				walk(child)
			}
		}
	}
	walk(value)
	if len(entries) > 0 {
		return entries
	}
	return findACPModelConfigChoices(value)
}

func findACPModelConfigChoices(value any) []map[string]any {
	entries := []map[string]any{}
	var walk func(any)
	walk = func(raw any) {
		switch item := raw.(type) {
		case map[string]any:
			id := strings.ToLower(directString(item, "id", "configId", "config_id"))
			category := strings.ToLower(directString(item, "category", "name", "label"))
			if strings.Contains(id, "model") || category == "model" || category == "models" {
				for _, key := range []string{"options", "choices"} {
					if list, ok := item[key].([]any); ok {
						for _, child := range list {
							if entry, ok := child.(map[string]any); ok {
								entries = append(entries, entry)
							}
						}
					}
				}
			}
			for _, child := range item {
				walk(child)
			}
		case []any:
			for _, child := range item {
				walk(child)
			}
		}
	}
	walk(value)
	return entries
}

func parseACPThinkingLevels(value any) ([]RuntimeLevel, string) {
	type choice struct {
		value string
		label string
	}
	choices := []choice{}
	defaultLevel := ""
	var walk func(any)
	walk = func(raw any) {
		switch item := raw.(type) {
		case map[string]any:
			id := strings.ToLower(directString(item, "id", "configId", "config_id"))
			category := strings.ToLower(directString(item, "category", "name"))
			isThinking := strings.Contains(id, "think") || strings.Contains(id, "thought") || strings.Contains(id, "reason") || strings.Contains(id, "effort") || strings.Contains(category, "think") || strings.Contains(category, "reason")
			if isThinking {
				defaultLevel = directString(item, "currentValue", "current_value", "defaultValue", "default_value")
				for _, key := range []string{"options", "choices"} {
					if list, ok := item[key].([]any); ok {
						for _, rawChoice := range list {
							entry, ok := rawChoice.(map[string]any)
							if !ok {
								continue
							}
							value := directString(entry, "value", "id")
							if value != "" {
								label := directString(entry, "name", "label", "title")
								choices = append(choices, choice{value: value, label: label})
							}
						}
					}
				}
				return
			}
			for _, child := range item {
				walk(child)
			}
		case []any:
			for _, child := range item {
				walk(child)
			}
		}
	}
	walk(value)
	levels := make([]RuntimeLevel, 0, len(choices))
	seen := map[string]bool{}
	for _, item := range choices {
		if seen[item.value] || !safeCatalogValue(item.value) {
			continue
		}
		seen[item.value] = true
		label := item.label
		if label == "" {
			label = levelLabel(item.value)
		}
		levels = append(levels, RuntimeLevel{Value: item.value, Label: label})
	}
	sort.SliceStable(levels, func(i, j int) bool {
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
	return levels, defaultLevel
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
