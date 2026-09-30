//go:build !windows

package scan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adro-project/adro/core/errs"
)

// SyncParent makes an atomic rename durable at the directory-entry boundary.
func SyncParent(path string) (err error) {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open parent directory for sync: %w", err)
	}
	defer func() {
		if closeErr := directory.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close parent directory after sync: %w", closeErr))
		}
	}()
	info, err := directory.Stat()
	if err != nil {
		return fmt.Errorf("inspect parent directory for sync: %w", err)
	}
	if !info.IsDir() {
		return &errs.Error{Kind: errs.KindInvalid, Op: "scan.sync_parent", Code: "directory_sync.not_directory", Err: os.ErrInvalid}
	}
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync parent directory: %w", err)
	}
	return nil
}
