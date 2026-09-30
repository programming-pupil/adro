package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/adro-project/adro/core/budget"
	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/harness"
	"github.com/adro-project/adro/internal/obs/trace"
	graphmodel "github.com/adro-project/adro/internal/orchestration/graph"
	"github.com/adro-project/adro/internal/provider"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	agent := graphmodel.AgentDefinition{ID: "agent", WorkspaceID: "w", Revision: 1, Name: "agent", Role: "developer", Status: graphmodel.AgentActive, ExecutorBinding: graphmodel.ExecutorBinding{ProviderID: "local"}, InputSchema: graphmodel.SchemaRef{ID: "in", Version: 1}, OutputSchema: graphmodel.SchemaRef{ID: "out", Version: 1}}
	if err := repo.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	graph := graphmodel.WorkflowGraph{ID: "g", Version: 1, EntryNodeIDs: []string{"n"}, ExitNodeIDs: []string{"n"}, Nodes: []graphmodel.WorkflowNode{{ID: "n", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: agent.ID, Revision: 1}}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "plan", RequirementID: "req", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft, IdempotencyKey: "plan"}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	created, _ := graphmodel.NewEvent(nil, plan.ID, plan.WorkspaceID, "plan.created", "plan", plan)
	if err := repo.CreatePlanWithEvent(plan, created); err != nil {
		t.Fatal(err)
	}
	projection, err := repo.GetProjection(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, _, err := trace.StartRemoteSpan(context.Background(), "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "vendor=value")
	if err != nil {
		t.Fatal(err)
	}
	executor := Executor{Provider: p, Repository: repo, Events: repo, Owner: "worker"}
	if _, err := executor.DispatchReady(ctx, plan, &projection, testEnvelope(), "work", agent.ID); err != nil {
		t.Fatal(err)
	}
	providerSpan, err := trace.ParseTraceParent(p.lastTrace, "vendor=value")
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
	graph := graphmodel.WorkflowGraph{ID: "input-graph", Version: 1, EntryNodeIDs: []string{"agent"}, ExitNodeIDs: []string{"agent"}, Nodes: []graphmodel.WorkflowNode{{ID: "agent", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "a", Revision: 1}}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "input-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := graphmodel.NewProjection(plan)
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
	agent := graphmodel.AgentDefinition{ID: "durable-agent", WorkspaceID: "w", Revision: 1, Name: "durable", Status: graphmodel.AgentActive, ExecutorBinding: graphmodel.ExecutorBinding{ProviderID: "mock"}, InputSchema: graphmodel.SchemaRef{ID: "input", Version: 1}, OutputSchema: graphmodel.SchemaRef{ID: "output", Version: 1}}
	if err := repo.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	graph := graphmodel.WorkflowGraph{ID: "durable-graph", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []graphmodel.WorkflowNode{{ID: "node", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: agent.ID, Revision: agent.Revision}}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "durable-plan", RequirementID: "req", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
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
	if recovered.Attempts[started[0].ID].Status != graphmodel.AttemptRunning {
		t.Fatalf("projection was not durably saved: %+v", recovered.Attempts[started[0].ID])
	}
	if _, err := executor.FinishAttempt(context.Background(), plan, &recovered, started[0].ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, AttemptID: started[0].ID, LeaseToken: started[0].Lease.FencingToken, Event: "success", Result: graphmodel.StructuredResult{Outcome: "pass", EvidenceIDs: []string{"evidence"}}}); err != nil {
		t.Fatal(err)
	}
	restarted, err = NewPersistentRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	finalProjection, err := restarted.GetProjection(plan.ID)
	if err != nil || finalProjection.Status != graphmodel.PlanTerminal || finalProjection.TerminalOutcome != "succeeded" {
		t.Fatalf("finish was not durably saved: projection=%+v err=%v", finalProjection, err)
	}
}

func TestWorkerRecoversUnboundAttemptFromOutbox(t *testing.T) {
	repo := NewMemoryRepository()
	agent := graphmodel.AgentDefinition{ID: "recover-agent", WorkspaceID: "w", Revision: 1, Name: "recover", Status: graphmodel.AgentActive, ExecutorBinding: graphmodel.ExecutorBinding{ProviderID: "mock"}, InputSchema: graphmodel.SchemaRef{ID: "input"}, OutputSchema: graphmodel.SchemaRef{ID: "output"}}
	if err := repo.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	graph := graphmodel.WorkflowGraph{ID: "recover-graph", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []graphmodel.WorkflowNode{{ID: "node", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: agent.ID, Revision: agent.Revision}}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "recover-plan", RequirementID: "req", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
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
	lease := graphmodel.Lease{Key: plan.ID + ":node", Owner: "old-worker", FencingToken: 7, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	attempt, err := projection.StartAttempt(plan, "node", "unbound-attempt", 1, lease, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: lease.FencingToken, IdempotencyKey: "recover-dispatch", PayloadHash: "payload"})
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
	agent := graphmodel.AgentDefinition{ID: "timeout-agent", WorkspaceID: "w", Revision: 1, Name: "timeout", Status: graphmodel.AgentActive, ExecutorBinding: graphmodel.ExecutorBinding{ProviderID: "mock"}, InputSchema: graphmodel.SchemaRef{ID: "input"}, OutputSchema: graphmodel.SchemaRef{ID: "output"}}
	if err := repo.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	graph := graphmodel.WorkflowGraph{ID: "timeout-graph", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []graphmodel.WorkflowNode{{ID: "node", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: agent.ID, Revision: 1}}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "timeout-plan", RequirementID: "req", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
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
	if len(finished) != 1 || finished[0].Status != graphmodel.AttemptTimedOut {
		t.Fatalf("finished=%+v", finished)
	}
	if projection.Status != graphmodel.PlanTerminal || projection.TerminalOutcome != "timed_out" {
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
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "outbox-dispatch", RequirementID: "req", WorkspaceID: "w", GraphSnapshot: graphmodel.WorkflowGraph{ID: "outbox-graph", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []graphmodel.WorkflowNode{{ID: "node", Kind: graphmodel.NodeGate}}}, Status: graphmodel.PlanDraft}).Freeze()
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
	if err := repo.AckOutbox(claimed.ID, "crashed-worker", now.Add(2*time.Minute), errors.New("stale worker")); !errors.Is(err, graphmodel.ErrLeaseLost) {
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
	agent := graphmodel.AgentDefinition{ID: "leader", WorkspaceID: "w", Revision: 1, Name: "leader", Status: graphmodel.AgentActive, ExecutorBinding: graphmodel.ExecutorBinding{ProviderID: "local"}, InputSchema: graphmodel.SchemaRef{ID: "input", Version: 1}, OutputSchema: graphmodel.SchemaRef{ID: "output", Version: 1}}
	if err := repo.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	squadGraph := graphmodel.WorkflowGraph{ID: "nested", Version: 1, EntryNodeIDs: []string{"child"}, ExitNodeIDs: []string{"child"}, Nodes: []graphmodel.WorkflowNode{{ID: "child", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: agent.ID, Revision: agent.Revision}}}}
	squad := graphmodel.SquadDefinition{ID: "squad", WorkspaceID: "w", Revision: 1, PublishedVersion: 1, Name: "squad", Status: graphmodel.SquadPublished, Members: []graphmodel.SquadMember{{ID: "leader-member", AgentID: agent.ID, Role: "leader", Leader: true}}, Graph: squadGraph}
	if err := repo.SaveSquad(squad, 0); err != nil {
		t.Fatal(err)
	}
	parentGraph := graphmodel.WorkflowGraph{ID: "parent", Version: 1, EntryNodeIDs: []string{"squad-node"}, ExitNodeIDs: []string{"squad-node"}, Nodes: []graphmodel.WorkflowNode{{ID: "squad-node", Kind: graphmodel.NodeSquad, SquadRef: &graphmodel.VersionedRef{ID: squad.ID, Revision: squad.Revision}}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "parent-plan", RequirementID: "req", WorkspaceID: "w", GraphSnapshot: parentGraph, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreatePlan(plan); err != nil {
		t.Fatal(err)
	}
	projection, err := graphmodel.NewProjection(plan)
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
	agent := graphmodel.AgentDefinition{ID: "leader", WorkspaceID: "w", Revision: 1, Name: "leader", Status: graphmodel.AgentActive, ExecutorBinding: graphmodel.ExecutorBinding{ProviderID: "mock"}, InputSchema: graphmodel.SchemaRef{ID: "input"}, OutputSchema: graphmodel.SchemaRef{ID: "output"}}
	if err := repo.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	childGraph := graphmodel.WorkflowGraph{ID: "child-graph", Version: 1, EntryNodeIDs: []string{"member"}, ExitNodeIDs: []string{"member"}, Nodes: []graphmodel.WorkflowNode{{ID: "member", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: agent.ID, Revision: 1}}}}
	squad := graphmodel.SquadDefinition{ID: "squad-blocking", WorkspaceID: "w", Revision: 1, PublishedVersion: 1, Name: "squad", Status: graphmodel.SquadPublished, Members: []graphmodel.SquadMember{{ID: "leader", AgentID: agent.ID, Role: "leader", Leader: true}}, Graph: childGraph}
	if err := repo.SaveSquad(squad, 0); err != nil {
		t.Fatal(err)
	}
	parentGraph := graphmodel.WorkflowGraph{ID: "parent-blocking", Version: 1, EntryNodeIDs: []string{"squad"}, ExitNodeIDs: []string{"squad"}, Nodes: []graphmodel.WorkflowNode{{ID: "squad", Kind: graphmodel.NodeSquad, SquadRef: &graphmodel.VersionedRef{ID: squad.ID, Revision: 1}}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "parent-blocking", RequirementID: "req", WorkspaceID: "w", GraphSnapshot: parentGraph, Status: graphmodel.PlanDraft}).Freeze()
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
	if len(finished) != 0 || projection.Status == graphmodel.PlanTerminal {
		t.Fatalf("parent completed before child: finished=%+v projection=%+v", finished, projection)
	}
}

func graphForTest() graphmodel.WorkflowGraph {
	return graphmodel.WorkflowGraph{ID: "g", Version: 1, EntryNodeIDs: []string{"dev"}, ExitNodeIDs: []string{"test"}, Nodes: []graphmodel.WorkflowNode{{ID: "dev", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "a", Revision: 1}}, {ID: "unit", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "u", Revision: 1}}, {ID: "test", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "t", Revision: 1}}}, Edges: []graphmodel.WorkflowEdge{{ID: "dev-ok", From: "dev", To: "unit", On: graphmodel.EdgeSuccess, MaxTraversals: 1}, {ID: "unit-ok", From: "unit", To: "test", On: graphmodel.EdgeSuccess, MaxTraversals: 1}, {ID: "unit-bug", From: "unit", To: "dev", On: graphmodel.EdgeBug, Predicate: graphmodel.Predicate{Kind: "field_eq", Field: "bug", Value: true}, MaxTraversals: 2}}}
}

func TestGraphValidationAndReducerFeedback(t *testing.T) {
	g := graphForTest()
	if err := graphmodel.ValidateGraph(g); err != nil {
		t.Fatal(err)
	}
	plan := graphmodel.RequirementExecutionPlan{ID: "p", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: graphmodel.PlanDraft}
	plan, err := plan.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	proj, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	a, err := proj.StartAttempt(plan, "dev", "a1", 1, graphmodel.Lease{FencingToken: 1}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, PayloadHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = proj.FinishAttempt(plan, a.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "success", Result: graphmodel.StructuredResult{Outcome: "pass", EvidenceIDs: []string{"dev-evidence"}}}); err != nil {
		t.Fatal(err)
	}
	if proj.Nodes["unit"].Status != graphmodel.AttemptReady {
		t.Fatalf("unit status %s", proj.Nodes["unit"].Status)
	}
	u, err := proj.StartAttempt(plan, "unit", "u1", 1, graphmodel.Lease{FencingToken: 2}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, PayloadHash: "u"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = proj.FinishAttempt(plan, u.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, Event: "bug", Result: graphmodel.StructuredResult{Outcome: "bug", EvidenceIDs: []string{"unit-failure"}, Fields: map[string]any{"bug": true}}}); err != nil {
		t.Fatal(err)
	}
	if proj.Nodes["dev"].Status != graphmodel.AttemptReady {
		t.Fatalf("feedback did not ready dev: %s", proj.Nodes["dev"].Status)
	}
}

func TestLateAttemptAndIdempotencyFailClosed(t *testing.T) {
	g := graphForTest()
	plan, _ := (graphmodel.RequirementExecutionPlan{ID: "p", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: graphmodel.PlanDraft}).Freeze()
	proj, _ := graphmodel.NewProjection(plan)
	a, err := proj.StartAttempt(plan, "dev", "a1", 1, graphmodel.Lease{FencingToken: 1}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, IdempotencyKey: "k", PayloadHash: "same"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := proj.StartAttempt(plan, "dev", "a1", 1, graphmodel.Lease{FencingToken: 1}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, IdempotencyKey: "k", PayloadHash: "same"}); err != nil || got.ID != a.ID {
		t.Fatalf("idempotent retry: %v %#v", err, got)
	}
	if _, err := proj.StartAttempt(plan, "dev", "a2", 2, graphmodel.Lease{FencingToken: 2}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, IdempotencyKey: "k", PayloadHash: "different"}); !errors.Is(err, graphmodel.ErrIdempotencyConflict) {
		t.Fatalf("want idempotency conflict, got %v", err)
	}
	if _, err := proj.FinishAttempt(plan, "missing", graphmodel.TransitionInput{}); !errors.Is(err, graphmodel.ErrStaleAttempt) {
		t.Fatalf("want stale attempt, got %v", err)
	}
}

