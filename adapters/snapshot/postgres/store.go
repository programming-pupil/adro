// Package postgres implements the durable PostgreSQL SnapshotStore adapter.
// Snapshots are replay accelerators only: event history remains authoritative.
package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adro-project/adro/core"
	"github.com/adro-project/adro/ports/scope"
	"github.com/adro-project/adro/ports/snapshot"
	_ "github.com/lib/pq"
)

const defaultMaxPayload = 64 << 20

type Options struct {
	Clock      core.Clock
	MaxPayload int64
}

type Store struct {
	db         *sql.DB
	clock      core.Clock
	maxPayload int64
	ownsDB     bool
	closed     atomic.Bool
	closeMu    sync.Once
}

func Open(dsn string, options Options) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("PostgreSQL snapshot store DSN is required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL snapshot store: %w", err)
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
		return nil, errors.New("PostgreSQL snapshot database handle is required")
	}
	if options.Clock == nil {
		options.Clock = core.SystemClock{}
	}
	if options.MaxPayload <= 0 {
		options.MaxPayload = defaultMaxPayload
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping PostgreSQL snapshot store: %w", err)
	}
	if err := EnsureSchema(db); err != nil {
		return nil, err
	}
	return &Store{db: db, clock: options.Clock, maxPayload: options.MaxPayload}, nil
}

// EnsureSchema creates only the adapter-owned table and can be called during a
// larger migration transaction by operators that do not use New.
func EnsureSchema(db interface {
	Exec(query string, args ...any) (sql.Result, error)
}) error {
	if db == nil {
		return errors.New("snapshot schema database handle is required")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS runtime_snapshots (
		tenant_id text NOT NULL,
		stream_id text NOT NULL,
		sequence bigint NOT NULL CHECK (sequence > 0),
		schema_version integer NOT NULL CHECK (schema_version > 0),
		encoding_version integer NOT NULL CHECK (encoding_version > 0),
		hash_algorithm text NOT NULL,
		hash_version integer NOT NULL CHECK (hash_version > 0),
		digest text NOT NULL,
		payload bytea NOT NULL,
		created_at_us bigint NOT NULL,
		PRIMARY KEY (tenant_id, stream_id)
	)`)
	if err != nil {
		return fmt.Errorf("create PostgreSQL snapshot schema: %w", err)
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
		return snapshot.ErrClosed
	}
	return nil
}

func (s *Store) Get(ctx context.Context, streamID string) (snapshot.Snapshot, error) {
	if err := s.checkOpen(); err != nil {
		return snapshot.Snapshot{}, err
	}
	tenantID, err := scope.Tenant(ctx)
	if err != nil {
		return snapshot.Snapshot{}, err
	}
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return snapshot.Snapshot{}, errors.New("stream_id is required")
	}
	var value snapshot.Snapshot
	var createdAtMicros int64
	err = s.db.QueryRowContext(ctx, `SELECT tenant_id, stream_id, sequence, schema_version,
		encoding_version, hash_algorithm, hash_version, digest, payload, created_at_us
		FROM runtime_snapshots WHERE tenant_id=$1 AND stream_id=$2`, tenantID, streamID).
		Scan(&value.TenantID, &value.StreamID, &value.Sequence, &value.SchemaVersion,
			&value.EncodingVersion, &value.HashAlgorithm, &value.HashVersion, &value.Digest,
			&value.Payload, &createdAtMicros)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot.Snapshot{}, snapshot.ErrNotFound
	}
	if err != nil {
		return snapshot.Snapshot{}, fmt.Errorf("read PostgreSQL snapshot: %w", err)
	}
	value.CreatedAt = time.UnixMicro(createdAtMicros).UTC()
	if err := validate(value, tenantID, streamID); err != nil {
		return snapshot.Snapshot{}, err
	}
	if err := validatePayloadSize(value.Payload, s.maxPayload); err != nil {
		return snapshot.Snapshot{}, err
	}
	return clone(value), nil
}

func (s *Store) Put(ctx context.Context, value snapshot.Snapshot, expectedSequence int64) error {
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
	now := value.CreatedAt.UTC()
	if now.IsZero() {
		now = s.clock.Now().UTC()
		value.CreatedAt = now
	}
	if err := validate(value, tenantID, value.StreamID); err != nil {
		return err
	}
	if err := validatePayloadSize(value.Payload, s.maxPayload); err != nil {
		return err
	}
	if expectedSequence < 0 {
		return errors.New("expected sequence cannot be negative")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin PostgreSQL snapshot write: %w", err)
	}
	defer tx.Rollback()
	var existing snapshot.Snapshot
	var createdAtMicros int64
	err = tx.QueryRowContext(ctx, `SELECT tenant_id, stream_id, sequence, schema_version,
		encoding_version, hash_algorithm, hash_version, digest, payload, created_at_us
		FROM runtime_snapshots WHERE tenant_id=$1 AND stream_id=$2 FOR UPDATE`, tenantID, value.StreamID).
		Scan(&existing.TenantID, &existing.StreamID, &existing.Sequence, &existing.SchemaVersion,
			&existing.EncodingVersion, &existing.HashAlgorithm, &existing.HashVersion, &existing.Digest,
			&existing.Payload, &createdAtMicros)
	if errors.Is(err, sql.ErrNoRows) {
		if expectedSequence != 0 {
			return snapshot.ErrConflict
		}
	} else if err != nil {
		return fmt.Errorf("lock PostgreSQL snapshot: %w", err)
	} else {
		existing.CreatedAt = time.UnixMicro(createdAtMicros).UTC()
		if err := validate(existing, tenantID, value.StreamID); err != nil {
			return err
		}
		if equalSnapshot(existing, value) {
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit PostgreSQL snapshot replay: %w", err)
			}
			return nil
		}
		if existing.Sequence != expectedSequence || value.Sequence <= existing.Sequence {
			return snapshot.ErrConflict
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO runtime_snapshots
		(tenant_id, stream_id, sequence, schema_version, encoding_version, hash_algorithm,
		hash_version, digest, payload, created_at_us)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (tenant_id, stream_id) DO UPDATE SET
		sequence=excluded.sequence, schema_version=excluded.schema_version,
		encoding_version=excluded.encoding_version, hash_algorithm=excluded.hash_algorithm,
		hash_version=excluded.hash_version, digest=excluded.digest, payload=excluded.payload,
		created_at_us=excluded.created_at_us`, tenantID, value.StreamID, value.Sequence,
		value.SchemaVersion, value.EncodingVersion, value.HashAlgorithm, value.HashVersion,
		value.Digest, value.Payload, now.UnixMicro())
	if err != nil {
		return fmt.Errorf("write PostgreSQL snapshot: %w", err)
	}
	return tx.Commit()
}

