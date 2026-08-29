package artifact

import (
	"context"
	"io"
	"strings"
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
