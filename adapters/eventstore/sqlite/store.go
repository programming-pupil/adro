// Package sqlite implements the single-node reference EventStore backend.
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

	"github.com/adro-project/adro/core"
	"github.com/adro-project/adro/core/ids"
	"github.com/adro-project/adro/ports/eventstore"
	_ "modernc.org/sqlite"
)

const defaultPollInterval = 10 * time.Millisecond

type Options struct {
	Clock        core.Clock
	IDs          core.IDGenerator
	PollInterval time.Duration
}

type Store struct {
	db           *sql.DB
	clock        core.Clock
	ids          core.IDGenerator
	pollInterval time.Duration
	ownsDB       bool
	rootContext  context.Context
	cancel       context.CancelFunc
	closed       atomic.Bool
	closeOnce    sync.Once
}

// Open creates an on-disk SQLite reference store. The caller must close it.
func Open(path string, options Options) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("sqlite event store path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve sqlite event store path: %w", err)
	}
	dsn := (&url.URL{Scheme: "file", Path: absolute}).String() + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite event store: %w", err)
	}
	store, err := New(db, options)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	store.ownsDB = true
	return store, nil
}

// New initializes a store around a caller-owned database handle.
func New(db *sql.DB, options Options) (*Store, error) {
	if db == nil {
		return nil, errors.New("sqlite database handle is required")
	}
	if options.Clock == nil || options.IDs == nil {
		return nil, errors.New("sqlite event store clock and ID generator are required")
	}
	if options.PollInterval < 0 {
		return nil, errors.New("sqlite event store poll interval cannot be negative")
	}
	if options.PollInterval == 0 {
		options.PollInterval = defaultPollInterval
	}

	// The reference profile deliberately owns one writer connection. This also
	// ensures connection-scoped PRAGMAs apply to every operation.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, pragma := range []string{
		"PRAGMA foreign_keys=ON",
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=FULL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			return nil, fmt.Errorf("configure sqlite event store: %w", err)
		}
	}
	if err := createSchema(db); err != nil {
		return nil, err
	}
	root, cancel := context.WithCancel(context.Background())
	return &Store{
		db: db, clock: options.Clock, ids: options.IDs, pollInterval: options.PollInterval,
		rootContext: root, cancel: cancel,
	}, nil
}

func createSchema(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS event_streams (
			stream_id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			current_sequence INTEGER NOT NULL CHECK (current_sequence >= 0),
			head_digest TEXT NOT NULL,
			updated_at_us INTEGER NOT NULL,
			UNIQUE (tenant_id, stream_id)
		)`,
		`CREATE TABLE IF NOT EXISTS event_records (
			tenant_id TEXT NOT NULL,
			stream_id TEXT NOT NULL,
			sequence INTEGER NOT NULL CHECK (sequence > 0),
			event_id TEXT NOT NULL UNIQUE,
			event_type TEXT NOT NULL,
			idempotency_key TEXT NOT NULL DEFAULT '',
			append_digest TEXT NOT NULL,
			payload_digest TEXT NOT NULL,
			previous_digest TEXT NOT NULL DEFAULT '',
			envelope_digest TEXT NOT NULL,
			envelope_json BLOB NOT NULL,
			committed_at_us INTEGER NOT NULL,
			PRIMARY KEY (tenant_id, stream_id, sequence),
			FOREIGN KEY (stream_id) REFERENCES event_streams(stream_id),
			UNIQUE (tenant_id, stream_id, idempotency_key)
		)`,
		`CREATE INDEX IF NOT EXISTS event_records_stream_sequence_idx
			ON event_records (stream_id, sequence)`,
		`CREATE TABLE IF NOT EXISTS event_outbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id TEXT NOT NULL,
			stream_id TEXT NOT NULL,
			sequence INTEGER NOT NULL,
			ordinal INTEGER NOT NULL,
			topic TEXT NOT NULL,
			message_key TEXT NOT NULL,
			payload BLOB NOT NULL,
			created_at_us INTEGER NOT NULL,
			FOREIGN KEY (stream_id) REFERENCES event_streams(stream_id),
			UNIQUE (tenant_id, topic, message_key),
			UNIQUE (tenant_id, stream_id, sequence, ordinal)
		)`,
		`CREATE TABLE IF NOT EXISTS event_snapshots (
			tenant_id TEXT NOT NULL,
			stream_id TEXT NOT NULL,
			sequence INTEGER NOT NULL CHECK (sequence > 0),
			digest TEXT NOT NULL,
			payload BLOB NOT NULL,
			updated_at_us INTEGER NOT NULL,
			PRIMARY KEY (tenant_id, stream_id),
			FOREIGN KEY (stream_id) REFERENCES event_streams(stream_id)
		)`,
		`CREATE TABLE IF NOT EXISTS event_leases (
			tenant_id TEXT NOT NULL,
			stream_id TEXT NOT NULL,
			owner TEXT NOT NULL,
			fencing_token INTEGER NOT NULL CHECK (fencing_token > 0),
			expires_at_us INTEGER NOT NULL,
			updated_at_us INTEGER NOT NULL,
			PRIMARY KEY (tenant_id, stream_id)
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("create sqlite event store schema: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.cancel()
		if s.ownsDB {
			closeErr = s.db.Close()
		}
	})
	return closeErr
}

func (s *Store) checkOpen() error {
	if s == nil || s.closed.Load() {
		return eventstore.ErrClosed
	}
	return nil
}

func validateScope(tenantID, streamID string) error {
	if err := ids.Validate("tenant", tenantID); err != nil {
		return err
	}
	return ids.Validate("stream", streamID)
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
	if tx.done {
		return errors.New("sqlite transaction is already closed")
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
