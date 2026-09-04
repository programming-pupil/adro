package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	contextcontract "github.com/adro-project/adro/internal/context"
	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/harness"
	"github.com/adro-project/adro/internal/provider"
	"github.com/adro-project/adro/internal/telemetry"
)

// Executor is the small single-node runtime adapter.  It keeps the reducer as
// the only place that mutates graph state, while the provider receives the
// complete typed scope and context envelope.  Production workers can replace
// this adapter with a queue consumer without changing the contracts.
type Executor struct {
	Provider         provider.ExecutionProvider
	Repository       Repository
	Events           interface{ AppendEvent(Event) error }
	GateEvaluator    GateEvaluator
	MergeReducer     MergeReducer
	RepairController RepairController
	Tracer           telemetry.Tracer
	Owner            string
	Now              func() time.Time
	LeaseTTL         time.Duration
}

func (e Executor) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

func (e Executor) leaseTTL(node WorkflowNode) time.Duration {
	if node.Timeout > 0 {
		return node.Timeout
	}
	if e.LeaseTTL > 0 {
		return e.LeaseTTL
	}
	return 15 * time.Minute
}

func (e Executor) tracer() telemetry.Tracer {
	if e.Tracer.Exporter != nil {
		return e.Tracer
	}
	return telemetry.Tracer{Exporter: telemetry.ExporterFromEnvironment()}
}

type eventAppender interface {
	AppendEvent(Event) error
	ListEvents(planID string, after int64) []Event
}

type projectionSaver interface{ SaveProjection(PlanProjection) error }
type projectionEventCommitter interface {
	CommitEventProjection(Event, PlanProjection) error
}

type outboxStore interface {
	EnqueueOutbox(OutboxRecord) (OutboxRecord, bool, error)
	ClaimOutboxByID(id, owner string, ttl time.Duration, now time.Time) (OutboxRecord, error)
	AckOutbox(id, owner string, now time.Time, deliveryErr error) error
}

// AdvanceStructural executes graph-owned nodes that do not require an external
// provider. Merge/gate nodes complete with explicit evidence; human nodes enter
// waiting and can only leave through an approval transition. Keeping this in
// the executor preserves the same attempt/event transaction as agent nodes.
func (e Executor) AdvanceStructural(ctx context.Context, plan RequirementExecutionPlan, projection *PlanProjection, envelope harness.ContextEnvelope, limit int) ([]NodeAttempt, error) {
	if projection == nil {
		return nil, fmt.Errorf("projection is required")
	}
	if err := envelope.Validate(); err != nil {
		return nil, fmt.Errorf("context envelope: %w", err)
	}
	started := make([]NodeAttempt, 0)
	for _, node := range ReadyNodesAt(plan, *projection, e.now()) {
		if node.Kind == NodeAgent || node.Kind == NodeSquad || (limit > 0 && len(started) >= limit) {
			continue
		}
		before := cloneProjection(*projection)
		attemptNo := projection.Nodes[node.ID].AttemptNo + 1
		attemptID := domain.NewID()
		now := e.now()
		lease := Lease{Key: plan.ID + ":" + node.ID, Owner: e.Owner, FencingToken: now.UnixNano(), ExpiresAt: now.Add(e.leaseTTL(node))}
		key := plan.ID + ":" + node.ID + ":" + fmt.Sprint(attemptNo)
		h := sha256.Sum256([]byte(key + envelope.ReplayKey))
		a, err := projection.StartAttempt(plan, node.ID, attemptID, attemptNo, lease, envelope, TransitionInput{PlanRevision: plan.Revision, LeaseToken: lease.FencingToken, IdempotencyKey: key, PayloadHash: hex.EncodeToString(h[:]), Now: now})
		if err != nil {
			return started, err
		}
		if err := e.commitAttemptEvent(ctx, plan, projection, a, "attempt.started", key, map[string]any{"node_id": node.ID, "attempt_id": a.ID, "attempt_no": a.AttemptNo, "lease": lease, "context": envelope, "dispatch_payload_hash": hex.EncodeToString(h[:]), "started_at": now}); err != nil {
			*projection = before
			return started, err
		}
		if node.Kind == NodeHuman {
			waiting, err := projection.FinishAttempt(plan, a.ID, TransitionInput{PlanRevision: plan.Revision, AttemptID: a.ID, LeaseToken: lease.FencingToken, Event: "waiting", Result: StructuredResult{Outcome: "approval_pending", Summary: "human approval required"}, IdempotencyKey: key + ":waiting", Now: now})
			if err != nil {
				*projection = before
				return started, err
			}
			if err := e.commitAttemptEvent(ctx, plan, projection, waiting, "attempt.finished", key+":finished", map[string]any{"attempt_id": waiting.ID, "event": "waiting", "result": waiting.Result, "transition_idempotency_key": key + ":waiting", "transition_at": now}, lease.FencingToken); err != nil {
				*projection = before
				return started, err
			}
			started = append(started, waiting)
			continue
		}
		input := StructuralInput{Plan: plan, Projection: cloneProjection(*projection), Node: node, Attempt: a, Envelope: envelope, Incoming: incomingStructuralSources(plan, *projection, node.ID)}
		decision, err := e.evaluateStructural(ctx, input)
		if err != nil {
			*projection = before
			return started, err
		}
		if decision.Event == "" || decision.Result.ReasonCode == "" || len(decision.Result.EvidenceIDs) == 0 {
			*projection = before
			return started, fmt.Errorf("%s node %s returned an incomplete structural decision", node.Kind, node.ID)
		}
		finishKey := key + ":" + decision.Result.ReasonCode
		finished, err := projection.FinishAttempt(plan, a.ID, TransitionInput{PlanRevision: plan.Revision, AttemptID: a.ID, LeaseToken: lease.FencingToken, Event: decision.Event, Result: decision.Result, Failure: decision.Failure, OutputArtifacts: decision.ArtifactIDs, IdempotencyKey: finishKey, Now: now})
		if err != nil {
			*projection = before
			return started, err
		}
		if err := e.commitAttemptEvent(ctx, plan, projection, finished, "attempt.finished", key+":finished", map[string]any{"attempt_id": finished.ID, "event": decision.Event, "result": finished.Result, "failure": decision.Failure, "artifact_ids": finished.OutputArtifacts, "transition_idempotency_key": finishKey, "transition_at": now}, lease.FencingToken); err != nil {
			*projection = before
			return started, err
		}
		started = append(started, finished)
	}
	return started, nil
}

