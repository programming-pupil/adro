// Package durable contains small, dependency-free primitives shared by the
// local durable stores. The production profile can replace these primitives
// with a database/queue adapter without changing the domain contracts.
package durable

// WithExclusive executes fn while holding an inter-process lock associated
// with path. The lock file is intentionally separate from the snapshot so an
// atomic rename never invalidates a lock held by another process.
func WithExclusive(path string, fn func() error) error {
	return withExclusive(path, fn)
}
