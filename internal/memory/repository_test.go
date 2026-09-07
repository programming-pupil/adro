package memory

import (
	"errors"
	"math"
	"path/filepath"
	"strings"
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

type fixedEmbeddingProvider struct {
	modelID string
	err     error
}

func (p *fixedEmbeddingProvider) ModelID() string { return p.modelID }

func (p *fixedEmbeddingProvider) Embed(value string) ([]float64, error) {
	if p.err != nil {
		return nil, p.err
	}
	value = strings.ToLower(value)
	switch {
	case strings.Contains(value, "deployment approval"), strings.Contains(value, "release readiness"), strings.Contains(value, "green light"):
		return []float64{1, 0}, nil
	case strings.Contains(value, "maintenance"), strings.Contains(value, "unrelated"):
		return []float64{0, 1}, nil
	default:
		return []float64{0.5, 0.5}, nil
	}
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

func TestRepositoryOwnedEmbeddingRanksSemanticMatchesAndIgnoresCallerScores(t *testing.T) {
	scope := Scope{TenantID: "tenant", WorkspaceID: "workspace"}
	provider := &fixedEmbeddingProvider{modelID: "codex-embed-v1"}
	r, err := NewRepositoryWithEmbeddingProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []AddInput{
		{ID: "semantic", Scope: scope, Claim: "deployment approval", Content: "green light", SourceIDs: []string{"source-a"}, EmbeddingScore: 0, LexicalScore: 0},
		{ID: "caller-score-winner", Scope: scope, Claim: "maintenance note", Content: "unrelated detail", SourceIDs: []string{"source-b"}, EmbeddingScore: 1, LexicalScore: 1},
	} {
		item, addErr := r.Add(input)
		if addErr != nil {
			t.Fatal(addErr)
		}
		if _, addErr = r.Transition(scope, item.ID, Quarantined, "reviewer", "quality review"); addErr != nil {
			t.Fatal(addErr)
		}
		if _, addErr = r.Confirm(scope, item.ID, "reviewer", "quality review"); addErr != nil {
			t.Fatal(addErr)
		}
	}
	items := r.Query(QueryInput{Scope: scope, Claim: "release readiness", Limit: 1})
	if len(items) != 1 || items[0].ID != "semantic" {
		t.Fatalf("embedding provider did not rank semantic result: %+v", items)
	}
	if items[0].EmbeddingModelID != "codex-embed-v1" || len(items[0].Embedding) != 2 {
		t.Fatalf("embedding identity was not persisted: %+v", items[0])
	}
	if got := r.ScorerVersion(); got != "embedding-v1:codex-embed-v1" {
		t.Fatalf("scorer version=%q", got)
	}
}

func TestEmbeddingProviderFailsClosedOnQueryAndInvalidVectors(t *testing.T) {
	scope := Scope{TenantID: "tenant", WorkspaceID: "workspace"}
	provider := &fixedEmbeddingProvider{modelID: "codex-embed-v1"}
	r, err := NewRepositoryWithEmbeddingProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	provider.err = errors.New("embedding service unavailable")
	if items := r.Query(QueryInput{Scope: scope, Claim: "anything"}); items != nil {
		t.Fatalf("provider query error returned results: %+v", items)
	}

	for name, vector := range map[string][]float64{
		"empty": nil,
		"nan":   {math.NaN()},
		"inf":   {math.Inf(1)},
	} {
		bad := &fixedEmbeddingProvider{modelID: "bad-" + name}
		bad.err = nil
		original := bad.Embed
		_ = original
		// Keep the test provider's normal mapping out of this branch and
		// override the returned vector with a small local adapter.
		badProvider := embeddingFuncProvider{modelID: bad.modelID, vector: vector}
		badRepo, createErr := NewRepositoryWithEmbeddingProvider(badProvider)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, addErr := badRepo.Add(AddInput{ID: name, Scope: scope, Claim: "claim", Content: "content", SourceIDs: []string{"source"}}); addErr == nil {
			t.Fatalf("invalid %s embedding was accepted", name)
		}
	}
}

type embeddingFuncProvider struct {
	modelID string
	vector  []float64
}

func (p embeddingFuncProvider) ModelID() string { return p.modelID }
func (p embeddingFuncProvider) Embed(string) ([]float64, error) {
	return append([]float64(nil), p.vector...), nil
}

func TestEmbeddingProviderRejectsTypedNilAndPersistsIndexIdentity(t *testing.T) {
	var typedNil *fixedEmbeddingProvider
	if _, err := NewRepositoryWithEmbeddingProvider(typedNil); err == nil {
		t.Fatal("typed nil embedding provider was accepted")
	}

	scope := Scope{TenantID: "tenant", WorkspaceID: "workspace"}
	path := filepath.Join(t.TempDir(), "memory.json")
	provider := &fixedEmbeddingProvider{modelID: "codex-embed-v1"}
	r, err := NewPersistentRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetEmbeddingProvider(provider); err != nil {
		t.Fatal(err)
	}
	item, err := r.Add(AddInput{ID: "persisted", Scope: scope, Claim: "deployment approval", Content: "green light", SourceIDs: []string{"source"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Transition(scope, item.ID, Quarantined, "reviewer", "quality review"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Confirm(scope, item.ID, "reviewer", "quality review"); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewPersistentRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.SetEmbeddingProvider(provider); err != nil {
		t.Fatal(err)
	}
	if got := reopened.Query(QueryInput{Scope: scope, Claim: "release readiness", Limit: 1}); len(got) != 1 || got[0].ID != item.ID {
		t.Fatalf("reloaded embedding index did not retrieve item: %+v", got)
	}
	if err := reopened.SetEmbeddingProvider(&fixedEmbeddingProvider{modelID: "different-model"}); err != nil {
		t.Fatal(err)
	}
	if got := reopened.Query(QueryInput{Scope: scope, Claim: "release readiness", Limit: 1}); got != nil && len(got) != 0 {
		t.Fatalf("different embedding index mixed old vectors: %+v", got)
	}
}

func timeZero() (t time.Time) { return }
