package memory

import (
	"errors"
	"testing"
	"time"
)

func TestEvidenceLifecycleAndScopeIsolation(t *testing.T) {
	r := NewRepository()
	scope := Scope{TenantID: "t1", WorkspaceID: "w1", ProjectID: "p1"}
	item, err := r.Add(AddInput{ID: "fact-1", Scope: scope, Kind: "decision", Claim: "release gate", Content: "requires human approval", SourceIDs: []string{"event-1"}})
	if err != nil || item.Status != Candidate {
		t.Fatalf("add item=%+v err=%v", item, err)
	}
	if _, err := r.Get(Scope{TenantID: "t2", WorkspaceID: "w2", ProjectID: "p1"}, item.ID); !errors.Is(err, ErrScope) {
		t.Fatalf("scope leak: %v", err)
	}
	if _, err := r.Confirm(scope, item.ID, "reviewer", "verified event evidence"); !errors.Is(err, ErrTransition) {
		t.Fatalf("candidate was confirmed without quarantine: %v", err)
	}
	if _, err := r.Transition(scope, item.ID, Quarantined, "reviewer", "isolate pending review"); err != nil {
		t.Fatal(err)
	}
	confirmed, err := r.Confirm(scope, item.ID, "reviewer", "verified event evidence")
	if err != nil || confirmed.Status != Confirmed {
		t.Fatalf("confirm=%+v err=%v", confirmed, err)
	}
	if got := len(r.Stable(scope, timeZero())); got != 1 {
		t.Fatalf("stable=%d", got)
	}
}

func TestConflictCannotBePromoted(t *testing.T) {
	r := NewRepository()
	scope := Scope{TenantID: "t", WorkspaceID: "w"}
	if _, err := r.Add(AddInput{ID: "a", Scope: scope, Claim: "version", Content: "one", SourceIDs: []string{"e1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Add(AddInput{ID: "b", Scope: scope, Claim: "version", Content: "two", SourceIDs: []string{"e2"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Transition(scope, "b", Confirmed, "reviewer", "confirm"); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict promoted: %v", err)
	}
	if _, err := r.ResolveConflict(scope, "b", []string{"a"}, "reviewer", "evidence e2 supersedes e1"); err != nil {
		t.Fatal(err)
	}
	if winner, err := r.Confirm(scope, "b", "reviewer", "conflict resolved"); err != nil || winner.Status != Confirmed {
		t.Fatalf("resolved winner=%+v err=%v", winner, err)
	}
	if items := r.Query(QueryInput{Scope: scope, Claim: "version"}); len(items) != 1 || items[0].ID != "b" {
		t.Fatalf("stable retrieval=%+v", items)
	}
}

func TestSupersedeRejectsUnconfirmedWithoutCreatingReplacement(t *testing.T) {
	r := NewRepository()
	scope := Scope{TenantID: "t", WorkspaceID: "w"}
	if _, err := r.Add(AddInput{ID: "candidate", Scope: scope, Claim: "version", Content: "one", SourceIDs: []string{"e1"}}); err != nil {
		t.Fatal(err)
	}
	_, err := r.Supersede(scope, "candidate", AddInput{ID: "replacement", Claim: "version", Content: "two", SourceIDs: []string{"e2"}}, "reviewer", "replace")
	if !errors.Is(err, ErrTransition) {
		t.Fatalf("expected unconfirmed source rejection, got %v", err)
	}
	if _, err := r.Get(scope, "replacement"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replacement leaked after rejected supersede: %v", err)
	}
}

func timeZero() (t time.Time) { return }
