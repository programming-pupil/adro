// Package postgres implements the production PostgreSQL ProjectionStore
// backend. Rows are tenant-scoped, content-addressed and updated with an
// explicit version CAS so stale projection workers fail closed.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adro-project/adro/adapters/projection/internal/contract"
	"github.com/adro-project/adro/core"
	"github.com/adro-project/adro/ports/projection"
	"github.com/adro-project/adro/ports/scope"
	_ "github.com/lib/pq"
)

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
	closeOnce  sync.Once
}

func Open(dsn string, options Options) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("PostgreSQL projection store DSN is required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL projection store: %w", err)
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
		return nil, errors.New("PostgreSQL projection database handle is required")
	}
	if options.Clock == nil {
		options.Clock = core.SystemClock{}
	}
	if options.MaxPayload <= 0 {
		options.MaxPayload = contract.DefaultMaxPayload
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping PostgreSQL projection store: %w", err)
	}
	if err := EnsureSchema(db); err != nil {
		return nil, err
	}
	return &Store{db: db, clock: options.Clock, maxPayload: options.MaxPayload}, nil
}

func EnsureSchema(db interface {
	Exec(query string, args ...any) (sql.Result, error)
}) error {
	if db == nil {
		return errors.New("projection schema database handle is required")
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS runtime_projections (
			tenant_id text NOT NULL,
			projection_name text NOT NULL,
			record_key text NOT NULL,
			version bigint NOT NULL CHECK (version > 0),
			source_stream text NOT NULL,
			source_sequence bigint NOT NULL CHECK (source_sequence > 0),
			digest text NOT NULL,
			payload bytea NOT NULL,
			updated_at_us bigint NOT NULL,
			PRIMARY KEY (tenant_id, projection_name, record_key)
		)`,
		`CREATE INDEX IF NOT EXISTS runtime_projections_source_idx
			ON runtime_projections (tenant_id, source_stream, source_sequence)`,
		`CREATE TABLE IF NOT EXISTS runtime_projection_offsets (
			tenant_id text NOT NULL,
			projection_name text NOT NULL,
			partition_id text NOT NULL,
			last_sequence bigint NOT NULL CHECK (last_sequence >= 0),
			projection_digest text NOT NULL,
			updated_at_us bigint NOT NULL,
			PRIMARY KEY (tenant_id, projection_name, partition_id)
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("create PostgreSQL projection schema: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		if s.ownsDB {
			err = s.db.Close()
		}
	})
	return err
}

func (s *Store) checkOpen() error {
	if s == nil || s.closed.Load() {
		return projection.ErrClosed
	}
	return nil
}

func (s *Store) Get(ctx context.Context, tenantID, projectionName, key string) (projection.Record, error) {
	if err := s.checkOpen(); err != nil {
		return projection.Record{}, err
	}
	if err := ctx.Err(); err != nil {
		return projection.Record{}, err
	}
	if err := contract.RequireTenant(ctx, tenantID); err != nil {
		return projection.Record{}, err
	}
	if err := contract.ValidateIdentity(tenantID, projectionName, key); err != nil {
		return projection.Record{}, err
	}
	item, err := scan(s.db.QueryRowContext(ctx, `SELECT tenant_id, projection_name, record_key,
		version, source_stream, source_sequence, digest, payload, updated_at_us
		FROM runtime_projections
		WHERE tenant_id=$1 AND projection_name=$2 AND record_key=$3`,
		strings.TrimSpace(tenantID), strings.TrimSpace(projectionName), strings.TrimSpace(key)))
	if errors.Is(err, sql.ErrNoRows) {
		return projection.Record{}, projection.ErrNotFound
	}
	if err != nil {
		return projection.Record{}, fmt.Errorf("read PostgreSQL projection: %w", err)
	}
	if err := contract.ValidateStored(item, tenantID, s.maxPayload); err != nil {
		return projection.Record{}, err
	}
	return contract.Clone(item), nil
}

