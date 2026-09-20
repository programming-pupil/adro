// Package projection defines a rebuildable read-model store. Projection data
// is never authoritative; each record names the source stream and sequence it
// was derived from so a worker can detect stale writes and rebuild from zero.
package projection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

var (
	ErrNotFound       = errors.New("projection record not found")
	ErrConflict       = errors.New("projection compare-and-swap conflict")
	ErrTenantMismatch = errors.New("projection tenant boundary mismatch")
	ErrCorrupt        = errors.New("projection record integrity failure")
	ErrLimit          = errors.New("projection payload exceeds size limit")
	ErrClosed         = errors.New("projection store is closed")
	ErrOffsetNotFound = errors.New("projection offset not found")
	ErrOffsetConflict = errors.New("projection offset compare-and-swap conflict")
)

type Record struct {
	TenantID       string    `json:"tenant_id"`
	Projection     string    `json:"projection"`
	Key            string    `json:"key"`
	Version        int64     `json:"version"`
	SourceStream   string    `json:"source_stream"`
	SourceSequence int64     `json:"source_sequence"`
	Digest         string    `json:"digest"`
	Payload        []byte    `json:"payload"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Digest returns the stable SHA-256 digest used by projection records and
// offsets. Callers may persist the result as a lowercase hexadecimal string.
func Digest(payload []byte) string {
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}

type Store interface {
	Get(context.Context, string, string, string) (Record, error)
	Put(context.Context, Record, int64) error
	Delete(context.Context, string, string, string, int64) error
	List(context.Context, string, string) ([]Record, error)
}

// Offset is the durable checkpoint for one projection partition. The event
// stream remains authoritative; the digest lets a worker fail closed when the
// read model and its checkpoint diverge after a crash or manual mutation.
type Offset struct {
	TenantID         string    `json:"tenant_id"`
	Projection       string    `json:"projection"`
	PartitionID      string    `json:"partition_id"`
	LastSequence     int64     `json:"last_sequence"`
	ProjectionDigest string    `json:"projection_digest"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// OffsetStore persists a projection worker checkpoint with compare-and-swap
// semantics. Implementations must enforce the same tenant scope as Store.
type OffsetStore interface {
	GetOffset(context.Context, string, string, string) (Offset, error)
	PutOffset(context.Context, Offset, int64) error
}
