// Package leasestore defines write ownership and fencing for durable streams.
package leasestore

import (
	"context"
	"errors"
	"time"
)

var (
	ErrBusy = errors.New("lease is held by another owner")
	ErrLost = errors.New("lease is no longer owned")
)

type Lease struct {
	TenantID     string
	StreamID     string
	Owner        string
	FencingToken int64
	ExpiresAt    time.Time
}

type Store interface {
	Acquire(context.Context, string, string, string, time.Duration) (Lease, error)
	Renew(context.Context, Lease, time.Duration) (Lease, error)
	Release(context.Context, Lease) error
}
