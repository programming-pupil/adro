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

// ArchiveRef identifies the immutable source window retained for a compaction.
// It is deliberately a reference and digest, never an inline copy of the
// source content.
type ArchiveRef struct {
	URI       string `json:"uri"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type,omitempty"`
}

type RecallProbe struct {
	RequiredFacts []string `json:"required_facts"`
	FoundFacts    []string `json:"found_facts"`
	MissingFacts  []string `json:"missing_facts,omitempty"`
	Coverage      float64  `json:"coverage"`
	Digest        string   `json:"digest"`
	Verified      bool     `json:"verified"`
}

// CompactionLineage connects a compacted manifest to the immutable source
// window, archive, summary and recall evidence. It can be persisted beside a
// CompressionRecord or embedded in an evidence bundle.
type CompactionLineage struct {
	Version              string      `json:"version"`
	SessionID            string      `json:"session_id"`
	ParentManifestDigest string      `json:"parent_manifest_digest"`
	CompactedDigest      string      `json:"compacted_digest"`
	SourceWindowDigest   string      `json:"source_window_digest"`
	SummaryDigest        string      `json:"summary_digest,omitempty"`
	Archive              ArchiveRef  `json:"archive"`
	SourceBlockIDs       []string    `json:"source_block_ids"`
	SourceTokens         int64       `json:"source_tokens"`
	SummaryTokens        int64       `json:"summary_tokens"`
	Coverage             float64     `json:"coverage"`
	QualityScore         float64     `json:"quality_score"`
	Recall               RecallProbe `json:"recall"`
	ReplayKey            string      `json:"replay_key"`
	EvidenceDigest       string      `json:"evidence_digest"`
}

const CompactionLineageVersion = "compaction-lineage-v1"

// RequiredFacts returns deterministic mandatory facts that must survive a
// compaction. Callers can add domain-specific decisions and constraints.
func RequiredFacts(manifest Manifest) ([]string, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	facts := make([]string, 0)
	for _, block := range manifest.Blocks {
		if !block.Mandatory {
			continue
		}
		for _, fact := range semanticFacts(block.Content) {
			fact = strings.TrimSpace(fact)
			if fact != "" {
				facts = append(facts, fact)
			}
		}
	}
	return uniqueSortedStrings(facts), nil
}

// VerifyRecall checks that required facts remain observable in the compacted
// manifest and reports missing facts without exposing source content in the
// report. It also enforces that mandatory blocks are not silently dropped.
func VerifyRecall(source, compacted Manifest, required []string) (RecallProbe, error) {
	if err := source.Validate(); err != nil {
		return RecallProbe{}, fmt.Errorf("source manifest: %w", err)
	}
	if err := compacted.Validate(); err != nil {
		return RecallProbe{}, fmt.Errorf("compacted manifest: %w", err)
	}
	if source.SessionID != compacted.SessionID || compacted.Version < source.Version {
		return RecallProbe{}, errors.New("compaction recall manifests are incompatible")
	}
	if len(required) == 0 {
		var err error
		required, err = RequiredFacts(source)
		if err != nil {
			return RecallProbe{}, err
		}
	}
	required = uniqueSortedStrings(required)
	joined := make([]string, 0, len(compacted.Blocks))
	for _, block := range compacted.Blocks {
		joined = append(joined, block.Content)
	}
	text := strings.ToLower(strings.Join(joined, "\n"))
	probe := RecallProbe{RequiredFacts: append([]string(nil), required...)}
	for _, fact := range required {
		if strings.Contains(text, strings.ToLower(fact)) {
			probe.FoundFacts = append(probe.FoundFacts, fact)
		} else {
			probe.MissingFacts = append(probe.MissingFacts, fact)
		}
	}
	if len(required) == 0 {
		probe.Coverage = 1
	} else {
		probe.Coverage = float64(len(probe.FoundFacts)) / float64(len(required))
	}
	probe.Verified = len(probe.MissingFacts) == 0
	probe.Digest = recallDigest(probe)
	for _, block := range source.Blocks {
		if !block.Mandatory {
			continue
		}
		found := false
		for _, candidate := range compacted.Blocks {
			if candidate.ID == block.ID && candidate.Hash == block.Hash && candidate.Content == block.Content {
				found = true
				break
			}
		}
		if !found {
			probe.Verified = false
			probe.MissingFacts = appendUnique(probe.MissingFacts, "mandatory_block:"+block.ID)
		}
	}
	probe.MissingFacts = uniqueSortedStrings(probe.MissingFacts)
	probe.FoundFacts = uniqueSortedStrings(probe.FoundFacts)
	probe.Digest = recallDigest(probe)
	return probe, nil
}

