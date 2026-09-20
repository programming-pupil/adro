// Package skills defines the durable, provider-neutral SkillBundle boundary.
// A bundle is immutable once a revision is active; tool and secret access is
// granted by policy at activation time and is never implied by bundle content.
package skills

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const BundleSchemaVersion = 1
const maxBundleTokens int64 = 10_000_000

var (
	ErrInvalid          = errors.New("invalid SkillBundle")
	ErrNotFound         = errors.New("SkillBundle not found")
	ErrConflict         = errors.New("SkillBundle revision conflict")
	ErrQuarantined      = errors.New("SkillBundle is quarantined")
	ErrRevoked          = errors.New("SkillBundle signing key is revoked")
	ErrCapabilityDenied = errors.New("SkillBundle capability is not granted")
	ErrDependency       = errors.New("SkillBundle dependency is not satisfiable")
	ErrNotActive        = errors.New("SkillBundle is not active")
)

type State string

const (
	StateInstalled   State = "installed"
	StateActive      State = "active"
	StateDisabled    State = "disabled"
	StateQuarantined State = "quarantined"
	StateRevoked     State = "revoked"
)

type Resource struct {
	ID              string `json:"id"`
	URI             string `json:"uri"`
	Digest          string `json:"digest"`
	MediaType       string `json:"media_type"`
	Source          string `json:"source"`
	License         string `json:"license"`
	Trust           string `json:"trust"`
	Sensitivity     string `json:"sensitivity,omitempty"`
	SelectionReason string `json:"selection_reason,omitempty"`
}

type ToolSchema struct {
	Name               string   `json:"name"`
	InputSchemaDigest  string   `json:"input_schema_digest"`
	OutputSchemaDigest string   `json:"output_schema_digest"`
	Capabilities       []string `json:"capabilities,omitempty"`
	SideEffectClass    string   `json:"side_effect_class"`
	SchemaVersion      int      `json:"schema_version"`
}

type SchemaRef struct {
	ID      string `json:"id"`
	Digest  string `json:"digest"`
	Version string `json:"version"`
}

type Example struct {
	ID        string `json:"id"`
	Digest    string `json:"digest"`
	MediaType string `json:"media_type"`
}

type Dependency struct {
	ID                string `json:"id"`
	VersionConstraint string `json:"version_constraint"`
}

// SkillBundle is the signed, immutable input to context assembly. It carries
// declarations and digests only; it does not contain secret values or granted
// tool handles.
type SkillBundle struct {
	SchemaVersion int          `json:"schema_version"`
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Version       string       `json:"version"`
	Revision      int64        `json:"revision"`
	Source        string       `json:"source"`
	License       string       `json:"license"`
	Trust         string       `json:"trust"`
	Sensitivity   string       `json:"sensitivity"`
	Instructions  string       `json:"instructions"`
	Resources     []Resource   `json:"resources,omitempty"`
	Tools         []ToolSchema `json:"tools,omitempty"`
	Schemas       []SchemaRef  `json:"schemas,omitempty"`
	Examples      []Example    `json:"examples,omitempty"`
	Capabilities  []string     `json:"capabilities,omitempty"`
	Dependencies  []Dependency `json:"dependencies,omitempty"`
	MaxTokens     int64        `json:"max_tokens"`
	Digest        string       `json:"digest"`
	Signature     string       `json:"signature,omitempty"`
	SignerKeyID   string       `json:"signer_key_id,omitempty"`
}

