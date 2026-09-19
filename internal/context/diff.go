package context

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// DiffKind describes the change observed for one context block. The diff never
// contains block content; callers can inspect the corresponding manifest when
// policy permits, while the diagnostic remains safe to persist and export.
type DiffKind string

const (
	DiffAdded   DiffKind = "added"
	DiffRemoved DiffKind = "removed"
	DiffChanged DiffKind = "changed"
)

type BlockDiff struct {
	ID            string   `json:"id"`
	Kind          DiffKind `json:"kind"`
	BeforeHash    string   `json:"before_hash,omitempty"`
	AfterHash     string   `json:"after_hash,omitempty"`
	BeforeTokens  int64    `json:"before_tokens,omitempty"`
	AfterTokens   int64    `json:"after_tokens,omitempty"`
	Reasons       []string `json:"reasons,omitempty"`
	BeforeMinimal bool     `json:"before_mandatory,omitempty"`
	AfterMinimal  bool     `json:"after_mandatory,omitempty"`
}

type ManifestDiff struct {
	SessionID      string      `json:"session_id"`
	FromVersion    int64       `json:"from_version"`
	ToVersion      int64       `json:"to_version"`
	FromDigest     string      `json:"from_digest"`
	ToDigest       string      `json:"to_digest"`
	TokenBefore    int64       `json:"token_before"`
	TokenAfter     int64       `json:"token_after"`
	TokenDelta     int64       `json:"token_delta"`
	Added          []BlockDiff `json:"added,omitempty"`
	Removed        []BlockDiff `json:"removed,omitempty"`
	Changed        []BlockDiff `json:"changed,omitempty"`
	MandatoryAdded []string    `json:"mandatory_added,omitempty"`
	MandatoryGone  []string    `json:"mandatory_removed,omitempty"`
	Digest         string      `json:"digest"`
}

