// Package postgres implements the production PostgreSQL BlobStore adapter.
// Blob identity is the SHA-256 digest of the plaintext; encrypted bytes never
// change the stable reference carried by events and projections.
package postgres

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adro-project/adro/core"
	"github.com/adro-project/adro/ports/blobstore"
	"github.com/adro-project/adro/ports/scope"
	_ "github.com/lib/pq"
)

const defaultMaxBytes = 64 << 20

var ErrClosed = errors.New("PostgreSQL blob store is closed")

// Encryptor protects payload bytes at rest. The key reference is persisted in
// metadata, while key material remains inside the caller-owned key manager.
type Encryptor interface {
	Seal(context.Context, string, []byte) ([]byte, error)
	Open(context.Context, string, []byte) ([]byte, error)
}

// KeyResolver resolves a short-lived key reference. It must not return a key
// that is shared across tenants unless the caller has explicitly authorized it.
type KeyResolver func(context.Context, string) ([]byte, error)

// AESGCM is a small envelope primitive for the reference adapter. Production
// deployments should resolve keys through a KMS-backed KeyResolver.
type AESGCM struct{ Resolve KeyResolver }

func (a AESGCM) Seal(ctx context.Context, keyRef string, plaintext []byte) ([]byte, error) {
	key, err := a.key(ctx, keyRef)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES key: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate encryption nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, plaintext, []byte(keyRef))
	result := make([]byte, 1+len(nonce)+len(sealed))
	result[0] = 1
	copy(result[1:], nonce)
	copy(result[1+len(nonce):], sealed)
	return result, nil
}

func (a AESGCM) Open(ctx context.Context, keyRef string, ciphertext []byte) ([]byte, error) {
	key, err := a.key(ctx, keyRef)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES key: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	if len(ciphertext) < 1+gcm.NonceSize()+gcm.Overhead() || ciphertext[0] != 1 {
		return nil, errors.New("unsupported encrypted blob envelope")
	}
	return gcm.Open(nil, ciphertext[1:1+gcm.NonceSize()], ciphertext[1+gcm.NonceSize():], []byte(keyRef))
}

func (a AESGCM) key(ctx context.Context, keyRef string) ([]byte, error) {
	if a.Resolve == nil || strings.TrimSpace(keyRef) == "" {
		return nil, errors.New("encryption key resolver and key reference are required")
	}
	key, err := a.Resolve(ctx, keyRef)
	if err != nil {
		return nil, err
	}
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return nil, errors.New("AES key must be 128, 192, or 256 bits")
	}
	return append([]byte(nil), key...), nil
}

type Options struct {
	Clock     core.Clock
	Encryptor Encryptor
	MaxBytes  int64
}

type Store struct {
	db        *sql.DB
	clock     core.Clock
	encryptor Encryptor
	maxBytes  int64
	ownsDB    bool
	closed    atomic.Bool
	closeMu   sync.Once
}

func Open(dsn string, options Options) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("PostgreSQL blob store DSN is required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL blob store: %w", err)
	}
	store, err := New(db, options)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	store.ownsDB = true
	return store, nil
}

func New(db *sql.DB, options Options) (*Store, error) {
	if db == nil {
		return nil, errors.New("PostgreSQL blob database handle is required")
	}
	if options.Clock == nil {
		options.Clock = core.SystemClock{}
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = defaultMaxBytes
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping PostgreSQL blob store: %w", err)
	}
	if err := EnsureSchema(db); err != nil {
		return nil, err
	}
	return &Store{db: db, clock: options.Clock, encryptor: options.Encryptor, maxBytes: options.MaxBytes}, nil
}

