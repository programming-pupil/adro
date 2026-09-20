// Package policy defines deterministic, capability-based authorization facts.
// It contains no I/O and never inspects tool or adapter names: callers supply
// explicit capabilities and durable scope metadata.
package policy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	pathpkg "path"
	"sort"
	"strconv"
	"strings"
	"time"

	coreencoding "github.com/adro-project/adro/core/encoding"
	"github.com/adro-project/adro/core/ids"
)

const EngineVersion = "adro.policy.v1"

var (
	ErrInvalid           = errors.New("invalid policy input")
	ErrReplayConflict    = errors.New("policy replay conflicts with historical decision")
	ErrPrivilegeEscalate = errors.New("child policy expands parent authority")
)

type Outcome string

const (
	OutcomeAllow           Outcome = "allow"
	OutcomeDeny            Outcome = "deny"
	OutcomeRequireApproval Outcome = "require_approval"
)

func (o Outcome) Valid() bool {
	switch o {
	case OutcomeAllow, OutcomeDeny, OutcomeRequireApproval:
		return true
	default:
		return false
	}
}

type Sensitivity string

const (
	SensitivityPublic       Sensitivity = "public"
	SensitivityInternal     Sensitivity = "internal"
	SensitivityConfidential Sensitivity = "confidential"
	SensitivityRestricted   Sensitivity = "restricted"
	SensitivitySecret       Sensitivity = "secret"
)

var sensitivityOrder = map[Sensitivity]int{
	SensitivityPublic: 0, SensitivityInternal: 1, SensitivityConfidential: 2,
	SensitivityRestricted: 3, SensitivitySecret: 4,
}

func (s Sensitivity) Valid() bool {
	_, ok := sensitivityOrder[s]
	return ok
}

func AtMostSensitivity(actual, maximum Sensitivity) bool {
	actualRank, actualOK := sensitivityOrder[actual]
	maximumRank, maximumOK := sensitivityOrder[maximum]
	return actualOK && maximumOK && actualRank <= maximumRank
}

type EgressRule struct {
	Destination    string      `json:"destination"`
	Purpose        string      `json:"purpose"`
	MaxSensitivity Sensitivity `json:"max_sensitivity"`
}

type NetworkRule struct {
	Domain   string `json:"domain,omitempty"`
	IP       string `json:"ip,omitempty"`
	CIDR     string `json:"cidr,omitempty"`
	Ports    []int  `json:"ports"`
	Protocol string `json:"protocol"`
	Purpose  string `json:"purpose"`
}

// ValidateNetworkEgress proves that every network grant has a matching data
// obligation and every data obligation is enforceable by a network grant.
// It deliberately performs no DNS lookup; domain grants bind exact names and
// IP/CIDR grants bind literal addresses.
func ValidateNetworkEgress(networkRules []NetworkRule, egressRules []EgressRule) error {
	if len(networkRules) == 0 && len(egressRules) == 0 {
		return nil
	}
	if len(networkRules) == 0 || len(egressRules) == 0 {
		return fmt.Errorf("%w: network and data-egress permissions must be declared together", ErrInvalid)
	}
	for index, networkRule := range networkRules {
		if err := validateNetworkRule(networkRule); err != nil {
			return fmt.Errorf("%w: network rule %d: %v", ErrInvalid, index, err)
		}
		covered := false
		for _, egressRule := range egressRules {
			if networkRuleCovers(networkRule, egressRule) {
				covered = true
				break
			}
		}
		if !covered {
			return fmt.Errorf("%w: network grant lacks a matching data-egress obligation", ErrInvalid)
		}
	}
	for index, egressRule := range egressRules {
		canonical, err := CanonicalDestination(egressRule.Destination)
		if err != nil || !canonicalValue(strings.TrimSpace(egressRule.Purpose), 256) || !egressRule.MaxSensitivity.Valid() {
			return fmt.Errorf("%w: data-egress rule %d is invalid", ErrInvalid, index)
		}
		egressRule.Destination = canonical
		covered := false
		for _, networkRule := range networkRules {
			if networkRuleCovers(networkRule, egressRule) {
				covered = true
				break
			}
		}
		if !covered {
			return fmt.Errorf("%w: data-egress destination is not covered by a network grant", ErrInvalid)
		}
	}
	return nil
}

