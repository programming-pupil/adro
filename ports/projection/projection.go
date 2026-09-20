// Package projection defines a rebuildable read-model store. Projection data
// is never authoritative; each record names the source stream and sequence it
// was derived from so a worker can detect stale writes and rebuild from zero.
package projection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	coreencoding "github.com/adro-project/adro/core/encoding"
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
	ErrDiverged       = errors.New("projection deterministic divergence")
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

// ProjectionDigest computes the canonical digest for records belonging to one
// source stream. The projection port owns this encoding so workers and storage
// adapters cannot drift in their offset integrity checks.
func ProjectionDigest(tenantID, projectionName, sourceStream string, records []Record) (string, error) {
	filtered := make([]Record, 0, len(records))
	for _, record := range records {
		if record.TenantID == tenantID && record.Projection == projectionName && record.SourceStream == sourceStream {
			filtered = append(filtered, record)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Key < filtered[j].Key })
	canonicalRecords := make([]projectionDigestRecord, 0, len(filtered))
	for _, record := range filtered {
		canonicalRecords = append(canonicalRecords, projectionDigestRecord{
			Key: record.Key, Version: record.Version, SourceStream: record.SourceStream,
			SourceSequence: record.SourceSequence, Digest: record.Digest, Payload: record.Payload,
		})
	}
	return coreencoding.Digest(struct {
		TenantID   string                   `json:"tenant_id"`
		Projection string                   `json:"projection"`
		Records    []projectionDigestRecord `json:"records"`
	}{TenantID: tenantID, Projection: projectionName, Records: canonicalRecords})
}

type projectionDigestRecord struct {
	Key            string `json:"key"`
	Version        int64  `json:"version"`
	SourceStream   string `json:"source_stream"`
	SourceSequence int64  `json:"source_sequence"`
	Digest         string `json:"digest"`
	Payload        []byte `json:"payload"`
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

// BatchMutation is one ordered record operation in an atomic projection batch.
// SourceSequence is carried per mutation because one event page may update the
// same key more than once across different events.
type BatchMutation struct {
	Key            string `json:"key"`
	Payload        []byte `json:"payload"`
	Delete         bool   `json:"delete"`
	SourceSequence int64  `json:"source_sequence"`
}

// Batch groups all mutations derived from an event page. Implementations must
// commit the record changes and the resulting offset as one local transaction.
type Batch struct {
	TenantID     string          `json:"tenant_id"`
	Projection   string          `json:"projection"`
	PartitionID  string          `json:"partition_id"`
	LastSequence int64           `json:"last_sequence"`
	Mutations    []BatchMutation `json:"mutations"`
}

// BatchResult reports the committed mutation effects and digest.
type BatchResult struct {
	UpdatedRecords   int
	DeletedRecords   int
	SkippedMutations int
	ProjectionDigest string
}

// AtomicStore extends Store and OffsetStore for backends that can commit a
// complete projection page and its offset in one transaction.
type AtomicStore interface {
	Store
	OffsetStore
	ApplyBatch(context.Context, Batch, int64) (BatchResult, error)
}
