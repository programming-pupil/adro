package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/harness"
	"github.com/adro-project/adro/internal/provider"
)

// Executor is the small single-node runtime adapter.  It keeps the reducer as
// the only place that mutates graph state, while the provider receives the
// complete typed scope and context envelope.  Production workers can replace
// this adapter with a queue consumer without changing the contracts.
type Executor struct {
	Provider provider.ExecutionProvider
	Events   interface{ AppendEvent(Event) error }
	Owner    string
}

type eventAppender interface {
	AppendEvent(Event) error
	ListEvents(planID string, after int64) []Event
}

type projectionSaver interface{ SaveProjection(PlanProjection) error }

func (e Executor) DispatchReady(ctx context.Context, plan RequirementExecutionPlan, projection *PlanProjection, envelope harness.ContextEnvelope, workItemID, agentBindingID string) ([]NodeAttempt, error) {
	if e.Provider == nil || projection == nil {
		return nil, fmt.Errorf("provider and projection are required")
	}
	verified, err := envelope.Manifest.Envelope()
	if err != nil {
		return nil, fmt.Errorf("context envelope: %w", err)
	}
	if verified.SelectionDigest != envelope.SelectionDigest || verified.ReplayKey != envelope.ReplayKey {
		return nil, fmt.Errorf("context envelope digest mismatch")
	}
	ready := ReadyNodes(plan, *projection)
	started := make([]NodeAttempt, 0, len(ready))
	var eventStore eventAppender
	if e.Events != nil {
		var ok bool
		eventStore, ok = e.Events.(eventAppender)
		if !ok {
			return nil, fmt.Errorf("event store must support append and tail reads")
		}
	}
	var projectionStore projectionSaver
	if e.Events != nil {
		projectionStore, _ = e.Events.(projectionSaver)
	}
	for _, node := range ready {
		if node.Kind != NodeAgent {
			continue
		}
		before := cloneProjection(*projection)
		attemptNo := projection.Nodes[node.ID].AttemptNo + 1
		attemptID := domain.NewID()
		lease := Lease{Key: plan.ID + ":" + node.ID, Owner: e.Owner, FencingToken: time.Now().UnixNano(), ExpiresAt: time.Now().UTC().Add(node.Timeout)}
		if lease.ExpiresAt.Equal(time.Time{}) || node.Timeout <= 0 {
			lease.ExpiresAt = time.Now().UTC().Add(15 * time.Minute)
		}
		key := plan.ID + ":" + node.ID + ":" + fmt.Sprint(attemptNo)
		h := sha256.Sum256([]byte(key + envelope.ReplayKey))
		payloadHash := hex.EncodeToString(h[:])
		a, err := projection.StartAttempt(plan, node.ID, attemptID, attemptNo, lease, envelope, TransitionInput{PlanRevision: plan.Revision, LeaseToken: lease.FencingToken, IdempotencyKey: key, PayloadHash: payloadHash})
		if err != nil {
			return started, err
		}
		if eventStore != nil {
			tail := eventStore.ListEvents(plan.ID, 0)
			var previous *Event
			if len(tail) > 0 {
				p := tail[len(tail)-1]
				previous = &p
			}
			ev, evErr := NewEvent(previous, plan.ID, plan.WorkspaceID, "attempt.started", key, map[string]any{"node_id": node.ID, "attempt_id": a.ID, "attempt_no": a.AttemptNo, "lease": lease, "context": envelope})
			if evErr != nil {
				*projection = before
				return started, evErr
			}
			ev.FencingToken = lease.FencingToken
			ev.EnvelopeHash = eventDigest(ev)
			if appendErr := appendWithTail(eventStore, ev); appendErr != nil {
				*projection = before
				return started, fmt.Errorf("append attempt.started: %w", appendErr)
			}
		}
		if projectionStore != nil {
			if err := projectionStore.SaveProjection(*projection); err != nil {
				*projection = before
				return started, fmt.Errorf("persist attempt projection: %w", err)
			}
		}
		binding := agentBindingID
		if node.AgentRef != nil && binding == "" {
			binding = node.AgentRef.ID
		}
		_, runErr := e.Provider.StartRun(ctx, provider.StartRunCommand{PlanID: plan.ID, NodeID: node.ID, AttemptID: a.ID, WorkItemID: workItemID, AgentBindingID: binding, SessionID: envelope.Manifest.SessionID, ContextEnvelope: envelope, IdempotencyKey: key, ExpectedRevision: plan.Revision})
		if runErr != nil {
			failure := TransitionInput{PlanRevision: plan.Revision, LeaseToken: lease.FencingToken, Event: "failure", Failure: &FailureReason{Code: string(provider.ErrorCodeOf(runErr)), Message: runErr.Error(), Retryable: true}, Result: StructuredResult{Outcome: "failure"}}
			if _, finishErr := e.FinishAttempt(ctx, plan, projection, a.ID, failure); finishErr != nil {
				return started, fmt.Errorf("provider failed (%v), finish failed: %w", runErr, finishErr)
			}
			return started, runErr
		}
		started = append(started, a)
	}
	return started, nil
}