func (e Executor) evaluateStructural(ctx context.Context, input StructuralInput) (StructuralDecision, error) {
	switch input.Node.Kind {
	case NodeGate:
		evaluator := e.GateEvaluator
		if evaluator == nil {
			evaluator = DefaultGateEvaluator{}
		}
		return evaluator.EvaluateGate(ctx, input)
	case NodeMerge:
		reducer := e.MergeReducer
		if reducer == nil {
			reducer = DefaultMergeReducer{}
		}
		return reducer.ReduceMerge(ctx, input)
	case NodeRepair:
		controller := e.RepairController
		if controller == nil {
			controller = DefaultRepairController{}
		}
		return controller.PlanRepair(ctx, input)
	default:
		return StructuralDecision{}, fmt.Errorf("unsupported structural node kind %q", input.Node.Kind)
	}
}

func (e Executor) commitAttemptEvent(ctx context.Context, plan RequirementExecutionPlan, projection *PlanProjection, attempt NodeAttempt, typ, key string, payload map[string]any, fencing ...int64) error {
	if e.Events == nil {
		if saver, ok := e.Repository.(projectionSaver); ok && e.Repository != nil {
			if _, err := e.Repository.GetPlan(plan.WorkspaceID, plan.ID); err != nil {
				// Callers that only use a repository for roster lookup may run
				// the pure executor with an unpersisted plan. There is no durable
				// projection to save in that compatibility mode.
				if errors.Is(err, ErrNotFound) {
					return nil
				}
				return err
			}
			return saver.SaveProjection(*projection)
		}
		return nil
	}
	store, ok := e.Events.(eventAppender)
	if !ok {
		return fmt.Errorf("event store must support append and tail reads")
	}
	tail := store.ListEvents(plan.ID, 0)
	var previous *Event
	if len(tail) > 0 {
		p := tail[len(tail)-1]
		previous = &p
	}
	ev, err := NewEventWithContext(ctx, previous, plan.ID, plan.WorkspaceID, typ, key, payload)
	if err != nil {
		return err
	}
	ev.AttemptID = attempt.ID
	ev.NodeID = attempt.NodeID
	if len(fencing) > 0 {
		ev.FencingToken = fencing[0]
	} else {
		ev.FencingToken = attempt.Lease.FencingToken
	}
	ev.EnvelopeHash = eventDigest(ev)
	if committer, ok := e.Events.(projectionEventCommitter); ok {
		return committer.CommitEventProjection(ev, *projection)
	}
	if err := appendWithTail(store, ev); err != nil {
		return err
	}
	if saver, ok := e.Events.(projectionSaver); ok {
		return saver.SaveProjection(*projection)
	}
	return nil
}

