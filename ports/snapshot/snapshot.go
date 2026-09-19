// Package snapshot defines the optional replay acceleration boundary.
package snapshot

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("snapshot not found")
	ErrConflict = errors.New("snapshot compare-and-swap conflict")
	ErrCorrupt  = errors.New("snapshot integrity failure")
)

// Snapshot is a verified state cache. Event history remains authoritative and
// can rebuild the snapshot after loss or corruption.
type Snapshot struct {
	TenantID        string    `json:"tenant_id"`
	StreamID        string    `json:"stream_id"`
	Sequence        int64     `json:"sequence"`
	SchemaVersion   int       `json:"schema_version"`
	EncodingVersion int       `json:"encoding_version"`
	HashAlgorithm   string    `json:"hash_algorithm"`
	HashVersion     int       `json:"hash_version"`
	Digest          string    `json:"digest"`
	Payload         []byte    `json:"payload"`
	CreatedAt       time.Time `json:"created_at"`
}

type Store interface {
	Get(context.Context, string) (Snapshot, error)
	Put(context.Context, Snapshot, int64) error
}
