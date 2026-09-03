package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/harness"
	"github.com/adro-project/adro/internal/provider"
	"github.com/adro-project/adro/internal/telemetry"
)

// Worker is the recoverable polling loop for the local profile. A scheduler
// tick only reserves and starts attempts; Worker.Reconcile observes provider
// snapshots and commits terminal results through the same reducer/event path.
// Queue-backed deployments can use the same methods from a durable consumer.
type Worker struct {
	Scheduler    Scheduler
	PollInterval time.Duration
	MaxTicks     int
}

type WorkerReport struct {
	Ticks      int            `json:"ticks"`
	Started    []NodeAttempt  `json:"started,omitempty"`
	Finished   []NodeAttempt  `json:"finished,omitempty"`
	LastStatus ScheduleReport `json:"last_status"`
}

type outboxRecoveryStore interface {
	ListOutbox(planID, status string) []OutboxRecord
	ClaimOutboxByID(id, owner string, ttl time.Duration, now time.Time) (OutboxRecord, error)
	AckOutbox(id, owner string, now time.Time, deliveryErr error) error
}

// Reconcile converts provider terminal snapshots into typed graph outcomes.
// Unknown provider states are left running; an expired lease is timed out
// locally so a takeover can fence the old worker before retrying.
func (w Worker) Reconcile(ctx context.Context, plan RequirementExecutionPlan, projection *PlanProjection) ([]NodeAttempt, error) {
	if projection == nil {
		return nil, errors.New("projection is required")
	}
	if w.Scheduler.Executor.Provider == nil {
		return nil, errors.New("provider is required")
	}
	now := w.Scheduler.now()
	finished := make([]NodeAttempt, 0)
	for _, attempt := range cloneProjection(*projection).Attempts {
		if attempt.Status != AttemptRunning {
			continue
		}
		// A Squad attempt owns a durable child plan. Advance that child before
		// consulting the leader provider; this makes nested execution recoverable
		// and lets the parent aggregate a terminal child result deterministically.
		if attempt.ChildPlanID != "" && w.Scheduler.Repository != nil {
			childPlan, planErr := w.Scheduler.Repository.GetPlan(plan.WorkspaceID, attempt.ChildPlanID)
			if planErr != nil {
				return finished, fmt.Errorf("load nested squad plan %s: %w", attempt.ChildPlanID, planErr)
			}
			childProjection, projectionErr := w.Scheduler.Repository.GetProjection(childPlan.ID)
			if projectionErr != nil {
				return finished, fmt.Errorf("load nested squad projection %s: %w", childPlan.ID, projectionErr)
			}
			childScheduler := w.Scheduler
			childScheduler.Executor.Repository = w.Scheduler.Repository
			childWorker := Worker{Scheduler: childScheduler, MaxTicks: 1}
			if _, childErr := childWorker.Run(ctx, childPlan, &childProjection, attempt.InputManifest, attempt.RunID, ""); childErr != nil && ctx.Err() != nil {
				return finished, childErr
			}
			if childProjection.Status == PlanTerminal {
				event := "success"
				result := StructuredResult{Outcome: "pass", Summary: "nested squad plan completed", EvidenceIDs: []string{"child-plan:" + childPlan.ID}}
				var failure *FailureReason
				if childProjection.TerminalOutcome != "succeeded" {
					event = "failure"
					result.Outcome = "failure"
					failure = &FailureReason{Code: "nested_squad_failed", Message: childProjection.TerminalOutcome, Retryable: true}
				}
				item, finishErr := w.Scheduler.Executor.FinishAttempt(ctx, plan, projection, attempt.ID, TransitionInput{PlanRevision: plan.Revision, AttemptID: attempt.ID, LeaseToken: attempt.Lease.FencingToken, Event: event, Result: result, Failure: failure, Now: now})
				if finishErr != nil {
					return finished, finishErr
				}
				finished = append(finished, item)
				continue
			}
			// The nested plan is the Squad attempt's source of truth. A leader
			// provider snapshot is only an implementation detail and must not be
			// allowed to complete the parent while the child graph is still active.
			continue
		}
		if !attempt.Lease.ExpiresAt.IsZero() && !now.Before(attempt.Lease.ExpiresAt) {
			item, err := w.Scheduler.Executor.FinishAttempt(ctx, plan, projection, attempt.ID, TransitionInput{PlanRevision: plan.Revision, AttemptID: attempt.ID, LeaseToken: attempt.Lease.FencingToken, Event: "timeout", Result: StructuredResult{Outcome: "timeout", Summary: "provider lease expired", EvidenceIDs: []string{"lease-expired:" + attempt.ID}}, Failure: &FailureReason{Code: "lease_expired", Message: "provider lease expired", Retryable: true}, Now: now})
			if err != nil {
				return finished, err
			}
			finished = append(finished, item)
			continue
		}
		runID := strings.TrimSpace(attempt.RunID)
		if runID == "" {
			// A crash can leave the durable attempt and provider.start outbox
			// committed before the provider returns its binding. Reclaim that
			// intent with the original idempotency key instead of stranding the
			// graph in running forever.
			bound, recovered, recoverErr := w.recoverUnboundAttempt(ctx, plan, projection, attempt)
			if recoverErr != nil {
				return finished, recoverErr
			}
			if recovered {
				attempt = bound
				runID = strings.TrimSpace(attempt.RunID)
			} else {
				continue
			}
		}
		snapshot, err := w.Scheduler.Executor.Provider.GetRun(ctx, runID)
		if err != nil {
			return finished, err
		}
		status := strings.ToLower(strings.TrimSpace(snapshot.Status))
		usage := map[string]any{
			"tokens":     snapshot.Usage.InputTokens + snapshot.Usage.OutputTokens,
			"tool_calls": len(snapshot.ToolEvents),
		}
		if snapshot.Usage.EstimatedCost > 0 {
			usage["cost_cents"] = int64(snapshot.Usage.EstimatedCost * 100)
		}
		var event string
		var result StructuredResult
		var failure *FailureReason
		switch status {
		case "completed", "passed", "success", "succeeded":
			event, result = "success", StructuredResult{Outcome: "pass", Summary: snapshot.Output, Fields: usage}
			if snapshot.LastEventID != "" {
				result.EvidenceIDs = []string{snapshot.LastEventID}
			} else {
				result.EvidenceIDs = []string{"provider-run:" + runID + ":completed"}
			}
		case "failed", "error", "failure":
			event, result = "failure", StructuredResult{Outcome: "failure", Summary: snapshot.Error, Fields: usage, EvidenceIDs: []string{"provider-run:" + runID + ":failed"}}
			failure = &FailureReason{Code: "provider_failed", Message: snapshot.Error, Retryable: true}
		case "cancelled", "canceled":
			event, result = "cancel", StructuredResult{Outcome: "cancelled", Summary: snapshot.Error, Fields: usage, EvidenceIDs: []string{"provider-run:" + runID + ":cancelled"}}
			failure = &FailureReason{Code: "cancelled", Message: snapshot.Error}
		default:
			continue
		}
		item, err := w.Scheduler.Executor.FinishAttempt(ctx, plan, projection, attempt.ID, TransitionInput{PlanRevision: plan.Revision, AttemptID: attempt.ID, LeaseToken: attempt.Lease.FencingToken, Event: event, Result: result, Failure: failure, Now: now})
		if err != nil {
			return finished, err
		}
		finished = append(finished, item)
	}
	return finished, nil
}