func (s *Store) Put(ctx context.Context, item projection.Record, expectedVersion int64) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if expectedVersion < 0 {
		return errors.New("projection expected version cannot be negative")
	}
	tenantID, err := scope.Tenant(ctx)
	if err != nil {
		return err
	}
	item = contract.Normalize(item, s.clock.Now())
	if item.Digest == "" {
		item.Digest = contract.Digest(item.Payload)
	}
	if err := contract.ValidateForWrite(item, tenantID, s.maxPayload); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin PostgreSQL projection write: %w", err)
	}
	defer tx.Rollback()
	current, found, err := readTx(ctx, tx, item.TenantID, item.Projection, item.Key, s.maxPayload)
	if err != nil {
		return err
	}
	if !found {
		if expectedVersion != 0 || item.Version != 1 {
			return projection.ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO runtime_projections
			(tenant_id, projection_name, record_key, version, source_stream, source_sequence, digest, payload, updated_at_us)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, item.TenantID, item.Projection, item.Key,
			item.Version, item.SourceStream, item.SourceSequence, item.Digest, item.Payload, item.UpdatedAt.UnixMicro()); err != nil {
			return fmt.Errorf("insert PostgreSQL projection: %w", err)
		}
	} else {
		if contract.Equivalent(current, item) && expectedVersion == current.Version {
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit PostgreSQL projection replay: %w", err)
			}
			return nil
		}
		if current.SourceStream != item.SourceStream || item.SourceSequence <= current.SourceSequence || current.Version != expectedVersion || item.Version != current.Version+1 {
			return projection.ErrConflict
		}
		result, err := tx.ExecContext(ctx, `UPDATE runtime_projections SET
			version=$1, source_stream=$2, source_sequence=$3, digest=$4, payload=$5, updated_at_us=$6
			WHERE tenant_id=$7 AND projection_name=$8 AND record_key=$9 AND version=$10`,
			item.Version, item.SourceStream, item.SourceSequence, item.Digest, item.Payload, item.UpdatedAt.UnixMicro(),
			item.TenantID, item.Projection, item.Key, expectedVersion)
		if err != nil {
			return fmt.Errorf("update PostgreSQL projection: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect PostgreSQL projection update: %w", err)
		}
		if rows != 1 {
			return projection.ErrConflict
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit PostgreSQL projection write: %w", err)
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, tenantID, projectionName, key string, expectedVersion int64) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if expectedVersion < 1 {
		return projection.ErrConflict
	}
	if err := contract.RequireTenant(ctx, tenantID); err != nil {
		return err
	}
	if err := contract.ValidateIdentity(tenantID, projectionName, key); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin PostgreSQL projection delete: %w", err)
	}
	defer tx.Rollback()
	current, found, err := readTx(ctx, tx, strings.TrimSpace(tenantID), strings.TrimSpace(projectionName), strings.TrimSpace(key), s.maxPayload)
	if err != nil {
		return err
	}
	if !found {
		return projection.ErrNotFound
	}
	if current.Version != expectedVersion {
		return projection.ErrConflict
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM runtime_projections
		WHERE tenant_id=$1 AND projection_name=$2 AND record_key=$3 AND version=$4`,
		current.TenantID, current.Projection, current.Key, current.Version)
	if err != nil {
		return fmt.Errorf("delete PostgreSQL projection: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect PostgreSQL projection delete: %w", err)
	}
	if rows != 1 {
		return projection.ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit PostgreSQL projection delete: %w", err)
	}
	return nil
}

func (s *Store) List(ctx context.Context, tenantID, projectionName string) ([]projection.Record, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := contract.RequireTenant(ctx, tenantID); err != nil {
		return nil, err
	}
	if err := contract.ValidateIdentity(tenantID, projectionName, "list-key"); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT tenant_id, projection_name, record_key,
		version, source_stream, source_sequence, digest, payload, updated_at_us
		FROM runtime_projections WHERE tenant_id=$1 AND projection_name=$2 ORDER BY record_key`,
		strings.TrimSpace(tenantID), strings.TrimSpace(projectionName))
	if err != nil {
		return nil, fmt.Errorf("list PostgreSQL projections: %w", err)
	}
	defer rows.Close()
	result := make([]projection.Record, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan PostgreSQL projection: %w", err)
		}
		if err := contract.ValidateStored(item, tenantID, s.maxPayload); err != nil {
			return nil, err
		}
		result = append(result, contract.Clone(item))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL projections: %w", err)
	}
	return result, nil
}

func (s *Store) GetOffset(ctx context.Context, tenantID, projectionName, partitionID string) (projection.Offset, error) {
	if err := s.checkOpen(); err != nil {
		return projection.Offset{}, err
	}
	if err := ctx.Err(); err != nil {
		return projection.Offset{}, err
	}
	if err := contract.RequireTenant(ctx, tenantID); err != nil {
		return projection.Offset{}, err
	}
	if err := contract.ValidateIdentity(tenantID, projectionName, partitionID); err != nil {
		return projection.Offset{}, err
	}
	item, err := scanOffset(s.db.QueryRowContext(ctx, `SELECT tenant_id, projection_name, partition_id,
		last_sequence, projection_digest, updated_at_us
		FROM runtime_projection_offsets
		WHERE tenant_id=$1 AND projection_name=$2 AND partition_id=$3`,
		strings.TrimSpace(tenantID), strings.TrimSpace(projectionName), strings.TrimSpace(partitionID)))
	if errors.Is(err, sql.ErrNoRows) {
		return projection.Offset{}, projection.ErrOffsetNotFound
	}
	if err != nil {
		return projection.Offset{}, fmt.Errorf("read PostgreSQL projection offset: %w", err)
	}
	if err := contract.ValidateOffset(item, tenantID, true); err != nil {
		return projection.Offset{}, err
	}
	return item, nil
}

