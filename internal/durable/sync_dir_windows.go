//go:build windows

package durable

// Windows does not provide the POSIX directory-fsync contract through
// os.File.Sync. Atomic replacement remains the platform commit boundary; the
// production Windows profile must rely on its database-backed durable store.
func syncParent(string) error {
	return nil
}
