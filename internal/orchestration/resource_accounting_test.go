package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/adro-project/adro/core/testkit"
	"github.com/adro-project/adro/internal/durable"
	"github.com/adro-project/adro/internal/provider"
)

func resourceScope(agent string) ResourceScope {
	return ResourceScope{TenantID: "tenant-a", WorkspaceID: "workspace-a", AgentID: agent, SessionID: "session-a", StepID: "step-a", ModelCallID: "model-call-a", CostCenter: "delivery"}
}

func newTestLedger(t *testing.T, path string, clock *testkit.ManualClock) *ResourceLedger {
	t.Helper()
	ledger, err := NewResourceLedger(path, ResourceLedgerOptions{Clock: clock, IDs: &testkit.SequenceIDs{}})
	if err != nil {
		t.Fatal(err)
	}
	return ledger
}

func TestResourceVectorRejectsNegativeAndOverflow(t *testing.T) {
	if err := (ResourceVector{Tokens: -1}).Validate(); err == nil {
		t.Fatal("negative resource was accepted")
	}
	if _, err := (ResourceVector{Tokens: math.MaxInt64}).Add(ResourceVector{Tokens: 1}); !errors.Is(err, ErrResourceOverflow) {
		t.Fatalf("overflow err=%v", err)
	}
	if excess := (ResourceVector{Tokens: 11, ConcurrencySlots: 1}).Excess(ResourceVector{Tokens: 10}); excess.Tokens != 1 || excess.ConcurrencySlots != 0 {
		t.Fatalf("unexpected excess=%+v", excess)
	}
	ledger := newTestLedger(t, "", testkit.NewManualClock(time.Date(2026, 9, 19, 7, 0, 0, 0, time.UTC)))
	if err := ledger.SetQuota(ResourceQuota{Scope: ResourceScope{TenantID: "tenant-a", WorkspaceID: "workspace-a", CostCenter: "hidden"}, HardLimit: ResourceVector{Tokens: 10}}); err == nil {
		t.Fatal("quota accepted usage-only attribution fields")
	}
}

