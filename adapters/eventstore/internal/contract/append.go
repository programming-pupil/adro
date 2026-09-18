// Package contract contains deterministic EventStore append preparation shared by SQL adapters.
package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adro-project/adro/core"
	coreencoding "github.com/adro-project/adro/core/encoding"
	"github.com/adro-project/adro/core/event"
	"github.com/adro-project/adro/core/ids"
	"github.com/adro-project/adro/ports/eventstore"
)

type appendIdentity struct {
	StreamID         string                     `json:"stream_id"`
	ExpectedSequence int64                      `json:"expected_sequence"`
	Events           []uncommittedIdentity      `json:"events"`
	Outbox           []eventstore.OutboxMessage `json:"outbox,omitempty"`
	Snapshot         *eventstore.SnapshotWrite  `json:"snapshot,omitempty"`
}

type uncommittedIdentity struct {
	StreamID       string          `json:"stream_id"`
	EventType      string          `json:"event_type"`
	TenantID       string          `json:"tenant_id"`
	WorkspaceID    string          `json:"workspace_id"`
	Actor          event.Actor     `json:"actor"`
	CorrelationID  string          `json:"correlation_id"`
	CausationID    string          `json:"causation_id,omitempty"`
	IdempotencyKey string          `json:"idempotency_key"`
	FencingToken   int64           `json:"fencing_token,omitempty"`
	OccurredAt     string          `json:"occurred_at"`
	Classification string          `json:"classification"`
	Payload        json.RawMessage `json:"payload"`
}

type ValidatedAppend struct {
	Digest      string
	TenantID    string
	WorkspaceID string
}

func ValidateAppend(request eventstore.AppendRequest) (ValidatedAppend, error) {
	if request.ExpectedSequence < 0 || len(request.Events) == 0 {
		return ValidatedAppend{}, errors.New("non-negative expected sequence and at least one event are required")
	}
	if err := ids.Validate("stream", request.StreamID); err != nil {
		return ValidatedAppend{}, err
	}
	tenantID, workspaceID := request.Events[0].TenantID, request.Events[0].WorkspaceID
	if err := ids.Validate("tenant", tenantID); err != nil {
		return ValidatedAppend{}, err
	}
	if err := ids.Validate("workspace", workspaceID); err != nil {
		return ValidatedAppend{}, err
	}
	seenKeys := make(map[string]struct{}, len(request.Events))
	identity := appendIdentity{StreamID: request.StreamID, ExpectedSequence: request.ExpectedSequence, Outbox: request.Outbox, Snapshot: request.Snapshot}
	for index, item := range request.Events {
		if item.StreamID != request.StreamID || item.TenantID != tenantID || item.WorkspaceID != workspaceID {
			return ValidatedAppend{}, fmt.Errorf("event %d does not match append stream scope", index)
		}
		if strings.TrimSpace(item.IdempotencyKey) == "" {
			return ValidatedAppend{}, fmt.Errorf("event %d idempotency key is required", index)
		}
		if _, duplicate := seenKeys[item.IdempotencyKey]; duplicate {
			return ValidatedAppend{}, fmt.Errorf("event %d repeats an idempotency key", index)
		}
		seenKeys[item.IdempotencyKey] = struct{}{}
		payload, err := coreencoding.Canonicalize(item.Payload)
		if err != nil {
			return ValidatedAppend{}, fmt.Errorf("canonicalize event %d payload: %w", index, err)
		}
		identity.Events = append(identity.Events, uncommittedIdentity{
			StreamID: item.StreamID, EventType: item.EventType, TenantID: item.TenantID, WorkspaceID: item.WorkspaceID,
			Actor: item.Actor, CorrelationID: item.CorrelationID, CausationID: item.CausationID,
			IdempotencyKey: item.IdempotencyKey, FencingToken: item.FencingToken,
			OccurredAt:     item.OccurredAt.UTC().Truncate(time.Microsecond).Format("2006-01-02T15:04:05.000000Z"),
			Classification: item.Classification, Payload: payload,
		})
	}
	for index, message := range request.Outbox {
		if strings.TrimSpace(message.Topic) == "" || strings.TrimSpace(message.Key) == "" {
			return ValidatedAppend{}, fmt.Errorf("outbox message %d topic and key are required", index)
		}
	}
	digest, err := coreencoding.Digest(identity)
	if err != nil {
		return ValidatedAppend{}, err
	}
	return ValidatedAppend{Digest: digest, TenantID: tenantID, WorkspaceID: workspaceID}, nil
}

func CommitEvents(request eventstore.AppendRequest, committedAt time.Time, ids core.IDGenerator, previousDigest string) ([]event.Envelope, error) {
	envelopes := make([]event.Envelope, 0, len(request.Events))
	for index, uncommitted := range request.Events {
		envelope, err := event.Commit(uncommitted, event.CommitMetadata{
			Sequence: request.ExpectedSequence + int64(index) + 1,
			EventID:  ids.NewID("event"), CommittedAt: committedAt, PreviousDigest: previousDigest,
		})
		if err != nil {
			return nil, fmt.Errorf("commit event %d: %w", index, err)
		}
		envelopes = append(envelopes, envelope)
		previousDigest = envelope.EnvelopeDigest
	}
	return envelopes, nil
}

func DigestBytes(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
