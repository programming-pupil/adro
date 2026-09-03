package orchestration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/harness"
	"github.com/adro-project/adro/internal/telemetry"
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
	TraceParent    string          `json:"traceparent,omitempty"`
	TraceState     string          `json:"tracestate,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

func NewEvent(previous *Event, planID, workspaceID, typ, idempotency string, payload any) (Event, error) {
	return NewEventWithContext(context.Background(), previous, planID, workspaceID, typ, idempotency, payload)
}

func NewEventWithContext(ctx context.Context, previous *Event, planID, workspaceID, typ, idempotency string, payload any) (Event, error) {
	if planID == "" || workspaceID == "" || typ == "" {
		return Event{}, errors.New("plan, workspace and event type are required")
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	traceParent, traceState := telemetry.Carrier(ctx)
	e := Event{ID: domain.NewID(), PlanID: planID, WorkspaceID: workspaceID, Sequence: 1, Type: typ, Payload: b, PayloadHash: payloadDigest(b), IdempotencyKey: idempotency, TraceParent: traceParent, TraceState: traceState, CreatedAt: time.Now().UTC()}
	if previous != nil {
		e.Sequence = previous.Sequence + 1
		e.PreviousHash = previous.EnvelopeHash
	}
	e.EnvelopeHash = eventDigest(e)
	return e, nil
}

// Seal recomputes the tamper-evident hashes after a caller adds typed scope
// such as node, attempt, run or fencing data. NewEvent seals the base event,
// but those fields are intentionally populated by the executor only after the
// attempt has been reserved.
func (e *Event) Seal() {
	if e == nil {
		return
	}
	e.PayloadHash = payloadDigest(e.Payload)
	e.EnvelopeHash = eventDigest(*e)
}

func eventDigest(e Event) string {
	cp := e
	cp.EnvelopeHash = ""
	cp.Payload = canonicalPayload(cp.Payload)
	b, _ := json.Marshal(cp)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func ValidateEventChain(events []Event, planID, workspaceID string) error {
	var prev string
	for i, e := range events {
		if e.ID == "" || e.Type == "" || e.PlanID == "" || e.WorkspaceID == "" || len(e.Payload) == 0 || e.PayloadHash == "" || e.EnvelopeHash == "" {
			return fmt.Errorf("event %d is incomplete", i+1)
		}
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
		if payloadDigest(e.Payload) != e.PayloadHash {
			return fmt.Errorf("event payload hash mismatch at %d", e.Sequence)
		}
		if e.TraceParent != "" || e.TraceState != "" {
			if _, err := telemetry.ParseTraceParent(e.TraceParent, e.TraceState); err != nil {
				return fmt.Errorf("event trace context mismatch at %d: %w", e.Sequence, err)
			}
		}
		prev = e.EnvelopeHash
	}
	return nil
}

func canonicalPayload(payload json.RawMessage) json.RawMessage {
	var compact []byte
	buffer := make([]byte, 0, len(payload))
	out := bytes.NewBuffer(buffer)
	if err := json.Compact(out, payload); err != nil {
		return payload
	}
	compact = append(compact, out.Bytes()...)
	return compact
}

func payloadDigest(payload json.RawMessage) string {
	h := sha256.Sum256(canonicalPayload(payload))
	return hex.EncodeToString(h[:])
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
		case "plan.created", "plan.published":
			// Plan records are immutable snapshots persisted separately. These
			// lifecycle events are part of the chain but do not mutate the node
			// projection during replay.
			continue
		case "attempt.started":
			var x struct {
				NodeID              string                  `json:"node_id"`
				AttemptID           string                  `json:"attempt_id"`
				AttemptNo           int                     `json:"attempt_no"`
				Lease               Lease                   `json:"lease"`
				Context             harness.ContextEnvelope `json:"context"`
				DispatchPayloadHash string                  `json:"dispatch_payload_hash"`
				StartedAt           time.Time               `json:"started_at"`
				ChildPlanID         string                  `json:"child_plan_id"`
			}
			if err := json.Unmarshal(e.Payload, &x); err != nil {
				return PlanProjection{}, err
			}
			if x.StartedAt.IsZero() {
				x.StartedAt = e.CreatedAt
			}
			started, err := p.StartAttempt(plan, x.NodeID, x.AttemptID, x.AttemptNo, x.Lease, x.Context, TransitionInput{PlanRevision: plan.Revision, LeaseToken: x.Lease.FencingToken, IdempotencyKey: e.IdempotencyKey, PayloadHash: x.DispatchPayloadHash, Now: x.StartedAt})
			if err != nil {
				return PlanProjection{}, err
			}
			if x.ChildPlanID != "" {
				started.ChildPlanID = x.ChildPlanID
				p.Attempts[started.ID] = started
			}
		case "attempt.bound":
			var x struct {
				AttemptID   string `json:"attempt_id"`
				RunID       string `json:"run_id"`
				SessionID   string `json:"session_id"`
				WorkDir     string `json:"workdir"`
				ChildPlanID string `json:"child_plan_id"`
			}
			if err := json.Unmarshal(e.Payload, &x); err != nil {
				return PlanProjection{}, err
			}
			if x.AttemptID == "" || e.AttemptID != "" && e.AttemptID != x.AttemptID {
				return PlanProjection{}, ErrStaleAttempt
			}
			a, ok := p.Attempts[x.AttemptID]
			if !ok || a.Status != AttemptRunning || e.FencingToken != a.Lease.FencingToken {
				return PlanProjection{}, ErrStaleAttempt
			}
			if e.NodeID != "" && e.NodeID != a.NodeID || x.RunID == "" || x.SessionID == "" || x.WorkDir == "" {
				return PlanProjection{}, errors.New("attempt.bound provenance is incomplete")
			}
			a.RunID, a.SessionID, a.WorkDir, a.ChildPlanID = x.RunID, x.SessionID, x.WorkDir, x.ChildPlanID
			p.Attempts[x.AttemptID] = a
		case "attempt.finished":
			var x struct {
				AttemptID                string           `json:"attempt_id"`
				Event                    string           `json:"event"`
				Result                   StructuredResult `json:"result"`
				Failure                  *FailureReason   `json:"failure"`
				TransitionIdempotencyKey *string          `json:"transition_idempotency_key"`
				TransitionAt             time.Time        `json:"transition_at"`
			}
			if err := json.Unmarshal(e.Payload, &x); err != nil {
				return PlanProjection{}, err
			}
			transitionKey := e.IdempotencyKey
			if x.TransitionIdempotencyKey != nil {
				transitionKey = *x.TransitionIdempotencyKey
			}
			if x.TransitionAt.IsZero() {
				x.TransitionAt = e.CreatedAt
			}
			if _, err := p.FinishAttempt(plan, x.AttemptID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: e.FencingToken, Event: x.Event, Result: x.Result, Failure: x.Failure, IdempotencyKey: transitionKey, Now: x.TransitionAt}); err != nil {
				return PlanProjection{}, err
			}
		case "node.retry_requested":
			var x struct {
				NodeID string `json:"node_id"`
			}
			if err := json.Unmarshal(e.Payload, &x); err != nil {
				return PlanProjection{}, err
			}
			if err := p.Retry(plan, x.NodeID); err != nil {
				return PlanProjection{}, err
			}
		default:
			return PlanProjection{}, fmt.Errorf("unsupported replay event %q", e.Type)
		}
	}
	return p, nil
}
