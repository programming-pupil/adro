// Package context defines the immutable, provider-facing context contract.
// It is separate from the session store so plans can persist and replay a
// manifest without loading mutable current memory.
package context

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Block struct {
	ID              string            `json:"id"`
	Kind            string            `json:"kind"`
	Source          string            `json:"source"`
	Content         string            `json:"content"`
	Hash            string            `json:"hash"`
	Policy          string            `json:"policy"`
	Trust           string            `json:"trust"`
	SelectionReason string            `json:"selection_reason"`
	TokenEstimate   int64             `json:"token_estimate"`
	Mandatory       bool              `json:"mandatory"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}
type Manifest struct {
	SessionID               string              `json:"session_id"`
	Version                 int64               `json:"version"`
	SemanticSnapshotVersion int64               `json:"semantic_snapshot_version"`
	TokenBudget             int64               `json:"token_budget"`
	TokenEstimate           int64               `json:"token_estimate"`
	Blocks                  []Block             `json:"blocks"`
	RequiredBlockIDs        []string            `json:"required_block_ids,omitempty"`
	OmittedRequiredIDs      []string            `json:"omitted_required_ids,omitempty"`
	CompilerVersion         string              `json:"compiler_version"`
	TokenizerID             string              `json:"tokenizer_id"`
	CompressionRecords      []CompressionRecord `json:"compression_records,omitempty"`
	PromptManifest          PromptManifest      `json:"prompt_manifest"`
	Digest                  string              `json:"digest"`
	PromptManifestHash      string              `json:"prompt_manifest_hash"`
	ParentDigest            string              `json:"parent_digest,omitempty"`
	CreatedAt               time.Time           `json:"created_at"`
}
type Envelope struct {
	Manifest        Manifest `json:"manifest"`
	SelectionDigest string   `json:"selection_digest"`
	ReplayKey       string   `json:"replay_key"`
}

// PromptSegment is the typed, provider-neutral prompt layer contract. A
// provider adapter may choose its wire format, but it cannot reorder or
// weaken these segments without invalidating the manifest.
type PromptSegment struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Version     int64  `json:"version"`
	Hash        string `json:"hash"`
	Trust       string `json:"trust"`
	Mandatory   bool   `json:"mandatory"`
	TokenBudget int64  `json:"token_budget"`
	Sensitivity string `json:"sensitivity,omitempty"`
	Source      string `json:"source"`
	Content     string `json:"content"`
}

// PromptManifest is shared by graph, pipeline, comment, repair, and session
// dispatches. Its digest is independent from presentation-only timestamps.
type PromptManifest struct {
	Version  string          `json:"version"`
	Segments []PromptSegment `json:"segments"`
	Digest   string          `json:"digest"`
}

const (
	PromptManifestVersion = "prompt-manifest-v1"
	CompilerVersion       = "adro-context-v2"
	TokenizerID           = "rune4-v1"
)

var promptSegmentRanks = map[string]int{
	"system_policy": 10, "workspace_policy": 20, "agent_role": 30,
	"plan_node_contract": 40, "latest_objective": 50,
	"acceptance_criteria": 60, "context_memory": 70, "tool_schema": 80,
	"evidence": 90, "output_contract": 100,
}

// BuildPromptManifest translates compiled context blocks into canonical
// semantic layers. Callers may override the derived layer with metadata
// prompt_kind when they have a stronger contract than the generic adapter.
func BuildPromptManifest(blocks []Block) (PromptManifest, error) {
	segments := make([]PromptSegment, 0, len(blocks))
	for _, block := range blocks {
		kind := strings.TrimSpace(block.Metadata["prompt_kind"])
		if kind == "" {
			kind = promptKindForBlock(block)
		}
		if _, ok := promptSegmentRanks[kind]; !ok {
			return PromptManifest{}, fmt.Errorf("prompt segment %s has unsupported kind %q", block.ID, kind)
		}
		if block.ID == "" || block.Source == "" || block.Hash == "" || block.Policy == "" || block.Trust == "" || block.SelectionReason == "" || block.TokenEstimate < 1 || strings.TrimSpace(block.Content) == "" {
			return PromptManifest{}, fmt.Errorf("prompt segment %s has incomplete lineage", block.ID)
		}
		segments = append(segments, PromptSegment{
			ID: block.ID, Kind: kind, Version: 1, Hash: block.Hash, Trust: block.Trust,
			Mandatory: block.Mandatory, TokenBudget: block.TokenEstimate,
			Sensitivity: strings.TrimSpace(block.Metadata["sensitivity"]), Source: block.Source, Content: block.Content,
		})
	}
	sort.SliceStable(segments, func(i, j int) bool {
		left, right := promptSegmentRanks[segments[i].Kind], promptSegmentRanks[segments[j].Kind]
		if left == right {
			return segments[i].ID < segments[j].ID
		}
		return left < right
	})
	manifest := PromptManifest{Version: PromptManifestVersion, Segments: segments}
	manifest.Digest = promptManifestDigest(manifest)
	return manifest, nil
}

func (p PromptManifest) Validate() error {
	if p.Version != PromptManifestVersion || p.Digest == "" {
		return errors.New("invalid prompt manifest metadata")
	}
	lastRank := -1
	seen := map[string]struct{}{}
	for _, segment := range p.Segments {
		rank, ok := promptSegmentRanks[segment.Kind]
		if !ok || segment.Version < 1 || segment.ID == "" || segment.Hash == "" || segment.Trust == "" || segment.Source == "" || segment.TokenBudget < 1 || strings.TrimSpace(segment.Content) == "" {
			return fmt.Errorf("invalid prompt segment %s", segment.ID)
		}
		if rank < lastRank {
			return errors.New("prompt manifest segment order is not canonical")
		}
		if _, ok := seen[segment.ID]; ok {
			return fmt.Errorf("duplicate prompt segment %s", segment.ID)
		}
		seen[segment.ID] = struct{}{}
		lastRank = rank
	}
	if promptManifestDigest(p) != p.Digest {
		return errors.New("prompt manifest digest mismatch")
	}
	return nil
}

// RenderPromptManifest is the provider-neutral text adapter for the typed
// prompt contract. The manifest remains authoritative: rendering preserves
// canonical segment order and includes each segment's lineage metadata so a
// provider cannot silently collapse or reorder policy, objective, tool, or
// evidence layers. Callers that need a structured wire format should send the
// PromptManifest itself and use this only as the textual compatibility view.
func RenderPromptManifest(p PromptManifest) (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	var builder strings.Builder
	for _, segment := range p.Segments {
		if strings.TrimSpace(segment.Content) == "" {
			continue
		}
		fmt.Fprintf(&builder, "[ADRO_PROMPT_SEGMENT kind=%s id=%s version=%d hash=%s trust=%s mandatory=%t source=%s]\n", segment.Kind, segment.ID, segment.Version, segment.Hash, segment.Trust, segment.Mandatory, segment.Source)
		builder.WriteString(segment.Content)
		if !strings.HasSuffix(segment.Content, "\n") {
			builder.WriteByte('\n')
		}
		builder.WriteString("[/ADRO_PROMPT_SEGMENT]\n")
	}
	return strings.TrimSpace(builder.String()), nil
}

func promptKindForBlock(block Block) string {
	if block.Mandatory && strings.EqualFold(strings.TrimSpace(block.Source), "system") {
		return "system_policy"
	}
	if block.Mandatory && strings.Contains(strings.ToLower(block.SelectionReason), "objective") {
		return "latest_objective"
	}
	switch strings.ToLower(strings.TrimSpace(block.Kind)) {
	case "system", "policy":
		return "system_policy"
	case "tool", "tool_call", "tool_result", "transaction", "tool_schema":
		return "tool_schema"
	case "code", "json", "artifact", "evidence":
		return "evidence"
	case "turn":
		return "context_memory"
	case "archive", "memory", "summary":
		return "context_memory"
	default:
		return "plan_node_contract"
	}
}

func promptManifestDigest(p PromptManifest) string {
	cp := p
	cp.Digest = ""
	b, _ := json.Marshal(cp)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Rehash recalculates derived digest fields after durable provenance or
// compression records are attached to a compiled selection.
func (m Manifest) Rehash() (Manifest, error) {
	prompt, err := BuildPromptManifest(m.Blocks)
	if err != nil {
		return Manifest{}, err
	}
	m.PromptManifest = prompt
	m.Digest = ""
	m.PromptManifestHash = ""
	m.Digest = manifestDigest(m)
	m.PromptManifestHash = promptManifestHash(m)
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

type CompressionRecord struct {
	SourceBlockIDs []string `json:"source_block_ids"`
	Algorithm      string   `json:"algorithm"`
	Version        string   `json:"version"`
	SourceHash     string   `json:"source_hash"`
	SummaryHash    string   `json:"summary_hash,omitempty"`
	TargetTokens   int64    `json:"target_tokens"`
	RetainedFacts  []string `json:"retained_facts,omitempty"`
	DroppedFacts   []string `json:"dropped_facts,omitempty"`
	QualityScore   float64  `json:"quality_score,omitempty"`
	FallbackReason string   `json:"fallback_reason,omitempty"`
	DroppedReason  string   `json:"dropped_reason,omitempty"`
	OverflowReason string   `json:"overflow_reason,omitempty"`
	ReplayKey      string   `json:"replay_key"`
}

type SummaryRequest struct {
	Blocks       []Block
	TargetTokens int64
}

type SummaryResult struct {
	Content       string
	RetainedFacts []string
	DroppedFacts  []string
	QualityScore  float64
}

type Summarizer interface {
	Summarize(SummaryRequest) (SummaryResult, error)
}

type SummarizerFunc func(SummaryRequest) (SummaryResult, error)

func (f SummarizerFunc) Summarize(request SummaryRequest) (SummaryResult, error) {
	return f(request)
}

// ExtractiveSummarizer is the local, deterministic semantic fallback. It
// prioritizes constraints, decisions, failures, and recent facts while
// retaining exact source text so the result is auditable without a model.
type ExtractiveSummarizer struct{}

type summaryFact struct {
	ID       string
	Text     string
	Weight   int
	Position int
}

func (ExtractiveSummarizer) Summarize(request SummaryRequest) (SummaryResult, error) {
	if request.TargetTokens < 1 {
		return SummaryResult{}, errors.New("summary target token budget is required")
	}
	facts := make([]summaryFact, 0)
	position := 0
	for _, block := range request.Blocks {
		for _, text := range semanticFacts(block.Content) {
			weight := 1
			lower := strings.ToLower(text)
			for _, keyword := range []string{"must", "required", "requirement", "constraint", "decision", "invariant", "failure", "failed", "error", "bug", "security", "acceptance"} {
				if strings.Contains(lower, keyword) {
					weight += 3
				}
			}
			if block.Mandatory {
				weight += 8
			}
			facts = append(facts, summaryFact{ID: factDigest(block.ID, text), Text: text, Weight: weight, Position: position})
			position++
		}
	}
	if len(facts) == 0 {
		return SummaryResult{}, errors.New("no semantic facts available")
	}
	ordered := append([]summaryFact(nil), facts...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Weight == ordered[j].Weight {
			return ordered[i].Position > ordered[j].Position
		}
		return ordered[i].Weight > ordered[j].Weight
	})
	selected := make([]summaryFact, 0)
	var used int64
	for _, fact := range ordered {
		cost := tokenEstimate(fact.Text + "\n")
		if cost < 1 || used+cost > request.TargetTokens {
			continue
		}
		selected = append(selected, fact)
		used += cost
	}
	if len(selected) == 0 {
		return SummaryResult{}, ErrOverflow
	}
	sort.SliceStable(selected, func(i, j int) bool { return selected[i].Position < selected[j].Position })
	retainedSet := map[string]struct{}{}
	retained := make([]string, 0, len(selected))
	lines := make([]string, 0, len(selected))
	retainedWeight := 0
	totalWeight := 0
	for _, fact := range facts {
		totalWeight += fact.Weight
	}
	for _, fact := range selected {
		retainedSet[fact.ID] = struct{}{}
		retained = append(retained, fact.ID)
		lines = append(lines, fact.Text)
		retainedWeight += fact.Weight
	}
	dropped := make([]string, 0, len(facts)-len(selected))
	for _, fact := range facts {
		if _, ok := retainedSet[fact.ID]; !ok {
			dropped = append(dropped, fact.ID)
		}
	}
	quality := float64(retainedWeight) / float64(totalWeight)
	return SummaryResult{Content: strings.Join(lines, "\n"), RetainedFacts: retained, DroppedFacts: dropped, QualityScore: quality}, nil
}

var ErrOverflow = errors.New("context token budget exceeded")

func HashBlock(b Block) string {
	h := sha256.Sum256([]byte(b.Content))
	return hex.EncodeToString(h[:])
}
func NewManifest(session string, version, budget int64, blocks []Block) (Manifest, error) {
	if strings.TrimSpace(session) == "" || version < 1 || budget < 1 {
		return Manifest{}, errors.New("session, positive version and token budget are required")
	}
	cp := append([]Block(nil), blocks...)
	if cp == nil {
		cp = []Block{}
	}
	sort.SliceStable(cp, func(i, j int) bool { return cp[i].ID < cp[j].ID })
	var total int64
	required := make([]string, 0)
	for i := range cp {
		if cp[i].ID == "" || cp[i].Source == "" || cp[i].Policy == "" || cp[i].Trust == "" || cp[i].SelectionReason == "" || cp[i].TokenEstimate < 1 {
			return Manifest{}, fmt.Errorf("block %s is missing lineage metadata", cp[i].ID)
		}
		if cp[i].Hash == "" {
			cp[i].Hash = HashBlock(cp[i])
		}
		total += cp[i].TokenEstimate
		if cp[i].Mandatory {
			required = append(required, cp[i].ID)
		}
	}
	if total > budget {
		return Manifest{}, ErrOverflow
	}
	prompt, err := BuildPromptManifest(cp)
	if err != nil {
		return Manifest{}, err
	}
	m := Manifest{SessionID: session, Version: version, SemanticSnapshotVersion: version, TokenBudget: budget, TokenEstimate: total, Blocks: cp, RequiredBlockIDs: required, CompilerVersion: CompilerVersion, TokenizerID: TokenizerID, PromptManifest: prompt, CreatedAt: time.Now().UTC()}
	m.Digest = manifestDigest(m)
	m.PromptManifestHash = promptManifestHash(m)
	return m, nil
}
func (m Manifest) Validate() error {
	if m.SessionID == "" || m.Digest == "" || m.Version < 1 || m.SemanticSnapshotVersion < 1 || m.TokenBudget < 1 || m.TokenEstimate < 0 || m.TokenEstimate > m.TokenBudget {
		return errors.New("invalid context manifest")
	}
	if m.CompilerVersion == "" || m.TokenizerID == "" {
		return errors.New("context compiler metadata is required")
	}
	if err := m.PromptManifest.Validate(); err != nil {
		return fmt.Errorf("prompt manifest: %w", err)
	}
	if err := validatePromptManifestBindings(m); err != nil {
		return err
	}
	if len(m.OmittedRequiredIDs) > 0 {
		return errors.New("context manifest omits required blocks")
	}
	required := make(map[string]struct{}, len(m.RequiredBlockIDs))
	seenBlocks := make(map[string]struct{}, len(m.Blocks))
	var total int64
	for _, b := range m.Blocks {
		if b.ID == "" {
			return errors.New("context block id is required")
		}
		if _, exists := seenBlocks[b.ID]; exists {
			return fmt.Errorf("duplicate context block %s", b.ID)
		}
		seenBlocks[b.ID] = struct{}{}
		if b.Hash != HashBlock(b) {
			return fmt.Errorf("block %s hash mismatch", b.ID)
		}
		if b.Mandatory {
			required[b.ID] = struct{}{}
		}
		total += b.TokenEstimate
	}
	if total != m.TokenEstimate {
		return fmt.Errorf("context token estimate mismatch: got %d want %d", m.TokenEstimate, total)
	}
	seenRequired := make(map[string]struct{}, len(m.RequiredBlockIDs))
	for _, id := range m.RequiredBlockIDs {
		if _, exists := seenRequired[id]; exists {
			return fmt.Errorf("duplicate required context block %s", id)
		}
		seenRequired[id] = struct{}{}
		if _, ok := required[id]; !ok {
			return fmt.Errorf("required context block %s is missing", id)
		}
	}
	if len(seenRequired) != len(required) {
		return errors.New("required context block set is incomplete")
	}
	if manifestDigest(m) != m.Digest {
		return errors.New("context manifest digest mismatch")
	}
	if m.PromptManifestHash == "" || m.PromptManifestHash != promptManifestHash(m) {
		return errors.New("context prompt manifest hash mismatch")
	}
	return nil
}

// validatePromptManifestBindings makes the typed prompt layer a projection of
// the exact selected blocks, rather than a second independently editable list.
// Without this check a caller could preserve a valid manifest digest while
// omitting the latest objective from PromptManifest and still dispatch it.
func validatePromptManifestBindings(m Manifest) error {
	if len(m.PromptManifest.Segments) != len(m.Blocks) {
		return fmt.Errorf("prompt manifest block coverage mismatch: segments=%d blocks=%d", len(m.PromptManifest.Segments), len(m.Blocks))
	}
	blocks := make(map[string]Block, len(m.Blocks))
	for _, block := range m.Blocks {
		blocks[block.ID] = block
	}
	for _, segment := range m.PromptManifest.Segments {
		block, ok := blocks[segment.ID]
		if !ok {
			return fmt.Errorf("prompt manifest references unknown block %s", segment.ID)
		}
		if segment.Hash != block.Hash || segment.Source != block.Source || segment.Content != block.Content || segment.Mandatory != block.Mandatory || segment.TokenBudget != block.TokenEstimate {
			return fmt.Errorf("prompt manifest binding mismatch for block %s", segment.ID)
		}
		kind := strings.TrimSpace(block.Metadata["prompt_kind"])
		if kind == "" {
			kind = promptKindForBlock(block)
		}
		if segment.Kind != kind {
			return fmt.Errorf("prompt manifest kind mismatch for block %s", segment.ID)
		}
	}
	return nil
}
func (m Manifest) Envelope() (Envelope, error) {
	if err := m.Validate(); err != nil {
		return Envelope{}, err
	}
	b, _ := json.Marshal(struct {
		Digest string  `json:"digest"`
		Blocks []Block `json:"blocks"`
	}{m.Digest, m.Blocks})
	h := sha256.Sum256(b)
	sel := hex.EncodeToString(h[:])
	return Envelope{Manifest: m, SelectionDigest: sel, ReplayKey: fmt.Sprintf("%s:%d:%s", m.SessionID, m.Version, sel)}, nil
}
func manifestDigest(m Manifest) string {
	cp := m
	cp.Digest = ""
	cp.PromptManifestHash = ""
	// Compression records explain how optional context was reduced; they are
	// derived evidence keyed by SourceHash and must not perturb the immutable
	// block-selection digest (or its replay key).
	cp.CompressionRecords = nil
	cp.PromptManifest.Digest = ""
	cp.CreatedAt = time.Time{}
	b, _ := json.Marshal(cp)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func promptManifestHash(m Manifest) string {
	data, _ := json.Marshal(struct {
		ManifestDigest string         `json:"manifest_digest"`
		Prompt         PromptManifest `json:"prompt"`
	}{m.Digest, m.PromptManifest})
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// Compile deterministically keeps mandatory blocks first and then selects
// optional blocks by ID. It never truncates a mandatory/system block.
func Compile(session string, version, budget int64, blocks []Block) (Manifest, CompressionRecord, error) {
	return CompileWithSummarizer(session, version, budget, blocks, ExtractiveSummarizer{})
}

// RenderManifest is the canonical textual adapter for callers that can carry
// the typed prompt contract. Legacy wire formats may still use the raw block
// view from their adapter, but they must obtain those blocks from the same
// validated Manifest.
func RenderManifest(manifest Manifest) (string, error) {
	if err := manifest.Validate(); err != nil {
		return "", err
	}
	return RenderPromptManifest(manifest.PromptManifest)
}

func CompileWithSummarizer(session string, version, budget int64, blocks []Block, summarizer Summarizer) (Manifest, CompressionRecord, error) {
	m, err := NewManifest(session, version, budget, blocks)
	if err == nil {
		return m, CompressionRecord{}, nil
	}
	if !errors.Is(err, ErrOverflow) {
		return Manifest{}, CompressionRecord{}, err
	}
	// A tool transaction is one atomic selection unit. If a transaction member
	// is mandatory, promote its paired records as mandatory too; otherwise the
	// compiler could retain a call while silently dropping its result (or vice
	// versa) under pressure.
	mandatoryTransactions := map[string]bool{}
	for _, b := range blocks {
		if key := atomicGroupKey(b); key != "" && b.Mandatory {
			mandatoryTransactions[key] = true
		}
	}
	mandatory := []Block{}
	optional := []Block{}
	atomicOptional := []Block{}
	semanticOptional := []Block{}
	for _, b := range blocks {
		if key := atomicGroupKey(b); key != "" && mandatoryTransactions[key] {
			b.Mandatory = true
		}
		if b.Mandatory {
			mandatory = append(mandatory, b)
		} else {
			optional = append(optional, b)
			if blockIsAtomic(b) {
				atomicOptional = append(atomicOptional, b)
			} else {
				semanticOptional = append(semanticOptional, b)
			}
		}
	}
	sort.Slice(optional, func(i, j int) bool { return optional[i].ID < optional[j].ID })
	sort.Slice(atomicOptional, func(i, j int) bool { return atomicOptional[i].ID < atomicOptional[j].ID })
	sort.Slice(semanticOptional, func(i, j int) bool { return semanticOptional[i].ID < semanticOptional[j].ID })
	var total int64
	for _, b := range mandatory {
		total += b.TokenEstimate
	}
	if total > budget {
		return Manifest{}, CompressionRecord{SourceBlockIDs: blockIDs(blocks), Algorithm: "preserve-mandatory", Version: "v1", SourceHash: blockSetHash(blocks), TargetTokens: budget, OverflowReason: "mandatory_blocks_exceed_budget"}, ErrOverflow
	}
	selected := append([]Block(nil), mandatory...)
	sourceHash := blockSetHash(blocks)
	record := CompressionRecord{SourceBlockIDs: blockIDs(blocks), Algorithm: "semantic-extractive", Version: "v1", SourceHash: sourceHash}
	retainedBlocks := map[string]struct{}{}
	droppedBlocks := map[string]struct{}{}
	for _, group := range atomicBlockGroups(atomicOptional) {
		if total+group.tokens > budget {
			for _, b := range group.blocks {
				droppedBlocks[b.ID] = struct{}{}
			}
			continue
		}
		selected = append(selected, group.blocks...)
		total += group.tokens
		for _, b := range group.blocks {
			retainedBlocks[b.ID] = struct{}{}
		}
	}
	remaining := budget - total
	if summarizer == nil {
		summarizer = ExtractiveSummarizer{}
	}
	if remaining > 0 && len(semanticOptional) > 0 {
		summary, summaryErr := summarizer.Summarize(SummaryRequest{Blocks: semanticOptional, TargetTokens: remaining})
		if summaryErr == nil && strings.TrimSpace(summary.Content) != "" && summary.QualityScore >= 0.60 {
			summaryBlock := Block{ID: "summary:" + sourceHash[:16], Kind: "summary", Source: "compression:" + sourceHash, Content: strings.TrimSpace(summary.Content), Policy: "summarize", Trust: "derived", SelectionReason: "semantic_compaction", TokenEstimate: tokenEstimate(summary.Content), Metadata: map[string]string{"source_hash": sourceHash, "algorithm": "semantic-extractive", "version": "v1"}}
			if summaryBlock.TokenEstimate > 0 && summaryBlock.TokenEstimate <= remaining {
				summaryBlock.Hash = HashBlock(summaryBlock)
				selected = append(selected, summaryBlock)
				total += summaryBlock.TokenEstimate
				record.SummaryHash = summaryBlock.Hash
				record.RetainedFacts = append([]string(nil), summary.RetainedFacts...)
				record.DroppedFacts = append([]string(nil), summary.DroppedFacts...)
				record.QualityScore = summary.QualityScore
			} else {
				record.FallbackReason = "summary_exceeds_budget"
			}
		} else if summaryErr != nil {
			record.FallbackReason = "summarizer_failed: " + summaryErr.Error()
		} else {
			record.FallbackReason = "summary_quality_below_threshold"
		}
	}
	if record.SummaryHash == "" {
		record.Algorithm = "deterministic-selection"
		record.Version = "v2"
		for _, b := range semanticOptional {
			if total+b.TokenEstimate > budget {
				droppedBlocks[b.ID] = struct{}{}
				continue
			}
			selected = append(selected, b)
			total += b.TokenEstimate
			retainedBlocks[b.ID] = struct{}{}
		}
		if len(optional) > 0 {
			record.QualityScore = float64(len(retainedBlocks)) / float64(len(optional))
		}
	}
	for _, b := range optional {
		if _, ok := retainedBlocks[b.ID]; ok {
			record.RetainedFacts = append(record.RetainedFacts, "block:"+b.ID)
			continue
		}
		if _, ok := droppedBlocks[b.ID]; !ok {
			droppedBlocks[b.ID] = struct{}{}
		}
		record.DroppedFacts = append(record.DroppedFacts, "block:"+b.ID)
	}
	m, err = NewManifest(session, version, budget, selected)
	if err != nil {
		record.OverflowReason = "compression_failed"
		return Manifest{}, record, err
	}
	record.TargetTokens = total
	record.ReplayKey = m.Digest
	if record.Algorithm != "" {
		m.CompressionRecords = []CompressionRecord{record}
		m.Digest = manifestDigest(m)
		m.PromptManifestHash = promptManifestHash(m)
	}
	return m, record, nil
}

// atomicGroupKey identifies records that must be selected or dropped as one
// unit. Tool call/result records share a call ID; other atomic blocks stand on
// their own so code, JSON, and artifact payloads are never rune-truncated.
func atomicGroupKey(block Block) string {
	if !blockIsAtomic(block) {
		return ""
	}
	if callID := strings.TrimSpace(block.Metadata["tool_call_id"]); callID != "" {
		return "tool:" + callID
	}
	return "block:" + block.ID
}

type atomicBlockGroup struct {
	key    string
	blocks []Block
	tokens int64
}

func atomicBlockGroups(blocks []Block) []atomicBlockGroup {
	groupsByKey := make(map[string]*atomicBlockGroup, len(blocks))
	for _, block := range blocks {
		key := atomicGroupKey(block)
		group := groupsByKey[key]
		if group == nil {
			group = &atomicBlockGroup{key: key}
			groupsByKey[key] = group
		}
		group.blocks = append(group.blocks, block)
		group.tokens += block.TokenEstimate
	}
	groups := make([]atomicBlockGroup, 0, len(groupsByKey))
	for _, group := range groupsByKey {
		sort.SliceStable(group.blocks, func(i, j int) bool { return group.blocks[i].ID < group.blocks[j].ID })
		groups = append(groups, *group)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].key < groups[j].key })
	return groups
}

// blockIsAtomic marks content whose syntax or transaction boundary must be
// preserved as a whole. Such blocks may be selected or dropped, but are never
// passed through the extractive summarizer and never rune-truncated.
func blockIsAtomic(block Block) bool {
	if strings.EqualFold(strings.TrimSpace(block.Metadata["atomic"]), "true") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(block.Kind)) {
	case "code", "json", "tool", "tool_call", "tool_result", "transaction":
		return true
	default:
		return strings.TrimSpace(block.Metadata["tool_call_id"]) != ""
	}
}

func semanticFacts(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	replacer := strings.NewReplacer(". ", ".\n", "! ", "!\n", "? ", "?\n", "; ", ";\n")
	parts := strings.Split(replacer.Replace(content), "\n")
	facts := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		facts = append(facts, part)
	}
	return facts
}

func tokenEstimate(content string) int64 {
	runes := len([]rune(strings.TrimSpace(content)))
	if runes == 0 {
		return 0
	}
	return int64((runes + 3) / 4)
}

// EstimateTokens is the single tokenizer used while adapting durable session
// records into context.Blocks. It is intentionally conservative and stable so
// a replay does not change selection merely because the caller package used a
// different approximation.
func EstimateTokens(content string) int64 {
	return tokenEstimate(content)
}

func factDigest(blockID, fact string) string {
	h := sha256.Sum256([]byte(blockID + "\x00" + fact))
	return "fact:" + hex.EncodeToString(h[:])
}

func blockIDs(blocks []Block) []string {
	ids := make([]string, 0, len(blocks))
	for _, block := range blocks {
		ids = append(ids, block.ID)
	}
	sort.Strings(ids)
	return ids
}

func blockSetHash(blocks []Block) string {
	type sourceBlock struct {
		ID   string `json:"id"`
		Hash string `json:"hash"`
	}
	items := make([]sourceBlock, 0, len(blocks))
	for _, block := range blocks {
		hash := block.Hash
		if hash == "" {
			hash = HashBlock(block)
		}
		items = append(items, sourceBlock{ID: block.ID, Hash: hash})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	data, _ := json.Marshal(items)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
