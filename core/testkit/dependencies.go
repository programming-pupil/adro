// Package testkit provides deterministic runtime dependencies for tests.
package testkit

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewManualClock(now time.Time) *ManualClock {
	return &ManualClock{now: now.UTC()}
}

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) Advance(delta time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(delta)
}

type SequenceIDs struct {
	mu   sync.Mutex
	next uint64
}

func (g *SequenceIDs) NewID(namespace string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	return fmt.Sprintf("%s-%06d", namespace, g.next)
}

type SequenceRandom struct {
	mu     sync.Mutex
	values []uint64
	next   int
}

func NewSequenceRandom(values ...uint64) *SequenceRandom {
	return &SequenceRandom{values: append([]uint64(nil), values...)}
}

func (r *SequenceRandom) Uint64() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.values) == 0 {
		return 0
	}
	value := r.values[r.next%len(r.values)]
	r.next++
	return value
}

type RecordingSleeper struct {
	mu        sync.Mutex
	Durations []time.Duration
}

func (s *RecordingSleeper) Sleep(ctx context.Context, duration time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.Durations = append(s.Durations, duration)
	s.mu.Unlock()
	return nil
}

type FixedBackoff time.Duration

func (b FixedBackoff) Next(int, string, uint64) time.Duration { return time.Duration(b) }
