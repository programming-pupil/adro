package security

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// TrustLevel describes how much authority data may carry. It is independent
// from integrity: a correctly hashed tool result remains untrusted data.
type TrustLevel string

const (
	TrustUntrusted TrustLevel = "untrusted"
	TrustReviewed  TrustLevel = "reviewed"
	TrustVerified  TrustLevel = "verified"
	TrustTrusted   TrustLevel = "trusted"
)

var trustOrder = map[TrustLevel]int{
	TrustUntrusted: 0,
	TrustReviewed:  1,
	TrustVerified:  2,
	TrustTrusted:   3,
}

func (t TrustLevel) Valid() bool {
	_, ok := trustOrder[t]
	return ok
}

// LessTrusted returns the lower-trust value. Derived data may use this to
// preserve the weakest input trust without allowing an elevation.
func LessTrusted(left, right TrustLevel) TrustLevel {
	leftOrder, leftOK := trustOrder[left]
	rightOrder, rightOK := trustOrder[right]
	if !leftOK || !rightOK {
		return TrustUntrusted
	}
	if leftOrder <= rightOrder {
		return left
	}
	return right
}

// TrustZone records the boundary where data entered the runtime. User,
// retrieved, tool, remote and model content are always untrusted regardless of
// what instructions their payload contains.
type TrustZone string

const (
	ZoneSystemPolicy     TrustZone = "system_policy"
	ZoneWorkspacePolicy  TrustZone = "workspace_policy"
	ZoneVerifiedArtifact TrustZone = "verified_artifact"
	ZoneUserContent      TrustZone = "user_content"
	ZoneRetrievedContent TrustZone = "retrieved_content"
	ZoneToolOutput       TrustZone = "tool_output"
	ZoneRemoteContent    TrustZone = "remote_content"
	ZoneModelOutput      TrustZone = "model_output"
	ZoneUnknown          TrustZone = "unknown"
)

func (z TrustZone) Valid() bool {
	switch z {
	case ZoneSystemPolicy, ZoneWorkspacePolicy, ZoneVerifiedArtifact,
		ZoneUserContent, ZoneRetrievedContent, ZoneToolOutput,
		ZoneRemoteContent, ZoneModelOutput, ZoneUnknown:
		return true
	default:
		return false
	}
}

type TaintLabel string

const (
	TaintUserContent      TaintLabel = "untrusted_user_content"
	TaintRetrievedContent TaintLabel = "untrusted_retrieved_content"
	TaintToolOutput       TaintLabel = "untrusted_tool_output"
	TaintRemoteContent    TaintLabel = "untrusted_remote_content"
	TaintModelOutput      TaintLabel = "untrusted_model_output"
	TaintUnknownSource    TaintLabel = "untrusted_unknown_source"
)

func (t TaintLabel) Valid() bool {
	switch t {
	case TaintUserContent, TaintRetrievedContent, TaintToolOutput,
		TaintRemoteContent, TaintModelOutput, TaintUnknownSource:
		return true
	default:
		return false
	}
}

// Provenance is the minimum security metadata carried by context, tool and
// model data. Taint labels are canonicalized so digests remain deterministic.
type Provenance struct {
	Source      string       `json:"source"`
	TrustLevel  TrustLevel   `json:"trust_level"`
	Sensitivity Sensitivity  `json:"sensitivity"`
	TenantScope string       `json:"tenant_scope"`
	Purpose     string       `json:"purpose"`
	TaintLabels []TaintLabel `json:"taint_labels"`
}

func (p Provenance) Validate() error {
	if !canonicalBounded(p.Source, 512) || !canonicalBounded(p.TenantScope, 256) || !canonicalBounded(p.Purpose, 256) {
		return errors.New("source, tenant scope, and purpose must be canonical non-empty values")
	}
	if !p.TrustLevel.Valid() || !p.Sensitivity.Valid() {
		return errors.New("trust level and sensitivity must be valid")
	}
	previous := ""
	for _, label := range p.TaintLabels {
		if !label.Valid() {
			return fmt.Errorf("invalid taint label %q", label)
		}
		current := string(label)
		if previous != "" && current <= previous {
			return errors.New("taint labels must be unique and canonically sorted")
		}
		previous = current
	}
	if len(p.TaintLabels) > 0 && p.TrustLevel != TrustUntrusted {
		return errors.New("tainted data cannot be trusted, verified, or reviewed")
	}
	if p.TrustLevel == TrustUntrusted && len(p.TaintLabels) == 0 {
		return errors.New("untrusted data requires at least one taint label")
	}
	return nil
}

func CanonicalizeProvenance(p Provenance) (Provenance, error) {
	p.Source = strings.TrimSpace(p.Source)
	p.TenantScope = strings.TrimSpace(p.TenantScope)
	p.Purpose = strings.TrimSpace(p.Purpose)
	labels := make([]TaintLabel, 0, len(p.TaintLabels))
	seen := make(map[TaintLabel]struct{}, len(p.TaintLabels))
	for _, label := range p.TaintLabels {
		if !label.Valid() {
			return Provenance{}, fmt.Errorf("invalid taint label %q", label)
		}
		if _, exists := seen[label]; exists {
			continue
		}
		seen[label] = struct{}{}
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool { return labels[i] < labels[j] })
	p.TaintLabels = labels
	if len(labels) > 0 {
		p.TrustLevel = TrustUntrusted
	}
	if err := p.Validate(); err != nil {
		return Provenance{}, err
	}
	return p, nil
}

