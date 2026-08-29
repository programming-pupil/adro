package observer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSnapshotReportsChangedFiles(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "test@example.invalid"}, {"config", "user.name", "Test"}} {
		runGit(t, dir, args...)
	}
	write := filepath.Join(dir, "README.md")
	if err := os.WriteFile(write, []byte("one\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial")
	if err := os.WriteFile(write, []byte("two\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "change")
	diff, err := Snapshot(context.Background(), dir, "work", "repo", "HEAD~1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Files) != 1 || diff.Files[0] != "README.md" || diff.ContentSHA256 == "" {
		t.Fatalf("diff=%+v", diff)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, output)
	}
}