func TestIdempotencyKeyReturnsOriginalAttemptAcrossRetryID(t *testing.T) {
	g := graphForTest()
	plan, _ := (graphmodel.RequirementExecutionPlan{ID: "p", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: graphmodel.PlanDraft}).Freeze()
	proj, _ := graphmodel.NewProjection(plan)
	a, err := proj.StartAttempt(plan, "dev", "a1", 1, graphmodel.Lease{FencingToken: 1}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, IdempotencyKey: "dispatch", PayloadHash: "same"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := proj.StartAttempt(plan, "dev", "different-id", 1, graphmodel.Lease{FencingToken: 1}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, IdempotencyKey: "dispatch", PayloadHash: "same"})
	if err != nil || got.ID != a.ID {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestFreezeIsImmutable(t *testing.T) {
	g := graphForTest()
	plan, _ := (graphmodel.RequirementExecutionPlan{ID: "p", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: graphmodel.PlanDraft}).Freeze()
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
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "persist-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graphForTest(), Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CreatePlan(plan); err != nil {
		t.Fatal(err)
	}
	p, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SaveProjection(p); err != nil {
		t.Fatal(err)
	}
	ev, err := graphmodel.NewEvent(nil, plan.ID, plan.WorkspaceID, "plan.created", "plan:key", plan)
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
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "lease-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graphForTest(), Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	a, err := p.StartAttempt(plan, "dev", "lease-attempt", 1, graphmodel.Lease{FencingToken: 1, ExpiresAt: now.Add(time.Second)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.FinishAttempt(plan, a.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "success", Result: graphmodel.StructuredResult{Outcome: "pass"}, Now: now.Add(2 * time.Second)}); !errors.Is(err, graphmodel.ErrLeaseLost) {
		t.Fatalf("want expired lease rejection, got %v", err)
	}
}

func TestStartAttemptEnforcesPlanDeadlineAndConcurrentBudget(t *testing.T) {
	now := time.Now().UTC()
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "budget-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graphForTest(), PolicySnapshot: graphmodel.PolicySnapshot{Budget: budget.Budget{Tokens: 1, Concurrent: 1}}, Deadline: now.Add(time.Second), Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	envelope := testEnvelope()
	envelope.Manifest.TokenBudget = 2
	envelope.Manifest.TokenEstimate = 2
	if _, err := p.StartAttempt(plan, "dev", "budget-attempt", 1, graphmodel.Lease{FencingToken: 1, ExpiresAt: now.Add(time.Minute)}, envelope, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now}); !errors.Is(err, graphmodel.ErrBudgetExceeded) {
		t.Fatalf("want budget rejection, got %v", err)
	}
	plan.Deadline = now.Add(-time.Second)
	if _, err := p.StartAttempt(plan, "dev", "deadline-attempt", 1, graphmodel.Lease{FencingToken: 1, ExpiresAt: now.Add(time.Minute)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now}); !errors.Is(err, graphmodel.ErrDeadlineExceeded) {
		t.Fatalf("want deadline rejection, got %v", err)
	}
}

func TestFinishAttemptEnforcesNodeOutputBudget(t *testing.T) {
	now := time.Now().UTC()
	graph := graphmodel.WorkflowGraph{ID: "node-budget", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []graphmodel.WorkflowNode{{ID: "node", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "agent", Revision: 1}, Budget: budget.Budget{Tokens: 2}}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "node-budget-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	envelope := testEnvelope()
	envelope.Manifest.TokenEstimate = 1
	a, err := p.StartAttempt(plan, "node", "node-budget-attempt", 1, graphmodel.Lease{FencingToken: 1, ExpiresAt: now.Add(time.Minute)}, envelope, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.FinishAttempt(plan, a.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "success", Result: graphmodel.StructuredResult{Outcome: "pass", EvidenceIDs: []string{"evidence"}, Fields: map[string]any{"tokens": int64(2)}}, Now: now.Add(time.Second)})
	if !errors.Is(err, graphmodel.ErrBudgetExceeded) {
		t.Fatalf("expected node budget error, got %v", err)
	}
	if p.Attempts[a.ID].Status != graphmodel.AttemptRunning {
		t.Fatalf("budget rejection mutated attempt: %+v", p.Attempts[a.ID])
	}
}

func TestFinishAttemptRequiresEvidenceBeforeRouting(t *testing.T) {
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "evidence-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graphForTest(), Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := projection.StartAttempt(plan, "dev", "evidence-attempt", 1, graphmodel.Lease{FencingToken: 1}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.FinishAttempt(plan, attempt.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "success", Result: graphmodel.StructuredResult{Outcome: "pass"}}); !errors.Is(err, graphmodel.ErrEvidenceRequired) {
		t.Fatalf("evidence-free completion advanced: %v", err)
	}
	if projection.Attempts[attempt.ID].Status != graphmodel.AttemptRunning || projection.Nodes["unit"].Status != graphmodel.AttemptPending {
		t.Fatalf("evidence rejection mutated projection: %+v", projection)
	}
}