type Bundle struct {
	ID                 string       `json:"id"`
	Version            string       `json:"version"`
	TenantID           string       `json:"tenant_id"`
	WorkspaceID        string       `json:"workspace_id"`
	Capabilities       []string     `json:"capabilities"`
	DeniedCapabilities []string     `json:"denied_capabilities,omitempty"`
	Egress             []EgressRule `json:"egress,omitempty"`
}

type FrozenBundle struct {
	Bundle
	Digest string `json:"digest"`
}

func FreezeBundle(bundle Bundle) (FrozenBundle, error) {
	bundle.ID = strings.TrimSpace(bundle.ID)
	bundle.Version = strings.TrimSpace(bundle.Version)
	bundle.TenantID = strings.TrimSpace(bundle.TenantID)
	bundle.WorkspaceID = strings.TrimSpace(bundle.WorkspaceID)
	if !canonicalValue(bundle.ID, 256) || !canonicalValue(bundle.Version, 256) {
		return FrozenBundle{}, fmt.Errorf("%w: policy id, version, tenant, and workspace are required", ErrInvalid)
	}
	if err := ids.Validate("tenant", bundle.TenantID); err != nil {
		return FrozenBundle{}, err
	}
	if err := ids.Validate("workspace", bundle.WorkspaceID); err != nil {
		return FrozenBundle{}, err
	}
	capabilities, err := canonicalSet(bundle.Capabilities, true)
	if err != nil {
		return FrozenBundle{}, fmt.Errorf("%w: capabilities: %v", ErrInvalid, err)
	}
	denied, err := canonicalSet(bundle.DeniedCapabilities, false)
	if err != nil {
		return FrozenBundle{}, fmt.Errorf("%w: denied capabilities: %v", ErrInvalid, err)
	}
	allowed := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		allowed[capability] = struct{}{}
	}
	for _, capability := range denied {
		if _, overlap := allowed[capability]; overlap {
			return FrozenBundle{}, fmt.Errorf("%w: capability %q is both allowed and denied", ErrInvalid, capability)
		}
	}
	bundle.Capabilities, bundle.DeniedCapabilities = capabilities, denied
	bundle.Egress = append([]EgressRule(nil), bundle.Egress...)
	seenRules := make(map[string]struct{}, len(bundle.Egress))
	for index := range bundle.Egress {
		rule := &bundle.Egress[index]
		rule.Destination, err = CanonicalDestination(rule.Destination)
		if err != nil {
			return FrozenBundle{}, fmt.Errorf("%w: egress rule %d: %v", ErrInvalid, index, err)
		}
		rule.Purpose = strings.TrimSpace(rule.Purpose)
		if !canonicalValue(rule.Purpose, 256) || !rule.MaxSensitivity.Valid() {
			return FrozenBundle{}, fmt.Errorf("%w: egress rule %d purpose and sensitivity are required", ErrInvalid, index)
		}
		key := rule.Destination + "\x00" + rule.Purpose
		if _, duplicate := seenRules[key]; duplicate {
			return FrozenBundle{}, fmt.Errorf("%w: duplicate egress rule", ErrInvalid)
		}
		seenRules[key] = struct{}{}
	}
	sort.Slice(bundle.Egress, func(i, j int) bool {
		left := bundle.Egress[i].Destination + "\x00" + bundle.Egress[i].Purpose
		right := bundle.Egress[j].Destination + "\x00" + bundle.Egress[j].Purpose
		return left < right
	})
	digest, err := coreencoding.Digest(bundle)
	if err != nil {
		return FrozenBundle{}, fmt.Errorf("freeze policy bundle: %w", err)
	}
	return FrozenBundle{Bundle: bundle, Digest: digest}, nil
}

