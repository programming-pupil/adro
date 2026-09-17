package sqlite

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
	tx, err := beginImmediate(ctx, s.db)
	if err != nil {
		return leasestore.Lease{}, fmt.Errorf("begin sqlite lease acquire: %w", err)
	}
	defer tx.rollback()

	now := s.clock.Now().UTC().Truncate(time.Microsecond)
	var currentOwner string
	var token, expiresAtMicros int64
	err = tx.conn.QueryRowContext(ctx, `SELECT owner, fencing_token, expires_at_us
		FROM event_leases WHERE tenant_id=? AND stream_id=?`, tenantID, streamID).Scan(&currentOwner, &token, &expiresAtMicros)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		token = 1
	case err != nil:
		return leasestore.Lease{}, fmt.Errorf("read sqlite lease: %w", err)
	case currentOwner != owner && expiresAtMicros > now.UnixMicro():
		return leasestore.Lease{}, leasestore.ErrBusy
	default:
		token++
	}
	expiresAt := now.Add(ttl).Truncate(time.Microsecond)
	if _, err := tx.conn.ExecContext(ctx, `INSERT INTO event_leases
		(tenant_id, stream_id, owner, fencing_token, expires_at_us, updated_at_us)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (tenant_id, stream_id) DO UPDATE SET
		owner=excluded.owner, fencing_token=excluded.fencing_token,
		expires_at_us=excluded.expires_at_us, updated_at_us=excluded.updated_at_us`,
		tenantID, streamID, owner, token, expiresAt.UnixMicro(), now.UnixMicro()); err != nil {
		return leasestore.Lease{}, fmt.Errorf("write sqlite lease: %w", err)
	}
	if err := tx.commit(ctx); err != nil {
		return leasestore.Lease{}, fmt.Errorf("commit sqlite lease acquire: %w", err)
	}
	return leasestore.Lease{TenantID: tenantID, StreamID: streamID, Owner: owner, FencingToken: token, ExpiresAt: expiresAt}, nil
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
	now := s.clock.Now().UTC().Truncate(time.Microsecond)
	expiresAt := now.Add(ttl).Truncate(time.Microsecond)
	result, err := s.db.ExecContext(ctx, `UPDATE event_leases
		SET expires_at_us=?, updated_at_us=?
		WHERE tenant_id=? AND stream_id=? AND owner=? AND fencing_token=? AND expires_at_us>?`,
		expiresAt.UnixMicro(), now.UnixMicro(), lease.TenantID, lease.StreamID, lease.Owner,
		lease.FencingToken, now.UnixMicro())
	if err != nil {
		return leasestore.Lease{}, fmt.Errorf("renew sqlite lease: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return leasestore.Lease{}, leasestore.ErrLost
	}
	lease.ExpiresAt = expiresAt
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
	now := s.clock.Now().UTC().Truncate(time.Microsecond)
	result, err := s.db.ExecContext(ctx, `UPDATE event_leases
		SET expires_at_us=0, updated_at_us=?
		WHERE tenant_id=? AND stream_id=? AND owner=? AND fencing_token=? AND expires_at_us>?`,
		now.UnixMicro(), lease.TenantID, lease.StreamID, lease.Owner, lease.FencingToken, now.UnixMicro())
	if err != nil {
		return fmt.Errorf("release sqlite lease: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return leasestore.ErrLost
	}
	return nil
}

func (s *Store) assertLease(ctx context.Context, conn *sql.Conn, tenantID, streamID string, assertion eventstore.LeaseAssertion) error {
	if strings.TrimSpace(assertion.Owner) == "" || assertion.FencingToken <= 0 {
		return eventstore.ErrLeaseLost
	}
	var owner string
	var token, expiresAtMicros int64
	err := conn.QueryRowContext(ctx, `SELECT owner, fencing_token, expires_at_us
		FROM event_leases WHERE tenant_id=? AND stream_id=?`, tenantID, streamID).Scan(&owner, &token, &expiresAtMicros)
	if errors.Is(err, sql.ErrNoRows) {
		return eventstore.ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("read sqlite append lease: %w", err)
	}
	if owner != assertion.Owner || token != assertion.FencingToken || expiresAtMicros <= s.clock.Now().UTC().UnixMicro() {
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