func TestUnroutedFailureFailsClosedInsteadOfStrandingPlan(t *testing.T) {
	graph := graphmodel.WorkflowGraph{
		ID:           "unrouted-failure",
		Version:      1,
		EntryNodeIDs: []string{"work"},
		ExitNodeIDs:  []string{"done"},
		Nodes: []graphmodel.WorkflowNode{
			{ID: "work", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "work-agent", Revision: 1}},
			{ID: "done", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "done-agent", Revision: 1}},
		},
		Edges: []graphmodel.WorkflowEdge{{ID: "work-success", From: "work", To: "done", On: graphmodel.EdgeSuccess}},
	}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "unrouted-failure-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	attempt, err := projection.StartAttempt(plan, "work", "unrouted-failure-attempt", 1, graphmodel.Lease{FencingToken: 1, ExpiresAt: now.Add(time.Minute)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.FinishAttempt(plan, attempt.ID, graphmodel.TransitionInput{
		PlanRevision: plan.Revision,
		AttemptID:    attempt.ID,
		LeaseToken:   1,
		Event:        "failure",
		Result:       graphmodel.StructuredResult{Outcome: "failure", ReasonCode: "provider_failed", EvidenceIDs: []string{"failure-evidence"}},
		Failure:      &graphmodel.FailureReason{Code: "provider_failed", Message: "provider failed", Retryable: false},
		Now:          now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if projection.Status != graphmodel.PlanTerminal || projection.TerminalOutcome != "failed" {
		t.Fatalf("unrouted failure stranded plan: status=%s outcome=%q nodes=%+v", projection.Status, projection.TerminalOutcome, projection.Nodes)
	}
}

func TestSchedulerDeadlineClosesActiveAndReadyWork(t *testing.T) {
	now := time.Now().UTC()
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "scheduler-deadline", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graphForTest(), Deadline: now.Add(time.Second), Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	active, err := projection.StartAttempt(plan, "dev", "deadline-running", 1, graphmodel.Lease{FencingToken: 7, ExpiresAt: now.Add(time.Minute)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 7, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	scheduler := Scheduler{Executor: Executor{}, Config: SchedulerConfig{Now: func() time.Time { return now.Add(2 * time.Second) }}}
	report, tickErr := scheduler.Tick(context.Background(), plan, &projection, testEnvelope(), "work", "")
	if !errors.Is(tickErr, graphmodel.ErrDeadlineExceeded) {
		t.Fatalf("want deadline error, got %v", tickErr)
	}
	if !report.Terminal || projection.Status != graphmodel.PlanTerminal || projection.TerminalOutcome != "timed_out" {
		t.Fatalf("deadline did not close plan: report=%+v projection=%+v", report, projection)
	}
	if got := projection.Attempts[active.ID].Status; got != graphmodel.AttemptTimedOut {
		t.Fatalf("active attempt status=%s", got)
	}
	if projection.Attempts[active.ID].FailureReason == nil || projection.Attempts[active.ID].FailureReason.Code != "plan_deadline_exceeded" {
		t.Fatalf("missing stable deadline reason: %+v", projection.Attempts[active.ID].FailureReason)
	}

	// A deadline must also terminate a plan that has only ready/pending nodes;
	// otherwise a worker with no MaxTicks would poll forever.
	readyProjection, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	readyReport, readyErr := scheduler.Tick(context.Background(), plan, &readyProjection, testEnvelope(), "work", "")
	if !errors.Is(readyErr, graphmodel.ErrDeadlineExceeded) || !readyReport.Terminal || readyProjection.Status != graphmodel.PlanTerminal || readyProjection.TerminalOutcome != "timed_out" {
		t.Fatalf("ready deadline did not close plan: report=%+v err=%v projection=%+v", readyReport, readyErr, readyProjection)
	}
}

func TestFanOutJoinAndStructuralMerge(t *testing.T) {
	g := graphmodel.WorkflowGraph{ID: "fanout", Version: 1, EntryNodeIDs: []string{"source"}, ExitNodeIDs: []string{"merge"}, Nodes: []graphmodel.WorkflowNode{
		{ID: "source", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "a", Revision: 1}},
		{ID: "left", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "l", Revision: 1}},
		{ID: "right", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "r", Revision: 1}},
		{ID: "merge", Kind: graphmodel.NodeMerge, JoinPolicy: graphmodel.JoinAll},
	}, Edges: []graphmodel.WorkflowEdge{
		{ID: "to-left", From: "source", To: "left", On: graphmodel.EdgeSuccess, FanOut: true},
		{ID: "to-right", From: "source", To: "right", On: graphmodel.EdgeSuccess, FanOut: true},
		{ID: "left-merge", From: "left", To: "merge", On: graphmodel.EdgeSuccess},
		{ID: "right-merge", From: "right", To: "merge", On: graphmodel.EdgeSuccess},
	}}
	if err := graphmodel.ValidateGraph(g); err != nil {
		t.Fatal(err)
	}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "fanout-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	source, err := p.StartAttempt(plan, "source", "source-attempt", 1, graphmodel.Lease{FencingToken: 1, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.FinishAttempt(plan, source.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "success", Result: graphmodel.StructuredResult{Outcome: "pass", EvidenceIDs: []string{"source-evidence"}}, Now: now}); err != nil {
		t.Fatal(err)
	}
	if p.Nodes["left"].Status != graphmodel.AttemptReady || p.Nodes["right"].Status != graphmodel.AttemptReady {
		t.Fatalf("fanout not ready: left=%s right=%s", p.Nodes["left"].Status, p.Nodes["right"].Status)
	}
	for i, id := range []string{"left", "right"} {
		token := int64(i + 2)
		a, err := p.StartAttempt(plan, id, id+"-attempt", 1, graphmodel.Lease{FencingToken: token, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: token, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.FinishAttempt(plan, a.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: token, Event: "success", Result: graphmodel.StructuredResult{Outcome: "pass", EvidenceIDs: []string{id + "-evidence"}}, Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	if got := ReadyNodesAt(plan, p, now); len(got) != 1 || got[0].ID != "merge" {
		t.Fatalf("merge readiness=%v", got)
	}
	adv, err := (Executor{}).AdvanceStructural(context.Background(), plan, &p, testEnvelope(), 1)
	if err != nil || len(adv) != 1 || p.Status != graphmodel.PlanTerminal {
		t.Fatalf("structural merge adv=%v err=%v status=%s", adv, err, p.Status)
	}
}

func TestGateEvaluatorFailsClosedWithReasonAndSourceEvidence(t *testing.T) {
	graph := graphmodel.WorkflowGraph{ID: "gate-contract", Version: 1, EntryNodeIDs: []string{"source"}, ExitNodeIDs: []string{"gate"}, Nodes: []graphmodel.WorkflowNode{
		{ID: "source", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "agent", Revision: 1}},
		{ID: "gate", Kind: graphmodel.NodeGate, GatePolicy: graphmodel.GatePolicy{Predicate: graphmodel.Predicate{Kind: "field_eq", Field: "quality", Value: "approved"}, RequiredEvidence: []string{"review:source"}}},
	}, Edges: []graphmodel.WorkflowEdge{{ID: "source-gate", From: "source", To: "gate", On: graphmodel.EdgeSuccess}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "gate-contract-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	source, err := projection.StartAttempt(plan, "source", "gate-source", 1, graphmodel.Lease{FencingToken: 1, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.FinishAttempt(plan, source.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "success", Result: graphmodel.StructuredResult{Outcome: "pass", Fields: map[string]any{"quality": "rejected"}, EvidenceIDs: []string{"review:source"}}, Now: now}); err != nil {
		t.Fatal(err)
	}
	advanced, err := (Executor{}).AdvanceStructural(context.Background(), plan, &projection, testEnvelope(), 1)
	if err != nil || len(advanced) != 1 {
		t.Fatalf("gate advance=%+v err=%v", advanced, err)
	}
	gate := advanced[0]
	if gate.Status != graphmodel.AttemptFailed || gate.Result.ReasonCode != graphmodel.GateReasonPredicateFailed || projection.TerminalOutcome != "failed" {
		t.Fatalf("gate did not fail closed: %+v projection=%+v", gate, projection)
	}
	if !graphmodel.Contains(gate.Result.EvidenceIDs, "review:source") || len(gate.Result.EvidenceIDs) < 2 {
		t.Fatalf("gate decision lost source or decision evidence: %+v", gate.Result.EvidenceIDs)
	}
}

func TestMergeReducerRecordsConflictArtifact(t *testing.T) {
	graph := graphmodel.WorkflowGraph{ID: "merge-contract", Version: 1, EntryNodeIDs: []string{"left", "right"}, ExitNodeIDs: []string{"merge"}, Nodes: []graphmodel.WorkflowNode{
		{ID: "left", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "left-agent", Revision: 1}},
		{ID: "right", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "right-agent", Revision: 1}},
		{ID: "merge", Kind: graphmodel.NodeMerge, JoinPolicy: graphmodel.JoinAll, MergePolicy: graphmodel.MergePolicy{ConflictPolicy: "fail", KeyFields: []string{"release"}, RequireEvidence: true}},
	}, Edges: []graphmodel.WorkflowEdge{{ID: "left-merge", From: "left", To: "merge", On: graphmodel.EdgeSuccess}, {ID: "right-merge", From: "right", To: "merge", On: graphmodel.EdgeSuccess}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "merge-contract-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := graphmodel.NewProjection(plan)
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
		attempt, startErr := projection.StartAttempt(plan, item.node, item.node+"-attempt", 1, graphmodel.Lease{FencingToken: token, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: token, Now: now})
		if startErr != nil {
			t.Fatal(startErr)
		}
		if _, finishErr := projection.FinishAttempt(plan, attempt.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: token, Event: "success", Result: graphmodel.StructuredResult{Outcome: "pass", Fields: map[string]any{"release": item.value}, EvidenceIDs: []string{item.evidence}}, Now: now}); finishErr != nil {
			t.Fatal(finishErr)
		}
	}
	advanced, err := (Executor{}).AdvanceStructural(context.Background(), plan, &projection, testEnvelope(), 1)
	if err != nil || len(advanced) != 1 {
		t.Fatalf("merge advance=%+v err=%v", advanced, err)
	}
	merge := advanced[0]
	conflicts, ok := merge.Result.Fields["conflicts"].(map[string][]any)
	if merge.Status != graphmodel.AttemptFailed || merge.Result.ReasonCode != graphmodel.MergeReasonConflict || !ok || len(conflicts["release"]) != 2 || len(merge.Result.EvidenceIDs) != 3 {
		t.Fatalf("merge conflict evidence=%+v fields=%#v", merge, merge.Result.Fields)
	}
}

func TestRepairControllerCreatesBoundedPlanWithLineage(t *testing.T) {
	graph := graphmodel.WorkflowGraph{ID: "repair-contract", Version: 1, EntryNodeIDs: []string{"test"}, ExitNodeIDs: []string{"done"}, Nodes: []graphmodel.WorkflowNode{
		{ID: "test", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "tester", Revision: 1}},
		{ID: "repair", Kind: graphmodel.NodeRepair, RepairPolicy: graphmodel.RepairPolicy{TargetNodeID: "test", Scope: []string{"internal/orchestration"}, VerificationNodeIDs: []string{"test"}, MaxRounds: 2, Budget: budget.Budget{Tokens: 2000, ToolCalls: 5}}},
		{ID: "done", Kind: graphmodel.NodeHuman},
	}, Edges: []graphmodel.WorkflowEdge{
		{ID: "test-repair", From: "test", To: "repair", On: graphmodel.EdgeBug, LoopGroup: "repair", MaxTraversals: 2},
		{ID: "repair-test", From: "repair", To: "test", On: graphmodel.EdgeSuccess, LoopGroup: "repair", MaxTraversals: 2},
		{ID: "test-done", From: "test", To: "done", On: graphmodel.EdgeSuccess},
	}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "repair-contract-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	source, err := projection.StartAttempt(plan, "test", "failed-test", 1, graphmodel.Lease{FencingToken: 1, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.FinishAttempt(plan, source.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "bug", Result: graphmodel.StructuredResult{Outcome: "bug", EvidenceIDs: []string{"test-report:failed"}}, Failure: &graphmodel.FailureReason{Code: "test_bug", Message: "regression"}, Now: now}); err != nil {
		t.Fatal(err)
	}
	advanced, err := (Executor{}).AdvanceStructural(context.Background(), plan, &projection, testEnvelope(), 1)
	if err != nil || len(advanced) != 1 {
		t.Fatalf("repair advance=%+v err=%v", advanced, err)
	}
	repair := advanced[0]
	sourceIDs, ok := repair.Result.Fields["source_attempt_ids"].([]string)
	if repair.Status != graphmodel.AttemptPassed || repair.Result.ReasonCode != graphmodel.RepairReasonPlanned || !ok || len(sourceIDs) != 1 || sourceIDs[0] != source.ID || !graphmodel.Contains(repair.Result.EvidenceIDs, "test-report:failed") {
		t.Fatalf("repair plan lost lineage: %+v", repair)
	}
}

