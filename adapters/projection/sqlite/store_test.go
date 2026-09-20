package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	projectionconformance "github.com/adro-project/adro/conformance/projection"
	"github.com/adro-project/adro/core/testkit"
	"github.com/adro-project/adro/ports/projection"
	"github.com/adro-project/adro/ports/scope"
)

func TestSQLiteProjectionConformance(t *testing.T) {
	projectionconformance.Run(t, func(t *testing.T) projectionconformance.Backend {
		store, err := Open(filepath.Join(t.TempDir(), "projection.db"), Options{
			Clock: testkit.NewManualClock(time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)),
		})
		if err != nil {
			t.Fatal(err)
		}
		return store
	})
}

func TestSQLiteProjectionRejectsTamperedStoredPayload(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "projection.db"), Options{Clock: testkit.NewManualClock(time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC))})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := scope.WithTenant(context.Background(), "tenant-a")
	item := projection.Record{TenantID: "tenant-a", Projection: "sessions", Key: "tamper", Version: 1, SourceStream: "stream-a", SourceSequence: 1, Payload: []byte("original")}
	if err := store.Put(ctx, item, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE runtime_projections SET payload=? WHERE tenant_id=? AND projection_name=? AND record_key=?`, []byte("tampered"), "tenant-a", "sessions", "tamper"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "tenant-a", "sessions", "tamper"); !errors.Is(err, projection.ErrCorrupt) {
		t.Fatalf("tampered projection error=%v", err)
	}
}

func TestSQLiteProjectionCloseIsTerminal(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "projection.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(scope.WithTenant(context.Background(), "tenant-a"), "tenant-a", "sessions", "key"); !errors.Is(err, projection.ErrClosed) {
		t.Fatalf("closed get error=%v", err)
	}
}

func TestSQLiteProjectionRejectsTamperedOffsetDigest(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "projection.db"), Options{Clock: testkit.NewManualClock(time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC))})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := scope.WithTenant(context.Background(), "tenant-a")
	if err := store.PutOffset(ctx, projection.Offset{TenantID: "tenant-a", Projection: "sessions", PartitionID: "stream-a", LastSequence: 1, ProjectionDigest: strings.Repeat("1", 64)}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE runtime_projection_offsets SET projection_digest=? WHERE tenant_id=? AND projection_name=? AND partition_id=?`, "tampered", "tenant-a", "sessions", "stream-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetOffset(ctx, "tenant-a", "sessions", "stream-a"); !errors.Is(err, projection.ErrCorrupt) {
		t.Fatalf("tampered offset error=%v", err)
	}
}
