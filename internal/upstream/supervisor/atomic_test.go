package supervisor

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/adro-project/adro/core/errs"
)

func assertNoTemps(t *testing.T, dir string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, ".adro-record-*"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("temporary files remain: %v (%v)", paths, err)
	}
}

func TestSaveAtomicReportsVisibleButUnconfirmedWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proc.json")
	failure := errors.New("directory sync failed")
	called := false
	err := SaveAtomic(path, []byte("complete"), func(got string) error {
		called = true
		if got != path {
			t.Fatalf("sync path=%q", got)
		}
		data, err := os.ReadFile(got)
		if err != nil || string(data) != "complete" {
			t.Fatalf("parent sync preceded publication: %q %v", data, err)
		}
		return failure
	})
	if !called || !errors.Is(err, failure) || errs.KindOf(err) != errs.KindAmbiguous || !errs.Retryable(err) {
		t.Fatalf("post-rename outcome not preserved: %v", err)
	}
	assertNoTemps(t, dir)
}

func TestSaveAtomicPreRenameFailures(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "directory")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(blocked, "keep")
	if err := os.WriteFile(sentinel, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{blocked, filepath.Join(dir, "missing", "proc.json")} {
		err := SaveAtomic(path, []byte("new"), func(string) error {
			t.Fatal("sync called despite failed rename/create")
			return nil
		})
		if errs.KindOf(err) != errs.KindUnavailable {
			t.Fatalf("failure was not surfaced: %v", err)
		}
		assertNoTemps(t, dir)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || !bytes.Equal(data, []byte("existing")) {
		t.Fatalf("existing destination damaged: %q %v", data, err)
	}
}

func TestSaveAtomicRejectsInvalidDestination(t *testing.T) {
	for _, path := range []string{"", ".", string(filepath.Separator)} {
		if err := SaveAtomic(path, nil, func(string) error { return nil }); errs.KindOf(err) != errs.KindInvalid {
			t.Fatalf("destination %q: %v", path, err)
		}
	}
	path := filepath.Join(t.TempDir(), "proc.json")
	if err := SaveAtomic(path, nil, nil); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("nil durability function accepted: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid input changed destination: %v", err)
	}
}
