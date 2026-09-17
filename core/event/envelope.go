// Package event defines the authoritative durable event envelope.
package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	coreencoding "github.com/adro-project/adro/core/encoding"
	"github.com/adro-project/adro/core/ids"
)

const SchemaVersion = 1

type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Envelope struct {
	StreamID       string                `json:"stream_id"`
	Sequence       int64                 `json:"sequence"`
	EventID        string                `json:"event_id"`
	EventType      string                `json:"event_type"`
	SchemaVersion  int                   `json:"schema_version"`
	TenantID       string                `json:"tenant_id"`
	WorkspaceID    string                `json:"workspace_id"`
	Actor          Actor                 `json:"actor"`
	CorrelationID  string                `json:"correlation_id"`
	CausationID    string                `json:"causation_id,omitempty"`
	IdempotencyKey string                `json:"idempotency_key,omitempty"`
	FencingToken   int64                 `json:"fencing_token,omitempty"`
	OccurredAt     time.Time             `json:"occurred_at"`
	CommittedAt    time.Time             `json:"committed_at"`
	Classification string                `json:"classification"`
	Payload        json.RawMessage       `json:"payload"`
	PayloadDigest  string                `json:"payload_digest"`
	PreviousDigest string                `json:"previous_digest,omitempty"`
	EnvelopeDigest string                `json:"envelope_digest"`
	Encoding       coreencoding.Identity `json:"encoding"`
}

type Uncommitted struct {
	StreamID       string
	EventType      string
	TenantID       string
	WorkspaceID    string
	Actor          Actor
	CorrelationID  string
	CausationID    string
	IdempotencyKey string
	FencingToken   int64
	OccurredAt     time.Time
	Classification string
	Payload        json.RawMessage
}

type CommitMetadata struct {
	Sequence       int64
	EventID        string
	CommittedAt    time.Time
	PreviousDigest string
}

func Commit(uncommitted Uncommitted, metadata CommitMetadata) (Envelope, error) {
	payload, err := coreencoding.Canonicalize(uncommitted.Payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("canonicalize event payload: %w", err)
	}
	envelope := Envelope{
		StreamID: uncommitted.StreamID, Sequence: metadata.Sequence, EventID: metadata.EventID,
		EventType: uncommitted.EventType, SchemaVersion: SchemaVersion,
		TenantID: uncommitted.TenantID, WorkspaceID: uncommitted.WorkspaceID, Actor: uncommitted.Actor,
		CorrelationID: uncommitted.CorrelationID, CausationID: uncommitted.CausationID,
		IdempotencyKey: uncommitted.IdempotencyKey, FencingToken: uncommitted.FencingToken,
		OccurredAt: normalizeTime(uncommitted.OccurredAt), CommittedAt: normalizeTime(metadata.CommittedAt),
		Classification: uncommitted.Classification, Payload: payload, PreviousDigest: metadata.PreviousDigest,
		Encoding: coreencoding.Current,
	}
	if err := envelope.validateRequired(); err != nil {
		return Envelope{}, err
	}
	envelope.PayloadDigest, err = coreencoding.DigestRaw(payload)
	if err != nil {
		return Envelope{}, err
	}
	envelope.EnvelopeDigest, err = envelope.digest()
	if err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

func (e Envelope) Verify() error {
	if err := e.validateRequired(); err != nil {
		return err
	}
	payloadDigest, err := coreencoding.DigestRaw(e.Payload)
	if err != nil {
		return err
	}
	if payloadDigest != e.PayloadDigest {
		return errors.New("event payload digest mismatch")
	}
	digest, err := e.digest()
	if err != nil {
		return err
	}
	if digest != e.EnvelopeDigest {
		return errors.New("event envelope digest mismatch")
	}
	return nil
}

func (e Envelope) validateRequired() error {
	for _, item := range []struct {
		namespace string
		value     string
	}{
		{namespace: "stream", value: e.StreamID},
		{namespace: "event", value: e.EventID},
		{namespace: "tenant", value: e.TenantID},
		{namespace: "workspace", value: e.WorkspaceID},
		{namespace: "actor", value: e.Actor.ID},
	} {
		if err := ids.Validate(item.namespace, item.value); err != nil {
			return err
		}
	}
	if e.Sequence < 1 || e.EventType == "" || e.CorrelationID == "" || e.Actor.Type == "" || e.Actor.ID == "" || e.Classification == "" {
		return errors.New("sequence, event_type, actor, correlation_id and classification are required")
	}
	if e.SchemaVersion != SchemaVersion || e.Encoding != coreencoding.Current {
		return errors.New("unsupported event schema or encoding identity")
	}
	if e.OccurredAt.IsZero() || e.CommittedAt.IsZero() {
		return errors.New("occurred_at and committed_at are required")
	}
	return nil
}

func (e Envelope) digest() (string, error) {
	return coreencoding.Digest(struct {
		StreamID       string                `json:"stream_id"`
		Sequence       int64                 `json:"sequence"`
		EventID        string                `json:"event_id"`
		EventType      string                `json:"event_type"`
		SchemaVersion  int                   `json:"schema_version"`
		TenantID       string                `json:"tenant_id"`
		WorkspaceID    string                `json:"workspace_id"`
		Actor          Actor                 `json:"actor"`
		CorrelationID  string                `json:"correlation_id"`
		CausationID    string                `json:"causation_id,omitempty"`
		IdempotencyKey string                `json:"idempotency_key,omitempty"`
		FencingToken   int64                 `json:"fencing_token,omitempty"`
		OccurredAt     string                `json:"occurred_at"`
		CommittedAt    string                `json:"committed_at"`
		Classification string                `json:"classification"`
		Payload        json.RawMessage       `json:"payload"`
		PayloadDigest  string                `json:"payload_digest"`
		PreviousDigest string                `json:"previous_digest,omitempty"`
		Encoding       coreencoding.Identity `json:"encoding"`
	}{
		StreamID: e.StreamID, Sequence: e.Sequence, EventID: e.EventID, EventType: e.EventType,
		SchemaVersion: e.SchemaVersion, TenantID: e.TenantID, WorkspaceID: e.WorkspaceID,
		Actor: e.Actor, CorrelationID: e.CorrelationID, CausationID: e.CausationID,
		IdempotencyKey: e.IdempotencyKey, FencingToken: e.FencingToken,
		OccurredAt: formatTime(e.OccurredAt), CommittedAt: formatTime(e.CommittedAt),
		Classification: e.Classification, Payload: e.Payload, PayloadDigest: e.PayloadDigest,
		PreviousDigest: e.PreviousDigest, Encoding: e.Encoding,
	})
}

func normalizeTime(value time.Time) time.Time { return value.UTC().Truncate(time.Microsecond) }

func formatTime(value time.Time) string {
	return normalizeTime(value).Format("2006-01-02T15:04:05.000000Z")
}
