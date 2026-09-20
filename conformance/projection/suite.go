// Package projection contains the shared contract tests for rebuildable
// projection stores. Every backend must preserve tenant scope, digest
// integrity, monotonic source progress and version CAS.
package projection

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	projectionport "github.com/adro-project/adro/ports/projection"
	"github.com/adro-project/adro/ports/scope"
)

type Backend interface {
	projectionport.Store
	Close() error
}

type Factory func(*testing.T) Backend

func Run(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("cas_replay_and_list", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scope.WithTenant(context.Background(), "tenant-a")
		first := projectionport.Record{
			TenantID: "tenant-a", Projection: "sessions", Key: "session-1", Version: 1,
			SourceStream: "stream-a", SourceSequence: 1, Payload: []byte(`{"state":"running"}`),
		}
		if err := store.Put(ctx, first, 0); err != nil {
			t.Fatal(err)
		}
		if err := store.Put(ctx, first, 1); err != nil {
			t.Fatalf("idempotent replay failed: %v", err)
		}
		got, err := store.Get(ctx, "tenant-a", "sessions", "session-1")
		if err != nil || got.Version != 1 || got.Digest == "" || string(got.Payload) != string(first.Payload) {
			t.Fatalf("got=%+v err=%v", got, err)
		}
		items, err := store.List(ctx, "tenant-a", "sessions")
		if err != nil || len(items) != 1 || items[0].Key != "session-1" {
			t.Fatalf("items=%+v err=%v", items, err)
		}
	})

	t.Run("stale_and_out_of_order_updates_fail", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scope.WithTenant(context.Background(), "tenant-a")
		base := projectionport.Record{TenantID: "tenant-a", Projection: "sessions", Key: "session-2", Version: 1, SourceStream: "stream-a", SourceSequence: 10, Payload: []byte("one")}
		if err := store.Put(ctx, base, 0); err != nil {
			t.Fatal(err)
		}
		stale := base
		stale.Version = 2
		stale.SourceSequence = 9
		stale.Payload = []byte("stale")
		if err := store.Put(ctx, stale, 1); !errors.Is(err, projectionport.ErrConflict) {
			t.Fatalf("out-of-order error=%v", err)
		}
		current := base
		current.Version = 2
		current.SourceSequence = 11
		current.Payload = []byte("two")
		if err := store.Put(ctx, current, 1); err != nil {
			t.Fatal(err)
		}
		if err := store.Put(ctx, current, 2); err != nil {
			t.Fatalf("same-version replay failed: %v", err)
		}
		if err := store.Put(ctx, projectionport.Record{TenantID: "tenant-a", Projection: "sessions", Key: "session-2", Version: 3, SourceStream: "stream-a", SourceSequence: 12, Payload: []byte("three")}, 1); !errors.Is(err, projectionport.ErrConflict) {
			t.Fatalf("stale CAS error=%v", err)
		}
	})

	t.Run("tenant_scope_and_delete", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scope.WithTenant(context.Background(), "tenant-a")
		item := projectionport.Record{TenantID: "tenant-a", Projection: "sessions", Key: "session-3", Version: 1, SourceStream: "stream-a", SourceSequence: 1, Payload: []byte("data")}
		if err := store.Put(ctx, item, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Get(scope.WithTenant(context.Background(), "tenant-b"), "tenant-a", "sessions", "session-3"); !errors.Is(err, projectionport.ErrTenantMismatch) {
			t.Fatalf("cross-tenant get error=%v", err)
		}
		if err := store.Delete(ctx, "tenant-a", "sessions", "session-3", 0); !errors.Is(err, projectionport.ErrConflict) {
			t.Fatalf("stale delete error=%v", err)
		}
		if err := store.Delete(ctx, "tenant-a", "sessions", "session-3", 1); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Get(ctx, "tenant-a", "sessions", "session-3"); !errors.Is(err, projectionport.ErrNotFound) {
			t.Fatalf("deleted get error=%v", err)
		}
	})

	t.Run("missing_scope_fails_closed", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		item := projectionport.Record{TenantID: "tenant-a", Projection: "sessions", Key: "session-4", Version: 1, SourceStream: "stream-a", SourceSequence: 1, Payload: []byte("data")}
		if err := store.Put(context.Background(), item, 0); err == nil {
			t.Fatal("unscoped write unexpectedly succeeded")
		}
		if _, err := store.List(context.Background(), "tenant-a", "sessions"); err == nil {
			t.Fatal("unscoped list unexpectedly succeeded")
		}
	})

	t.Run("integrity_and_identity_fail_closed", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scope.WithTenant(context.Background(), "tenant-a")
		item := projectionport.Record{
			TenantID: "tenant-a", Projection: "sessions", Key: "session-6", Version: 1,
			SourceStream: "stream-a", SourceSequence: 1, Payload: []byte("data"),
			Digest: strings.Repeat("0", 64),
		}
		if err := store.Put(ctx, item, 0); !errors.Is(err, projectionport.ErrCorrupt) {
			t.Fatalf("digest mismatch error=%v", err)
		}
		item.Key = "session\n6"
		item.Digest = ""
		if err := store.Put(ctx, item, 0); err == nil {
			t.Fatal("control-character key unexpectedly succeeded")
		}
	})

	t.Run("updated_at_is_stable_and_utc", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scope.WithTenant(context.Background(), "tenant-a")
		item := projectionport.Record{TenantID: "tenant-a", Projection: "sessions", Key: "session-5", Version: 1, SourceStream: "stream-a", SourceSequence: 1, Payload: []byte("data"), UpdatedAt: time.Now()}
		if err := store.Put(ctx, item, 0); err != nil {
			t.Fatal(err)
		}
		got, err := store.Get(ctx, "tenant-a", "sessions", "session-5")
		if err != nil || got.UpdatedAt.IsZero() || got.UpdatedAt.Location() != time.UTC {
			t.Fatalf("updated_at=%v err=%v", got.UpdatedAt, err)
		}
	})
}
