package policy

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func git(t *testing.T, root string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func write(t *testing.T, root, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestScanIndexWorktreeNamesAndExemptions(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "--quiet")
	write(t, root, "staged.txt", "QuartzUnit\n")
	write(t, root, "working.txt", "ordinary\n")
	write(t, root, "QuartzUnit.txt", "ordinary\n")
	write(t, root, "drivers/installed.yaml", "QuartzUnit\n")
	write(t, root, "drivers/fakecli.yaml", "QuartzUnit\n")
	write(t, root, "go.sum", "QuartzUnit\n")
	write(t, root, "THIRD_PARTY_LICENSES/example.txt", "QuartzUnit\n")
	write(t, root, "image.bin", "\x00QuartzUnit\n")
	git(t, root, "add", ".")
	write(t, root, "staged.txt", "ordinary\n")
	write(t, root, "working.txt", "QuartzUnit\n")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	violations, err := Scan(ctx, root, exampleList(t))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"staged.txt/index/1":                          true,
		"working.txt/worktree/1":                      true,
		"QuartzUnit.txt/path/0":                       true,
		"drivers/fakecli.yaml/index/1":                true,
		"drivers/fakecli.yaml/worktree/1":             true,
		"go.sum/index/1":                              true,
		"go.sum/worktree/1":                           true,
		"THIRD_PARTY_LICENSES/example.txt/index/1":    true,
		"THIRD_PARTY_LICENSES/example.txt/worktree/1": true,
	}
	for _, v := range violations {
		key := fmt.Sprintf("%s/%s/%d", v.Path, v.Source, v.Line)
		if !want[key] {
			t.Errorf("unexpected or duplicate violation: %s", key)
		}
		delete(want, key)
		if v.Path != "QuartzUnit.txt" && strings.Contains(strings.ToLower(v.String()), "quartzunit") {
			t.Errorf("diagnostic exposed source content: %s", v)
		}
	}
	if len(want) > 0 {
		t.Fatalf("missed violations: %v", want)
	}
}

func TestScanMissingWorktreeAndCancelledProcessFail(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "--quiet")
	write(t, root, "removed.txt", "ordinary\n")
	git(t, root, "add", ".")
	if err := os.Remove(filepath.Join(root, "removed.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(context.Background(), root, exampleList(t)); err == nil {
		t.Fatal("missing tracked file must fail, not be silently ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, root, exampleList(t)); err == nil {
		t.Fatal("cancelled git command was accepted")
	}
}

func TestCleanRepositoryPasses(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "--quiet")
	write(t, root, "safe.txt", "ordinary record\n")
	git(t, root, "add", ".")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	violations, err := Scan(ctx, root, exampleList(t))
	if err != nil || len(violations) != 0 {
		t.Fatalf("clean repository: %v, %v", violations, err)
	}
}

func TestReadWorkingDoesNotFollowSymlinkOutsideRoot(t *testing.T) {
	dir := t.TempDir()
	external := t.TempDir()
	write(t, external, "record", "QuartzUnit")
	if err := os.Symlink(external, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	data, err := readWorking(root, indexedFile{path: "escape"})
	if err != nil || string(data) != external {
		t.Fatalf("symlink text: %q, %v", data, err)
	}
	if _, err := readWorking(root, indexedFile{path: "escape/record"}); err == nil {
		t.Fatal("symlink parent escaped checkout")
	}
}

func TestMalformedIndexAndBlobStreamFail(t *testing.T) {
	hash := strings.Repeat("a", 40)
	for _, input := range []string{"bad\x00", "100644 " + hash + " 1\tx\x00", "100644 invalid 0\tx\x00", "100644 " + hash + " 0\t../outside\x00", "160000 " + hash + " 0\tsubmodule\x00"} {
		if _, err := parseIndex([]byte(input)); err == nil {
			t.Errorf("malformed index accepted: %q", input)
		}
	}
	files := []indexedFile{{path: "test.txt", hash: hash}}
	for _, input := range []string{"", hash + " missing\n", hash + " tree 0\n\n", hash + " blob -1\n", hash + " blob 16777217\n", hash + " blob 3\nabc", hash + " blob 3\nabc\nextra"} {
		if err := readBlobs(bufio.NewReader(strings.NewReader(input)), files, func(indexedFile, []byte) {}); err == nil {
			t.Errorf("malformed blob stream accepted: %q", input)
		}
	}
	called := false
	err := readBlobs(bufio.NewReader(strings.NewReader(hash+" blob 3\nabc\n")), files, func(_ indexedFile, data []byte) { called = string(data) == "abc" })
	if err != nil || !called {
		t.Fatalf("valid blob rejected: %v", err)
	}
}
