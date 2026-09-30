package supervisor

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/adro-project/adro/core/errs"
)

// SaveAtomic publishes a complete encoded record with private permissions.
// The caller owns and validates the parent directory and bounds the record.
// syncParent must durably sync the destination's parent, as scan.SyncParent does.
// A KindAmbiguous result means the new bytes are visible but durability is not
// established. Callers must reconcile that outcome instead of assuming rollback.
// V2-04 supplies process-record and receipt schemas; this function owns only I/O.
func SaveAtomic(path string, record []byte, syncParent func(string) error) (result error) {
	if path == "" || filepath.Base(path) == "." || filepath.Base(path) == string(filepath.Separator) || syncParent == nil {
		return atomicError(errs.KindInvalid, "invalid_destination", nil)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".adro-record-*")
	if err != nil {
		return atomicError(errs.KindUnavailable, "create_temp", err)
	}
	tmp := f.Name()
	defer func() {
		if f != nil {
			if err := f.Close(); err != nil {
				result = errors.Join(result, atomicError(errs.KindUnavailable, "close_temp", err))
			}
		}
		if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, atomicError(errs.KindUnavailable, "remove_temp", err))
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		return atomicError(errs.KindUnavailable, "chmod_temp", err)
	}
	n, err := f.Write(record)
	if err != nil {
		return atomicError(errs.KindUnavailable, "write_record", err)
	}
	if n != len(record) {
		return atomicError(errs.KindUnavailable, "short_write", io.ErrShortWrite)
	}
	if err := f.Sync(); err != nil {
		return atomicError(errs.KindUnavailable, "sync_file", err)
	}
	closeErr := f.Close()
	f = nil
	if closeErr != nil {
		return atomicError(errs.KindUnavailable, "close_temp", closeErr)
	}
	if err := os.Rename(tmp, path); err != nil {
		return atomicError(errs.KindUnavailable, "rename_record", err)
	}
	if err := syncParent(path); err != nil {
		return atomicError(errs.KindAmbiguous, "sync_parent", err)
	}
	return nil
}

func atomicError(kind errs.Kind, code string, cause error) error {
	return &errs.Error{Kind: kind, Op: "supervisor.save_atomic", Code: code, Err: cause}
}
