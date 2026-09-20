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
	projectionport.AtomicStore
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
		if err := store.PutOffset(context.Background(), projectionport.Offset{TenantID: "tenant-a", Projection: "sessions", PartitionID: "stream-4", LastSequence: 0, ProjectionDigest: strings.Repeat("0", 64)}, 0); err == nil {
			t.Fatal("unscoped offset write unexpectedly succeeded")
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

	t.Run("offset_cas_and_scope", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scope.WithTenant(context.Background(), "tenant-a")
		digestOne := strings.Repeat("1", 64)
		digestTwo := strings.Repeat("2", 64)
		first := projectionport.Offset{TenantID: "tenant-a", Projection: "sessions", PartitionID: "stream-offset", LastSequence: 1, ProjectionDigest: digestOne}
		if err := store.PutOffset(ctx, first, 0); err != nil {
			t.Fatal(err)
		}
		replay := first
		if err := store.PutOffset(ctx, replay, 1); err != nil {
			t.Fatalf("offset replay failed: %v", err)
		}
		if err := store.PutOffset(ctx, projectionport.Offset{TenantID: "tenant-a", Projection: "sessions", PartitionID: "stream-offset", LastSequence: 2, ProjectionDigest: digestTwo}, 0); !errors.Is(err, projectionport.ErrOffsetConflict) {
			t.Fatalf("stale offset error=%v", err)
		}
		if err := store.PutOffset(ctx, projectionport.Offset{TenantID: "tenant-a", Projection: "sessions", PartitionID: "stream-offset", LastSequence: 2, ProjectionDigest: digestTwo}, 1); err != nil {
			t.Fatal(err)
		}
		got, err := store.GetOffset(ctx, "tenant-a", "sessions", "stream-offset")
		if err != nil || got.LastSequence != 2 || got.ProjectionDigest != digestTwo || got.UpdatedAt.IsZero() {
			t.Fatalf("offset=%+v err=%v", got, err)
		}
		if _, err := store.GetOffset(scope.WithTenant(context.Background(), "tenant-b"), "tenant-a", "sessions", "stream-offset"); !errors.Is(err, projectionport.ErrTenantMismatch) {
			t.Fatalf("cross-tenant offset error=%v", err)
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
	runAtomicBatchConformance(t, factory)
}

func runAtomicBatchConformance(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("atomic_batch_commit_replay_and_delete", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scope.WithTenant(context.Background(), "tenant-a")
		batch := projectionport.Batch{
			TenantID: "tenant-a", Projection: "sessions", PartitionID: "stream-a", LastSequence: 2,
			Mutations: []projectionport.BatchMutation{
				{Key: "session-a", Payload: []byte("one"), SourceSequence: 1},
				{Key: "session-b", Payload: []byte("other"), SourceSequence: 1},
				{Key: "session-a", Payload: []byte("two"), SourceSequence: 2},
			},
		}
		result, err := store.ApplyBatch(ctx, batch, 0)
		if err != nil {
			t.Fatal(err)
		}
		if result.UpdatedRecords != 3 || result.DeletedRecords != 0 || result.SkippedMutations != 0 || result.ProjectionDigest == "" {
			t.Fatalf("first batch result=%+v", result)
		}
		item, err := store.Get(ctx, "tenant-a", "sessions", "session-a")
		if err != nil || item.Version != 2 || item.SourceSequence != 2 || string(item.Payload) != "two" {
			t.Fatalf("session-a=%+v err=%v", item, err)
		}
		offset, err := store.GetOffset(ctx, "tenant-a", "sessions", "stream-a")
		if err != nil || offset.LastSequence != 2 || offset.ProjectionDigest != result.ProjectionDigest {
			t.Fatalf("offset=%+v err=%v", offset, err)
		}
		if _, err := store.ApplyBatch(scope.WithTenant(context.Background(), "tenant-b"), projectionport.Batch{
			TenantID: "tenant-b", Projection: "sessions", PartitionID: "stream-a", LastSequence: 1,
			Mutations: []projectionport.BatchMutation{{Key: "tenant-b-key", Payload: []byte("other"), SourceSequence: 1}},
		}, 0); err != nil {
			t.Fatalf("cross-tenant seed batch: %v", err)
		}

		replay, err := store.ApplyBatch(ctx, batch, 2)
		if err != nil {
			t.Fatalf("idempotent batch replay: %v", err)
		}
		if replay.UpdatedRecords != 0 || replay.DeletedRecords != 0 || replay.SkippedMutations != 3 || replay.ProjectionDigest != result.ProjectionDigest {
			t.Fatalf("replay result=%+v", replay)
		}

		deleted, err := store.ApplyBatch(ctx, projectionport.Batch{
			TenantID: "tenant-a", Projection: "sessions", PartitionID: "stream-a", LastSequence: 3,
			Mutations: []projectionport.BatchMutation{{Key: "session-a", Delete: true, SourceSequence: 3}},
		}, 2)
		if err != nil {
			t.Fatalf("delete batch: %v", err)
		}
		if deleted.DeletedRecords != 1 || deleted.ProjectionDigest == result.ProjectionDigest {
			t.Fatalf("delete result=%+v", deleted)
		}
		if _, err := store.Get(ctx, "tenant-a", "sessions", "session-a"); !errors.Is(err, projectionport.ErrNotFound) {
			t.Fatalf("deleted record error=%v", err)
		}
	})

	t.Run("atomic_batch_rolls_back_records_and_offset", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scope.WithTenant(context.Background(), "tenant-a")
		seed := projectionport.Batch{
			TenantID: "tenant-a", Projection: "sessions", PartitionID: "stream-a", LastSequence: 1,
			Mutations: []projectionport.BatchMutation{{Key: "stable", Payload: []byte("before"), SourceSequence: 1}},
		}
		if _, err := store.ApplyBatch(ctx, seed, 0); err != nil {
			t.Fatal(err)
		}
		// This row belongs to another source stream. It is excluded from the
		// stream-a digest, but must still make the second mutation fail closed.
		if err := store.Put(ctx, projectionport.Record{
			TenantID: "tenant-a", Projection: "sessions", Key: "foreign", Version: 1,
			SourceStream: "stream-b", SourceSequence: 1, Payload: []byte("foreign"),
		}, 0); err != nil {
			t.Fatal(err)
		}
		_, err := store.ApplyBatch(ctx, projectionport.Batch{
			TenantID: "tenant-a", Projection: "sessions", PartitionID: "stream-a", LastSequence: 2,
			Mutations: []projectionport.BatchMutation{
				{Key: "stable", Payload: []byte("after"), SourceSequence: 2},
				{Key: "foreign", Payload: []byte("invalid"), SourceSequence: 2},
			},
		}, 1)
		if !errors.Is(err, projectionport.ErrDiverged) {
			t.Fatalf("divergent batch error=%v", err)
		}
		stable, err := store.Get(ctx, "tenant-a", "sessions", "stable")
		if err != nil || string(stable.Payload) != "before" || stable.SourceSequence != 1 {
			t.Fatalf("rollback record=%+v err=%v", stable, err)
		}
		offset, err := store.GetOffset(ctx, "tenant-a", "sessions", "stream-a")
		if err != nil || offset.LastSequence != 1 {
			t.Fatalf("rollback offset=%+v err=%v", offset, err)
		}
	})

	t.Run("atomic_batch_rejects_scope_and_noncanonical_keys", func(t *testing.T) {
		store := factory(t)
		defer store.Close()
		ctx := scope.WithTenant(context.Background(), "tenant-a")
		batch := projectionport.Batch{
			TenantID: "tenant-a", Projection: "sessions", PartitionID: "stream-a", LastSequence: 1,
			Mutations: []projectionport.BatchMutation{{Key: " key", Payload: []byte("value"), SourceSequence: 1}},
		}
		if _, err := store.ApplyBatch(ctx, batch, 0); err == nil {
			t.Fatal("noncanonical key unexpectedly succeeded")
		}
		if _, err := store.ApplyBatch(scope.WithTenant(context.Background(), "tenant-b"), projectionport.Batch{
			TenantID: "tenant-a", Projection: "sessions", PartitionID: "stream-a", LastSequence: 1,
		}, 0); !errors.Is(err, projectionport.ErrTenantMismatch) {
			t.Fatalf("cross-tenant batch error=%v", err)
		}
	})
}