func NewProvenance(zone TrustZone, source, tenantScope, purpose string, sensitivity Sensitivity) (Provenance, error) {
	if !zone.Valid() {
		return Provenance{}, fmt.Errorf("invalid trust zone %q", zone)
	}
	if !sensitivity.Valid() {
		return Provenance{}, fmt.Errorf("invalid sensitivity %q", sensitivity)
	}
	provenance := Provenance{
		Source: source, Sensitivity: sensitivity, TenantScope: tenantScope, Purpose: purpose,
	}
	switch zone {
	case ZoneSystemPolicy, ZoneWorkspacePolicy:
		provenance.TrustLevel = TrustTrusted
	case ZoneVerifiedArtifact:
		provenance.TrustLevel = TrustVerified
	case ZoneUserContent:
		provenance.TrustLevel, provenance.TaintLabels = TrustUntrusted, []TaintLabel{TaintUserContent}
	case ZoneRetrievedContent:
		provenance.TrustLevel, provenance.TaintLabels = TrustUntrusted, []TaintLabel{TaintRetrievedContent}
	case ZoneToolOutput:
		provenance.TrustLevel, provenance.TaintLabels = TrustUntrusted, []TaintLabel{TaintToolOutput}
	case ZoneRemoteContent:
		provenance.TrustLevel, provenance.TaintLabels = TrustUntrusted, []TaintLabel{TaintRemoteContent}
	case ZoneModelOutput:
		provenance.TrustLevel, provenance.TaintLabels = TrustUntrusted, []TaintLabel{TaintModelOutput}
	case ZoneUnknown:
		provenance.TrustLevel, provenance.TaintLabels = TrustUntrusted, []TaintLabel{TaintUnknownSource}
	}
	return CanonicalizeProvenance(provenance)
}

// DeriveProvenance creates data in zone while preserving the weakest trust,
// highest sensitivity and complete taint union from every input. Tenant scopes
// must match; cross-tenant derivation is rejected rather than merged.
func DeriveProvenance(zone TrustZone, source, tenantScope, purpose string, sensitivity Sensitivity, inputs ...Provenance) (Provenance, error) {
	derived, err := NewProvenance(zone, source, tenantScope, purpose, sensitivity)
	if err != nil {
		return Provenance{}, err
	}
	labels := append([]TaintLabel(nil), derived.TaintLabels...)
	for _, input := range inputs {
		input, err = CanonicalizeProvenance(input)
		if err != nil {
			return Provenance{}, fmt.Errorf("invalid derived input: %w", err)
		}
		if input.TenantScope != derived.TenantScope {
			return Provenance{}, errors.New("derived data cannot cross tenant scopes")
		}
		derived.TrustLevel = LessTrusted(derived.TrustLevel, input.TrustLevel)
		derived.Sensitivity = MaxSensitivity(derived.Sensitivity, input.Sensitivity)
		labels = append(labels, input.TaintLabels...)
	}
	derived.TaintLabels = labels
	return CanonicalizeProvenance(derived)
}

// EnforceTrustZone rejects metadata that attempts to elevate a zone above its
// runtime-defined trust. Additional taints and lower trust remain valid.
func EnforceTrustZone(zone TrustZone, provenance Provenance) error {
	originalTrust := provenance.TrustLevel
	if !originalTrust.Valid() {
		return fmt.Errorf("invalid trust level %q", originalTrust)
	}
	canonical, err := CanonicalizeProvenance(provenance)
	if err != nil {
		return err
	}
	required, err := NewProvenance(zone, canonical.Source, canonical.TenantScope, canonical.Purpose, canonical.Sensitivity)
	if err != nil {
		return err
	}
	if trustOrder[originalTrust] > trustOrder[required.TrustLevel] {
		return fmt.Errorf("trust zone %s cannot carry trust level %s", zone, originalTrust)
	}
	requiredLabels := make(map[TaintLabel]struct{}, len(required.TaintLabels))
	for _, label := range canonical.TaintLabels {
		requiredLabels[label] = struct{}{}
	}
	for _, label := range required.TaintLabels {
		if _, ok := requiredLabels[label]; !ok {
			return fmt.Errorf("trust zone %s requires taint %s", zone, label)
		}
	}
	return nil
}

func MaxSensitivity(left, right Sensitivity) Sensitivity {
	leftOrder, leftOK := sensitivityOrder[left]
	rightOrder, rightOK := sensitivityOrder[right]
	if !leftOK {
		return right
	}
	if !rightOK || leftOrder >= rightOrder {
		return left
	}
	return right
}

func canonicalBounded(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}
