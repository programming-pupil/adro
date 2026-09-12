package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverLocalRuntimesFindsEveryInstalledClient(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"claude", "codex", "cursor-agent"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("SHELL", "")
	t.Setenv("ADRO_EXECUTOR", "")
	items := DiscoverLocalRuntimes()
	if len(items) != len(RuntimeRegistry) {
		t.Fatalf("discovered %d registry entries, want %d", len(items), len(RuntimeRegistry))
	}
	installed := map[string]bool{}
	for _, item := range items {
		if item.Installed {
			installed[item.ID] = true
		}
	}
	for _, id := range []string{"claude", "codex", "cursor"} {
		if !installed[id] {
			t.Fatalf("runtime %q was not discovered: %+v", id, items)
		}
	}
}

func TestDiscoverLocalRuntimesIncludesExplicitExecutor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "private-agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("SHELL", "")
	t.Setenv("ADRO_EXECUTOR", path)
	items := DiscoverLocalRuntimes()
	if len(items) != len(RuntimeRegistry)+1 || items[0].ID != "local" || !items[0].Installed || !items[0].AdapterAvailable {
		t.Fatalf("explicit executor missing: %+v", items)
	}
}

func TestDiscoverLocalRuntimesHonorsPinnedExecutable(t *testing.T) {
	dir := t.TempDir()
	pinned := filepath.Join(dir, "company-codex")
	if err := os.WriteFile(pinned, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SHELL", "")
	t.Setenv("ADRO_CODEX_PATH", pinned)
	want, err := executablePath(pinned)
	if err != nil {
		t.Fatal(err)
	}
	items := DiscoverLocalRuntimes()
	for _, item := range items {
		if item.ID == "codex" {
			if !item.Installed || item.ExecutablePath != want {
				t.Fatalf("pinned runtime=%+v", item)
			}
			return
		}
	}
	t.Fatal("codex descriptor missing")
}

func TestDiscoverLocalRuntimesUsesLoginShellPath(t *testing.T) {
	dir := t.TempDir()
	codex := filepath.Join(dir, "shell-only-codex")
	if err := os.WriteFile(codex, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	shell := filepath.Join(dir, "zsh")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nprintf 'codex\\t"+codex+"\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SHELL", shell)
	// Keep the test focused on login-shell discovery even on macOS machines
	// that have Codex bundled inside ChatGPT.app.
	t.Setenv("ADRO_CODEX_PATH", "")
	want, err := executablePath(codex)
	if err != nil {
		t.Fatal(err)
	}
	items := DiscoverLocalRuntimes()
	for _, item := range items {
		if item.ID == "codex" {
			if !item.Installed || item.ExecutablePath != want {
				t.Fatalf("shell-discovered runtime=%+v", item)
			}
			return
		}
	}
	t.Fatal("codex descriptor missing")
}

func TestRuntimeRegistryHasUniqueIdentityAndCommand(t *testing.T) {
	ids, commands := map[string]bool{}, map[string]bool{}
	for _, item := range RuntimeRegistry {
		if ids[item.ID] || commands[item.Command] {
			t.Fatalf("duplicate runtime descriptor: %+v", item)
		}
		ids[item.ID], commands[item.Command] = true, true
	}
}

func TestDiscoverLocalProviderUsesTheSharedRuntimeRegistry(t *testing.T) {
	dir := t.TempDir()
	qwen := filepath.Join(dir, "qwen")
	if err := os.WriteFile(qwen, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("SHELL", "")
	t.Setenv("ADRO_EXECUTOR", "")
	t.Setenv("ADRO_EXECUTOR_COMMAND", "")
	// Prevent the macOS desktop-app fallback from winning ahead of the
	// registry entry under test.
	t.Setenv("ADRO_CODEX_PATH", filepath.Join(dir, "missing-codex"))

	provider, err := DiscoverLocalProvider(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := executablePath(qwen)
	if err != nil {
		t.Fatal(err)
	}
	if provider.Executable != want || provider.executorKind() != "qwen" {
		t.Fatalf("provider executable=%q kind=%q, want %q/qwen", provider.Executable, provider.executorKind(), want)
	}
}