func EnsureSchema(db interface {
	Exec(query string, args ...any) (sql.Result, error)
}) error {
	if db == nil {
		return errors.New("blob schema database handle is required")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS runtime_blobs (
		tenant_id text NOT NULL,
		digest text NOT NULL,
		size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
		media_type text NOT NULL DEFAULT '',
		encryption_key text NOT NULL DEFAULT '',
		classification text NOT NULL DEFAULT '',
		retain_until_us bigint,
		created_at_us bigint NOT NULL,
		tombstoned boolean NOT NULL DEFAULT false,
		legal_hold boolean NOT NULL DEFAULT false,
		tombstone text NOT NULL DEFAULT '',
		stored_digest text NOT NULL DEFAULT '',
		payload bytea NOT NULL,
		PRIMARY KEY (tenant_id, digest)
	)`)
	if err != nil {
		return fmt.Errorf("create PostgreSQL blob schema: %w", err)
	}
	if _, err = db.Exec(`ALTER TABLE runtime_blobs ADD COLUMN IF NOT EXISTS stored_digest text NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add PostgreSQL blob stored digest: %w", err)
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS runtime_blobs_retention_idx ON runtime_blobs (tenant_id, retain_until_us, tombstoned)`)
	if err != nil {
		return fmt.Errorf("create PostgreSQL blob retention index: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	var err error
	s.closeMu.Do(func() {
		s.closed.Store(true)
		if s.ownsDB {
			err = s.db.Close()
		}
	})
	return err
}

func (s *Store) checkOpen() error {
	if s == nil || s.closed.Load() {
		return ErrClosed
	}
	return nil
}

func (s *Store) Put(ctx context.Context, request blobstore.BlobPutRequest, source io.Reader) (blobstore.BlobRef, error) {
	if err := s.checkOpen(); err != nil {
		return blobstore.BlobRef{}, err
	}
	if source == nil || strings.TrimSpace(request.TenantID) == "" {
		return blobstore.BlobRef{}, errors.New("tenant and reader are required")
	}
	if err := ctx.Err(); err != nil {
		return blobstore.BlobRef{}, err
	}
	tenantID, err := scope.Tenant(ctx)
	if err != nil || tenantID != strings.TrimSpace(request.TenantID) {
		if err != nil {
			return blobstore.BlobRef{}, err
		}
		return blobstore.BlobRef{}, scope.ErrMissingTenant
	}
	max := request.MaxBytes
	if max <= 0 || max > s.maxBytes {
		max = s.maxBytes
	}
	plaintext, err := readBounded(ctx, source, max)
	if err != nil {
		return blobstore.BlobRef{}, err
	}
	digestBytes := sha256.Sum256(plaintext)
	digest := hex.EncodeToString(digestBytes[:])
	stored := append([]byte(nil), plaintext...)
	if strings.TrimSpace(request.EncryptionKey) != "" {
		if s.encryptor == nil {
			return blobstore.BlobRef{}, errors.New("encryption key requested but blob encryptor is unavailable")
		}
		stored, err = s.encryptor.Seal(ctx, strings.TrimSpace(request.EncryptionKey), plaintext)
		if err != nil {
			return blobstore.BlobRef{}, fmt.Errorf("encrypt blob: %w", err)
		}
	}
	storedDigest := digestBytesHex(stored)
	ref := blobstore.BlobRef{TenantID: tenantID, Digest: digest, Size: int64(len(plaintext)), MediaType: request.MediaType, EncryptionKey: strings.TrimSpace(request.EncryptionKey), Classification: request.Classification, RetainUntil: request.RetainUntil.UTC()}
	now := s.clock.Now().UTC()
	var retain any
	if !ref.RetainUntil.IsZero() {
		retain = ref.RetainUntil.UnixMicro()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return blobstore.BlobRef{}, fmt.Errorf("begin PostgreSQL blob write: %w", err)
	}
	defer tx.Rollback()
	var existing blobstore.BlobMetadata
	var existingRetain sql.NullInt64
	var existingCreated int64
	var existingStoredDigest sql.NullString
	var existingStored []byte
	err = tx.QueryRowContext(ctx, `SELECT size_bytes, media_type, encryption_key, classification,
		retain_until_us, created_at_us, tombstoned, legal_hold, tombstone, stored_digest, payload
		FROM runtime_blobs WHERE tenant_id=$1 AND digest=$2 FOR UPDATE`, tenantID, digest).
		Scan(&existing.Size, &existing.MediaType, &existing.EncryptionKey, &existing.Classification,
			&existingRetain, &existingCreated, &existing.Tombstoned, &existing.LegalHold, &existing.Tombstone, &existingStoredDigest, &existingStored)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO runtime_blobs
			(tenant_id, digest, size_bytes, media_type, encryption_key, classification,
			 retain_until_us, created_at_us, tombstoned, legal_hold, tombstone, stored_digest, payload)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,false,$9,'',$10,$11)`, tenantID, digest, ref.Size,
			ref.MediaType, ref.EncryptionKey, ref.Classification, retain, now.UnixMicro(), request.LegalHold, storedDigest, stored)
		if err != nil {
			return blobstore.BlobRef{}, fmt.Errorf("insert PostgreSQL blob: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return blobstore.BlobRef{}, fmt.Errorf("commit PostgreSQL blob: %w", err)
		}
		return ref, nil
	}
	if err != nil {
		return blobstore.BlobRef{}, fmt.Errorf("lock PostgreSQL blob: %w", err)
	}
	if existingStoredDigest.Valid {
		existing.StoredDigest = strings.TrimSpace(existingStoredDigest.String)
	}
	if existing.Tombstoned {
		return blobstore.BlobRef{}, blobstore.ErrTombstoned
	}
	if existingRetain.Valid {
		existing.RetainUntil = time.UnixMicro(existingRetain.Int64).UTC()
	}
	existing.CreatedAt = time.UnixMicro(existingCreated).UTC()
	if existing.Size != ref.Size || existing.MediaType != ref.MediaType || existing.EncryptionKey != ref.EncryptionKey || existing.Classification != ref.Classification || !existing.RetainUntil.Equal(ref.RetainUntil) {
		return blobstore.BlobRef{}, blobstore.ErrConflict
	}
	if existing.LegalHold != request.LegalHold {
		return blobstore.BlobRef{}, blobstore.ErrConflict
	}
	existing.TenantID, existing.Digest = tenantID, digest
	if err := s.verifyStoredPayload(ctx, existing, existingStored); err != nil {
		return blobstore.BlobRef{}, err
	}
	if err := tx.Commit(); err != nil {
		return blobstore.BlobRef{}, fmt.Errorf("commit PostgreSQL blob replay: %w", err)
	}
	return ref, nil
}

