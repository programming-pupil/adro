// Package blobstore defines the durable content-addressed blob boundary.
package blobstore

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrNotFound   = errors.New("blob not found")
	ErrConflict   = errors.New("blob metadata conflict")
	ErrTombstoned = errors.New("blob is tombstoned")
	ErrLegalHold  = errors.New("blob is under legal hold")
	ErrLimit      = errors.New("blob exceeds size limit")
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
