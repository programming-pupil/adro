package store

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/adro-project/adro/internal/domain"
)

func TestConcurrentCommentEditAllowsSingleRevisionWinner(t *testing.T) {
	m := NewMemory()
	requirement, err := m.CreateRequirement(domain.Requirement{WorkspaceID: "w", Title: "comment race", Description: "exercise optimistic edit concurrency", AcceptanceCriteria: []string{"one revision wins"}, AssigneeMemberIDs: []string{"author"}})
	if err != nil {
		t.Fatal(err)
	}
	comment, err := m.CreateComment(domain.Comment{WorkspaceID: "w", TargetType: "requirement", TargetID: requirement.ID, AuthorID: "author", AuthorType: "member", Content: "original"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, content := range []string{"edit-a", "edit-b"} {
		wg.Add(1)
		go func(content string) {
			defer wg.Done()
			_, updateErr := m.UpdateComment(comment.ID, 1, content, nil, nil, content, "member")
			errs <- updateErr
		}(content)
	}
	wg.Wait()
	close(errs)
	succeeded, conflicted := 0, 0
	for updateErr := range errs {
		switch {
		case updateErr == nil:
			succeeded++
		case errors.Is(updateErr, ErrConflict):
			conflicted++
		default:
			t.Fatalf("unexpected edit error: %v", updateErr)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent edit result succeeded=%d conflicted=%d", succeeded, conflicted)
	}
	current, err := m.GetComment(comment.ID)
	if err != nil || current.Revision != 2 {
		t.Fatalf("comment revision=%d err=%v", current.Revision, err)
	}
}

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

func TestPersistentMemoryRejectsStaleWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-plane.json")
	first, err := NewPersistentMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPersistentMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.CreateRequirement(domain.Requirement{WorkspaceID: "w", Title: "winner", Description: "durable", AcceptanceCriteria: []string{"ok"}, AssigneeMemberIDs: []string{"dev"}, RepositoryIDs: []string{"repo"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := second.CreateRequirement(domain.Requirement{WorkspaceID: "w", Title: "stale", Description: "must reject", AcceptanceCriteria: []string{"ok"}, AssigneeMemberIDs: []string{"dev"}, RepositoryIDs: []string{"repo"}}); err == nil {
		t.Fatal("expected stale writer to be rejected")
	}
}

func TestPersistentMemoryRetainsImmutableCommentAttachmentRevisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-plane.json")
	first, err := NewPersistentMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	requirement, err := first.CreateRequirement(domain.Requirement{WorkspaceID: "w", Title: "comment lineage", Description: "survive restart", AcceptanceCriteria: []string{"revisions"}, AssigneeMemberIDs: []string{"dev"}, RepositoryIDs: []string{"repo"}})
	if err != nil {
		t.Fatal(err)
	}
	comment, err := first.CreateComment(domain.Comment{WorkspaceID: "w", TargetType: "requirement", TargetID: requirement.ID, AuthorID: "author", AuthorType: "member", Content: "first"})
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := first.SaveAttachment(domain.EntityAttachment{WorkspaceID: "w", OwnerType: "comment", OwnerID: comment.ID, Filename: "evidence.txt", ArtifactURI: "artifact://tenant/evidence/1"})
	if err != nil {
		t.Fatal(err)
	}
	attachmentIDs := []string{attachment.ID}
	if _, err := first.UpdateComment(comment.ID, 1, "second", nil, &attachmentIDs, "editor", "member"); err != nil {
		t.Fatal(err)
	}
	second, err := NewPersistentMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	revisions, err := second.ListCommentRevisions(comment.ID)
	if err != nil || len(revisions) != 2 {
		t.Fatalf("revisions=%+v err=%v", revisions, err)
	}
	if revisions[0].Content != "first" || len(revisions[0].AttachmentIDs) != 0 || revisions[1].Content != "second" || len(revisions[1].AttachmentIDs) != 1 || revisions[1].AttachmentIDs[0] != attachment.ID || revisions[1].EditorID != "editor" {
		t.Fatalf("comment lineage=%+v", revisions)
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

func TestContextAndRepairLineageRejectRewindsAndProtectSlices(t *testing.T) {
	m := NewMemory()
	first, err := m.SaveContextManifest(domain.ContextManifest{
		ContextID: "ctx", Version: 1, StableSummary: "initial",
		Repositories:      []domain.ContextRepository{{ID: "repo", Baseline: "a"}},
		LatestEvidenceIDs: []string{"e1"}, ArtifactRefs: []string{"artifact://one"},
	})
	if err != nil {
		t.Fatal(err)
	}
	first.Repositories[0].Baseline = "caller-mutated"
	first.LatestEvidenceIDs[0] = "caller-mutated"
	latest, err := m.GetContextManifest("ctx", 0)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Repositories[0].Baseline != "a" || latest.LatestEvidenceIDs[0] != "e1" {
		t.Fatalf("context manifest leaked mutable slices: %+v", latest)
	}
	second, err := m.SaveContextManifest(domain.ContextManifest{
		ContextID: "ctx", StableSummary: "next",
		Repositories: []domain.ContextRepository{{ID: "repo", Baseline: "a", Head: "b"}},
	})
	if err != nil || second.Version != 2 {
		t.Fatalf("automatic context version=%+v err=%v", second, err)
	}
	if _, err := m.SaveContextManifest(domain.ContextManifest{
		ContextID: "ctx", Version: 1, StableSummary: "rewind",
		Repositories: []domain.ContextRepository{{ID: "repo", Baseline: "a"}},
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("context version rewind was accepted: %v", err)
	}
	repair, err := m.SaveRepairAttempt(domain.RepairAttempt{
		BugID: "bug", WorkItemID: "work", Attempt: 1, ContextID: "ctx",
		Brief: domain.RepairBrief{FailedEvidence: []string{"e1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	repair.Brief.FailedEvidence[0] = "caller-mutated"
	listed := m.ListRepairAttempts("bug")
	if len(listed) != 1 || listed[0].Brief.FailedEvidence[0] != "e1" {
		t.Fatalf("repair evidence leaked mutable slices: %+v", listed)
	}
	if _, err := m.SaveRepairAttempt(domain.RepairAttempt{
		BugID: "bug", WorkItemID: "work", Attempt: 1, ContextID: "ctx",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate repair attempt was accepted: %v", err)
	}
}