// BuildCompactionLineage creates and validates a replayable evidence record.
// The caller must provide an archive reference before it can be considered
// complete; retaining only an in-memory summary is intentionally rejected.
func BuildCompactionLineage(source, compacted Manifest, record CompressionRecord, archive ArchiveRef, requiredFacts []string) (CompactionLineage, error) {
	if err := source.Validate(); err != nil {
		return CompactionLineage{}, fmt.Errorf("source manifest: %w", err)
	}
	if err := compacted.Validate(); err != nil {
		return CompactionLineage{}, fmt.Errorf("compacted manifest: %w", err)
	}
	if source.SessionID != compacted.SessionID || compacted.Version < source.Version {
		return CompactionLineage{}, errors.New("compaction lineage manifests are incompatible")
	}
	if strings.TrimSpace(archive.URI) == "" || strings.TrimSpace(archive.Digest) == "" || archive.Size < 0 {
		return CompactionLineage{}, errors.New("immutable archive reference is required")
	}
	probe, err := VerifyRecall(source, compacted, requiredFacts)
	if err != nil {
		return CompactionLineage{}, err
	}
	if !probe.Verified {
		return CompactionLineage{}, errors.New("compaction recall probe failed")
	}
	if compacted.TokenEstimate >= source.TokenEstimate {
		return CompactionLineage{}, errors.New("compaction did not reduce token estimate")
	}
	sourceIDs := make([]string, 0, len(source.Blocks))
	for _, block := range source.Blocks {
		sourceIDs = append(sourceIDs, block.ID)
	}
	lineage := CompactionLineage{
		Version: CompactionLineageVersion, SessionID: source.SessionID,
		ParentManifestDigest: source.Digest, CompactedDigest: compacted.Digest,
		SourceWindowDigest: blockSetHash(source.Blocks), SummaryDigest: record.SummaryHash,
		Archive: archive, SourceBlockIDs: uniqueSortedStrings(sourceIDs),
		SourceTokens: source.TokenEstimate, SummaryTokens: compacted.TokenEstimate,
		Coverage: probe.Coverage, QualityScore: record.QualityScore,
		Recall: probe, ReplayKey: compacted.Digest,
	}
	lineage.EvidenceDigest = lineageDigest(lineage)
	if err := lineage.Validate(); err != nil {
		return CompactionLineage{}, err
	}
	return lineage, nil
}

func (l CompactionLineage) Validate() error {
	if l.Version != CompactionLineageVersion || strings.TrimSpace(l.SessionID) == "" || l.ParentManifestDigest == "" || l.CompactedDigest == "" || l.SourceWindowDigest == "" || l.ReplayKey == "" || l.EvidenceDigest == "" {
		return errors.New("invalid compaction lineage metadata")
	}
	if strings.TrimSpace(l.Archive.URI) == "" || strings.TrimSpace(l.Archive.Digest) == "" || l.Archive.Size < 0 || len(l.SourceBlockIDs) == 0 || l.SourceTokens < 1 || l.SummaryTokens < 1 || l.SummaryTokens >= l.SourceTokens {
		return errors.New("invalid compaction lineage archive or token metadata")
	}
	if l.Coverage < 0 || l.Coverage > 1 || l.QualityScore < 0 || l.QualityScore > 1 || !l.Recall.Verified || l.Recall.Coverage < 0 || l.Recall.Coverage > 1 {
		return errors.New("invalid compaction lineage quality metadata")
	}
	if l.Recall.Digest == "" || len(l.Recall.MissingFacts) != 0 {
		return errors.New("compaction recall is not verified")
	}
	if lineageDigest(l) != l.EvidenceDigest {
		return errors.New("compaction lineage digest mismatch")
	}
	return nil
}

func uniqueSortedStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func recallDigest(probe RecallProbe) string {
	copy := probe
	copy.Digest = ""
	data, _ := json.Marshal(copy)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func lineageDigest(lineage CompactionLineage) string {
	copy := lineage
	copy.EvidenceDigest = ""
	data, _ := json.Marshal(copy)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func validateCompressionRecords(manifest Manifest) error {
	for index, record := range manifest.CompressionRecords {
		if strings.TrimSpace(record.Algorithm) == "" || strings.TrimSpace(record.Version) == "" || strings.TrimSpace(record.SourceHash) == "" || strings.TrimSpace(record.ReplayKey) == "" || record.TargetTokens < 1 {
			return fmt.Errorf("compression record %d has incomplete lineage", index)
		}
		if len(record.SourceBlockIDs) == 0 {
			return fmt.Errorf("compression record %d has no source window", index)
		}
		if record.QualityScore < 0 || record.QualityScore > 1 {
			return fmt.Errorf("compression record %d has invalid quality score", index)
		}
		if record.SummaryHash != "" && record.SummaryHash == record.SourceHash {
			return fmt.Errorf("compression record %d summary hash equals source window", index)
		}
	}
	return nil
}