type Installation struct {
	Bundle        SkillBundle `json:"bundle"`
	State         State       `json:"state"`
	InstalledAt   time.Time   `json:"installed_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
	ActivatedAt   time.Time   `json:"activated_at,omitempty"`
	QuarantinedAt time.Time   `json:"quarantined_at,omitempty"`
	Reason        string      `json:"reason,omitempty"`
}

type Event struct {
	Sequence  int64     `json:"sequence"`
	Type      string    `json:"type"`
	BundleID  string    `json:"bundle_id"`
	Version   string    `json:"version"`
	Revision  int64     `json:"revision"`
	Digest    string    `json:"digest"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type InstallRequest struct {
	Bundle           SkillBundle
	PublicKey        ed25519.PublicKey
	RequireSignature bool
}

type ActivationPolicy struct {
	GrantedCapabilities []string
	MaxTokens           int64
	AllowedSources      []string
	RequireSignature    bool
}

type FrozenRevision struct {
	Bundle SkillBundle `json:"bundle"`
	Digest string      `json:"digest"`
}

type RegistryOptions struct {
	Path string
	Now  func() time.Time
}

type persistedState struct {
	Version       int                     `json:"version"`
	Installations map[string]Installation `json:"installations"`
	Events        []Event                 `json:"events"`
	RevokedKeys   map[string]bool         `json:"revoked_keys"`
}

type Registry struct {
	mu            sync.RWMutex
	path          string
	now           func() time.Time
	installations map[string]Installation
	events        []Event
	revokedKeys   map[string]bool
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)

func NewRegistry(options RegistryOptions) (*Registry, error) {
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	registry := &Registry{path: strings.TrimSpace(options.Path), now: now, installations: map[string]Installation{}, revokedKeys: map[string]bool{}}
	if registry.path == "" {
		return registry, nil
	}
	data, err := os.ReadFile(registry.path)
	if errors.Is(err, os.ErrNotExist) {
		return registry, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read SkillBundle registry: %w", err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil || state.Version != 1 {
		return nil, fmt.Errorf("decode SkillBundle registry: %w", ErrInvalid)
	}
	if state.Installations != nil {
		registry.installations = state.Installations
	}
	registry.events = append([]Event(nil), state.Events...)
	if state.RevokedKeys != nil {
		registry.revokedKeys = state.RevokedKeys
	}
	for key, installation := range registry.installations {
		if err := installation.Bundle.Validate(); err != nil {
			return nil, fmt.Errorf("validate SkillBundle %s: %w", key, err)
		}
		if installation.State == "" {
			return nil, fmt.Errorf("validate SkillBundle %s: %w", key, ErrInvalid)
		}
	}
	return registry, nil
}

func (b SkillBundle) Validate() error {
	if b.SchemaVersion != BundleSchemaVersion || !idPattern.MatchString(strings.TrimSpace(b.ID)) || strings.TrimSpace(b.Name) == "" || strings.TrimSpace(b.Version) == "" || b.Revision < 1 || strings.TrimSpace(b.Source) == "" || strings.TrimSpace(b.License) == "" || strings.TrimSpace(b.Trust) == "" || strings.TrimSpace(b.Instructions) == "" || b.MaxTokens < 1 || b.MaxTokens > maxBundleTokens {
		return ErrInvalid
	}
	for _, value := range []string{b.Source, b.Trust, b.Sensitivity} {
		if strings.ContainsAny(value, "\r\n") {
			return ErrInvalid
		}
	}
	seen := map[string]struct{}{}
	for _, resource := range b.Resources {
		if !idPattern.MatchString(strings.TrimSpace(resource.ID)) || strings.TrimSpace(resource.URI) == "" || !validDigest(resource.Digest) || strings.TrimSpace(resource.MediaType) == "" || strings.TrimSpace(resource.Source) == "" || strings.TrimSpace(resource.License) == "" || strings.TrimSpace(resource.Trust) == "" || strings.TrimSpace(resource.SelectionReason) == "" {
			return ErrInvalid
		}
		if _, ok := seen["resource:"+resource.ID]; ok {
			return fmt.Errorf("%w: duplicate resource %s", ErrInvalid, resource.ID)
		}
		seen["resource:"+resource.ID] = struct{}{}
	}
	toolNames := map[string]struct{}{}
	for _, tool := range b.Tools {
		if !idPattern.MatchString(strings.TrimSpace(tool.Name)) || !validDigest(tool.InputSchemaDigest) || !validDigest(tool.OutputSchemaDigest) || tool.SchemaVersion < 1 || !validSideEffectClass(tool.SideEffectClass) {
			return ErrInvalid
		}
		if _, ok := toolNames[tool.Name]; ok {
			return fmt.Errorf("%w: duplicate tool schema %s", ErrInvalid, tool.Name)
		}
		toolNames[tool.Name] = struct{}{}
		for _, capability := range tool.Capabilities {
			if strings.TrimSpace(capability) == "" || strings.ContainsAny(capability, "\r\n") {
				return ErrInvalid
			}
		}
	}
	for _, schema := range b.Schemas {
		if !idPattern.MatchString(strings.TrimSpace(schema.ID)) || !validDigest(schema.Digest) || strings.TrimSpace(schema.Version) == "" {
			return ErrInvalid
		}
		if _, ok := seen["schema:"+schema.ID]; ok {
			return fmt.Errorf("%w: duplicate schema %s", ErrInvalid, schema.ID)
		}
		seen["schema:"+schema.ID] = struct{}{}
	}
	for _, example := range b.Examples {
		if !idPattern.MatchString(strings.TrimSpace(example.ID)) || !validDigest(example.Digest) || strings.TrimSpace(example.MediaType) == "" {
			return ErrInvalid
		}
	}
	capabilities := map[string]struct{}{}
	for _, capability := range b.Capabilities {
		capability = strings.TrimSpace(capability)
		if capability == "" || strings.ContainsAny(capability, "\r\n") {
			return ErrInvalid
		}
		if _, ok := capabilities[capability]; ok {
			return fmt.Errorf("%w: duplicate capability %s", ErrInvalid, capability)
		}
		capabilities[capability] = struct{}{}
	}
	dependencies := map[string]struct{}{}
	for _, dependency := range b.Dependencies {
		if !idPattern.MatchString(strings.TrimSpace(dependency.ID)) || dependency.ID == b.ID || strings.TrimSpace(dependency.VersionConstraint) == "" {
			return ErrInvalid
		}
		if _, ok := dependencies[dependency.ID]; ok {
			return fmt.Errorf("%w: duplicate dependency %s", ErrInvalid, dependency.ID)
		}
		dependencies[dependency.ID] = struct{}{}
	}
	if b.Digest != "" {
		digest, err := b.CanonicalDigest()
		if err != nil || digest != b.Digest {
			return fmt.Errorf("%w: digest mismatch", ErrInvalid)
		}
	}
	return nil
}

func (b SkillBundle) CanonicalDigest() (string, error) {
	if b.SchemaVersion == 0 {
		b.SchemaVersion = BundleSchemaVersion
	}
	if err := b.validateWithoutDigest(); err != nil {
		return "", err
	}
	canonical := canonicalBundle(b)
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("canonical SkillBundle: %w", err)
	}
	hash := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}