func TestRepairLifecycleRequiresTargetPatchAndVerification(t *testing.T) {
	graph := graphmodel.WorkflowGraph{ID: "repair-lifecycle", Version: 1, EntryNodeIDs: []string{"test"}, ExitNodeIDs: []string{"done"}, Nodes: []graphmodel.WorkflowNode{
		{ID: "test", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "tester", Revision: 1}},
		{ID: "repair", Kind: graphmodel.NodeRepair, RepairPolicy: graphmodel.RepairPolicy{VerificationNodeIDs: []string{"verify"}, MaxRounds: 2}},
		{ID: "verify", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "tester", Revision: 1}},
		{ID: "done", Kind: graphmodel.NodeHuman},
	}, Edges: []graphmodel.WorkflowEdge{
		{ID: "test-repair", From: "test", To: "repair", On: graphmodel.EdgeBug, LoopGroup: "repair", MaxTraversals: 2},
		{ID: "repair-test", From: "repair", To: "test", On: graphmodel.EdgeSuccess, LoopGroup: "repair", MaxTraversals: 2},
		{ID: "test-verify", From: "test", To: "verify", On: graphmodel.EdgeSuccess},
		{ID: "verify-done", From: "verify", To: "done", On: graphmodel.EdgeSuccess},
	}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "repair-lifecycle-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	failed, err := projection.StartAttempt(plan, "test", "test-attempt-1", 1, graphmodel.Lease{FencingToken: 1, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.FinishAttempt(plan, failed.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "bug", Result: graphmodel.StructuredResult{Outcome: "bug", EvidenceIDs: []string{"unit-failure"}}, Failure: &graphmodel.FailureReason{Code: "unit_failed", Message: "unit test failed", Retryable: true}, Now: now}); err != nil {
		t.Fatal(err)
	}
	advanced, err := (Executor{}).AdvanceStructural(context.Background(), plan, &projection, testEnvelope(), 1)
	if err != nil || len(advanced) != 1 {
		t.Fatalf("repair planning failed: attempts=%+v err=%v", advanced, err)
	}
	repair := advanced[0]
	if repair.RepairState != graphmodel.RepairPlanned || len(projection.RepairPlans) != 1 {
		t.Fatalf("repair plan was not durable: attempt=%+v plans=%+v", repair, projection.RepairPlans)
	}
	if len(repair.OutputArtifacts) != 1 || repair.OutputArtifacts[0] == "" {
		t.Fatalf("repair plan artifact was not attached to immutable attempt: %+v", repair)
	}
	var repairPlan graphmodel.RepairPlan
	for _, candidate := range projection.RepairPlans {
		repairPlan = candidate
	}
	if repairPlan.State != graphmodel.RepairPlanned || repairPlan.TargetNodeID != "test" || repairPlan.VerificationNodeIDs[0] != "verify" {
		t.Fatalf("invalid repair plan=%+v", repairPlan)
	}
	if repairPlan.RepairNodeID != "repair" || len(repairPlan.RepairAttemptIDs) != 1 || repairPlan.RepairAttemptIDs[0] != repair.ID || len(repairPlan.StateHistory) != 1 || repairPlan.StateHistory[0] != graphmodel.RepairPlanned {
		t.Fatalf("repair plan lost immutable controller lineage: %+v", repairPlan)
	}
	target, err := projection.StartAttempt(plan, "test", "test-attempt-2", 2, graphmodel.Lease{FencingToken: 2, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, Now: now})
	if err != nil || target.RepairState != graphmodel.RepairDispatched || target.RepairPlanID != repairPlan.ID {
		t.Fatalf("target was not dispatched through repair controller: target=%+v err=%v", target, err)
	}
	if projection.RepairPlans[repairPlan.ID].State != graphmodel.RepairDispatched {
		t.Fatalf("target dispatch did not advance plan: %+v", projection.RepairPlans[repairPlan.ID])
	}
	patched, err := projection.FinishAttempt(plan, target.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, Event: "success", Result: graphmodel.StructuredResult{Outcome: "pass", EvidenceIDs: []string{"patch"}}, Now: now})
	if err != nil || patched.RepairState != graphmodel.RepairPatched {
		t.Fatalf("target patch did not advance lifecycle: attempt=%+v err=%v", patched, err)
	}
	verification, err := projection.StartAttempt(plan, "verify", "verify-attempt-1", 1, graphmodel.Lease{FencingToken: 3, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 3, Now: now})
	if err != nil || verification.RepairState != graphmodel.RepairVerifying || verification.RepairPlanID != repairPlan.ID {
		t.Fatalf("verification was not forced through repair controller: attempt=%+v err=%v", verification, err)
	}
	verified, err := projection.FinishAttempt(plan, verification.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 3, Event: "success", Result: graphmodel.StructuredResult{Outcome: "pass", EvidenceIDs: []string{"qa-pass"}}, Now: now})
	if err != nil || verified.RepairState != graphmodel.RepairVerified {
		t.Fatalf("verification did not complete lifecycle: attempt=%+v err=%v", verified, err)
	}
	if got := projection.RepairPlans[repairPlan.ID].State; got != graphmodel.RepairVerified {
		t.Fatalf("repair controller state=%q, want verified", got)
	}
	history := projection.RepairPlans[repairPlan.ID].StateHistory
	for _, want := range []graphmodel.RepairLifecycle{graphmodel.RepairPlanned, graphmodel.RepairDispatched, graphmodel.RepairPatched, graphmodel.RepairVerifying, graphmodel.RepairVerified} {
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

func TestRepairLifecycleRetainsRoundAcrossTransientProviderFailure(t *testing.T) {
	graph := graphmodel.WorkflowGraph{ID: "repair-transient-provider", Version: 1, EntryNodeIDs: []string{"test"}, ExitNodeIDs: []string{"done"}, Nodes: []graphmodel.WorkflowNode{
		{ID: "test", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "tester", Revision: 1}, RetryPolicy: graphmodel.RetryPolicy{MaxAttempts: 3}},
		{ID: "repair", Kind: graphmodel.NodeRepair, RepairPolicy: graphmodel.RepairPolicy{VerificationNodeIDs: []string{"verify"}, MaxRounds: 2}},
		{ID: "verify", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "tester", Revision: 1}, RetryPolicy: graphmodel.RetryPolicy{MaxAttempts: 3}},
		{ID: "done", Kind: graphmodel.NodeHuman},
	}, Edges: []graphmodel.WorkflowEdge{
		{ID: "test-repair", From: "test", To: "repair", On: graphmodel.EdgeBug, LoopGroup: "repair", MaxTraversals: 2},
		{ID: "repair-test", From: "repair", To: "test", On: graphmodel.EdgeSuccess, LoopGroup: "repair", MaxTraversals: 2},
		{ID: "test-verify", From: "test", To: "verify", On: graphmodel.EdgeSuccess},
		{ID: "verify-done", From: "verify", To: "done", On: graphmodel.EdgeSuccess},
	}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "repair-transient-provider-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	failed, err := projection.StartAttempt(plan, "test", "transient-test-1", 1, graphmodel.Lease{FencingToken: 1, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.FinishAttempt(plan, failed.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "bug", Result: graphmodel.StructuredResult{Outcome: "bug", EvidenceIDs: []string{"unit-failure"}}, Failure: &graphmodel.FailureReason{Code: "unit_failed", Message: "unit test failed"}, Now: now}); err != nil {
		t.Fatal(err)
	}
	advanced, err := (Executor{}).AdvanceStructural(context.Background(), plan, &projection, testEnvelope(), 1)
	if err != nil || len(advanced) != 1 {
		t.Fatalf("repair planning failed: attempts=%+v err=%v", advanced, err)
	}
	repairID := advanced[0].RepairPlanID

	target1, err := projection.StartAttempt(plan, "test", "transient-test-2", 2, graphmodel.Lease{FencingToken: 2, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, Now: now})
	if err != nil || target1.RepairPlanID != repairID || target1.RepairState != graphmodel.RepairDispatched {
		t.Fatalf("repair target was not dispatched: attempt=%+v err=%v", target1, err)
	}
	if _, err := projection.FinishAttempt(plan, target1.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, Event: "failure", Result: graphmodel.StructuredResult{Outcome: "failure", EvidenceIDs: []string{"provider-failed"}}, Failure: &graphmodel.FailureReason{Code: "provider_failed", Message: "upstream provider failed", Retryable: true}, Now: now}); err != nil {
		t.Fatal(err)
	}
	if got := projection.RepairPlans[repairID].State; got != graphmodel.RepairDispatched {
		t.Fatalf("transient target failure consumed repair lifecycle: got=%s plan=%+v", got, projection.RepairPlans[repairID])
	}

	target2, err := projection.StartAttempt(plan, "test", "transient-test-3", 3, graphmodel.Lease{FencingToken: 3, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 3, Now: now})
	if err != nil || target2.RepairPlanID != repairID || target2.RepairState != graphmodel.RepairDispatched {
		t.Fatalf("target retry lost repair lineage: attempt=%+v err=%v", target2, err)
	}
	if got := projection.RepairPlans[repairID].Round; got != 1 {
		t.Fatalf("transient target failure advanced repair round: got=%d", got)
	}
	if _, err := projection.FinishAttempt(plan, target2.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 3, Event: "success", Result: graphmodel.StructuredResult{Outcome: "pass", EvidenceIDs: []string{"patch"}}, Now: now}); err != nil {
		t.Fatal(err)
	}

	verify1, err := projection.StartAttempt(plan, "verify", "transient-verify-1", 1, graphmodel.Lease{FencingToken: 4, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 4, Now: now})
	if err != nil || verify1.RepairPlanID != repairID || verify1.RepairState != graphmodel.RepairVerifying {
		t.Fatalf("verification was not linked to repair plan: attempt=%+v err=%v", verify1, err)
	}
	if _, err := projection.FinishAttempt(plan, verify1.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 4, Event: "failure", Result: graphmodel.StructuredResult{Outcome: "failure", EvidenceIDs: []string{"provider-failed-verification"}}, Failure: &graphmodel.FailureReason{Code: "provider_failed", Message: "upstream provider failed", Retryable: true}, Now: now}); err != nil {
		t.Fatal(err)
	}
	if got := projection.RepairPlans[repairID].State; got != graphmodel.RepairVerifying {
		t.Fatalf("transient verification failure consumed repair lifecycle: got=%s plan=%+v", got, projection.RepairPlans[repairID])
	}

	verify2, err := projection.StartAttempt(plan, "verify", "transient-verify-2", 2, graphmodel.Lease{FencingToken: 5, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 5, Now: now})
	if err != nil || verify2.RepairPlanID != repairID || verify2.RepairState != graphmodel.RepairVerifying {
		t.Fatalf("verification retry lost repair lineage: attempt=%+v err=%v", verify2, err)
	}
	if _, err := projection.FinishAttempt(plan, verify2.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 5, Event: "success", Result: graphmodel.StructuredResult{Outcome: "pass", EvidenceIDs: []string{"qa-pass"}}, Now: now}); err != nil {
		t.Fatal(err)
	}
	if got := projection.RepairPlans[repairID].State; got != graphmodel.RepairVerified {
		t.Fatalf("repair lifecycle did not verify after transient retries: got=%s plan=%+v", got, projection.RepairPlans[repairID])
	}
	if err := projection.Validate(); err != nil {
		t.Fatalf("repair projection invalid after transient retries: %v", err)
	}
}

