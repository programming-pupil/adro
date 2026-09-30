package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adro-project/adro/ports/scope"
	"github.com/adro-project/adro/ports/snapshot"
)

func TestSnapshotValidationRejectsCrossTenantAndDigestMismatch(t *testing.T) {
	value := snapshot.Snapshot{TenantID: "tenant-a", StreamID: "stream-a", Sequence: 1, SchemaVersion: 1, EncodingVersion: 1, HashAlgorithm: "sha256", HashVersion: 1, Digest: "bad", Payload: []byte("state"), CreatedAt: time.Unix(1, 0).UTC()}
	if err := validate(value, "tenant-a", "stream-a"); !errors.Is(err, snapshot.ErrCorrupt) {
		t.Fatalf("digest validation error=%v", err)
	}
	value.Digest = ""
	if err := validate(value, "tenant-b", "stream-a"); !errors.Is(err, snapshot.ErrCorrupt) {
		t.Fatalf("tenant validation error=%v", err)
	}
}

func TestSnapshotPayloadLimitIsExplicit(t *testing.T) {
	if err := validatePayloadSize([]byte("1234"), 4); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(validatePayloadSize([]byte("12345"), 4), snapshot.ErrLimit) {
		t.Fatal("oversize snapshot payload was accepted")
	}
	if err := validatePayloadSize([]byte("small"), 0); err != nil {
		t.Fatalf("default payload limit rejected small payload: %v", err)
	}
}

func TestPostgresSnapshotCASAndTenantScope(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("ADRO_POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("set ADRO_POSTGRES_TEST_DSN to run PostgreSQL snapshot conformance")
	}
	store, err := Open(dsn, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.db.Exec(`TRUNCATE runtime_snapshots`); err != nil {
		t.Fatal(err)
	}
	payload := []byte("state-1")
	digest := sha256.Sum256(payload)
	value := snapshot.Snapshot{TenantID: "tenant-a", StreamID: "snapshot-test", Sequence: 1, SchemaVersion: 1, EncodingVersion: 1, HashAlgorithm: "sha256", HashVersion: 1, Digest: hex.EncodeToString(digest[:]), Payload: payload, CreatedAt: time.Now().UTC()}
	ctx := scope.WithTenant(context.Background(), "tenant-a")
	if err := store.Put(ctx, value, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, value, 0); err != nil {
		t.Fatalf("idempotent put: %v", err)
	}
	if _, err := store.Get(scope.WithTenant(context.Background(), "tenant-b"), value.StreamID); !errors.Is(err, snapshot.ErrNotFound) {
		t.Fatalf("cross-tenant read err=%v", err)
	}
	if err := store.Put(ctx, snapshot.Snapshot{TenantID: "tenant-a", StreamID: value.StreamID, Sequence: 2, SchemaVersion: 1, EncodingVersion: 1, HashAlgorithm: "sha256", HashVersion: 1, Digest: value.Digest, Payload: payload, CreatedAt: value.CreatedAt}, 0); !errors.Is(err, snapshot.ErrConflict) {
		t.Fatalf("stale CAS err=%v", err)
	}
}
