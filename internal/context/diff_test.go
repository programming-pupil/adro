package context

import (
	"testing"
)

func testContextBlock(id, source, content, policy, trust, reason string, mandatory bool) Block {
	return Block{ID: id, Source: source, Content: content, Policy: policy, Trust: trust, SelectionReason: reason, Mandatory: mandatory, TokenEstimate: Rune4Tokenizer{}.Estimate(content)}
}

func TestManifestDiffIsDeterministicAndContentFree(t *testing.T) {
	previous, err := NewManifest("diff-session", 1, 64, []Block{
		testContextBlock("system", "system", "never leak secrets", "mandatory", "trusted", "required", true),
		testContextBlock("old", "memory", "old fact", "optional", "reviewed", "memory", false),
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := NewManifest("diff-session", 2, 64, []Block{
		testContextBlock("system", "system", "never leak credentials", "mandatory", "trusted", "required", true),
		testContextBlock("new", "memory", "new fact", "optional", "reviewed", "memory", false),
	})
	if err != nil {
		t.Fatal(err)
	}
	diff, err := Diff(previous, current)
	if err != nil {
		t.Fatal(err)
	}
	if err := diff.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(diff.Added) != 1 || len(diff.Removed) != 1 || len(diff.Changed) != 1 || diff.TokenDelta != current.TokenEstimate-previous.TokenEstimate {
		t.Fatalf("unexpected diff: %+v", diff)
	}
	if diff.Changed[0].ID != "system" || len(diff.Changed[0].Reasons) == 0 {
		t.Fatalf("changed metadata missing: %+v", diff)
	}
	if diff.Added[0].ID == "" || diff.Added[0].AfterHash == "" || diff.Added[0].BeforeHash != "" {
		t.Fatalf("added block leaked invalid hashes: %+v", diff.Added[0])
	}
	if diff.Digest == "" {
		t.Fatal("diff digest is empty")
	}
	// Reordering the input blocks must not change the projection digest.
	current.Blocks[0], current.Blocks[1] = current.Blocks[1], current.Blocks[0]
	current, err = current.Rehash()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Diff(previous, current)
	if err != nil || second.Digest != diff.Digest {
		t.Fatalf("diff changed under block reorder: first=%s second=%s err=%v", diff.Digest, second.Digest, err)
	}
}

func TestManifestDiffRejectsCrossSessionAndTampering(t *testing.T) {
	first, err := NewManifest("a", 1, 16, []Block{testContextBlock("x", "memory", "fact", "optional", "reviewed", "memory", false)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewManifest("b", 2, 16, []Block{testContextBlock("x", "memory", "fact", "optional", "reviewed", "memory", false)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Diff(first, second); err == nil {
		t.Fatal("cross-session diff unexpectedly accepted")
	}
	third, err := NewManifest("a", 2, 16, []Block{testContextBlock("x", "memory", "changed", "optional", "reviewed", "memory", false)})
	if err != nil {
		t.Fatal(err)
	}
	diff, err := Diff(first, third)
	if err != nil {
		t.Fatal(err)
	}
	diff.TokenDelta++
	// The exact error is intentionally not part of the wire contract; any
	// validation failure is sufficient to prove the digest boundary.
	if err := diff.Validate(); err == nil {
		t.Fatal("tampered diff unexpectedly accepted")
	}
}

func TestCompactionLineageRequiresArchiveAndVerifiedRecall(t *testing.T) {
	mandatory := testContextBlock("objective", "user", "ship the idempotent durable change", "mandatory", "source", "latest_objective", true)
	optional := testContextBlock("history", "memory", "old history can be summarized", "optional", "reviewed", "history", false)
	source, err := NewManifest("compact", 1, 128, []Block{mandatory, optional})
	if err != nil {
		t.Fatal(err)
	}
	compacted, record, err := Compile("compact", 2, source.Blocks[0].TokenEstimate+4, []Block{mandatory, optional})
	if err != nil {
		t.Fatal(err)
	}
	if compacted.TokenEstimate >= source.TokenEstimate {
		// Use an explicit compacted manifest when the tiny fixture happens to
		// fit without invoking the summarizer.
		compacted, err = NewManifest("compact", 2, mandatory.TokenEstimate+1, []Block{mandatory})
		if err != nil {
			t.Fatal(err)
		}
		record = CompressionRecord{Algorithm: "deterministic-selection", Version: "v1", SourceHash: blockSetHash(source.Blocks), SourceBlockIDs: blockIDs(source.Blocks), TargetTokens: compacted.TokenEstimate, QualityScore: 1, ReplayKey: compacted.Digest}
	}
	facts, err := RequiredFacts(source)
	if err != nil {
		t.Fatal(err)
	}
	lineage, err := BuildCompactionLineage(source, compacted, record, ArchiveRef{URI: "blob://archive/compact", Digest: "sha256:archive", Size: 128}, facts)
	if err != nil {
		t.Fatal(err)
	}
	if err := lineage.Validate(); err != nil || !lineage.Recall.Verified || lineage.Archive.URI == "" {
		t.Fatalf("lineage=%+v err=%v", lineage, err)
	}
	probe, err := VerifyRecall(source, compacted, []string{"ship the idempotent durable change"})
	if err != nil || !probe.Verified {
		t.Fatalf("recall probe=%+v err=%v", probe, err)
	}
	if _, err := BuildCompactionLineage(source, compacted, record, ArchiveRef{}, facts); err == nil {
		t.Fatal("lineage without archive unexpectedly accepted")
	}
}