func (s *Store) Open(ctx context.Context, ref blobstore.BlobRef) (io.ReadCloser, error) {
	plaintext, metadata, err := s.read(ctx, ref)
	if err != nil {
		return nil, err
	}
	if metadata.Tombstoned {
		return nil, blobstore.ErrTombstoned
	}
	return io.NopCloser(bytes.NewReader(plaintext)), nil
}

func (s *Store) Stat(ctx context.Context, ref blobstore.BlobRef) (blobstore.BlobMetadata, error) {
	_, metadata, err := s.read(ctx, ref)
	return metadata, err
}

func (s *Store) Tombstone(ctx context.Context, ref blobstore.BlobRef, reason string) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	tenantID, err := scope.Tenant(ctx)
	if err != nil {
		return err
	}
	if tenantID != ref.TenantID || !validDigest(ref.Digest) {
		return blobstore.ErrNotFound
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("tombstone reason is required")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE runtime_blobs SET tombstoned=true, tombstone=$1
		WHERE tenant_id=$2 AND digest=$3 AND legal_hold=false AND tombstoned=false`, reason, tenantID, ref.Digest)
	if err != nil {
		return fmt.Errorf("tombstone PostgreSQL blob: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 1 {
		return nil
	}
	meta, statErr := s.Stat(ctx, ref)
	if errors.Is(statErr, blobstore.ErrNotFound) {
		return statErr
	}
	if errors.Is(statErr, blobstore.ErrTombstoned) {
		if meta.Tombstone == reason {
			return nil
		}
		return blobstore.ErrConflict
	}
	if statErr != nil {
		return statErr
	}
	if meta.LegalHold {
		return blobstore.ErrLegalHold
	}
	if meta.Tombstoned && meta.Tombstone == reason {
		return nil
	}
	return blobstore.ErrConflict
}

// List returns metadata for a tenant. It is intentionally outside the narrow
// BlobStore interface so lifecycle workers can perform mark-and-sweep without
// acquiring arbitrary SQL access.
func (s *Store) List(ctx context.Context, tenantID string) ([]blobstore.BlobMetadata, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	scoped, err := scope.Tenant(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(tenantID) == "" {
		tenantID = scoped
	}
	if tenantID != scoped {
		return nil, scope.ErrMissingTenant
	}
	rows, err := s.db.QueryContext(ctx, `SELECT tenant_id, digest, size_bytes, media_type,
		encryption_key, classification, retain_until_us, created_at_us, tombstoned, legal_hold, tombstone, stored_digest, payload
		FROM runtime_blobs WHERE tenant_id=$1 ORDER BY digest`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list PostgreSQL blobs: %w", err)
	}
	defer rows.Close()
	result := make([]blobstore.BlobMetadata, 0)
	for rows.Next() {
		var item blobstore.BlobMetadata
		var retain sql.NullInt64
		var created int64
		var storedDigest sql.NullString
		var stored []byte
		// The inventory is a lifecycle trust boundary. Verify the content while
		// scanning so GC cannot turn a corrupted row into a deletion proof.
		if err := rows.Scan(&item.TenantID, &item.Digest, &item.Size, &item.MediaType, &item.EncryptionKey, &item.Classification, &retain, &created, &item.Tombstoned, &item.LegalHold, &item.Tombstone, &storedDigest, &stored); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL blob metadata: %w", err)
		}
		if storedDigest.Valid {
			item.StoredDigest = strings.TrimSpace(storedDigest.String)
		}
		if retain.Valid {
			item.RetainUntil = time.UnixMicro(retain.Int64).UTC()
		}
		item.CreatedAt = time.UnixMicro(created).UTC()
		if err := s.verifyStoredPayload(ctx, item, stored); err != nil {
			return nil, fmt.Errorf("verify PostgreSQL blob %s: %w", item.Digest, err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// Purge permanently removes a previously tombstoned blob. It is separate
// from Tombstone so retention workers can produce an auditable two-phase proof.
func (s *Store) Purge(ctx context.Context, ref blobstore.BlobRef, reason string) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tenantID, err := scope.Tenant(ctx)
	if err != nil {
		return err
	}
	if tenantID != ref.TenantID || !validDigest(ref.Digest) || strings.TrimSpace(reason) == "" {
		return blobstore.ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin PostgreSQL blob purge: %w", err)
	}
	defer tx.Rollback()
	var item blobstore.BlobMetadata
	var retain sql.NullInt64
	var created int64
	var storedDigest sql.NullString
	var stored []byte
	err = tx.QueryRowContext(ctx, `SELECT tenant_id, digest, size_bytes, media_type,
		encryption_key, classification, retain_until_us, created_at_us, tombstoned, legal_hold, tombstone, stored_digest, payload
		FROM runtime_blobs WHERE tenant_id=$1 AND digest=$2 FOR UPDATE`, tenantID, ref.Digest).
		Scan(&item.TenantID, &item.Digest, &item.Size, &item.MediaType, &item.EncryptionKey, &item.Classification,
			&retain, &created, &item.Tombstoned, &item.LegalHold, &item.Tombstone, &storedDigest, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return blobstore.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock PostgreSQL blob for purge: %w", err)
	}
	if retain.Valid {
		item.RetainUntil = time.UnixMicro(retain.Int64).UTC()
	}
	item.CreatedAt = time.UnixMicro(created).UTC()
	if storedDigest.Valid {
		item.StoredDigest = strings.TrimSpace(storedDigest.String)
	}
	if item.Size != ref.Size || item.Digest != ref.Digest || item.TenantID != tenantID {
		return blobstore.ErrConflict
	}
	if item.LegalHold {
		return blobstore.ErrLegalHold
	}
	if !item.Tombstoned {
		return blobstore.ErrNotTombstoned
	}
	if item.Tombstone != strings.TrimSpace(reason) {
		return blobstore.ErrConflict
	}
	if err := s.verifyStoredPayload(ctx, item, stored); err != nil {
		return fmt.Errorf("verify PostgreSQL blob before purge: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM runtime_blobs
		WHERE tenant_id=$1 AND digest=$2 AND tombstoned=true AND legal_hold=false AND tombstone=$3`, tenantID, ref.Digest, strings.TrimSpace(reason))
	if err != nil {
		return fmt.Errorf("purge PostgreSQL blob: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		if err != nil {
			return fmt.Errorf("verify PostgreSQL blob purge result: %w", err)
		}
		return blobstore.ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit PostgreSQL blob purge: %w", err)
	}
	return nil
}

