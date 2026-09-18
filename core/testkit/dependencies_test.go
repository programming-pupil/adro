package testkit

import (
	"context"
	"testing"
	"time"
)

func TestDependenciesAreDeterministic(t *testing.T) {
	clock := NewManualClock(time.Date(2026, 9, 17, 12, 0, 0, 0, time.FixedZone("offset", 8*60*60)))
	clock.Advance(time.Second)
	if got := clock.Now(); got.Location() != time.UTC || got.Second() != 1 {
		t.Fatalf("clock=%s", got)
	}
	ids := &SequenceIDs{}
	if got := ids.NewID("event"); got != "event-000001" {
		t.Fatalf("id=%q", got)
	}
	random := NewSequenceRandom(7, 9)
	if random.Uint64() != 7 || random.Uint64() != 9 || random.Uint64() != 7 {
		t.Fatal("random sequence did not repeat deterministically")
	}
	sleeper := &RecordingSleeper{}
	if err := sleeper.Sleep(context.Background(), 3*time.Second); err != nil || len(sleeper.Durations) != 1 {
		t.Fatalf("sleep err=%v durations=%v", err, sleeper.Durations)
	}
}
