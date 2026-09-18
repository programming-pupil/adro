// Package core contains deterministic dependencies shared by reducers.
package core

import (
	"context"
	"errors"
	"time"
)

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewID(namespace string) string
}

type Random interface {
	Uint64() uint64
}

type Sleeper interface {
	Sleep(context.Context, time.Duration) error
}

type BackoffPolicy interface {
	Next(attempt int, class string, random uint64) time.Duration
}

type Dependencies struct {
	Clock   Clock
	IDs     IDGenerator
	Random  Random
	Sleeper Sleeper
	Backoff BackoffPolicy
}

func (d Dependencies) Validate() error {
	if d.Clock == nil || d.IDs == nil || d.Random == nil || d.Sleeper == nil || d.Backoff == nil {
		return errors.New("clock, ids, random, sleeper and backoff dependencies are required")
	}
	return nil
}
