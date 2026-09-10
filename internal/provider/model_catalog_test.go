package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverRuntimeModelsUsesNativeCatalogs(t *testing.T) {
	dir := t.TempDir()
	codex := filepath.Join(dir, "codex")
	codexOutput := `{"models":[{"slug":"model-a","display_name":"Model A","priority":1,"default_reasoning_level":"high","supported_reasoning_levels":[{"effort":"low","description":"fast"},{"effort":"high","description":"deep"}],"service_tiers":[{"id":"priority","name":"Fast"}]}]}`
	if err := os.WriteFile(codex, []byte("#!/bin/sh\nprintf '%s' '"+codexOutput+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(dir, "claude")
	if err := os.WriteFile(claude, []byte("#!/bin/sh\nprintf '%s\\n' '  --effort <level> Effort level (low, medium, high, xhigh, max)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("ADRO_EXECUTOR", "")

	codexCatalog, err := DiscoverRuntimeModels(context.Background(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if !codexCatalog.Dynamic || codexCatalog.Fallback || len(codexCatalog.Models) != 1 {
		t.Fatalf("codex catalog=%+v", codexCatalog)
	}
	model := codexCatalog.Models[0]
	if model.ID != "model-a" || model.Thinking == nil || len(model.Thinking.SupportedLevels) != 2 || len(model.ServiceTiers) != 1 {
		t.Fatalf("codex model=%+v", model)
	}

	claudeCatalog, err := DiscoverRuntimeModels(context.Background(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	var sonnet, opus *RuntimeModel
	for i := range claudeCatalog.Models {
		if claudeCatalog.Models[i].ID == "claude-sonnet-4-6" {
			sonnet = &claudeCatalog.Models[i]
		}
		if claudeCatalog.Models[i].ID == "claude-opus-4-8" {
			opus = &claudeCatalog.Models[i]
		}
	}
	if sonnet == nil || opus == nil || sonnet.Thinking == nil || opus.Thinking == nil {
		t.Fatalf("claude catalog=%+v", claudeCatalog)
	}
	if len(sonnet.Thinking.SupportedLevels) >= len(opus.Thinking.SupportedLevels) {
		t.Fatalf("model-specific efforts not filtered: sonnet=%+v opus=%+v", sonnet.Thinking, opus.Thinking)
	}
}

func TestDiscoverRuntimeModelsRejectsUnavailableRuntime(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("ADRO_EXECUTOR", "")
	if _, err := DiscoverRuntimeModels(context.Background(), "codex"); err == nil {
		t.Fatal("uninstalled runtime catalog was returned")
	}
	if _, err := DiscoverRuntimeModels(context.Background(), "unknown"); err == nil {
		t.Fatal("unknown runtime catalog was returned")
	}
}
