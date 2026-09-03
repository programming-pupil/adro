package context

import (
	"errors"
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
	if len(m2.Blocks) != 1 || rec.Algorithm == "" {
		t.Fatalf("compile=%#v %#v", m2, rec)
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
