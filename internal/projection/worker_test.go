package projection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	storeevent "github.com/adro-project/adro/adapters/eventstore/sqlite"
	storeprojection "github.com/adro-project/adro/adapters/projection/memory"
	coreevent "github.com/adro-project/adro/core/event"
	"github.com/adro-project/adro/core/testkit"
	eventstoreport "github.com/adro-project/adro/ports/eventstore"
	projectionport "github.com/adro-project/adro/ports/projection"
	"github.com/adro-project/adro/ports/scope"
)

type workerFixture struct {
	worker *Worker
	events *storeevent.Store
	store  *storeprojection.Store
	ctx    context.Context
}

func newWorkerFixture(t *testing.T) workerFixture {
	t.Helper()
	clock := testkit.NewManualClock(time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC))
	events, err := storeevent.Open(filepath.Join(t.TempDir(), "events.db"), storeevent.Options{Clock: clock, IDs: &testkit.SequenceIDs{}, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = events.Close() })
	store := storeprojection.New(clock.Now)
	worker, err := NewWorker(Config{
		Events: events, Store: store, Offsets: store, Atomic: store, Clock: clock,
		TenantID: "tenant-a", StreamID: "stream-a", ProjectionName: "sessions",
		PageSize: 2, BufferSize: 4, CASRetries: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return workerFixture{worker: worker, events: events, store: store, ctx: scope.WithTenant(context.Background(), "tenant-a")}
}

func appendSessionEvent(t *testing.T, events eventstoreport.Store, sequence int64, key, value string) {
	t.Helper()
	payload, err := json.Marshal(struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}{Key: key, Value: value})
	if err != nil {
		t.Fatal(err)
	}
	_, err = events.Append(context.Background(), eventstoreport.AppendRequest{
		StreamID:         "stream-a",
		ExpectedSequence: sequence,
		Events: []coreevent.Uncommitted{{
			StreamID: "stream-a", EventType: "session.updated", TenantID: "tenant-a", WorkspaceID: "workspace-a",
			Actor: coreevent.Actor{Type: "system", ID: "worker-test"}, CorrelationID: fmt.Sprintf("corr-%d", sequence+1),
			IdempotencyKey: fmt.Sprintf("event-%d", sequence+1), OccurredAt: time.Date(2026, 9, 20, 0, 0, int(sequence+1), 0, time.UTC),
			Classification: "internal", Payload: payload,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func sessionProjector(_ context.Context, envelope coreevent.Envelope) ([]Mutation, error) {
	var payload struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return nil, err
	}
	return []Mutation{{Key: payload.Key, Payload: []byte(payload.Value)}}, nil
}

func TestWorkerRebuildPersistsOffsetAndIsIdempotent(t *testing.T) {
	fixture := newWorkerFixture(t)
	appendSessionEvent(t, fixture.events, 0, "session-1", "one")
	appendSessionEvent(t, fixture.events, 1, "session-1", "two")

	report, err := fixture.worker.Rebuild(fixture.ctx, sessionProjector)
	if err != nil {
		t.Fatal(err)
	}
	if report.Events != 2 || report.UpdatedRecords != 2 || report.LastSequence != 2 {
		t.Fatalf("first report=%+v", report)
	}
	item, err := fixture.store.Get(fixture.ctx, "tenant-a", "sessions", "session-1")
	if err != nil || item.Version != 2 || item.SourceSequence != 2 || string(item.Payload) != "two" {
		t.Fatalf("record=%+v err=%v", item, err)
	}
	offset, err := fixture.store.GetOffset(fixture.ctx, "tenant-a", "sessions", "stream-a")
	if err != nil || offset.LastSequence != 2 || offset.ProjectionDigest == "" {
		t.Fatalf("offset=%+v err=%v", offset, err)
	}

	replay, err := fixture.worker.Rebuild(fixture.ctx, sessionProjector)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Events != 2 || replay.UpdatedRecords != 0 || replay.SkippedMutations != 2 || replay.LastSequence != 2 {
		t.Fatalf("replay report=%+v", replay)
	}
}

func TestWorkerRebuildRejectsDeterministicDrift(t *testing.T) {
	fixture := newWorkerFixture(t)
	appendSessionEvent(t, fixture.events, 0, "session-1", "one")
	if _, err := fixture.worker.Rebuild(fixture.ctx, sessionProjector); err != nil {
		t.Fatal(err)
	}
	drifted := func(context.Context, coreevent.Envelope) ([]Mutation, error) {
		return []Mutation{{Key: "session-1", Payload: []byte("different")}}, nil
	}
	if _, err := fixture.worker.Rebuild(fixture.ctx, drifted); !errors.Is(err, ErrDiverged) {
		t.Fatalf("drift error=%v", err)
	}
}

func TestWorkerRunConsumesSubscriptionAfterReplay(t *testing.T) {
	fixture := newWorkerFixture(t)
	appendSessionEvent(t, fixture.events, 0, "session-1", "one")
	ctx, cancel := context.WithCancel(fixture.ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- fixture.worker.Run(ctx, sessionProjector)
	}()
	waitForProjection(t, fixture.store, fixture.ctx, "one")
	appendSessionEvent(t, fixture.events, 1, "session-1", "two")
	waitForProjection(t, fixture.store, fixture.ctx, "two")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run error=%v", err)
	}
}

func TestWorkerFailsClosedWithoutScopeAndOnOffsetDigestMismatch(t *testing.T) {
	fixture := newWorkerFixture(t)
	appendSessionEvent(t, fixture.events, 0, "session-1", "one")
	if _, err := fixture.worker.Rebuild(context.Background(), sessionProjector); !errors.Is(err, scope.ErrMissingTenant) {
		t.Fatalf("unscoped rebuild error=%v", err)
	}
	if _, err := fixture.worker.Rebuild(fixture.ctx, sessionProjector); err != nil {
		t.Fatal(err)
	}
	offset, err := fixture.store.GetOffset(fixture.ctx, "tenant-a", "sessions", "stream-a")
	if err != nil || offset.LastSequence != 1 {
		t.Fatal(err)
	}
	corrupted := &corruptedOffsetStore{OffsetStore: fixture.store}
	corruptWorker, err := NewWorker(Config{
		Events: fixture.events, Store: fixture.store, Offsets: corrupted, Clock: testkit.NewManualClock(time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)),
		TenantID: "tenant-a", StreamID: "stream-a", ProjectionName: "sessions",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := corruptWorker.Run(fixture.ctx, sessionProjector); !errors.Is(err, ErrDiverged) {
		t.Fatalf("offset mismatch error=%v", err)
	}
}

func TestWorkerRejectsDuplicateMutation(t *testing.T) {
	fixture := newWorkerFixture(t)
	appendSessionEvent(t, fixture.events, 0, "session-1", "one")
	duplicate := func(context.Context, coreevent.Envelope) ([]Mutation, error) {
		return []Mutation{{Key: "session-1", Payload: []byte("one")}, {Key: "session-1", Payload: []byte("two")}}, nil
	}
	if _, err := fixture.worker.Rebuild(fixture.ctx, duplicate); !errors.Is(err, ErrDuplicateMutation) {
		t.Fatalf("duplicate mutation error=%v", err)
	}
}

func TestWorkerRunRejectsUnfillableSubscriptionGap(t *testing.T) {
	fixture := newWorkerFixture(t)
	appendSessionEvent(t, fixture.events, 0, "session-1", "one")
	appendSessionEvent(t, fixture.events, 1, "session-2", "two")
	appendSessionEvent(t, fixture.events, 2, "session-3", "three")
	worker, err := NewWorker(Config{
		Events: &gappedEventStore{base: fixture.events}, Store: fixture.store, Offsets: fixture.store,
		Clock:    testkit.NewManualClock(time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)),
		TenantID: "tenant-a", StreamID: "stream-a", ProjectionName: "sessions", PageSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(fixture.ctx, time.Second)
	defer cancel()
	if err := worker.Run(ctx, sessionProjector); !errors.Is(err, ErrSequenceGap) {
		t.Fatalf("unfillable subscription gap error=%v", err)
	}
}

func waitForProjection(t *testing.T, store projectionport.Store, ctx context.Context, want string) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		item, err := store.Get(ctx, "tenant-a", "sessions", "session-1")
		if err == nil && string(item.Payload) == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("projection did not reach %q; last item=%+v err=%v", want, item, err)
		case <-ticker.C:
		}
	}
}

type corruptedOffsetStore struct {
	projectionport.OffsetStore
}

func (s *corruptedOffsetStore) GetOffset(ctx context.Context, tenantID, projectionName, partitionID string) (projectionport.Offset, error) {
	offset, err := s.OffsetStore.GetOffset(ctx, tenantID, projectionName, partitionID)
	if err == nil {
		offset.ProjectionDigest = "0000000000000000000000000000000000000000000000000000000000000000"
	}
	return offset, err
}

type gappedEventStore struct {
	base *storeevent.Store
}

func (s *gappedEventStore) Append(ctx context.Context, request eventstoreport.AppendRequest) (eventstoreport.AppendResult, error) {
	return s.base.Append(ctx, request)
}

func (s *gappedEventStore) Read(ctx context.Context, streamID string, after int64, limit int) ([]coreevent.Envelope, error) {
	if after == 1 {
		return []coreevent.Envelope{}, nil
	}
	return s.base.Read(ctx, streamID, after, limit)
}

func (s *gappedEventStore) Head(ctx context.Context, streamID string) (int64, string, error) {
	return s.base.Head(ctx, streamID)
}

func (s *gappedEventStore) Subscribe(ctx context.Context, request eventstoreport.Subscription) (eventstoreport.EventSubscription, error) {
	events, err := s.base.Read(ctx, request.StreamID, 2, 1)
	if err != nil {
		return nil, err
	}
	return newStaticSubscription(events), nil
}

type staticSubscription struct {
	events    chan coreevent.Envelope
	errors    chan error
	closeOnce sync.Once
}

func newStaticSubscription(events []coreevent.Envelope) *staticSubscription {
	stream := make(chan coreevent.Envelope, len(events))
	for _, event := range events {
		stream <- event
	}
	close(stream)
	errors := make(chan error)
	close(errors)
	return &staticSubscription{events: stream, errors: errors}
}

func (s *staticSubscription) Events() <-chan coreevent.Envelope { return s.events }
func (s *staticSubscription) Errors() <-chan error              { return s.errors }
func (s *staticSubscription) Close() error {
	s.closeOnce.Do(func() {})
	return nil
}

func TestWorkerAtomicBatchFailureRollsBackRecordsAndOffset(t *testing.T) {
	fixture := newWorkerFixture(t)
	if err := fixture.store.Put(fixture.ctx, projectionport.Record{
		TenantID: "tenant-a", Projection: "sessions", Key: "session-2", Version: 1,
		SourceStream: "stream-b", SourceSequence: 1, Payload: []byte("foreign"),
	}, 0); err != nil {
		t.Fatal(err)
	}
	appendSessionEvent(t, fixture.events, 0, "session-1", "one")
	appendSessionEvent(t, fixture.events, 1, "session-2", "two")
	if err := fixture.worker.Run(fixture.ctx, sessionProjector); !errors.Is(err, ErrDiverged) {
		t.Fatalf("atomic batch error=%v", err)
	}
	if _, err := fixture.store.Get(fixture.ctx, "tenant-a", "sessions", "session-1"); !errors.Is(err, projectionport.ErrNotFound) {
		t.Fatalf("rolled back record error=%v", err)
	}
	if _, err := fixture.store.GetOffset(fixture.ctx, "tenant-a", "sessions", "stream-a"); !errors.Is(err, projectionport.ErrOffsetNotFound) {
		t.Fatalf("rolled back offset error=%v", err)
	}
}