func (w Worker) recoverUnboundAttempt(ctx context.Context, plan RequirementExecutionPlan, projection *PlanProjection, attempt NodeAttempt) (NodeAttempt, bool, error) {
	if w.Scheduler.Executor.Provider == nil || attempt.ID == "" || attempt.IdempotencyKey == "" {
		return attempt, false, nil
	}
	store, ok := w.Scheduler.Executor.Events.(outboxRecoveryStore)
	if !ok {
		if candidate, candidateOK := w.Scheduler.Repository.(outboxRecoveryStore); candidateOK {
			store, ok = candidate, true
		}
	}
	if !ok {
		return attempt, false, nil
	}
	var intent *OutboxRecord
	for _, candidate := range store.ListOutbox(plan.ID, "") {
		if candidate.Status == "acked" || candidate.Status == "failed" {
			continue
		}
		if value, exists := candidate.Payload["attempt_id"].(string); exists && value == attempt.ID {
			copy := candidate
			intent = &copy
			break
		}
	}
	if intent == nil {
		return attempt, false, nil
	}
	owner := strings.TrimSpace(w.Scheduler.Executor.Owner)
	if owner == "" {
		owner = "worker-recovery"
	}
	claimed, err := store.ClaimOutboxByID(intent.ID, owner, w.Scheduler.Executor.leaseTTL(workflowNodeFor(plan, attempt.NodeID)), w.Scheduler.now())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return attempt, false, nil
		}
		return attempt, false, fmt.Errorf("claim unbound provider outbox: %w", err)
	}
	deliveryCtx, _, _ := telemetry.StartRemoteSpan(ctx, claimed.TraceParent, claimed.TraceState)
	traceParent, traceState := telemetry.Carrier(deliveryCtx)
	binding, startErr := w.Scheduler.Executor.Provider.StartRun(deliveryCtx, provider.StartRunCommand{PlanID: plan.ID, NodeID: attempt.NodeID, AttemptID: attempt.ID, WorkItemID: claimed.PayloadString("work_item_id"), AgentBindingID: claimed.PayloadString("agent_binding_id"), Input: envelopeInput(attempt.InputManifest), SessionID: attempt.InputManifest.Manifest.SessionID, ContextEnvelope: attempt.InputManifest, IdempotencyKey: attempt.IdempotencyKey, ExpectedRevision: plan.Revision, TraceParent: traceParent, TraceState: traceState})
	if startErr != nil {
		_ = store.AckOutbox(claimed.ID, owner, w.Scheduler.now(), startErr)
		return attempt, false, nil
	}
	if strings.TrimSpace(binding.ID) == "" || strings.TrimSpace(binding.SessionID) == "" || strings.TrimSpace(binding.WorkDir) == "" {
		_ = store.AckOutbox(claimed.ID, owner, w.Scheduler.now(), errors.New("provider returned incomplete binding"))
		return attempt, false, errors.New("provider returned incomplete binding during recovery")
	}
	attempt.RunID, attempt.SessionID, attempt.WorkDir = binding.ID, binding.SessionID, binding.WorkDir
	projection.Attempts[attempt.ID] = attempt
	if err := w.Scheduler.Executor.commitAttemptEvent(deliveryCtx, plan, projection, attempt, "attempt.bound", attempt.IdempotencyKey+":bound", map[string]any{"attempt_id": attempt.ID, "run_id": attempt.RunID, "session_id": attempt.SessionID, "workdir": attempt.WorkDir}, attempt.Lease.FencingToken); err != nil {
		return attempt, false, fmt.Errorf("persist recovered provider binding: %w", err)
	}
	if err := store.AckOutbox(claimed.ID, owner, w.Scheduler.now(), nil); err != nil {
		return attempt, false, fmt.Errorf("ack recovered provider outbox: %w", err)
	}
	return attempt, true, nil
}