func (b SkillBundle) Sign(privateKey ed25519.PrivateKey, keyID string) (SkillBundle, error) {
	if len(privateKey) != ed25519.PrivateKeySize || strings.TrimSpace(keyID) == "" {
		return SkillBundle{}, ErrInvalid
	}
	digest, err := b.CanonicalDigest()
	if err != nil {
		return SkillBundle{}, err
	}
	b.Digest = digest
	b.SignerKeyID = strings.TrimSpace(keyID)
	b.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(digest)))
	return b, b.Validate()
}

func (b SkillBundle) Verify(publicKey ed25519.PublicKey) error {
	if len(publicKey) != ed25519.PublicKeySize || strings.TrimSpace(b.Signature) == "" || strings.TrimSpace(b.SignerKeyID) == "" {
		return ErrQuarantined
	}
	digest, err := b.CanonicalDigest()
	if err != nil || digest != b.Digest {
		return ErrQuarantined
	}
	signature, err := base64.StdEncoding.DecodeString(b.Signature)
	if err != nil || !ed25519.Verify(publicKey, []byte(digest), signature) {
		return ErrQuarantined
	}
	return nil
}

func (b SkillBundle) validateWithoutDigest() error {
	copy := b
	copy.Digest, copy.Signature, copy.SignerKeyID = "", "", ""
	return copy.Validate()
}

