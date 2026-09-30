package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupPolicy(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	salt := bytes.Repeat([]byte{7}, 16)
	hash := sha256.Sum256(append(append([]byte(nil), salt...), "quartzunit"...))
	if err := os.WriteFile(filepath.Join(root, "policy.sha256"), []byte(fmt.Sprintf("%x\nsub 10 %x\n", salt, hash)), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestAddReadsStandardInputAndPrintsOnlyHashes(t *testing.T) {
	root := setupPolicy(t)
	var out bytes.Buffer
	err := run([]string{"add", "-root", root, "-list", "policy.sha256", "-kind", "sub"}, strings.NewReader("QuartzUnit\n"), &out)
	if err != nil || !strings.HasPrefix(out.String(), "sub 10 ") || strings.Contains(strings.ToLower(out.String()), "quartz") {
		t.Fatalf("add output: %q, %v", out.String(), err)
	}
	for _, input := range []string{"", "qxz", "two words"} {
		if err := run([]string{"add", "-root", root, "-list", "policy.sha256", "-kind", "sub"}, strings.NewReader(input), &out); err == nil {
			t.Errorf("bad input accepted: %q", input)
		}
	}
}

func TestExplainShowsOnlyLocallyRequestedMatch(t *testing.T) {
	root := setupPolicy(t)
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("ordinary\nQuartzUnit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := run([]string{"explain", "-root", root, "-list", "policy.sha256", "source.txt:2"}, nil, &out)
	if err != nil || !strings.Contains(out.String(), `"quartzunit"`) || strings.Contains(out.String(), "ordinary") {
		t.Fatalf("explain output: %q, %v", out.String(), err)
	}
	for _, location := range []string{"source.txt:1", "source.txt:3", "source.txt:-1", "../source.txt:1", "source.txt"} {
		if err := run([]string{"explain", "-root", root, "-list", "policy.sha256", location}, nil, &out); err == nil {
			t.Errorf("invalid explain request accepted: %q", location)
		}
	}
}

func TestCommandRejectsUnknownArguments(t *testing.T) {
	root := setupPolicy(t)
	var out bytes.Buffer
	for _, args := range [][]string{nil, {"check", "-unknown"}, {"unknown", "-root", root, "-list", "policy.sha256"}, {"add", "-root", root, "-list", "policy.sha256", "plaintext"}} {
		if err := run(args, nil, &out); err == nil {
			t.Errorf("invalid arguments accepted: %v", args)
		}
	}
}
