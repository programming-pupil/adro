package memory

import (
	"errors"
	"testing"
	"time"
)

type fixedRetrievalScorer struct {
	version string
	scores  map[string]float64
}

func (s fixedRetrievalScorer) Version() string { return s.version }
func (s fixedRetrievalScorer) Score(_ string, item Item) (float64, error) {
	return s.scores[item.ID], nil
}

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

func TestQueryDoesNotTrustCallerProvidedEvidenceScores(t *testing.T) {
	r := NewRepository()
	scope := Scope{TenantID: "tenant", WorkspaceID: "workspace"}
	for _, input := range []AddInput{
		{ID: "lexical-winner", Scope: scope, Claim: "release gate human approval", Content: "human approval required", SourceIDs: []string{"source-a"}, EmbeddingScore: 0, LexicalScore: 0},
		{ID: "caller-score-winner", Scope: scope, Claim: "release gate other detail", Content: "unrelated detail", SourceIDs: []string{"source-b"}, EmbeddingScore: 1, LexicalScore: 1},
	} {
		item, err := r.Add(input)
		if err != nil {
			t.Fatal(err)
		}
		if item.Status == Candidate {
			if _, err := r.Transition(scope, item.ID, Quarantined, "reviewer", "quality review"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := r.Confirm(scope, item.ID, "reviewer", "quality review"); err != nil {
			t.Fatal(err)
		}
	}
	items := r.Query(QueryInput{Scope: scope, Claim: "human approval", Limit: 2})
	if len(items) != 1 || items[0].ID != "lexical-winner" {
		t.Fatalf("caller scores or nonmatching content influenced retrieval: %+v", items)
	}
}

func TestVersionedScorerAndRetrievalEvaluationAreRecorded(t *testing.T) {
	scope := Scope{TenantID: "tenant", WorkspaceID: "workspace"}
	r := NewRepositoryWithScorer(fixedRetrievalScorer{version: "embedding-v3:index-7", scores: map[string]float64{"expected": 0.2, "distractor": 0.9}})
	for _, input := range []AddInput{
		{ID: "expected", Scope: scope, Claim: "release decision approved", Content: "approval", SourceIDs: []string{"evidence-1"}},
		{ID: "distractor", Scope: scope, Claim: "release decision other", Content: "other", SourceIDs: []string{"wrong-source"}},
	} {
		item, err := r.Add(input)
		if err != nil {
			t.Fatal(err)
		}
		if item.Status == Candidate {
			if _, err := r.Transition(scope, item.ID, Quarantined, "reviewer", "quality review"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := r.Confirm(scope, item.ID, "reviewer", "quality review"); err != nil {
			t.Fatal(err)
		}
	}
	report := r.Evaluate(scope, []RetrievalEvaluationCase{{Query: "release decision", ExpectedIDs: []string{"expected"}, ExpectedSourceID: map[string][]string{"expected": {"evidence-1"}}, Limit: 1}})
	if report.ScorerVersion != "embedding-v3:index-7" || report.Precision != 0 || report.Recall != 0 || report.Pollution != 1 {
		t.Fatalf("evaluation did not expose bad scorer quality: %+v", report)
	}
}

func TestRepositoryOwnedSemanticScorerCanRankWithoutLexicalMatch(t *testing.T) {
	scope := Scope{TenantID: "tenant", WorkspaceID: "workspace"}
	r := NewRepositoryWithScorer(fixedRetrievalScorer{version: "semantic-v1", scores: map[string]float64{"semantic": 1, "other": 0}})
	for _, input := range []AddInput{
		{ID: "semantic", Scope: scope, Claim: "deployment approval", Content: "green light", SourceIDs: []string{"source-a"}},
		{ID: "other", Scope: scope, Claim: "unrelated note", Content: "maintenance", SourceIDs: []string{"source-b"}},
	} {
		item, err := r.Add(input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Transition(scope, item.ID, Quarantined, "reviewer", "quality review"); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Confirm(scope, item.ID, "reviewer", "quality review"); err != nil {
			t.Fatal(err)
		}
	}
	items := r.Query(QueryInput{Scope: scope, Claim: "release readiness", Limit: 1})
	if len(items) != 1 || items[0].ID != "semantic" {
		t.Fatalf("semantic scorer was blocked by lexical prefilter: %+v", items)
	}
}

func timeZero() (t time.Time) { return }
