// Package blobstore defines the durable content-addressed blob boundary.
package blobstore

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrNotFound      = errors.New("blob not found")
	ErrConflict      = errors.New("blob metadata conflict")
	ErrTombstoned    = errors.New("blob is tombstoned")
	ErrLegalHold     = errors.New("blob is under legal hold")
	ErrNotTombstoned = errors.New("blob is not tombstoned")
	ErrLimit         = errors.New("blob exceeds size limit")
)

// BlobRef is safe to persist in an event. It contains identity and policy
// metadata, never the blob bytes.
type BlobRef struct {
	TenantID       string    `json:"tenant_id"`
	Digest         string    `json:"digest"`
	Size           int64     `json:"size"`
	MediaType      string    `json:"media_type"`
	EncryptionKey  string    `json:"encryption_key,omitempty"`
	Classification string    `json:"classification,omitempty"`
	RetainUntil    time.Time `json:"retain_until,omitempty"`
}

type BlobMetadata struct {
	BlobRef
	CreatedAt  time.Time `json:"created_at"`
	Tombstoned bool      `json:"tombstoned"`
	LegalHold  bool      `json:"legal_hold"`
	Tombstone  string    `json:"tombstone,omitempty"`
	// StoredDigest authenticates the bytes kept by the backend. It is distinct
	// from BlobRef.Digest, which always identifies the plaintext and therefore
	// remains stable when an encrypted representation is re-sealed.
	StoredDigest string `json:"stored_digest,omitempty"`
}

type BlobPutRequest struct {
	TenantID       string
	MediaType      string
	EncryptionKey  string
	Classification string
	RetainUntil    time.Time
	LegalHold      bool
	MaxBytes       int64
}

type BlobStore interface {
	Put(context.Context, BlobPutRequest, io.Reader) (BlobRef, error)
	Open(context.Context, BlobRef) (io.ReadCloser, error)
	Stat(context.Context, BlobRef) (BlobMetadata, error)
	Tombstone(context.Context, BlobRef, string) error
}

// Inventory is the optional lifecycle boundary used by mark-and-sweep workers.
// Implementations must enforce the same tenant scope and integrity checks as
// BlobStore; callers never receive arbitrary SQL or filesystem paths.
type Inventory interface {
	BlobStore
	List(context.Context, string) ([]BlobMetadata, error)
	Purge(context.Context, BlobRef, string) error
}
