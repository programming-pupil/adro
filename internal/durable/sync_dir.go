package durable

// SyncParent makes a completed atomic rename durable at the directory-entry
// boundary on platforms that expose directory fsync. Platform implementations
// document any weaker guarantee explicitly.
func SyncParent(path string) error {
	return syncParent(path)
}
