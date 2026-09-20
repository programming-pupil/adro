// Package eventstore persists policy decisions in the authoritative EventStore.
package eventstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/adro-project/adro/core/event"
	corepolicy "github.com/adro-project/adro/core/policy"
	eventstoreport "github.com/adro-project/adro/ports/eventstore"
	policyport "github.com/adro-project/adro/ports/policy"
)

const EventTypeDecisionRecorded = "policy.decision.recorded"

type Recorder struct {
	store eventstoreport.Store
}

func New(store eventstoreport.Store) (*Recorder, error) {
	if store == nil {
		return nil, errors.New("policy decision recorder requires an EventStore")
	}
	return &Recorder{store: store}, nil
}

func (r *Recorder) RecordDecision(ctx context.Context, record corepolicy.DecisionRecord) error {
	if r == nil || r.store == nil {
		return errors.New("policy decision recorder is unavailable")
	}
	digest, err := corepolicy.DecisionDigest(record)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode policy decision: %w", err)
	}
	classification := string(record.Sensitivity)
	if classification == "" {
		classification = string(corepolicy.SensitivityInternal)
	}
	streamID := "policy:" + digest
	_, err = r.store.Append(ctx, eventstoreport.AppendRequest{
		StreamID: streamID, ExpectedSequence: 0,
		Events: []event.Uncommitted{{
			StreamID: streamID, EventType: EventTypeDecisionRecorded,
			TenantID: record.TenantID, WorkspaceID: record.WorkspaceID,
			Actor:         event.Actor{Type: "policy_subject", ID: record.ActorID},
			CorrelationID: digest, IdempotencyKey: digest,
			OccurredAt: record.EvaluatedAt, Classification: classification, Payload: payload,
		}},
	})
	if err != nil {
		return fmt.Errorf("persist policy decision: %w", err)
	}
	return nil
}

func (r *Recorder) ReadDecision(ctx context.Context, digest string) (corepolicy.DecisionRecord, error) {
	if r == nil || r.store == nil {
		return corepolicy.DecisionRecord{}, errors.New("policy decision recorder is unavailable")
	}
	streamID := "policy:" + digest
	events, err := r.store.Read(ctx, streamID, 0, 2)
	if err != nil {
		return corepolicy.DecisionRecord{}, fmt.Errorf("read policy decision: %w", err)
	}
	if len(events) == 0 {
		return corepolicy.DecisionRecord{}, policyport.ErrDecisionNotFound
	}
	if len(events) != 1 || events[0].EventType != EventTypeDecisionRecorded {
		return corepolicy.DecisionRecord{}, eventstoreport.ErrCorrupt
	}
	var record corepolicy.DecisionRecord
	if err := json.Unmarshal(events[0].Payload, &record); err != nil {
		return corepolicy.DecisionRecord{}, eventstoreport.ErrCorrupt
	}
	actual, err := corepolicy.DecisionDigest(record)
	classification := string(record.Sensitivity)
	if classification == "" {
		classification = string(corepolicy.SensitivityInternal)
	}
	envelope := events[0]
	if err != nil || actual != digest || envelope.Verify() != nil ||
		envelope.StreamID != streamID || envelope.TenantID != record.TenantID || envelope.WorkspaceID != record.WorkspaceID ||
		envelope.Actor.Type != "policy_subject" || envelope.Actor.ID != record.ActorID ||
		envelope.CorrelationID != digest || envelope.IdempotencyKey != digest ||
		!envelope.OccurredAt.Equal(record.EvaluatedAt) || envelope.Classification != classification {
		return corepolicy.DecisionRecord{}, eventstoreport.ErrCorrupt
	}
	return record, nil
}
