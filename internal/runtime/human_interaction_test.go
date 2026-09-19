package runtime

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/adro-project/adro/core/identity"
	"github.com/adro-project/adro/core/testkit"
	"github.com/adro-project/adro/internal/security"
)

func humanActor(scope Scope, id string, now time.Time) identity.Actor {
	return identity.Actor{
		Type: identity.ActorHuman, ID: id, TenantID: scope.TenantID, WorkspaceID: scope.WorkspaceID,
		AuthnMethod: "human_session", CredentialID: "credential-" + id, Audience: "adro-runtime",
		IssuedAt: now.Add(-time.Hour).UTC().Truncate(time.Microsecond), ExpiresAt: now.Add(2 * time.Hour).UTC().Truncate(time.Microsecond),
	}
}

func interactionRequest(now time.Time, turnID, requestID string, actors ...identity.ActorRef) HumanInteractionRequest {
	return HumanInteractionRequest{
		RequestID: requestID, RequestVersion: 1, TurnID: turnID, Kind: HumanKindChoice,
		Prompt: "Choose the recovery path", ResponseSchema: `{"enum":["resume","stop"],"type":"string"}`,
		Deadline: now.Add(time.Hour), EligibleActors: actors, ClaimPolicy: HumanClaimNone,
		ContextDigest: "context-digest", Sensitivity: security.SensitivityInternal,
		RequestedAt: now, IdempotencyKey: requestID + "-create",
	}
}

func approvalRequest(now time.Time, turnID, requestID string, actors ...identity.ActorRef) ApprovalRequest {
	return ApprovalRequest{
		RequestID: requestID, RequestVersion: 3, TurnID: turnID, Prompt: "Authorize deployment",
		Capability: "deployment.write", Risk: "external_write", PolicyBundleDigest: "policy-digest",
		Deadline: now.Add(time.Hour), EligibleActors: actors, ClaimPolicy: HumanClaimRequired, ClaimTTL: 15 * time.Minute,
		ContextDigest: "approval-context", Sensitivity: security.SensitivityRestricted,
		RequestedAt: now, IdempotencyKey: requestID + "-create",
	}
}