func (e Executor) DispatchReady(ctx context.Context, plan RequirementExecutionPlan, projection *PlanProjection, envelope harness.ContextEnvelope, workItemID, agentBindingID string) ([]NodeAttempt, error) {
	return e.dispatchReady(ctx, plan, projection, envelope, workItemID, agentBindingID, 0)
}

// DispatchReadyLimited is used by bounded workers to preserve the projection
// and event ordering while enforcing a per-tick concurrency ceiling.
func (e Executor) DispatchReadyLimited(ctx context.Context, plan RequirementExecutionPlan, projection *PlanProjection, envelope harness.ContextEnvelope, workItemID, agentBindingID string, limit int) ([]NodeAttempt, error) {
	return e.dispatchReady(ctx, plan, projection, envelope, workItemID, agentBindingID, limit)
}

func (e Executor) dispatchReady(ctx context.Context, plan RequirementExecutionPlan, projection *PlanProjection, envelope harness.ContextEnvelope, workItemID, agentBindingID string, limit int) ([]NodeAttempt, error) {
	if projection == nil {
		return nil, fmt.Errorf("projection is required")
	}
	if err := envelope.Validate(); err != nil {
		return nil, fmt.Errorf("context envelope: %w", err)
	}
	// Use the executor's injected clock so retry backoff and deadline gates are
	// evaluated against the same instant as the scheduler.  Calling the wall
	// clock here would allow a retry to dispatch early during replay/tests and
	// could also race a plan deadline between the scheduler and provider call.
	ready := ReadyNodesAt(plan, *projection, e.now())
	hasProviderNode := false
	for _, node := range ready {
		if node.Kind == NodeAgent || node.Kind == NodeSquad {
			hasProviderNode = true
			break
		}
	}
	if hasProviderNode && e.Provider == nil {
		return nil, fmt.Errorf("provider is required for ready agent nodes")
	}
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
	if projectionStore == nil && e.Repository != nil {
		if _, err := e.Repository.GetPlan(plan.WorkspaceID, plan.ID); err == nil {
			projectionStore, _ = e.Repository.(projectionSaver)
		}
	}
	if projectionStore == nil {
		// No persisted plan means this is a pure in-memory execution. The
		// caller still receives the updated projection directly.
	}
	for _, node := range ready {
		if node.Kind != NodeAgent && node.Kind != NodeSquad {
			continue
		}
		if limit > 0 && len(started) >= limit {
			break
		}
		before := cloneProjection(*projection)
		agentInstructions := ""
		if node.Kind == NodeAgent && e.Repository != nil && node.AgentRef != nil {
			agent, lookupErr := e.Repository.GetAgent(plan.WorkspaceID, node.AgentRef.ID, node.AgentRef.Revision)
			if lookupErr != nil {
				return started, fmt.Errorf("resolve agent node %s: %w", node.ID, lookupErr)
			}
			if agent.Status != AgentActive {
				return started, fmt.Errorf("agent node %s references non-active agent", node.ID)
			}
			if capabilityErr := e.requireAgentCapabilities(ctx, agent); capabilityErr != nil {
				return started, fmt.Errorf("agent node %s: %w", node.ID, capabilityErr)
			}
			agentInstructions = agent.Instructions
		}
		var squadDefinition *SquadDefinition
		if node.Kind == NodeSquad {
			if e.Repository == nil || node.SquadRef == nil {
				return started, fmt.Errorf("squad node %s requires a repository and squad_ref", node.ID)
			}
			squad, squadErr := e.Repository.GetSquad(plan.WorkspaceID, node.SquadRef.ID, node.SquadRef.Revision)
			if squadErr != nil {
				return started, fmt.Errorf("resolve squad node %s: %w", node.ID, squadErr)
			}
			if squad.Status != SquadPublished {
				return started, fmt.Errorf("squad node %s references non-published squad", node.ID)
			}
			leaderCount := 0
			for _, member := range squad.Members {
				if member.Leader {
					leaderCount++
				}
			}
			if leaderCount != 1 {
				return started, fmt.Errorf("squad node %s squad must have exactly one leader", node.ID)
			}
			squadDefinition = &squad
		}
		nodeEnvelope := envelope
		if e.Repository != nil || node.Kind == NodeSquad {
			// Keep the frozen node contract inside the same immutable context
			// envelope as the user objective and tool transaction. The textual
			// prompt remains a compatibility projection, never the source of truth.
			contract, marshalErr := json.Marshal(map[string]any{
				"plan_id": plan.ID, "plan_revision": plan.Revision, "node": node,
				"binding": agentBindingID, "instructions": agentInstructions,
			})
			if marshalErr != nil {
				return started, fmt.Errorf("encode graph node contract: %w", marshalErr)
			}
			var enrichErr error
			nodeEnvelope, enrichErr = nodeEnvelope.WithRequiredBlock(contextcontract.Block{
				ID:   "graph-node-contract:" + plan.ID + ":" + node.ID,
				Kind: "plan_node_contract", Source: "graph:" + plan.ID + ":node:" + node.ID,
				Content: string(contract), Policy: "frozen_plan", Trust: "plan_snapshot",
				SelectionReason: "mandatory_graph_node_contract", TokenEstimate: contextcontract.EstimateTokens(string(contract)),
				Metadata: map[string]string{"prompt_kind": "plan_node_contract", "atomic": "true"},
			})
			if enrichErr != nil {
				return started, fmt.Errorf("compile graph node context: %w", enrichErr)
			}
		}
		attemptNo := projection.Nodes[node.ID].AttemptNo + 1
		attemptID := domain.NewID()
		now := e.now()
		lease := Lease{Key: plan.ID + ":" + node.ID, Owner: e.Owner, FencingToken: now.UnixNano(), ExpiresAt: now.Add(e.leaseTTL(node))}
		if !plan.Deadline.IsZero() && plan.Deadline.Before(lease.ExpiresAt) {
			lease.ExpiresAt = plan.Deadline
		}
		key := plan.ID + ":" + node.ID + ":" + fmt.Sprint(attemptNo)
		h := sha256.Sum256([]byte(key + nodeEnvelope.ReplayKey))
		payloadHash := hex.EncodeToString(h[:])
		a, err := projection.StartAttempt(plan, node.ID, attemptID, attemptNo, lease, nodeEnvelope, TransitionInput{PlanRevision: plan.Revision, LeaseToken: lease.FencingToken, IdempotencyKey: key, PayloadHash: payloadHash, Now: now})
		if err != nil {
			return started, err
		}
		// Materialize a nested Squad plan before publishing the parent attempt
		// event. The child identity is part of the immutable start fact, so replay
		// never has to infer lineage from a later provider binding event.
		if squadDefinition != nil {
			childID, childErr := e.ensureNestedSquadPlan(ctx, plan, node, a, *squadDefinition)
			if childErr != nil {
				*projection = before
				return started, childErr
			}
			a.ChildPlanID = childID
			projection.Attempts[a.ID] = a
		}
		if eventStore != nil {
			tail := eventStore.ListEvents(plan.ID, 0)
			var previous *Event
			if len(tail) > 0 {
				p := tail[len(tail)-1]
				previous = &p
			}
			ev, evErr := NewEventWithContext(ctx, previous, plan.ID, plan.WorkspaceID, "attempt.started", key, map[string]any{"node_id": node.ID, "attempt_id": a.ID, "attempt_no": a.AttemptNo, "lease": lease, "context": nodeEnvelope, "dispatch_payload_hash": payloadHash, "started_at": now, "child_plan_id": a.ChildPlanID})
			if evErr != nil {
				*projection = before
				return started, evErr
			}
			ev.FencingToken = lease.FencingToken
			ev.EnvelopeHash = eventDigest(ev)
			if committer, ok := e.Events.(projectionEventCommitter); ok {
				if commitErr := committer.CommitEventProjection(ev, *projection); commitErr != nil {
					*projection = before
					return started, fmt.Errorf("commit attempt.started projection: %w", commitErr)
				}
			} else if appendErr := appendWithTail(eventStore, ev); appendErr != nil {
				*projection = before
				return started, fmt.Errorf("append attempt.started: %w", appendErr)
			}
		} else if projectionStore != nil {
			if err := projectionStore.SaveProjection(*projection); err != nil {
				*projection = before
				return started, fmt.Errorf("persist attempt projection: %w", err)
			}
		}
		var outbox OutboxRecord
		var outboxClaimed bool
		if store, ok := e.Events.(outboxStore); ok {
			owner := e.Owner
			if owner == "" {
				owner = "executor"
			}
			var outboxErr error
			traceParent, traceState := telemetry.Carrier(ctx)
			outbox, _, outboxErr = store.EnqueueOutbox(OutboxRecord{PlanID: plan.ID, WorkspaceID: plan.WorkspaceID, Kind: "provider.start", IdempotencyKey: key, TraceParent: traceParent, TraceState: traceState, Payload: map[string]any{"node_id": node.ID, "attempt_id": a.ID, "work_item_id": workItemID, "agent_binding_id": agentBindingID}})
			if outboxErr != nil {
				*projection = before
				return started, fmt.Errorf("enqueue provider.start: %w", outboxErr)
			}
			if _, claimErr := store.ClaimOutboxByID(outbox.ID, owner, 15*time.Minute, time.Now().UTC()); claimErr == nil {
				outboxClaimed = true
			} else if claimErr != nil {
				*projection = before
				return started, fmt.Errorf("claim provider.start outbox: %w", claimErr)
			}
		}
		binding := agentBindingID
		if node.AgentRef != nil && binding == "" {
			binding = node.AgentRef.ID
		}
		if node.Kind == NodeSquad {
			// A Squad is a provider execution boundary. The leader owns routing
			// and aggregation; the nested graph remains pinned by the published
			// Squad revision. Never silently auto-complete this node.
			if squadDefinition == nil {
				return started, fmt.Errorf("squad node %s has no resolved squad", node.ID)
			}
			squad := *squadDefinition
			var leader *SquadMember
			for i := range squad.Members {
				if squad.Members[i].Leader {
					leader = &squad.Members[i]
					break
				}
			}
			if leader == nil || strings.TrimSpace(leader.AgentID) == "" {
				return started, fmt.Errorf("squad node %s squad has no leader agent", node.ID)
			}
			if binding == "" {
				binding = leader.AgentID
			}
			if leaderAgent, leaderErr := e.Repository.GetAgent(plan.WorkspaceID, leader.AgentID, 0); leaderErr != nil {
				return started, fmt.Errorf("resolve squad leader %s: %w", leader.AgentID, leaderErr)
			} else if capabilityErr := e.requireAgentCapabilities(ctx, leaderAgent); capabilityErr != nil {
				return started, fmt.Errorf("squad node %s: %w", node.ID, capabilityErr)
			} else {
				agentInstructions = leaderAgent.Instructions
			}
		}
		providerCtx, finishProviderSpan := e.tracer().Start(ctx, "provider.start", map[string]string{
			"component": "orchestration",
			"node_kind": string(node.Kind),
		})
		traceParent, traceState := telemetry.Carrier(providerCtx)
		input := envelopeInput(nodeEnvelope)
		if e.Repository != nil || node.Kind == NodeSquad {
			input = nodeInput(nodeEnvelope, node, a.AttemptNo, binding, agentInstructions)
		}
		bindingResult, runErr := e.Provider.StartRun(providerCtx, provider.StartRunCommand{PlanID: plan.ID, NodeID: node.ID, AttemptID: a.ID, WorkItemID: workItemID, AgentBindingID: binding, Input: input, SessionID: nodeEnvelope.Manifest.SessionID, ContextEnvelope: nodeEnvelope, IdempotencyKey: key, ExpectedRevision: plan.Revision, TraceParent: traceParent, TraceState: traceState})
		if runErr != nil {
			_ = finishProviderSpan("error", runErr.Error())
		} else {
			_ = finishProviderSpan("ok", "")
		}
		if runErr != nil {
			failure := TransitionInput{PlanRevision: plan.Revision, LeaseToken: lease.FencingToken, Event: "failure", Failure: &FailureReason{Code: string(provider.ErrorCodeOf(runErr)), Message: runErr.Error(), Retryable: true}, Result: StructuredResult{Outcome: "failure", EvidenceIDs: []string{"provider-dispatch:" + a.ID}}}
			if _, finishErr := e.FinishAttempt(ctx, plan, projection, a.ID, failure); finishErr != nil {
				return started, fmt.Errorf("provider failed (%v), finish failed: %w", runErr, finishErr)
			}
			if outboxClaimed {
				if store, ok := e.Events.(outboxStore); ok {
					owner := e.Owner
					if owner == "" {
						owner = "executor"
					}
					_ = store.AckOutbox(outbox.ID, owner, time.Now().UTC(), runErr)
				}
			}
			return started, runErr
		}
		// Persist provider provenance before acknowledging the dispatch. This is
		// a separate immutable event so replay can reconstruct the exact run,
		// session and workdir that belong to this attempt.
		bound := projection.Attempts[a.ID]
		bound.RunID, bound.SessionID, bound.WorkDir = bindingResult.ID, bindingResult.SessionID, bindingResult.WorkDir
		projection.Attempts[a.ID] = bound
		a = bound
		if eventStore != nil {
			tail := eventStore.ListEvents(plan.ID, 0)
			var previous *Event
			if len(tail) > 0 {
				p := tail[len(tail)-1]
				previous = &p
			}
			boundEvent, eventErr := NewEventWithContext(ctx, previous, plan.ID, plan.WorkspaceID, "attempt.bound", key+":bound", map[string]any{"attempt_id": a.ID, "run_id": a.RunID, "session_id": a.SessionID, "workdir": a.WorkDir, "child_plan_id": a.ChildPlanID})
			if eventErr != nil {
				return started, eventErr
			}
			boundEvent.AttemptID, boundEvent.NodeID, boundEvent.FencingToken = a.ID, a.NodeID, a.Lease.FencingToken
			boundEvent.EnvelopeHash = eventDigest(boundEvent)
			if committer, ok := e.Events.(projectionEventCommitter); ok {
				if commitErr := committer.CommitEventProjection(boundEvent, *projection); commitErr != nil {
					return started, fmt.Errorf("commit attempt.bound projection: %w", commitErr)
				}
			} else if appendErr := appendWithTail(eventStore, boundEvent); appendErr != nil {
				return started, fmt.Errorf("append attempt.bound: %w", appendErr)
			} else if projectionStore != nil {
				if saveErr := projectionStore.SaveProjection(*projection); saveErr != nil {
					return started, fmt.Errorf("persist bound attempt projection: %w", saveErr)
				}
			}
		} else if projectionStore != nil {
			if saveErr := projectionStore.SaveProjection(*projection); saveErr != nil {
				return started, fmt.Errorf("persist bound attempt projection: %w", saveErr)
			}
		}
		if outboxClaimed {
			if store, ok := e.Events.(outboxStore); ok {
				owner := e.Owner
				if owner == "" {
					owner = "executor"
				}
				if ackErr := store.AckOutbox(outbox.ID, owner, time.Now().UTC(), nil); ackErr != nil {
					return started, fmt.Errorf("ack provider.start outbox: %w", ackErr)
				}
			}
		}
		started = append(started, a)
	}
	return started, nil
}

