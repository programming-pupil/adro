package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adro-project/adro/ports/scope"
)

func TestFileStoreRoundTripAndRange(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := Key{TenantID: "tenant", ArtifactID: "a", Version: 1}
	ctx := scope.WithTenant(context.Background(), key.TenantID)
	meta, err := s.Put(ctx, key, strings.NewReader("abcdef"), PutOptions{MediaType: "text/plain", Immutable: true})
	if err != nil {
		t.Fatal(err)
	}
	if meta.SizeBytes != 6 || meta.ContentSHA256 == "" {
		t.Fatalf("bad metadata: %+v", meta)
	}
	f, _, err := s.Open(ctx, key, ByteRange{Start: 1, End: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	if string(b) != "bcd" {
		t.Fatalf("range=%q", b)
	}
	if _, err := s.Put(ctx, key, strings.NewReader("new"), PutOptions{}); err == nil {
		t.Fatal("expected immutable artifact overwrite to fail")
	}
}
func TestFileStoreRejectsTraversal(t *testing.T) {
	s, _ := NewFileStore(t.TempDir())
	key := Key{TenantID: "../x", ArtifactID: "a", Version: 1}
	if _, err := s.Stat(scope.WithTenant(context.Background(), key.TenantID), key); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestImmutableArtifactConcurrentPutIsSingleCommit(t *testing.T) {
	s, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := Key{TenantID: "tenant", ArtifactID: "race", Version: 1}
	ctx := scope.WithTenant(context.Background(), key.TenantID)
	const writers = 32
	var wg sync.WaitGroup
	results := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := []byte("payload-" + string(rune('a'+i)))
			_, putErr := s.Put(ctx, key, bytes.NewReader(payload), PutOptions{MediaType: "text/plain", Immutable: true})
			results <- putErr
		}(i)
	}
	wg.Wait()
	close(results)
	successes := 0
	for putErr := range results {
		if putErr == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("immutable concurrent writes succeeded %d times", successes)
	}
	f, _, err := s.Open(ctx, key, ByteRange{End: -1})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	meta, err := s.Stat(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	if got := hex.EncodeToString(digest[:]); got != meta.ContentSHA256 {
		t.Fatalf("content digest %s does not match metadata %s", got, meta.ContentSHA256)
	}
}

func TestFileStoreFailsClosedOnContentAndMetadataTampering(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := Key{TenantID: "tenant", ArtifactID: "tamper", Version: 1}
	ctx := scope.WithTenant(context.Background(), key.TenantID)
	if _, err := s.Put(ctx, key, strings.NewReader("trusted"), PutOptions{MediaType: "text/plain", Immutable: true}); err != nil {
		t.Fatal(err)
	}
	contentPath := filepath.Join(dir, pathComponentDigest(key.TenantID), pathComponentDigest(key.ArtifactID), "1")
	if err := os.WriteFile(contentPath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Open(ctx, key, ByteRange{End: -1}); err == nil {
		t.Fatal("Open accepted tampered content")
	}
	if _, err := s.Stat(ctx, key); err == nil {
		t.Fatal("Stat accepted tampered content")
	}

	// Restore the content, then corrupt the signed metadata independently.
	if err := os.WriteFile(contentPath, []byte("trusted"), 0o600); err != nil {
		t.Fatal(err)
	}
	metaPath := contentPath + ".meta.json"
	metadata, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	corrupted := strings.Replace(string(metadata), `"size_bytes":7`, `"size_bytes":999`, 1)
	if corrupted == string(metadata) {
		t.Fatal("test did not alter metadata")
	}
	if err := os.WriteFile(metaPath, []byte(corrupted), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Open(ctx, key, ByteRange{End: -1}); err == nil {
		t.Fatal("Open accepted tampered metadata")
	}
	if _, err := s.Stat(ctx, key); err == nil {
		t.Fatal("Stat accepted tampered metadata")
	}
}

func TestArtifactLifecycleMarksRootsAndProducesDeletionProof(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now().UTC()
	lifecycle, err := NewLifecycle(store, LifecycleOptions{Path: filepath.Join(root, "lifecycle.json"), Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	oldKey := Key{TenantID: "tenant", ArtifactID: "old", Version: 1}
	protectedKey := Key{TenantID: "tenant", ArtifactID: "protected", Version: 1}
	ctx := scope.WithTenant(context.Background(), oldKey.TenantID)
	if _, err := store.Put(ctx, oldKey, strings.NewReader("old"), PutOptions{Immutable: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(ctx, protectedKey, strings.NewReader("protected"), PutOptions{Immutable: true}); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.AddRoot(LifecycleRoot{URI: protectedKey.URI(), Reason: "retained event", LegalHold: true}); err != nil {
		t.Fatal(err)
	}
	report, err := lifecycle.Collect(context.Background(), clock.Add(24*time.Hour), "tenant")
	if err != nil {
		t.Fatal(err)
	}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(report.Deleted) != 1 || report.Deleted[0] != oldKey.URI() || len(report.Protected) != 1 || len(report.KeyDestructionPending) != 1 {
		t.Fatalf("unexpected lifecycle report: %+v", report)
	}
	if _, err := store.Stat(ctx, oldKey); err == nil {
		t.Fatal("garbage artifact still exists")
	}
	if _, err := store.Stat(ctx, protectedKey); err != nil {
		t.Fatal("legal-hold artifact was deleted")
	}
	reloaded, err := NewLifecycle(store, LifecycleOptions{Path: filepath.Join(root, "lifecycle.json"), Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Proofs()) != 1 || reloaded.Proofs()[0].ProofDigest != report.ProofDigest {
		t.Fatalf("proof was not durable: %+v", reloaded.Proofs())
	}
}

func TestArtifactListVerifiesTenantBoundary(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tenant := range []string{"a", "b"} {
		ctx := scope.WithTenant(context.Background(), tenant)
		if _, err := store.Put(ctx, Key{TenantID: tenant, ArtifactID: "x", Version: 1}, strings.NewReader(tenant), PutOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.List(scope.WithTenant(context.Background(), "a"), "a")
	if err != nil || len(items) != 1 || items[0].Key.TenantID != "a" {
		t.Fatalf("tenant list=%+v err=%v", items, err)
	}
}

func TestFileStoreListRequiresTenantScope(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.List(context.Background(), "tenant"); !errors.Is(err, scope.ErrMissingTenant) {
		t.Fatalf("unscoped list err=%v", err)
	}
}

func TestFileStoreObjectOperationsRequireMatchingTenantScope(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := Key{TenantID: "tenant-a", ArtifactID: "private", Version: 1}
	if _, err := store.Put(context.Background(), key, strings.NewReader("secret"), PutOptions{}); !errors.Is(err, scope.ErrMissingTenant) {
		t.Fatalf("unscoped put err=%v", err)
	}
	owner := scope.WithTenant(context.Background(), key.TenantID)
	if _, err := store.Put(owner, key, strings.NewReader("secret"), PutOptions{}); err != nil {
		t.Fatal(err)
	}
	foreign := scope.WithTenant(context.Background(), "tenant-b")
	if _, _, err := store.Open(foreign, key, ByteRange{End: -1}); !errors.Is(err, scope.ErrMissingTenant) {
		t.Fatalf("foreign open err=%v", err)
	}
	if _, err := store.Stat(foreign, key); !errors.Is(err, scope.ErrMissingTenant) {
		t.Fatalf("foreign stat err=%v", err)
	}
	if err := store.Delete(foreign, key, DeleteOptions{}); !errors.Is(err, scope.ErrMissingTenant) {
		t.Fatalf("foreign delete err=%v", err)
	}
	if _, err := store.Stat(owner, key); err != nil {
		t.Fatalf("foreign operation removed object: %v", err)
	}
}