func workflowNodeFor(plan RequirementExecutionPlan, nodeID string) WorkflowNode {
	for _, node := range plan.GraphSnapshot.Nodes {
		if node.ID == nodeID {
			return node
		}
	}
	return WorkflowNode{ID: nodeID}
}

func (o OutboxRecord) PayloadString(key string) string {
	if o.Payload == nil {
		return ""
	}
	value, _ := o.Payload[key].(string)
	return strings.TrimSpace(value)
}

// Run performs bounded foreground ticks until the graph is terminal, no work
// remains, the context is cancelled, or MaxTicks is reached. It never starts
// an unowned background goroutine, which keeps crash/retry behavior observable
// to callers and test harnesses.
func (w Worker) Run(ctx context.Context, plan RequirementExecutionPlan, projection *PlanProjection, envelope harness.ContextEnvelope, workItemID, agentBindingID string) (WorkerReport, error) {
	if projection == nil {
		return WorkerReport{}, errors.New("projection is required")
	}
	interval := w.PollInterval
	if interval <= 0 {
		interval = 25 * time.Millisecond
	}
	report := WorkerReport{}
	for {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.Ticks++
		status, err := w.Scheduler.Tick(ctx, plan, projection, envelope, workItemID, agentBindingID)
		// A provider failure is already committed through FinishAttempt. If the
		// reducer scheduled an automatic retry or routed a feedback edge, keep
		// this foreground worker alive until that work is reconciled. Returning
		// the dispatch error here would strand a perfectly recoverable graph in
		// `ready` with a future RetryAt. Non-retryable/structural errors still
		// surface to the caller below.
		tickErr := err
		if err != nil && !errors.Is(err, ErrDeadlineExceeded) {
			_, retryScheduled := nextRetryAt(*projection, w.Scheduler.now())
			readyAfterFailure := len(ReadyNodesAt(plan, *projection, w.Scheduler.now())) > 0
			if !retryScheduled && !readyAfterFailure && projection.Status != PlanTerminal {
				return report, err
			}
		}
		report.LastStatus = status
		report.Started = append(report.Started, status.Started...)
		finished, reconcileErr := w.Reconcile(ctx, plan, projection)
		if reconcileErr != nil {
			return report, reconcileErr
		}
		report.Finished = append(report.Finished, finished...)
		if projection.Status == PlanTerminal {
			return report, nil
		}
		if errors.Is(tickErr, ErrDeadlineExceeded) {
			return report, nil
		}
		if w.MaxTicks > 0 && report.Ticks >= w.MaxTicks {
			return report, nil
		}
		// A provider attempt can remain running while no node is ready. Do not
		// treat that state as quiescence or the worker would strand the run.
		// The same applies to an automatic retry: the reducer leaves the node
		// ready with RetryAt in the future, so the worker must sleep until that
		// boundary instead of returning and silently abandoning the plan.
		readyNow := len(ReadyNodesAt(plan, *projection, w.Scheduler.now())) > 0
		if len(status.Started) == 0 && len(finished) == 0 && !readyNow && !hasRunningAttempts(*projection) {
			if retryAt, ok := nextRetryAt(*projection, w.Scheduler.now()); ok {
				wait := retryAt.Sub(w.Scheduler.now())
				if wait > interval {
					wait = interval
				}
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return report, ctx.Err()
				case <-timer.C:
				}
				continue
			}
			return report, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return report, ctx.Err()
		case <-timer.C:
		}
	}
}

func nextRetryAt(projection PlanProjection, now time.Time) (time.Time, bool) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var next time.Time
	for _, node := range projection.Nodes {
		if node.Status != AttemptReady || node.RetryAt == nil || !node.RetryAt.After(now) {
			continue
		}
		if next.IsZero() || node.RetryAt.Before(next) {
			next = *node.RetryAt
		}
	}
	return next, !next.IsZero()
}

func hasRunningAttempts(projection PlanProjection) bool {
	for _, attempt := range projection.Attempts {
		if attempt.Status == AttemptRunning {
			return true
		}
	}
	return false
}