func TestResourceLedgerEnforcesHierarchyAndParentReservation(t *testing.T) {
	now := time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)
	clock := testkit.NewManualClock(now)
	ledger := newTestLedger(t, "", clock)
	quotas := []ResourceQuota{
		{Scope: ResourceScope{TenantID: "tenant-a"}, HardLimit: ResourceVector{Tokens: 100, ConcurrencySlots: 2}},
		{Scope: ResourceScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}, HardLimit: ResourceVector{Tokens: 80, ConcurrencySlots: 2}},
		{Scope: ResourceScope{TenantID: "tenant-a", WorkspaceID: "workspace-a", AgentID: "child"}, HardLimit: ResourceVector{Tokens: 40, ConcurrencySlots: 1}},
	}
	for _, quota := range quotas {
		if err := ledger.SetQuota(quota); err != nil {
			t.Fatal(err)
		}
	}
	parent, created, _, err := ledger.Reserve(ResourceReservationSpec{
		ID: "parent", IdempotencyKey: "parent-key", Scope: resourceScope("parent"),
		Requested: ResourceVector{Tokens: 50, ConcurrencySlots: 1}, ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	})
	if err != nil || !created {
		t.Fatalf("parent=%+v created=%v err=%v", parent, created, err)
	}
	child, created, _, err := ledger.Reserve(ResourceReservationSpec{
		ID: "child", IdempotencyKey: "child-key", Scope: resourceScope("child"), ParentID: parent.ID,
		Requested: ResourceVector{Tokens: 30, ConcurrencySlots: 1}, ExpiresAt: now.Add(30 * time.Minute), CreatedAt: now,
	})
	if err != nil || !created {
		t.Fatalf("child=%+v created=%v err=%v", child, created, err)
	}
	if _, _, _, err := ledger.Reserve(ResourceReservationSpec{
		ID: "oversell", IdempotencyKey: "oversell-key", Scope: resourceScope("other"), ParentID: parent.ID,
		Requested: ResourceVector{Tokens: 21}, ExpiresAt: now.Add(30 * time.Minute), CreatedAt: now,
	}); !errors.Is(err, ErrResourceParentExhausted) {
		t.Fatalf("recursive oversell err=%v", err)
	}

	raw := json.RawMessage(`{"input_tokens":12,"output_tokens":8}`)
	usage, err := NewUsageRecord("usage-child", child.ID, resourceScope("child"), raw, ResourceVector{Tokens: 20, ToolCalls: 2, ConcurrencySlots: 1}, ResourceVector{Tokens: 18, ToolCalls: 1, ConcurrencySlots: 1}, false, false, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, created, _, err := ledger.RecordUsage(usage); err != nil || !created {
		t.Fatalf("record usage created=%v err=%v", created, err)
	}
	if _, err := ledger.Settle(child.ID, "settle-child", "completed", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	parent, err = ledger.GetReservation(parent.ID)
	if err != nil || parent.State.Consumed.Tokens != 20 {
		t.Fatalf("parent rollup=%+v err=%v", parent, err)
	}
	if _, err := ledger.Settle(parent.ID, "settle-parent", "completed", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	parent, _ = ledger.GetReservation(parent.ID)
	if parent.State.Released.Tokens != 30 || parent.State.Released.ConcurrencySlots != 1 || parent.State.Consumed.Tokens != 20 {
		t.Fatalf("settled parent state=%+v", parent.State)
	}
	dashboard, err := ledger.Dashboard(ResourceScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}, 10)
	if err != nil || dashboard.Consumed.Tokens != 20 || dashboard.ChildAgentUsage["child"].Tokens != 20 {
		t.Fatalf("dashboard=%+v err=%v", dashboard, err)
	}
}

func TestResourceLedgerIdempotencyDelayedBillingMissingUsageAndOverage(t *testing.T) {
	now := time.Date(2026, 9, 19, 9, 0, 0, 0, time.UTC)
	clock := testkit.NewManualClock(now)
	ledger := newTestLedger(t, "", clock)
	if err := ledger.SetQuota(ResourceQuota{Scope: ResourceScope{TenantID: "tenant-a"}, SoftLimit: ResourceVector{Tokens: 8}, HardLimit: ResourceVector{Tokens: 20}}); err != nil {
		t.Fatal(err)
	}
	spec := ResourceReservationSpec{ID: "reservation", IdempotencyKey: "reserve", Scope: resourceScope("agent"), Requested: ResourceVector{Tokens: 10, ConcurrencySlots: 1}, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	first, created, reports, err := ledger.Reserve(spec)
	if err != nil || !created || len(reports) != 1 || first.SoftActions[0] != "warning" {
		t.Fatalf("reserve=%+v created=%v reports=%+v err=%v", first, created, reports, err)
	}
	retry, created, _, err := ledger.Reserve(spec)
	if err != nil || created || retry.ID != first.ID {
		t.Fatalf("reserve retry=%+v created=%v err=%v", retry, created, err)
	}
	changed := spec
	changed.Requested.Tokens = 9
	if _, _, _, err := ledger.Reserve(changed); !errors.Is(err, ErrResourceConflict) {
		t.Fatalf("conflicting reserve err=%v", err)
	}
	missing, err := NewUsageRecord("missing", first.ID, resourceScope("agent"), nil, ResourceVector{}, ResourceVector{Tokens: 9, ConcurrencySlots: 1}, true, false, now.Add(time.Minute))
	if err != nil || missing.Normalized.Tokens != 9 || missing.Discrepancy.Tokens != 0 {
		t.Fatalf("missing usage=%+v err=%v", missing, err)
	}
	if _, created, _, err := ledger.RecordUsage(missing); err != nil || !created {
		t.Fatalf("missing usage created=%v err=%v", created, err)
	}
	if _, created, _, err := ledger.RecordUsage(missing); err != nil || created {
		t.Fatalf("duplicate usage created=%v err=%v", created, err)
	}
	conflict, err := NewUsageRecord("missing", first.ID, resourceScope("agent"), nil, ResourceVector{}, ResourceVector{Tokens: 8}, true, false, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ledger.RecordUsage(conflict); !errors.Is(err, ErrResourceConflict) {
		t.Fatalf("conflicting usage err=%v", err)
	}
	if _, err := ledger.Settle(first.ID, "settle", "provider_finished", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	late, err := NewUsageRecord("late-bill", first.ID, resourceScope("agent"), json.RawMessage(`{"billed_tokens":4}`), ResourceVector{Tokens: 4}, ResourceVector{Tokens: 3}, false, true, now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, created, reports, err := ledger.RecordUsage(late); err != nil || !created || reports[0].SoftExcess.Tokens == 0 {
		t.Fatalf("late usage created=%v reports=%+v err=%v", created, reports, err)
	}
	settled, _ := ledger.GetReservation(first.ID)
	if settled.State.Consumed.Tokens != 13 || settled.State.Overage.Tokens != 3 || settled.State.Released.ConcurrencySlots != 1 {
		t.Fatalf("late settled state=%+v", settled.State)
	}
	dashboard, err := ledger.Dashboard(ResourceScope{TenantID: "tenant-a"}, 10)
	if err != nil || dashboard.MissingProviderUsage != 1 || dashboard.DelayedBills != 1 || len(dashboard.OverageReservations) != 1 {
		t.Fatalf("dashboard=%+v err=%v", dashboard, err)
	}
	if _, err := NewUsageRecord("negative", first.ID, resourceScope("agent"), nil, ResourceVector{Tokens: -1}, ResourceVector{}, false, true, now); err == nil {
		t.Fatal("negative usage accepted")
	}
}

func TestResourceLedgerPersistsAndReapsOrphansAtomically(t *testing.T) {
	now := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	clock := testkit.NewManualClock(now)
	path := filepath.Join(t.TempDir(), "resources.json")
	ledger := newTestLedger(t, path, clock)
	spec := ResourceReservationSpec{ID: "orphan", IdempotencyKey: "orphan-key", Scope: resourceScope("agent"), Requested: ResourceVector{Tokens: 5, ConcurrencySlots: 1}, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	restore := durable.SetFaultInjector(func(point string) error {
		if point == "resource.persist.before_rename" {
			return errors.New("simulated crash")
		}
		return nil
	})
	_, _, _, persistErr := ledger.Reserve(spec)
	restore()
	if persistErr == nil {
		t.Fatal("faulted reservation persisted")
	}
	if _, err := ledger.GetReservation(spec.ID); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("faulted reservation remained: %v", err)
	}
	if _, _, _, err := ledger.Reserve(spec); err != nil {
		t.Fatal(err)
	}
	restarted := newTestLedger(t, path, clock)
	loaded, err := restarted.GetReservation(spec.ID)
	if err != nil || loaded.Status != ResourceReserved {
		t.Fatalf("restarted reservation=%+v err=%v", loaded, err)
	}
	clock.Advance(2 * time.Minute)
	expired, err := restarted.ReapOrphans(clock.Now())
	if err != nil || len(expired) != 1 || expired[0].Status != ResourceExpired || expired[0].State.Released.ConcurrencySlots != 1 {
		t.Fatalf("expired=%+v err=%v", expired, err)
	}
	retry, err := restarted.ReapOrphans(clock.Now())
	if err != nil || len(retry) != 0 {
		t.Fatalf("orphan retry=%+v err=%v", retry, err)
	}
}

func TestTerminalReservationRecoverySettlesAfterUsagePersistedBeforeCrash(t *testing.T) {
	now := time.Date(2026, 9, 19, 10, 30, 0, 0, time.UTC)
	clock := testkit.NewManualClock(now)
	path := filepath.Join(t.TempDir(), "resources.json")
	ledger := newTestLedger(t, path, clock)
	scope := resourceScope("recovery-agent")
	scope.SessionID = "session-recovery"
	scope.StepID = "attempt-recovery"
	scope.ModelCallID = "provider-run-recovery"
	scope.CostCenter = "incident-recovery"
	reservation, _, _, err := ledger.Reserve(ResourceReservationSpec{
		ID: "reservation-recovery", IdempotencyKey: "reserve-recovery", Scope: scope,
		Requested: ResourceVector{Tokens: 20, ConcurrencySlots: 1}, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	usage, err := NewUsageRecord(
		"usage:attempt-recovery", reservation.ID, scope, json.RawMessage(`{"input_tokens":7,"output_tokens":3}`),
		ResourceVector{Tokens: 10, ConcurrencySlots: 1}, ResourceVector{Tokens: 20, ConcurrencySlots: 1}, false, false, now.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, created, _, err := ledger.RecordUsage(usage); err != nil || !created {
		t.Fatalf("record usage created=%v err=%v", created, err)
	}

	// Simulate a process crash after durable usage was committed but before the
	// terminal settlement operation. Recovery must reuse the existing usage
	// event and close the reservation exactly once.
	restarted := newTestLedger(t, path, clock)
	attempt := NodeAttempt{
		ID: "attempt-recovery", RunID: "provider-run-recovery", SessionID: "session-recovery",
		ResourceReservationID: reservation.ID, Status: AttemptPassed,
	}
	plan := RequirementExecutionPlan{ID: "plan-recovery", RequirementID: "incident-recovery", WorkspaceID: "workspace-a"}
	if err := settleAttemptReservation(restarted, plan, attempt, usage.RawProvider, usage.Normalized, false, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	settled, err := restarted.GetReservation(reservation.ID)
	if err != nil || settled.Status != ResourceSettled || settled.State.Consumed.Tokens != 10 || settled.State.Released.Tokens != 10 || settled.State.Released.ConcurrencySlots != 1 {
		t.Fatalf("settled=%+v err=%v", settled, err)
	}
	replayed, err := restarted.GetUsage(usage.ID)
	if err != nil || replayed.PayloadDigest != usage.PayloadDigest {
		t.Fatalf("usage replay=%+v err=%v", replayed, err)
	}
	if err := settleAttemptReservation(restarted, plan, attempt, usage.RawProvider, usage.Normalized, false, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("idempotent terminal recovery: %v", err)
	}
	items, err := restarted.ListReservations(ResourceScope{TenantID: scope.TenantID, WorkspaceID: scope.WorkspaceID}, true)
	if err != nil || len(items) != 1 || items[0].Status != ResourceSettled {
		t.Fatalf("reservations=%+v err=%v", items, err)
	}
}

func TestResourceLedgerRejectsUsageOverflowWithoutPartialMutation(t *testing.T) {
	now := time.Date(2026, 9, 19, 11, 0, 0, 0, time.UTC)
	ledger := newTestLedger(t, "", testkit.NewManualClock(now))
	reservation, _, _, err := ledger.Reserve(ResourceReservationSpec{ID: "max", IdempotencyKey: "max-key", Scope: resourceScope("agent"), Requested: ResourceVector{Tokens: math.MaxInt64}, CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewUsageRecord("max-usage", reservation.ID, resourceScope("agent"), nil, ResourceVector{Tokens: math.MaxInt64}, ResourceVector{}, false, false, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ledger.RecordUsage(first); err != nil {
		t.Fatal(err)
	}
	overflow, err := NewUsageRecord("overflow", reservation.ID, resourceScope("agent"), nil, ResourceVector{Tokens: 1}, ResourceVector{}, false, false, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ledger.RecordUsage(overflow); !errors.Is(err, ErrResourceOverflow) {
		t.Fatalf("overflow err=%v", err)
	}
	loaded, _ := ledger.GetReservation(reservation.ID)
	if loaded.State.Consumed.Tokens != math.MaxInt64 {
		t.Fatalf("overflow partially mutated reservation=%+v", loaded.State)
	}
}

func TestFairAdmissionQueueIsDeterministicWeightedAndAgesPriority(t *testing.T) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	policy := FairQueuePolicy{
		AgingInterval: time.Minute, MaxPriority: 100, MaxPending: 20, ShedThreshold: 10,
		TenantWeights:     map[string]int64{"tenant-a": 1, "tenant-b": 2},
		EmergencyCeilings: map[string]int{"tenant-a": 80},
	}
	queue, err := NewFairAdmissionQueue(policy)
	if err != nil {
		t.Fatal(err)
	}
	request := func(id, tenant string, priority int, submitted time.Time) AdmissionRequest {
		return AdmissionRequest{ID: id, PlanID: "plan-" + tenant, NodeID: "node", Scope: ResourceScope{TenantID: tenant, WorkspaceID: "workspace", AgentID: "agent"}, Resources: ResourceVector{Tokens: 1, ConcurrencySlots: 1}, Priority: priority, Persisted: true, SchedulingCost: 1, SubmittedAt: submitted}
	}
	for _, item := range []AdmissionRequest{
		request("a-1", "tenant-a", 50, base), request("a-2", "tenant-a", 50, base),
		request("b-1", "tenant-b", 50, base), request("b-2", "tenant-b", 50, base),
	} {
		if _, err := queue.Submit(item); err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := queue.Claim(base, 4)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{claimed[0].RequestID, claimed[1].RequestID, claimed[2].RequestID, claimed[3].RequestID}
	want := []string{"b-1", "a-1", "b-2", "a-2"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("weighted order=%v want=%v", got, want)
		}
	}

	aging, _ := NewFairAdmissionQueue(policy)
	low := request("low", "tenant-a", 1, base)
	high := request("high", "tenant-b", 90, base.Add(98*time.Minute))
	if decision, err := aging.Submit(low); err != nil || decision.StarvationDeadline.Before(base.Add(99*time.Minute)) {
		t.Fatalf("low decision=%+v err=%v", decision, err)
	}
	if _, err := aging.Submit(high); err != nil {
		t.Fatal(err)
	}
	claimed, err = aging.Claim(base.Add(99*time.Minute), 1)
	if err != nil || len(claimed) != 1 || claimed[0].RequestID != "low" || claimed[0].EffectivePriority != 100 {
		t.Fatalf("priority aging claim=%+v err=%v", claimed, err)
	}

	emergency := request("emergency", "tenant-a", 99, base)
	emergency.Emergency = true
	decision, err := aging.Submit(emergency)
	if err != nil || !decision.PriorityWasCapped || decision.ConfiguredPriority != 80 {
		t.Fatalf("emergency ceiling decision=%+v err=%v", decision, err)
	}
}

func TestFairAdmissionQueueBackpressureAndLoadSheddingProtectRecovery(t *testing.T) {
	base := time.Date(2026, 9, 19, 13, 0, 0, 0, time.UTC)
	queue, err := NewFairAdmissionQueue(FairQueuePolicy{AgingInterval: time.Minute, MaxPriority: 10, MaxPending: 4, ShedThreshold: 2})
	if err != nil {
		t.Fatal(err)
	}
	makeRequest := func(id string, priority int, persisted, recovery bool) AdmissionRequest {
		return AdmissionRequest{ID: id, PlanID: "plan", NodeID: id, Scope: ResourceScope{TenantID: "tenant", WorkspaceID: "workspace", AgentID: "agent"}, Resources: ResourceVector{ConcurrencySlots: 1}, Priority: priority, Persisted: persisted, Recovery: recovery, SubmittedAt: base}
	}
	for _, request := range []AdmissionRequest{
		makeRequest("low-ephemeral", 1, false, false), makeRequest("high-ephemeral", 9, false, false),
		makeRequest("persisted", 0, true, false), makeRequest("recovery", 0, true, true),
	} {
		if _, err := queue.Submit(request); err != nil {
			t.Fatal(err)
		}
	}
	rejected, err := queue.Submit(makeRequest("burst-overflow", 10, false, false))
	if err != nil || rejected.State != AdmissionRejected || rejected.Reason != "queue_capacity_exhausted" {
		t.Fatalf("backpressure decision=%+v err=%v", rejected, err)
	}
	shed := queue.ShedOverload(base)
	if len(shed) != 2 || shed[0].RequestID != "low-ephemeral" || shed[1].RequestID != "high-ephemeral" {
		t.Fatalf("shed=%+v", shed)
	}
	claimed, err := queue.Claim(base, 10)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("protected claim=%+v err=%v", claimed, err)
	}
	for _, decision := range claimed {
		if decision.RequestID != "persisted" && decision.RequestID != "recovery" {
			t.Fatalf("unexpected protected claim=%+v", decision)
		}
	}
}

func TestAdmissionControllerDistinguishesWaitingFromPermanentRejection(t *testing.T) {
	now := time.Date(2026, 9, 19, 14, 0, 0, 0, time.UTC)
	ledger := newTestLedger(t, "", testkit.NewManualClock(now))
	if err := ledger.SetQuota(ResourceQuota{Scope: ResourceScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}, HardLimit: ResourceVector{Tokens: 10, ConcurrencySlots: 1}}); err != nil {
		t.Fatal(err)
	}
	queue, _ := NewFairAdmissionQueue(FairQueuePolicy{AgingInterval: time.Minute, MaxPriority: 100, MaxPending: 10})
	controller := AdmissionController{Ledger: ledger, Queue: queue}
	request := AdmissionRequest{ID: "first", PlanID: "plan", NodeID: "node-1", Scope: resourceScope("agent"), Resources: ResourceVector{Tokens: 5, ConcurrencySlots: 1}, Priority: 10, Persisted: true, SubmittedAt: now, Deadline: now.Add(time.Hour)}
	first, err := controller.TryAdmit(request)
	if err != nil || first.State != AdmissionAdmitted || first.ReservationID == "" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	secondRequest := request
	secondRequest.ID, secondRequest.NodeID = "second", "node-2"
	second, err := controller.TryAdmit(secondRequest)
	if err != nil || second.State != AdmissionWaiting || second.StarvationDeadline.IsZero() || queue.Pending() != 1 {
		t.Fatalf("second=%+v pending=%d err=%v", second, queue.Pending(), err)
	}
	if _, err := ledger.Release(first.ReservationID, "release-first", "benchmark capacity released", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	second, err = controller.TryAdmit(secondRequest)
	if err != nil || second.State != AdmissionAdmitted || queue.Pending() != 0 {
		t.Fatalf("readmitted second=%+v pending=%d err=%v", second, queue.Pending(), err)
	}
	impossibleRequest := request
	impossibleRequest.ID, impossibleRequest.NodeID = "impossible", "node-3"
	impossibleRequest.Resources = ResourceVector{Tokens: 11}
	impossible, err := controller.TryAdmit(impossibleRequest)
	if err != nil || impossible.State != AdmissionRejected || impossible.Reason != "request_exceeds_hard_limit" {
		t.Fatalf("impossible=%+v err=%v", impossible, err)
	}
}

type resourceTerminalProvider struct {
	*testProvider
	now time.Time
}

func (p *resourceTerminalProvider) GetRun(_ context.Context, runID string) (provider.RunSnapshot, error) {
	finished := p.now.Add(time.Second)
	return provider.RunSnapshot{
		ID: runID, Status: "completed", LastEventID: "provider-event", ExecutorPath: "mock-runtime",
		Output:     `ADRO_RESULT_JSON={"outcome":"pass","reason_code":"ok","summary":"complete","evidence_ids":["provider-evidence"],"fields":{}}`,
		FinishedAt: &finished, Usage: provider.Usage{InputTokens: 7, OutputTokens: 3, DurationMS: 250},
		ToolEvents: []provider.ToolEvent{{CallID: "tool-1", Name: "native_tool", Phase: "before", Sequence: 1}, {CallID: "tool-1", Name: "native_tool", Phase: "after", Sequence: 2}},
	}, nil
}

func TestSchedulerAdmissionReservesBeforeDispatchAndWorkerSettlesUsage(t *testing.T) {
	now := time.Date(2026, 9, 19, 15, 0, 0, 0, time.UTC)
	ledger := newTestLedger(t, "", testkit.NewManualClock(now))
	if err := ledger.SetQuota(ResourceQuota{Scope: ResourceScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}, HardLimit: ResourceVector{Tokens: 30, ToolCalls: 4, ConcurrencySlots: 1}}); err != nil {
		t.Fatal(err)
	}
	queue, _ := NewFairAdmissionQueue(FairQueuePolicy{AgingInterval: time.Minute, MaxPriority: 100, MaxPending: 10})
	providerRuntime := &resourceTerminalProvider{testProvider: newTestProvider(), now: now}
	graph := WorkflowGraph{
		ID: "admission-graph", Version: 1, EntryNodeIDs: []string{"a", "b"}, ExitNodeIDs: []string{"a", "b"},
		Nodes: []WorkflowNode{
			{ID: "a", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "agent-a", Revision: 1}, Budget: Budget{Tokens: 15, ToolCalls: 2}},
			{ID: "b", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "agent-b", Revision: 1}, Budget: Budget{Tokens: 15, ToolCalls: 2}},
		},
	}
	plan, err := (RequirementExecutionPlan{ID: "admission-plan", RequirementID: "requirement", WorkspaceID: "workspace-a", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(plan)
	if err != nil {
		t.Fatal(err)
	}
	controller := &AdmissionController{Ledger: ledger, Queue: queue}
	scheduler := Scheduler{
		Executor: Executor{Provider: providerRuntime, Owner: "worker"}, Admission: controller,
		Config: SchedulerConfig{MaxConcurrent: 2, TenantID: "tenant-a", Now: func() time.Time { return now }},
	}
	report, err := scheduler.Tick(context.Background(), plan, &projection, testEnvelope(), "cost-center", "")
	if err != nil || len(report.Started) != 1 || report.Started[0].NodeID != "a" {
		t.Fatalf("tick report=%+v err=%v", report, err)
	}
	if report.Admissions["a"].State != AdmissionAdmitted || report.Admissions["b"].State != AdmissionWaiting {
		t.Fatalf("admission decisions=%+v", report.Admissions)
	}
	attempt := report.Started[0]
	if attempt.ResourceReservationID == "" || projection.Nodes["a"].ResourceReservationID != attempt.ResourceReservationID {
		t.Fatalf("attempt/projection reservation missing attempt=%+v node=%+v", attempt, projection.Nodes["a"])
	}
	reserved, err := ledger.GetReservation(attempt.ResourceReservationID)
	if err != nil || reserved.Status != ResourceReserved || reserved.State.Reserved.ConcurrencySlots != 1 {
		t.Fatalf("reserved=%+v err=%v", reserved, err)
	}
	worker := Worker{Scheduler: scheduler, MaxTicks: 1}
	finished, err := worker.Reconcile(context.Background(), plan, &projection)
	if err != nil || len(finished) != 1 || finished[0].Status != AttemptPassed {
		t.Fatalf("reconcile finished=%+v err=%v", finished, err)
	}
	settled, err := ledger.GetReservation(attempt.ResourceReservationID)
	if err != nil || settled.Status != ResourceSettled || settled.State.Consumed.Tokens != 10 || settled.State.Consumed.ToolCalls != 2 || settled.State.Consumed.OutputBytes == 0 || settled.State.Released.ConcurrencySlots != 1 {
		t.Fatalf("settled=%+v err=%v", settled, err)
	}
	dashboard, err := ledger.Dashboard(ResourceScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}, 10)
	if err != nil || len(dashboard.RecentUsage) != 1 || len(dashboard.RecentUsage[0].RawProvider) == 0 || dashboard.RecentUsage[0].Discrepancy.Tokens != -5 {
		t.Fatalf("dashboard=%+v err=%v", dashboard, err)
	}
}

func TestSchedulerReleasesAdmissionWhenDispatchFails(t *testing.T) {
	now := time.Date(2026, 9, 19, 16, 0, 0, 0, time.UTC)
	ledger := newTestLedger(t, "", testkit.NewManualClock(now))
	controller := &AdmissionController{Ledger: ledger}
	graph := WorkflowGraph{ID: "dispatch-failure", Version: 1, EntryNodeIDs: []string{"node"}, ExitNodeIDs: []string{"node"}, Nodes: []WorkflowNode{{ID: "node", Kind: NodeAgent, AgentRef: &VersionedRef{ID: "agent", Revision: 1}, Budget: Budget{Tokens: 5}}}}
	plan, err := (RequirementExecutionPlan{ID: "dispatch-failure", RequirementID: "requirement", WorkspaceID: "workspace-a", GraphSnapshot: graph, Status: PlanDraft}).Freeze()
	if err != nil {
		t.Fatal(err)
	}
	projection, _ := NewProjection(plan)
	scheduler := Scheduler{Admission: controller, Config: SchedulerConfig{TenantID: "tenant-a", Now: func() time.Time { return now }}}
	report, err := scheduler.Tick(context.Background(), plan, &projection, testEnvelope(), "cost-center", "")
	if err == nil || report.Admissions["node"].ReservationID == "" {
		t.Fatalf("dispatch failure report=%+v err=%v", report, err)
	}
	released, getErr := ledger.GetReservation(report.Admissions["node"].ReservationID)
	if getErr != nil || released.Status != ResourceReleased || released.TerminalReason != "dispatch_not_started" {
		t.Fatalf("released=%+v err=%v", released, getErr)
	}
}