func TestRepairWaitsForAllVerificationExitNodes(t *testing.T) {
	graph := graphmodel.WorkflowGraph{
		ID: "repair-multi-verification", Version: 1, EntryNodeIDs: []string{"source"}, ExitNodeIDs: []string{"verify-a", "verify-b"},
		Nodes: []graphmodel.WorkflowNode{
			{ID: "source", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "tester", Revision: 1}},
			{ID: "repair", Kind: graphmodel.NodeRepair, RepairPolicy: graphmodel.RepairPolicy{TargetNodeID: "patch", VerificationNodeIDs: []string{"verify-a", "verify-b"}, MaxRounds: 1}},
			{ID: "patch", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "tester", Revision: 1}},
			{ID: "verify-a", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "tester", Revision: 1}},
			{ID: "verify-b", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "tester", Revision: 1}},
		},
		Edges: []graphmodel.WorkflowEdge{
			{ID: "source-repair", From: "source", To: "repair", On: graphmodel.EdgeBug, MaxTraversals: 1},
			{ID: "repair-patch", From: "repair", To: "patch", On: graphmodel.EdgeSuccess, MaxTraversals: 1},
			{ID: "patch-verify-a", From: "patch", To: "verify-a", On: graphmodel.EdgeSuccess, FanOut: true},
			{ID: "patch-verify-b", From: "patch", To: "verify-b", On: graphmodel.EdgeSuccess, FanOut: true},
		},
	}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "repair-multi-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	start := func(nodeID, attemptID string, no int, token int64) graphmodel.NodeAttempt {
		t.Helper()
		attempt, startErr := projection.StartAttempt(plan, nodeID, attemptID, no, graphmodel.Lease{FencingToken: token, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: token, Now: now})
		if startErr != nil {
			t.Fatal(startErr)
		}
		return attempt
	}
	finish := func(attempt graphmodel.NodeAttempt, event string, outcome string, token int64) graphmodel.NodeAttempt {
		t.Helper()
		finished, finishErr := projection.FinishAttempt(plan, attempt.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: token, Event: event, Result: graphmodel.StructuredResult{Outcome: outcome, EvidenceIDs: []string{attempt.ID + ":evidence"}}, Now: now})
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
	if patch.RepairState != graphmodel.RepairDispatched {
		t.Fatalf("patch was not dispatched: %+v", patch)
	}
	finish(patch, "success", "pass", 2)
	verifyA := start("verify-a", "verify-a-1", 1, 3)
	verifyB := start("verify-b", "verify-b-1", 1, 4)
	if verifyA.RepairState != graphmodel.RepairVerifying || verifyB.RepairState != graphmodel.RepairVerifying {
		t.Fatalf("verification attempts were not marked verifying: a=%+v b=%+v", verifyA, verifyB)
	}
	finish(verifyA, "success", "pass", 3)
	if projection.Status == graphmodel.PlanTerminal {
		t.Fatalf("first verification exit prematurely terminalized plan: %+v", projection)
	}
	finish(verifyB, "success", "pass", 4)
	if projection.Status != graphmodel.PlanTerminal || projection.TerminalOutcome != "succeeded" {
		t.Fatalf("all verification exits did not complete plan: projection=%+v", projection)
	}
}

