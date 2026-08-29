// Package observer turns a runner checkout into a deterministic changed-files
// snapshot. It has no control-plane dependencies, so a long-lived fs watcher
// can call Snapshot on a debounce interval or a one-shot job can call it after
// a gate completes.
package observer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os/exec"
	"strings"

	"github.com/adro-project/adro/internal/domain"
)

func Snapshot(ctx context.Context, repositoryPath, workItemID, repositoryID, baseCommit, headCommit string) (domain.DiffSnapshot, error) {
	if strings.TrimSpace(repositoryPath) == "" || workItemID == "" || repositoryID == "" {
		return domain.DiffSnapshot{}, errors.New("repository_path, work_item_id and repository_id are required")
	}
	if baseCommit == "" {
		baseCommit = "HEAD~1"
	}
	if headCommit == "" {
		headCommit = "HEAD"
	}
	filesOutput, err := git(ctx, repositoryPath, "diff", "--name-only", baseCommit, headCommit)
	if err != nil {
		return domain.DiffSnapshot{}, err
	}
	statOutput, err := git(ctx, repositoryPath, "diff", "--stat", baseCommit, headCommit)
	if err != nil {
		return domain.DiffSnapshot{}, err
	}
	files := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(filesOutput), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	h := sha256.Sum256([]byte(filesOutput + "\x00" + statOutput))
	return domain.DiffSnapshot{ID: domain.NewID(), WorkItemID: workItemID, RepositoryID: repositoryID, BaseCommit: baseCommit, HeadCommit: headCommit, Stat: map[string]any{"raw": strings.TrimSpace(statOutput), "file_count": len(files)}, Files: files, ContentSHA256: hex.EncodeToString(h[:])}, nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}
