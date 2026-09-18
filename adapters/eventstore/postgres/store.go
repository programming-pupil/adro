// Package postgres implements the production PostgreSQL EventStore backend.
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

	"github.com/adro-project/adro/core"
	"github.com/adro-project/adro/core/ids"
	"github.com/adro-project/adro/ports/eventstore"
	_ "github.com/lib/pq"
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

func Open(dsn string, options Options) (*Store, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, errors.New("PostgreSQL event store DSN is required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL event store: %w", err)
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(16)
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
		return nil, errors.New("PostgreSQL database handle is required")
	}
	if options.Clock == nil || options.IDs == nil {
		return nil, errors.New("PostgreSQL event store clock and ID generator are required")
	}
	if options.PollInterval < 0 {
		return nil, errors.New("PostgreSQL event store poll interval cannot be negative")
	}
	if options.PollInterval == 0 {
		options.PollInterval = defaultPollInterval
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping PostgreSQL event store: %w", err)
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
			stream_id text PRIMARY KEY,
			tenant_id text NOT NULL,
			workspace_id text NOT NULL,
			current_sequence bigint NOT NULL CHECK (current_sequence >= 0),
			head_digest text NOT NULL,
			updated_at_us bigint NOT NULL,
			UNIQUE (tenant_id, stream_id)
		)`,
		`CREATE TABLE IF NOT EXISTS event_records (
			tenant_id text NOT NULL,
			stream_id text NOT NULL,
			sequence bigint NOT NULL CHECK (sequence > 0),
			event_id text NOT NULL UNIQUE,
			event_type text NOT NULL,
			idempotency_key text NOT NULL,
			append_digest text NOT NULL,
			payload_digest text NOT NULL,
			previous_digest text NOT NULL DEFAULT '',
			envelope_digest text NOT NULL,
			envelope_json bytea NOT NULL,
			committed_at_us bigint NOT NULL,
			PRIMARY KEY (tenant_id, stream_id, sequence),
			FOREIGN KEY (tenant_id, stream_id) REFERENCES event_streams(tenant_id, stream_id),
			UNIQUE (tenant_id, stream_id, idempotency_key)
		)`,
		`CREATE INDEX IF NOT EXISTS event_records_stream_sequence_idx
			ON event_records (stream_id, sequence)`,
		`CREATE TABLE IF NOT EXISTS runtime_event_outbox (
			id bigserial PRIMARY KEY,
			tenant_id text NOT NULL,
			stream_id text NOT NULL,
			sequence bigint NOT NULL,
			ordinal integer NOT NULL,
			topic text NOT NULL,
			message_key text NOT NULL,
			payload bytea NOT NULL,
			created_at_us bigint NOT NULL,
			FOREIGN KEY (tenant_id, stream_id) REFERENCES event_streams(tenant_id, stream_id),
			UNIQUE (tenant_id, topic, message_key),
			UNIQUE (tenant_id, stream_id, sequence, ordinal)
		)`,
		`CREATE TABLE IF NOT EXISTS event_snapshots (
			tenant_id text NOT NULL,
			stream_id text NOT NULL,
			sequence bigint NOT NULL CHECK (sequence > 0),
			digest text NOT NULL,
			payload bytea NOT NULL,
			updated_at_us bigint NOT NULL,
			PRIMARY KEY (tenant_id, stream_id),
			FOREIGN KEY (tenant_id, stream_id) REFERENCES event_streams(tenant_id, stream_id)
		)`,
		`CREATE TABLE IF NOT EXISTS event_leases (
			tenant_id text NOT NULL,
			stream_id text NOT NULL,
			owner text NOT NULL,
			fencing_token bigint NOT NULL CHECK (fencing_token > 0),
			expires_at_us bigint NOT NULL,
			updated_at_us bigint NOT NULL,
			PRIMARY KEY (tenant_id, stream_id)
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("create PostgreSQL event store schema: %w", err)
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

func databaseNowMicros(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int64, error) {
	var now int64
	if err := queryer.QueryRowContext(ctx, `SELECT floor(extract(epoch FROM clock_timestamp()) * 1000000)::bigint`).Scan(&now); err != nil {
		return 0, fmt.Errorf("read PostgreSQL transaction time: %w", err)
	}
	return now, nil
}
