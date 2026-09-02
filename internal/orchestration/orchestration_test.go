package orchestration

import (
	"errors"
	"github.com/adro-project/adro/internal/harness"
	"path/filepath"
	"testing"
	"time"
)

func testEnvelope() harness.ContextEnvelope {
	return harness.ContextEnvelope{Manifest: harness.ContextManifest{SessionID: "s", Version: 1, TokenBudget: 1, Digest: "d"}, SelectionDigest: "sel", ReplayKey: "r"}
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
	if _, err = proj.FinishAttempt(plan, a.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 1, Event: "success", Result: StructuredResult{Outcome: "pass"}}); err != nil {
		t.Fatal(err)
	}
	if proj.Nodes["unit"].Status != AttemptReady {
		t.Fatalf("unit status %s", proj.Nodes["unit"].Status)
	}
	u, err := proj.StartAttempt(plan, "unit", "u1", 1, Lease{FencingToken: 2}, testEnvelope(), TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, PayloadHash: "u"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = proj.FinishAttempt(plan, u.ID, TransitionInput{PlanRevision: plan.Revision, LeaseToken: 2, Event: "bug", Result: StructuredResult{Outcome: "bug", Fields: map[string]any{"bug": true}}}); err != nil {
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
