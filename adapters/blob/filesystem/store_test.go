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
)

func TestBlobStoreContentAddressingIsolationAndTombstone(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
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
	meta, err := store.Stat(ctx, ref)
	if err != nil || !meta.Tombstoned || meta.Tombstone != "privacy_request" {
		t.Fatalf("metadata=%+v err=%v", meta, err)
	}
}

func TestBlobStoreRejectsOversizeAndLegalHoldDeletion(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), blobstore.BlobPutRequest{TenantID: "tenant-a", MaxBytes: 4}, strings.NewReader("12345")); !errors.Is(err, blobstore.ErrLimit) {
		t.Fatalf("oversize err=%v", err)
	}
	ref, err := store.Put(context.Background(), blobstore.BlobPutRequest{TenantID: "tenant-a", MaxBytes: 16, LegalHold: true}, strings.NewReader("held"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Tombstone(context.Background(), ref, "delete"); !errors.Is(err, blobstore.ErrLegalHold) {
		t.Fatalf("legal hold err=%v", err)
	}
}

func TestBlobStoreDetectsTamperedPayload(t *testing.T) {
	root := filepath.Join(t.TempDir(), "blobs")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(context.Background(), blobstore.BlobPutRequest{TenantID: "tenant-a"}, strings.NewReader("tamper"))
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
	if _, err := store.Open(context.Background(), ref); err == nil {
		t.Fatal("tampered payload opened")
	}
}