func (s *Store) read(ctx context.Context, ref blobstore.BlobRef) ([]byte, blobstore.BlobMetadata, error) {
	if err := s.checkOpen(); err != nil {
		return nil, blobstore.BlobMetadata{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, blobstore.BlobMetadata{}, err
	}
	tenantID, err := scope.Tenant(ctx)
	if err != nil {
		return nil, blobstore.BlobMetadata{}, err
	}
	if tenantID != ref.TenantID || !validDigest(ref.Digest) || ref.Size < 0 {
		return nil, blobstore.BlobMetadata{}, blobstore.ErrNotFound
	}
	var item blobstore.BlobMetadata
	var retain sql.NullInt64
	var created int64
	var storedDigest sql.NullString
	var stored []byte
	err = s.db.QueryRowContext(ctx, `SELECT tenant_id, digest, size_bytes, media_type,
		encryption_key, classification, retain_until_us, created_at_us, tombstoned, legal_hold, tombstone, stored_digest, payload
		FROM runtime_blobs WHERE tenant_id=$1 AND digest=$2`, tenantID, ref.Digest).
		Scan(&item.TenantID, &item.Digest, &item.Size, &item.MediaType, &item.EncryptionKey, &item.Classification,
			&retain, &created, &item.Tombstoned, &item.LegalHold, &item.Tombstone, &storedDigest, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, blobstore.BlobMetadata{}, blobstore.ErrNotFound
	}
	if err != nil {
		return nil, blobstore.BlobMetadata{}, fmt.Errorf("read PostgreSQL blob: %w", err)
	}
	if retain.Valid {
		item.RetainUntil = time.UnixMicro(retain.Int64).UTC()
	}
	item.CreatedAt = time.UnixMicro(created).UTC()
	if storedDigest.Valid {
		item.StoredDigest = strings.TrimSpace(storedDigest.String)
	}
	if ref.Size > 0 && item.Size != ref.Size {
		return nil, item, blobstore.ErrConflict
	}
	if item.Tombstoned {
		return nil, item, blobstore.ErrTombstoned
	}
	plaintext, err := s.plaintext(ctx, item, stored)
	if err != nil {
		return nil, item, err
	}
	return plaintext, item, nil
}

func (s *Store) verifyStoredPayload(ctx context.Context, metadata blobstore.BlobMetadata, stored []byte) error {
	if metadata.StoredDigest == "" {
		if metadata.Tombstoned {
			return errors.New("tombstoned blob is missing stored ciphertext digest")
		}
		// Rows written before stored_digest was introduced can still be
		// validated while their key is available. They are not eligible for a
		// deletion proof until a subsequent write records the digest.
	} else if !validDigest(metadata.StoredDigest) || digestBytesHex(stored) != strings.ToLower(metadata.StoredDigest) {
		return errors.New("blob integrity mismatch: stored digest")
	}
	if metadata.Tombstoned {
		// The ciphertext digest is independently verifiable after key
		// destruction, so inventory never needs to decrypt a tombstone.
		return nil
	}
	_, err := s.plaintext(ctx, metadata, stored)
	return err
}

func (s *Store) plaintext(ctx context.Context, metadata blobstore.BlobMetadata, stored []byte) ([]byte, error) {
	plaintext := append([]byte(nil), stored...)
	var err error
	if metadata.EncryptionKey != "" {
		if s.encryptor == nil {
			return nil, errors.New("encrypted blob requires an encryptor")
		}
		plaintext, err = s.encryptor.Open(ctx, metadata.EncryptionKey, stored)
		if err != nil {
			return nil, fmt.Errorf("decrypt blob: %w", err)
		}
	}
	if int64(len(plaintext)) != metadata.Size {
		return nil, errors.New("blob integrity mismatch: size")
	}
	digest := sha256.Sum256(plaintext)
	if hex.EncodeToString(digest[:]) != strings.ToLower(metadata.Digest) {
		return nil, errors.New("blob integrity mismatch: digest")
	}
	return append([]byte(nil), plaintext...), nil
}

func readBounded(ctx context.Context, source io.Reader, max int64) ([]byte, error) {
	if max <= 0 {
		return nil, blobstore.ErrLimit
	}
	reader := &contextReader{ctx: ctx, reader: source}
	data, err := io.ReadAll(io.LimitReader(reader, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, blobstore.ErrLimit
	}
	return data, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func validDigest(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func digestBytesHex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

var _ blobstore.BlobStore = (*Store)(nil)
var _ blobstore.Inventory = (*Store)(nil)
