package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/adro-project/adro/internal/domain"
)

func TestPersistentMemoryRoundTripsControlPlaneAndContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-plane.json")
	first, err := NewPersistentMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	req, err := first.CreateRequirement(domain.Requirement{WorkspaceID: "w", Title: "Persist", Description: "round trip", AcceptanceCriteria: []string{"survives"}, AssigneeMemberIDs: []string{"dev"}, RepositoryIDs: []string{"repo"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.RememberIdempotency("requirement:local:key", req); err != nil {
		t.Fatal(err)
	}
	item, _, err := first.CreateWorkItemIfAbsent(domain.WorkItem{RequirementID: req.ID, RepositoryID: "repo", MemberID: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.SaveContextManifest(domain.ContextManifest{ContextID: "context-" + item.ID, StableSummary: "persisted context", Repositories: []domain.ContextRepository{{ID: "repo", Baseline: "abc"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.SaveRepairAttempt(domain.RepairAttempt{BugID: "bug-1", WorkItemID: item.ID, Attempt: 1, ContextID: "context-" + item.ID, Brief: domain.RepairBrief{BugID: "bug-1", Attempt: 1}}); err != nil {
		t.Fatal(err)
	}
	second, err := NewPersistentMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := second.GetRequirement(req.ID); err != nil || got.Title != req.Title {
		t.Fatalf("requirement=%+v err=%v", got, err)
	}
	if got, ok := second.Idempotent("requirement:local:key", domain.Requirement{}); !ok || got.(domain.Requirement).ID != req.ID {
		t.Fatalf("idempotency=%#v ok=%v", got, ok)
	}
	if got, err := second.GetContextManifest("context-"+item.ID, 0); err != nil || got.StableSummary != "persisted context" {
		t.Fatalf("manifest=%+v err=%v", got, err)
	}
	if got := second.ListRepairAttempts("bug-1"); len(got) != 1 || got[0].Attempt != 1 {
		t.Fatalf("repair attempts=%+v", got)
	}
}

func TestPersistentMutationRollsBackWhenStateCannotBeReplaced(t *testing.T) {
	m := NewMemory()
	// A directory is a valid parent for the temporary file, but cannot be the
	// destination of the atomic rename. This simulates a storage outage without
	// relying on platform-specific permission behavior.
	m.statePath = t.TempDir()
	req := domain.Requirement{WorkspaceID: "w", Title: "must not acknowledge", Description: "durability", AcceptanceCriteria: []string{"rollback"}, AssigneeMemberIDs: []string{"dev"}, RepositoryIDs: []string{"repo"}}
	if _, err := m.CreateRequirement(req); err == nil {
		t.Fatal("expected persistence failure")
	}
	if _, err := m.GetRequirement(req.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed mutation remained visible: %v", err)
	}
}

func TestIdempotencyMutationRollsBackWhenStateCannotBeReplaced(t *testing.T) {
	m := NewMemory()
	m.statePath = t.TempDir()
	if err := m.RememberIdempotency("broken", map[string]any{"value": "must not stick"}); err == nil {
		t.Fatal("expected persistence failure")
	}
	if _, ok := m.Idempotent("broken", nil); ok {
		t.Fatal("failed idempotency mutation remained visible")
	}
}
