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
	t.Setenv("ADRO_EXECUTOR", path)
	items := DiscoverLocalRuntimes()
	if len(items) != len(RuntimeRegistry)+1 || items[0].ID != "local" || !items[0].Installed || !items[0].AdapterAvailable {
		t.Fatalf("explicit executor missing: %+v", items)
	}
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
