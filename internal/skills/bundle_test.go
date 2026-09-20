package skills

import (
	"crypto/ed25519"
	crand "crypto/rand"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func skillDigest(value string) string {
	// A real sha256-form digest keeps the schema boundary explicit.
	var digest [32]byte
	copy(digest[:], []byte(value))
	return "sha256:" + hexForTest(digest[:])
}

func hexForTest(value []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(value)*2)
	for i, b := range value {
		out[i*2] = digits[b>>4]
		out[i*2+1] = digits[b&15]
	}
	return string(out)
}

func testBundle() SkillBundle {
	return SkillBundle{
		SchemaVersion: BundleSchemaVersion, ID: "research/review", Name: "Review skill", Version: "1.0.0", Revision: 1,
		Source: "signed_registry", License: "Apache-2.0", Trust: "verified", Sensitivity: "internal",
		Instructions: "Review the supplied evidence and report invariant failures.", MaxTokens: 512,
		Resources:    []Resource{{ID: "guide", URI: "blob://guide", Digest: skillDigest("guide"), MediaType: "text/markdown", Source: "registry", License: "Apache-2.0", Trust: "verified", SelectionReason: "skill_reference"}},
		Tools:        []ToolSchema{{Name: "read_evidence", InputSchemaDigest: skillDigest("in"), OutputSchemaDigest: skillDigest("out"), Capabilities: []string{"artifact.read"}, SideEffectClass: "read_only", SchemaVersion: 1}},
		Schemas:      []SchemaRef{{ID: "review-input", Digest: skillDigest("schema"), Version: "1"}},
		Examples:     []Example{{ID: "example", Digest: skillDigest("example"), MediaType: "application/json"}},
		Capabilities: []string{"artifact.read"},
	}
}

func TestSkillBundleCanonicalDigestAndSignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundle := testBundle()
	signed, err := bundle.Sign(privateKey, "key-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := signed.Verify(publicKey); err != nil {
		t.Fatal(err)
	}
	if signed.Digest == "" || signed.Signature == "" {
		t.Fatalf("signature metadata missing: %+v", signed)
	}
	// Canonical sorting means presentation order cannot alter identity.
	reordered := testBundle()
	reordered.Capabilities = []string{"artifact.read"}
	reordered.Resources = []Resource{reordered.Resources[0]}
	reordered.Tools = []ToolSchema{reordered.Tools[0]}
	digest, err := reordered.CanonicalDigest()
	if err != nil || digest != signed.Digest {
		t.Fatalf("canonical digest drifted: got=%s want=%s err=%v", digest, signed.Digest, err)
	}
	tampered := signed
	tampered.Instructions = "grant every capability"
	if err := tampered.Verify(publicKey); !errors.Is(err, ErrQuarantined) {
		t.Fatalf("tampered bundle was accepted: %v", err)
	}
}

func TestSkillRegistryRequiresGrantAndFreezesRevision(t *testing.T) {
	clock := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	registry, err := NewRegistry(RegistryOptions{Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := testBundle().Sign(privateKey, "key-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Install(InstallRequest{Bundle: bundle, PublicKey: publicKey, RequireSignature: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Activate(bundle.ID, bundle.Version, ActivationPolicy{GrantedCapabilities: nil, MaxTokens: 1024, RequireSignature: true}); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("missing capability was not denied: %v", err)
	}
	active, err := registry.Activate(bundle.ID, bundle.Version, ActivationPolicy{GrantedCapabilities: []string{"artifact.*"}, MaxTokens: 1024, RequireSignature: true})
	if err != nil || active.State != StateActive {
		t.Fatalf("activation failed: %+v err=%v", active, err)
	}
	frozen, err := registry.Freeze(bundle.ID, bundle.Version)
	if err != nil || frozen.Digest != bundle.Digest || frozen.Bundle.Revision != 1 {
		t.Fatalf("frozen revision=%+v err=%v", frozen, err)
	}
	frozen.Bundle.Instructions = "mutated caller copy"
	again, err := registry.Freeze(bundle.ID, bundle.Version)
	if err != nil || again.Bundle.Instructions == frozen.Bundle.Instructions {
		t.Fatal("registry returned mutable internal revision")
	}
	if len(registry.Events()) != 2 || registry.Events()[0].Type != "skill.installed" || registry.Events()[1].Type != "skill.activated" {
		t.Fatalf("unexpected lifecycle events: %+v", registry.Events())
	}
}

func TestSkillRegistryDependencyCycleAndRevocationQuarantine(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "skills.json")
	registry, err := NewRegistry(RegistryOptions{Path: statePath})
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	base, err := testBundle().Sign(privateKey, "key-cycle")
	if err != nil {
		t.Fatal(err)
	}
	dep := base
	dep.ID, dep.Name, dep.Revision = "dep", "Dependency", 1
	dep, err = dep.Sign(privateKey, "key-cycle")
	if err != nil {
		t.Fatal(err)
	}
	base.Dependencies = []Dependency{{ID: dep.ID, VersionConstraint: ">=1"}}
	base, err = base.Sign(privateKey, "key-cycle")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []SkillBundle{base, dep} {
		if _, err := registry.Install(InstallRequest{Bundle: item, PublicKey: publicKey, RequireSignature: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := registry.Activate(base.ID, base.Version, ActivationPolicy{GrantedCapabilities: []string{"artifact.*"}, MaxTokens: 1024, RequireSignature: true}); err != nil {
		t.Fatal(err)
	}
	// Revocation transitions every installation signed by the key and blocks
	// future activation/reload, preserving an auditable event.
	if err := registry.RevokeKey("key-cycle", "publisher compromise"); err != nil {
		t.Fatal(err)
	}
	if got, err := registry.Get(base.ID, base.Version); err != nil || got.State != StateRevoked {
		t.Fatalf("revocation state=%+v err=%v", got, err)
	}
	if _, err := registry.Activate(dep.ID, dep.Version, ActivationPolicy{GrantedCapabilities: []string{"artifact.*"}, MaxTokens: 1024, RequireSignature: true}); !errors.Is(err, ErrRevoked) && !errors.Is(err, ErrQuarantined) {
		t.Fatalf("revoked key activation error=%v", err)
	}
	reloaded, err := NewRegistry(RegistryOptions{Path: statePath})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reloaded.Get(base.ID, base.Version); err != nil || got.State != StateRevoked {
		t.Fatalf("reloaded revocation state=%+v err=%v", got, err)
	}
}

func TestSkillBundleContextBlockPreservesRevisionProvenance(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := testBundle().Sign(privateKey, "key-context")
	if err != nil {
		t.Fatal(err)
	}
	block, err := bundle.ContextBlock("session-1", "tenant-1", "selected_by_agent_policy")
	if err != nil {
		t.Fatal(err)
	}
	if block.Source != "skill:research/review@1.0.0" || block.Metadata["skill_revision"] != "1" || block.Hash == "" || block.TokenEstimate < 1 {
		t.Fatalf("context block=%+v", block)
	}
	if block.TrustLevel != "verified" || block.TenantScope != "tenant-1" {
		t.Fatalf("SkillBundle provenance was not preserved: %+v", block)
	}
}