func TestRepairVerificationCanFollowAChainedSuccessPath(t *testing.T) {
	graph := graphmodel.WorkflowGraph{
		ID: "repair-chained-verification", Version: 1, EntryNodeIDs: []string{"source"}, ExitNodeIDs: []string{"qa"},
		Nodes: []graphmodel.WorkflowNode{
			{ID: "source", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "source-agent", Revision: 1}},
			{ID: "repair", Kind: graphmodel.NodeRepair, RepairPolicy: graphmodel.RepairPolicy{TargetNodeID: "patch", VerificationNodeIDs: []string{"unit", "qa"}, MaxRounds: 1}},
			{ID: "patch", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "developer", Revision: 1}},
			{ID: "unit", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "unit-agent", Revision: 1}},
			{ID: "qa", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "qa-agent", Revision: 1}},
		},
		Edges: []graphmodel.WorkflowEdge{
			{ID: "source-repair", From: "source", To: "repair", On: graphmodel.EdgeBug, MaxTraversals: 1},
			{ID: "repair-patch", From: "repair", To: "patch", On: graphmodel.EdgeSuccess, MaxTraversals: 1},
			{ID: "patch-unit", From: "patch", To: "unit", On: graphmodel.EdgeSuccess, MaxTraversals: 1},
			{ID: "unit-qa", From: "unit", To: "qa", On: graphmodel.EdgeSuccess, MaxTraversals: 1},
		},
	}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "repair-chained-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	start := func(nodeID, attemptID string, no int, token int64) graphmodel.NodeAttempt {
		t.Helper()
		attempt, startErr := projection.StartAttempt(plan, nodeID, attemptID, no, graphmodel.Lease{FencingToken: token, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: token, Now: now})
		if startErr != nil {
			t.Fatal(startErr)
		}
		return attempt
	}
	finish := func(attempt graphmodel.NodeAttempt, event, outcome string, token int64) graphmodel.NodeAttempt {
		t.Helper()
		finished, finishErr := projection.FinishAttempt(plan, attempt.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: token, Event: event, Result: graphmodel.StructuredResult{Outcome: outcome, EvidenceIDs: []string{attempt.ID + ":evidence"}}, Now: now})
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
	if patch.RepairState != graphmodel.RepairDispatched {
		t.Fatalf("patch was not dispatched: %+v", patch)
	}
	finish(patch, "success", "pass", 2)
	unit := start("unit", "chain-unit-1", 1, 3)
	if unit.RepairState != graphmodel.RepairVerifying {
		t.Fatalf("unit was not marked verifying: %+v", unit)
	}
	finish(unit, "success", "pass", 3)
	qa := start("qa", "chain-qa-1", 1, 4)
	if qa.RepairState != graphmodel.RepairVerifying {
		t.Fatalf("chained QA was not marked verifying: %+v", qa)
	}
	finish(qa, "success", "pass", 4)
	if projection.Status != graphmodel.PlanTerminal || projection.TerminalOutcome != "succeeded" {
		t.Fatalf("chained verification did not complete plan: %+v", projection)
	}
}

func TestJoinPoliciesCoverSuccessTimeoutAndShortCircuit(t *testing.T) {
	testCases := []struct {
		name            string
		policy          graphmodel.JoinPolicy
		quorum          int
		secondEvent     string
		secondOutcome   string
		secondEdge      graphmodel.EdgeEvent
		readyAfterFirst bool
		failurePolicy   string
		firstEvent      string
		firstOutcome    string
		firstEdge       graphmodel.EdgeEvent
	}{
		{name: "all_waits_for_timed_out_sibling", policy: graphmodel.JoinAll, secondEvent: "timeout", secondOutcome: "timeout", secondEdge: graphmodel.EdgeTimeout, firstEvent: "success", firstOutcome: "pass", firstEdge: graphmodel.EdgeSuccess},
		{name: "quorum_one_advances_on_first_success", policy: graphmodel.JoinQuorum, quorum: 1, secondEvent: "timeout", secondOutcome: "timeout", secondEdge: graphmodel.EdgeTimeout, readyAfterFirst: true, firstEvent: "success", firstOutcome: "pass", firstEdge: graphmodel.EdgeSuccess},
		{name: "first_success_advances_on_first_success", policy: graphmodel.JoinFirstSuccess, secondEvent: "timeout", secondOutcome: "timeout", secondEdge: graphmodel.EdgeTimeout, readyAfterFirst: true, firstEvent: "success", firstOutcome: "pass", firstEdge: graphmodel.EdgeSuccess},
		{name: "all_short_circuits_on_failure", policy: graphmodel.JoinAll, failurePolicy: "short_circuit", secondEvent: "success", secondOutcome: "pass", secondEdge: graphmodel.EdgeSuccess, readyAfterFirst: true, firstEvent: "failure", firstOutcome: "failure", firstEdge: graphmodel.EdgeFailure},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			graph := graphmodel.WorkflowGraph{ID: "join-" + tc.name, Version: 1, EntryNodeIDs: []string{"left", "right"}, ExitNodeIDs: []string{"merge"}, Nodes: []graphmodel.WorkflowNode{
				{ID: "left", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "left-agent", Revision: 1}},
				{ID: "right", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "right-agent", Revision: 1}},
				{ID: "merge", Kind: graphmodel.NodeMerge, JoinPolicy: tc.policy, JoinQuorum: tc.quorum, JoinFailurePolicy: tc.failurePolicy},
			}, Edges: []graphmodel.WorkflowEdge{
				{ID: "left-merge", From: "left", To: "merge", On: tc.firstEdge},
				{ID: "right-merge", From: "right", To: "merge", On: tc.secondEdge},
			}}
			plan, err := (graphmodel.RequirementExecutionPlan{ID: "plan-" + tc.name, RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
			if err != nil {
				t.Fatal(err)
			}
			projection, err := graphmodel.NewProjection(plan)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			left, err := projection.StartAttempt(plan, "left", "left-"+tc.name, 1, graphmodel.Lease{FencingToken: 1, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
			if err != nil {
				t.Fatal(err)
			}
			right, err := projection.StartAttempt(plan, "right", "right-"+tc.name, 1, graphmodel.Lease{FencingToken: 2, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, Now: now})
			if err != nil {
				t.Fatal(err)
			}
			firstFailure := (*graphmodel.FailureReason)(nil)
			if tc.firstEvent == "failure" {
				firstFailure = &graphmodel.FailureReason{Code: "branch_failed", Message: "left branch failed"}
			}
			if _, err := projection.FinishAttempt(plan, left.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: tc.firstEvent, Result: graphmodel.StructuredResult{Outcome: tc.firstOutcome, EvidenceIDs: []string{"left-evidence"}}, Failure: firstFailure, Now: now}); err != nil {
				t.Fatal(err)
			}
			ready := ReadyNodesAt(plan, projection, now)
			if got := len(ready) == 1 && ready[0].ID == "merge"; got != tc.readyAfterFirst {
				t.Fatalf("ready after first=%v want=%v nodes=%+v", got, tc.readyAfterFirst, ready)
			}
			if !tc.readyAfterFirst {
				secondFailure := (*graphmodel.FailureReason)(nil)
				if tc.secondEvent == "timeout" {
					secondFailure = &graphmodel.FailureReason{Code: "branch_timeout", Message: "right branch timed out"}
				}
				if _, err := projection.FinishAttempt(plan, right.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, Event: tc.secondEvent, Result: graphmodel.StructuredResult{Outcome: tc.secondOutcome, EvidenceIDs: []string{"right-evidence"}}, Failure: secondFailure, Now: now}); err != nil {
					t.Fatal(err)
				}
				ready = ReadyNodesAt(plan, projection, now)
				if len(ready) != 1 || ready[0].ID != "merge" {
					t.Fatalf("merge did not become ready after sibling outcome: %+v", ready)
				}
			}
			advanced, err := (Executor{}).AdvanceStructural(context.Background(), plan, &projection, testEnvelope(), 1)
			if err != nil || len(advanced) != 1 || advanced[0].NodeID != "merge" || projection.Status != graphmodel.PlanTerminal {
				t.Fatalf("merge advance=%+v projection=%+v err=%v", advanced, projection, err)
			}
		})
	}
}

