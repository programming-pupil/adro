// Package sqlite implements the single-node durable ProjectionStore backend.
// Projection rows are rebuildable read models; the authoritative event stream
// remains outside this adapter.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adro-project/adro/adapters/projection/internal/contract"
	"github.com/adro-project/adro/core"
	"github.com/adro-project/adro/ports/projection"
	"github.com/adro-project/adro/ports/scope"
	_ "modernc.org/sqlite"
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

func Open(path string, options Options) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("sqlite projection store path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve sqlite projection store path: %w", err)
	}
	dsn := (&url.URL{Scheme: "file", Path: absolute}).String() + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite projection store: %w", err)
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
		return nil, errors.New("sqlite projection database handle is required")
	}
	if options.Clock == nil {
		options.Clock = core.SystemClock{}
	}
	if options.MaxPayload <= 0 {
		options.MaxPayload = contract.DefaultMaxPayload
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, pragma := range []string{
		"PRAGMA foreign_keys=ON",
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=FULL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			return nil, fmt.Errorf("configure sqlite projection store: %w", err)
		}
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
			tenant_id TEXT NOT NULL,
			projection_name TEXT NOT NULL,
			record_key TEXT NOT NULL,
			version INTEGER NOT NULL CHECK (version > 0),
			source_stream TEXT NOT NULL,
			source_sequence INTEGER NOT NULL CHECK (source_sequence > 0),
			digest TEXT NOT NULL,
			payload BLOB NOT NULL,
			updated_at_us INTEGER NOT NULL,
			PRIMARY KEY (tenant_id, projection_name, record_key)
		)`,
		`CREATE INDEX IF NOT EXISTS runtime_projections_source_idx
			ON runtime_projections (tenant_id, source_stream, source_sequence)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("create sqlite projection schema: %w", err)
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
	row := s.db.QueryRowContext(ctx, `SELECT tenant_id, projection_name, record_key,
		version, source_stream, source_sequence, digest, payload, updated_at_us
		FROM runtime_projections
		WHERE tenant_id=? AND projection_name=? AND record_key=?`,
		strings.TrimSpace(tenantID), strings.TrimSpace(projectionName), strings.TrimSpace(key))
	item, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return projection.Record{}, projection.ErrNotFound
	}
	if err != nil {
		return projection.Record{}, fmt.Errorf("read sqlite projection: %w", err)
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
	tx, err := beginImmediate(ctx, s.db)
	if err != nil {
		return fmt.Errorf("begin sqlite projection write: %w", err)
	}
	defer tx.rollback()
	current, found, err := readTx(ctx, tx.conn, item.TenantID, item.Projection, item.Key, s.maxPayload)
	if err != nil {
		return err
	}
	if !found {
		if expectedVersion != 0 || item.Version != 1 {
			return projection.ErrConflict
		}
		if err := insertTx(ctx, tx.conn, item); err != nil {
			return err
		}
	} else {
		if contract.Equivalent(current, item) && expectedVersion == current.Version {
			return tx.commit(ctx)
		}
		if current.SourceStream != item.SourceStream || item.SourceSequence <= current.SourceSequence || current.Version != expectedVersion || item.Version != current.Version+1 {
			return projection.ErrConflict
		}
		if err := updateTx(ctx, tx.conn, item, current.Version); err != nil {
			return err
		}
	}
	return tx.commit(ctx)
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
	tx, err := beginImmediate(ctx, s.db)
	if err != nil {
		return fmt.Errorf("begin sqlite projection delete: %w", err)
	}
	defer tx.rollback()
	current, found, err := readTx(ctx, tx.conn, strings.TrimSpace(tenantID), strings.TrimSpace(projectionName), strings.TrimSpace(key), s.maxPayload)
	if err != nil {
		return err
	}
	if !found {
		return projection.ErrNotFound
	}
	if current.Version != expectedVersion {
		return projection.ErrConflict
	}
	result, err := tx.conn.ExecContext(ctx, `DELETE FROM runtime_projections
		WHERE tenant_id=? AND projection_name=? AND record_key=? AND version=?`,
		current.TenantID, current.Projection, current.Key, current.Version)
	if err != nil {
		return fmt.Errorf("delete sqlite projection: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect sqlite projection delete: %w", err)
	}
	if rows != 1 {
		return projection.ErrConflict
	}
	return tx.commit(ctx)
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
		FROM runtime_projections WHERE tenant_id=? AND projection_name=? ORDER BY record_key`,
		strings.TrimSpace(tenantID), strings.TrimSpace(projectionName))
	if err != nil {
		return nil, fmt.Errorf("list sqlite projections: %w", err)
	}
	defer rows.Close()
	result := make([]projection.Record, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan sqlite projection: %w", err)
		}
		if err := contract.ValidateStored(item, tenantID, s.maxPayload); err != nil {
			return nil, err
		}
		result = append(result, contract.Clone(item))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite projections: %w", err)
	}
	return result, nil
}

func (s *Store) Health(ctx context.Context) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	return s.db.PingContext(ctx)
}

type immediateTx struct {
	conn *sql.Conn
	done bool
}

func beginImmediate(ctx context.Context, db *sql.DB) (*immediateTx, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &immediateTx{conn: conn}, nil
}

func (tx *immediateTx) commit(ctx context.Context) error {
	if tx == nil || tx.done {
		return errors.New("sqlite projection transaction is already closed")
	}
	tx.done = true
	_, err := tx.conn.ExecContext(ctx, "COMMIT")
	closeErr := tx.conn.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func (tx *immediateTx) rollback() {
	if tx == nil || tx.done {
		return
	}
	tx.done = true
	_, _ = tx.conn.ExecContext(context.Background(), "ROLLBACK")
	_ = tx.conn.Close()
}

func scan(row interface{ Scan(...any) error }) (projection.Record, error) {
	var item projection.Record
	var updatedAtMicros int64
	err := row.Scan(&item.TenantID, &item.Projection, &item.Key, &item.Version,
		&item.SourceStream, &item.SourceSequence, &item.Digest, &item.Payload, &updatedAtMicros)
	if err != nil {
		return projection.Record{}, err
	}
	item.UpdatedAt = time.UnixMicro(updatedAtMicros).UTC()
	return item, nil
}

func readTx(ctx context.Context, conn *sql.Conn, tenantID, projectionName, key string, maxPayload int64) (projection.Record, bool, error) {
	row := conn.QueryRowContext(ctx, `SELECT tenant_id, projection_name, record_key,
		version, source_stream, source_sequence, digest, payload, updated_at_us
		FROM runtime_projections
		WHERE tenant_id=? AND projection_name=? AND record_key=?`, tenantID, projectionName, key)
	item, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return projection.Record{}, false, nil
	}
	if err != nil {
		return projection.Record{}, false, fmt.Errorf("read sqlite projection for update: %w", err)
	}
	if err := contract.ValidateStored(item, tenantID, maxPayload); err != nil {
		return projection.Record{}, false, err
	}
	return item, true, nil
}

func insertTx(ctx context.Context, conn *sql.Conn, item projection.Record) error {
	_, err := conn.ExecContext(ctx, `INSERT INTO runtime_projections
		(tenant_id, projection_name, record_key, version, source_stream, source_sequence, digest, payload, updated_at_us)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, item.TenantID, item.Projection, item.Key,
		item.Version, item.SourceStream, item.SourceSequence, item.Digest, item.Payload, item.UpdatedAt.UnixMicro())
	if err != nil {
		return fmt.Errorf("insert sqlite projection: %w", err)
	}
	return nil
}

func updateTx(ctx context.Context, conn *sql.Conn, item projection.Record, expectedVersion int64) error {
	result, err := conn.ExecContext(ctx, `UPDATE runtime_projections SET
		version=?, source_stream=?, source_sequence=?, digest=?, payload=?, updated_at_us=?
		WHERE tenant_id=? AND projection_name=? AND record_key=? AND version=?`,
		item.Version, item.SourceStream, item.SourceSequence, item.Digest, item.Payload, item.UpdatedAt.UnixMicro(),
		item.TenantID, item.Projection, item.Key, expectedVersion)
	if err != nil {
		return fmt.Errorf("update sqlite projection: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect sqlite projection update: %w", err)
	}
	if rows != 1 {
		return projection.ErrConflict
	}
	return nil
}

var _ projection.Store = (*Store)(nil)
