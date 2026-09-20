package identity

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testActor(now time.Time) Actor {
	return Actor{
		Type: ActorHuman, ID: "human-1", TenantID: "tenant-1", WorkspaceID: "workspace-1",
		AuthnMethod: "local_session", CredentialID: "session-1", Audience: "adro-api",
		IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
	}
}

func TestActorContextAndScopeAreImmutable(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	actor := testActor(now)
	ctx, err := WithVerifiedActor(context.Background(), actor, now, "adro-api")
	if err != nil {
		t.Fatal(err)
	}
	actor.ID = "mutated"
	got, ok := FromContext(ctx)
	if !ok || got.ID != "human-1" || got.TenantID != "tenant-1" || got.WorkspaceID != "workspace-1" {
		t.Fatalf("actor=%+v ok=%v", got, ok)
	}
	got.Delegation = append(got.Delegation, Transition{})
	again, _ := FromContext(ctx)
	if len(again.Delegation) != 0 {
		t.Fatalf("context actor was mutable: %+v", again)
	}
}

func TestDelegationAndPrivilegedTransitionsPreserveFullChain(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	root := testActor(now)
	agent, err := TransitionTo(root, ActorRef{Type: ActorAgent, ID: "agent-1"}, TransitionDelegation, "execute approved plan", "", "delegated_session", "delegation-1", "adro-runtime", now, now.Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	worker, err := TransitionTo(agent, ActorRef{Type: ActorWorker, ID: "worker-1"}, TransitionTakeover, "recover expired claim", "approval-1", "worker_lease", "lease-1", "adro-runtime", now.Add(time.Minute), now.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if worker.Original() != (ActorRef{Type: ActorHuman, ID: "human-1"}) || len(worker.Delegation) != 2 || worker.Delegation[1].From.ID != "agent-1" {
		t.Fatalf("worker chain=%+v", worker)
	}
	if _, err := TransitionTo(root, ActorRef{Type: ActorService, ID: "service-1"}, TransitionImpersonation, "support", "", "service", "credential", "adro-api", now, now.Add(time.Minute)); !errors.Is(err, ErrTransitionUnapproved) {
		t.Fatalf("unapproved impersonation returned %v", err)
	}
	if _, err := TransitionTo(agent, ActorRef{Type: ActorWorker, ID: "worker-2"}, TransitionDelegation, "work", "", "worker", "credential", "adro-runtime", now, root.ExpiresAt.Add(time.Minute)); !errors.Is(err, ErrScopeEscalation) {
		t.Fatalf("expanded expiry returned %v", err)
	}
}

func TestActorFailsClosedForAudienceExpiryAndUnknownType(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	actor := testActor(now)
	if err := actor.Validate(now, "other-api"); !errors.Is(err, ErrAudienceMismatch) {
		t.Fatalf("audience error=%v", err)
	}
	if err := actor.Validate(actor.ExpiresAt, "adro-api"); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("expiry error=%v", err)
	}
	actor.Type = "system"
	if err := actor.Validate(now, "adro-api"); !errors.Is(err, ErrInvalidActor) {
		t.Fatalf("unknown type error=%v", err)
	}
}