func TestHumanTakeoverFencesOldWorkerAndRoutesTimeoutEdge(t *testing.T) {
	graph := graphmodel.WorkflowGraph{ID: "human-takeover", Version: 1, EntryNodeIDs: []string{"worker"}, ExitNodeIDs: []string{"human"}, Nodes: []graphmodel.WorkflowNode{
		{ID: "worker", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "agent", Revision: 1}},
		{ID: "human", Kind: graphmodel.NodeHuman},
	}, Edges: []graphmodel.WorkflowEdge{{ID: "timeout-human", From: "worker", To: "human", On: graphmodel.EdgeTimeout}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "human-takeover-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	attempt, err := projection.StartAttempt(plan, "worker", "worker-attempt", 1, graphmodel.Lease{Owner: "worker-1", FencingToken: 9, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 9, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := projection.TakeOver(plan, attempt.ID, "human-operator", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	finished := projection.Attempts[attempt.ID]
	if finished.Status != graphmodel.AttemptTimedOut || finished.Lease.FencingToken != 10 || finished.FailureReason == nil || finished.FailureReason.Code != "human_takeover" || projection.Nodes["human"].Status != graphmodel.AttemptReady {
		t.Fatalf("takeover state=%+v projection=%+v", finished, projection)
	}
	if _, err := projection.FinishAttempt(plan, attempt.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 9, Event: "success", Result: graphmodel.StructuredResult{Outcome: "pass", EvidenceIDs: []string{"late"}}, Now: now.Add(2 * time.Minute)}); err == nil {
		t.Fatal("old worker completion was accepted after takeover")
	}
	waiting, err := (Executor{}).AdvanceStructural(context.Background(), plan, &projection, testEnvelope(), 1)
	if err != nil || len(waiting) != 1 || waiting[0].NodeID != "human" || waiting[0].Status != graphmodel.AttemptWaiting {
		t.Fatalf("human handoff=%+v err=%v projection=%+v", waiting, err, projection)
	}
}

func TestAutomaticRetryBackoffAndLineage(t *testing.T) {
	g := graphmodel.WorkflowGraph{ID: "retry", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []graphmodel.WorkflowNode{{ID: "node", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "a", Revision: 1}, RetryPolicy: graphmodel.RetryPolicy{MaxAttempts: 2, Backoff: time.Minute}}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "retry-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	a, err := p.StartAttempt(plan, "node", "a1", 1, graphmodel.Lease{FencingToken: 1, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.FinishAttempt(plan, a.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "failure", Failure: &graphmodel.FailureReason{Code: "temporary", Message: "retry", Retryable: true}, Result: graphmodel.StructuredResult{Outcome: "failure", EvidenceIDs: []string{"retryable-failure"}}, Now: now}); err != nil {
		t.Fatal(err)
	}
	if p.Nodes["node"].Status != graphmodel.AttemptReady || p.Nodes["node"].RetryAt == nil {
		t.Fatalf("retry was not scheduled: %+v", p.Nodes["node"])
	}
	if _, err := p.StartAttempt(plan, "node", "a2", 2, graphmodel.Lease{FencingToken: 2, ExpiresAt: now.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, Now: now}); !errors.Is(err, graphmodel.ErrRetryBackoff) {
		t.Fatalf("want backoff, got %v", err)
	}
	later := now.Add(2 * time.Minute)
	next, err := p.StartAttempt(plan, "node", "a2", 2, graphmodel.Lease{FencingToken: 2, ExpiresAt: later.Add(time.Hour)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, Now: later})
	if err != nil {
		t.Fatal(err)
	}
	if next.RetryOf != a.ID || next.ParentAttemptID != a.ID {
		t.Fatalf("lineage not retained: %+v", next)
	}
}