func canonicalBundle(b SkillBundle) any {
	copy := b
	copy.Digest, copy.Signature, copy.SignerKeyID = "", "", ""
	copy.Resources = append([]Resource(nil), copy.Resources...)
	sort.Slice(copy.Resources, func(i, j int) bool { return copy.Resources[i].ID < copy.Resources[j].ID })
	copy.Tools = append([]ToolSchema(nil), copy.Tools...)
	for i := range copy.Tools {
		copy.Tools[i].Capabilities = sortedUnique(copy.Tools[i].Capabilities)
	}
	sort.Slice(copy.Tools, func(i, j int) bool { return copy.Tools[i].Name < copy.Tools[j].Name })
	copy.Schemas = append([]SchemaRef(nil), copy.Schemas...)
	sort.Slice(copy.Schemas, func(i, j int) bool { return copy.Schemas[i].ID < copy.Schemas[j].ID })
	copy.Examples = append([]Example(nil), copy.Examples...)
	sort.Slice(copy.Examples, func(i, j int) bool { return copy.Examples[i].ID < copy.Examples[j].ID })
	copy.Capabilities = sortedUnique(copy.Capabilities)
	copy.Dependencies = append([]Dependency(nil), copy.Dependencies...)
	sort.Slice(copy.Dependencies, func(i, j int) bool { return copy.Dependencies[i].ID < copy.Dependencies[j].ID })
	return copy
}