func (s *Store) PutOffset(ctx context.Context, item projection.Offset, expectedSequence int64) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if expectedSequence < 0 {
		return projection.ErrOffsetConflict
	}
	tenantID, err := scope.Tenant(ctx)
	if err != nil {
		return err
	}
	item = contract.NormalizeOffset(item, s.clock.Now())
	if err := contract.ValidateOffset(item, tenantID, false); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin PostgreSQL projection offset write: %w", err)
	}
	defer tx.Rollback()
	current, found, err := readOffsetTx(ctx, tx, item.TenantID, item.Projection, item.PartitionID)
	if err != nil {
		return err
	}
	if !found {
		if expectedSequence != 0 {
			return projection.ErrOffsetConflict
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO runtime_projection_offsets
			(tenant_id, projection_name, partition_id, last_sequence, projection_digest, updated_at_us)
			VALUES ($1,$2,$3,$4,$5,$6)`, item.TenantID, item.Projection, item.PartitionID,
			item.LastSequence, item.ProjectionDigest, item.UpdatedAt.UnixMicro()); err != nil {
			return fmt.Errorf("insert PostgreSQL projection offset: %w", err)
		}
		return tx.Commit()
	}
	if current.TenantID != item.TenantID {
		return projection.ErrTenantMismatch
	}
	if current.LastSequence == item.LastSequence && current.ProjectionDigest == item.ProjectionDigest && expectedSequence == current.LastSequence {
		return tx.Commit()
	}
	if expectedSequence != current.LastSequence || item.LastSequence <= current.LastSequence {
		return projection.ErrOffsetConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE runtime_projection_offsets SET
		last_sequence=$1, projection_digest=$2, updated_at_us=$3
		WHERE tenant_id=$4 AND projection_name=$5 AND partition_id=$6 AND last_sequence=$7`,
		item.LastSequence, item.ProjectionDigest, item.UpdatedAt.UnixMicro(), item.TenantID,
		item.Projection, item.PartitionID, expectedSequence)
	if err != nil {
		return fmt.Errorf("update PostgreSQL projection offset: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect PostgreSQL projection offset update: %w", err)
	}
	if rows != 1 {
		return projection.ErrOffsetConflict
	}
	return tx.Commit()
}

func (s *Store) Health(ctx context.Context) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	return s.db.PingContext(ctx)
}

func readTx(ctx context.Context, tx *sql.Tx, tenantID, projectionName, key string, maxPayload int64) (projection.Record, bool, error) {
	item, err := scan(tx.QueryRowContext(ctx, `SELECT tenant_id, projection_name, record_key,
		version, source_stream, source_sequence, digest, payload, updated_at_us
		FROM runtime_projections
		WHERE tenant_id=$1 AND projection_name=$2 AND record_key=$3 FOR UPDATE`, tenantID, projectionName, key))
	if errors.Is(err, sql.ErrNoRows) {
		return projection.Record{}, false, nil
	}
	if err != nil {
		return projection.Record{}, false, fmt.Errorf("lock PostgreSQL projection: %w", err)
	}
	if err := contract.ValidateStored(item, tenantID, maxPayload); err != nil {
		return projection.Record{}, false, err
	}
	return item, true, nil
}

func readOffsetTx(ctx context.Context, tx *sql.Tx, tenantID, projectionName, partitionID string) (projection.Offset, bool, error) {
	item, err := scanOffset(tx.QueryRowContext(ctx, `SELECT tenant_id, projection_name, partition_id,
		last_sequence, projection_digest, updated_at_us
		FROM runtime_projection_offsets
		WHERE tenant_id=$1 AND projection_name=$2 AND partition_id=$3 FOR UPDATE`, tenantID, projectionName, partitionID))
	if errors.Is(err, sql.ErrNoRows) {
		return projection.Offset{}, false, nil
	}
	if err != nil {
		return projection.Offset{}, false, fmt.Errorf("lock PostgreSQL projection offset: %w", err)
	}
	if err := contract.ValidateOffset(item, tenantID, true); err != nil {
		return projection.Offset{}, false, err
	}
	return item, true, nil
}

func scan(row interface{ Scan(...any) error }) (projection.Record, error) {
	var item projection.Record
	var updatedAtMicros int64
	if err := row.Scan(&item.TenantID, &item.Projection, &item.Key, &item.Version,
		&item.SourceStream, &item.SourceSequence, &item.Digest, &item.Payload, &updatedAtMicros); err != nil {
		return projection.Record{}, err
	}
	item.UpdatedAt = time.UnixMicro(updatedAtMicros).UTC()
	return item, nil
}

func scanOffset(row interface{ Scan(...any) error }) (projection.Offset, error) {
	var item projection.Offset
	var updatedAtMicros int64
	if err := row.Scan(&item.TenantID, &item.Projection, &item.PartitionID, &item.LastSequence,
		&item.ProjectionDigest, &updatedAtMicros); err != nil {
		return projection.Offset{}, err
	}
	item.UpdatedAt = time.UnixMicro(updatedAtMicros).UTC()
	return item, nil
}

var _ projection.Store = (*Store)(nil)
var _ projection.OffsetStore = (*Store)(nil)
