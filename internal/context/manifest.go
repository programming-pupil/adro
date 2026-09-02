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
	TargetTokens   int64    `json:"target_tokens"`
	DroppedReason  string   `json:"dropped_reason,omitempty"`
	OverflowReason string   `json:"overflow_reason,omitempty"`
	ReplayKey      string   `json:"replay_key"`
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
	m.PromptManifestHash = m.Digest
	return m, nil
}
func (m Manifest) Validate() error {
	if m.SessionID == "" || m.Digest == "" || m.TokenBudget < 1 || m.TokenEstimate < 0 || m.TokenEstimate > m.TokenBudget {
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

// Compile deterministically keeps mandatory blocks first and then selects
// optional blocks by ID. It never truncates a mandatory/system block.
func Compile(session string, version, budget int64, blocks []Block) (Manifest, CompressionRecord, error) {
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
		return Manifest{}, CompressionRecord{OverflowReason: "mandatory_blocks_exceed_budget"}, ErrOverflow
	}
	selected := append([]Block(nil), mandatory...)
	for _, b := range optional {
		if total+b.TokenEstimate > budget {
			continue
		}
		selected = append(selected, b)
		total += b.TokenEstimate
	}
	m, err = NewManifest(session, version, budget, selected)
	if err != nil {
		return Manifest{}, CompressionRecord{OverflowReason: "deterministic_selection_failed"}, err
	}
	source := make([]string, 0, len(blocks))
	for _, b := range blocks {
		source = append(source, b.ID)
	}
	sort.Strings(source)
	record := CompressionRecord{SourceBlockIDs: source, Algorithm: "deterministic-selection", Version: "v1", TargetTokens: total, ReplayKey: m.Digest}
	return m, record, nil
}