func TestFailedExitTerminalizesWhenRepairVerificationIsPending(t *testing.T) {
	graph := graphmodel.WorkflowGraph{
		ID: "failed-exit-with-pending-repair", Version: 1,
		EntryNodeIDs: []string{"qa"}, ExitNodeIDs: []string{"qa"},
		Nodes: []graphmodel.WorkflowNode{{ID: "qa", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "qa-agent", Revision: 1}, RetryPolicy: graphmodel.RetryPolicy{MaxAttempts: 1}}},
	}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "failed-exit-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	projection.RepairPlans["repair-1"] = graphmodel.RepairPlan{ID: "repair-1", PlanID: plan.ID, RepairNodeID: "repair", RepairAttemptID: "repair-attempt", TargetNodeID: "developer", VerificationNodeIDs: []string{"unit"}, MaxRounds: 1, Round: 1, State: graphmodel.RepairPlanned, StateHistory: []graphmodel.RepairLifecycle{graphmodel.RepairPlanned}}
	now := time.Now().UTC()
	attempt, err := projection.StartAttempt(plan, "qa", "qa-attempt", 1, graphmodel.Lease{FencingToken: 1, ExpiresAt: now.Add(time.Minute)}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	finished, err := projection.FinishAttempt(plan, attempt.ID, graphmodel.TransitionInput{
		PlanRevision: plan.Revision,
		AttemptID:    attempt.ID,
		LeaseToken:   attempt.Lease.FencingToken,
		Event:        "failure",
		Result:       graphmodel.StructuredResult{Outcome: "failure", Summary: "provider exhausted retries", EvidenceIDs: []string{"qa-provider-failure"}},
		Failure:      &graphmodel.FailureReason{Code: "provider_failed", Message: "provider exhausted retries", Retryable: true},
		Now:          now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != graphmodel.AttemptFailed || projection.Status != graphmodel.PlanTerminal || projection.TerminalOutcome != "failed" {
		t.Fatalf("failed exit was stranded: attempt=%+v status=%s outcome=%q", finished, projection.Status, projection.TerminalOutcome)
	}
}

func TestHumanGateWaitsUntilApproval(t *testing.T) {
	g := graphmodel.WorkflowGraph{ID: "human", Version: 1, EntryNodeIDs: []string{"gate"}, ExitNodeIDs: []string{"next"}, Nodes: []graphmodel.WorkflowNode{{ID: "gate", Kind: graphmodel.NodeHuman}, {ID: "next", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "a", Revision: 1}}}, Edges: []graphmodel.WorkflowEdge{{ID: "approved", From: "gate", To: "next", On: graphmodel.EdgeApproval}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "human-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	advanced, err := (Executor{}).AdvanceStructural(context.Background(), plan, &p, testEnvelope(), 1)
	if err != nil || len(advanced) != 1 {
		t.Fatalf("advance=%v err=%v", advanced, err)
	}
	waiting := advanced[0]
	if waiting.Status != graphmodel.AttemptWaiting || p.Nodes["next"].Status != graphmodel.AttemptPending {
		t.Fatalf("gate advanced too far: %+v nodes=%+v", waiting, p.Nodes)
	}
	if _, err := p.FinishAttempt(plan, waiting.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: waiting.Lease.FencingToken, Event: "approval_granted", Result: graphmodel.StructuredResult{Outcome: "approved", EvidenceIDs: []string{"human-decision"}}, Now: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if p.Nodes["next"].Status != graphmodel.AttemptReady {
		t.Fatalf("approval did not open next node: %+v", p.Nodes["next"])
	}
}

func TestApprovalDenialDoesNotFollowApprovalEdge(t *testing.T) {
	g := graphmodel.WorkflowGraph{ID: "denial", Version: 1, EntryNodeIDs: []string{"gate"}, ExitNodeIDs: []string{"next"}, Nodes: []graphmodel.WorkflowNode{{ID: "gate", Kind: graphmodel.NodeHuman}, {ID: "next", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "a", Revision: 1}}}, Edges: []graphmodel.WorkflowEdge{{ID: "approved", From: "gate", To: "next", On: graphmodel.EdgeApproval}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "denial-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := (Executor{}).AdvanceStructural(context.Background(), plan, &p, testEnvelope(), 1)
	if err != nil || len(waiting) != 1 {
		t.Fatalf("advance=%v err=%v", waiting, err)
	}
	if _, err := p.FinishAttempt(plan, waiting[0].ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: waiting[0].Lease.FencingToken, Event: "approval_denied", Result: graphmodel.StructuredResult{Outcome: "denied", EvidenceIDs: []string{"denial"}}}); err != nil {
		t.Fatal(err)
	}
	if p.Nodes["next"].Status != graphmodel.AttemptPending || p.Nodes["gate"].Status != graphmodel.AttemptFailed {
		t.Fatalf("denial advanced graph: gate=%s next=%s", p.Nodes["gate"].Status, p.Nodes["next"].Status)
	}
}

func TestRetryOnAndQuorumValidation(t *testing.T) {
	g := graphmodel.WorkflowGraph{ID: "quorum", Version: 1, EntryNodeIDs: []string{"a", "b"}, ExitNodeIDs: []string{"join"}, Nodes: []graphmodel.WorkflowNode{{ID: "a", Kind: graphmodel.NodeGate}, {ID: "b", Kind: graphmodel.NodeGate}, {ID: "join", Kind: graphmodel.NodeMerge, JoinPolicy: graphmodel.JoinQuorum, JoinQuorum: 3}}, Edges: []graphmodel.WorkflowEdge{{ID: "a-join", From: "a", To: "join", On: graphmodel.EdgeSuccess}, {ID: "b-join", From: "b", To: "join", On: graphmodel.EdgeSuccess}}}
	if err := graphmodel.ValidateGraph(g); err == nil || !strings.Contains(err.Error(), "join_quorum.exceeds_incoming") {
		t.Fatalf("expected incoming quorum validation error, got %v", err)
	}
	g = graphmodel.WorkflowGraph{
		ID: "retry-on", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"},
		Nodes: []graphmodel.WorkflowNode{{ID: "node", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: "a", Revision: 1}, RetryPolicy: graphmodel.RetryPolicy{MaxAttempts: 2, RetryOn: []string{"network"}}}},
	}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "retry-on-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: g, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.StartAttempt(plan, "node", "retry-on-attempt", 1, graphmodel.Lease{FencingToken: 1}, testEnvelope(), graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.FinishAttempt(plan, a.ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "failure", Failure: &graphmodel.FailureReason{Code: "temporary", Message: "no retry", Retryable: true}, Result: graphmodel.StructuredResult{Outcome: "failure", EvidenceIDs: []string{"failure-evidence"}}}); err != nil {
		t.Fatal(err)
	}
	if p.Nodes["node"].Status != graphmodel.AttemptFailed || p.Nodes["node"].RetryAt != nil {
		t.Fatalf("retry-on ignored: %+v", p.Nodes["node"])
	}
}

func TestReplayProjectionAfterStructuralEvents(t *testing.T) {
	graph := graphmodel.WorkflowGraph{ID: "replay", Version: 1, EntryNodeIDs: []string{"gate"}, ExitNodeIDs: []string{"gate"}, Nodes: []graphmodel.WorkflowNode{{ID: "gate", Kind: graphmodel.NodeGate}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "replay-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	repo := NewMemoryRepository()
	if err := repo.CreatePlan(plan); err != nil {
		t.Fatal(err)
	}
	p, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	executor := Executor{Events: repo, Owner: "worker"}
	advanced, err := executor.AdvanceStructural(context.Background(), plan, &p, testEnvelope(), 1)
	if err != nil || len(advanced) != 1 {
		t.Fatalf("advance=%v err=%v", advanced, err)
	}
	if replayed, err := graphmodel.ReplayProjection(plan, repo.ListEvents(plan.ID, 0)); err != nil {
		t.Fatalf("replay failed: %v", err)
	} else if replayed.Status != graphmodel.PlanTerminal || replayed.Attempts[advanced[0].ID].Status != graphmodel.AttemptPassed {
		t.Fatalf("replayed=%+v", replayed)
	}
}

func TestCreatePlanWithEventCommitsReplayBoundaryAtomically(t *testing.T) {
	graph := graphmodel.WorkflowGraph{ID: "atomic-plan", Version: 1, EntryNodeIDs: []string{"gate"}, ExitNodeIDs: []string{"gate"}, Nodes: []graphmodel.WorkflowNode{{ID: "gate", Kind: graphmodel.NodeGate}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "atomic-plan", RequirementID: "req", WorkspaceID: "workspace", GraphSnapshot: graph, Status: graphmodel.PlanDraft, IdempotencyKey: "atomic-key"}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	repo := NewMemoryRepository()
	event, err := graphmodel.NewEvent(nil, plan.ID, plan.WorkspaceID, "plan.created", plan.IdempotencyKey, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreatePlanWithEvent(plan, event); err != nil {
		t.Fatal(err)
	}
	if got := repo.ListEvents(plan.ID, 0); len(got) != 1 || got[0].Type != "plan.created" || got[0].Sequence != 1 {
		t.Fatalf("plan lifecycle event was not committed with plan: %+v", got)
	}
	if _, err := graphmodel.ReplayProjection(plan, repo.ListEvents(plan.ID, 0)); err != nil {
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

func TestTerminalOutcomeRecordsApprovalDenial(t *testing.T) {
	graph := graphmodel.WorkflowGraph{ID: "denied-exit", Version: 1, EntryNodeIDs: []string{"gate"}, ExitNodeIDs: []string{"gate"}, Nodes: []graphmodel.WorkflowNode{{ID: "gate", Kind: graphmodel.NodeHuman}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "denied-exit-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := (Executor{}).AdvanceStructural(context.Background(), plan, &projection, testEnvelope(), 1)
	if err != nil || len(waiting) != 1 {
		t.Fatalf("advance=%v err=%v", waiting, err)
	}
	if _, err := projection.FinishAttempt(plan, waiting[0].ID, graphmodel.TransitionInput{PlanRevision: plan.Revision, LeaseToken: waiting[0].Lease.FencingToken, Event: "approval_denied", Result: graphmodel.StructuredResult{Outcome: "denied", EvidenceIDs: []string{"denial"}}}); err != nil {
		t.Fatal(err)
	}
	if projection.Status != graphmodel.PlanTerminal || projection.TerminalOutcome != "failed" {
		t.Fatalf("denied terminal outcome=%q status=%s", projection.TerminalOutcome, projection.Status)
	}
}

func TestWorkerDoesNotTreatRunningAttemptAsQuiescent(t *testing.T) {
	projection := graphmodel.PlanProjection{Attempts: map[string]graphmodel.NodeAttempt{
		"running": {ID: "running", Status: graphmodel.AttemptRunning},
	}}
	if !hasRunningAttempts(projection) {
		t.Fatal("running attempt was not detected")
	}
	projection.Attempts["running"] = graphmodel.NodeAttempt{ID: "running", Status: graphmodel.AttemptPassed}
	if hasRunningAttempts(projection) {
		t.Fatal("terminal attempt was reported as running")
	}
}

func TestSquadNodeIsProviderDispatchBoundary(t *testing.T) {
	r := NewMemoryRepository()
	agent := graphmodel.AgentDefinition{ID: "leader", WorkspaceID: "w", Revision: 1, Name: "leader", Status: graphmodel.AgentActive, ExecutorBinding: graphmodel.ExecutorBinding{ProviderID: "mock"}, InputSchema: graphmodel.SchemaRef{ID: "input"}, OutputSchema: graphmodel.SchemaRef{ID: "output"}}
	if err := r.SaveAgent(agent, 0); err != nil {
		t.Fatal(err)
	}
	squad := graphmodel.SquadDefinition{ID: "squad", WorkspaceID: "w", Revision: 1, Name: "squad", Status: graphmodel.SquadDraft, Members: []graphmodel.SquadMember{{ID: "leader-member", AgentID: agent.ID, Role: "leader", Leader: true}}, Graph: graphmodel.WorkflowGraph{ID: "squad-graph", Version: 1, EntryNodeIDs: []string{"member"}, ExitNodeIDs: []string{"member"}, Nodes: []graphmodel.WorkflowNode{{ID: "member", Kind: graphmodel.NodeAgent, AgentRef: &graphmodel.VersionedRef{ID: agent.ID, Revision: 1}}}}}
	if err := r.SaveSquad(squad, 0); err != nil {
		t.Fatal(err)
	}
	squad.Status = graphmodel.SquadPublished
	squad.Revision = 2
	squad.PublishedVersion = 1
	if err := r.SaveSquad(squad, 1); err != nil {
		t.Fatal(err)
	}
	graph := graphmodel.WorkflowGraph{ID: "outer", Version: 1, EntryNodeIDs: []string{"squad-node"}, ExitNodeIDs: []string{"squad-node"}, Nodes: []graphmodel.WorkflowNode{{ID: "squad-node", Kind: graphmodel.NodeSquad, SquadRef: &graphmodel.VersionedRef{ID: squad.ID, Revision: squad.Revision}}}}
	plan, err := (graphmodel.RequirementExecutionPlan{ID: "squad-plan", RequirementID: "r", WorkspaceID: "w", GraphSnapshot: graph, Status: graphmodel.PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	p, err := graphmodel.NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	provider := newTestProvider()
	started, err := (Executor{Provider: provider, Repository: r, Owner: "worker"}).DispatchReady(context.Background(), plan, &p, testEnvelope(), "work", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(started) != 1 || started[0].Status != graphmodel.AttemptRunning || provider.lastBinding != agent.ID {
		t.Fatalf("squad dispatch=%+v binding=%q", started, provider.lastBinding)
	}
}