func (e Executor) requireAgentCapabilities(ctx context.Context, agent AgentDefinition) error {
	if len(agent.ExecutorBinding.RequiredCaps) == 0 {
		return nil
	}
	if e.Provider == nil {
		return errors.New("capability_unavailable: provider is required")
	}
	caps, err := e.Provider.Capabilities(ctx)
	if err != nil {
		return fmt.Errorf("capability_unavailable: %w", err)
	}
	missing := make([]string, 0)
	for _, required := range agent.ExecutorBinding.RequiredCaps {
		if strings.TrimSpace(required) != "" && !caps.Supports(required) {
			missing = append(missing, required)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("capability_unavailable: missing %s", strings.Join(missing, ","))
	}
	return nil
}

// ensureNestedSquadPlan materializes the published Squad graph as an
// independent immutable child plan. The parent attempt keeps the child ID so
// recovery workers can resume the child after a process restart and replay can
// prove exactly which Squad revision ran.
func (e Executor) ensureNestedSquadPlan(ctx context.Context, parent RequirementExecutionPlan, node WorkflowNode, attempt NodeAttempt, squad SquadDefinition) (string, error) {
	if e.Repository == nil {
		return "", errors.New("repository is required for nested squad execution")
	}
	childID := parent.ID + "/squad/" + attempt.ID
	if existing, err := e.Repository.GetPlan(parent.WorkspaceID, childID); err == nil {
		if existing.ParentPlanID != parent.ID || existing.ParentAttemptID != attempt.ID {
			return "", fmt.Errorf("nested squad plan %s has mismatched parent lineage", childID)
		}
		return childID, nil
	}
	selected := VersionedRef{ID: squad.ID, Revision: squad.Revision, Version: squad.PublishedVersion}
	created := e.now()
	child := RequirementExecutionPlan{
		ID: childID, RequirementID: parent.RequirementID, WorkspaceID: parent.WorkspaceID,
		GraphSnapshot: squad.Graph, SelectedRef: selected,
		PolicySnapshot: PolicySnapshot{Digest: parent.PolicySnapshot.Digest, ToolPolicy: squad.Policy.ToolPolicy, Budget: squad.Policy.Budget, CapturedAt: created},
		ContextRoot:    parent.ContextRoot, Status: PlanDraft, CreatedAt: created,
		Deadline: parent.Deadline, ParentPlanID: parent.ID, ParentAttemptID: attempt.ID,
		IdempotencyKey: parent.ID + ":" + attempt.ID + ":nested",
	}
	frozen, err := child.Freeze()
	if err != nil {
		return "", fmt.Errorf("freeze nested squad plan %s: %w", node.ID, err)
	}
	event, err := NewEventWithContext(ctx, nil, frozen.ID, frozen.WorkspaceID, "plan.created", frozen.IdempotencyKey, frozen)
	if err != nil {
		return "", err
	}
	if control, ok := e.Repository.(interface {
		CreatePlanWithEvent(RequirementExecutionPlan, Event) error
	}); ok {
		if err := control.CreatePlanWithEvent(frozen, event); err != nil {
			if existing, getErr := e.Repository.GetPlan(parent.WorkspaceID, childID); getErr == nil && existing.ParentPlanID == parent.ID && existing.ParentAttemptID == attempt.ID {
				return childID, nil
			}
			return "", fmt.Errorf("persist nested squad plan: %w", err)
		}
	} else {
		if err := e.Repository.CreatePlan(frozen); err != nil {
			if existing, getErr := e.Repository.GetPlan(parent.WorkspaceID, childID); getErr == nil && existing.ParentPlanID == parent.ID && existing.ParentAttemptID == attempt.ID {
				return childID, nil
			}
			return "", fmt.Errorf("persist nested squad plan: %w", err)
		}
		childProjection, err := NewProjection(frozen)
		if err != nil {
			return "", err
		}
		if err := e.Repository.SaveProjection(childProjection); err != nil {
			return "", fmt.Errorf("persist nested squad projection: %w", err)
		}
		if e.Events != nil {
			if store, ok := e.Events.(eventAppender); ok {
				if appendErr := store.AppendEvent(event); appendErr != nil {
					return "", fmt.Errorf("append nested squad plan event: %w", appendErr)
				}
			}
		}
	}
	return childID, nil
}

// nodeInput is the deterministic provider prompt for graph-native runs. The
// envelope remains the authoritative structured contract; this rendered input
// is only the textual adapter payload consumed by legacy executables. Keeping
// the node contract in-band prevents a provider from confusing a feedback
// attempt with the original task when a plan has several active agents.
func nodeInput(envelope harness.ContextEnvelope, node WorkflowNode, attemptNo int, binding, instructions string) string {
	metadata := map[string]any{
		"node_id": node.ID, "node_kind": node.Kind, "attempt_no": attemptNo,
		"agent_binding_id": strings.TrimSpace(binding), "session_id": envelope.Manifest.SessionID,
		"context_replay_key": envelope.ReplayKey,
	}
	encoded, _ := json.Marshal(metadata)
	var builder strings.Builder
	builder.WriteString("ADRO_GRAPH_NODE_JSON=")
	builder.Write(encoded)
	builder.WriteString("\nYou are executing one immutable ADRO graph attempt. Follow the node contract and the durable context below.\n")
	if text := strings.TrimSpace(instructions); text != "" {
		builder.WriteString("Agent instructions:\n")
		builder.WriteString(text)
		builder.WriteString("\n")
	}
	builder.WriteString("Always finish with one plain-text line exactly in this form: ADRO_RESULT_JSON={\"outcome\":\"pass|failure|bug\",\"reason_code\":\"stable_reason\",\"summary\":\"what happened\",\"evidence_ids\":[\"evidence\"],\"fields\":{}}. Use failure for a failed check, bug for a defect found after a previously passing step, and pass only when the node work is complete.\n\n")
	if rendered, err := contextcontract.RenderPromptManifest(envelope.Manifest.PromptManifest); err == nil {
		builder.WriteString(rendered)
	} else {
		// Legacy callers may construct a placeholder envelope without a prompt
		// manifest. Preserve their context while making the failure observable
		// in-band; compiled production envelopes always take the authoritative
		// manifest path above.
		builder.WriteString("[ADRO_PROMPT_MANIFEST_INVALID]")
		for _, block := range envelope.Manifest.Blocks {
			if strings.TrimSpace(block.Content) == "" {
				continue
			}
			builder.WriteByte('\n')
			builder.WriteString(block.Content)
		}
	}
	return strings.TrimSpace(builder.String())
}

// envelopeInput is retained for compatibility with callers that only need the
// rendered context, while all graph dispatches use nodeInput above.
func envelopeInput(envelope harness.ContextEnvelope) string {
	// Even the in-memory executor path must consume the same provider-neutral
	// prompt contract as persisted graph dispatches. Raw block concatenation
	// loses layer order and lineage, allowing this compatibility path to drift
	// from the authoritative compiler.
	if rendered, err := envelope.Render(); err == nil {
		return rendered
	}
	// Some source-compatible unit callers construct a minimal envelope without
	// the newer PromptManifest fields. Production dispatch rejects such an
	// envelope in StartAttempt; retaining this narrow fallback keeps the helper
	// useful for those adapters without permitting an invalid envelope through
	// the real scheduler boundary.
	parts := make([]string, 0, len(envelope.Manifest.Blocks))
	for _, block := range envelope.Manifest.Blocks {
		if strings.TrimSpace(block.Content) != "" {
			parts = append(parts, block.Content)
		}
	}
	return strings.Join(parts, "\n")
}

// FinishAttempt applies a provider outcome and appends the matching immutable
// event. The projection is only retained when both reducer and event commit
// succeed; this is the local transaction boundary used by recovery workers.
func (e Executor) FinishAttempt(ctx context.Context, plan RequirementExecutionPlan, projection *PlanProjection, attemptID string, input TransitionInput) (NodeAttempt, error) {
	if projection == nil {
		return NodeAttempt{}, fmt.Errorf("projection is required")
	}
	if input.Now.IsZero() {
		input.Now = e.now()
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
		transitionKey := input.IdempotencyKey
		payload := map[string]any{"attempt_id": attemptID, "event": input.Event, "result": input.Result, "failure": input.Failure, "artifact_ids": a.OutputArtifacts, "transition_idempotency_key": transitionKey, "transition_at": input.Now}
		idempotency := input.IdempotencyKey
		if idempotency == "" {
			idempotency = a.IdempotencyKey
		}
		idempotency += ":finished"
		ev, evErr := NewEventWithContext(ctx, previous, plan.ID, plan.WorkspaceID, "attempt.finished", idempotency, payload)
		if evErr != nil {
			*projection = old
			return NodeAttempt{}, evErr
		}
		ev.FencingToken = input.LeaseToken
		ev.EnvelopeHash = eventDigest(ev)
		if committer, ok := e.Events.(projectionEventCommitter); ok {
			if commitErr := committer.CommitEventProjection(ev, *projection); commitErr != nil {
				*projection = old
				return NodeAttempt{}, commitErr
			}
		} else if appendErr := appendWithTail(store, ev); appendErr != nil {
			*projection = old
			return NodeAttempt{}, appendErr
		}
	} else {
		var saver projectionSaver
		if candidate, ok := e.Events.(projectionSaver); ok {
			saver = candidate
		} else if e.Repository != nil {
			if _, lookupErr := e.Repository.GetPlan(plan.WorkspaceID, plan.ID); lookupErr == nil {
				saver, _ = e.Repository.(projectionSaver)
			}
		}
		if saver != nil {
			if saveErr := saver.SaveProjection(*projection); saveErr != nil {
				*projection = old
				return NodeAttempt{}, saveErr
			}
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
	retry.TraceParent, retry.TraceState = ev.TraceParent, ev.TraceState
	retry.Seal()
	return store.AppendEvent(retry)
}

func cloneProjection(p PlanProjection) PlanProjection {
	cp := p
	cp.Nodes = map[string]NodeProjection{}
	for k, v := range p.Nodes {
		v.ReadyEdgeIDs = append([]string(nil), v.ReadyEdgeIDs...)
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
	cp.RepairPlans = map[string]RepairPlan{}
	for id, repair := range p.RepairPlans {
		repair.VerificationNodeIDs = append([]string(nil), repair.VerificationNodeIDs...)
		repair.StateHistory = append([]RepairLifecycle(nil), repair.StateHistory...)
		repair.RepairAttemptIDs = append([]string(nil), repair.RepairAttemptIDs...)
		repair.SourceAttemptIDs = append([]string(nil), repair.SourceAttemptIDs...)
		repair.Scope = append([]string(nil), repair.Scope...)
		repair.PatchArtifactIDs = append([]string(nil), repair.PatchArtifactIDs...)
		repair.VerificationArtifactIDs = append([]string(nil), repair.VerificationArtifactIDs...)
		verificationAttempts := repair.VerificationAttempts
		repair.VerificationAttempts = map[string]string{}
		for nodeID, attemptID := range verificationAttempts {
			repair.VerificationAttempts[nodeID] = attemptID
		}
		verifiedNodes := repair.VerifiedNodes
		repair.VerifiedNodes = map[string]bool{}
		for nodeID, verified := range verifiedNodes {
			repair.VerifiedNodes[nodeID] = verified
		}
		cp.RepairPlans[id] = repair
	}
	return cp
}