// FinishAttempt applies a provider outcome and appends the matching immutable
// event. The projection is only retained when both reducer and event commit
// succeed; this is the local transaction boundary used by recovery workers.
func (e Executor) FinishAttempt(_ context.Context, plan RequirementExecutionPlan, projection *PlanProjection, attemptID string, input TransitionInput) (NodeAttempt, error) {
	if projection == nil {
		return NodeAttempt{}, fmt.Errorf("projection is required")
	}
	old := cloneProjection(*projection)
	a, err := projection.FinishAttempt(plan, attemptID, input)
	if err != nil {
		return NodeAttempt{}, err
	}
	if store, ok := e.Events.(eventAppender); ok {
		tail := store.ListEvents(plan.ID, 0)
		var previous *Event
		if len(tail) > 0 {
			p := tail[len(tail)-1]
			previous = &p
		}
		payload := map[string]any{"attempt_id": attemptID, "event": input.Event, "result": input.Result, "failure": input.Failure}
		idempotency := input.IdempotencyKey
		if idempotency == "" {
			idempotency = a.IdempotencyKey
		}
		idempotency += ":finished"
		ev, evErr := NewEvent(previous, plan.ID, plan.WorkspaceID, "attempt.finished", idempotency, payload)
		if evErr != nil {
			*projection = old
			return NodeAttempt{}, evErr
		}
		ev.FencingToken = input.LeaseToken
		ev.EnvelopeHash = eventDigest(ev)
		if appendErr := appendWithTail(store, ev); appendErr != nil {
			*projection = old
			return NodeAttempt{}, appendErr
		}
	}
	if saver, ok := e.Events.(projectionSaver); ok {
		if saveErr := saver.SaveProjection(*projection); saveErr != nil {
			*projection = old
			return NodeAttempt{}, saveErr
		}
	}
	return a, nil
}

func appendWithTail(store eventAppender, ev Event) error {
	if err := store.AppendEvent(ev); err == nil {
		return nil
	}
	// A peer may have appended between the tail read and our append. Rebuild
	// predecessor/sequence from the new tail once, retaining the same event ID
	// and payload/idempotency key.
	tail := store.ListEvents(ev.PlanID, 0)
	var previous *Event
	if len(tail) > 0 {
		p := tail[len(tail)-1]
		previous = &p
	}
	retry, err := NewEvent(previous, ev.PlanID, ev.WorkspaceID, ev.Type, ev.IdempotencyKey, json.RawMessage(ev.Payload))
	if err != nil {
		return err
	}
	retry.ID, retry.RunID, retry.NodeID, retry.AttemptID, retry.FencingToken = ev.ID, ev.RunID, ev.NodeID, ev.AttemptID, ev.FencingToken
	return store.AppendEvent(retry)
}

func cloneProjection(p PlanProjection) PlanProjection {
	cp := p
	cp.Nodes = map[string]NodeProjection{}
	for k, v := range p.Nodes {
		cp.Nodes[k] = v
	}
	cp.Attempts = map[string]NodeAttempt{}
	for k, v := range p.Attempts {
		cp.Attempts[k] = v
	}
	cp.Traversals = map[string]int{}
	for k, v := range p.Traversals {
		cp.Traversals[k] = v
	}
	cp.Idempotency = map[string]string{}
	for k, v := range p.Idempotency {
		cp.Idempotency[k] = v
	}
	cp.Decisions = append([]FeedbackDecision(nil), p.Decisions...)
	return cp
}
