// Package projection defines a rebuildable read-model store. Projection data
// is never authoritative; each record names the source stream and sequence it
// was derived from so a worker can detect stale writes and rebuild from zero.
package projection

import (
	"context"
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

type Store interface {
	Get(context.Context, string, string, string) (Record, error)
	Put(context.Context, Record, int64) error
	Delete(context.Context, string, string, string, int64) error
	List(context.Context, string, string) ([]Record, error)
}
