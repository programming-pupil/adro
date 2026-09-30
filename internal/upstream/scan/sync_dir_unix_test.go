//go:build !windows

package scan_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/adro-project/adro/internal/upstream/scan"
)

func TestSyncParentAfterRename(t *testing.T) {
	dir := t.TempDir()
	temporary := filepath.Join(dir, "pending")
	committed := filepath.Join(dir, "committed")
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if _, err := file.WriteString("durable record"); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, committed); err != nil {
		t.Fatal(err)
	}
	if err := scan.SyncParent(committed); err != nil {
		t.Fatal(err)
	}
	if err := scan.SyncParent(committed); err != nil {
		t.Fatalf("repeated directory sync: %v", err)
	}
	data, err := os.ReadFile(committed)
	if err != nil || string(data) != "durable record" {
		t.Fatalf("record after sync: %q, %v", data, err)
	}
}

func TestSyncParentMissingOrInvalidDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := scan.SyncParent(filepath.Join(dir, "missing", "record")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing directory: %v", err)
	}
	file := filepath.Join(dir, "ordinary-file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := scan.SyncParent(filepath.Join(file, "record")); err == nil {
		t.Fatal("file used as a directory was accepted")
	}
}
