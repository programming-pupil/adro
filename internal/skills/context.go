package skills

import (
	"errors"
	"fmt"
	"strings"

	runtimecontext "github.com/adro-project/adro/internal/context"
	"github.com/adro-project/adro/internal/security"
)

// ContextBlock turns an active, immutable SkillBundle into a provenance-rich
// context block. It carries declarations and instructions only; capability
// grants and secret leases are resolved separately by policy.
func (b SkillBundle) ContextBlock(session, tenantScope, selectionReason string) (runtimecontext.Block, error) {
	if err := b.Validate(); err != nil {
		return runtimecontext.Block{}, err
	}
	if strings.TrimSpace(b.Digest) == "" || strings.TrimSpace(session) == "" || strings.TrimSpace(tenantScope) == "" || strings.TrimSpace(selectionReason) == "" {
		return runtimecontext.Block{}, errors.New("active SkillBundle context requires digest, session, tenant scope and selection reason")
	}
	sensitivity := security.Sensitivity(strings.ToLower(strings.TrimSpace(b.Sensitivity)))
	if sensitivity == "" {
		sensitivity = security.SensitivityInternal
	}
	if !sensitivity.Valid() {
		return runtimecontext.Block{}, fmt.Errorf("invalid SkillBundle sensitivity %q", b.Sensitivity)
	}
	block := runtimecontext.Block{
		ID:              "skill:" + b.ID + "@" + b.Version + ":r" + fmt.Sprintf("%d", b.Revision),
		Kind:            "skill",
		Source:          "skill:" + b.ID + "@" + b.Version,
		Content:         b.Instructions,
		Policy:          "frozen_skill_revision",
		TrustLevel:      security.TrustVerified,
		Sensitivity:     sensitivity,
		TenantScope:     tenantScope,
		Purpose:         "model_context",
		SelectionReason: selectionReason,
		TokenEstimate:   runtimecontext.Rune4Tokenizer{}.Estimate(b.Instructions),
		Mandatory:       false,
		Metadata: map[string]string{
			"prompt_kind": "agent_role",
			"skill_id":    b.ID, "skill_version": b.Version, "skill_revision": fmt.Sprintf("%d", b.Revision),
			"skill_digest": b.Digest, "skill_source": b.Source, "skill_license": b.License,
		},
	}
	if block.TokenEstimate < 1 {
		return runtimecontext.Block{}, errors.New("SkillBundle instructions cannot be empty")
	}
	block.Hash = runtimecontext.HashBlock(block)
	return block, nil
}
