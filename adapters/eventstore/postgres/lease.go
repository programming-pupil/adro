package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adro-project/adro/core/ids"
	"github.com/adro-project/adro/ports/eventstore"
	"github.com/adro-project/adro/ports/leasestore"
)

func (s *Store) Acquire(ctx context.Context, tenantID, streamID, owner string, ttl time.Duration) (leasestore.Lease, error) {
	if err := s.checkOpen(); err != nil {
		return leasestore.Lease{}, err
	}
	if err := validateLeaseInput(tenantID, streamID, owner, ttl); err != nil {
		return leasestore.Lease{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return leasestore.Lease{}, fmt.Errorf("begin PostgreSQL lease acquire: %w", err)
	}
	defer tx.Rollback()
	var token, expiresAt int64
	err = tx.QueryRowContext(ctx, `INSERT INTO event_leases
		(tenant_id, stream_id, owner, fencing_token, expires_at_us, updated_at_us)
		SELECT $1, $2, $3, 1, clock.now_us + $4, clock.now_us
		FROM (SELECT floor(extract(epoch FROM clock_timestamp()) * 1000000)::bigint AS now_us) AS clock
		ON CONFLICT (tenant_id, stream_id) DO NOTHING
		RETURNING fencing_token, expires_at_us`, tenantID, streamID, owner, ttl.Microseconds()).Scan(&token, &expiresAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return leasestore.Lease{}, fmt.Errorf("insert PostgreSQL lease: %w", err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		var currentOwner string
		var currentExpires int64
		if err := tx.QueryRowContext(ctx, `SELECT owner, fencing_token, expires_at_us FROM event_leases
			WHERE tenant_id=$1 AND stream_id=$2 FOR UPDATE`, tenantID, streamID).
			Scan(&currentOwner, &token, &currentExpires); err != nil {
			return leasestore.Lease{}, fmt.Errorf("lock PostgreSQL lease: %w", err)
		}
		now, err := databaseNowMicros(ctx, tx)
		if err != nil {
			return leasestore.Lease{}, err
		}
		if currentOwner != owner && currentExpires > now {
			return leasestore.Lease{}, leasestore.ErrBusy
		}
		token++
		expiresAt = now + ttl.Microseconds()
		if _, err := tx.ExecContext(ctx, `UPDATE event_leases SET owner=$1, fencing_token=$2,
			expires_at_us=$3, updated_at_us=$4 WHERE tenant_id=$5 AND stream_id=$6`,
			owner, token, expiresAt, now, tenantID, streamID); err != nil {
			return leasestore.Lease{}, fmt.Errorf("take over PostgreSQL lease: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return leasestore.Lease{}, fmt.Errorf("commit PostgreSQL lease acquire: %w", err)
	}
	return leasestore.Lease{TenantID: tenantID, StreamID: streamID, Owner: owner, FencingToken: token, ExpiresAt: time.UnixMicro(expiresAt).UTC()}, nil
}

func (s *Store) Renew(ctx context.Context, lease leasestore.Lease, ttl time.Duration) (leasestore.Lease, error) {
	if err := s.checkOpen(); err != nil {
		return leasestore.Lease{}, err
	}
	if err := validateLeaseInput(lease.TenantID, lease.StreamID, lease.Owner, ttl); err != nil {
		return leasestore.Lease{}, err
	}
	if lease.FencingToken <= 0 {
		return leasestore.Lease{}, errors.New("positive lease fencing token is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return leasestore.Lease{}, fmt.Errorf("begin PostgreSQL lease renew: %w", err)
	}
	defer tx.Rollback()
	var currentOwner string
	var currentToken, currentExpires int64
	err = tx.QueryRowContext(ctx, `SELECT owner, fencing_token, expires_at_us FROM event_leases
		WHERE tenant_id=$1 AND stream_id=$2 FOR UPDATE`, lease.TenantID, lease.StreamID).
		Scan(&currentOwner, &currentToken, &currentExpires)
	if errors.Is(err, sql.ErrNoRows) {
		return leasestore.Lease{}, leasestore.ErrLost
	}
	if err != nil {
		return leasestore.Lease{}, fmt.Errorf("lock PostgreSQL lease for renewal: %w", err)
	}
	now, err := databaseNowMicros(ctx, tx)
	if err != nil {
		return leasestore.Lease{}, err
	}
	if currentOwner != lease.Owner || currentToken != lease.FencingToken || currentExpires <= now {
		return leasestore.Lease{}, leasestore.ErrLost
	}
	expiresAt := now + ttl.Microseconds()
	result, err := tx.ExecContext(ctx, `UPDATE event_leases SET expires_at_us=$1, updated_at_us=$2
		WHERE tenant_id=$3 AND stream_id=$4 AND owner=$5 AND fencing_token=$6`,
		expiresAt, now, lease.TenantID, lease.StreamID, lease.Owner, lease.FencingToken)
	if err != nil {
		return leasestore.Lease{}, fmt.Errorf("renew PostgreSQL lease: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return leasestore.Lease{}, leasestore.ErrLost
	}
	if err := tx.Commit(); err != nil {
		return leasestore.Lease{}, fmt.Errorf("commit PostgreSQL lease renew: %w", err)
	}
	lease.ExpiresAt = time.UnixMicro(expiresAt).UTC()
	return lease, nil
}

func (s *Store) Release(ctx context.Context, lease leasestore.Lease) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	if err := validateScope(lease.TenantID, lease.StreamID); err != nil {
		return err
	}
	if strings.TrimSpace(lease.Owner) == "" || lease.FencingToken <= 0 {
		return errors.New("lease owner and positive fencing token are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin PostgreSQL lease release: %w", err)
	}
	defer tx.Rollback()
	var currentOwner string
	var currentToken, currentExpires int64
	err = tx.QueryRowContext(ctx, `SELECT owner, fencing_token, expires_at_us FROM event_leases
		WHERE tenant_id=$1 AND stream_id=$2 FOR UPDATE`, lease.TenantID, lease.StreamID).
		Scan(&currentOwner, &currentToken, &currentExpires)
	if errors.Is(err, sql.ErrNoRows) {
		return leasestore.ErrLost
	}
	if err != nil {
		return fmt.Errorf("lock PostgreSQL lease for release: %w", err)
	}
	now, err := databaseNowMicros(ctx, tx)
	if err != nil {
		return err
	}
	if currentOwner != lease.Owner || currentToken != lease.FencingToken || currentExpires <= now {
		return leasestore.ErrLost
	}
	result, err := tx.ExecContext(ctx, `UPDATE event_leases SET expires_at_us=0, updated_at_us=$1
		WHERE tenant_id=$2 AND stream_id=$3 AND owner=$4 AND fencing_token=$5`,
		now, lease.TenantID, lease.StreamID, lease.Owner, lease.FencingToken)
	if err != nil {
		return fmt.Errorf("release PostgreSQL lease: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return leasestore.ErrLost
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit PostgreSQL lease release: %w", err)
	}
	return nil
}

func assertLease(ctx context.Context, tx *sql.Tx, tenantID, streamID string, assertion eventstore.LeaseAssertion) error {
	if strings.TrimSpace(assertion.Owner) == "" || assertion.FencingToken <= 0 {
		return eventstore.ErrLeaseLost
	}
	var owner string
	var token, expiresAt int64
	err := tx.QueryRowContext(ctx, `SELECT owner, fencing_token, expires_at_us FROM event_leases
		WHERE tenant_id=$1 AND stream_id=$2 FOR UPDATE`, tenantID, streamID).Scan(&owner, &token, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return eventstore.ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("assert PostgreSQL append lease: %w", err)
	}
	now, err := databaseNowMicros(ctx, tx)
	if err != nil {
		return err
	}
	if owner != assertion.Owner || token != assertion.FencingToken || expiresAt <= now {
		return eventstore.ErrLeaseLost
	}
	return nil
}

func validateLeaseInput(tenantID, streamID, owner string, ttl time.Duration) error {
	if err := validateScope(tenantID, streamID); err != nil {
		return err
	}
	if err := ids.Validate("lease owner", owner); err != nil {
		return err
	}
	if ttl.Microseconds() <= 0 {
		return errors.New("lease TTL must be at least one microsecond")
	}
	return nil
}
