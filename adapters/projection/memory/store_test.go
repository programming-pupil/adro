package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adro-project/adro/ports/projection"
)

func TestProjectionStoreCASAndTenantIsolation(t *testing.T) {
	clock := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	store := New(func() time.Time { return clock })
	record := projection.Record{TenantID: "tenant-a", Projection: "sessions", Key: "s1", Version: 1, SourceStream: "stream-a", SourceSequence: 1, Payload: []byte(`{"state":"running"}`)}
	if err := store.Put(context.Background(), record, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), projection.Record{TenantID: "tenant-a", Projection: "sessions", Key: "s1", Version: 2, SourceStream: "stream-a", SourceSequence: 2, Digest: "bad", Payload: []byte("next")}, 1); err == nil {
		t.Fatal("invalid digest accepted")
	}
	got, err := store.Get(context.Background(), "tenant-a", "sessions", "s1")
	if err != nil || got.Version != 1 || got.TenantID != "tenant-a" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if _, err := store.Get(context.Background(), "tenant-b", "sessions", "s1"); !errors.Is(err, projection.ErrNotFound) {
		t.Fatalf("cross-tenant read error=%v", err)
	}
	if err := store.Delete(context.Background(), "tenant-a", "sessions", "s1", 0); !errors.Is(err, projection.ErrConflict) {
		t.Fatalf("stale delete error=%v", err)
	}
}
