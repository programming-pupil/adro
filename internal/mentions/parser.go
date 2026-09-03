// Package mentions parses the structured mention URI used by comments. It is
// intentionally independent from roster lookup so preview and create can use
// exactly the same AST and source digest.
package mentions

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type TargetType string

const (
	TargetAgent TargetType = "agent"
	TargetSquad TargetType = "squad"
	// TargetMember and TargetIssue are render-only references. They are part of
	// the Multica mention grammar but must never become execution targets.
	TargetMember TargetType = "member"
	TargetIssue  TargetType = "issue"
	TargetAll    TargetType = "all"
)

type Mention struct {
	TargetType    TargetType `json:"target_type"`
	TargetID      string     `json:"target_id"`
	DisplayText   string     `json:"display_text"`
	Start         int        `json:"start"`
	End           int        `json:"end"`
	ParserVersion string     `json:"parser_version"`
	SourceHash    string     `json:"source_hash"`
}
type ParseResult struct {
	Mentions      []Mention `json:"mentions"`
	ParserVersion string    `json:"parser_version"`
	SourceHash    string    `json:"source_hash"`
}

var mentionPattern = regexp.MustCompile(`\[([^\]]*)\]\(mention://(agent|squad|member|issue|all)/([^\)]+)\)`)

const ParserVersion = "mention-uri-v1"

func Parse(content string) (ParseResult, error) {
	h := sha256.Sum256([]byte(content))
	result := ParseResult{ParserVersion: ParserVersion, SourceHash: hex.EncodeToString(h[:]), Mentions: []Mention{}}
	matches := mentionPattern.FindAllStringSubmatchIndex(content, -1)
	for _, m := range matches {
		display := content[m[2]:m[3]]
		kind := TargetType(content[m[4]:m[5]])
		id := content[m[6]:m[7]]
		if err := validate(kind, id); err != nil {
			return ParseResult{}, fmt.Errorf("mention at %d: %w", m[0], err)
		}
		result.Mentions = append(result.Mentions, Mention{TargetType: kind, TargetID: id, DisplayText: display, Start: m[0], End: m[1], ParserVersion: ParserVersion, SourceHash: result.SourceHash})
	}
	// A valid mention must account for every structured URI marker. Without
	// this check a comment containing one valid URI followed by malformed
	// `mention://...` text would silently route only the valid target.
	if strings.Contains(content, "mention://") {
		covered := make([]bool, len(content))
		for _, m := range matches {
			for i := m[0]; i < m[1] && i < len(covered); i++ {
				covered[i] = true
			}
		}
		for start := 0; start < len(content); {
			relative := strings.Index(content[start:], "mention://")
			if relative < 0 {
				break
			}
			index := start + relative
			if !covered[index] {
				return ParseResult{}, errors.New("invalid structured mention syntax")
			}
			start = index + len("mention://")
		}
	}
	return result, nil
}
func validate(kind TargetType, id string) error {
	if kind == TargetAll {
		if id != "all" {
			return errors.New("all mention must use all/all")
		}
		return nil
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("target id is required")
	}
	if strings.ContainsAny(id, "/?# ") {
		return errors.New("target id contains invalid characters")
	}
	return nil
}
func (p ParseResult) Targets() []Mention {
	seen := map[string]bool{}
	out := make([]Mention, 0, len(p.Mentions))
	for _, m := range p.Mentions {
		k := string(m.TargetType) + ":" + m.TargetID
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, m)
	}
	return out
}

// InvocationTargets returns only mentions that are allowed to create work.
// Member/issue references intentionally remain available in Targets for UI
// rendering and audit, while this view prevents accidental dispatch.
func (p ParseResult) InvocationTargets() []Mention {
	out := make([]Mention, 0, len(p.Mentions))
	for _, mention := range p.Targets() {
		if mention.TargetType == TargetAgent || mention.TargetType == TargetSquad || mention.TargetType == TargetAll {
			out = append(out, mention)
		}
	}
	return out
}
