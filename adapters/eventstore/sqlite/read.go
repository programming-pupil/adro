package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/adro-project/adro/core/event"
	"github.com/adro-project/adro/core/ids"
	"github.com/adro-project/adro/ports/eventstore"
)

const maxReadLimit = 1000

func (s *Store) Read(ctx context.Context, streamID string, after int64, limit int) ([]event.Envelope, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	if err := ids.Validate("stream", streamID); err != nil {
		return nil, err
	}
	if after < 0 || limit < 1 || limit > maxReadLimit {
		return nil, errors.New("event read requires after >= 0 and limit between 1 and 1000")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin sqlite event read: %w", err)
	}
	defer tx.Rollback()
	var headSequence int64
	var headDigest string
	err = tx.QueryRowContext(ctx, `SELECT current_sequence, head_digest FROM event_streams WHERE stream_id=?`, streamID).Scan(&headSequence, &headDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return []event.Envelope{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read sqlite event stream head: %w", err)
	}
	if after >= headSequence {
		return []event.Envelope{}, nil
	}
	previousDigest := ""
	if after > 0 {
		if err := tx.QueryRowContext(ctx, `SELECT envelope_digest FROM event_records
			WHERE stream_id=? AND sequence=?`, streamID, after).Scan(&previousDigest); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, eventstore.ErrCorrupt
			}
			return nil, fmt.Errorf("read sqlite previous event digest: %w", err)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT tenant_id, sequence, event_id, event_type, idempotency_key,
		payload_digest, previous_digest, envelope_digest, envelope_json, committed_at_us
		FROM event_records WHERE stream_id=? AND sequence>? ORDER BY sequence LIMIT ?`, streamID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("read sqlite events: %w", err)
	}
	defer rows.Close()

	expectedSequence := after + 1
	events := make([]event.Envelope, 0, limit)
	for rows.Next() {
		var tenantID, eventID, eventType, idempotencyKey, payloadDigest, storedPrevious, envelopeDigest string
		var sequence, committedAtMicros int64
		var raw []byte
		if err := rows.Scan(&tenantID, &sequence, &eventID, &eventType, &idempotencyKey, &payloadDigest,
			&storedPrevious, &envelopeDigest, &raw, &committedAtMicros); err != nil {
			return nil, fmt.Errorf("scan sqlite event: %w", err)
		}
		var envelope event.Envelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, eventstore.ErrCorrupt
		}
		if envelope.StreamID != streamID || envelope.TenantID != tenantID || envelope.Sequence != sequence ||
			envelope.EventID != eventID || envelope.EventType != eventType || envelope.IdempotencyKey != idempotencyKey ||
			envelope.PayloadDigest != payloadDigest || envelope.CommittedAt.UnixMicro() != committedAtMicros ||
			envelope.PreviousDigest != storedPrevious || envelope.EnvelopeDigest != envelopeDigest ||
			sequence != expectedSequence || storedPrevious != previousDigest || envelope.Verify() != nil {
			return nil, eventstore.ErrCorrupt
		}
		events = append(events, envelope)
		expectedSequence++
		previousDigest = envelopeDigest
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite events: %w", err)
	}
	if len(events) == 0 {
		return nil, eventstore.ErrCorrupt
	}
	last := events[len(events)-1]
	if len(events) < limit && last.Sequence < headSequence {
		return nil, eventstore.ErrCorrupt
	}
	if last.Sequence == headSequence && last.EnvelopeDigest != headDigest {
		return nil, eventstore.ErrCorrupt
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit sqlite event read: %w", err)
	}
	return events, nil
}

func (s *Store) Head(ctx context.Context, streamID string) (int64, string, error) {
	if err := s.checkOpen(); err != nil {
		return 0, "", err
	}
	if err := ids.Validate("stream", streamID); err != nil {
		return 0, "", err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, "", fmt.Errorf("begin sqlite event head read: %w", err)
	}
	defer tx.Rollback()
	var sequence int64
	var digest string
	err = tx.QueryRowContext(ctx, `SELECT current_sequence, head_digest FROM event_streams WHERE stream_id=?`, streamID).Scan(&sequence, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("read sqlite event head: %w", err)
	}
	if sequence == 0 {
		if digest != "" {
			return 0, "", eventstore.ErrCorrupt
		}
		return 0, "", nil
	}
	var storedDigest string
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT envelope_digest, envelope_json FROM event_records
		WHERE stream_id=? AND sequence=?`, streamID, sequence).Scan(&storedDigest, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, "", eventstore.ErrCorrupt
		}
		return 0, "", fmt.Errorf("read sqlite head event: %w", err)
	}
	var envelope event.Envelope
	if json.Unmarshal(raw, &envelope) != nil || envelope.Verify() != nil || envelope.Sequence != sequence ||
		envelope.StreamID != streamID || storedDigest != digest || envelope.EnvelopeDigest != digest {
		return 0, "", eventstore.ErrCorrupt
	}
	if err := tx.Commit(); err != nil {
		return 0, "", fmt.Errorf("commit sqlite event head read: %w", err)
	}
	return sequence, digest, nil
}

func (s *Store) Subscribe(ctx context.Context, request eventstore.Subscription) (eventstore.EventSubscription, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	if err := validateScope(request.TenantID, request.StreamID); err != nil {
		return nil, err
	}
	if request.After < 0 {
		return nil, errors.New("subscription cursor cannot be negative")
	}
	if request.BufferSize <= 0 {
		request.BufferSize = 64
	}
	if request.BufferSize > maxReadLimit {
		return nil, errors.New("subscription buffer cannot exceed 1000 events")
	}
	var tenantID string
	err := s.db.QueryRowContext(ctx, `SELECT tenant_id FROM event_streams WHERE stream_id=?`, request.StreamID).Scan(&tenantID)
	if err == nil && tenantID != request.TenantID {
		return nil, errors.New("event stream tenant mismatch")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("validate sqlite event subscription: %w", err)
	}

	subscriptionContext, cancel := context.WithCancel(ctx)
	subscription := &subscription{
		events: make(chan event.Envelope, request.BufferSize), errors: make(chan error, 1), cancel: cancel,
	}
	go s.runSubscription(subscriptionContext, subscription, request)
	return subscription, nil
}

func (s *Store) runSubscription(ctx context.Context, subscription *subscription, request eventstore.Subscription) {
	defer close(subscription.events)
	defer close(subscription.errors)
	ticker := s.clockTicker()
	defer ticker.Stop()
	after := request.After
	for {
		events, err := s.Read(ctx, request.StreamID, after, request.BufferSize)
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, eventstore.ErrClosed) {
				select {
				case subscription.errors <- err:
				default:
				}
			}
			return
		}
		for _, envelope := range events {
			if envelope.TenantID != request.TenantID {
				select {
				case subscription.errors <- errors.New("event stream tenant mismatch"):
				default:
				}
				return
			}
			select {
			case subscription.events <- envelope:
				after = envelope.Sequence
			case <-ctx.Done():
				return
			case <-s.rootContext.Done():
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-s.rootContext.Done():
			return
		case <-ticker.C:
		}
	}
}

type subscription struct {
	events    chan event.Envelope
	errors    chan error
	cancel    context.CancelFunc
	closeOnce sync.Once
}

func (s *subscription) Events() <-chan event.Envelope { return s.events }
func (s *subscription) Errors() <-chan error          { return s.errors }
func (s *subscription) Close() error {
	s.closeOnce.Do(s.cancel)
	return nil
}

func (s *Store) clockTicker() *time.Ticker { return time.NewTicker(s.pollInterval) }
