package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	for _, node := range ready {
		if node.Kind != NodeAgent {
			continue
		}
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
		if e.Events != nil {
			if ev, evErr := NewEvent(nil, plan.ID, plan.WorkspaceID, "attempt.started", key, map[string]any{"node_id": node.ID, "attempt_id": a.ID, "attempt_no": a.AttemptNo, "lease": lease, "context": envelope}); evErr == nil {
				ev.FencingToken = lease.FencingToken
				_ = e.Events.AppendEvent(ev)
			}
		}
		binding := agentBindingID
		if node.AgentRef != nil && binding == "" {
			binding = node.AgentRef.ID
		}
		_, runErr := e.Provider.StartRun(ctx, provider.StartRunCommand{PlanID: plan.ID, NodeID: node.ID, AttemptID: a.ID, WorkItemID: workItemID, AgentBindingID: binding, SessionID: envelope.Manifest.SessionID, ContextEnvelope: envelope, IdempotencyKey: key, ExpectedRevision: plan.Revision})
		if runErr != nil {
			_, _ = projection.FinishAttempt(plan, a.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: lease.FencingToken, Event: "failure", Failure: &FailureReason{Code: string(provider.ErrorCodeOf(runErr)), Message: runErr.Error(), Retryable: true}, Result: StructuredResult{Outcome: "failure"}})
			return started, runErr
		}
		started = append(started, a)
	}
	return started, nil
}
