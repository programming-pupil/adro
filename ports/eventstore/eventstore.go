// Package eventstore defines the authoritative event persistence port.
package eventstore

import (
	"context"
	"errors"

	"github.com/adro-project/adro/core/event"
)

var (
	ErrConflict  = errors.New("event stream expected sequence conflict")
	ErrLeaseLost = errors.New("event stream lease assertion failed")
)

type OutboxMessage struct {
	Topic   string
	Key     string
	Payload []byte
}

type SnapshotWrite struct {
	Sequence int64
	Digest   string
	Payload  []byte
}

type LeaseAssertion struct {
	Owner        string
	FencingToken int64
}

type AppendRequest struct {
	StreamID         string
	ExpectedSequence int64
	Events           []event.Uncommitted
	Outbox           []OutboxMessage
	Snapshot         *SnapshotWrite
	Lease            *LeaseAssertion
}

type AppendResult struct {
	FirstSequence int64
	LastSequence  int64
	Events        []event.Envelope
}

type Subscription struct {
	TenantID   string
	StreamID   string
	After      int64
	BufferSize int
}

type EventSubscription interface {
	Events() <-chan event.Envelope
	Errors() <-chan error
	Close() error
}

type Store interface {
	Append(context.Context, AppendRequest) (AppendResult, error)
	Read(context.Context, string, int64, int) ([]event.Envelope, error)
	Head(context.Context, string) (int64, string, error)
	Subscribe(context.Context, Subscription) (EventSubscription, error)
}
