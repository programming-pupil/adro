package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	storecontract "github.com/adro-project/adro/adapters/eventstore/internal/contract"
	coreencoding "github.com/adro-project/adro/core/encoding"
	"github.com/adro-project/adro/core/event"
	"github.com/adro-project/adro/ports/eventstore"
)

func (s *Store) Append(ctx context.Context, request eventstore.AppendRequest) (eventstore.AppendResult, error) {
	if err := s.checkOpen(); err != nil {
		return eventstore.AppendResult{}, err
	}
	validated, err := storecontract.ValidateAppend(request)
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return eventstore.AppendResult{}, fmt.Errorf("begin PostgreSQL event append: %w", err)
	}
	defer tx.Rollback()

	committedAt := s.clock.Now().UTC().Truncate(time.Microsecond)
	if _, err := tx.ExecContext(ctx, `INSERT INTO event_streams
		(stream_id, tenant_id, workspace_id, current_sequence, head_digest, updated_at_us)
		VALUES ($1, $2, $3, 0, '', $4) ON CONFLICT (stream_id) DO NOTHING`,
		request.StreamID, validated.TenantID, validated.WorkspaceID, committedAt.UnixMicro()); err != nil {
		return eventstore.AppendResult{}, fmt.Errorf("ensure PostgreSQL event stream: %w", err)
	}
	currentSequence, headDigest, existingTenant, existingWorkspace, err := loadStreamForUpdate(ctx, tx, request.StreamID)
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	if replay, found, err := findReplay(ctx, tx, request, validated.Digest); err != nil {
		return eventstore.AppendResult{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return eventstore.AppendResult{}, fmt.Errorf("commit PostgreSQL replay read: %w", err)
		}
		return replay, nil
	}
	if existingTenant != validated.TenantID || existingWorkspace != validated.WorkspaceID {
		return eventstore.AppendResult{}, errors.New("event stream tenant or workspace mismatch")
	}
	if currentSequence != request.ExpectedSequence {
		return eventstore.AppendResult{}, eventstore.ErrConflict
	}
	if request.Lease != nil {
		if err := assertLease(ctx, tx, validated.TenantID, request.StreamID, *request.Lease); err != nil {
			return eventstore.AppendResult{}, err
		}
	}

	envelopes, err := storecontract.CommitEvents(request, committedAt, s.ids, headDigest)
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	lastSequence := envelopes[len(envelopes)-1].Sequence
	headDigest = envelopes[len(envelopes)-1].EnvelopeDigest
	if request.Snapshot != nil {
		if request.Snapshot.Sequence != lastSequence {
			return eventstore.AppendResult{}, errors.New("snapshot sequence must equal append terminal sequence")
		}
		if storecontract.DigestBytes(request.Snapshot.Payload) != request.Snapshot.Digest {
			return eventstore.AppendResult{}, errors.New("snapshot digest does not match payload")
		}
	}

	for _, envelope := range envelopes {
		raw, err := coreencoding.Marshal(envelope)
		if err != nil {
			return eventstore.AppendResult{}, fmt.Errorf("encode committed event: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO event_records
			(tenant_id, stream_id, sequence, event_id, event_type, idempotency_key, append_digest,
			 payload_digest, previous_digest, envelope_digest, envelope_json, committed_at_us)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			envelope.TenantID, envelope.StreamID, envelope.Sequence, envelope.EventID, envelope.EventType,
			envelope.IdempotencyKey, validated.Digest, envelope.PayloadDigest, envelope.PreviousDigest,
			envelope.EnvelopeDigest, raw, envelope.CommittedAt.UnixMicro()); err != nil {
			return eventstore.AppendResult{}, fmt.Errorf("insert PostgreSQL event: %w", err)
		}
	}
	if request.Snapshot != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO event_snapshots
			(tenant_id, stream_id, sequence, digest, payload, updated_at_us)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (tenant_id, stream_id) DO UPDATE SET
			sequence=excluded.sequence, digest=excluded.digest, payload=excluded.payload, updated_at_us=excluded.updated_at_us`,
			validated.TenantID, request.StreamID, request.Snapshot.Sequence, request.Snapshot.Digest,
			request.Snapshot.Payload, committedAt.UnixMicro()); err != nil {
			return eventstore.AppendResult{}, fmt.Errorf("write PostgreSQL event snapshot: %w", err)
		}
	}
	for index, message := range request.Outbox {
		if _, err := tx.ExecContext(ctx, `INSERT INTO runtime_event_outbox
			(tenant_id, stream_id, sequence, ordinal, topic, message_key, payload, created_at_us)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, validated.TenantID, request.StreamID,
			lastSequence, index, message.Topic, message.Key, message.Payload, committedAt.UnixMicro()); err != nil {
			return eventstore.AppendResult{}, fmt.Errorf("insert PostgreSQL event outbox: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE event_streams
		SET current_sequence=$1, head_digest=$2, updated_at_us=$3
		WHERE stream_id=$4 AND tenant_id=$5 AND current_sequence=$6`,
		lastSequence, headDigest, committedAt.UnixMicro(), request.StreamID, validated.TenantID, request.ExpectedSequence)
	if err != nil {
		return eventstore.AppendResult{}, fmt.Errorf("advance PostgreSQL event stream: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return eventstore.AppendResult{}, eventstore.ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return eventstore.AppendResult{}, fmt.Errorf("commit PostgreSQL event append: %w", err)
	}
	return eventstore.AppendResult{FirstSequence: envelopes[0].Sequence, LastSequence: lastSequence, Events: envelopes}, nil
}

func loadStreamForUpdate(ctx context.Context, tx *sql.Tx, streamID string) (int64, string, string, string, error) {
	var sequence int64
	var digest, tenantID, workspaceID string
	if err := tx.QueryRowContext(ctx, `SELECT current_sequence, head_digest, tenant_id, workspace_id
		FROM event_streams WHERE stream_id=$1 FOR UPDATE`, streamID).Scan(&sequence, &digest, &tenantID, &workspaceID); err != nil {
		return 0, "", "", "", fmt.Errorf("lock PostgreSQL event stream: %w", err)
	}
	return sequence, digest, tenantID, workspaceID, nil
}

func findReplay(ctx context.Context, tx *sql.Tx, request eventstore.AppendRequest, requestDigest string) (eventstore.AppendResult, bool, error) {
	envelopes := make([]event.Envelope, 0, len(request.Events))
	for _, item := range request.Events {
		var storedDigest, eventID, eventType, idempotencyKey, payloadDigest, previousDigest, envelopeDigest string
		var sequence, committedAtMicros int64
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT sequence, event_id, event_type, idempotency_key,
			append_digest, payload_digest, previous_digest, envelope_digest, envelope_json, committed_at_us
			FROM event_records WHERE tenant_id=$1 AND stream_id=$2 AND idempotency_key=$3`,
			item.TenantID, request.StreamID, item.IdempotencyKey).
			Scan(&sequence, &eventID, &eventType, &idempotencyKey, &storedDigest, &payloadDigest,
				&previousDigest, &envelopeDigest, &raw, &committedAtMicros)
		if errors.Is(err, sql.ErrNoRows) {
			if len(envelopes) == 0 {
				continue
			}
			return eventstore.AppendResult{}, false, eventstore.ErrIdempotencyConflict
		}
		if err != nil {
			return eventstore.AppendResult{}, false, fmt.Errorf("read PostgreSQL idempotency record: %w", err)
		}
		if storedDigest != requestDigest {
			return eventstore.AppendResult{}, false, eventstore.ErrIdempotencyConflict
		}
		var envelope event.Envelope
		if err := json.Unmarshal(raw, &envelope); err != nil || envelope.VerifyStored() != nil {
			return eventstore.AppendResult{}, false, eventstore.ErrCorrupt
		}
		if envelope.StreamID != request.StreamID || envelope.TenantID != item.TenantID ||
			envelope.Sequence != sequence || envelope.EventID != eventID || envelope.EventType != eventType ||
			envelope.IdempotencyKey != idempotencyKey || envelope.PayloadDigest != payloadDigest ||
			envelope.PreviousDigest != previousDigest || envelope.EnvelopeDigest != envelopeDigest ||
			envelope.CommittedAt.UnixMicro() != committedAtMicros {
			return eventstore.AppendResult{}, false, eventstore.ErrCorrupt
		}
		envelopes = append(envelopes, envelope)
	}
	if len(envelopes) == 0 {
		return eventstore.AppendResult{}, false, nil
	}
	if len(envelopes) != len(request.Events) {
		return eventstore.AppendResult{}, false, eventstore.ErrIdempotencyConflict
	}
	sort.Slice(envelopes, func(i, j int) bool { return envelopes[i].Sequence < envelopes[j].Sequence })
	for index, envelope := range envelopes {
		if envelope.Sequence != request.ExpectedSequence+int64(index)+1 {
			return eventstore.AppendResult{}, false, eventstore.ErrIdempotencyConflict
		}
	}
	lastSequence := envelopes[len(envelopes)-1].Sequence
	if err := verifyReplayOutbox(ctx, tx, request, lastSequence); err != nil {
		return eventstore.AppendResult{}, false, err
	}
	if request.Snapshot != nil {
		var currentSequence int64
		if err := tx.QueryRowContext(ctx, `SELECT current_sequence FROM event_streams WHERE stream_id=$1`, request.StreamID).Scan(&currentSequence); err != nil {
			return eventstore.AppendResult{}, false, eventstore.ErrCorrupt
		}
		if currentSequence == lastSequence {
			var sequence int64
			var digest string
			var payload []byte
			err := tx.QueryRowContext(ctx, `SELECT sequence, digest, payload FROM event_snapshots
				WHERE tenant_id=$1 AND stream_id=$2`, request.Events[0].TenantID, request.StreamID).
				Scan(&sequence, &digest, &payload)
			if err != nil || sequence != request.Snapshot.Sequence || digest != request.Snapshot.Digest || string(payload) != string(request.Snapshot.Payload) {
				return eventstore.AppendResult{}, false, eventstore.ErrCorrupt
			}
		}
	}
	return eventstore.AppendResult{FirstSequence: envelopes[0].Sequence, LastSequence: lastSequence, Events: envelopes}, true, nil
}

func verifyReplayOutbox(ctx context.Context, tx *sql.Tx, request eventstore.AppendRequest, sequence int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT topic, message_key, payload FROM runtime_event_outbox
		WHERE tenant_id=$1 AND stream_id=$2 AND sequence=$3 ORDER BY ordinal`, request.Events[0].TenantID, request.StreamID, sequence)
	if err != nil {
		return fmt.Errorf("read PostgreSQL replay outbox: %w", err)
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		if index >= len(request.Outbox) {
			return eventstore.ErrCorrupt
		}
		var topic, key string
		var payload []byte
		if err := rows.Scan(&topic, &key, &payload); err != nil {
			return fmt.Errorf("scan PostgreSQL replay outbox: %w", err)
		}
		expected := request.Outbox[index]
		if topic != expected.Topic || key != expected.Key || string(payload) != string(expected.Payload) {
			return eventstore.ErrCorrupt
		}
		index++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate PostgreSQL replay outbox: %w", err)
	}
	if index != len(request.Outbox) {
		return eventstore.ErrCorrupt
	}
	return nil
}