// Diff compares two validated manifests. A context diff is a projection of
// immutable manifests, so it is deterministic and can be regenerated from
// sequence zero during replay.
func Diff(previous, current Manifest) (ManifestDiff, error) {
	if err := previous.Validate(); err != nil {
		return ManifestDiff{}, fmt.Errorf("previous manifest: %w", err)
	}
	if err := current.Validate(); err != nil {
		return ManifestDiff{}, fmt.Errorf("current manifest: %w", err)
	}
	if previous.SessionID != current.SessionID {
		return ManifestDiff{}, errors.New("context diff cannot cross sessions")
	}
	if current.Version < previous.Version {
		return ManifestDiff{}, errors.New("context diff version moved backwards")
	}

	before := make(map[string]Block, len(previous.Blocks))
	after := make(map[string]Block, len(current.Blocks))
	for _, block := range previous.Blocks {
		before[block.ID] = block
	}
	for _, block := range current.Blocks {
		after[block.ID] = block
	}
	ids := make([]string, 0, len(before)+len(after))
	seen := make(map[string]struct{}, len(before)+len(after))
	for id := range before {
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for id := range after {
		if _, ok := seen[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	diff := ManifestDiff{
		SessionID: previous.SessionID, FromVersion: previous.Version, ToVersion: current.Version,
		FromDigest: previous.Digest, ToDigest: current.Digest,
		TokenBefore: previous.TokenEstimate, TokenAfter: current.TokenEstimate,
	}
	diff.TokenDelta = diff.TokenAfter - diff.TokenBefore
	for _, id := range ids {
		oldBlock, hadOld := before[id]
		newBlock, hadNew := after[id]
		switch {
		case !hadOld:
			diff.Added = append(diff.Added, blockDiff(DiffAdded, Block{}, newBlock))
			if newBlock.Mandatory {
				diff.MandatoryAdded = append(diff.MandatoryAdded, id)
			}
		case !hadNew:
			diff.Removed = append(diff.Removed, blockDiff(DiffRemoved, oldBlock, Block{}))
			if oldBlock.Mandatory {
				diff.MandatoryGone = append(diff.MandatoryGone, id)
			}
		case blockFingerprint(oldBlock) != blockFingerprint(newBlock):
			diff.Changed = append(diff.Changed, blockDiff(DiffChanged, oldBlock, newBlock))
			if oldBlock.Mandatory != newBlock.Mandatory {
				if newBlock.Mandatory {
					diff.MandatoryAdded = append(diff.MandatoryAdded, id)
				} else {
					diff.MandatoryGone = append(diff.MandatoryGone, id)
				}
			}
		}
	}
	// The loops above already visit sorted IDs. Keep explicit sorting on the
	// aggregate fields so a caller constructing equivalent manifests with a
	// different block order receives the same digest.
	sort.Strings(diff.MandatoryAdded)
	sort.Strings(diff.MandatoryGone)
	diff.Digest = manifestDiffDigest(diff)
	return diff, nil
}

func (m Manifest) Diff(previous Manifest) (ManifestDiff, error) { return Diff(previous, m) }

func (d ManifestDiff) Validate() error {
	if strings.TrimSpace(d.SessionID) == "" || d.FromVersion < 1 || d.ToVersion < d.FromVersion || d.FromDigest == "" || d.ToDigest == "" || d.Digest == "" {
		return errors.New("invalid context diff metadata")
	}
	if d.TokenBefore < 0 || d.TokenAfter < 0 || d.TokenDelta != d.TokenAfter-d.TokenBefore {
		return errors.New("invalid context diff token accounting")
	}
	seen := map[string]DiffKind{}
	for _, list := range [][]BlockDiff{d.Added, d.Removed, d.Changed} {
		for _, item := range list {
			if strings.TrimSpace(item.ID) == "" {
				return errors.New("context diff block id is required")
			}
			if prior, ok := seen[item.ID]; ok {
				return fmt.Errorf("context diff block %s appears as %s and %s", item.ID, prior, item.Kind)
			}
			seen[item.ID] = item.Kind
			switch item.Kind {
			case DiffAdded:
				if item.AfterHash == "" || item.BeforeHash != "" || item.AfterTokens < 1 {
					return fmt.Errorf("invalid added block diff %s", item.ID)
				}
			case DiffRemoved:
				if item.BeforeHash == "" || item.AfterHash != "" || item.BeforeTokens < 1 {
					return fmt.Errorf("invalid removed block diff %s", item.ID)
				}
			case DiffChanged:
				if item.BeforeHash == "" || item.AfterHash == "" || item.BeforeTokens < 1 || item.AfterTokens < 1 || len(item.Reasons) == 0 {
					return fmt.Errorf("invalid changed block diff %s", item.ID)
				}
			default:
				return fmt.Errorf("unknown context diff kind %q", item.Kind)
			}
		}
	}
	if manifestDiffDigest(d) != d.Digest {
		return errors.New("context diff digest mismatch")
	}
	return nil
}

func blockDiff(kind DiffKind, before, after Block) BlockDiff {
	result := BlockDiff{ID: after.ID, Kind: kind}
	if result.ID == "" {
		result.ID = before.ID
	}
	if before.ID != "" {
		result.BeforeHash, result.BeforeTokens, result.BeforeMinimal = before.Hash, before.TokenEstimate, before.Mandatory
	}
	if after.ID != "" {
		result.AfterHash, result.AfterTokens, result.AfterMinimal = after.Hash, after.TokenEstimate, after.Mandatory
	}
	if kind == DiffChanged {
		result.Reasons = blockChangeReasons(before, after)
	}
	return result
}

func blockChangeReasons(before, after Block) []string {
	reasons := make([]string, 0, 8)
	if before.Hash != after.Hash || before.Content != after.Content {
		reasons = append(reasons, "content")
	}
	if before.Source != after.Source || before.TenantScope != after.TenantScope || before.Purpose != after.Purpose || before.TrustLevel != after.TrustLevel || before.Sensitivity != after.Sensitivity || !equalTaintLabels(before.TaintLabels, after.TaintLabels) {
		reasons = append(reasons, "provenance")
	}
	if before.Policy != after.Policy || before.SelectionReason != after.SelectionReason {
		reasons = append(reasons, "selection")
	}
	if before.TokenEstimate != after.TokenEstimate {
		reasons = append(reasons, "token_estimate")
	}
	if before.Mandatory != after.Mandatory {
		reasons = append(reasons, "mandatory")
	}
	if before.Kind != after.Kind || !equalStringMap(before.Metadata, after.Metadata) {
		reasons = append(reasons, "metadata")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "encoding")
	}
	return reasons
}

func equalStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func blockFingerprint(block Block) string {
	data, _ := json.Marshal(block)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func manifestDiffDigest(diff ManifestDiff) string {
	copy := diff
	copy.Digest = ""
	data, _ := json.Marshal(copy)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
