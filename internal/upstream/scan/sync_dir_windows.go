//go:build windows

package scan

import "github.com/adro-project/adro/core/errs"

// Windows does not provide the POSIX directory-fsync contract through
// os.File.Sync. Atomic replacement remains the platform commit boundary; the
// caller must not report a durable commit after this capability rejection.
func SyncParent(string) error {
	return &errs.Error{Kind: errs.KindUnsupported, Op: "scan.sync_parent", Code: "directory_sync.unsupported"}
}
