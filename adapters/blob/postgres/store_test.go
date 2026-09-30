package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/adro-project/adro/ports/blobstore"
	"github.com/adro-project/adro/ports/scope"
)

func TestAESGCMRoundTripAndEnvelopeValidation(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	cipher := AESGCM{Resolve: func(context.Context, string) ([]byte, error) { return key, nil }}
	sealed, err := cipher.Seal(context.Background(), "kms://tenant-a/key-1", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := cipher.Open(context.Background(), "kms://tenant-a/key-1", sealed)
	if err != nil || string(opened) != "secret" {
		t.Fatalf("opened=%q err=%v", opened, err)
	}
	if _, err := cipher.Open(context.Background(), "kms://tenant-a/key-1", []byte("bad")); err == nil {
		t.Fatal("malformed envelope was accepted")
	}
}

func TestPostgresBlobInventoryVerifiesEncryptedPayload(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	store := &Store{encryptor: AESGCM{Resolve: func(context.Context, string) ([]byte, error) { return key, nil }}}
	ref := blobstore.BlobRef{TenantID: "tenant-a", Size: 6, EncryptionKey: "kms://tenant-a/key-1"}
	// Use the actual digest instead of a hand-written value so the helper
	// exercises the same content-addressed contract as the SQL adapter.
	plain := []byte("secret")
	digest := sha256.Sum256(plain)
	ref.Digest = hex.EncodeToString(digest[:])
	sealed, err := store.encryptor.Seal(context.Background(), ref.EncryptionKey, plain)
	if err != nil {
		t.Fatal(err)
	}
	storedDigest := digestBytesHex(sealed)
	if err := store.verifyStoredPayload(context.Background(), blobstore.BlobMetadata{BlobRef: ref, StoredDigest: storedDigest}, sealed); err != nil {
		t.Fatalf("encrypted payload rejected: %v", err)
	}
	sealed[len(sealed)-1] ^= 1
	if err := store.verifyStoredPayload(context.Background(), blobstore.BlobMetadata{BlobRef: ref, StoredDigest: storedDigest}, sealed); err == nil {
		t.Fatal("tampered encrypted payload accepted")
	}
}

func TestPostgresBlobTombstoneVerificationDoesNotRequireKey(t *testing.T) {
	plain := []byte("secret")
	sealed := []byte("ciphertext-proof")
	digest := sha256.Sum256(plain)
	metadata := blobstore.BlobMetadata{
		BlobRef: blobstore.BlobRef{
			TenantID: "tenant-a", Digest: hex.EncodeToString(digest[:]), Size: int64(len(plain)),
			EncryptionKey: "kms://tenant-a/key-1",
		},
		Tombstoned:   true,
		StoredDigest: digestBytesHex(sealed),
	}
	store := &Store{}
	if err := store.verifyStoredPayload(context.Background(), metadata, sealed); err != nil {
		t.Fatalf("tombstone proof unexpectedly required key: %v", err)
	}
	sealed[0] ^= 1
	if err := store.verifyStoredPayload(context.Background(), metadata, sealed); err == nil {
		t.Fatal("tampered tombstone accepted")
	}
	sealed[0] ^= 1
	metadata.StoredDigest = ""
	if err := store.verifyStoredPayload(context.Background(), metadata, sealed); err == nil {
		t.Fatal("tombstone without ciphertext proof accepted")
	}
}

func TestPostgresBlobEncryptionTenantIsolationAndTombstone(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("ADRO_POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("set ADRO_POSTGRES_TEST_DSN to run PostgreSQL blob conformance")
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	store, err := Open(dsn, Options{Encryptor: AESGCM{Resolve: func(context.Context, string) ([]byte, error) { return key, nil }}})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.db.Exec(`TRUNCATE runtime_blobs`); err != nil {
		t.Fatal(err)
	}
	ctx := scope.WithTenant(context.Background(), "tenant-a")
	ref, err := store.Put(ctx, blobstore.BlobPutRequest{TenantID: "tenant-a", MediaType: "text/plain", EncryptionKey: "kms://tenant-a/key-1", Classification: "secret"}, strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := store.Open(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil || string(data) != "hello" {
		t.Fatalf("data=%q err=%v", data, readErr)
	}
	if _, err := store.Open(scope.WithTenant(context.Background(), "tenant-b"), ref); !errors.Is(err, blobstore.ErrNotFound) {
		t.Fatalf("cross-tenant open err=%v", err)
	}
	if err := store.Tombstone(ctx, ref, "privacy_request"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(ctx, ref); !errors.Is(err, blobstore.ErrTombstoned) {
		t.Fatalf("tombstoned open err=%v", err)
	}
}
