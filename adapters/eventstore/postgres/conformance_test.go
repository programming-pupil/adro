package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	storecontract "github.com/adro-project/adro/adapters/eventstore/internal/contract"
	storeconformance "github.com/adro-project/adro/conformance/eventstore"
	"github.com/adro-project/adro/core/event"
	"github.com/adro-project/adro/core/testkit"
	"github.com/adro-project/adro/ports/eventstore"
	"github.com/lib/pq"
)

func TestPostgresEventStoreConformance(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("ADRO_POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("set ADRO_POSTGRES_TEST_DSN to run PostgreSQL EventStore conformance")
	}
	storeconformance.Run(t, func(t *testing.T) storeconformance.Fixture {
		return newFixture(t, dsn)
	})
	seedBackupEvidence(t, dsn)
}

func TestPostgresEventStoreConformanceLeaseWaitUsesFreshDatabaseTime(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("ADRO_POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("set ADRO_POSTGRES_TEST_DSN to run PostgreSQL EventStore conformance")
	}
	store, err := Open(dsn, Options{
		Clock: testkit.NewManualClock(time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)),
		IDs:   &lockedIDs{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.db.Exec(`TRUNCATE runtime_event_outbox, event_snapshots, event_records, event_streams, event_leases RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	lease, err := store.Acquire(context.Background(), "tenant-lock-time", "stream-lock-time", "worker-a", 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	blocker, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	var marker int
	if err := blocker.QueryRow(`SELECT 1 FROM event_leases WHERE tenant_id=$1 AND stream_id=$2 FOR UPDATE`, lease.TenantID, lease.StreamID).Scan(&marker); err != nil {
		t.Fatal(err)
	}

	type acquireResult struct {
		owner string
		token int64
		err   error
	}
	result := make(chan acquireResult, 1)
	go func() {
		acquired, err := store.Acquire(context.Background(), lease.TenantID, lease.StreamID, "worker-b", time.Second)
		result <- acquireResult{owner: acquired.Owner, token: acquired.FencingToken, err: err}
	}()
	waitForBlockedLeaseInsert(t, store.db)
	if remaining := time.Until(lease.ExpiresAt) + 50*time.Millisecond; remaining > 0 {
		time.Sleep(remaining)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}

	select {
	case acquired := <-result:
		if acquired.err != nil {
			t.Fatalf("expired lease takeover after lock wait: %v", acquired.err)
		}
		if acquired.owner != "worker-b" || acquired.token <= lease.FencingToken {
			t.Fatalf("unexpected takeover lease: owner=%q token=%d", acquired.owner, acquired.token)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("lease takeover remained blocked after row lock release")
	}
}

func TestPostgresEventStoreConformanceTenantForeignKeys(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("ADRO_POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("set ADRO_POSTGRES_TEST_DSN to run PostgreSQL EventStore conformance")
	}
	store, err := Open(dsn, Options{
		Clock: testkit.NewManualClock(time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)),
		IDs:   &lockedIDs{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.db.Exec(`TRUNCATE runtime_event_outbox, event_snapshots, event_records, event_streams, event_leases RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	request := eventstore.AppendRequest{
		StreamID: "stream-fk",
		Events: []event.Uncommitted{{
			StreamID: "stream-fk", EventType: "conformance.event", TenantID: "tenant-1", WorkspaceID: "workspace-1",
			Actor: event.Actor{Type: "test", ID: "actor-1"}, CorrelationID: "correlation-1",
			IdempotencyKey: "fk-event", OccurredAt: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC),
			Classification: "internal", Payload: json.RawMessage(`{"value":1}`),
		}},
	}
	if _, err := store.Append(context.Background(), request); err != nil {
		t.Fatal(err)
	}

	assertForeignKeyViolation(t, store.db, `INSERT INTO event_records
		(tenant_id, stream_id, sequence, event_id, event_type, idempotency_key, append_digest,
		 payload_digest, previous_digest, envelope_digest, envelope_json, committed_at_us)
		SELECT 'tenant-2', stream_id, 2, 'event-fk-copy', event_type, 'fk-copy', append_digest,
		       payload_digest, envelope_digest, envelope_digest, envelope_json, committed_at_us
		FROM event_records WHERE tenant_id='tenant-1' AND stream_id='stream-fk' AND sequence=1`)
	assertForeignKeyViolation(t, store.db, `INSERT INTO runtime_event_outbox
		(tenant_id, stream_id, sequence, ordinal, topic, message_key, payload, created_at_us)
		VALUES ('tenant-2', 'stream-fk', 1, 0, 'fk.topic', 'fk-key', 'payload', 1)`)
	assertForeignKeyViolation(t, store.db, `INSERT INTO event_snapshots
		(tenant_id, stream_id, sequence, digest, payload, updated_at_us)
		VALUES ('tenant-2', 'stream-fk', 1, 'digest', 'payload', 1)`)
}

func assertForeignKeyViolation(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	_, err := db.Exec(statement)
	var pgErr *pq.Error
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("expected PostgreSQL foreign-key violation, got %v", err)
	}
}

func waitForBlockedLeaseInsert(t *testing.T, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		err := db.QueryRow(`SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			WHERE pid <> pg_backend_pid()
			  AND datname = current_database()
			  AND wait_event_type = 'Lock'
			  AND query LIKE '%event_leases%'
		)`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("lease acquire did not block on the held row lock")
}

func newFixture(t *testing.T, dsn string) storeconformance.Fixture {
	t.Helper()
	clock := testkit.NewManualClock(time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC))
	ids := &lockedIDs{}
	options := Options{Clock: clock, IDs: ids, PollInterval: time.Millisecond}
	var opened []*Store
	open := func() (*Store, error) {
		store, err := Open(dsn, options)
		if err == nil {
			opened = append(opened, store)
		}
		return store, err
	}
	store, err := open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`TRUNCATE runtime_event_outbox, event_snapshots, event_records, event_streams, event_leases RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, store := range opened {
			_ = store.Close()
		}
		db, err := sql.Open("postgres", dsn)
		if err == nil {
			_, _ = db.Exec(`TRUNCATE runtime_event_outbox, event_snapshots, event_records, event_streams, event_leases RESTART IDENTITY CASCADE`)
			_ = db.Close()
		}
	})
	return storeconformance.Fixture{
		Backend: store,
		Reopen: func() (storeconformance.Backend, error) {
			return open()
		},
		Advance: func(time.Duration) {
			if _, err := store.db.Exec(`UPDATE event_leases SET expires_at_us=0`); err != nil {
				t.Fatal(err)
			}
		},
		OutboxCount: func(tenantID, streamID string) (int, error) {
			return countRows(store.db, `SELECT count(*) FROM runtime_event_outbox WHERE tenant_id=$1 AND stream_id=$2`, tenantID, streamID)
		},
		Snapshot: func(tenantID, streamID string) (storeconformance.Snapshot, error) {
			var snapshot storeconformance.Snapshot
			err := store.db.QueryRow(`SELECT sequence, digest, payload FROM event_snapshots WHERE tenant_id=$1 AND stream_id=$2`, tenantID, streamID).
				Scan(&snapshot.Sequence, &snapshot.Digest, &snapshot.Payload)
			return snapshot, err
		},
		CorruptMiddle: func(streamID string, sequence int64) error {
			_, err := store.db.Exec(`UPDATE event_records SET envelope_json=$1 WHERE stream_id=$2 AND sequence=$3`, []byte(`{"corrupt":true}`), streamID, sequence)
			return err
		},
		DeleteTail: func(streamID string, sequence int64) error {
			_, err := store.db.Exec(`DELETE FROM event_records WHERE stream_id=$1 AND sequence=$2`, streamID, sequence)
			return err
		},
	}
}

func seedBackupEvidence(t *testing.T, dsn string) {
	t.Helper()
	clock := testkit.NewManualClock(time.Date(2026, 9, 17, 13, 0, 0, 0, time.UTC))
	store, err := Open(dsn, Options{Clock: clock, IDs: &lockedIDs{}, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	lease, err := store.Acquire(context.Background(), "tenant-backup", "stream-backup", "worker-backup", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	snapshotPayload := []byte("backup-snapshot")
	request := eventstore.AppendRequest{
		StreamID: "stream-backup", ExpectedSequence: 0,
		Events: []event.Uncommitted{{
			StreamID: "stream-backup", EventType: "backup.evidence", TenantID: "tenant-backup", WorkspaceID: "workspace-backup",
			Actor: event.Actor{Type: "test", ID: "actor-backup"}, CorrelationID: "correlation-backup",
			IdempotencyKey: "backup-event", OccurredAt: clock.Now(), Classification: "internal",
			Payload: json.RawMessage(`{"evidence":"backup-restore"}`),
		}},
		Outbox:   []eventstore.OutboxMessage{{Topic: "backup.evidence", Key: "backup-outbox", Payload: []byte("backup-message")}},
		Snapshot: &eventstore.SnapshotWrite{Sequence: 1, Digest: storecontract.DigestBytes(snapshotPayload), Payload: snapshotPayload},
		Lease:    &eventstore.LeaseAssertion{Owner: lease.Owner, FencingToken: lease.FencingToken},
	}
	if _, err := store.Append(context.Background(), request); err != nil {
		t.Fatal(err)
	}
}

type lockedIDs struct {
	mu   sync.Mutex
	next int
}

func (g *lockedIDs) NewID(namespace string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	return namespace + "-" + time.Date(2026, 1, 1, 0, 0, g.next, 0, time.UTC).Format("150405")
}

func countRows(db *sql.DB, query string, args ...any) (int, error) {
	var count int
	err := db.QueryRowContext(context.Background(), query, args...).Scan(&count)
	return count, err
}
