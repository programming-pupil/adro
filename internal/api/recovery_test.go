package api

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/adro-project/adro/internal/artifact"
	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/harness"
	"github.com/adro-project/adro/internal/provider"
	"github.com/adro-project/adro/internal/store"
	"log/slog"
)

func newRecoveryFixture(t *testing.T) (*Server, domain.PipelineRun) {
	t.Helper()
	t.Setenv("ADRO_AUTH_MODE", "optional")
	control := store.NewMemory()
	requirement, err := control.CreateRequirement(domain.Requirement{WorkspaceID: "workspace", Title: "recover", Description: "recover provider intent", AcceptanceCriteria: []string{"intent is durable"}, AssigneeMemberIDs: []string{"member"}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := control.CreatePipeline(domain.PipelineRun{ID: "pipeline-recovery", WorkspaceID: "workspace", RequirementID: requirement.ID, SessionID: "session-recovery", Roles: domain.PipelineAgentRoles{Designer: "designer", Developer: "developer", Tester: "tester", Arbitrator: "arbitrator"}, Context: domain.PipelineContext{RequirementText: "recover"}, MaxRetries: 2, CoverageThreshold: 80})
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	artifacts, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := New(control, provider.NewLocalProvider("/bin/sh", []string{"-c", "sleep 1"}, t.TempDir(), bus), artifacts, bus, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := server.ensureHarnessSession(run); err != nil {
		t.Fatal(err)
	}
	return server, run
}

func TestRecoveryWorkerReplaysDurableProviderIntent(t *testing.T) {
	server, run := newRecoveryFixture(t)
	turn, err := server.Harness.AppendTurn(run.SessionID, harness.Turn{Role: harness.RoleUser, Content: "recover provider", IdempotencyKey: "recover:turn"})
	if err != nil {
		t.Fatal(err)
	}
	key := "recover:dispatch"
	_, err = server.enqueueProviderDispatch(run, key, providerDispatchIntent{
		PipelineID: run.ID, ExpectedVersion: run.Version, Stage: run.PipelineStage, AgentID: run.Roles.Designer,
		TurnHash: turn.Hash, ProviderIssueID: "issue-recovery", Command: provider.StartRunCommand{WorkItemID: "recovery-item", ProviderIssueID: "issue-recovery", AgentBindingID: run.Roles.Designer, Input: "recover provider", IdempotencyKey: key},
	})
	if err != nil {
		t.Fatal(err)
	}
	server.recoverOnce(context.Background(), harness.Dispatcher{Store: server.Harness, Publisher: harnessPublisher{server: server}, Owner: "test-worker", LeaseTTL: time.Minute})
	updated, err := server.Store.GetPipeline(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != domain.PipelineWaiting || updated.ActiveProviderTaskID == "" || updated.Version != run.Version+1 {
		t.Fatalf("recovery did not bind provider: %+v", updated)
	}
	recovered, err := server.Harness.Recover(run.SessionID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.PendingEffects) != 0 {
		t.Fatalf("intent was not acknowledged: %+v", recovered.PendingEffects)
	}
	if _, err := server.Harness.GetSession(run.SessionID); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryWorkerAcknowledgesLostOutboxAckWithoutDuplicateRun(t *testing.T) {
	server, run := newRecoveryFixture(t)
	turn, err := server.Harness.AppendTurn(run.SessionID, harness.Turn{Role: harness.RoleUser, Content: "replay provider", IdempotencyKey: "replay:turn"})
	if err != nil {
		t.Fatal(err)
	}
	key := "replay:dispatch"
	event, err := server.enqueueProviderDispatch(run, key, providerDispatchIntent{
		PipelineID: run.ID, ExpectedVersion: run.Version, Stage: run.PipelineStage, AgentID: run.Roles.Designer,
		TurnHash: turn.Hash, ProviderIssueID: "issue-replay", Command: provider.StartRunCommand{WorkItemID: "replay-item", ProviderIssueID: "issue-replay", AgentBindingID: run.Roles.Designer, Input: "replay provider", IdempotencyKey: key},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.processProviderDispatchIntent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	first, err := server.Store.GetPipeline(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.ActiveProviderTaskID == "" {
		t.Fatal("initial provider binding is empty")
	}
	// The simulated crash happened after the state update but before outbox
	// acknowledgement. The worker must observe the committed binding and only
	// acknowledge the stale intent.
	server.recoverOnce(context.Background(), harness.Dispatcher{Store: server.Harness, Publisher: harnessPublisher{server: server}, Owner: "test-worker", LeaseTTL: time.Minute})
	second, err := server.Store.GetPipeline(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.ActiveProviderTaskID != first.ActiveProviderTaskID || second.Version != first.Version {
		t.Fatalf("replay created a duplicate provider run: first=%+v second=%+v", first, second)
	}
	recovered, err := server.Harness.Recover(run.SessionID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.PendingEffects) != 0 {
		t.Fatalf("stale intent remains pending: %+v", recovered.PendingEffects)
	}
}

func TestRecoveryWorkerReplaysWorkItemIntentAndProvenance(t *testing.T) {
	server, run := newRecoveryFixture(t)
	item, _, err := server.Store.CreateWorkItemIfAbsent(domain.WorkItem{ID: "work-recovery", RequirementID: run.RequirementID, RepositoryID: "repo", MemberID: "member", DeveloperAgentBindingID: "designer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Harness.EnsureSession(harness.Session{ID: "session-work-recovery", TenantID: "workspace", WorkspaceID: "workspace"}); err != nil {
		t.Fatal(err)
	}
	turn, err := server.Harness.AppendTurn("session-work-recovery", harness.Turn{Role: harness.RoleUser, Content: "run work item", IdempotencyKey: "work:turn"})
	if err != nil {
		t.Fatal(err)
	}
	key := "work:dispatch"
	if _, err := server.enqueueExecutionDispatch("session-work-recovery", key, providerDispatchIntent{
		WorkItemID: item.ID, RequirementID: run.RequirementID, AgentID: "designer", RepositoryID: item.RepositoryID, TurnHash: turn.Hash,
		Command: provider.StartRunCommand{WorkItemID: item.ID, AgentBindingID: "designer", Input: "run work item", SessionID: "session-work-recovery", IdempotencyKey: key},
	}); err != nil {
		t.Fatal(err)
	}
	server.recoverOnce(context.Background(), harness.Dispatcher{Store: server.Harness, Publisher: harnessPublisher{server: server}, Owner: "test-worker", LeaseTTL: time.Minute})
	provenance, ok := server.Store.FindProvenance(item.ID)
	if !ok || provenance.ProviderTaskID == "" || provenance.ProviderSessionID == "" {
		t.Fatalf("work item provenance was not recovered: %+v ok=%v", provenance, ok)
	}
	recovered, err := server.Harness.Recover("session-work-recovery", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.PendingEffects) != 0 {
		t.Fatalf("work item intent remains pending: %+v", recovered.PendingEffects)
	}
}

func TestWorkItemIntentReplayAfterProvenanceCommitDoesNotStartAnotherRun(t *testing.T) {
	server, run := newRecoveryFixture(t)
	item, _, err := server.Store.CreateWorkItemIfAbsent(domain.WorkItem{ID: "work-replay", RequirementID: run.RequirementID, RepositoryID: "repo", MemberID: "member", DeveloperAgentBindingID: "designer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Harness.EnsureSession(harness.Session{ID: "session-work-replay", TenantID: "workspace", WorkspaceID: "workspace"}); err != nil {
		t.Fatal(err)
	}
	turn, err := server.Harness.AppendTurn("session-work-replay", harness.Turn{Role: harness.RoleUser, Content: "run twice safely"})
	if err != nil {
		t.Fatal(err)
	}
	key := "work:replay"
	event, err := server.enqueueExecutionDispatch("session-work-replay", key, providerDispatchIntent{
		WorkItemID: item.ID, RequirementID: run.RequirementID, AgentID: "designer", RepositoryID: item.RepositoryID, TurnHash: turn.Hash,
		Command: provider.StartRunCommand{WorkItemID: item.ID, AgentBindingID: "designer", Input: "run twice safely", SessionID: "session-work-replay", IdempotencyKey: key},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.processProviderDispatchIntent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	first, ok := server.Store.FindProvenance(item.ID)
	if !ok || first.ProviderTaskID == "" {
		t.Fatalf("first provenance missing: %+v ok=%v", first, ok)
	}
	if err := server.processProviderDispatchIntent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	second, ok := server.Store.FindProvenance(item.ID)
	if !ok || second.ProviderTaskID != first.ProviderTaskID || second.ProviderIdempotencyKey != key {
		t.Fatalf("replay replaced provenance: first=%+v second=%+v ok=%v", first, second, ok)
	}
}

func TestBugRepairIntentRecoveryPersistsAttemptAndProvenance(t *testing.T) {
	server, run := newRecoveryFixture(t)
	bug, _, err := server.Store.UpsertBug(domain.Bug{ID: "bug-recovery", WorkspaceID: run.WorkspaceID, RequirementID: run.RequirementID, WorkItemID: "work-bug-recovery", RepositoryID: "repo", Fingerprint: "fp-recovery", Title: "repair me", LogExcerpt: "integration failed", Status: domain.BugOpen})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Harness.EnsureSession(harness.Session{ID: "session-bug-recovery", TenantID: run.WorkspaceID, WorkspaceID: run.WorkspaceID}); err != nil {
		t.Fatal(err)
	}
	turn, err := server.Harness.AppendTurn("session-bug-recovery", harness.Turn{Role: harness.RoleUser, Content: "repair me", IdempotencyKey: "bug:repair:turn"})
	if err != nil {
		t.Fatal(err)
	}
	key := "bug:" + bug.ID + ":attempt:1"
	event, err := server.Harness.EnqueueOutbox("session-bug-recovery", key, providerDispatchIntent{Type: providerDispatchIntentType, Kind: "bug", BugID: bug.ID, WorkItemID: bug.WorkItemID, RequirementID: bug.RequirementID, AgentID: "developer", HarnessSessionID: "session-bug-recovery", ContextID: "context-" + bug.WorkItemID, ContextVersion: 1, RepairAttempt: 1, TurnHash: turn.Hash, Command: provider.StartRunCommand{WorkItemID: bug.WorkItemID, Input: "repair me", SessionID: "provider-session", IdempotencyKey: key}})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.processBugDispatchIntent(context.Background(), event, mustBugIntent(t, event)); err != nil {
		t.Fatal(err)
	}
	if attempts := server.Store.ListRepairAttempts(bug.ID); len(attempts) != 1 || attempts[0].ProviderTaskID == "" {
		t.Fatalf("repair attempts=%+v", attempts)
	}
	if provenance, ok := server.Store.FindProvenance(bug.WorkItemID); !ok || provenance.ProviderIdempotencyKey != key {
		t.Fatalf("provenance=%+v ok=%v", provenance, ok)
	}
}

func mustBugIntent(t *testing.T, event harness.OutboxEvent) providerDispatchIntent {
	t.Helper()
	var intent providerDispatchIntent
	if err := json.Unmarshal(event.Payload, &intent); err != nil {
		t.Fatal(err)
	}
	return intent
}
