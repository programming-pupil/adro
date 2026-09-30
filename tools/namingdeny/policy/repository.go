package policy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const maxFileBytes = 16 << 20

type Violation struct {
	Path   string
	Line   int
	Rule   string
	Source string
}

func (v Violation) String() string {
	return fmt.Sprintf("naming: %s:%d %s (%s)", v.Path, v.Line, v.Rule, v.Source)
}

type indexedFile struct {
	path string
	hash string
}

// Scan checks both staged blobs and working files. An unstaged edit cannot hide
// a forbidden staged value, and a clean index cannot hide an unstaged violation.
// Symlinks are scanned as link text and are never followed outside the checkout.
func Scan(ctx context.Context, root string, d *List) ([]Violation, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "--stage", "-z")
	cmd.Dir = root
	index, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("naming: read git index: %w", err)
	}
	files, err := parseIndex(index)
	if err != nil {
		return nil, err
	}
	var violations []Violation
	checkout, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("naming: open checkout: %w", err)
	}
	defer checkout.Close()
	for _, file := range files {
		if rule, _ := d.Hit(file.path); rule != "" {
			violations = append(violations, Violation{Path: file.path, Line: 0, Rule: rule, Source: "path"})
		}
		data, err := readWorking(checkout, file)
		if err != nil {
			return nil, fmt.Errorf("naming: read %s: %w", file.path, err)
		}
		violations = append(violations, scanContent(d, file.path, "worktree", data)...)
	}
	err = scanIndex(ctx, root, files, func(file indexedFile, data []byte) {
		violations = append(violations, scanContent(d, file.path, "index", data)...)
	})
	if err != nil {
		return nil, err
	}
	return violations, nil
}

func parseIndex(data []byte) ([]indexedFile, error) {
	var files []indexedFile
	if len(data) == 0 {
		return nil, nil
	}
	for _, entry := range bytes.Split(bytes.TrimSuffix(data, []byte{0}), []byte{0}) {
		parts := strings.SplitN(string(entry), "\t", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("naming: malformed index entry")
		}
		metadata := strings.Fields(parts[0])
		if len(metadata) != 3 || metadata[2] != "0" {
			return nil, fmt.Errorf("naming: malformed or unmerged index entry")
		}
		hash, err := hex.DecodeString(metadata[1])
		if err != nil || (len(hash) != 20 && len(hash) != 32) {
			return nil, fmt.Errorf("naming: malformed index object ID")
		}
		path := parts[1]
		if path == "" || filepath.IsAbs(path) || filepath.ToSlash(filepath.Clean(path)) != path || path == ".." || strings.HasPrefix(path, "../") {
			return nil, fmt.Errorf("naming: unsafe index path")
		}
		if !Excluded(path) {
			if metadata[0] != "100644" && metadata[0] != "100755" && metadata[0] != "120000" {
				return nil, fmt.Errorf("naming: unsupported tracked object at %s", path)
			}
			files = append(files, indexedFile{path: path, hash: metadata[1]})
		}
	}
	return files, nil
}

func readWorking(root *os.Root, file indexedFile) ([]byte, error) {
	path := file.path
	info, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := root.Readlink(path)
		return []byte(target), err
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return nil, fmt.Errorf("not a regular file or exceeds %d-byte scan limit", maxFileBytes)
	}
	f, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	closeErr := f.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(data) > maxFileBytes {
		return nil, fmt.Errorf("file grew beyond scan limit")
	}
	return data, nil
}

func scanContent(d *List, path, source string, data []byte) []Violation {
	if bytes.IndexByte(data, 0) >= 0 {
		return nil
	}
	var out []Violation
	for i, line := range strings.Split(string(data), "\n") {
		if rule, _ := d.Hit(line); rule != "" {
			out = append(out, Violation{Path: path, Line: i + 1, Rule: rule, Source: source})
		}
	}
	return out
}

// scanIndex uses one streaming batch subprocess rather than loading all blobs
// into memory or spawning a process for every file. The child is always reaped.
func scanIndex(ctx context.Context, root string, files []indexedFile, visit func(indexedFile, []byte)) error {
	if len(files) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "cat-file", "--batch")
	cmd.Dir = root
	var input strings.Builder
	for _, file := range files {
		input.WriteString(file.hash + "\n")
	}
	cmd.Stdin = strings.NewReader(input.String())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("naming: open blob stream: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("naming: start blob reader: %w", err)
	}
	reader := bufio.NewReader(stdout)
	readErr := readBlobs(reader, files, visit)
	if readErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return readErr
	}
	if waitErr != nil {
		return fmt.Errorf("naming: read staged blobs: %w", waitErr)
	}
	return nil
}

func readBlobs(reader *bufio.Reader, files []indexedFile, visit func(indexedFile, []byte)) error {
	for _, file := range files {
		header, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("naming: missing blob header: %w", err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != file.hash || fields[1] != "blob" {
			return fmt.Errorf("naming: invalid blob header for %s", file.path)
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 || size > maxFileBytes {
			return fmt.Errorf("naming: invalid or oversized staged blob at %s", file.path)
		}
		data := make([]byte, int(size)+1)
		if _, err := io.ReadFull(reader, data); err != nil || data[len(data)-1] != '\n' {
			return fmt.Errorf("naming: incomplete staged blob at %s", file.path)
		}
		visit(file, data[:len(data)-1])
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		return fmt.Errorf("naming: unexpected trailing data in blob stream")
	}
	return nil
}