func (s *Store) Health(ctx context.Context) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	return s.db.PingContext(ctx)
}

func validate(value snapshot.Snapshot, tenantID, streamID string) error {
	if strings.TrimSpace(tenantID) == "" || value.TenantID != tenantID || strings.TrimSpace(value.StreamID) == "" || value.StreamID != streamID || value.Sequence < 1 || value.SchemaVersion < 1 || value.EncodingVersion < 1 || strings.TrimSpace(value.HashAlgorithm) == "" || value.HashVersion < 1 || value.CreatedAt.IsZero() || len(value.Payload) == 0 {
		return snapshot.ErrCorrupt
	}
	if !strings.EqualFold(value.HashAlgorithm, "sha256") {
		return snapshot.ErrCorrupt
	}
	digest := sha256.Sum256(value.Payload)
	if strings.ToLower(strings.TrimSpace(value.Digest)) != hex.EncodeToString(digest[:]) {
		return snapshot.ErrCorrupt
	}
	return nil
}

func validatePayloadSize(payload []byte, max int64) error {
	if max <= 0 {
		max = defaultMaxPayload
	}
	if int64(len(payload)) > max {
		return snapshot.ErrLimit
	}
	return nil
}

func equalSnapshot(a, b snapshot.Snapshot) bool {
	return a.TenantID == b.TenantID && a.StreamID == b.StreamID && a.Sequence == b.Sequence && a.SchemaVersion == b.SchemaVersion && a.EncodingVersion == b.EncodingVersion && a.HashAlgorithm == b.HashAlgorithm && a.HashVersion == b.HashVersion && strings.EqualFold(a.Digest, b.Digest) && string(a.Payload) == string(b.Payload)
}

func clone(value snapshot.Snapshot) snapshot.Snapshot {
	value.Payload = append([]byte(nil), value.Payload...)
	return value
}

var _ snapshot.Store = (*Store)(nil)
