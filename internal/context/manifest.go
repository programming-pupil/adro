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
	SessionID               string    `json:"session_id"`
	Version                 int64     `json:"version"`
	SemanticSnapshotVersion int64     `json:"semantic_snapshot_version"`
	TokenBudget             int64     `json:"token_budget"`
	TokenEstimate           int64     `json:"token_estimate"`
	Blocks                  []Block   `json:"blocks"`
	Digest                  string    `json:"digest"`
	PromptManifestHash      string    `json:"prompt_manifest_hash"`
	ParentDigest            string    `json:"parent_digest,omitempty"`
	CreatedAt               time.Time `json:"created_at"`
}
type Envelope struct {
	Manifest        Manifest `json:"manifest"`
	SelectionDigest string   `json:"selection_digest"`
	ReplayKey       string   `json:"replay_key"`
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
	sort.SliceStable(cp, func(i, j int) bool { return cp[i].ID < cp[j].ID })
	var total int64
	for i := range cp {
		if cp[i].ID == "" || cp[i].Source == "" || cp[i].Policy == "" || cp[i].Trust == "" || cp[i].SelectionReason == "" || cp[i].TokenEstimate < 1 {
			return Manifest{}, fmt.Errorf("block %s is missing lineage metadata", cp[i].ID)
		}
		if cp[i].Hash == "" {
			cp[i].Hash = HashBlock(cp[i])
		}
		total += cp[i].TokenEstimate
	}
	if total > budget {
		return Manifest{}, ErrOverflow
	}
	m := Manifest{SessionID: session, Version: version, SemanticSnapshotVersion: version, TokenBudget: budget, TokenEstimate: total, Blocks: cp, CreatedAt: time.Now().UTC()}
	m.Digest = manifestDigest(m)
	m.PromptManifestHash = promptManifestHash(m)
	return m, nil
}
func (m Manifest) Validate() error {
	if m.SessionID == "" || m.Digest == "" || m.Version < 1 || m.SemanticSnapshotVersion < 1 || m.TokenBudget < 1 || m.TokenEstimate < 0 || m.TokenEstimate > m.TokenBudget {
		return errors.New("invalid context manifest")
	}
	for _, b := range m.Blocks {
		if b.Hash != HashBlock(b) {
			return fmt.Errorf("block %s hash mismatch", b.ID)
		}
	}
	if manifestDigest(m) != m.Digest {
		return errors.New("context manifest digest mismatch")
	}
	if m.PromptManifestHash == "" || m.PromptManifestHash != promptManifestHash(m) {
		return errors.New("context prompt manifest hash mismatch")
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
	cp.CreatedAt = time.Time{}
	b, _ := json.Marshal(cp)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func promptManifestHash(m Manifest) string {
	type promptBlock struct {
		ID      string `json:"id"`
		Hash    string `json:"hash"`
		Content string `json:"content"`
	}
	blocks := make([]promptBlock, 0, len(m.Blocks))
	for _, block := range m.Blocks {
		blocks = append(blocks, promptBlock{ID: block.ID, Hash: block.Hash, Content: block.Content})
	}
	data, _ := json.Marshal(struct {
		ManifestDigest string        `json:"manifest_digest"`
		Blocks         []promptBlock `json:"blocks"`
	}{m.Digest, blocks})
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// Compile deterministically keeps mandatory blocks first and then selects
// optional blocks by ID. It never truncates a mandatory/system block.
func Compile(session string, version, budget int64, blocks []Block) (Manifest, CompressionRecord, error) {
	return CompileWithSummarizer(session, version, budget, blocks, ExtractiveSummarizer{})
}

func CompileWithSummarizer(session string, version, budget int64, blocks []Block, summarizer Summarizer) (Manifest, CompressionRecord, error) {
	m, err := NewManifest(session, version, budget, blocks)
	if err == nil {
		return m, CompressionRecord{}, nil
	}
	if !errors.Is(err, ErrOverflow) {
		return Manifest{}, CompressionRecord{}, err
	}
	mandatory := []Block{}
	optional := []Block{}
	for _, b := range blocks {
		if b.Mandatory {
			mandatory = append(mandatory, b)
		} else {
			optional = append(optional, b)
		}
	}
	sort.Slice(optional, func(i, j int) bool { return optional[i].ID < optional[j].ID })
	var total int64
	for _, b := range mandatory {
		total += b.TokenEstimate
	}
	if total > budget {
		return Manifest{}, CompressionRecord{SourceBlockIDs: blockIDs(blocks), Algorithm: "preserve-mandatory", Version: "v1", SourceHash: blockSetHash(blocks), TargetTokens: budget, OverflowReason: "mandatory_blocks_exceed_budget"}, ErrOverflow
	}
	selected := append([]Block(nil), mandatory...)
	remaining := budget - total
	sourceHash := blockSetHash(blocks)
	record := CompressionRecord{SourceBlockIDs: blockIDs(blocks), Algorithm: "semantic-extractive", Version: "v1", SourceHash: sourceHash}
	if summarizer == nil {
		summarizer = ExtractiveSummarizer{}
	}
	if remaining > 0 && len(optional) > 0 {
		summary, summaryErr := summarizer.Summarize(SummaryRequest{Blocks: optional, TargetTokens: remaining})
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
		retained := map[string]struct{}{}
		for _, b := range optional {
			if total+b.TokenEstimate > budget {
				continue
			}
			selected = append(selected, b)
			total += b.TokenEstimate
			retained[b.ID] = struct{}{}
			record.RetainedFacts = append(record.RetainedFacts, "block:"+b.ID)
		}
		for _, b := range optional {
			if _, ok := retained[b.ID]; !ok {
				record.DroppedFacts = append(record.DroppedFacts, "block:"+b.ID)
			}
		}
		if len(optional) > 0 {
			record.QualityScore = float64(len(retained)) / float64(len(optional))
		}
	}
	m, err = NewManifest(session, version, budget, selected)
	if err != nil {
		record.OverflowReason = "compression_failed"
		return Manifest{}, record, err
	}
	record.TargetTokens = total
	record.ReplayKey = m.Digest
	return m, record, nil
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
