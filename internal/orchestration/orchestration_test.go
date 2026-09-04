package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/harness"
	"github.com/adro-project/adro/internal/provider"
	"github.com/adro-project/adro/internal/telemetry"
)

type testProvider struct {
	*provider.MockProvider
	lastBinding string
	lastInput   string
	lastTrace   string
}

type blockingProvider struct {
	*provider.MockProvider
}

type timedOutProvider struct {
	*testProvider
}

func (p *timedOutProvider) GetRun(_ context.Context, runID string) (provider.RunSnapshot, error) {
	now := time.Now().UTC()
	return provider.RunSnapshot{ID: runID, Status: "timed_out", Error: "executor deadline exceeded", FinishedAt: &now}, nil
}

type recoveryProvider struct {
	*testProvider
}

func (p *recoveryProvider) StartRun(ctx context.Context, command provider.StartRunCommand) (provider.RunBinding, error) {
	binding, err := p.testProvider.StartRun(ctx, command)
	if err == nil {
		binding.WorkDir = "/recovered/workdir"
	}
	return binding, err
}

func (p *blockingProvider) GetRun(_ context.Context, runID string) (provider.RunSnapshot, error) {
	return provider.RunSnapshot{ID: runID, Status: "running"}, nil
}

func newTestProvider() *testProvider {
	return &testProvider{MockProvider: provider.NewMockProvider(events.NewBus())}
}

func (p *testProvider) StartRun(ctx context.Context, command provider.StartRunCommand) (provider.RunBinding, error) {
	p.lastBinding = command.AgentBindingID
	p.lastInput = command.Input
	p.lastTrace = command.TraceParent
	return p.MockProvider.StartRun(ctx, command)
}

