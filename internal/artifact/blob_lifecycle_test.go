package artifact

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	blobfs "github.com/adro-project/adro/adapters/blob/filesystem"
	"github.com/adro-project/adro/ports/blobstore"
	"github.com/adro-project/adro/ports/scope"
)

func TestBlobLifecycleTombstonesThenPurgesAndPreservesRoots(t *testing.T) {
	store, err := blobfs.New(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := scope.WithTenant(context.Background(), "tenant-a")
	kept, err := store.Put(ctx, blobstore.BlobPutRequest{TenantID: "tenant-a"}, strings.NewReader("kept"))
	if err != nil {
		t.Fatal(err)
	}
	unrooted, err := store.Put(ctx, blobstore.BlobPutRequest{TenantID: "tenant-a"}, strings.NewReader("sweep"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	lifecycle, err := NewBlobLifecycle(store, BlobLifecycleOptions{Path: filepath.Join(t.TempDir(), "lifecycle.json"), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.AddRoot(BlobLifecycleRoot{TenantID: "tenant-a", Digest: kept.Digest, Reason: "active-session", LegalHold: true}); err != nil {
		t.Fatal(err)
	}
	first, err := lifecycle.Collect(ctx, "tenant-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tombstoned) != 1 || len(first.Deleted) != 0 || len(first.Protected) != 1 {
		t.Fatalf("first report=%+v", first)
	}
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
	second, err := lifecycle.Collect(ctx, "tenant-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Deleted) != 1 || second.Deleted[0] != "tenant-a:"+unrooted.Digest {
		t.Fatalf("second report=%+v", second)
	}
	if _, err := store.Open(ctx, unrooted); !errors.Is(err, blobstore.ErrNotFound) {
		t.Fatalf("purged blob err=%v", err)
	}
	reader, err := store.Open(ctx, kept)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(data) != "kept" {
		t.Fatalf("rooted blob data=%q err=%v", data, err)
	}
}
