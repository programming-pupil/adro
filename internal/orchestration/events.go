package orchestration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/adro-project/adro/internal/harness"
	"time"
)

type Event struct {
	ID             string          `json:"event_id"`
	PlanID         string          `json:"plan_id"`
	WorkspaceID    string          `json:"workspace_id"`
	RunID          string          `json:"run_id,omitempty"`
	NodeID         string          `json:"node_id,omitempty"`
	AttemptID      string          `json:"attempt_id,omitempty"`
	Sequence       int64           `json:"sequence"`
	Type           string          `json:"event_type"`
	Payload        json.RawMessage `json:"payload"`
	PayloadHash    string          `json:"payload_hash"`
	PreviousHash   string          `json:"previous_hash,omitempty"`
	EnvelopeHash   string          `json:"envelope_hash"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	FencingToken   int64           `json:"fencing_token"`
	CreatedAt      time.Time       `json:"created_at"`
}

func NewEvent(previous *Event, planID, workspaceID, typ, idempotency string, payload any) (Event, error) {
	if planID == "" || workspaceID == "" || typ == "" {
		return Event{}, errors.New("plan, workspace and event type are required")
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	h := sha256.Sum256(b)
	e := Event{ID: fmt.Sprintf("%s-%d", planID, time.Now().UnixNano()), PlanID: planID, WorkspaceID: workspaceID, Sequence: 1, Type: typ, Payload: b, PayloadHash: hex.EncodeToString(h[:]), IdempotencyKey: idempotency, CreatedAt: time.Now().UTC()}
	if previous != nil {
		e.Sequence = previous.Sequence + 1
		e.PreviousHash = previous.EnvelopeHash
	}
	e.EnvelopeHash = eventDigest(e)
	return e, nil
}
func eventDigest(e Event) string {
	cp := e
	cp.EnvelopeHash = ""
	b, _ := json.Marshal(cp)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func ValidateEventChain(events []Event, planID, workspaceID string) error {
	var prev string
	for i, e := range events {
		if e.PlanID != planID || e.WorkspaceID != workspaceID {
			return errors.New("event scope mismatch")
		}
		if e.Sequence != int64(i+1) {
			return fmt.Errorf("event sequence gap at %d", e.Sequence)
		}
		if e.PreviousHash != prev {
			return fmt.Errorf("event previous hash mismatch at %d", e.Sequence)
		}
		if eventDigest(e) != e.EnvelopeHash {
			return fmt.Errorf("event envelope hash mismatch at %d", e.Sequence)
		}
		h := sha256.Sum256(e.Payload)
		if hex.EncodeToString(h[:]) != e.PayloadHash {
			return fmt.Errorf("event payload hash mismatch at %d", e.Sequence)
		}
		prev = e.EnvelopeHash
	}
	return nil
}

func ReplayProjection(plan RequirementExecutionPlan, events []Event) (PlanProjection, error) {
	p, err := NewProjection(plan)
	if err != nil {
		return PlanProjection{}, err
	}
	if err := ValidateEventChain(events, plan.ID, plan.WorkspaceID); err != nil {
		return PlanProjection{}, err
	}
	for _, e := range events {
		switch e.Type {
		case "attempt.started":
			var x struct {
				NodeID, AttemptID string
				AttemptNo         int
				Lease             Lease
				Context           harness.ContextEnvelope
			}
			if err := json.Unmarshal(e.Payload, &x); err != nil {
				return PlanProjection{}, err
			}
			if _, err := p.StartAttempt(plan, x.NodeID, x.AttemptID, x.AttemptNo, x.Lease, x.Context, TransitionInput{PlanRevision: plan.Revision, LeaseToken: x.Lease.FencingToken, IdempotencyKey: e.IdempotencyKey, PayloadHash: e.PayloadHash}); err != nil {
				return PlanProjection{}, err
			}
		case "attempt.finished":
			var x struct {
				AttemptID, Event string
				Result           StructuredResult
				Failure          *FailureReason
			}
			if err := json.Unmarshal(e.Payload, &x); err != nil {
				return PlanProjection{}, err
			}
			if _, err := p.FinishAttempt(plan, x.AttemptID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: e.FencingToken, Event: x.Event, Result: x.Result, Failure: x.Failure, IdempotencyKey: e.IdempotencyKey}); err != nil {
				return PlanProjection{}, err
			}
		default:
			return PlanProjection{}, fmt.Errorf("unsupported replay event %q", e.Type)
		}
	}
	return p, nil
}
