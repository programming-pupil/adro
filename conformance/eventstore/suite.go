// Package eventstore provides a reusable conformance suite for EventStore backends.
package eventstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/adro-project/adro/core/event"
	storeport "github.com/adro-project/adro/ports/eventstore"
	"github.com/adro-project/adro/ports/leasestore"
)

type Backend interface {
	storeport.Store
	leasestore.Store
	Close() error
}

type Snapshot struct {
	Sequence int64
	Digest   string
	Payload  []byte
}

type Fixture struct {
	Backend       Backend
	Reopen        func() (Backend, error)
	Advance       func(time.Duration)
	OutboxCount   func(string, string) (int, error)
	Snapshot      func(string, string) (Snapshot, error)
	CorruptMiddle func(string, int64) error
	DeleteTail    func(string, int64) error
}

type Factory func(*testing.T) Fixture

func Run(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("append_read_head_and_atomic_companions", func(t *testing.T) {
		fixture := factory(t)
		request := appendRequest("stream-main", 0, uncommitted("stream-main", "event-1", `{"value":1}`), uncommitted("stream-main", "event-2", `{"value":2}`))
		request.Outbox = []storeport.OutboxMessage{{Topic: "runtime.events", Key: "outbox-1", Payload: []byte("message")}}
		snapshotPayload := []byte("snapshot-v2")
		request.Snapshot = &storeport.SnapshotWrite{Sequence: 2, Digest: digest(snapshotPayload), Payload: snapshotPayload}

		result, err := fixture.Backend.Append(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if result.FirstSequence != 1 || result.LastSequence != 2 || len(result.Events) != 2 {
			t.Fatalf("unexpected append result: %+v", result)
		}
		if result.Events[0].PreviousDigest != "" || result.Events[1].PreviousDigest != result.Events[0].EnvelopeDigest {
			t.Fatalf("event chain is not contiguous: %+v", result.Events)
		}
		read, err := fixture.Backend.Read(context.Background(), "stream-main", 0, 10)
		if err != nil || len(read) != 2 || read[1].EnvelopeDigest != result.Events[1].EnvelopeDigest {
			t.Fatalf("read=%+v err=%v", read, err)
		}
		sequence, head, err := fixture.Backend.Head(context.Background(), "stream-main")
		if err != nil || sequence != 2 || head != result.Events[1].EnvelopeDigest {
			t.Fatalf("head sequence=%d digest=%q err=%v", sequence, head, err)
		}
		if fixture.OutboxCount != nil {
			count, err := fixture.OutboxCount("tenant-1", "stream-main")
			if err != nil || count != 1 {
				t.Fatalf("outbox count=%d err=%v", count, err)
			}
		}
		if fixture.Snapshot != nil {
			stored, err := fixture.Snapshot("tenant-1", "stream-main")
			if err != nil || stored.Sequence != 2 || stored.Digest != request.Snapshot.Digest || string(stored.Payload) != string(snapshotPayload) {
				t.Fatalf("snapshot=%+v err=%v", stored, err)
			}
		}
	})

	t.Run("expected_sequence_cas", func(t *testing.T) {
		fixture := factory(t)
		if _, err := fixture.Backend.Append(context.Background(), appendRequest("stream-cas", 0, uncommitted("stream-cas", "cas-1", `{"value":1}`))); err != nil {
			t.Fatal(err)
		}
		_, err := fixture.Backend.Append(context.Background(), appendRequest("stream-cas", 0, uncommitted("stream-cas", "cas-2", `{"value":2}`)))
		if !errors.Is(err, storeport.ErrConflict) {
			t.Fatalf("stale append error=%v", err)
		}
		sequence, _, err := fixture.Backend.Head(context.Background(), "stream-cas")
		if err != nil || sequence != 1 {
			t.Fatalf("head sequence=%d err=%v", sequence, err)
		}
	})

	t.Run("idempotency_replay_and_conflict", func(t *testing.T) {
		fixture := factory(t)
		request := appendRequest("stream-idempotency", 0, uncommitted("stream-idempotency", "same-key", `{"value":1}`))
		first, err := fixture.Backend.Append(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		second, err := fixture.Backend.Append(context.Background(), request)
		if err != nil || second.Events[0].EventID != first.Events[0].EventID {
			t.Fatalf("replay=%+v err=%v", second, err)
		}
		conflict := appendRequest("stream-idempotency", 0, uncommitted("stream-idempotency", "same-key", `{"value":2}`))
		if _, err := fixture.Backend.Append(context.Background(), conflict); !errors.Is(err, storeport.ErrIdempotencyConflict) {
			t.Fatalf("idempotency conflict error=%v", err)
		}
	})

	t.Run("atomic_rollback", func(t *testing.T) {
		fixture := factory(t)
		first := appendRequest("stream-atomic-a", 0, uncommitted("stream-atomic-a", "atomic-a", `{"value":1}`))
		first.Outbox = []storeport.OutboxMessage{{Topic: "runtime.events", Key: "duplicate", Payload: []byte("first")}}
		if _, err := fixture.Backend.Append(context.Background(), first); err != nil {
			t.Fatal(err)
		}
		second := appendRequest("stream-atomic-b", 0, uncommitted("stream-atomic-b", "atomic-b", `{"value":2}`))
		snapshotPayload := []byte("must-roll-back")
		second.Snapshot = &storeport.SnapshotWrite{Sequence: 1, Digest: digest(snapshotPayload), Payload: snapshotPayload}
		second.Outbox = []storeport.OutboxMessage{{Topic: "runtime.events", Key: "duplicate", Payload: []byte("second")}}
		if _, err := fixture.Backend.Append(context.Background(), second); err == nil {
			t.Fatal("duplicate outbox key unexpectedly committed")
		}
		sequence, _, err := fixture.Backend.Head(context.Background(), "stream-atomic-b")
		if err != nil || sequence != 0 {
			t.Fatalf("rolled back stream head=%d err=%v", sequence, err)
		}
		if fixture.Snapshot != nil {
			if snapshot, err := fixture.Snapshot("tenant-1", "stream-atomic-b"); err == nil {
				t.Fatalf("rolled back snapshot remains visible: %+v", snapshot)
			}
		}
	})

	t.Run("lease_fencing", func(t *testing.T) {
		fixture := factory(t)
		lease, err := fixture.Backend.Acquire(context.Background(), "tenant-1", "stream-lease", "worker-a", time.Minute)
		if err != nil || lease.FencingToken != 1 {
			t.Fatalf("lease=%+v err=%v", lease, err)
		}
		if _, err := fixture.Backend.Acquire(context.Background(), "tenant-1", "stream-lease", "worker-b", time.Minute); !errors.Is(err, leasestore.ErrBusy) {
			t.Fatalf("competing acquire error=%v", err)
		}
		request := appendRequest("stream-lease", 0, uncommitted("stream-lease", "lease-1", `{"value":1}`))
		request.Lease = &storeport.LeaseAssertion{Owner: lease.Owner, FencingToken: lease.FencingToken}
		if _, err := fixture.Backend.Append(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		fixture.Advance(2 * time.Minute)
		newLease, err := fixture.Backend.Acquire(context.Background(), "tenant-1", "stream-lease", "worker-b", time.Minute)
		if err != nil || newLease.FencingToken <= lease.FencingToken {
			t.Fatalf("takeover lease=%+v err=%v", newLease, err)
		}
		stale := appendRequest("stream-lease", 1, uncommitted("stream-lease", "lease-stale", `{"value":2}`))
		stale.Lease = &storeport.LeaseAssertion{Owner: lease.Owner, FencingToken: lease.FencingToken}
		if _, err := fixture.Backend.Append(context.Background(), stale); !errors.Is(err, storeport.ErrLeaseLost) {
			t.Fatalf("stale lease append error=%v", err)
		}
		fresh := appendRequest("stream-lease", 1, uncommitted("stream-lease", "lease-2", `{"value":2}`))
		fresh.Lease = &storeport.LeaseAssertion{Owner: newLease.Owner, FencingToken: newLease.FencingToken}
		if _, err := fixture.Backend.Append(context.Background(), fresh); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.Backend.Renew(context.Background(), lease, time.Minute); !errors.Is(err, leasestore.ErrLost) {
			t.Fatalf("stale renew error=%v", err)
		}
		if err := fixture.Backend.Release(context.Background(), newLease); err != nil {
			t.Fatal(err)
		}
		reacquired, err := fixture.Backend.Acquire(context.Background(), "tenant-1", "stream-lease", "worker-b", time.Minute)
		if err != nil || reacquired.FencingToken <= newLease.FencingToken {
			t.Fatalf("reacquired lease=%+v err=%v", reacquired, err)
		}
		if _, err := fixture.Backend.Renew(context.Background(), newLease, time.Minute); !errors.Is(err, leasestore.ErrLost) {
			t.Fatalf("released lease renewed with error=%v", err)
		}
	})

	t.Run("concurrent_append_has_one_winner", func(t *testing.T) {
		fixture := factory(t)
		const writers = 12
		var successes, conflicts int
		var unexpected []error
		var mutex sync.Mutex
		var wait sync.WaitGroup
		for index := 0; index < writers; index++ {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				key := fmt.Sprintf("concurrent-%d", index)
				_, err := fixture.Backend.Append(context.Background(), appendRequest("stream-concurrent", 0, uncommitted("stream-concurrent", key, fmt.Sprintf(`{"writer":%d}`, index))))
				mutex.Lock()
				defer mutex.Unlock()
				switch {
				case err == nil:
					successes++
				case errors.Is(err, storeport.ErrConflict):
					conflicts++
				default:
					unexpected = append(unexpected, err)
				}
			}(index)
		}
		wait.Wait()
		if successes != 1 || conflicts != writers-1 || len(unexpected) != 0 {
			t.Fatalf("successes=%d conflicts=%d unexpected=%v", successes, conflicts, unexpected)
		}
	})

	t.Run("restart_and_subscription", func(t *testing.T) {
		fixture := factory(t)
		if _, err := fixture.Backend.Append(context.Background(), appendRequest("stream-restart", 0, uncommitted("stream-restart", "restart-1", `{"value":1}`))); err != nil {
			t.Fatal(err)
		}
		if err := fixture.Backend.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := fixture.Reopen()
		if err != nil {
			t.Fatal(err)
		}
		read, err := reopened.Read(context.Background(), "stream-restart", 0, 10)
		if err != nil || len(read) != 1 {
			t.Fatalf("restart read=%+v err=%v", read, err)
		}
		subscription, err := reopened.Subscribe(context.Background(), storeport.Subscription{TenantID: "tenant-1", StreamID: "stream-restart", After: 1, BufferSize: 2})
		if err != nil {
			t.Fatal(err)
		}
		defer subscription.Close()
		if _, err := reopened.Append(context.Background(), appendRequest("stream-restart", 1, uncommitted("stream-restart", "restart-2", `{"value":2}`))); err != nil {
			t.Fatal(err)
		}
		select {
		case received := <-subscription.Events():
			if received.Sequence != 2 {
				t.Fatalf("subscription sequence=%d", received.Sequence)
			}
		case err := <-subscription.Errors():
			t.Fatalf("subscription error=%v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("subscription did not deliver committed event")
		}
	})

	t.Run("tenant_isolation", func(t *testing.T) {
		fixture := factory(t)
		if _, err := fixture.Backend.Append(context.Background(), appendRequest("stream-tenant", 0, uncommitted("stream-tenant", "tenant-a", `{"value":1}`))); err != nil {
			t.Fatal(err)
		}
		other := appendRequest("stream-tenant", 1, uncommittedForTenant("tenant-2", "workspace-2", "stream-tenant", "tenant-b", `{"value":2}`))
		if _, err := fixture.Backend.Append(context.Background(), other); err == nil {
			t.Fatal("cross-tenant append unexpectedly succeeded")
		}
		if _, err := fixture.Backend.Subscribe(context.Background(), storeport.Subscription{TenantID: "tenant-2", StreamID: "stream-tenant", BufferSize: 1}); err == nil {
			t.Fatal("cross-tenant subscription unexpectedly succeeded")
		}
	})

	t.Run("corruption_fails_closed", func(t *testing.T) {
		fixture := factory(t)
		if fixture.CorruptMiddle == nil || fixture.DeleteTail == nil {
			t.Skip("backend did not provide fault hooks")
		}
		request := appendRequest("stream-corrupt", 0,
			uncommitted("stream-corrupt", "corrupt-1", `{"value":1}`),
			uncommitted("stream-corrupt", "corrupt-2", `{"value":2}`),
			uncommitted("stream-corrupt", "corrupt-3", `{"value":3}`),
		)
		if _, err := fixture.Backend.Append(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		if err := fixture.CorruptMiddle("stream-corrupt", 2); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.Backend.Read(context.Background(), "stream-corrupt", 0, 10); !errors.Is(err, storeport.ErrCorrupt) {
			t.Fatalf("middle corruption error=%v", err)
		}

		fixture = factory(t)
		if _, err := fixture.Backend.Append(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		if err := fixture.DeleteTail("stream-corrupt", 3); err != nil {
			t.Fatal(err)
		}
		if _, _, err := fixture.Backend.Head(context.Background(), "stream-corrupt"); !errors.Is(err, storeport.ErrCorrupt) {
			t.Fatalf("torn tail error=%v", err)
		}
	})
}

func appendRequest(streamID string, expected int64, events ...event.Uncommitted) storeport.AppendRequest {
	return storeport.AppendRequest{StreamID: streamID, ExpectedSequence: expected, Events: events}
}

func uncommitted(streamID, key, payload string) event.Uncommitted {
	return uncommittedForTenant("tenant-1", "workspace-1", streamID, key, payload)
}

func uncommittedForTenant(tenantID, workspaceID, streamID, key, payload string) event.Uncommitted {
	return event.Uncommitted{
		StreamID: streamID, EventType: "conformance.event", TenantID: tenantID, WorkspaceID: workspaceID,
		Actor: event.Actor{Type: "test", ID: "actor-1"}, CorrelationID: "correlation-1",
		IdempotencyKey: key, OccurredAt: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC),
		Classification: "internal", Payload: json.RawMessage(payload),
	}
}

func digest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
