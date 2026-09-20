package filesystem

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adro-project/adro/ports/blobstore"
	"github.com/adro-project/adro/ports/scope"
)

func TestBlobStoreContentAddressingIsolationAndTombstone(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := scope.WithTenant(context.Background(), "tenant-a")
	ref, err := store.Put(ctx, blobstore.BlobPutRequest{TenantID: "tenant-a", MediaType: "text/plain", Classification: "internal", MaxBytes: 128}, strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Size != 5 || ref.Digest == "" || ref.TenantID != "tenant-a" {
		t.Fatalf("ref=%+v", ref)
	}
	duplicate, err := store.Put(ctx, blobstore.BlobPutRequest{TenantID: "tenant-a", MediaType: "text/plain", Classification: "internal", MaxBytes: 128}, strings.NewReader("hello"))
	if err != nil || duplicate.Digest != ref.Digest {
		t.Fatalf("duplicate=%+v err=%v", duplicate, err)
	}
	if _, err := store.Stat(ctx, blobstore.BlobRef{TenantID: "tenant-b", Digest: ref.Digest, Size: ref.Size}); !errors.Is(err, blobstore.ErrNotFound) {
		t.Fatalf("cross-tenant blob visible: %v", err)
	}
	reader, err := store.Open(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(data) != "hello" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if err := store.Tombstone(ctx, ref, "privacy_request"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(ctx, ref); !errors.Is(err, blobstore.ErrTombstoned) {
		t.Fatalf("tombstoned blob opened: %v", err)
	}
	if err := store.Tombstone(ctx, ref, ""); err == nil {
		t.Fatal("empty tombstone reason accepted")
	}
	if err := store.Tombstone(ctx, ref, "privacy_request"); err != nil {
		t.Fatalf("idempotent tombstone failed: %v", err)
	}
	meta, err := store.Stat(ctx, ref)
	if err != nil || !meta.Tombstoned || meta.Tombstone != "privacy_request" {
		t.Fatalf("metadata=%+v err=%v", meta, err)
	}
	if err := store.Purge(ctx, ref, "wrong-reason"); !errors.Is(err, blobstore.ErrConflict) {
		t.Fatalf("wrong purge reason err=%v", err)
	}
	if err := store.Purge(ctx, ref, "privacy_request"); err != nil {
		t.Fatalf("purge failed: %v", err)
	}
	if _, err := store.Stat(ctx, ref); !errors.Is(err, blobstore.ErrNotFound) {
		t.Fatalf("purged blob stat err=%v", err)
	}
}

func TestBlobStoreRejectsOversizeAndLegalHoldDeletion(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(scope.WithTenant(context.Background(), "tenant-a"), blobstore.BlobPutRequest{TenantID: "tenant-a", MaxBytes: 4}, strings.NewReader("12345")); !errors.Is(err, blobstore.ErrLimit) {
		t.Fatalf("oversize err=%v", err)
	}
	ref, err := store.Put(scope.WithTenant(context.Background(), "tenant-a"), blobstore.BlobPutRequest{TenantID: "tenant-a", MaxBytes: 16, LegalHold: true}, strings.NewReader("held"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Tombstone(scope.WithTenant(context.Background(), "tenant-a"), ref, "delete"); !errors.Is(err, blobstore.ErrLegalHold) {
		t.Fatalf("legal hold err=%v", err)
	}
}

func TestBlobStoreDetectsTamperedPayload(t *testing.T) {
	root := filepath.Join(t.TempDir(), "blobs")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(scope.WithTenant(context.Background(), "tenant-a"), blobstore.BlobPutRequest{TenantID: "tenant-a"}, strings.NewReader("tamper"))
	if err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(root, "*", "*", ref.Digest+".blob"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("blob paths=%v err=%v", matches, err)
	}
	if err := os.WriteFile(matches[0], []byte("changed"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(scope.WithTenant(context.Background(), "tenant-a"), ref); err == nil {
		t.Fatal("tampered payload opened")
	}
	if _, err := store.Put(scope.WithTenant(context.Background(), "tenant-a"), blobstore.BlobPutRequest{TenantID: "tenant-a"}, strings.NewReader("tamper")); err == nil {
		t.Fatal("tampered idempotent replay was accepted")
	}
	if err := os.WriteFile(matches[0], []byte("tamper"), 0o640); err != nil {
		t.Fatal(err)
	}
	metadataMatches, err := filepath.Glob(filepath.Join(root, "*", "*", ref.Digest+".json"))
	if err != nil || len(metadataMatches) != 1 {
		t.Fatalf("metadata paths=%v err=%v", metadataMatches, err)
	}
	metadata, err := os.ReadFile(metadataMatches[0])
	if err != nil {
		t.Fatal(err)
	}
	corruptDigest := strings.Repeat("0", 64)
	if !strings.Contains(string(metadata), `"stored_digest":"`+ref.Digest+`"`) {
		t.Fatalf("stored digest missing from metadata: %s", metadata)
	}
	metadata = []byte(strings.Replace(string(metadata), `"stored_digest":"`+ref.Digest+`"`, `"stored_digest":"`+corruptDigest+`"`, 1))
	if err := os.WriteFile(metadataMatches[0], metadata, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(scope.WithTenant(context.Background(), "tenant-a"), ref); err == nil {
		t.Fatal("tampered stored digest was accepted")
	}
}
