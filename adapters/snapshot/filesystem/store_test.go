package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adro-project/adro/ports/snapshot"
)

func testSnapshot(seq int64, payload string) snapshot.Snapshot {
	digest := sha256.Sum256([]byte(payload))
	return snapshot.Snapshot{TenantID: "tenant-a", StreamID: "stream-a", Sequence: seq, SchemaVersion: 1, EncodingVersion: 1, HashAlgorithm: "sha256", HashVersion: 1, Digest: hex.EncodeToString(digest[:]), Payload: []byte(payload), CreatedAt: time.Date(2026, 9, 19, 6, 0, 0, 0, time.UTC)}
}

func TestSnapshotStoreCASIdempotenceAndRestart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "snapshots")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	value := testSnapshot(1, "state-1")
	if err := store.Put(context.Background(), value, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), value, 0); err != nil {
		t.Fatalf("idempotent put: %v", err)
	}
	if err := store.Put(context.Background(), testSnapshot(2, "state-2"), 0); !errors.Is(err, snapshot.ErrConflict) {
		t.Fatalf("stale CAS err=%v", err)
	}
	value2 := testSnapshot(2, "state-2")
	if err := store.Put(context.Background(), value2, 1); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(context.Background(), "stream-a")
	if err != nil || got.Sequence != 2 || string(got.Payload) != "state-2" {
		t.Fatalf("snapshot=%+v err=%v", got, err)
	}
}

func TestSnapshotStoreFailsClosedOnCorruption(t *testing.T) {
	root := filepath.Join(t.TempDir(), "snapshots")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	value := testSnapshot(1, "state")
	if err := store.Put(context.Background(), value, 0); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(root, "*.json"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("snapshot paths=%v err=%v", matches, err)
	}
	if err := os.WriteFile(matches[0], []byte(`{"sequence":1}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), "stream-a"); !errors.Is(err, snapshot.ErrCorrupt) {
		t.Fatalf("corrupt snapshot err=%v", err)
	}
}
