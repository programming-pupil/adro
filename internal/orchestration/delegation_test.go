package orchestration

import (
	"errors"
	"testing"
	"time"
)

func TestFreezeDelegationOnlyAllowsNarrowerAuthority(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	parent := DelegationGrant{RequestID: "root-request", ParentPlanID: "root-parent", ParentAttemptID: "root-attempt", ChildPlanID: "parent-plan", ChildAgentID: "parent-agent", TenantID: "tenant-a", WorkspaceID: "workspace-a", Capabilities: []CapabilityRef{{Name: "files.read", Version: "v1"}, {Name: "net.fetch", Version: "v1"}}, Budget: Budget{Tokens: 1000, ToolCalls: 10, CostCents: 500, Duration: time.Hour, Concurrent: 2}, Deadline: now.Add(2 * time.Hour), RecursionDepth: 1, MaxChildDepth: 3}
	parent.Digest = delegationDigest(parent)
	child, err := FreezeDelegation(parent, DelegationRequest{RequestID: "child-request", ParentPlanID: "parent-plan", ParentAttemptID: "parent-attempt", ChildPlanID: "child-plan", ChildAgentID: "child-agent", TenantID: "tenant-a", WorkspaceID: "workspace-a", Capabilities: []CapabilityRef{{Name: "files.read", Version: "v1"}}, ContextBlockIDs: []string{"z", "a", "a"}, Budget: Budget{Tokens: 500, ToolCalls: 4, CostCents: 200, Duration: 30 * time.Minute, Concurrent: 1}, Deadline: now.Add(time.Hour), RecursionDepth: 2, MaxChildDepth: 3, IdempotencyKey: "delegate-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if child.Digest == "" || len(child.ContextBlockIDs) != 2 || child.ContextBlockIDs[0] != "a" || child.Budget.Tokens != 500 {
		t.Fatalf("child grant=%+v", child)
	}
	if err := child.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFreezeDelegationRejectsEscalationAndTenantCrossing(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	parent := DelegationGrant{RequestID: "root-request", ParentPlanID: "root-parent", ParentAttemptID: "root-attempt", ChildPlanID: "parent-plan", ChildAgentID: "parent-agent", TenantID: "tenant-a", WorkspaceID: "workspace-a", Capabilities: []CapabilityRef{{Name: "files.read", Version: "v1"}}, Budget: Budget{Tokens: 100, ToolCalls: 2, Concurrent: 1}, RecursionDepth: 1, MaxChildDepth: 2}
	parent.Digest = delegationDigest(parent)
	base := DelegationRequest{RequestID: "child-request", ParentPlanID: "parent-plan", ParentAttemptID: "parent-attempt", ChildPlanID: "child-plan", ChildAgentID: "child-agent", TenantID: "tenant-a", WorkspaceID: "workspace-a", Capabilities: []CapabilityRef{{Name: "files.read", Version: "v1"}}, Budget: Budget{Tokens: 101, ToolCalls: 1, Concurrent: 1}, RecursionDepth: 2, MaxChildDepth: 2, IdempotencyKey: "delegate-1"}
	if _, err := FreezeDelegation(parent, base, now); !errors.Is(err, ErrDelegationEscalation) {
		t.Fatalf("budget escalation err=%v", err)
	}
	base.Budget.Tokens = 50
	base.TenantID = "tenant-b"
	if _, err := FreezeDelegation(parent, base, now); !errors.Is(err, ErrDelegationEscalation) {
		t.Fatalf("tenant crossing err=%v", err)
	}
}