func TestExecutorPropagatesW3CTraceIntoEventOutboxAndProviderCommand(t *testing.T) {
	p := newTestProvider()
	repo := NewMemoryRepository()
	agent := AgentDefinition{ID: "agent", WorkspaceID: "w", Revision: 1, Name: "agent", Role: "developer", Status: AgentActive, ExecutorBinding: ExecutorBinding{ProviderID: "local"}, InputSchema: SchemaRef{ID: "in", Version: 1}, OutputSchema: SchemaRef{ID: "out", Version: 1}}
	if err := repo.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	graph := WorkflowGraph{ID: "g", Version: 1, EntryNodeIDs: []string{"n"}, ExitNodeIDs: []string{"n"}, Nodes: []WorkflowNode{{ID: "n", Kind: NodeAgent, AgentRef: &VersionedRef{ID: agent.ID, Revision: 1}}}}
	plan, err := (RequirementExecutionPlan{ID: "plan", RequirementID: "req", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft, IdempotencyKey: "plan"}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	created, _ := NewEvent(nil, plan.ID, plan.WorkspaceID, "plan.created", "plan", plan)
	if err := repo.CreatePlanWithEvent(plan, created); err != nil {
		t.Fatal(err)
	}
	projection, err := repo.GetProjection(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, _, err := telemetry.StartRemoteSpan(context.Background(), "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "vendor=value")
	if err != nil {
		t.Fatal(err)
	}
	executor := Executor{Provider: p, Repository: repo, Events: repo, Owner: "worker"}
	if _, err := executor.DispatchReady(ctx, plan, &projection, testEnvelope(), "work", agent.ID); err != nil {
		t.Fatal(err)
	}
	providerSpan, err := telemetry.ParseTraceParent(p.lastTrace, "vendor=value")
	if err != nil || providerSpan.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("provider trace=%q err=%v", p.lastTrace, err)
	}
	events := repo.ListEvents(plan.ID, 0)
	if len(events) < 2 || !strings.Contains(events[1].TraceParent, providerSpan.TraceID) {
		t.Fatalf("orchestration event trace missing: %+v", events)
	}
	outbox := repo.ListOutbox(plan.ID, "")
	if len(outbox) != 1 || !strings.Contains(outbox[0].TraceParent, providerSpan.TraceID) || outbox[0].TraceState != "vendor=value" {
		t.Fatalf("outbox trace missing: %+v", outbox)
	}
}

func testEnvelope() harness.ContextEnvelope {
	manifest := harness.ContextManifest{SessionID: "s", Version: 1, TokenBudget: 1, Digest: "d"}
	envelope, _ := manifest.Envelope()
	return envelope
}

func TestExecutorRendersEnvelopeBlocksForProviderInput(t *testing.T) {
	p := newTestProvider()
	manifest := harness.ContextManifest{SessionID: "s", Version: 1, TokenBudget: 20, TokenEstimate: 2, Digest: "d", Blocks: []harness.ContextBlock{{ID: "b", Source: "test", Content: "execute this", Hash: "", Policy: "required", Trust: "trusted", SelectionReason: "test", TokenEstimate: 2}}}
	// ContextBlock hashes are verified by the envelope constructor.
	h := sha256.Sum256([]byte("execute this"))
	manifest.Blocks[0].Hash = hex.EncodeToString(h[:])
	canonical := manifest
	canonical.Digest = ""
	canonical.CreatedAt = time.Time{}
	digestPayload, err := json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(digestPayload)
	manifest.Digest = hex.EncodeToString(digest[:])
	envelope, err := manifest.Envelope()
	if err != nil {
		t.Fatal(err)
	}
	graph := WorkflowGraph{ID: "input-graph", Version: 1, EntryNodeIDs: []string{"agent"}, ExitNodeIDs: []string{"agent"}, Nodes: []WorkflowNode{{ID: "agent", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "a", Revision: 1}}}}
	plan, err := (RequirementExecutionPlan{ID: "input-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	exec := Executor{Provider: p, Owner: "test"}
	if _, err := exec.DispatchReady(context.Background(), plan, &projection, envelope, "work", "binding"); err != nil {
		t.Fatal(err)
	}
	if p.lastInput != "execute this" {
		t.Fatalf("provider input=%q", p.lastInput)
	}
}

func TestExecutorPersistsProjectionWithoutEventStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orchestration.json")
	repo, err := NewPersistentRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	agent := AgentDefinition{ID: "durable-agent", WorkspaceID: "w", Revision: 1, Name: "durable", Status: AgentActive, ExecutorBinding: ExecutorBinding{ProviderID: "mock"}, InputSchema: SchemaRef{ID: "input", Version: 1}, OutputSchema: SchemaRef{ID: "output", Version: 1}}
	if err := repo.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	graph := WorkflowGraph{ID: "durable-graph", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []WorkflowNode{{ID: "node", Kind: NodeAgent, AgentRef: &VersionedRef{ID: agent.ID, Revision: agent.Revision}}}}
	plan, err := (RequirementExecutionPlan{ID: "durable-plan", RequirementID: "req", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreatePlan(plan); err != nil {
		t.Fatal(err)
	}
	projection, err := repo.GetProjection(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	provider := newTestProvider()
	// No event store is supplied. The executor must still persist the
	// projection through the repository so a restart can recover the lease.
	executor := Executor{Provider: provider, Repository: repo, Owner: "worker"}
	started, err := executor.DispatchReady(context.Background(), plan, &projection, testEnvelope(), "work", "")
	if err != nil || len(started) != 1 {
		t.Fatalf("started=%+v err=%v", started, err)
	}
	restarted, err := NewPersistentRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := restarted.GetProjection(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Attempts[started[0].ID].Status != AttemptRunning {
		t.Fatalf("projection was not durably saved: %+v", recovered.Attempts[started[0].ID])
	}
	if _, err := executor.FinishAttempt(context.Background(), plan, &recovered, started[0].ID, TransitionInput{PlanRevision: plan.Revision, AttemptID: started[0].ID, LeaseToken: started[0].Lease.FencingToken, Event: "success", Result: StructuredResult{Outcome: "pass", EvidenceIDs: []string{"evidence"}}}); err != nil {
		t.Fatal(err)
	}
	restarted, err = NewPersistentRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	finalProjection, err := restarted.GetProjection(plan.ID)
	if err != nil || finalProjection.Status != PlanTerminal || finalProjection.TerminalOutcome != "succeeded" {
		t.Fatalf("finish was not durably saved: projection=%+v err=%v", finalProjection, err)
	}
}

func TestWorkerRecoversUnboundAttemptFromOutbox(t *testing.T) {
	repo := NewMemoryRepository()
	agent := AgentDefinition{ID: "recover-agent", WorkspaceID: "w", Revision: 1, Name: "recover", Status: AgentActive, ExecutorBinding: ExecutorBinding{ProviderID: "mock"}, InputSchema: SchemaRef{ID: "input"}, OutputSchema: SchemaRef{ID: "output"}}
	if err := repo.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	graph := WorkflowGraph{ID: "recover-graph", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []WorkflowNode{{ID: "node", Kind: NodeAgent, AgentRef: &VersionedRef{ID: agent.ID, Revision: agent.Revision}}}}
	plan, err := (RequirementExecutionPlan{ID: "recover-plan", RequirementID: "req", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreatePlan(plan); err != nil {
		t.Fatal(err)
	}
	projection, err := repo.GetProjection(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease := Lease{Key: plan.ID + ":node", Owner: "old-worker", FencingToken: 7, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	attempt, err := projection.StartAttempt(plan, "node", "unbound-attempt", 1, lease, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: lease.FencingToken, IdempotencyKey: "recover-dispatch", PayloadHash: "payload"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveProjection(projection); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.EnqueueOutbox(OutboxRecord{ID: "recover-outbox", PlanID: plan.ID, WorkspaceID: plan.WorkspaceID, Kind: "provider.start", IdempotencyKey: attempt.IdempotencyKey, Payload: map[string]any{"attempt_id": attempt.ID, "work_item_id": "work-item"}}); err != nil {
		t.Fatal(err)
	}
	provider := &recoveryProvider{testProvider: newTestProvider()}
	worker := Worker{Scheduler: Scheduler{Repository: repo, Executor: Executor{Provider: provider, Repository: repo, Events: repo, Owner: "recovery-worker"}}}
	finished, err := worker.Reconcile(context.Background(), plan, &projection)
	if err != nil {
		t.Fatal(err)
	}
	if len(finished) != 0 || projection.Attempts[attempt.ID].RunID == "" {
		t.Fatalf("unbound attempt was not recovered: finished=%+v attempt=%+v", finished, projection.Attempts[attempt.ID])
	}
	outbox := repo.ListOutbox(plan.ID, "")
	if len(outbox) != 1 || outbox[0].Status != "acked" {
		t.Fatalf("recovery outbox status=%+v", outbox)
	}
}

func TestWorkerReconcilesProviderTimeoutImmediately(t *testing.T) {
	repo := NewMemoryRepository()
	agent := AgentDefinition{ID: "timeout-agent", WorkspaceID: "w", Revision: 1, Name: "timeout", Status: AgentActive, ExecutorBinding: ExecutorBinding{ProviderID: "mock"}, InputSchema: SchemaRef{ID: "input"}, OutputSchema: SchemaRef{ID: "output"}}
	if err := repo.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	graph := WorkflowGraph{ID: "timeout-graph", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []WorkflowNode{{ID: "node", Kind: NodeAgent, AgentRef: &VersionedRef{ID: agent.ID, Revision: 1}}}}
	plan, err := (RequirementExecutionPlan{ID: "timeout-plan", RequirementID: "req", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreatePlan(plan); err != nil {
		t.Fatal(err)
	}
	projection, err := repo.GetProjection(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	executor := Executor{Provider: &timedOutProvider{testProvider: newTestProvider()}, Repository: repo, Events: repo, Owner: "timeout-worker"}
	started, err := executor.DispatchReady(context.Background(), plan, &projection, testEnvelope(), "work", agent.ID)
	if err != nil || len(started) != 1 {
		t.Fatalf("dispatch=%+v err=%v", started, err)
	}
	worker := Worker{Scheduler: Scheduler{Repository: repo, Executor: executor}}
	finished, err := worker.Reconcile(context.Background(), plan, &projection)
	if err != nil {
		t.Fatal(err)
	}
	if len(finished) != 1 || finished[0].Status != AttemptTimedOut {
		t.Fatalf("finished=%+v", finished)
	}
	if projection.Status != PlanTerminal || projection.TerminalOutcome != "timed_out" {
		t.Fatalf("timeout did not close graph: %+v", projection)
	}
}

func TestClaimOutboxByIDDoesNotLeaseAnotherIntent(t *testing.T) {
	repo := NewMemoryRepository()
	now := time.Now().UTC()
	first, _, err := repo.EnqueueOutbox(OutboxRecord{ID: "first", PlanID: "plan", WorkspaceID: "w", Kind: "provider.start", IdempotencyKey: "first", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := repo.EnqueueOutbox(OutboxRecord{ID: "second", PlanID: "plan", WorkspaceID: "w", Kind: "provider.start", IdempotencyKey: "second", CreatedAt: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimOutboxByID(second.ID, "worker", time.Minute, now.Add(2*time.Second))
	if err != nil || claimed.ID != second.ID {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	items := repo.ListOutbox("plan", "leased")
	if len(items) != 1 || items[0].ID != second.ID {
		t.Fatalf("wrong intent leased: %+v (first=%+v)", items, first)
	}
}

func TestOutboxDispatcherRetriesAndTakesOverExpiredLease(t *testing.T) {
	repo := NewMemoryRepository()
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	plan, err := (RequirementExecutionPlan{ID: "outbox-dispatch", RequirementID: "req", WorkspaceID: "w", GraphSnapshot: WorkflowGraph{ID: "outbox-graph", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []WorkflowNode{{ID: "node", Kind: NodeGate}}}, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreatePlan(plan); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.EnqueueOutbox(OutboxRecord{ID: "dispatch-1", PlanID: plan.ID, WorkspaceID: plan.WorkspaceID, Kind: "provider.start", IdempotencyKey: "dispatch-1", MaxAttempts: 3, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimOutbox(plan.ID, "crashed-worker", time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ClaimOutbox(plan.ID, "worker-2", time.Minute, now.Add(2*time.Minute)); err != nil {
		t.Fatal("expired lease should be takeable: ", err)
	}
	// The second worker's failed delivery reaches the configured retry limit.
	if err := repo.AckOutbox(claimed.ID, "crashed-worker", now.Add(2*time.Minute), errors.New("stale worker")); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("stale worker must be fenced, got ", err)
	}
	if err := repo.AckOutbox(claimed.ID, "worker-2", now.Add(2*time.Minute), errors.New("provider unavailable")); err != nil {
		t.Fatal(err)
	}
	item := repo.ListOutbox(plan.ID, "")[0]
	if item.Status != "pending" || item.Attempts != 2 {
		t.Fatalf("first failure should remain retryable: %+v", item)
	}
	d := OutboxDispatcher{Store: repo, Owner: "worker-3", LeaseTTL: time.Minute, MaxBatch: 1, Now: func() time.Time { return now.Add(3 * time.Minute) }}
	report, err := d.Drain(context.Background(), plan.ID, func(context.Context, OutboxRecord) error { return errors.New("permanent provider failure") })
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 1 || repo.ListOutbox(plan.ID, "")[0].Status != "failed" {
		t.Fatalf("dispatcher should terminally fail max-attempt intent: report=%+v item=%+v", report, repo.ListOutbox(plan.ID, ""))
	}
}

func TestSquadDispatchCreatesRecoverableChildPlan(t *testing.T) {
	repo := NewMemoryRepository()
	agent := AgentDefinition{ID: "leader", WorkspaceID: "w", Revision: 1, Name: "leader", Status: AgentActive, ExecutorBinding: ExecutorBinding{ProviderID: "local"}, InputSchema: SchemaRef{ID: "input", Version: 1}, OutputSchema: SchemaRef{ID: "output", Version: 1}}
	if err := repo.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	squadGraph := WorkflowGraph{ID: "nested", Version: 1, EntryNodeIDs: []string{"child"}, ExitNodeIDs: []string{"child"}, Nodes: []WorkflowNode{{ID: "child", Kind: NodeAgent, AgentRef: &VersionedRef{ID: agent.ID, Revision: agent.Revision}}}}
	squad := SquadDefinition{ID: "squad", WorkspaceID: "w", Revision: 1, PublishedVersion: 1, Name: "squad", Status: SquadPublished, Members: []SquadMember{{ID: "leader-member", AgentID: agent.ID, Role: "leader", Leader: true}}, Graph: squadGraph}
	if err := repo.SaveSquad(squad, 0); err != nil {
		t.Fatal(err)
	}
	parentGraph := WorkflowGraph{ID: "parent", Version: 1, EntryNodeIDs: []string{"squad-node"}, ExitNodeIDs: []string{"squad-node"}, Nodes: []WorkflowNode{{ID: "squad-node", Kind: NodeSquad, SquadRef: &VersionedRef{ID: squad.ID, Revision: squad.Revision}}}}
	plan, err := (RequirementExecutionPlan{ID: "parent-plan", RequirementID: "req", WorkspaceID: "w", GraphSnapshot: parentGraph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreatePlan(plan); err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	provider := newTestProvider()
	exec := Executor{Provider: provider, Repository: repo, Events: repo, Owner: "worker"}
	started, err := exec.DispatchReady(context.Background(), plan, &projection, testEnvelope(), "work", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(started) != 1 || started[0].ChildPlanID == "" {
		t.Fatalf("squad attempt=%+v", started)
	}
	child, err := repo.GetPlan("w", started[0].ChildPlanID)
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentPlanID != plan.ID || child.ParentAttemptID != started[0].ID {
		t.Fatalf("child lineage=%+v", child)
	}
	if _, err := repo.GetProjection(child.ID); err != nil {
		t.Fatal(err)
	}
}

func TestNestedSquadDoesNotCompleteParentBeforeChild(t *testing.T) {
	repo := NewMemoryRepository()
	agent := AgentDefinition{ID: "leader", WorkspaceID: "w", Revision: 1, Name: "leader", Status: AgentActive, ExecutorBinding: ExecutorBinding{ProviderID: "mock"}, InputSchema: SchemaRef{ID: "input"}, OutputSchema: SchemaRef{ID: "output"}}
	if err := repo.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	childGraph := WorkflowGraph{ID: "child-graph", Version: 1, EntryNodeIDs: []string{"member"}, ExitNodeIDs: []string{"member"}, Nodes: []WorkflowNode{{ID: "member", Kind: NodeAgent, AgentRef: &VersionedRef{ID: agent.ID, Revision: 1}}}}
	squad := SquadDefinition{ID: "squad-blocking", WorkspaceID: "w", Revision: 1, PublishedVersion: 1, Name: "squad", Status: SquadPublished, Members: []SquadMember{{ID: "leader", AgentID: agent.ID, Role: "leader", Leader: true}}, Graph: childGraph}
	if err := repo.SaveSquad(squad, 0); err != nil {
		t.Fatal(err)
	}
	parentGraph := WorkflowGraph{ID: "parent-blocking", Version: 1, EntryNodeIDs: []string{"squad"}, ExitNodeIDs: []string{"squad"}, Nodes: []WorkflowNode{{ID: "squad", Kind: NodeSquad, SquadRef: &VersionedRef{ID: squad.ID, Revision: 1}}}}
	plan, err := (RequirementExecutionPlan{ID: "parent-blocking", RequirementID: "req", WorkspaceID: "w", GraphSnapshot: parentGraph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreatePlan(plan); err != nil {
		t.Fatal(err)
	}
	projection, err := repo.GetProjection(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := &blockingProvider{MockProvider: provider.NewMockProvider(events.NewBus())}
	executor := Executor{Provider: p, Repository: repo, Events: repo, Owner: "worker"}
	started, err := executor.DispatchReady(context.Background(), plan, &projection, testEnvelope(), "work", "")
	if err != nil || len(started) != 1 {
		t.Fatalf("dispatch=%+v err=%v", started, err)
	}
	worker := Worker{Scheduler: Scheduler{Repository: repo, Executor: executor}, MaxTicks: 1}
	finished, err := worker.Reconcile(context.Background(), plan, &projection)
	if err != nil {
		t.Fatal(err)
	}
	if len(finished) != 0 || projection.Status == PlanTerminal {
		t.Fatalf("parent completed before child: finished=%+v projection=%+v", finished, projection)
	}
}

func graphForTest() WorkflowGraph {
	return WorkflowGraph{ID: "g", Version: 1, EntryNodeIDs: []string{"dev"}, ExitNodeIDs: []string{"test"}, Nodes: []WorkflowNode{{ID: "dev", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "a", Revision: 1}}, {ID: "unit", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "u", Revision: 1}}, {ID: "test", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "t", Revision: 1}}}, Edges: []WorkflowEdge{{ID: "dev-ok", From: "dev", To: "unit", On: EdgeSuccess, MaxTraversals: 1}, {ID: "unit-ok", From: "unit", To: "test", On: EdgeSuccess, MaxTraversals: 1}, {ID: "unit-bug", From: "unit", To: "dev", On: EdgeBug, Predicate: Predicate{Kind: "field_eq", Field: "bug", Value: true}, MaxTraversals: 2}}}
}

func TestGraphValidationAndReducerFeedback(t *testing.T) {
	g := graphForTest()
	if err := ValidateGraph(g); err != nil {
		t.Fatal(err)
	}
	plan := RequirementExecutionPlan{ID: "p", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: PlanDraft}
	plan, err := plan.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	proj, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	a, err := proj.StartAttempt(plan, "dev", "a1", 1, Lease{FencingToken: 1}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, PayloadHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = proj.FinishAttempt(plan, a.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "success", Result: StructuredResult{Outcome: "pass", EvidenceIDs: []string{"dev-evidence"}}}); err != nil {
		t.Fatal(err)
	}
	if proj.Nodes["unit"].Status != AttemptReady {
		t.Fatalf("unit status %s", proj.Nodes["unit"].Status)
	}
	u, err := proj.StartAttempt(plan, "unit", "u1", 1, Lease{FencingToken: 2}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, PayloadHash: "u"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = proj.FinishAttempt(plan, u.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, Event: "bug", Result: StructuredResult{Outcome: "bug", EvidenceIDs: []string{"unit-failure"}, Fields: map[string]any{"bug": true}}}); err != nil {
		t.Fatal(err)
	}
	if proj.Nodes["dev"].Status != AttemptReady {
		t.Fatalf("feedback did not ready dev: %s", proj.Nodes["dev"].Status)
	}
}
func TestLateAttemptAndIdempotencyFailClosed(t *testing.T) {
	g := graphForTest()
	plan, _ := (RequirementExecutionPlan{ID: "p", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: PlanDraft}).Freeze()
	proj, _ := NewProjection(plan)
	a, err := proj.StartAttempt(plan, "dev", "a1", 1, Lease{FencingToken: 1}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, IdempotencyKey: "k", PayloadHash: "same"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := proj.StartAttempt(plan, "dev", "a1", 1, Lease{FencingToken: 1}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, IdempotencyKey: "k", PayloadHash: "same"}); err != nil || got.ID != a.ID {
		t.Fatalf("idempotent retry: %v %#v", err, got)
	}
	if _, err := proj.StartAttempt(plan, "dev", "a2", 2, Lease{FencingToken: 2}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, IdempotencyKey: "k", PayloadHash: "different"}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("want idempotency conflict, got %v", err)
	}
	if _, err := proj.FinishAttempt(plan, "missing", TransitionInput{}); !errors.Is(err, ErrStaleAttempt) {
		t.Fatalf("want stale attempt, got %v", err)
	}
}

func TestIdempotencyKeyReturnsOriginalAttemptAcrossRetryID(t *testing.T) {
	g := graphForTest()
	plan, _ := (RequirementExecutionPlan{ID: "p", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: PlanDraft}).Freeze()
	proj, _ := NewProjection(plan)
	a, err := proj.StartAttempt(plan, "dev", "a1", 1, Lease{FencingToken: 1}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, IdempotencyKey: "dispatch", PayloadHash: "same"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := proj.StartAttempt(plan, "dev", "different-id", 1, Lease{FencingToken: 1}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, IdempotencyKey: "dispatch", PayloadHash: "same"})
	if err != nil || got.ID != a.ID {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestFreezeIsImmutable(t *testing.T) {
	g := graphForTest()
	plan, _ := (RequirementExecutionPlan{ID: "p", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: PlanDraft}).Freeze()
	if _, err := plan.Freeze(); err == nil {
		t.Fatal("expected second freeze to fail")
	}
}

func TestPersistentRepositoryRoundTripsPlanProjectionAndEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orchestration.json")
	r, err := NewPersistentRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := (RequirementExecutionPlan{ID: "persist-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graphForTest(), Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CreatePlan(plan); err != nil {
		t.Fatal(err)
	}
	p, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SaveProjection(p); err != nil {
		t.Fatal(err)
	}
	ev, err := NewEvent(nil, plan.ID, plan.WorkspaceID, "plan.created", "plan:key", plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AppendEvent(ev); err != nil {
		t.Fatal(err)
	}
	restored, err := NewPersistentRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := restored.GetPlan("w", plan.ID); err != nil || got.PlanHash != plan.PlanHash {
		t.Fatalf("plan=%+v err=%v", got, err)
	}
	if _, err := restored.GetProjection(plan.ID); err != nil {
		t.Fatal(err)
	}
	if got := restored.ListEvents(plan.ID, 0); len(got) != 1 || got[0].EnvelopeHash != ev.EnvelopeHash {
		t.Fatalf("events=%+v", got)
	}
}

func TestFinishAttemptRejectsExpiredLease(t *testing.T) {
	plan, err := (RequirementExecutionPlan{ID: "lease-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graphForTest(), Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	a, err := p.StartAttempt(plan, "dev", "lease-attempt", 1, Lease{FencingToken: 1, ExpiresAt: now.Add(time.Second)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.FinishAttempt(plan, a.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "success", Result: StructuredResult{Outcome: "pass"}, Now: now.Add(2 * time.Second)}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("want expired lease rejection, got %v", err)
	}
}

func TestStartAttemptEnforcesPlanDeadlineAndConcurrentBudget(t *testing.T) {
	now := time.Now().UTC()
	plan, err := (RequirementExecutionPlan{ID: "budget-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graphForTest(), PolicySnapshot: PolicySnapshot{Budget: Budget{Tokens: 1, Concurrent: 1}}, Deadline: now.Add(time.Second), Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	envelope := testEnvelope()
	envelope.Manifest.TokenBudget = 2
	envelope.Manifest.TokenEstimate = 2
	if _, err := p.StartAttempt(plan, "dev", "budget-attempt", 1, Lease{FencingToken: 1, ExpiresAt: now.Add(time.Minute)}, envelope, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("want budget rejection, got %v", err)
	}
	plan.Deadline = now.Add(-time.Second)
	if _, err := p.StartAttempt(plan, "dev", "deadline-attempt", 1, Lease{FencingToken: 1, ExpiresAt: now.Add(time.Minute)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now}); !errors.Is(err, ErrDeadlineExceeded) {
		t.Fatalf("want deadline rejection, got %v", err)
	}
}

func TestFinishAttemptEnforcesNodeOutputBudget(t *testing.T) {
	now := time.Now().UTC()
	graph := WorkflowGraph{ID: "node-budget", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []WorkflowNode{{ID: "node", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "agent", Revision: 1}, Budget: Budget{Tokens: 2}}}}
	plan, err := (RequirementExecutionPlan{ID: "node-budget-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	envelope := testEnvelope()
	envelope.Manifest.TokenEstimate = 1
	a, err := p.StartAttempt(plan, "node", "node-budget-attempt", 1, Lease{FencingToken: 1, ExpiresAt: now.Add(time.Minute)}, envelope, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.FinishAttempt(plan, a.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "success", Result: StructuredResult{Outcome: "pass", EvidenceIDs: []string{"evidence"}, Fields: map[string]any{"tokens": int64(2)}}, Now: now.Add(time.Second)})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected node budget error, got %v", err)
	}
	if p.Attempts[a.ID].Status != AttemptRunning {
		t.Fatalf("budget rejection mutated attempt: %+v", p.Attempts[a.ID])
	}
}

func TestFinishAttemptRequiresEvidenceBeforeRouting(t *testing.T) {
	plan, err := (RequirementExecutionPlan{ID: "evidence-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graphForTest(), Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := projection.StartAttempt(plan, "dev", "evidence-attempt", 1, Lease{FencingToken: 1}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.FinishAttempt(plan, attempt.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "success", Result: StructuredResult{Outcome: "pass"}}); !errors.Is(err, ErrEvidenceRequired) {
		t.Fatalf("evidence-free completion advanced: %v", err)
	}
	if projection.Attempts[attempt.ID].Status != AttemptRunning || projection.Nodes["unit"].Status != AttemptPending {
		t.Fatalf("evidence rejection mutated projection: %+v", projection)
	}
}

func TestUnroutedFailureFailsClosedInsteadOfStrandingPlan(t *testing.T) {
	graph := WorkflowGraph{
		ID:           "unrouted-failure",
		Version:      1,
		EntryNodeIDs: []string{"work"},
		ExitNodeIDs:  []string{"done"},
		Nodes: []WorkflowNode{
			{ID: "work", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "work-agent", Revision: 1}},
			{ID: "done", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "done-agent", Revision: 1}},
		},
		Edges: []WorkflowEdge{{ID: "work-success", From: "work", To: "done", On: EdgeSuccess}},
	}
	plan, err := (RequirementExecutionPlan{ID: "unrouted-failure-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	attempt, err := projection.StartAttempt(plan, "work", "unrouted-failure-attempt", 1, Lease{FencingToken: 1, ExpiresAt: now.Add(time.Minute)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.FinishAttempt(plan, attempt.ID, TransitionInput{
		PlanRevision: plan.Revision,
		AttemptID:    attempt.ID,
		LeaseToken:   1,
		Event:        "failure",
		Result:       StructuredResult{Outcome: "failure", ReasonCode: "provider_failed", EvidenceIDs: []string{"failure-evidence"}},
		Failure:      &FailureReason{Code: "provider_failed", Message: "provider failed", Retryable: false},
		Now:          now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if projection.Status != PlanTerminal || projection.TerminalOutcome != "failed" {
		t.Fatalf("unrouted failure stranded plan: status=%s outcome=%q nodes=%+v", projection.Status, projection.TerminalOutcome, projection.Nodes)
	}
}

func TestSchedulerDeadlineClosesActiveAndReadyWork(t *testing.T) {
	now := time.Now().UTC()
	plan, err := (RequirementExecutionPlan{ID: "scheduler-deadline", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graphForTest(), Deadline: now.Add(time.Second), Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	active, err := projection.StartAttempt(plan, "dev", "deadline-running", 1, Lease{FencingToken: 7, ExpiresAt: now.Add(time.Minute)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 7, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	scheduler := Scheduler{Executor: Executor{}, Config: SchedulerConfig{Now: func() time.Time { return now.Add(2 * time.Second) }}}
	report, tickErr := scheduler.Tick(context.Background(), plan, &projection, testEnvelope(), "work", "")
	if !errors.Is(tickErr, ErrDeadlineExceeded) {
		t.Fatalf("want deadline error, got %v", tickErr)
	}
	if !report.Terminal || projection.Status != PlanTerminal || projection.TerminalOutcome != "timed_out" {
		t.Fatalf("deadline did not close plan: report=%+v projection=%+v", report, projection)
	}
	if got := projection.Attempts[active.ID].Status; got != AttemptTimedOut {
		t.Fatalf("active attempt status=%s", got)
	}
	if projection.Attempts[active.ID].FailureReason == nil || projection.Attempts[active.ID].FailureReason.Code != "plan_deadline_exceeded" {
		t.Fatalf("missing stable deadline reason: %+v", projection.Attempts[active.ID].FailureReason)
	}

	// A deadline must also terminate a plan that has only ready/pending nodes;
	// otherwise a worker with no MaxTicks would poll forever.
	readyProjection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	readyReport, readyErr := scheduler.Tick(context.Background(), plan, &readyProjection, testEnvelope(), "work", "")
	if !errors.Is(readyErr, ErrDeadlineExceeded) || !readyReport.Terminal || readyProjection.Status != PlanTerminal || readyProjection.TerminalOutcome != "timed_out" {
		t.Fatalf("ready deadline did not close plan: report=%+v err=%v projection=%+v", readyReport, readyErr, readyProjection)
	}
}

func TestFanOutJoinAndStructuralMerge(t *testing.T) {
	g := WorkflowGraph{ID: "fanout", Version: 1, EntryNodeIDs: []string{"source"}, ExitNodeIDs: []string{"merge"}, Nodes: []WorkflowNode{
		{ID: "source", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "a", Revision: 1}},
		{ID: "left", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "l", Revision: 1}},
		{ID: "right", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "r", Revision: 1}},
		{ID: "merge", Kind: NodeMerge, JoinPolicy: JoinAll},
	}, Edges: []WorkflowEdge{
		{ID: "to-left", From: "source", To: "left", On: EdgeSuccess, FanOut: true},
		{ID: "to-right", From: "source", To: "right", On: EdgeSuccess, FanOut: true},
		{ID: "left-merge", From: "left", To: "merge", On: EdgeSuccess},
		{ID: "right-merge", From: "right", To: "merge", On: EdgeSuccess},
	}}
	if err := ValidateGraph(g); err != nil {
		t.Fatal(err)
	}
	plan, err := (RequirementExecutionPlan{ID: "fanout-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	source, err := p.StartAttempt(plan, "source", "source-attempt", 1, Lease{FencingToken: 1, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.FinishAttempt(plan, source.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "success", Result: StructuredResult{Outcome: "pass", EvidenceIDs: []string{"source-evidence"}}, Now: now}); err != nil {
		t.Fatal(err)
	}
	if p.Nodes["left"].Status != AttemptReady || p.Nodes["right"].Status != AttemptReady {
		t.Fatalf("fanout not ready: left=%s right=%s", p.Nodes["left"].Status, p.Nodes["right"].Status)
	}
	for i, id := range []string{"left", "right"} {
		token := int64(i + 2)
		a, err := p.StartAttempt(plan, id, id+"-attempt", 1, Lease{FencingToken: token, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: token, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.FinishAttempt(plan, a.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: token, Event: "success", Result: StructuredResult{Outcome: "pass", EvidenceIDs: []string{id + "-evidence"}}, Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	if got := ReadyNodesAt(plan, p, now); len(got) != 1 || got[0].ID != "merge" {
		t.Fatalf("merge readiness=%v", got)
	}
	adv, err := (Executor{}).AdvanceStructural(context.Background(), plan, &p, testEnvelope(), 1)
	if err != nil || len(adv) != 1 || p.Status != PlanTerminal {
		t.Fatalf("structural merge adv=%v err=%v status=%s", adv, err, p.Status)
	}
}

func TestGateEvaluatorFailsClosedWithReasonAndSourceEvidence(t *testing.T) {
	graph := WorkflowGraph{ID: "gate-contract", Version: 1, EntryNodeIDs: []string{"source"}, ExitNodeIDs: []string{"gate"}, Nodes: []WorkflowNode{
		{ID: "source", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "agent", Revision: 1}},
		{ID: "gate", Kind: NodeGate, GatePolicy: GatePolicy{Predicate: Predicate{Kind: "field_eq", Field: "quality", Value: "approved"}, RequiredEvidence: []string{"review:source"}}},
	}, Edges: []WorkflowEdge{{ID: "source-gate", From: "source", To: "gate", On: EdgeSuccess}}}
	plan, err := (RequirementExecutionPlan{ID: "gate-contract-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	source, err := projection.StartAttempt(plan, "source", "gate-source", 1, Lease{FencingToken: 1, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.FinishAttempt(plan, source.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "success", Result: StructuredResult{Outcome: "pass", Fields: map[string]any{"quality": "rejected"}, EvidenceIDs: []string{"review:source"}}, Now: now}); err != nil {
		t.Fatal(err)
	}
	advanced, err := (Executor{}).AdvanceStructural(context.Background(), plan, &projection, testEnvelope(), 1)
	if err != nil || len(advanced) != 1 {
		t.Fatalf("gate advance=%+v err=%v", advanced, err)
	}
	gate := advanced[0]
	if gate.Status != AttemptFailed || gate.Result.ReasonCode != GateReasonPredicateFailed || projection.TerminalOutcome != "failed" {
		t.Fatalf("gate did not fail closed: %+v projection=%+v", gate, projection)
	}
	if !contains(gate.Result.EvidenceIDs, "review:source") || len(gate.Result.EvidenceIDs) < 2 {
		t.Fatalf("gate decision lost source or decision evidence: %+v", gate.Result.EvidenceIDs)
	}
}

func TestDiagnoseGraphSummarizesExecutionControls(t *testing.T) {
	graph := WorkflowGraph{
		ID: "diagnostics", Version: 1, EntryNodeIDs: []string{"a"}, ExitNodeIDs: []string{"review"},
		Nodes: []WorkflowNode{
			{ID: "a", Kind: NodeAgent, RetryPolicy: RetryPolicy{MaxAttempts: 3}, Budget: Budget{Tokens: 100, ToolCalls: 2, Concurrent: 2}},
			{ID: "b", Kind: NodeMerge, JoinPolicy: JoinAll, Budget: Budget{Tokens: 50}},
			{ID: "review", Kind: NodeHuman},
		},
		Edges: []WorkflowEdge{{ID: "loop", From: "b", To: "a", On: EdgeFailure, LoopGroup: "repair", MaxTraversals: 2, RequiredEvidence: []string{"test"}}},
	}
	d := DiagnoseGraph(graph)
	if d.NodeCount != 3 || d.EdgeCount != 1 || d.AgentNodeCount != 1 || d.StructuralNodeCount != 1 || d.HumanNodeCount != 1 || !d.RequiresHuman {
		t.Fatalf("unexpected graph counts: %+v", d)
	}
	if len(d.JoinNodeIDs) != 1 || d.JoinNodeIDs[0] != "b" || len(d.LoopEdgeIDs) != 1 || d.LoopEdgeIDs[0] != "loop" || len(d.RetryNodeIDs) != 1 || d.RetryNodeIDs[0] != "a" {
		t.Fatalf("execution controls were not summarized: %+v", d)
	}
	if d.TokenBudget != 150 || d.ToolCallBudget != 2 || d.MaxConcurrency != 2 || d.RequiredEvidenceEdges != 1 {
		t.Fatalf("budget summary=%+v", d)
	}
}

func TestMergeReducerRecordsConflictArtifact(t *testing.T) {
	graph := WorkflowGraph{ID: "merge-contract", Version: 1, EntryNodeIDs: []string{"left", "right"}, ExitNodeIDs: []string{"merge"}, Nodes: []WorkflowNode{
		{ID: "left", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "left-agent", Revision: 1}},
		{ID: "right", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "right-agent", Revision: 1}},
		{ID: "merge", Kind: NodeMerge, JoinPolicy: JoinAll, MergePolicy: MergePolicy{ConflictPolicy: "fail", KeyFields: []string{"release"}, RequireEvidence: true}},
	}, Edges: []WorkflowEdge{{ID: "left-merge", From: "left", To: "merge", On: EdgeSuccess}, {ID: "right-merge", From: "right", To: "merge", On: EdgeSuccess}}}
	plan, err := (RequirementExecutionPlan{ID: "merge-contract-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for index, item := range []struct {
		node     string
		value    string
		evidence string
	}{{"left", "candidate-a", "artifact:left"}, {"right", "candidate-b", "artifact:right"}} {
		token := int64(index + 1)
		attempt, startErr := projection.StartAttempt(plan, item.node, item.node+"-attempt", 1, Lease{FencingToken: token, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: token, Now: now})
		if startErr != nil {
			t.Fatal(startErr)
		}
		if _, finishErr := projection.FinishAttempt(plan, attempt.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: token, Event: "success", Result: StructuredResult{Outcome: "pass", Fields: map[string]any{"release": item.value}, EvidenceIDs: []string{item.evidence}}, Now: now}); finishErr != nil {
			t.Fatal(finishErr)
		}
	}
	advanced, err := (Executor{}).AdvanceStructural(context.Background(), plan, &projection, testEnvelope(), 1)
	if err != nil || len(advanced) != 1 {
		t.Fatalf("merge advance=%+v err=%v", advanced, err)
	}
	merge := advanced[0]
	conflicts, ok := merge.Result.Fields["conflicts"].(map[string][]any)
	if merge.Status != AttemptFailed || merge.Result.ReasonCode != MergeReasonConflict || !ok || len(conflicts["release"]) != 2 || len(merge.Result.EvidenceIDs) != 3 {
		t.Fatalf("merge conflict evidence=%+v fields=%#v", merge, merge.Result.Fields)
	}
}

func TestRepairControllerCreatesBoundedPlanWithLineage(t *testing.T) {
	graph := WorkflowGraph{ID: "repair-contract", Version: 1, EntryNodeIDs: []string{"test"}, ExitNodeIDs: []string{"done"}, Nodes: []WorkflowNode{
		{ID: "test", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "tester", Revision: 1}},
		{ID: "repair", Kind: NodeRepair, RepairPolicy: RepairPolicy{TargetNodeID: "test", Scope: []string{"internal/orchestration"}, VerificationNodeIDs: []string{"test"}, MaxRounds: 2, Budget: Budget{Tokens: 2000, ToolCalls: 5}}},
		{ID: "done", Kind: NodeHuman},
	}, Edges: []WorkflowEdge{
		{ID: "test-repair", From: "test", To: "repair", On: EdgeBug, LoopGroup: "repair", MaxTraversals: 2},
		{ID: "repair-test", From: "repair", To: "test", On: EdgeSuccess, LoopGroup: "repair", MaxTraversals: 2},
		{ID: "test-done", From: "test", To: "done", On: EdgeSuccess},
	}}
	plan, err := (RequirementExecutionPlan{ID: "repair-contract-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	source, err := projection.StartAttempt(plan, "test", "failed-test", 1, Lease{FencingToken: 1, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.FinishAttempt(plan, source.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "bug", Result: StructuredResult{Outcome: "bug", EvidenceIDs: []string{"test-report:failed"}}, Failure: &FailureReason{Code: "test_bug", Message: "regression"}, Now: now}); err != nil {
		t.Fatal(err)
	}
	advanced, err := (Executor{}).AdvanceStructural(context.Background(), plan, &projection, testEnvelope(), 1)
	if err != nil || len(advanced) != 1 {
		t.Fatalf("repair advance=%+v err=%v", advanced, err)
	}
	repair := advanced[0]
	sourceIDs, ok := repair.Result.Fields["source_attempt_ids"].([]string)
	if repair.Status != AttemptPassed || repair.Result.ReasonCode != RepairReasonPlanned || !ok || len(sourceIDs) != 1 || sourceIDs[0] != source.ID || !contains(repair.Result.EvidenceIDs, "test-report:failed") {
		t.Fatalf("repair plan lost lineage: %+v", repair)
	}
}

func TestRepairControllerFailsClosedWithoutReachableVerification(t *testing.T) {
	graph := WorkflowGraph{ID: "repair-invalid-runtime", Version: 1, EntryNodeIDs: []string{"repair"}, ExitNodeIDs: []string{"repair"}, Nodes: []WorkflowNode{
		{ID: "repair", Kind: NodeRepair, RepairPolicy: RepairPolicy{TargetNodeID: "target", VerificationNodeIDs: []string{"missing"}, MaxRounds: 1}},
		{ID: "target", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "dev", Revision: 1}},
	}, Edges: []WorkflowEdge{{ID: "repair-target", From: "repair", To: "target", On: EdgeSuccess, MaxTraversals: 1}}}
	plan := RequirementExecutionPlan{ID: "repair-invalid-runtime-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanReady, Revision: 1}
	decision, err := (DefaultRepairController{}).PlanRepair(context.Background(), StructuralInput{Plan: plan, Node: graph.Nodes[0], Incoming: []StructuralSource{{Attempt: NodeAttempt{ID: "failed", Result: StructuredResult{EvidenceIDs: []string{"failure"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Event != "failure" || decision.Result.ReasonCode != RepairReasonVerificationUnreachable || decision.Failure == nil {
		t.Fatalf("unreachable verification was not rejected: %+v", decision)
	}
}

func TestValidateGraphDerivesAUniqueRepairTarget(t *testing.T) {
	base := WorkflowGraph{
		ID: "repair-derived-target", Version: 1, EntryNodeIDs: []string{"source"}, ExitNodeIDs: []string{"verify"},
		Nodes: []WorkflowNode{
			{ID: "source", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "source-agent", Revision: 1}},
			{ID: "repair", Kind: NodeRepair, RepairPolicy: RepairPolicy{VerificationNodeIDs: []string{"verify"}, MaxRounds: 1}},
			{ID: "patch", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "developer", Revision: 1}},
			{ID: "verify", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "qa", Revision: 1}},
		},
		Edges: []WorkflowEdge{
			{ID: "source-repair", From: "source", To: "repair", On: EdgeBug, MaxTraversals: 1},
			{ID: "repair-patch", From: "repair", To: "patch", On: EdgeSuccess, MaxTraversals: 1},
			{ID: "patch-verify", From: "patch", To: "verify", On: EdgeSuccess, MaxTraversals: 1},
		},
	}
	if err := ValidateGraph(base); err != nil {
		t.Fatalf("unique success target should be derivable: %v", err)
	}
	ambiguous := base
	ambiguous.ID = "repair-ambiguous-target"
	ambiguous.Nodes = append(append([]WorkflowNode(nil), base.Nodes...), WorkflowNode{ID: "patch-alt", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "developer-2", Revision: 1}})
	ambiguous.Edges = append(append([]WorkflowEdge(nil), base.Edges...), WorkflowEdge{ID: "repair-patch-alt", From: "repair", To: "patch-alt", On: EdgeSuccess, MaxTraversals: 1}, WorkflowEdge{ID: "patch-alt-verify", From: "patch-alt", To: "verify", On: EdgeSuccess, MaxTraversals: 1})
	if err := ValidateGraph(ambiguous); err == nil || !strings.Contains(err.Error(), "target_node_id.required_or_uniquely_derivable") {
		t.Fatalf("ambiguous success targets must fail closed: %v", err)
	}
}

func TestRepairLifecycleRequiresTargetPatchAndVerification(t *testing.T) {
	graph := WorkflowGraph{ID: "repair-lifecycle", Version: 1, EntryNodeIDs: []string{"test"}, ExitNodeIDs: []string{"done"}, Nodes: []WorkflowNode{
		{ID: "test", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "tester", Revision: 1}},
		{ID: "repair", Kind: NodeRepair, RepairPolicy: RepairPolicy{VerificationNodeIDs: []string{"verify"}, MaxRounds: 2}},
		{ID: "verify", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "tester", Revision: 1}},
		{ID: "done", Kind: NodeHuman},
	}, Edges: []WorkflowEdge{
		{ID: "test-repair", From: "test", To: "repair", On: EdgeBug, LoopGroup: "repair", MaxTraversals: 2},
		{ID: "repair-test", From: "repair", To: "test", On: EdgeSuccess, LoopGroup: "repair", MaxTraversals: 2},
		{ID: "test-verify", From: "test", To: "verify", On: EdgeSuccess},
		{ID: "verify-done", From: "verify", To: "done", On: EdgeSuccess},
	}}
	plan, err := (RequirementExecutionPlan{ID: "repair-lifecycle-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	failed, err := projection.StartAttempt(plan, "test", "test-attempt-1", 1, Lease{FencingToken: 1, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.FinishAttempt(plan, failed.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "bug", Result: StructuredResult{Outcome: "bug", EvidenceIDs: []string{"unit-failure"}}, Failure: &FailureReason{Code: "unit_failed", Message: "unit test failed", Retryable: true}, Now: now}); err != nil {
		t.Fatal(err)
	}
	advanced, err := (Executor{}).AdvanceStructural(context.Background(), plan, &projection, testEnvelope(), 1)
	if err != nil || len(advanced) != 1 {
		t.Fatalf("repair planning failed: attempts=%+v err=%v", advanced, err)
	}
	repair := advanced[0]
	if repair.RepairState != RepairPlanned || len(projection.RepairPlans) != 1 {
		t.Fatalf("repair plan was not durable: attempt=%+v plans=%+v", repair, projection.RepairPlans)
	}
	if len(repair.OutputArtifacts) != 1 || repair.OutputArtifacts[0] == "" {
		t.Fatalf("repair plan artifact was not attached to immutable attempt: %+v", repair)
	}
	var repairPlan RepairPlan
	for _, candidate := range projection.RepairPlans {
		repairPlan = candidate
	}
	if repairPlan.State != RepairPlanned || repairPlan.TargetNodeID != "test" || repairPlan.VerificationNodeIDs[0] != "verify" {
		t.Fatalf("invalid repair plan=%+v", repairPlan)
	}
	if repairPlan.RepairNodeID != "repair" || len(repairPlan.RepairAttemptIDs) != 1 || repairPlan.RepairAttemptIDs[0] != repair.ID || len(repairPlan.StateHistory) != 1 || repairPlan.StateHistory[0] != RepairPlanned {
		t.Fatalf("repair plan lost immutable controller lineage: %+v", repairPlan)
	}
	target, err := projection.StartAttempt(plan, "test", "test-attempt-2", 2, Lease{FencingToken: 2, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, Now: now})
	if err != nil || target.RepairState != RepairDispatched || target.RepairPlanID != repairPlan.ID {
		t.Fatalf("target was not dispatched through repair controller: target=%+v err=%v", target, err)
	}
	if projection.RepairPlans[repairPlan.ID].State != RepairDispatched {
		t.Fatalf("target dispatch did not advance plan: %+v", projection.RepairPlans[repairPlan.ID])
	}
	patched, err := projection.FinishAttempt(plan, target.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, Event: "success", Result: StructuredResult{Outcome: "pass", EvidenceIDs: []string{"patch"}}, Now: now})
	if err != nil || patched.RepairState != RepairPatched {
		t.Fatalf("target patch did not advance lifecycle: attempt=%+v err=%v", patched, err)
	}
	verification, err := projection.StartAttempt(plan, "verify", "verify-attempt-1", 1, Lease{FencingToken: 3, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 3, Now: now})
	if err != nil || verification.RepairState != RepairVerifying || verification.RepairPlanID != repairPlan.ID {
		t.Fatalf("verification was not forced through repair controller: attempt=%+v err=%v", verification, err)
	}
	verified, err := projection.FinishAttempt(plan, verification.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 3, Event: "success", Result: StructuredResult{Outcome: "pass", EvidenceIDs: []string{"qa-pass"}}, Now: now})
	if err != nil || verified.RepairState != RepairVerified {
		t.Fatalf("verification did not complete lifecycle: attempt=%+v err=%v", verified, err)
	}
	if got := projection.RepairPlans[repairPlan.ID].State; got != RepairVerified {
		t.Fatalf("repair controller state=%q, want verified", got)
	}
	history := projection.RepairPlans[repairPlan.ID].StateHistory
	for _, want := range []RepairLifecycle{RepairPlanned, RepairDispatched, RepairPatched, RepairVerifying, RepairVerified} {
		found := false
		for _, state := range history {
			if state == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("repair controller history=%v missing %s", history, want)
		}
	}
	if err := projection.Validate(); err != nil {
		t.Fatalf("completed repair projection invalid: %v", err)
	}
}

func TestRepairWaitsForAllVerificationExitNodes(t *testing.T) {
	graph := WorkflowGraph{
		ID: "repair-multi-verification", Version: 1, EntryNodeIDs: []string{"source"}, ExitNodeIDs: []string{"verify-a", "verify-b"},
		Nodes: []WorkflowNode{
			{ID: "source", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "tester", Revision: 1}},
			{ID: "repair", Kind: NodeRepair, RepairPolicy: RepairPolicy{TargetNodeID: "patch", VerificationNodeIDs: []string{"verify-a", "verify-b"}, MaxRounds: 1}},
			{ID: "patch", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "tester", Revision: 1}},
			{ID: "verify-a", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "tester", Revision: 1}},
			{ID: "verify-b", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "tester", Revision: 1}},
		},
		Edges: []WorkflowEdge{
			{ID: "source-repair", From: "source", To: "repair", On: EdgeBug, MaxTraversals: 1},
			{ID: "repair-patch", From: "repair", To: "patch", On: EdgeSuccess, MaxTraversals: 1},
			{ID: "patch-verify-a", From: "patch", To: "verify-a", On: EdgeSuccess, FanOut: true},
			{ID: "patch-verify-b", From: "patch", To: "verify-b", On: EdgeSuccess, FanOut: true},
		},
	}
	plan, err := (RequirementExecutionPlan{ID: "repair-multi-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	start := func(nodeID, attemptID string, no int, token int64) NodeAttempt {
		t.Helper()
		attempt, startErr := projection.StartAttempt(plan, nodeID, attemptID, no, Lease{FencingToken: token, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: token, Now: now})
		if startErr != nil {
			t.Fatal(startErr)
		}
		return attempt
	}
	finish := func(attempt NodeAttempt, event string, outcome string, token int64) NodeAttempt {
		t.Helper()
		finished, finishErr := projection.FinishAttempt(plan, attempt.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: token, Event: event, Result: StructuredResult{Outcome: outcome, EvidenceIDs: []string{attempt.ID + ":evidence"}}, Now: now})
		if finishErr != nil {
			t.Fatal(finishErr)
		}
		return finished
	}

	source := start("source", "source-1", 1, 1)
	finish(source, "bug", "bug", 1)
	advanced, err := (Executor{}).AdvanceStructural(context.Background(), plan, &projection, testEnvelope(), 1)
	if err != nil || len(advanced) != 1 {
		t.Fatalf("repair planning failed: attempts=%+v err=%v", advanced, err)
	}
	patch := start("patch", "patch-1", 1, 2)
	if patch.RepairState != RepairDispatched {
		t.Fatalf("patch was not dispatched: %+v", patch)
	}
	finish(patch, "success", "pass", 2)
	verifyA := start("verify-a", "verify-a-1", 1, 3)
	verifyB := start("verify-b", "verify-b-1", 1, 4)
	if verifyA.RepairState != RepairVerifying || verifyB.RepairState != RepairVerifying {
		t.Fatalf("verification attempts were not marked verifying: a=%+v b=%+v", verifyA, verifyB)
	}
	finish(verifyA, "success", "pass", 3)
	if projection.Status == PlanTerminal {
		t.Fatalf("first verification exit prematurely terminalized plan: %+v", projection)
	}
	finish(verifyB, "success", "pass", 4)
	if projection.Status != PlanTerminal || projection.TerminalOutcome != "succeeded" {
		t.Fatalf("all verification exits did not complete plan: projection=%+v", projection)
	}
}

func TestRepairVerificationCanFollowAChainedSuccessPath(t *testing.T) {
	graph := WorkflowGraph{
		ID: "repair-chained-verification", Version: 1, EntryNodeIDs: []string{"source"}, ExitNodeIDs: []string{"qa"},
		Nodes: []WorkflowNode{
			{ID: "source", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "source-agent", Revision: 1}},
			{ID: "repair", Kind: NodeRepair, RepairPolicy: RepairPolicy{TargetNodeID: "patch", VerificationNodeIDs: []string{"unit", "qa"}, MaxRounds: 1}},
			{ID: "patch", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "developer", Revision: 1}},
			{ID: "unit", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "unit-agent", Revision: 1}},
			{ID: "qa", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "qa-agent", Revision: 1}},
		},
		Edges: []WorkflowEdge{
			{ID: "source-repair", From: "source", To: "repair", On: EdgeBug, MaxTraversals: 1},
			{ID: "repair-patch", From: "repair", To: "patch", On: EdgeSuccess, MaxTraversals: 1},
			{ID: "patch-unit", From: "patch", To: "unit", On: EdgeSuccess, MaxTraversals: 1},
			{ID: "unit-qa", From: "unit", To: "qa", On: EdgeSuccess, MaxTraversals: 1},
		},
	}
	plan, err := (RequirementExecutionPlan{ID: "repair-chained-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	start := func(nodeID, attemptID string, no int, token int64) NodeAttempt {
		t.Helper()
		attempt, startErr := projection.StartAttempt(plan, nodeID, attemptID, no, Lease{FencingToken: token, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: token, Now: now})
		if startErr != nil {
			t.Fatal(startErr)
		}
		return attempt
	}
	finish := func(attempt NodeAttempt, event, outcome string, token int64) NodeAttempt {
		t.Helper()
		finished, finishErr := projection.FinishAttempt(plan, attempt.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: token, Event: event, Result: StructuredResult{Outcome: outcome, EvidenceIDs: []string{attempt.ID + ":evidence"}}, Now: now})
		if finishErr != nil {
			t.Fatal(finishErr)
		}
		return finished
	}

	source := start("source", "chain-source-1", 1, 1)
	finish(source, "bug", "bug", 1)
	advanced, err := (Executor{}).AdvanceStructural(context.Background(), plan, &projection, testEnvelope(), 1)
	if err != nil || len(advanced) != 1 {
		t.Fatalf("repair planning failed: attempts=%+v err=%v", advanced, err)
	}
	patch := start("patch", "chain-patch-1", 1, 2)
	if patch.RepairState != RepairDispatched {
		t.Fatalf("patch was not dispatched: %+v", patch)
	}
	finish(patch, "success", "pass", 2)
	unit := start("unit", "chain-unit-1", 1, 3)
	if unit.RepairState != RepairVerifying {
		t.Fatalf("unit was not marked verifying: %+v", unit)
	}
	finish(unit, "success", "pass", 3)
	qa := start("qa", "chain-qa-1", 1, 4)
	if qa.RepairState != RepairVerifying {
		t.Fatalf("chained QA was not marked verifying: %+v", qa)
	}
	finish(qa, "success", "pass", 4)
	if projection.Status != PlanTerminal || projection.TerminalOutcome != "succeeded" {
		t.Fatalf("chained verification did not complete plan: %+v", projection)
	}
}

func TestJoinPoliciesCoverSuccessTimeoutAndShortCircuit(t *testing.T) {
	testCases := []struct {
		name            string
		policy          JoinPolicy
		quorum          int
		secondEvent     string
		secondOutcome   string
		secondEdge      EdgeEvent
		readyAfterFirst bool
		failurePolicy   string
		firstEvent      string
		firstOutcome    string
		firstEdge       EdgeEvent
	}{
		{name: "all_waits_for_timed_out_sibling", policy: JoinAll, secondEvent: "timeout", secondOutcome: "timeout", secondEdge: EdgeTimeout, firstEvent: "success", firstOutcome: "pass", firstEdge: EdgeSuccess},
		{name: "quorum_one_advances_on_first_success", policy: JoinQuorum, quorum: 1, secondEvent: "timeout", secondOutcome: "timeout", secondEdge: EdgeTimeout, readyAfterFirst: true, firstEvent: "success", firstOutcome: "pass", firstEdge: EdgeSuccess},
		{name: "first_success_advances_on_first_success", policy: JoinFirstSuccess, secondEvent: "timeout", secondOutcome: "timeout", secondEdge: EdgeTimeout, readyAfterFirst: true, firstEvent: "success", firstOutcome: "pass", firstEdge: EdgeSuccess},
		{name: "all_short_circuits_on_failure", policy: JoinAll, failurePolicy: "short_circuit", secondEvent: "success", secondOutcome: "pass", secondEdge: EdgeSuccess, readyAfterFirst: true, firstEvent: "failure", firstOutcome: "failure", firstEdge: EdgeFailure},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			graph := WorkflowGraph{ID: "join-" + tc.name, Version: 1, EntryNodeIDs: []string{"left", "right"}, ExitNodeIDs: []string{"merge"}, Nodes: []WorkflowNode{
				{ID: "left", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "left-agent", Revision: 1}},
				{ID: "right", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "right-agent", Revision: 1}},
				{ID: "merge", Kind: NodeMerge, JoinPolicy: tc.policy, JoinQuorum: tc.quorum, JoinFailurePolicy: tc.failurePolicy},
			}, Edges: []WorkflowEdge{
				{ID: "left-merge", From: "left", To: "merge", On: tc.firstEdge},
				{ID: "right-merge", From: "right", To: "merge", On: tc.secondEdge},
			}}
			plan, err := (RequirementExecutionPlan{ID: "plan-" + tc.name, RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
			if err != nil {
				t.Fatal(err)
			}
			projection, err := NewProjection(plan)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			left, err := projection.StartAttempt(plan, "left", "left-"+tc.name, 1, Lease{FencingToken: 1, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
			if err != nil {
				t.Fatal(err)
			}
			right, err := projection.StartAttempt(plan, "right", "right-"+tc.name, 1, Lease{FencingToken: 2, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, Now: now})
			if err != nil {
				t.Fatal(err)
			}
			firstFailure := (*FailureReason)(nil)
			if tc.firstEvent == "failure" {
				firstFailure = &FailureReason{Code: "branch_failed", Message: "left branch failed"}
			}
			if _, err := projection.FinishAttempt(plan, left.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: tc.firstEvent, Result: StructuredResult{Outcome: tc.firstOutcome, EvidenceIDs: []string{"left-evidence"}}, Failure: firstFailure, Now: now}); err != nil {
				t.Fatal(err)
			}
			ready := ReadyNodesAt(plan, projection, now)
			if got := len(ready) == 1 && ready[0].ID == "merge"; got != tc.readyAfterFirst {
				t.Fatalf("ready after first=%v want=%v nodes=%+v", got, tc.readyAfterFirst, ready)
			}
			if !tc.readyAfterFirst {
				secondFailure := (*FailureReason)(nil)
				if tc.secondEvent == "timeout" {
					secondFailure = &FailureReason{Code: "branch_timeout", Message: "right branch timed out"}
				}
				if _, err := projection.FinishAttempt(plan, right.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, Event: tc.secondEvent, Result: StructuredResult{Outcome: tc.secondOutcome, EvidenceIDs: []string{"right-evidence"}}, Failure: secondFailure, Now: now}); err != nil {
					t.Fatal(err)
				}
				ready = ReadyNodesAt(plan, projection, now)
				if len(ready) != 1 || ready[0].ID != "merge" {
					t.Fatalf("merge did not become ready after sibling outcome: %+v", ready)
				}
			}
			advanced, err := (Executor{}).AdvanceStructural(context.Background(), plan, &projection, testEnvelope(), 1)
			if err != nil || len(advanced) != 1 || advanced[0].NodeID != "merge" || projection.Status != PlanTerminal {
				t.Fatalf("merge advance=%+v projection=%+v err=%v", advanced, projection, err)
			}
		})
	}
}

func TestHumanTakeoverFencesOldWorkerAndRoutesTimeoutEdge(t *testing.T) {
	graph := WorkflowGraph{ID: "human-takeover", Version: 1, EntryNodeIDs: []string{"worker"}, ExitNodeIDs: []string{"human"}, Nodes: []WorkflowNode{
		{ID: "worker", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "agent", Revision: 1}},
		{ID: "human", Kind: NodeHuman},
	}, Edges: []WorkflowEdge{{ID: "timeout-human", From: "worker", To: "human", On: EdgeTimeout}}}
	plan, err := (RequirementExecutionPlan{ID: "human-takeover-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	attempt, err := projection.StartAttempt(plan, "worker", "worker-attempt", 1, Lease{Owner: "worker-1", FencingToken: 9, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 9, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := projection.TakeOver(plan, attempt.ID, "human-operator", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	finished := projection.Attempts[attempt.ID]
	if finished.Status != AttemptTimedOut || finished.Lease.FencingToken != 10 || finished.FailureReason == nil || finished.FailureReason.Code != "human_takeover" || projection.Nodes["human"].Status != AttemptReady {
		t.Fatalf("takeover state=%+v projection=%+v", finished, projection)
	}
	if _, err := projection.FinishAttempt(plan, attempt.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 9, Event: "success", Result: StructuredResult{Outcome: "pass", EvidenceIDs: []string{"late"}}, Now: now.Add(2 * time.Minute)}); err == nil {
		t.Fatal("old worker completion was accepted after takeover")
	}
	waiting, err := (Executor{}).AdvanceStructural(context.Background(), plan, &projection, testEnvelope(), 1)
	if err != nil || len(waiting) != 1 || waiting[0].NodeID != "human" || waiting[0].Status != AttemptWaiting {
		t.Fatalf("human handoff=%+v err=%v projection=%+v", waiting, err, projection)
	}
}

func TestAutomaticRetryBackoffAndLineage(t *testing.T) {
	g := WorkflowGraph{ID: "retry", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []WorkflowNode{{ID: "node", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "a", Revision: 1}, RetryPolicy: RetryPolicy{MaxAttempts: 2, Backoff: time.Minute}}}}
	plan, err := (RequirementExecutionPlan{ID: "retry-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	a, err := p.StartAttempt(plan, "node", "a1", 1, Lease{FencingToken: 1, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.FinishAttempt(plan, a.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "failure", Failure: &FailureReason{Code: "temporary", Message: "retry", Retryable: true}, Result: StructuredResult{Outcome: "failure", EvidenceIDs: []string{"retryable-failure"}}, Now: now}); err != nil {
		t.Fatal(err)
	}
	if p.Nodes["node"].Status != AttemptReady || p.Nodes["node"].RetryAt == nil {
		t.Fatalf("retry was not scheduled: %+v", p.Nodes["node"])
	}
	if _, err := p.StartAttempt(plan, "node", "a2", 2, Lease{FencingToken: 2, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, Now: now}); !errors.Is(err, ErrRetryBackoff) {
		t.Fatalf("want backoff, got %v", err)
	}
	later := now.Add(2 * time.Minute)
	next, err := p.StartAttempt(plan, "node", "a2", 2, Lease{FencingToken: 2, ExpiresAt: later.Add(time.Hour)}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, Now: later})
	if err != nil {
		t.Fatal(err)
	}
	if next.RetryOf != a.ID || next.ParentAttemptID != a.ID {
		t.Fatalf("lineage not retained: %+v", next)
	}
}

func TestHumanGateWaitsUntilApproval(t *testing.T) {
	g := WorkflowGraph{ID: "human", Version: 1, EntryNodeIDs: []string{"gate"}, ExitNodeIDs: []string{"next"}, Nodes: []WorkflowNode{{ID: "gate", Kind: NodeHuman}, {ID: "next", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "a", Revision: 1}}}, Edges: []WorkflowEdge{{ID: "approved", From: "gate", To: "next", On: EdgeApproval}}}
	plan, err := (RequirementExecutionPlan{ID: "human-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	advanced, err := (Executor{}).AdvanceStructural(context.Background(), plan, &p, testEnvelope(), 1)
	if err != nil || len(advanced) != 1 {
		t.Fatalf("advance=%v err=%v", advanced, err)
	}
	waiting := advanced[0]
	if waiting.Status != AttemptWaiting || p.Nodes["next"].Status != AttemptPending {
		t.Fatalf("gate advanced too far: %+v nodes=%+v", waiting, p.Nodes)
	}
	if _, err := p.FinishAttempt(plan, waiting.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: waiting.Lease.FencingToken, Event: "approval_granted", Result: StructuredResult{Outcome: "approved", EvidenceIDs: []string{"human-decision"}}, Now: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if p.Nodes["next"].Status != AttemptReady {
		t.Fatalf("approval did not open next node: %+v", p.Nodes["next"])
	}
}

func TestApprovalDenialDoesNotFollowApprovalEdge(t *testing.T) {
	g := WorkflowGraph{ID: "denial", Version: 1, EntryNodeIDs: []string{"gate"}, ExitNodeIDs: []string{"next"}, Nodes: []WorkflowNode{{ID: "gate", Kind: NodeHuman}, {ID: "next", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "a", Revision: 1}}}, Edges: []WorkflowEdge{{ID: "approved", From: "gate", To: "next", On: EdgeApproval}}}
	plan, err := (RequirementExecutionPlan{ID: "denial-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := (Executor{}).AdvanceStructural(context.Background(), plan, &p, testEnvelope(), 1)
	if err != nil || len(waiting) != 1 {
		t.Fatalf("advance=%v err=%v", waiting, err)
	}
	if _, err := p.FinishAttempt(plan, waiting[0].ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: waiting[0].Lease.FencingToken, Event: "approval_denied", Result: StructuredResult{Outcome: "denied", EvidenceIDs: []string{"denial"}}}); err != nil {
		t.Fatal(err)
	}
	if p.Nodes["next"].Status != AttemptPending || p.Nodes["gate"].Status != AttemptFailed {
		t.Fatalf("denial advanced graph: gate=%s next=%s", p.Nodes["gate"].Status, p.Nodes["next"].Status)
	}
}

func TestRetryOnAndQuorumValidation(t *testing.T) {
	g := WorkflowGraph{ID: "quorum", Version: 1, EntryNodeIDs: []string{"a", "b"}, ExitNodeIDs: []string{"join"}, Nodes: []WorkflowNode{{ID: "a", Kind: NodeGate}, {ID: "b", Kind: NodeGate}, {ID: "join", Kind: NodeMerge, JoinPolicy: JoinQuorum, JoinQuorum: 3}}, Edges: []WorkflowEdge{{ID: "a-join", From: "a", To: "join", On: EdgeSuccess}, {ID: "b-join", From: "b", To: "join", On: EdgeSuccess}}}
	if err := ValidateGraph(g); err == nil || !strings.Contains(err.Error(), "join_quorum.exceeds_incoming") {
		t.Fatalf("expected incoming quorum validation error, got %v", err)
	}
	g = WorkflowGraph{
		ID: "retry-on", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"},
		Nodes: []WorkflowNode{{ID: "node", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "a", Revision: 1}, RetryPolicy: RetryPolicy{MaxAttempts: 2, RetryOn: []string{"network"}}}},
	}
	plan, err := (RequirementExecutionPlan{ID: "retry-on-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.StartAttempt(plan, "node", "retry-on-attempt", 1, Lease{FencingToken: 1}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.FinishAttempt(plan, a.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "failure", Failure: &FailureReason{Code: "temporary", Message: "no retry", Retryable: true}, Result: StructuredResult{Outcome: "failure", EvidenceIDs: []string{"failure-evidence"}}}); err != nil {
		t.Fatal(err)
	}
	if p.Nodes["node"].Status != AttemptFailed || p.Nodes["node"].RetryAt != nil {
		t.Fatalf("retry-on ignored: %+v", p.Nodes["node"])
	}
}

func TestReplayProjectionAfterStructuralEvents(t *testing.T) {
	graph := WorkflowGraph{ID: "replay", Version: 1, EntryNodeIDs: []string{"gate"}, ExitNodeIDs: []string{"gate"}, Nodes: []WorkflowNode{{ID: "gate", Kind: NodeGate}}}
	plan, err := (RequirementExecutionPlan{ID: "replay-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	repo := NewMemoryRepository()
	if err := repo.CreatePlan(plan); err != nil {
		t.Fatal(err)
	}
	p, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	executor := Executor{Events: repo, Owner: "worker"}
	advanced, err := executor.AdvanceStructural(context.Background(), plan, &p, testEnvelope(), 1)
	if err != nil || len(advanced) != 1 {
		t.Fatalf("advance=%v err=%v", advanced, err)
	}
	if replayed, err := ReplayProjection(plan, repo.ListEvents(plan.ID, 0)); err != nil {
		t.Fatalf("replay failed: %v", err)
	} else if replayed.Status != PlanTerminal || replayed.Attempts[advanced[0].ID].Status != AttemptPassed {
		t.Fatalf("replayed=%+v", replayed)
	}
}

func TestOrchestrationIDsAreUUIDs(t *testing.T) {
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(NewID()) {
		t.Fatal("orchestration id is not a canonical UUID")
	}
}

func TestCreatePlanWithEventCommitsReplayBoundaryAtomically(t *testing.T) {
	graph := WorkflowGraph{ID: "atomic-plan", Version: 1, EntryNodeIDs: []string{"gate"}, ExitNodeIDs: []string{"gate"}, Nodes: []WorkflowNode{{ID: "gate", Kind: NodeGate}}}
	plan, err := (RequirementExecutionPlan{ID: "atomic-plan", RequirementID: "req", WorkspaceID: "workspace", GraphSnapshot: graph, Status: PlanDraft, IdempotencyKey: "atomic-key"}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	repo := NewMemoryRepository()
	event, err := NewEvent(nil, plan.ID, plan.WorkspaceID, "plan.created", plan.IdempotencyKey, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreatePlanWithEvent(plan, event); err != nil {
		t.Fatal(err)
	}
	if got := repo.ListEvents(plan.ID, 0); len(got) != 1 || got[0].Type != "plan.created" || got[0].Sequence != 1 {
		t.Fatalf("plan lifecycle event was not committed with plan: %+v", got)
	}
	if _, err := ReplayProjection(plan, repo.ListEvents(plan.ID, 0)); err != nil {
		t.Fatalf("atomic plan event is not replayable: %v", err)
	}
	// An idempotent retry with the same snapshot/event is a no-op.
	if err := repo.CreatePlanWithEvent(plan, event); err != nil {
		t.Fatal(err)
	}
	if got := repo.ListEvents(plan.ID, 0); len(got) != 1 {
		t.Fatalf("idempotent plan event duplicated: %+v", got)
	}
}

func TestValidateGraphRequiresStableID(t *testing.T) {
	graph := WorkflowGraph{Version: 1, EntryNodeIDs: []string{"gate"}, ExitNodeIDs: []string{"gate"}, Nodes: []WorkflowNode{{ID: "gate", Kind: NodeGate}}}
	if err := ValidateGraph(graph); err == nil || !strings.Contains(err.Error(), "graph.id.required") {
		t.Fatalf("expected graph.id.required, got %v", err)
	}
}

func TestLoopGroupRequiresHumanExit(t *testing.T) {
	graph := WorkflowGraph{ID: "bounded-loop", Version: 1, EntryNodeIDs: []string{"a"}, ExitNodeIDs: []string{"a"}, Nodes: []WorkflowNode{{ID: "a", Kind: NodeGate}}, Edges: []WorkflowEdge{{ID: "loop", From: "a", To: "a", On: EdgeSuccess, LoopGroup: "repair", MaxTraversals: 2}}}
	if err := ValidateGraph(graph); err == nil || !strings.Contains(err.Error(), "human_exit.required") {
		t.Fatalf("expected loop human exit error, got %v", err)
	}
	graph.Nodes = append(graph.Nodes, WorkflowNode{ID: "human", Kind: NodeHuman})
	graph.ExitNodeIDs = []string{"human"}
	graph.Edges = append(graph.Edges, WorkflowEdge{ID: "to-human", From: "a", To: "human", On: EdgeFailure, MaxTraversals: 1})
	if err := ValidateGraph(graph); err != nil {
		t.Fatalf("loop with human exit rejected: %v", err)
	}
}

func TestTerminalOutcomeRecordsApprovalDenial(t *testing.T) {
	graph := WorkflowGraph{ID: "denied-exit", Version: 1, EntryNodeIDs: []string{"gate"}, ExitNodeIDs: []string{"gate"}, Nodes: []WorkflowNode{{ID: "gate", Kind: NodeHuman}}}
	plan, err := (RequirementExecutionPlan{ID: "denied-exit-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := (Executor{}).AdvanceStructural(context.Background(), plan, &projection, testEnvelope(), 1)
	if err != nil || len(waiting) != 1 {
		t.Fatalf("advance=%v err=%v", waiting, err)
	}
	if _, err := projection.FinishAttempt(plan, waiting[0].ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: waiting[0].Lease.FencingToken, Event: "approval_denied", Result: StructuredResult{Outcome: "denied", EvidenceIDs: []string{"denial"}}}); err != nil {
		t.Fatal(err)
	}
	if projection.Status != PlanTerminal || projection.TerminalOutcome != "failed" {
		t.Fatalf("denied terminal outcome=%q status=%s", projection.TerminalOutcome, projection.Status)
	}
}

func TestWorkerDoesNotTreatRunningAttemptAsQuiescent(t *testing.T) {
	projection := PlanProjection{Attempts: map[string]NodeAttempt{
		"running": {ID: "running", Status: AttemptRunning},
	}}
	if !hasRunningAttempts(projection) {
		t.Fatal("running attempt was not detected")
	}
	projection.Attempts["running"] = NodeAttempt{ID: "running", Status: AttemptPassed}
	if hasRunningAttempts(projection) {
		t.Fatal("terminal attempt was reported as running")
	}
}

func TestSquadNodeIsProviderDispatchBoundary(t *testing.T) {
	r := NewMemoryRepository()
	agent := AgentDefinition{ID: "leader", WorkspaceID: "w", Revision: 1, Name: "leader", Status: AgentActive, ExecutorBinding: ExecutorBinding{ProviderID: "mock"}, InputSchema: SchemaRef{ID: "input"}, OutputSchema: SchemaRef{ID: "output"}}
	if err := r.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	squad := SquadDefinition{ID: "squad", WorkspaceID: "w", Revision: 1, Name: "squad", Status: SquadDraft, Members: []SquadMember{{ID: "leader-member", AgentID: agent.ID, Role: "leader", Leader: true}}, Graph: WorkflowGraph{ID: "squad-graph", Version: 1, EntryNodeIDs: []string{"member"}, ExitNodeIDs: []string{"member"}, Nodes: []WorkflowNode{{ID: "member", Kind: NodeAgent, AgentRef: &VersionedRef{ID: agent.ID, Revision: 1}}}}}
	if err := r.SaveSquad(squad, 0); err != nil {
		t.Fatal(err)
	}
	squad.Status = SquadPublished
	squad.Revision = 2
	squad.PublishedVersion = 1
	if err := r.SaveSquad(squad, 1); err != nil {
		t.Fatal(err)
	}
	graph := WorkflowGraph{ID: "outer", Version: 1, EntryNodeIDs: []string{"squad-node"}, ExitNodeIDs: []string{"squad-node"}, Nodes: []WorkflowNode{{ID: "squad-node", Kind: NodeSquad, SquadRef: &VersionedRef{ID: squad.ID, Revision: squad.Revision}}}}
	plan, err := (RequirementExecutionPlan{ID: "squad-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	provider := newTestProvider()
	started, err := (Executor{Provider: provider, Repository: r, Owner: "worker"}).DispatchReady(context.Background(), plan, &p, testEnvelope(), "work", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(started) != 1 || started[0].Status != AttemptRunning || provider.lastBinding != agent.ID {
		t.Fatalf("squad dispatch=%+v binding=%q", started, provider.lastBinding)
	}
}
