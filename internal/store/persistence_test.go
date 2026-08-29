package store

import (
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
	first.RememberIdempotency("requirement:local:key", req)
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
