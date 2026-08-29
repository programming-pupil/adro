package artifact

import (
	"bytes"
	"context"
	"testing"
)

func TestMigratorVerifiesDestinationHash(t *testing.T) {
	source, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	destination, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := Key{TenantID: "tenant", ArtifactID: "report", Version: 1}
	if _, err := source.Put(context.Background(), key, bytes.NewBufferString("verified"), PutOptions{MediaType: "text/plain", Immutable: true}); err != nil {
		t.Fatal(err)
	}
	meta, err := (Migrator{Source: source, Destination: destination}).Copy(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ContentSHA256 == "" || meta.SizeBytes != 8 {
		t.Fatalf("meta=%+v", meta)
	}
}
