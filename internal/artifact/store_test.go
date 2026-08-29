package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestFileStoreRoundTripAndRange(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := Key{TenantID: "tenant", ArtifactID: "a", Version: 1}
	meta, err := s.Put(context.Background(), key, strings.NewReader("abcdef"), PutOptions{MediaType: "text/plain", Immutable: true})
	if err != nil {
		t.Fatal(err)
	}
	if meta.SizeBytes != 6 || meta.ContentSHA256 == "" {
		t.Fatalf("bad metadata: %+v", meta)
	}
	f, _, err := s.Open(context.Background(), key, ByteRange{Start: 1, End: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	if string(b) != "bcd" {
		t.Fatalf("range=%q", b)
	}
	if _, err := s.Put(context.Background(), key, strings.NewReader("new"), PutOptions{}); err == nil {
		t.Fatal("expected immutable artifact overwrite to fail")
	}
}
func TestFileStoreRejectsTraversal(t *testing.T) {
	s, _ := NewFileStore(t.TempDir())
	if _, err := s.Stat(context.Background(), Key{TenantID: "../x", ArtifactID: "a", Version: 1}); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestImmutableArtifactConcurrentPutIsSingleCommit(t *testing.T) {
	s, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := Key{TenantID: "tenant", ArtifactID: "race", Version: 1}
	const writers = 32
	var wg sync.WaitGroup
	results := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := []byte("payload-" + string(rune('a'+i)))
			_, putErr := s.Put(context.Background(), key, bytes.NewReader(payload), PutOptions{MediaType: "text/plain", Immutable: true})
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
	f, _, err := s.Open(context.Background(), key, ByteRange{End: -1})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	meta, err := s.Stat(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	if got := hex.EncodeToString(digest[:]); got != meta.ContentSHA256 {
		t.Fatalf("content digest %s does not match metadata %s", got, meta.ContentSHA256)
	}
}