func validDigest(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func validSideEffectClass(value string) bool {
	switch strings.TrimSpace(value) {
	case "read_only", "idempotent_write", "reconcilable_write", "non_retriable_write":
		return true
	default:
		return false
	}
}

func sortedUnique(values []string) []string {
	set := map[string]struct{}{}
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

func keyFor(bundle SkillBundle) string { return bundle.ID + "@" + bundle.Version }

func (r *Registry) Install(request InstallRequest) (Installation, error) {
	if r == nil {
		return Installation{}, ErrInvalid
	}
	bundle := request.Bundle
	if err := bundle.validateWithoutDigest(); err != nil {
		return Installation{}, err
	}
	digest, err := bundle.CanonicalDigest()
	if err != nil {
		return Installation{}, err
	}
	if bundle.Digest == "" {
		bundle.Digest = digest
	} else if bundle.Digest != digest {
		return Installation{}, ErrInvalid
	}
	if request.RequireSignature {
		if err := bundle.Verify(request.PublicKey); err != nil {
			return Installation{}, err
		}
	}
	if strings.TrimSpace(bundle.Source) != "built_in" && (strings.TrimSpace(bundle.Signature) == "" || strings.TrimSpace(bundle.SignerKeyID) == "") {
		return Installation{}, ErrQuarantined
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.revokedKeys[bundle.SignerKeyID] {
		return Installation{}, ErrRevoked
	}
	key := keyFor(bundle)
	if existing, ok := r.installations[key]; ok && existing.Bundle.Digest != bundle.Digest {
		return Installation{}, ErrConflict
	}
	now := r.now().UTC()
	installation := Installation{Bundle: bundle, State: StateInstalled, InstalledAt: now, UpdatedAt: now}
	if existing, ok := r.installations[key]; ok {
		installation.InstalledAt = existing.InstalledAt
	}
	r.installations[key] = installation
	r.appendEventLocked("skill.installed", installation, "")
	if err := r.persistLocked(); err != nil {
		return Installation{}, err
	}
	return cloneInstallation(installation), nil
}

func (r *Registry) Activate(id, version string, policy ActivationPolicy) (Installation, error) {
	if r == nil {
		return Installation{}, ErrInvalid
	}
	key := strings.TrimSpace(id) + "@" + strings.TrimSpace(version)
	r.mu.Lock()
	defer r.mu.Unlock()
	installation, ok := r.installations[key]
	if !ok {
		return Installation{}, ErrNotFound
	}
	if installation.State == StateQuarantined || installation.State == StateRevoked {
		return Installation{}, ErrQuarantined
	}
	if policy.MaxTokens < 1 || installation.Bundle.MaxTokens > policy.MaxTokens {
		return Installation{}, fmt.Errorf("%w: token budget", ErrCapabilityDenied)
	}
	if len(policy.AllowedSources) > 0 && !contains(policy.AllowedSources, installation.Bundle.Source) {
		return Installation{}, fmt.Errorf("%w: source", ErrCapabilityDenied)
	}
	if policy.RequireSignature && installation.Bundle.Source != "built_in" {
		if installation.Bundle.Signature == "" || installation.Bundle.SignerKeyID == "" {
			return Installation{}, ErrQuarantined
		}
	}
	if r.revokedKeys[installation.Bundle.SignerKeyID] {
		installation.State, installation.Reason = StateRevoked, "signing_key_revoked"
		installation.UpdatedAt = r.now().UTC()
		r.installations[key] = installation
		_ = r.persistLocked()
		return Installation{}, ErrRevoked
	}
	for _, capability := range installation.Bundle.Capabilities {
		if !capabilityGranted(capability, policy.GrantedCapabilities) {
			return Installation{}, fmt.Errorf("%w: %s", ErrCapabilityDenied, capability)
		}
	}
	if err := r.validateDependenciesLocked(installation.Bundle.ID, installation.Bundle.Version, map[string]bool{}, map[string]bool{}); err != nil {
		return Installation{}, err
	}
	installation.State, installation.ActivatedAt, installation.UpdatedAt, installation.Reason = StateActive, r.now().UTC(), r.now().UTC(), ""
	r.installations[key] = installation
	r.appendEventLocked("skill.activated", installation, "")
	if err := r.persistLocked(); err != nil {
		return Installation{}, err
	}
	return cloneInstallation(installation), nil
}

func (r *Registry) Disable(id, version, reason string) (Installation, error) {
	return r.transition(id, version, StateDisabled, reason)
}

func (r *Registry) Quarantine(id, version, reason string) (Installation, error) {
	return r.transition(id, version, StateQuarantined, reason)
}

func (r *Registry) transition(id, version string, state State, reason string) (Installation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := strings.TrimSpace(id) + "@" + strings.TrimSpace(version)
	installation, ok := r.installations[key]
	if !ok {
		return Installation{}, ErrNotFound
	}
	if state != StateDisabled && state != StateQuarantined {
		return Installation{}, ErrInvalid
	}
	installation.State, installation.Reason, installation.UpdatedAt = state, strings.TrimSpace(reason), r.now().UTC()
	if state == StateQuarantined {
		installation.QuarantinedAt = installation.UpdatedAt
	}
	r.installations[key] = installation
	r.appendEventLocked("skill."+string(state), installation, installation.Reason)
	if err := r.persistLocked(); err != nil {
		return Installation{}, err
	}
	return cloneInstallation(installation), nil
}

func (r *Registry) RevokeKey(keyID, reason string) error {
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.revokedKeys[keyID] = true
	for key, installation := range r.installations {
		if installation.Bundle.SignerKeyID != keyID {
			continue
		}
		installation.State, installation.Reason, installation.UpdatedAt = StateRevoked, strings.TrimSpace(reason), r.now().UTC()
		r.installations[key] = installation
		r.appendEventLocked("skill.revoked", installation, installation.Reason)
	}
	return r.persistLocked()
}

func (r *Registry) Get(id, version string) (Installation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	installation, ok := r.installations[strings.TrimSpace(id)+"@"+strings.TrimSpace(version)]
	if !ok {
		return Installation{}, ErrNotFound
	}
	return cloneInstallation(installation), nil
}

func (r *Registry) Freeze(id, version string) (FrozenRevision, error) {
	installation, err := r.Get(id, version)
	if err != nil {
		return FrozenRevision{}, err
	}
	if installation.State != StateActive {
		return FrozenRevision{}, ErrNotActive
	}
	return FrozenRevision{Bundle: cloneBundle(installation.Bundle), Digest: installation.Bundle.Digest}, nil
}

func (r *Registry) Events() []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Event(nil), r.events...)
}

func (r *Registry) validateDependenciesLocked(id, version string, visiting, visited map[string]bool) error {
	key := id + "@" + version
	if visiting[key] {
		return fmt.Errorf("%w: dependency cycle at %s", ErrDependency, key)
	}
	if visited[key] {
		return nil
	}
	installation, ok := r.installations[key]
	if !ok {
		return fmt.Errorf("%w: %s", ErrDependency, key)
	}
	visiting[key] = true
	for _, dependency := range installation.Bundle.Dependencies {
		candidate, ok := r.findDependencyLocked(dependency)
		if !ok {
			return fmt.Errorf("%w: %s %s", ErrDependency, dependency.ID, dependency.VersionConstraint)
		}
		if candidate.State == StateQuarantined || candidate.State == StateRevoked {
			return fmt.Errorf("%w: dependency %s is not trusted", ErrDependency, dependency.ID)
		}
		if err := r.validateDependenciesLocked(candidate.Bundle.ID, candidate.Bundle.Version, visiting, visited); err != nil {
			return err
		}
	}
	delete(visiting, key)
	visited[key] = true
	return nil
}

func (r *Registry) findDependencyLocked(dependency Dependency) (Installation, bool) {
	keys := make([]string, 0)
	for key, installation := range r.installations {
		if installation.Bundle.ID == dependency.ID && installation.State != StateDisabled {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return Installation{}, false
	}
	return r.installations[keys[0]], true
}

func (r *Registry) appendEventLocked(eventType string, installation Installation, reason string) {
	r.events = append(r.events, Event{Sequence: int64(len(r.events) + 1), Type: eventType, BundleID: installation.Bundle.ID, Version: installation.Bundle.Version, Revision: installation.Bundle.Revision, Digest: installation.Bundle.Digest, Reason: reason, CreatedAt: r.now().UTC()})
}

func (r *Registry) persistLocked() error {
	if r.path == "" {
		return nil
	}
	state := persistedState{Version: 1, Installations: r.installations, Events: r.events, RevokedKeys: r.revokedKeys}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".skills-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, r.path)
}

func cloneBundle(bundle SkillBundle) SkillBundle {
	bundle.Resources = append([]Resource(nil), bundle.Resources...)
	bundle.Tools = append([]ToolSchema(nil), bundle.Tools...)
	for i := range bundle.Tools {
		bundle.Tools[i].Capabilities = append([]string(nil), bundle.Tools[i].Capabilities...)
	}
	bundle.Schemas = append([]SchemaRef(nil), bundle.Schemas...)
	bundle.Examples = append([]Example(nil), bundle.Examples...)
	bundle.Capabilities = append([]string(nil), bundle.Capabilities...)
	bundle.Dependencies = append([]Dependency(nil), bundle.Dependencies...)
	return bundle
}

func cloneInstallation(installation Installation) Installation {
	installation.Bundle = cloneBundle(installation.Bundle)
	return installation
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func capabilityGranted(required string, grants []string) bool {
	required = strings.TrimSpace(required)
	for _, grant := range grants {
		grant = strings.TrimSpace(grant)
		if grant == required || strings.HasSuffix(grant, "*") && strings.HasPrefix(required, strings.TrimSuffix(grant, "*")) {
			return true
		}
	}
	return false
}
