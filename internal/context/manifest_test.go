package context

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func findBlock(manifest Manifest, id string) (Block, bool) {
	for _, block := range manifest.Blocks {
		if block.ID == id {
			return block, true
		}
	}
	return Block{}, false
}

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
	if err := m.PromptManifest.Validate(); err != nil || len(m.PromptManifest.Segments) != 2 {
		t.Fatalf("prompt manifest=%+v err=%v", m.PromptManifest, err)
	}
	m2, rec, err := Compile("s", 1, 6, blocks)
	if err != nil {
		t.Fatal(err)
	}
	if len(m2.Blocks) != 2 || rec.Algorithm != "semantic-extractive" || rec.QualityScore <= 0 || rec.SourceHash == "" || rec.SummaryHash == "" {
		t.Fatalf("compile=%#v %#v", m2, rec)
	}
}

func TestPromptManifestCanonicalOrderAndTamperDetection(t *testing.T) {
	blocks := []Block{
		{ID: "memory", Kind: "memory", Source: "memory", Content: "prior fact", Hash: HashBlock(Block{Content: "prior fact"}), Policy: "optional", Trust: "reviewed", SelectionReason: "memory", TokenEstimate: 3},
		{ID: "objective", Kind: "turn", Source: "turn", Content: "latest objective", Hash: HashBlock(Block{Content: "latest objective"}), Policy: "mandatory", Trust: "source", SelectionReason: "latest_objective", TokenEstimate: 4, Mandatory: true},
	}
	prompt, err := BuildPromptManifest(blocks)
	if err != nil {
		t.Fatal(err)
	}
	if err := prompt.Validate(); err != nil {
		t.Fatal(err)
	}
	if prompt.Segments[0].Kind != "latest_objective" || prompt.Segments[1].Kind != "context_memory" {
		t.Fatalf("segments were not canonicalized: %+v", prompt.Segments)
	}
	prompt.Segments[0].Content = "tampered"
	if err := prompt.Validate(); err == nil {
		t.Fatal("tampered prompt segment must be rejected")
	}
}

