package provider

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxRuntimeSkillMetadataSize int64 = 1 << 20
	maxRuntimeSkillDirDepth           = 4
)

type RuntimeSkillSummary struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SourcePath  string `json:"source_path"`
	Provider    string `json:"provider"`
	Root        string `json:"root"`
	Plugin      string `json:"plugin,omitempty"`
	CanDisable  bool   `json:"can_disable"`
}

type runtimeSkillRoot struct {
	Path      string
	Kind      string
	KeyPrefix string
	Plugin    string
}

type claudePluginInstall struct {
	ID          string
	Name        string
	InstallPath string
}

type claudeInstalledPluginsFile struct {
	Plugins map[string][]struct {
		Scope       string `json:"scope"`
		InstallPath string `json:"installPath"`
	} `json:"plugins"`
}

type claudeSettingsFile struct {
	EnabledPlugins map[string]bool `json:"enabledPlugins"`
}

type claudePluginManifest struct {
	Name   string          `json:"name"`
	Skills json.RawMessage `json:"skills"`
}

// DiscoverRuntimeSkills lists metadata only. Skill bodies never cross this API
// boundary. Symlink traversal is depth-bounded and cycle-safe so installer-
// managed Skill links work without allowing unbounded filesystem walks.
func DiscoverRuntimeSkills(runtimeID string) ([]RuntimeSkillSummary, bool, error) {
	runtimeID = strings.TrimSpace(runtimeID)
	if runtimeID == "local" {
		// The configured local executor has no provider-owned skill directory.
		// It is still a valid runtime selection, so return an empty catalog
		// instead of making the workbench surface a false 404.
		return []RuntimeSkillSummary{}, true, nil
	}
	known := false
	for _, descriptor := range RuntimeRegistry {
		if descriptor.ID == runtimeID {
			known = true
			break
		}
	}
	if !known {
		return nil, false, nil
	}
	roots, err := runtimeSkillRoots(runtimeID)
	if err != nil {
		return nil, true, err
	}
	items := make([]RuntimeSkillSummary, 0)
	seen := map[string]bool{}
	for _, root := range roots {
		if root.Path == "" {
			continue
		}
		rootItems := make([]RuntimeSkillSummary, 0)
		discoverRuntimeSkillRoot(runtimeID, root, root.Path, root.Path, 0, map[string]bool{}, &rootItems)
		for _, item := range rootItems {
			if seen[item.Key] {
				continue
			}
			seen[item.Key] = true
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name == items[j].Name {
			return items[i].Key < items[j].Key
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return items, true, nil
}

func runtimeSkillRoots(runtimeID string) ([]runtimeSkillRoot, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := ""
	switch runtimeID {
	case "claude":
		root = filepath.Join(home, ".claude", "skills")
	case "codex":
		base := strings.TrimSpace(os.Getenv("CODEX_HOME"))
		if base == "" {
			base = filepath.Join(home, ".codex")
		}
		root = filepath.Join(base, "skills")
	case "cursor":
		root = filepath.Join(home, ".cursor", "skills")
	case "copilot":
		root = filepath.Join(home, ".copilot", "skills")
	case "opencode", "deveco":
		root = filepath.Join(home, ".config", runtimeID, "skills")
	case "openclaw", "hermes", "kimi", "kiro", "qoder", "qoderclicn", "traecli", "zeroclaw":
		folder := map[string]string{"qoderclicn": ".qoder-cn"}[runtimeID]
		if folder == "" {
			folder = "." + runtimeID
		}
		root = filepath.Join(home, folder, "skills")
	case "pi", "omp":
		root = filepath.Join(home, ".pi", "agent", "skills")
	case "antigravity":
		root = filepath.Join(home, ".gemini", "antigravity-cli", "skills")
	case "codebuddy":
		root = filepath.Join(home, ".codebuddy", "skills")
	case "grok":
		base := strings.TrimSpace(os.Getenv("GROK_HOME"))
		if base == "" {
			base = filepath.Join(home, ".grok")
		}
		root = filepath.Join(base, "skills")
	case "qwen":
		base := strings.TrimSpace(os.Getenv("QWEN_HOME"))
		if base == "" {
			base = filepath.Join(home, ".qwen")
		}
		root = filepath.Join(base, "skills")
	case "qwenpaw":
		base := strings.TrimSpace(os.Getenv("QWENPAW_WORKING_DIR"))
		if base == "" {
			base = filepath.Join(home, ".qwenpaw")
		}
		root = filepath.Join(base, "skill_pool")
	case "reasonix":
		base := strings.TrimSpace(os.Getenv("REASONIX_HOME"))
		if base == "" {
			base = filepath.Join(home, ".reasonix")
		}
		root = filepath.Join(base, "skills")
	case "dsh":
		base := strings.TrimSpace(os.Getenv("DSH_HOME"))
		if base == "" {
			base = filepath.Join(home, ".dsh")
		}
		root = filepath.Join(base, "skills")
	case "mcode":
		root = filepath.Join(home, ".minimax", "skills")
	case "dim":
		root = filepath.Join(home, ".dim", "skills")
	}
	roots := []runtimeSkillRoot{{Path: root, Kind: "provider"}, {Path: filepath.Join(home, ".agents", "skills"), Kind: "universal"}}
	if runtimeID == "claude" {
		for _, plugin := range enabledClaudePlugins(home) {
			manifest, _ := readClaudePluginManifest(plugin.InstallPath)
			for _, path := range claudePluginSkillPaths(plugin.InstallPath, manifest.Skills) {
				roots = append(roots, runtimeSkillRoot{
					Path: path, Kind: "plugin", KeyPrefix: plugin.Name + ":", Plugin: plugin.ID,
				})
			}
		}
	}
	return roots, nil
}

func discoverRuntimeSkillRoot(runtimeID string, root runtimeSkillRoot, walkRoot, currentDir string, depth int, visited map[string]bool, items *[]RuntimeSkillSummary) {
	if depth > maxRuntimeSkillDirDepth {
		return
	}
	resolved, err := filepath.EvalSymlinks(currentDir)
	if err != nil {
		return
	}
	if visited[resolved] {
		return
	}
	visited[resolved] = true
	entries, err := os.ReadDir(currentDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		directory := filepath.Join(currentDir, entry.Name())
		info, err := os.Stat(directory)
		if err != nil || !info.IsDir() {
			continue
		}
		metadataPath := filepath.Join(directory, "SKILL.md")
		metadataInfo, err := os.Stat(metadataPath)
		if err == nil && !metadataInfo.IsDir() && metadataInfo.Size() <= maxRuntimeSkillMetadataSize {
			relative, err := filepath.Rel(walkRoot, directory)
			if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue
			}
			key := root.KeyPrefix + filepath.ToSlash(filepath.Clean(relative))
			name, description, err := readRuntimeSkillMetadata(metadataPath)
			if err != nil {
				continue
			}
			if root.Plugin != "" {
				name = key
			} else if name == "" {
				name = filepath.Base(directory)
			}
			*items = append(*items, RuntimeSkillSummary{
				Key: key, Name: name, Description: description,
				SourcePath: portableHomePath(directory), Provider: runtimeID,
				Root: root.Kind, Plugin: root.Plugin,
				CanDisable: runtimeID == "codex" || runtimeID == "claude",
			})
			continue
		}
		discoverRuntimeSkillRoot(runtimeID, root, walkRoot, directory, depth+1, visited, items)
	}
}

func enabledClaudePlugins(home string) []claudePluginInstall {
	settingsRaw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return nil
	}
	var settings claudeSettingsFile
	if json.Unmarshal(settingsRaw, &settings) != nil || len(settings.EnabledPlugins) == 0 {
		return nil
	}
	installedRaw, err := os.ReadFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	if err != nil {
		return nil
	}
	var installed claudeInstalledPluginsFile
	if json.Unmarshal(installedRaw, &installed) != nil {
		return nil
	}
	ids := make([]string, 0, len(settings.EnabledPlugins))
	for id, enabled := range settings.EnabledPlugins {
		if enabled {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	plugins := make([]claudePluginInstall, 0, len(ids))
	for _, id := range ids {
		installs := installed.Plugins[id]
		if len(installs) == 0 {
			continue
		}
		selected := installs[len(installs)-1]
		for _, install := range installs {
			if install.Scope == "user" {
				selected = install
			}
		}
		installPath := strings.TrimSpace(selected.InstallPath)
		if installPath == "" {
			continue
		}
		name := strings.TrimSpace(strings.SplitN(id, "@", 2)[0])
		if manifest, ok := readClaudePluginManifest(installPath); ok && strings.TrimSpace(manifest.Name) != "" {
			name = strings.TrimSpace(manifest.Name)
		}
		if name != "" {
			plugins = append(plugins, claudePluginInstall{ID: id, Name: name, InstallPath: installPath})
		}
	}
	return plugins
}

func readClaudePluginManifest(installPath string) (claudePluginManifest, bool) {
	raw, err := os.ReadFile(filepath.Join(installPath, ".claude-plugin", "plugin.json"))
	if err != nil {
		return claudePluginManifest{}, false
	}
	var manifest claudePluginManifest
	if json.Unmarshal(raw, &manifest) != nil {
		return claudePluginManifest{}, false
	}
	return manifest, true
}

func claudePluginSkillPaths(installPath string, raw json.RawMessage) []string {
	candidates := []string{filepath.Join(installPath, "skills")}
	var one string
	if json.Unmarshal(raw, &one) == nil && strings.TrimSpace(one) != "" {
		candidates = append(candidates, one)
	} else {
		var many []string
		if json.Unmarshal(raw, &many) == nil {
			candidates = append(candidates, many...)
		}
	}
	installPath = filepath.Clean(installPath)
	seen := map[string]bool{}
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(installPath, filepath.FromSlash(candidate))
		}
		candidate = filepath.Clean(candidate)
		relative, err := filepath.Rel(installPath, candidate)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || seen[candidate] {
			continue
		}
		seen[candidate] = true
		result = append(result, candidate)
	}
	return result
}

func readRuntimeSkillMetadata(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), int(maxRuntimeSkillMetadataSize))
	frontmatter := false
	started := false
	name, description := "", ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !started {
			started = true
			if line == "---" {
				frontmatter = true
				continue
			}
		}
		if !frontmatter || line == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			name = value
		case "description":
			description = value
		}
	}
	return name, description, scanner.Err()
}

func portableHomePath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil {
		if relative, relErr := filepath.Rel(home, path); relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "~/" + filepath.ToSlash(relative)
		}
	}
	return filepath.Base(path)
}
