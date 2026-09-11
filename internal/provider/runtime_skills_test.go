package provider

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeRuntimeSkill(t *testing.T, root, key, content string) string {
	t.Helper()
	directory := filepath.Join(root, filepath.FromSlash(key))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestDiscoverRuntimeSkillsMergesProviderAndUniversalRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	writeRuntimeSkill(t, filepath.Join(home, ".codex", "skills"), "review", "---\nname: Runtime Review\ndescription: Review changes\n---\n")
	writeRuntimeSkill(t, filepath.Join(home, ".agents", "skills"), "review", "---\nname: Lower Priority\n---\n")
	writeRuntimeSkill(t, filepath.Join(home, ".agents", "skills"), "release/check", "---\nname: Release Check\n---\n")

	items, supported, err := DiscoverRuntimeSkills("codex")
	if err != nil || !supported {
		t.Fatalf("supported=%v err=%v", supported, err)
	}
	if len(items) != 2 {
		t.Fatalf("items=%+v", items)
	}
	byKey := map[string]RuntimeSkillSummary{}
	for _, item := range items {
		byKey[item.Key] = item
	}
	if byKey["review"].Name != "Runtime Review" || byKey["review"].Root != "provider" || !byKey["review"].CanDisable {
		t.Fatalf("provider Skill=%+v", byKey["review"])
	}
	if byKey["release/check"].Root != "universal" || byKey["release/check"].SourcePath != "~/.agents/skills/release/check" {
		t.Fatalf("universal Skill=%+v", byKey["release/check"])
	}
}

func TestDiscoverRuntimeSkillsFollowsSymlinksAndRejectsCycles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	target := writeRuntimeSkill(t, filepath.Join(home, "shared"), "linked", "---\nname: Linked Skill\n---\n")
	root := filepath.Join(home, ".codex", "skills")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "installer-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "cycle")); err != nil {
		t.Fatal(err)
	}

	items, supported, err := DiscoverRuntimeSkills("codex")
	if err != nil || !supported || len(items) != 1 {
		t.Fatalf("items=%+v supported=%v err=%v", items, supported, err)
	}
	if items[0].Key != "installer-link" || items[0].Name != "Linked Skill" || items[0].SourcePath != "~/.codex/skills/installer-link" {
		t.Fatalf("linked Skill=%+v", items[0])
	}
}

func TestDiscoverRuntimeSkillsIncludesOnlyEnabledContainedClaudePlugins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pluginRoot := filepath.Join(home, ".claude", "plugins", "cache", "review-pack")
	writeRuntimeSkill(t, filepath.Join(pluginRoot, "components"), "audit", "---\nname: Audit\ndescription: Audit code\n---\n")
	outside := writeRuntimeSkill(t, filepath.Join(home, "outside"), "escaped", "---\nname: Escaped\n---\n")
	_ = outside
	manifestDir := filepath.Join(pluginRoot, ".claude-plugin")
	if err := os.MkdirAll(manifestDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), []byte(`{"name":"review-pack","skills":["components","../../../outside"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	settingsDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(settingsDir, "plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(`{"enabledPlugins":{"review-pack@official":true,"disabled@official":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := `{"plugins":{"review-pack@official":[{"scope":"user","installPath":"` + filepath.ToSlash(pluginRoot) + `"}],"disabled@official":[{"scope":"user","installPath":"` + filepath.ToSlash(filepath.Join(home, "disabled")) + `"}]}}`
	if err := os.WriteFile(filepath.Join(settingsDir, "plugins", "installed_plugins.json"), []byte(registry), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRuntimeSkill(t, filepath.Join(home, "disabled", "skills"), "hidden", "---\nname: Hidden\n---\n")

	items, supported, err := DiscoverRuntimeSkills("claude")
	if err != nil || !supported || len(items) != 1 {
		t.Fatalf("items=%+v supported=%v err=%v", items, supported, err)
	}
	got := items[0]
	if got.Key != "review-pack:audit" || got.Name != got.Key || got.Root != "plugin" || got.Plugin != "review-pack@official" || !got.CanDisable {
		t.Fatalf("plugin Skill=%+v", got)
	}
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
	}
	if slices.Contains(keys, "review-pack:escaped") || slices.Contains(keys, "disabled:hidden") {
		t.Fatalf("unexpected plugin Skills=%v", keys)
	}
}

func TestDiscoverRuntimeSkillsRejectsUnknownRuntime(t *testing.T) {
	items, supported, err := DiscoverRuntimeSkills("missing")
	if err != nil || supported || items != nil {
		t.Fatalf("items=%+v supported=%v err=%v", items, supported, err)
	}
}