func TestManifestRejectsPromptLayerThatDoesNotCoverSelectedBlock(t *testing.T) {
	manifest, err := NewManifest("prompt-binding", 1, 32, []Block{{
		ID: "objective", Kind: "turn", Source: "user:latest", Content: "ship the complete change",
		Hash: HashBlock(Block{Content: "ship the complete change"}), Policy: "mandatory", Trust: "source",
		SelectionReason: "latest_objective", TokenEstimate: 6, Mandatory: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	manifest.PromptManifest.Segments[0].Content = "an older objective"
	manifest.PromptManifest.Digest = promptManifestDigest(manifest.PromptManifest)
	manifest.Digest = ""
	manifest.PromptManifestHash = ""
	manifest.Digest = manifestDigest(manifest)
	manifest.PromptManifestHash = promptManifestHash(manifest)
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "prompt manifest binding mismatch") {
		t.Fatalf("prompt layer drift was accepted: %v", err)
	}
}

func TestRenderPromptManifestPreservesCanonicalLayersAndLineage(t *testing.T) {
	manifest, err := NewManifest("render-session", 1, 20, []Block{
		{ID: "memory", Kind: "memory", Source: "memory", Content: "remember the decision", Policy: "optional", Trust: "reviewed", SelectionReason: "memory", TokenEstimate: 5},
		{ID: "objective", Kind: "turn", Source: "user:latest", Content: "latest objective", Policy: "mandatory", Trust: "source", SelectionReason: "latest_objective", TokenEstimate: 4, Mandatory: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderPromptManifest(manifest.PromptManifest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "kind=latest_objective") || !strings.Contains(rendered, "kind=context_memory") || !strings.Contains(rendered, "latest objective") || !strings.Contains(rendered, "hash=") {
		t.Fatalf("rendered prompt lost layer lineage: %s", rendered)
	}
	if strings.Index(rendered, "latest objective") > strings.Index(rendered, "remember the decision") {
		t.Fatalf("prompt layers are not canonical: %s", rendered)
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

func TestCompilerNeverTruncatesAtomicBlocks(t *testing.T) {
	mandatory := Block{ID: "objective", Kind: "turn", Source: "user:latest", Content: "实现 ✅ 保留完整 JSON：{\"status\":\"pass\",\"items\":[1,2,3]}", Policy: "mandatory", Trust: "source", SelectionReason: "latest_objective", TokenEstimate: tokenEstimate("实现 ✅ 保留完整 JSON：{\"status\":\"pass\",\"items\":[1,2,3]}") + 1, Mandatory: true}
	atomic := Block{ID: "code", Kind: "code", Source: "artifact:patch", Content: "func Add(a, b int) int {\n\treturn a + b\n}\n", Policy: "optional", Trust: "artifact", SelectionReason: "patch_context", TokenEstimate: tokenEstimate("func Add(a, b int) int {\n\treturn a + b\n}\n") + 1}
	manifest, record, err := Compile("atomic", 1, mandatory.TokenEstimate+2, []Block{mandatory, atomic})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := findBlock(manifest, mandatory.ID); !ok || got.Content != mandatory.Content {
		t.Fatalf("mandatory objective was altered: %#v", got)
	}
	if got, ok := findBlock(manifest, atomic.ID); ok && got.Content != atomic.Content {
		t.Fatalf("atomic code block was truncated: %#v", got)
	}
	if !containsString(record.DroppedFacts, "block:"+atomic.ID) {
		t.Fatalf("atomic drop was not recorded: %#v", record)
	}
}

func TestCompilerFailsClosedWhenMandatoryTransactionDoesNotFit(t *testing.T) {
	before := Block{ID: "tool-before", Kind: "tool_call", Source: "tool:call-1", Content: "{\"command\":\"go test ./...\"}", Policy: "transaction", Trust: "provider", SelectionReason: "unfinished_tool", TokenEstimate: 8, Mandatory: true, Metadata: map[string]string{"tool_call_id": "call-1", "tool_status": "before"}}
	after := Block{ID: "tool-after", Kind: "tool_result", Source: "tool:call-1", Content: "{\"exit_code\":1,\"stderr\":\"failed\"}", Policy: "transaction", Trust: "provider", SelectionReason: "unfinished_tool", TokenEstimate: 8, Mandatory: true, Metadata: map[string]string{"tool_call_id": "call-1", "tool_status": "after"}}
	if _, _, err := Compile("transaction", 1, 15, []Block{before, after}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("mandatory tool transaction must fail closed, got %v", err)
	}
}

func TestRenderManifestUsesTheValidatedPromptContract(t *testing.T) {
	manifest, _, err := Compile("render-compat", 1, 32, []Block{{
		ID: "objective", Kind: "turn", Source: "user:latest", Content: "最新目标：保留完整结果", Policy: "mandatory", Trust: "source",
		SelectionReason: "latest_objective", TokenEstimate: 8, Mandatory: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "kind=latest_objective") || !strings.Contains(rendered, "最新目标：保留完整结果") {
		t.Fatalf("compatibility rendering bypassed prompt manifest: %s", rendered)
	}
}

func TestCompilerNeverRuneTruncatesMandatoryUnicodeObjectives(t *testing.T) {
	for i := 0; i < 64; i++ {
		content := fmt.Sprintf("目标-%d：保留 ✅ 中文、e\u0301 和 JSON {\"attempt\":%d}", i, i)
		mandatory := Block{ID: fmt.Sprintf("objective-%d", i), Kind: "turn", Source: "user:latest", Content: content, Policy: "mandatory", Trust: "source", SelectionReason: "latest_objective", TokenEstimate: tokenEstimate(content), Mandatory: true}
		optional := Block{ID: fmt.Sprintf("history-%d", i), Kind: "memory", Source: "memory", Content: strings.Repeat("optional history ", 64), Policy: "optional", Trust: "reviewed", SelectionReason: "history", TokenEstimate: 512}
		manifest, _, err := Compile("unicode", int64(i+1), mandatory.TokenEstimate, []Block{optional, mandatory})
		if err != nil {
			t.Fatalf("iteration %d: mandatory objective overflowed its exact budget: %v", i, err)
		}
		block, ok := findBlock(manifest, mandatory.ID)
		if !ok || block.Content != content || !block.Mandatory {
			t.Fatalf("iteration %d: mandatory content was omitted or changed: %#v", i, block)
		}
	}
}

func TestCompilerKeepsToolTransactionTogether(t *testing.T) {
	objective := Block{ID: "objective", Kind: "turn", Source: "user:latest", Content: "ship the change", Policy: "mandatory", Trust: "source", SelectionReason: "latest_objective", TokenEstimate: 2, Mandatory: true}
	before := Block{ID: "tool-before", Kind: "tool_call", Source: "tool:call-1", Content: `{"command":"go test"}`, Policy: "transaction", Trust: "provider", SelectionReason: "completed_tool", TokenEstimate: 3, Metadata: map[string]string{"tool_call_id": "call-1", "tool_status": "before"}}
	after := Block{ID: "tool-after", Kind: "tool_result", Source: "tool:call-1", Content: `{"exit_code":0}`, Policy: "transaction", Trust: "provider", SelectionReason: "completed_tool", TokenEstimate: 3, Metadata: map[string]string{"tool_call_id": "call-1", "tool_status": "after"}}

	manifest, record, err := Compile("transaction-pair", 1, 7, []Block{objective, before, after})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findBlock(manifest, before.ID); ok {
		t.Fatalf("tool call was selected without its result: %+v", manifest.Blocks)
	}
	if _, ok := findBlock(manifest, after.ID); ok {
		t.Fatalf("tool result was selected without its call: %+v", manifest.Blocks)
	}
	if !containsString(record.DroppedFacts, "block:"+before.ID) || !containsString(record.DroppedFacts, "block:"+after.ID) {
		t.Fatalf("transaction pair drop was not recorded: %+v", record)
	}

	manifest, _, err = Compile("transaction-pair", 1, 8, []Block{objective, before, after})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findBlock(manifest, before.ID); !ok {
		t.Fatal("tool call should be retained when the complete transaction fits")
	}
	if _, ok := findBlock(manifest, after.ID); !ok {
		t.Fatal("tool result should be retained when the complete transaction fits")
	}
}

func FuzzCompilerPreservesMandatoryBoundaries(f *testing.F) {
	f.Add("目标 ✅ 保留完整 JSON {\"attempt\":1}", `{"command":"go test ./..."}`, `{"exit_code":1,"stderr":"failed"}`)
	f.Add("中文 e\u0301 newline\nacceptance", `{"path":"src/main.go","args":["--check"]}`, `{"exit_code":0}`)
	f.Fuzz(func(t *testing.T, objective, toolCall, toolResult string) {
		if strings.TrimSpace(objective) == "" || strings.TrimSpace(toolCall) == "" || strings.TrimSpace(toolResult) == "" {
			t.Skip()
		}
		objectiveTokens := tokenEstimate(objective)
		callTokens := tokenEstimate(toolCall)
		resultTokens := tokenEstimate(toolResult)
		if objectiveTokens < 1 || callTokens < 1 || resultTokens < 1 {
			t.Skip()
		}
		objectiveBlock := Block{ID: "objective", Kind: "turn", Source: "user:latest", Content: objective, Policy: "mandatory", Trust: "source", SelectionReason: "latest_objective", TokenEstimate: objectiveTokens, Mandatory: true}
		callBlock := Block{ID: "tool-call", Kind: "tool_call", Source: "tool:fuzz", Content: toolCall, Policy: "transaction", Trust: "provider", SelectionReason: "unfinished_tool", TokenEstimate: callTokens, Mandatory: true, Metadata: map[string]string{"tool_call_id": "fuzz", "tool_status": "before"}}
		resultBlock := Block{ID: "tool-result", Kind: "tool_result", Source: "tool:fuzz", Content: toolResult, Policy: "transaction", Trust: "provider", SelectionReason: "unfinished_tool", TokenEstimate: resultTokens, Mandatory: true, Metadata: map[string]string{"tool_call_id": "fuzz", "tool_status": "after"}}

		manifest, _, err := Compile("fuzz", 1, objectiveTokens+callTokens+resultTokens, []Block{objectiveBlock, callBlock, resultBlock})
		if err != nil {
			t.Fatalf("mandatory boundary overflowed an exact budget: %v", err)
		}
		objectiveSelected, ok := findBlock(manifest, objectiveBlock.ID)
		if !ok || objectiveSelected.Content != objective {
			t.Fatalf("mandatory objective was omitted or altered: selected=%#v original=%q", objectiveSelected, objective)
		}
		_, callSelected := findBlock(manifest, callBlock.ID)
		_, resultSelected := findBlock(manifest, resultBlock.ID)
		if callSelected != resultSelected {
			t.Fatalf("tool transaction was split: call=%t result=%t blocks=%+v", callSelected, resultSelected, manifest.Blocks)
		}
	})
}

func TestManifestRejectsTokenAndMandatorySetDrift(t *testing.T) {
	manifest, err := NewManifest("integrity", 1, 32, []Block{
		{ID: "objective", Kind: "turn", Source: "user:latest", Content: "preserve the objective", Policy: "mandatory", Trust: "source", SelectionReason: "latest_objective", TokenEstimate: 6, Mandatory: true},
		{ID: "memory", Kind: "memory", Source: "memory:1", Content: "prior context", Policy: "optional", Trust: "reviewed", SelectionReason: "memory", TokenEstimate: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest.TokenEstimate++
	manifest.Digest = ""
	manifest.PromptManifestHash = ""
	manifest.Digest = manifestDigest(manifest)
	manifest.PromptManifestHash = promptManifestHash(manifest)
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "token estimate mismatch") {
		t.Fatalf("token sum drift was accepted: %v", err)
	}

	manifest, err = NewManifest("integrity", 1, 32, []Block{
		{ID: "objective", Kind: "turn", Source: "user:latest", Content: "preserve the objective", Policy: "mandatory", Trust: "source", SelectionReason: "latest_objective", TokenEstimate: 6, Mandatory: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest.RequiredBlockIDs = nil
	manifest.Digest = ""
	manifest.PromptManifestHash = ""
	manifest.Digest = manifestDigest(manifest)
	manifest.PromptManifestHash = promptManifestHash(manifest)
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "required context block set is incomplete") {
		t.Fatalf("mandatory set drift was accepted: %v", err)
	}
}

func TestCompilerPropertyMandatoryContentIsExact(t *testing.T) {
	seeds := []string{"中文需求", "emoji ✅✅", "line\nwith\ncode", "JSON {\"nested\":true}", "a\u0301 combining"}
	for _, content := range seeds {
		t.Run(content, func(t *testing.T) {
			block := Block{ID: "latest", Kind: "turn", Source: "user", Content: content, Policy: "mandatory", Trust: "source", SelectionReason: "latest_objective", TokenEstimate: tokenEstimate(content), Mandatory: true}
			manifest, _, err := Compile("property", 1, block.TokenEstimate, []Block{block})
			if err != nil {
				t.Fatal(err)
			}
			got, ok := findBlock(manifest, block.ID)
			if !ok || got.Content != content {
				t.Fatalf("mandatory block changed: got=%q want=%q", got.Content, content)
			}
			if block.TokenEstimate > 1 {
				if _, _, err := Compile("property", 1, block.TokenEstimate-1, []Block{block}); !errors.Is(err, ErrOverflow) {
					t.Fatalf("under-budget mandatory block must overflow, got %v", err)
				}
			}
		})
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
