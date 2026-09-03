package context

import (
	"errors"
	"strings"
	"testing"
)

func TestManifestEnvelopeAndDeterministicCompile(t *testing.T) {
	blocks := []Block{{ID: "optional", Source: "memory", Content: "x", Policy: "optional", Trust: "reviewed", SelectionReason: "relevance", TokenEstimate: 4}, {ID: "system", Source: "system", Content: "rules", Policy: "mandatory", Trust: "trusted", SelectionReason: "required", TokenEstimate: 4, Mandatory: true}}
	m, err := NewManifest("s", 1, 8, blocks)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	e, err := m.Envelope()
	if err != nil || e.SelectionDigest == "" || e.ReplayKey == "" {
		t.Fatalf("envelope: %#v %v", e, err)
	}
	m2, rec, err := Compile("s", 1, 6, blocks)
	if err != nil {
		t.Fatal(err)
	}
	if len(m2.Blocks) != 2 || rec.Algorithm != "semantic-extractive" || rec.QualityScore <= 0 || rec.SourceHash == "" || rec.SummaryHash == "" {
		t.Fatalf("compile=%#v %#v", m2, rec)
	}
}

func TestSemanticCompileRetainsHighValueFactsAndRecordsQuality(t *testing.T) {
	blocks := []Block{
		{ID: "system", Source: "system", Content: "Never expose credentials.", Policy: "mandatory", Trust: "trusted", SelectionReason: "required", TokenEstimate: 6, Mandatory: true},
		{ID: "history", Source: "turns", Content: "Blue. Must preserve idempotency. Timeout failed. Quiet.", Policy: "optional", Trust: "source", SelectionReason: "history", TokenEstimate: 30},
	}
	manifest, record, err := Compile("semantic", 1, 18, blocks)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.TokenEstimate > manifest.TokenBudget || record.Algorithm != "semantic-extractive" || record.QualityScore < 0.60 || len(record.RetainedFacts) == 0 || len(record.DroppedFacts) == 0 {
		t.Fatalf("manifest=%+v record=%+v", manifest, record)
	}
	var summary string
	for _, block := range manifest.Blocks {
		if block.Kind == "summary" {
			summary = block.Content
		}
	}
	if summary == "" || !strings.Contains(summary, "idempotency") || !strings.Contains(summary, "failed") {
		t.Fatalf("high-value facts were not retained: %q", summary)
	}
}

func TestSemanticCompileFallsBackRecoverably(t *testing.T) {
	blocks := []Block{
		{ID: "mandatory", Source: "system", Content: "rules", Policy: "mandatory", Trust: "trusted", SelectionReason: "required", TokenEstimate: 4, Mandatory: true},
		{ID: "a", Source: "memory", Content: "first", Policy: "optional", Trust: "reviewed", SelectionReason: "relevance", TokenEstimate: 2},
		{ID: "b", Source: "memory", Content: "second", Policy: "optional", Trust: "reviewed", SelectionReason: "relevance", TokenEstimate: 2},
	}
	failing := SummarizerFunc(func(SummaryRequest) (SummaryResult, error) { return SummaryResult{}, errors.New("summarizer offline") })
	manifest, record, err := CompileWithSummarizer("fallback", 1, 6, blocks, failing)
	if err != nil {
		t.Fatal(err)
	}
	if record.Algorithm != "deterministic-selection" || !strings.Contains(record.FallbackReason, "summarizer offline") || record.ReplayKey != manifest.Digest || len(record.RetainedFacts) != 1 || len(record.DroppedFacts) != 1 {
		t.Fatalf("fallback was not recorded: manifest=%+v record=%+v", manifest, record)
	}
}
func TestManifestOverflow(t *testing.T) {
	_, _, err := Compile("s", 1, 2, []Block{{ID: "system", Source: "system", Content: "rules", Policy: "mandatory", Trust: "trusted", SelectionReason: "required", TokenEstimate: 4, Mandatory: true}})
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("want overflow, got %v", err)
	}
}

func TestManifestPromptHashAndSemanticSnapshotAreVerified(t *testing.T) {
	m, err := NewManifest("session", 3, 10, []Block{{ID: "system", Source: "system", Content: "rules", Policy: "mandatory", Trust: "trusted", SelectionReason: "required", TokenEstimate: 2, Mandatory: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	m.PromptManifestHash = "tampered"
	if err := m.Validate(); err == nil {
		t.Fatal("tampered prompt manifest hash must be rejected")
	}
	m, err = NewManifest("session", 3, 10, []Block{{ID: "system", Source: "system", Content: "rules", Policy: "mandatory", Trust: "trusted", SelectionReason: "required", TokenEstimate: 2, Mandatory: true}})
	if err != nil {
		t.Fatal(err)
	}
	m.SemanticSnapshotVersion = 0
	if err := m.Validate(); err == nil {
		t.Fatal("missing semantic snapshot version must be rejected")
	}
}