// Threat ID: TM-HUMAN-003
func TestHumanInteractionResponseAppliesOnlyAtSafeBoundaryAndReplays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	fixture := newLifecycleFixture(t, path)
	const turnID, stepID, requestID = "turn-human", "step-after-human", "human-request-1"
	startSessionTurn(t, fixture, turnID)
	createStep(t, fixture, turnID, stepID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	actor := humanActor(fixture.scope, "reviewer-1", now)
	request := interactionRequest(now, turnID, requestID, identity.ActorRef{Type: actor.Type, ID: actor.ID})
	asked, err := fixture.journal.RequestHumanInteraction(fixture.scope, request, lifecycleOwner, fixture.fence)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := fixture.journal.RequestHumanInteraction(fixture.scope, request, lifecycleOwner, fixture.fence)
	if err != nil || retry.EventID != asked.EventID {
		t.Fatalf("request retry=%+v err=%v", retry, err)
	}
	changed := request
	changed.Prompt = "Choose a different path"
	if _, err := fixture.journal.RequestHumanInteraction(fixture.scope, changed, lifecycleOwner, fixture.fence); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed request returned %v", err)
	}
	if _, err := fixture.journal.RespondHumanInteraction(fixture.scope, requestID, actor, 1, "answer-invalid", json.RawMessage(`"unknown"`), now.Add(time.Minute), lifecycleOwner, fixture.fence); !errors.Is(err, ErrHumanResponseInvalid) {
		t.Fatalf("invalid schema response returned %v", err)
	}
	response, err := fixture.journal.RespondHumanInteraction(fixture.scope, requestID, actor, 1, "answer-1", json.RawMessage(`"resume"`), now.Add(time.Minute), lifecycleOwner, fixture.fence)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := fixture.journal.RespondHumanInteraction(fixture.scope, requestID, actor, 1, "answer-1", json.RawMessage(`"resume"`), now.Add(2*time.Minute), lifecycleOwner, fixture.fence)
	if err != nil || duplicate.EventID != response.EventID {
		t.Fatalf("response retry=%+v err=%v", duplicate, err)
	}
	if _, err := fixture.journal.RespondHumanInteraction(fixture.scope, requestID, actor, 1, "answer-2", json.RawMessage(`"stop"`), now.Add(2*time.Minute), lifecycleOwner, fixture.fence); !errors.Is(err, ErrHumanResponseConflict) {
		t.Fatalf("changed response returned %v", err)
	}
	if _, err := fixture.journal.ApplyHumanResponseAtBoundary(fixture.scope, requestID, stepID, now.Add(3*time.Minute), lifecycleOwner, fixture.fence); !errors.Is(err, ErrUnsafeStepBoundary) {
		t.Fatalf("response applied while turn running: %v", err)
	}
	if _, err := fixture.journal.WaitTurnInput(fixture.scope, turnID, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	applied, err := fixture.journal.ApplyHumanResponseAtBoundary(fixture.scope, requestID, stepID, now.Add(3*time.Minute), lifecycleOwner, fixture.fence)
	if err != nil {
		t.Fatal(err)
	}
	reapplied, err := fixture.journal.ApplyHumanResponseAtBoundary(fixture.scope, requestID, stepID, now.Add(4*time.Minute), lifecycleOwner, fixture.fence)
	if err != nil || reapplied.EventID != applied.EventID {
		t.Fatalf("apply retry=%+v err=%v", reapplied, err)
	}
	state, err := fixture.journal.HumanRequestState(fixture.scope, requestID)
	if err != nil || state.Status != HumanRequestApplied || state.AppliedStepID != stepID || state.Response == nil || string(state.Response.Value) != `"resume"` {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	restarted, err := NewJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := restarted.HumanRequestState(fixture.scope, requestID)
	if err != nil || replayed.Status != HumanRequestApplied || replayed.DefinitionDigest != state.DefinitionDigest || replayed.Response.ResponseDigest != state.Response.ResponseDigest {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
}

// Threat ID: TM-HUMAN-001
func TestHumanApprovalConcurrentClaimTakeoverAndDuplicateDecision(t *testing.T) {
	fixture := newLifecycleFixture(t, "")
	const turnID, stepID, requestID = "turn-approval", "step-after-approval", "approval-1"
	startSessionTurn(t, fixture, turnID)
	createStep(t, fixture, turnID, stepID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	first := humanActor(fixture.scope, "reviewer-a", now)
	second := humanActor(fixture.scope, "reviewer-b", now)
	request := approvalRequest(now, turnID, requestID,
		identity.ActorRef{Type: first.Type, ID: first.ID}, identity.ActorRef{Type: second.Type, ID: second.ID})
	if _, err := fixture.journal.AskApproval(fixture.scope, request, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.WaitTurnApproval(fixture.scope, turnID, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}

	type claimResult struct {
		actor identity.Actor
		err   error
	}
	results := make(chan claimResult, 2)
	var wg sync.WaitGroup
	for index, actor := range []identity.Actor{first, second} {
		wg.Add(1)
		go func(index int, actor identity.Actor) {
			defer wg.Done()
			_, err := fixture.journal.ClaimHumanRequest(fixture.scope, requestID, actor, 3, "claim-"+string(rune('a'+index)), now.Add(time.Minute), lifecycleOwner, fixture.fence)
			results <- claimResult{actor: actor, err: err}
		}(index, actor)
	}
	wg.Wait()
	close(results)
	var winner, loser identity.Actor
	successes, held := 0, 0
	for result := range results {
		if result.err == nil {
			successes++
			winner = result.actor
		} else if errors.Is(result.err, ErrHumanClaimHeld) {
			held++
			loser = result.actor
		} else {
			t.Fatalf("claim error=%v", result.err)
		}
	}
	if successes != 1 || held != 1 {
		t.Fatalf("claim outcomes success=%d held=%d", successes, held)
	}
	if _, err := fixture.journal.TakeOverHumanRequest(fixture.scope, requestID, loser, 3, "takeover-1", "primary reviewer unavailable", "approval-supervisor", now.Add(2*time.Minute), lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.DecideApproval(fixture.scope, requestID, winner, 3, "decision-old", ApprovalDecision{Decision: "approved", Reason: "old claimant"}, now.Add(3*time.Minute), lifecycleOwner, fixture.fence); !errors.Is(err, ErrHumanClaimRequired) {
		t.Fatalf("old claimant decision returned %v", err)
	}
	decision, err := fixture.journal.DecideApproval(fixture.scope, requestID, loser, 3, "decision-1", ApprovalDecision{Decision: "approved", Reason: "verified"}, now.Add(3*time.Minute), lifecycleOwner, fixture.fence)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := fixture.journal.DecideApproval(fixture.scope, requestID, loser, 3, "decision-1", ApprovalDecision{Decision: "approved", Reason: "verified"}, now.Add(4*time.Minute), lifecycleOwner, fixture.fence)
	if err != nil || duplicate.EventID != decision.EventID {
		t.Fatalf("decision retry=%+v err=%v", duplicate, err)
	}
	if _, err := fixture.journal.DecideApproval(fixture.scope, requestID, loser, 3, "decision-1", ApprovalDecision{Decision: "denied", Reason: "changed"}, now.Add(4*time.Minute), lifecycleOwner, fixture.fence); !errors.Is(err, ErrHumanResponseConflict) {
		t.Fatalf("changed duplicate decision returned %v", err)
	}
	if _, err := fixture.journal.ApplyHumanResponseAtBoundary(fixture.scope, requestID, stepID, now.Add(5*time.Minute), lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	state, err := fixture.journal.HumanRequestState(fixture.scope, requestID)
	if err != nil || state.Status != HumanRequestApplied || state.ClaimGeneration != 2 || state.ClaimedBy == nil || state.ClaimedBy.ID != loser.ID {
		t.Fatalf("approval state=%+v err=%v", state, err)
	}
	asked, decided := 0, 0
	for _, event := range fixture.journal.List(fixture.scope) {
		if event.EventType == EventApprovalAsked {
			asked++
		}
		if event.EventType == EventApprovalDecided {
			decided++
		}
	}
	if asked != 1 || decided != 1 {
		t.Fatalf("approval event pair asked=%d decided=%d", asked, decided)
	}
}

// Threat ID: TM-HUMAN-002
func TestHumanRequestRejectsExpiredIneligibleAndUnsafeResponses(t *testing.T) {
	fixture := newLifecycleFixture(t, "")
	const turnID, requestID = "turn-expiry", "human-expiry"
	startSessionTurn(t, fixture, turnID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	eligible := humanActor(fixture.scope, "eligible", now)
	ineligible := humanActor(fixture.scope, "ineligible", now)
	request := interactionRequest(now, turnID, requestID, identity.ActorRef{Type: eligible.Type, ID: eligible.ID})
	request.Deadline = now.Add(time.Minute)
	if _, err := fixture.journal.RequestHumanInteraction(fixture.scope, request, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.RespondHumanInteraction(fixture.scope, requestID, ineligible, 1, "ineligible-answer", json.RawMessage(`"resume"`), now.Add(30*time.Second), lifecycleOwner, fixture.fence); !errors.Is(err, ErrHumanActorIneligible) {
		t.Fatalf("ineligible actor returned %v", err)
	}
	if _, err := fixture.journal.RespondHumanInteraction(fixture.scope, requestID, eligible, 1, "late-answer", json.RawMessage(`"resume"`), now.Add(2*time.Minute), lifecycleOwner, fixture.fence); !errors.Is(err, ErrHumanRequestExpired) {
		t.Fatalf("expired response returned %v", err)
	}
	timedOut, err := fixture.journal.ExpireHumanRequest(fixture.scope, requestID, now.Add(2*time.Minute), lifecycleOwner, fixture.fence)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := fixture.journal.ExpireHumanRequest(fixture.scope, requestID, now.Add(3*time.Minute), lifecycleOwner, fixture.fence)
	if err != nil || retry.EventID != timedOut.EventID {
		t.Fatalf("timeout retry=%+v err=%v", retry, err)
	}
	state, err := fixture.journal.HumanRequestState(fixture.scope, requestID)
	if err != nil || state.Status != HumanRequestTimedOut {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestHumanRequestWithdrawalAndActiveStepCannotConsumeResponse(t *testing.T) {
	fixture := newLifecycleFixture(t, "")
	const turnID, pendingStep, activeStep = "turn-withdraw", "step-pending", "step-active"
	startSessionTurn(t, fixture, turnID)
	createStep(t, fixture, turnID, pendingStep)
	createStep(t, fixture, turnID, activeStep)
	now := time.Now().UTC().Truncate(time.Microsecond)
	actor := humanActor(fixture.scope, "reviewer", now)
	withdraw := interactionRequest(now, turnID, "human-withdraw", identity.ActorRef{Type: actor.Type, ID: actor.ID})
	if _, err := fixture.journal.RequestHumanInteraction(fixture.scope, withdraw, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	withdrawn, err := fixture.journal.WithdrawHumanRequest(fixture.scope, withdraw.RequestID, actor, 1, "withdraw-1", "question superseded", now.Add(time.Minute), lifecycleOwner, fixture.fence)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := fixture.journal.WithdrawHumanRequest(fixture.scope, withdraw.RequestID, actor, 1, "withdraw-1", "question superseded", now.Add(2*time.Minute), lifecycleOwner, fixture.fence)
	if err != nil || retry.EventID != withdrawn.EventID {
		t.Fatalf("withdraw retry=%+v err=%v", retry, err)
	}
	if _, err := fixture.journal.RespondHumanInteraction(fixture.scope, withdraw.RequestID, actor, 1, "after-withdraw", json.RawMessage(`"resume"`), now.Add(2*time.Minute), lifecycleOwner, fixture.fence); !errors.Is(err, ErrHumanTransition) {
		t.Fatalf("withdrawn response returned %v", err)
	}

	active := interactionRequest(now, turnID, "human-active", identity.ActorRef{Type: actor.Type, ID: actor.ID})
	if _, err := fixture.journal.RequestHumanInteraction(fixture.scope, active, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.RespondHumanInteraction(fixture.scope, active.RequestID, actor, 1, "active-answer", json.RawMessage(`"resume"`), now.Add(time.Minute), lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.FreezeStepContext(fixture.scope, activeStep, lifecycleOwner, fixture.fence, fixture.context); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.CommitStepModelRequest(fixture.scope, activeStep, "model-active", lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.WaitTurnInput(fixture.scope, turnID, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.journal.ApplyHumanResponseAtBoundary(fixture.scope, active.RequestID, activeStep, now.Add(2*time.Minute), lifecycleOwner, fixture.fence); !errors.Is(err, ErrUnsafeStepBoundary) {
		t.Fatalf("active model step consumed response: %v", err)
	}
}

func TestHumanDeadlineUsesDurableAtLeastOnceTimer(t *testing.T) {
	root := t.TempDir()
	fixture := newLifecycleFixture(t, filepath.Join(root, "runtime.json"))
	const turnID, requestID = "turn-deadline", "human-deadline"
	startSessionTurn(t, fixture, turnID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	actor := humanActor(fixture.scope, "deadline-reviewer", now)
	request := interactionRequest(now, turnID, requestID, identity.ActorRef{Type: actor.Type, ID: actor.ID})
	request.Deadline = now.Add(5 * time.Minute)
	if _, err := fixture.journal.RequestHumanInteraction(fixture.scope, request, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	state, err := fixture.journal.HumanRequestState(fixture.scope, requestID)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := HumanDeadlineTimerSpec(state)
	if err != nil {
		t.Fatal(err)
	}
	clock := testkit.NewManualClock(now)
	timers, err := NewTimerStore(filepath.Join(root, "timers.json"), timerOptions(clock))
	if err != nil {
		t.Fatal(err)
	}
	timer, created, err := timers.Schedule(spec)
	if err != nil || !created {
		t.Fatalf("schedule=%+v created=%v err=%v", timer, created, err)
	}
	restartedTimers, err := NewTimerStore(filepath.Join(root, "timers.json"), timerOptions(clock))
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(6 * time.Minute)
	claims, err := restartedTimers.ClaimDue(clock.Now(), "timer-worker-1", time.Minute, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	first, err := fixture.journal.ApplyHumanDeadlineClaim(claims[0], clock.Now(), lifecycleOwner, fixture.fence)
	if err != nil || !first.Expired || first.Event.EventType != EventHumanInteractionTimedOut {
		t.Fatalf("first deadline=%+v err=%v", first, err)
	}
	// Simulate a crash after the timeout event but before timer acknowledgement.
	clock.Advance(2 * time.Minute)
	retryClaims, err := restartedTimers.ClaimDue(clock.Now(), "timer-worker-2", time.Minute, 1)
	if err != nil || len(retryClaims) != 1 || retryClaims[0].Timer.FencingToken <= claims[0].Timer.FencingToken {
		t.Fatalf("retry claims=%+v first=%+v err=%v", retryClaims, claims, err)
	}
	retry, err := fixture.journal.ApplyHumanDeadlineClaim(retryClaims[0], clock.Now(), lifecycleOwner, fixture.fence)
	if err != nil || !retry.Expired || retry.Event.EventID != first.Event.EventID || retry.Reason != "deadline_already_expired" {
		t.Fatalf("deadline retry=%+v err=%v", retry, err)
	}
	if _, err := restartedTimers.Acknowledge(timer.ID, "timer-worker-2", retryClaims[0].Timer.FencingToken, retryClaims[0].OccurrenceKey, clock.Now()); err != nil {
		t.Fatal(err)
	}
	loaded, err := restartedTimers.Get(timer.ID)
	if err != nil || loaded.State != TimerFired {
		t.Fatalf("timer=%+v err=%v", loaded, err)
	}
}
