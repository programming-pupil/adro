package sqlite_test

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/adro-project/adro/adapters/eventstore/sqlite"
	storeconformance "github.com/adro-project/adro/conformance/eventstore"
	"github.com/adro-project/adro/core/testkit"
	_ "modernc.org/sqlite"
)

func TestSQLiteEventStoreConformance(t *testing.T) {
	storeconformance.Run(t, newFixture)
}

func newFixture(t *testing.T) storeconformance.Fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	clock := testkit.NewManualClock(time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC))
	ids := &lockedIDs{}
	options := sqlite.Options{Clock: clock, IDs: ids, PollInterval: time.Millisecond}
	var opened []*sqlite.Store
	open := func() (*sqlite.Store, error) {
		store, err := sqlite.Open(path, options)
		if err == nil {
			opened = append(opened, store)
		}
		return store, err
	}
	store, err := open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, store := range opened {
			_ = store.Close()
		}
	})
	return storeconformance.Fixture{
		Backend: store,
		Reopen: func() (storeconformance.Backend, error) {
			return open()
		},
		Advance: clock.Advance,
		OutboxCount: func(tenantID, streamID string) (int, error) {
			return countRows(path, `SELECT count(*) FROM event_outbox WHERE tenant_id=? AND stream_id=?`, tenantID, streamID)
		},
		Snapshot: func(tenantID, streamID string) (storeconformance.Snapshot, error) {
			db, err := sql.Open("sqlite", path)
			if err != nil {
				return storeconformance.Snapshot{}, err
			}
			defer db.Close()
			var snapshot storeconformance.Snapshot
			err = db.QueryRow(`SELECT sequence, digest, payload FROM event_snapshots WHERE tenant_id=? AND stream_id=?`, tenantID, streamID).
				Scan(&snapshot.Sequence, &snapshot.Digest, &snapshot.Payload)
			return snapshot, err
		},
		CorruptMiddle: func(streamID string, sequence int64) error {
			return exec(path, `UPDATE event_records SET envelope_json=? WHERE stream_id=? AND sequence=?`, []byte(`{"corrupt":true}`), streamID, sequence)
		},
		DeleteTail: func(streamID string, sequence int64) error {
			return exec(path, `DELETE FROM event_records WHERE stream_id=? AND sequence=?`, streamID, sequence)
		},
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

func countRows(path, query string, args ...any) (int, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var count int
	err = db.QueryRow(query, args...).Scan(&count)
	return count, err
}

func exec(path, query string, args ...any) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(query, args...)
	return err
}