func CanonicalDestination(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", errors.New("destination must be an absolute credential-free https URL without query or fragment")
	}
	if parsed.RawPath != "" || parsed.EscapedPath() != parsed.Path || strings.ContainsAny(parsed.Path, "\\\x00\r\n") {
		return "", errors.New("destination path must be canonical and unescaped")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else if !validDomainName(host) {
		return "", errors.New("destination host is invalid")
	}
	port := parsed.Port()
	if port != "" {
		number, parseErr := strconv.Atoi(port)
		if parseErr != nil || number < 1 || number > 65535 {
			return "", errors.New("destination port is invalid")
		}
		if number == 443 {
			port = ""
		}
	}
	if port == "" {
		if strings.Contains(host, ":") {
			parsed.Host = "[" + host + "]"
		} else {
			parsed.Host = host
		}
	} else {
		parsed.Host = net.JoinHostPort(host, port)
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	cleanPath := pathpkg.Clean(parsed.Path)
	if cleanPath != parsed.Path && cleanPath+"/" != parsed.Path {
		return "", errors.New("destination path is not canonical")
	}
	parsed.Scheme = "https"
	parsed.RawPath = ""
	return parsed.String(), nil
}

type Input struct {
	TenantID    string      `json:"tenant_id"`
	WorkspaceID string      `json:"workspace_id"`
	ActorID     string      `json:"actor_id"`
	Capability  string      `json:"capability"`
	Destination string      `json:"destination,omitempty"`
	Purpose     string      `json:"purpose,omitempty"`
	Sensitivity Sensitivity `json:"sensitivity,omitempty"`
}

func NormalizeInput(input Input) (Input, error) {
	input.TenantID = strings.TrimSpace(input.TenantID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.Capability = strings.TrimSpace(input.Capability)
	input.Purpose = strings.TrimSpace(input.Purpose)
	if !canonicalValue(input.Capability, 256) {
		return Input{}, fmt.Errorf("%w: tenant, workspace, actor, and capability are required", ErrInvalid)
	}
	for namespace, value := range map[string]string{"tenant": input.TenantID, "workspace": input.WorkspaceID, "actor": input.ActorID} {
		if err := ids.Validate(namespace, value); err != nil {
			return Input{}, err
		}
	}
	if strings.TrimSpace(input.Destination) == "" {
		if input.Purpose != "" || input.Sensitivity != "" {
			return Input{}, fmt.Errorf("%w: egress purpose and sensitivity require a destination", ErrInvalid)
		}
		return input, nil
	}
	var err error
	input.Destination, err = CanonicalDestination(input.Destination)
	if err != nil {
		return Input{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if !canonicalValue(input.Purpose, 256) || !input.Sensitivity.Valid() {
		return Input{}, fmt.Errorf("%w: egress purpose and sensitivity are required", ErrInvalid)
	}
	return input, nil
}

type DecisionRecord struct {
	PolicyID      string      `json:"policy_id"`
	PolicyVersion string      `json:"policy_version"`
	BundleDigest  string      `json:"bundle_digest"`
	EngineVersion string      `json:"engine_version"`
	InputDigest   string      `json:"input_digest"`
	TenantID      string      `json:"tenant_id"`
	WorkspaceID   string      `json:"workspace_id"`
	ActorID       string      `json:"actor_id"`
	Capability    string      `json:"capability"`
	Destination   string      `json:"destination,omitempty"`
	Purpose       string      `json:"purpose,omitempty"`
	Sensitivity   Sensitivity `json:"sensitivity,omitempty"`
	Outcome       Outcome     `json:"outcome"`
	ReasonCode    string      `json:"reason_code"`
	EvaluatedAt   time.Time   `json:"evaluated_at"`
}

type Evaluator interface {
	Evaluate(context.Context, FrozenBundle, Input, time.Time) (DecisionRecord, error)
}

type BuiltinEvaluator struct{}

func (BuiltinEvaluator) Evaluate(ctx context.Context, bundle FrozenBundle, input Input, evaluatedAt time.Time) (DecisionRecord, error) {
	if err := contextError(ctx); err != nil {
		return DecisionRecord{}, err
	}
	record, normalized, err := newRecord(bundle, input, evaluatedAt, EngineVersion)
	if err != nil {
		return DecisionRecord{}, err
	}
	if normalized.TenantID != bundle.TenantID || normalized.WorkspaceID != bundle.WorkspaceID {
		record.Outcome, record.ReasonCode = OutcomeDeny, "tenant_scope_mismatch"
		return record, nil
	}
	if contains(bundle.DeniedCapabilities, normalized.Capability) || !contains(bundle.Capabilities, normalized.Capability) {
		record.Outcome, record.ReasonCode = OutcomeDeny, "capability_denied"
		return record, nil
	}
	if normalized.Destination == "" {
		record.Outcome, record.ReasonCode = OutcomeAllow, "capability_allowed"
		return record, nil
	}
	for _, rule := range bundle.Egress {
		if rule.Destination == normalized.Destination && rule.Purpose == normalized.Purpose {
			if AtMostSensitivity(normalized.Sensitivity, rule.MaxSensitivity) {
				record.Outcome, record.ReasonCode = OutcomeAllow, "egress_allowed"
			} else {
				record.Outcome, record.ReasonCode = OutcomeDeny, "sensitivity_exceeds_grant"
			}
			return record, nil
		}
	}
	record.Outcome, record.ReasonCode = OutcomeDeny, "egress_destination_or_purpose_denied"
	return record, nil
}

// EvaluateFailClosed converts evaluator outages, timeouts, invalid responses,
// and version mismatches into a durable deny decision. Invalid caller input is
// returned as an error because it cannot produce a trustworthy input digest.
func EvaluateFailClosed(ctx context.Context, evaluator Evaluator, bundle FrozenBundle, input Input, evaluatedAt time.Time, timeout time.Duration, expectedEngineVersion string) (DecisionRecord, error) {
	if evaluator == nil {
		evaluator = BuiltinEvaluator{}
	}
	if expectedEngineVersion == "" {
		expectedEngineVersion = EngineVersion
	}
	base, normalized, err := newRecord(bundle, input, evaluatedAt, expectedEngineVersion)
	if err != nil {
		return DecisionRecord{}, err
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if ctx == nil {
		ctx = context.Background()
	}
	evaluationCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	type evaluationResult struct {
		record DecisionRecord
		err    error
	}
	resultCh := make(chan evaluationResult, 1)
	go func() {
		record, evaluateErr := evaluator.Evaluate(evaluationCtx, bundle, normalized, base.EvaluatedAt)
		resultCh <- evaluationResult{record: record, err: evaluateErr}
	}()
	var result evaluationResult
	select {
	case result = <-resultCh:
	case <-evaluationCtx.Done():
		base.Outcome, base.ReasonCode = OutcomeDeny, "policy_engine_unavailable"
		return base, nil
	}
	if result.err != nil {
		base.Outcome, base.ReasonCode = OutcomeDeny, "policy_engine_unavailable"
		return base, nil
	}
	if err := validateRecord(result.record, base, expectedEngineVersion); err != nil {
		base.Outcome, base.ReasonCode = OutcomeDeny, "policy_engine_invalid_result"
		return base, nil
	}
	return result.record, nil
}

func Replay(record DecisionRecord, bundle FrozenBundle, input Input) (DecisionRecord, error) {
	base, _, err := newRecord(bundle, input, record.EvaluatedAt, record.EngineVersion)
	if err != nil {
		return DecisionRecord{}, err
	}
	if err := validateRecord(record, base, record.EngineVersion); err != nil {
		return DecisionRecord{}, fmt.Errorf("%w: %v", ErrReplayConflict, err)
	}
	return record, nil
}

func ValidateDecisionRecord(record DecisionRecord) error {
	for namespace, value := range map[string]string{
		"tenant": record.TenantID, "workspace": record.WorkspaceID, "actor": record.ActorID,
	} {
		if err := ids.Validate(namespace, value); err != nil {
			return err
		}
	}
	if !canonicalValue(record.PolicyID, 256) || !canonicalValue(record.PolicyVersion, 256) || !canonicalValue(record.BundleDigest, 128) ||
		!canonicalValue(record.EngineVersion, 256) || !canonicalValue(record.InputDigest, 128) || !canonicalValue(record.Capability, 256) ||
		!record.Outcome.Valid() || !canonicalValue(record.ReasonCode, 256) || record.EvaluatedAt.IsZero() ||
		!record.EvaluatedAt.Equal(record.EvaluatedAt.UTC().Truncate(time.Microsecond)) {
		return fmt.Errorf("%w: decision record is incomplete", ErrInvalid)
	}
	if record.Destination == "" {
		if record.Purpose != "" || record.Sensitivity != "" {
			return fmt.Errorf("%w: decision egress metadata is inconsistent", ErrInvalid)
		}
	} else {
		canonical, err := CanonicalDestination(record.Destination)
		if err != nil || canonical != record.Destination || !canonicalValue(record.Purpose, 256) || !record.Sensitivity.Valid() {
			return fmt.Errorf("%w: decision egress metadata is invalid", ErrInvalid)
		}
	}
	return nil
}

func DecisionDigest(record DecisionRecord) (string, error) {
	if err := ValidateDecisionRecord(record); err != nil {
		return "", err
	}
	return coreencoding.Digest(record)
}

type AuditResult struct {
	Historical DecisionRecord `json:"historical"`
	Recomputed DecisionRecord `json:"recomputed"`
	Diverged   bool           `json:"diverged"`
}

func Audit(ctx context.Context, evaluator Evaluator, historical DecisionRecord, bundle FrozenBundle, input Input, timeout time.Duration) (AuditResult, error) {
	if _, err := Replay(historical, bundle, input); err != nil {
		return AuditResult{}, err
	}
	recomputed, err := EvaluateFailClosed(ctx, evaluator, bundle, input, historical.EvaluatedAt, timeout, historical.EngineVersion)
	if err != nil {
		return AuditResult{}, err
	}
	return AuditResult{Historical: historical, Recomputed: recomputed, Diverged: historical.Outcome != recomputed.Outcome || historical.ReasonCode != recomputed.ReasonCode}, nil
}

func ValidateChild(parent, child FrozenBundle) error {
	if child.TenantID != parent.TenantID || child.WorkspaceID != parent.WorkspaceID {
		return fmt.Errorf("%w: tenant scope changed", ErrPrivilegeEscalate)
	}
	for _, capability := range child.Capabilities {
		if !contains(parent.Capabilities, capability) || contains(parent.DeniedCapabilities, capability) {
			return fmt.Errorf("%w: capability %q is not allowed by parent", ErrPrivilegeEscalate, capability)
		}
	}
	for _, rule := range child.Egress {
		covered := false
		for _, parentRule := range parent.Egress {
			if rule.Destination == parentRule.Destination && rule.Purpose == parentRule.Purpose && AtMostSensitivity(rule.MaxSensitivity, parentRule.MaxSensitivity) {
				covered = true
				break
			}
		}
		if !covered {
			return fmt.Errorf("%w: egress rule %s is broader than parent", ErrPrivilegeEscalate, rule.Destination)
		}
	}
	return nil
}

func newRecord(bundle FrozenBundle, input Input, evaluatedAt time.Time, engineVersion string) (DecisionRecord, Input, error) {
	refrozen, err := FreezeBundle(bundle.Bundle)
	if err != nil || refrozen.Digest != bundle.Digest {
		return DecisionRecord{}, Input{}, fmt.Errorf("%w: bundle digest mismatch", ErrInvalid)
	}
	normalized, err := NormalizeInput(input)
	if err != nil {
		return DecisionRecord{}, Input{}, err
	}
	inputDigest, err := coreencoding.Digest(normalized)
	if err != nil {
		return DecisionRecord{}, Input{}, err
	}
	if evaluatedAt.IsZero() || !canonicalValue(engineVersion, 256) {
		return DecisionRecord{}, Input{}, fmt.Errorf("%w: evaluation time and engine version are required", ErrInvalid)
	}
	return DecisionRecord{
		PolicyID: bundle.ID, PolicyVersion: bundle.Version, BundleDigest: bundle.Digest,
		EngineVersion: engineVersion, InputDigest: inputDigest,
		TenantID: normalized.TenantID, WorkspaceID: normalized.WorkspaceID, ActorID: normalized.ActorID,
		Capability: normalized.Capability, Destination: normalized.Destination, Purpose: normalized.Purpose, Sensitivity: normalized.Sensitivity,
		EvaluatedAt: evaluatedAt.UTC().Truncate(time.Microsecond),
	}, normalized, nil
}

func validateRecord(record, expected DecisionRecord, engineVersion string) error {
	if record.PolicyID != expected.PolicyID || record.PolicyVersion != expected.PolicyVersion || record.BundleDigest != expected.BundleDigest || record.InputDigest != expected.InputDigest ||
		record.TenantID != expected.TenantID || record.WorkspaceID != expected.WorkspaceID || record.ActorID != expected.ActorID || record.Capability != expected.Capability ||
		record.Destination != expected.Destination || record.Purpose != expected.Purpose || record.Sensitivity != expected.Sensitivity {
		return errors.New("policy identity or input digest changed")
	}
	if record.EngineVersion != engineVersion || !record.Outcome.Valid() || !canonicalValue(record.ReasonCode, 256) || !record.EvaluatedAt.Equal(expected.EvaluatedAt) {
		return errors.New("policy output, engine version, reason, or timestamp is invalid")
	}
	return nil
}

func canonicalSet(values []string, required bool) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !canonicalValue(value, 256) {
			return nil, errors.New("set contains an invalid value")
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, fmt.Errorf("set contains duplicate %q", value)
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if required && len(result) == 0 {
		return nil, errors.New("set cannot be empty")
	}
	sort.Strings(result)
	return result, nil
}

func canonicalValue(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}

func contains(values []string, target string) bool {
	index := sort.SearchStrings(values, target)
	return index < len(values) && values[index] == target
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func validateNetworkRule(rule NetworkRule) error {
	selectors := 0
	if strings.TrimSpace(rule.Domain) != "" {
		selectors++
		if !validDomainName(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(rule.Domain)), ".")) {
			return errors.New("network domain is invalid")
		}
	}
	if strings.TrimSpace(rule.IP) != "" {
		selectors++
		if net.ParseIP(strings.TrimSpace(rule.IP)) == nil {
			return errors.New("network IP is invalid")
		}
	}
	if strings.TrimSpace(rule.CIDR) != "" {
		selectors++
		if _, _, err := net.ParseCIDR(strings.TrimSpace(rule.CIDR)); err != nil {
			return errors.New("network CIDR is invalid")
		}
	}
	if selectors != 1 || !canonicalValue(strings.TrimSpace(rule.Purpose), 256) {
		return errors.New("one selector and a purpose are required")
	}
	protocol := strings.ToLower(strings.TrimSpace(rule.Protocol))
	if protocol != "https" && protocol != "tcp" {
		return errors.New("data egress requires https or tcp network protocol")
	}
	if len(rule.Ports) == 0 {
		return errors.New("network ports are required")
	}
	seen := make(map[int]struct{}, len(rule.Ports))
	for _, port := range rule.Ports {
		if port < 1 || port > 65535 {
			return errors.New("network port is invalid")
		}
		if _, duplicate := seen[port]; duplicate {
			return errors.New("network port is duplicated")
		}
		seen[port] = struct{}{}
	}
	return nil
}

func validDomainName(value string) bool {
	if value == "" || len(value) > 253 || strings.ContainsAny(value, " /:@\\\t\r\n") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func networkRuleCovers(rule NetworkRule, egress EgressRule) bool {
	if strings.TrimSpace(rule.Purpose) != strings.TrimSpace(egress.Purpose) {
		return false
	}
	protocol := strings.ToLower(strings.TrimSpace(rule.Protocol))
	if protocol != "https" && protocol != "tcp" {
		return false
	}
	canonical, err := CanonicalDestination(egress.Destination)
	if err != nil {
		return false
	}
	parsed, err := url.Parse(canonical)
	if err != nil {
		return false
	}
	port := 443
	if parsed.Port() != "" {
		port, err = strconv.Atoi(parsed.Port())
		if err != nil {
			return false
		}
	}
	if !containsPort(rule.Ports, port) {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	switch {
	case strings.TrimSpace(rule.Domain) != "":
		return strings.EqualFold(strings.TrimSuffix(rule.Domain, "."), strings.TrimSuffix(host, "."))
	case strings.TrimSpace(rule.IP) != "":
		left, right := net.ParseIP(strings.TrimSpace(rule.IP)), net.ParseIP(host)
		return left != nil && right != nil && left.Equal(right)
	case strings.TrimSpace(rule.CIDR) != "":
		address := net.ParseIP(host)
		_, network, parseErr := net.ParseCIDR(strings.TrimSpace(rule.CIDR))
		return parseErr == nil && address != nil && network.Contains(address)
	default:
		return false
	}
}

func containsPort(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
