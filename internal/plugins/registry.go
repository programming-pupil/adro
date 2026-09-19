// Package plugins owns the control-plane registry for independently packaged
// adapters. Installation requires a trusted publisher key, a canonical signed
// manifest, and explicit capability declarations. Registry state is durable;
// key revocation and health failures quarantine affected installations.
package plugins

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	corepolicy "github.com/adro-project/adro/core/policy"
	"github.com/adro-project/adro/internal/security"
	extensionport "github.com/adro-project/adro/ports/extensions"
)

const (
	registrySchemaVersion  = 2
	maxManifestMessageSize = 16 << 20
)

var (
	ErrNotFound      = errors.New("plugin not found")
	ErrConflict      = errors.New("plugin already installed")
	ErrUnverified    = errors.New("plugin manifest signature is not verified")
	ErrInvalid       = errors.New("invalid plugin manifest")
	ErrNotActive     = errors.New("plugin is not active")
	ErrKeyNotTrusted = errors.New("plugin signing key is not trusted")
	ErrKeyRevoked    = errors.New("plugin signing key is revoked")
	ErrNoRollback    = errors.New("plugin has no compatible rollback target")
	ErrIncompatible  = errors.New("plugin versions are incompatible")
)

type ExecutionMode string

const (
	ExecutionInProcess    ExecutionMode = "in_process"
	ExecutionOutOfProcess ExecutionMode = "out_of_process"
	ExecutionWASI         ExecutionMode = "wasi"
)

func (m ExecutionMode) Valid() bool {
	switch m {
	case ExecutionInProcess, ExecutionOutOfProcess, ExecutionWASI:
		return true
	default:
		return false
	}
}

type FilePermission struct {
	Path       string   `json:"path"`
	Operations []string `json:"operations"`
}

type NetworkPermission struct {
	Domain   string `json:"domain,omitempty"`
	IP       string `json:"ip,omitempty"`
	CIDR     string `json:"cidr,omitempty"`
	Ports    []int  `json:"ports"`
	Protocol string `json:"protocol"`
	Purpose  string `json:"purpose"`
}

type SecretPermission struct {
	Name        string `json:"name"`
	Destination string `json:"destination"`
	Purpose     string `json:"purpose"`
}

type DataEgressPermission struct {
	Destination    string               `json:"destination"`
	Purpose        string               `json:"purpose"`
	MaxSensitivity security.Sensitivity `json:"max_sensitivity"`
}

type Manifest struct {
	ID                      string                 `json:"id"`
	Name                    string                 `json:"name"`
	Publisher               string                 `json:"publisher"`
	Version                 string                 `json:"version"`
	ProtocolVersion         string                 `json:"protocol_version"`
	AdapterVersion          string                 `json:"adapter_version"`
	SchemaDigest            string                 `json:"schema_digest"`
	ArtifactDigest          string                 `json:"artifact_digest"`
	ExecutionMode           ExecutionMode          `json:"execution_mode"`
	MaxMessageBytes         int64                  `json:"max_message_bytes"`
	MinPlatform             string                 `json:"min_platform_version,omitempty"`
	MaxPlatform             string                 `json:"max_platform_version,omitempty"`
	Capabilities            []string               `json:"capabilities"`
	Permissions             []string               `json:"permissions,omitempty"`
	FilePermissions         []FilePermission       `json:"file_permissions,omitempty"`
	NetworkPermissions      []NetworkPermission    `json:"network_permissions,omitempty"`
	SecretPermissions       []SecretPermission     `json:"secret_permissions,omitempty"`
	DataEgressPermissions   []DataEgressPermission `json:"data_egress_permissions,omitempty"`
	CompatibleSchemaDigests []string               `json:"compatible_schema_digests,omitempty"`
}

type Installation struct {
	Manifest          Manifest  `json:"manifest"`
	TenantID          string    `json:"tenant_id"`
	WorkspaceID       string    `json:"workspace_id"`
	Digest            string    `json:"digest"`
	Signature         string    `json:"signature"`
	KeyID             string    `json:"key_id"`
	PublicKey         string    `json:"public_key,omitempty"`
	State             string    `json:"state"`
	HealthMessage     string    `json:"health_message,omitempty"`
	ConsecutiveErrors int       `json:"consecutive_errors"`
	InstalledAt       time.Time `json:"installed_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	ActivatedAt       time.Time `json:"activated_at,omitempty"`
	SupersededAt      time.Time `json:"superseded_at,omitempty"`
	LastHealthAt      time.Time `json:"last_health_at,omitempty"`
	Legacy            bool      `json:"legacy,omitempty"`
}

type InstallRequest struct {
	Manifest    Manifest `json:"manifest"`
	TenantID    string   `json:"tenant_id,omitempty"`
	WorkspaceID string   `json:"workspace_id,omitempty"`
	Digest      string   `json:"digest"`
	Signature   string   `json:"signature"`
	KeyID       string   `json:"key_id,omitempty"`
	// PublicKey is accepted only to derive KeyID for clients migrating from the
	// original API. The key must already exist in the registry trust store.
	PublicKey string `json:"public_key,omitempty"`
}

type KeyState string

const (
	KeyActive  KeyState = "active"
	KeyRetired KeyState = "retired"
	KeyRevoked KeyState = "revoked"
)

type TrustedKey struct {
	ID             string    `json:"id"`
	Publisher      string    `json:"publisher"`
	PublicKey      string    `json:"public_key"`
	State          KeyState  `json:"state"`
	AllowInProcess bool      `json:"allow_in_process"`
	AddedAt        time.Time `json:"added_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Replaces       string    `json:"replaces,omitempty"`
	RevokedAt      time.Time `json:"revoked_at,omitempty"`
	Reason         string    `json:"reason,omitempty"`
}

type TrustKeyRequest struct {
	ID             string `json:"id,omitempty"`
	Publisher      string `json:"publisher"`
	PublicKey      string `json:"public_key"`
	AllowInProcess bool   `json:"allow_in_process,omitempty"`
}

type KeyRotationRequest struct {
	CurrentKeyID   string `json:"current_key_id"`
	NewKeyID       string `json:"new_key_id,omitempty"`
	NewPublicKey   string `json:"new_public_key"`
	AllowInProcess bool   `json:"allow_in_process,omitempty"`
	Signature      string `json:"signature"`
}

type registryState struct {
	SchemaVersion int                     `json:"schema_version"`
	Items         map[string]Installation `json:"items"`
	Keys          map[string]TrustedKey   `json:"keys"`
	Active        map[string]string       `json:"active"`
	History       map[string][]string     `json:"history"`
}

type Registry struct {
	mu      sync.RWMutex
	path    string
	items   map[string]Installation
	keys    map[string]TrustedKey
	active  map[string]string
	history map[string][]string
	now     func() time.Time
}

func (r *Registry) Durable() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.path != ""
}

func New(statePath string) (*Registry, error) {
	r := &Registry{
		path: strings.TrimSpace(statePath), items: map[string]Installation{}, keys: map[string]TrustedKey{},
		active: map[string]string{}, history: map[string][]string{}, now: func() time.Time { return time.Now().UTC() },
	}
	if r.path == "" {
		return r, nil
	}
	data, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read plugin registry: %w", err)
	}
	if err := r.decodeState(data); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Registry) decodeState(data []byte) error {
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if json.Unmarshal(data, &header) == nil && header.SchemaVersion == registrySchemaVersion {
		var state registryState
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("decode plugin registry: %w", err)
		}
		if state.Items != nil {
			r.items = state.Items
		}
		if state.Keys != nil {
			r.keys = state.Keys
		}
		if state.Active != nil {
			r.active = state.Active
		}
		if state.History != nil {
			r.history = state.History
		}
		return r.validateLoadedState()
	}

	// Version 1 stored only installations and trusted each embedded public key.
	// Preserve visibility but quarantine every entry until an administrator
	// explicitly enrolls a publisher key and reinstalls the artifact.
	var legacy map[string]Installation
	if err := json.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("decode plugin registry: %w", err)
	}
	now := r.now()
	for key, item := range legacy {
		if item.WorkspaceID == "" {
			item.WorkspaceID = "local"
		}
		if item.TenantID == "" {
			item.TenantID = item.WorkspaceID
		}
		storageKey := key
		if !strings.Contains(storageKey, "\x00") {
			storageKey = item.WorkspaceID + "\x00" + item.Manifest.ID + "@" + item.Manifest.Version
		}
		item.State = "quarantined"
		item.HealthMessage = "legacy embedded signing key is not a trusted publisher key"
		item.UpdatedAt = now
		item.Legacy = true
		if item.KeyID == "" && item.PublicKey != "" {
			if publicKey, err := decodePublicKey(item.PublicKey); err == nil {
				item.KeyID = keyID(publicKey)
			}
		}
		r.items[storageKey] = item
	}
	return nil
}

func (r *Registry) validateLoadedState() error {
	for id, key := range r.keys {
		if id != key.ID {
			return fmt.Errorf("validate plugin key %s: key id mismatch", id)
		}
		if err := validateTrustedKey(key); err != nil {
			return fmt.Errorf("validate plugin key %s: %w", id, err)
		}
	}
	for storageKey, item := range r.items {
		if item.Legacy {
			if item.State != "quarantined" {
				return fmt.Errorf("validate plugin %s: legacy installation is not quarantined", storageKey)
			}
			continue
		}
		if err := r.validateInstallation(item); err != nil {
			return fmt.Errorf("validate plugin %s: %w", storageKey, err)
		}
	}
	for slot, installationKey := range r.active {
		item, ok := r.items[installationKey]
		if !ok || item.State != "active" && item.State != "degraded" || slot != activeSlot(item.WorkspaceID, item.Manifest.ID) {
			return fmt.Errorf("validate active plugin slot %s", slot)
		}
	}
	for storageKey, item := range r.items {
		if item.State != "active" && item.State != "degraded" {
			continue
		}
		if r.active[activeSlot(item.WorkspaceID, item.Manifest.ID)] != storageKey {
			return fmt.Errorf("validate schedulable plugin %s: active slot mismatch", storageKey)
		}
	}
	for slot, history := range r.history {
		if strings.TrimSpace(slot) == "" {
			return errors.New("plugin history contains an empty slot")
		}
		for _, installationKey := range history {
			if _, ok := r.items[installationKey]; !ok {
				return fmt.Errorf("plugin history %s references unknown installation %s", slot, installationKey)
			}
		}
	}
	return nil
}

func (r *Registry) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.persistLocked()
}

func (r *Registry) TrustKey(request TrustKeyRequest) (TrustedKey, error) {
	publisher := strings.TrimSpace(request.Publisher)
	if !canonicalIdentifier(publisher, 256) {
		return TrustedKey{}, fmt.Errorf("%w: publisher is required", ErrInvalid)
	}
	publicKey, err := decodePublicKey(request.PublicKey)
	if err != nil {
		return TrustedKey{}, err
	}
	id := strings.TrimSpace(request.ID)
	derivedID := keyID(publicKey)
	if id == "" {
		id = derivedID
	}
	if id != derivedID {
		return TrustedKey{}, fmt.Errorf("%w: key id does not match public key", ErrInvalid)
	}
	now := r.now()
	key := TrustedKey{
		ID: id, Publisher: publisher, PublicKey: base64.StdEncoding.EncodeToString(publicKey), State: KeyActive,
		AllowInProcess: request.AllowInProcess, AddedAt: now, UpdatedAt: now,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.keys[id]; exists {
		return TrustedKey{}, ErrConflict
	}
	for _, existing := range r.keys {
		if existing.Publisher == publisher && existing.State == KeyActive {
			return TrustedKey{}, fmt.Errorf("%w: publisher already has an active key; use rotation", ErrConflict)
		}
	}
	r.keys[id] = key
	if err := r.persistLocked(); err != nil {
		delete(r.keys, id)
		return TrustedKey{}, fmt.Errorf("persist trusted plugin key: %w", err)
	}
	return key, nil
}

func (r *Registry) RotateKey(request KeyRotationRequest) (TrustedKey, error) {
	newPublicKey, err := decodePublicKey(request.NewPublicKey)
	if err != nil {
		return TrustedKey{}, err
	}
	newID := strings.TrimSpace(request.NewKeyID)
	derivedID := keyID(newPublicKey)
	if newID == "" {
		newID = derivedID
	}
	if newID != derivedID {
		return TrustedKey{}, fmt.Errorf("%w: new key id does not match public key", ErrInvalid)
	}
	currentID := strings.TrimSpace(request.CurrentKeyID)
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.keys[currentID]
	if !ok {
		return TrustedKey{}, ErrKeyNotTrusted
	}
	if current.State != KeyActive {
		return TrustedKey{}, ErrKeyRevoked
	}
	if _, exists := r.keys[newID]; exists {
		return TrustedKey{}, ErrConflict
	}
	currentPublicKey, err := decodePublicKey(current.PublicKey)
	if err != nil {
		return TrustedKey{}, err
	}
	signature, err := decodeSignatureBytes(request.Signature)
	if err != nil || !ed25519.Verify(currentPublicKey, []byte(rotationDigest(current, newID, request.NewPublicKey, request.AllowInProcess)), signature) {
		return TrustedKey{}, ErrUnverified
	}
	now := r.now()
	previous := current
	current.State, current.UpdatedAt = KeyRetired, now
	rotated := TrustedKey{
		ID: newID, Publisher: current.Publisher, PublicKey: base64.StdEncoding.EncodeToString(newPublicKey), State: KeyActive,
		AllowInProcess: request.AllowInProcess, AddedAt: now, UpdatedAt: now, Replaces: current.ID,
	}
	r.keys[current.ID] = current
	r.keys[newID] = rotated
	if err := r.persistLocked(); err != nil {
		r.keys[current.ID] = previous
		delete(r.keys, newID)
		return TrustedKey{}, fmt.Errorf("persist plugin key rotation: %w", err)
	}
	return rotated, nil
}

func (r *Registry) RevokeKey(keyID, reason string) (TrustedKey, error) {
	keyID, reason = strings.TrimSpace(keyID), strings.TrimSpace(reason)
	if keyID == "" || reason == "" {
		return TrustedKey{}, fmt.Errorf("%w: key id and revocation reason are required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key, ok := r.keys[keyID]
	if !ok {
		return TrustedKey{}, ErrKeyNotTrusted
	}
	if key.State == KeyRevoked {
		return key, nil
	}
	previousKey := key
	previousItems := make(map[string]Installation)
	previousActive := cloneStringMap(r.active)
	now := r.now()
	key.State, key.RevokedAt, key.UpdatedAt, key.Reason = KeyRevoked, now, now, reason
	r.keys[keyID] = key
	for storageKey, item := range r.items {
		if item.KeyID != keyID {
			continue
		}
		previousItems[storageKey] = item
		item.State, item.HealthMessage, item.UpdatedAt = "quarantined", "signing key revoked: "+reason, now
		r.items[storageKey] = item
		delete(r.active, activeSlot(item.WorkspaceID, item.Manifest.ID))
	}
	if err := r.persistLocked(); err != nil {
		r.keys[keyID] = previousKey
		for storageKey, item := range previousItems {
			r.items[storageKey] = item
		}
		r.active = previousActive
		return TrustedKey{}, fmt.Errorf("persist plugin key revocation: %w", err)
	}
	return key, nil
}

func (r *Registry) ListKeys() []TrustedKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]TrustedKey, 0, len(r.keys))
	for _, key := range r.keys {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
	return keys
}

func (r *Registry) Install(request InstallRequest) (Installation, error) {
	if err := validateManifest(request.Manifest); err != nil {
		return Installation{}, err
	}
	digest, err := manifestDigest(request.Manifest)
	if err != nil {
		return Installation{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(request.Digest), digest) {
		return Installation{}, fmt.Errorf("%w: digest mismatch", ErrUnverified)
	}
	keyID := strings.TrimSpace(request.KeyID)
	if keyID == "" && strings.TrimSpace(request.PublicKey) != "" {
		publicKey, keyErr := decodePublicKey(request.PublicKey)
		if keyErr != nil {
			return Installation{}, keyErr
		}
		keyID = keyIDForBytes(publicKey)
	}
	signature, err := decodeSignatureBytes(request.Signature)
	if err != nil {
		return Installation{}, ErrUnverified
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	trustedKey, ok := r.keys[keyID]
	if !ok {
		return Installation{}, ErrKeyNotTrusted
	}
	if trustedKey.State != KeyActive {
		return Installation{}, ErrKeyRevoked
	}
	if trustedKey.Publisher != request.Manifest.Publisher {
		return Installation{}, fmt.Errorf("%w: publisher does not own signing key", ErrUnverified)
	}
	if request.Manifest.ExecutionMode == ExecutionInProcess && !trustedKey.AllowInProcess {
		return Installation{}, fmt.Errorf("%w: signing key is not approved for in-process adapters", ErrInvalid)
	}
	publicKey, err := decodePublicKey(trustedKey.PublicKey)
	if err != nil || !ed25519.Verify(publicKey, []byte(digest), signature) {
		return Installation{}, ErrUnverified
	}
	now := r.now()
	tenantID := strings.TrimSpace(request.TenantID)
	workspaceID := strings.TrimSpace(request.WorkspaceID)
	if workspaceID == "" {
		workspaceID = "local"
	}
	if tenantID == "" {
		tenantID = workspaceID
	}
	if !canonicalIdentifier(tenantID, 256) || !canonicalIdentifier(workspaceID, 256) {
		return Installation{}, fmt.Errorf("%w: canonical tenant and workspace are required", ErrInvalid)
	}
	item := Installation{
		Manifest: cloneManifest(request.Manifest), TenantID: tenantID, WorkspaceID: workspaceID,
		Digest: digest, Signature: request.Signature, KeyID: keyID, PublicKey: trustedKey.PublicKey,
		State: "verified", InstalledAt: now, UpdatedAt: now,
	}
	storageKey := installationKey(workspaceID, request.Manifest.ID, request.Manifest.Version)
	if _, exists := r.items[storageKey]; exists {
		return Installation{}, ErrConflict
	}
	r.items[storageKey] = item
	if err := r.persistLocked(); err != nil {
		delete(r.items, storageKey)
		return Installation{}, fmt.Errorf("persist plugin installation: %w", err)
	}
	return cloneInstallation(item), nil
}

func (r *Registry) Activate(id string) (Installation, error) {
	return r.activate(id, "")
}

func (r *Registry) ActivateForWorkspace(workspaceID, id string) (Installation, error) {
	return r.activate(id, strings.TrimSpace(workspaceID))
}

func (r *Registry) activate(id, workspaceID string) (Installation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	storageKey, item, ok := r.lookupLocked(id, workspaceID)
	if !ok {
		return Installation{}, ErrNotFound
	}
	if item.Legacy || (item.State != "verified" && item.State != "degraded" && item.State != "superseded" && item.State != "rolled_back") {
		return Installation{}, ErrNotActive
	}
	key, ok := r.keys[item.KeyID]
	if !ok {
		return Installation{}, ErrKeyNotTrusted
	}
	if key.State == KeyRevoked {
		return Installation{}, ErrKeyRevoked
	}
	previousItem := item
	previousActive := cloneStringMap(r.active)
	previousHistory := cloneStringSliceMap(r.history)
	now := r.now()
	slot := activeSlot(item.WorkspaceID, item.Manifest.ID)
	currentKey := r.active[slot]
	var previousCurrent Installation
	if currentKey != "" && currentKey != storageKey {
		previousCurrent = r.items[currentKey]
		current := previousCurrent
		current.State, current.SupersededAt, current.UpdatedAt = "superseded", now, now
		r.items[currentKey] = current
	}
	item.State, item.UpdatedAt, item.ActivatedAt, item.SupersededAt = "active", now, now, time.Time{}
	r.items[storageKey] = item
	r.active[slot] = storageKey
	history := r.history[slot]
	if len(history) == 0 || history[len(history)-1] != storageKey {
		r.history[slot] = append(history, storageKey)
	}
	if err := r.persistLocked(); err != nil {
		r.items[storageKey] = previousItem
		if currentKey != "" && currentKey != storageKey {
			r.items[currentKey] = previousCurrent
		}
		r.active = previousActive
		r.history = previousHistory
		return Installation{}, fmt.Errorf("persist plugin activation: %w", err)
	}
	return cloneInstallation(item), nil
}

func (r *Registry) RollbackForWorkspace(workspaceID, pluginID string) (Installation, error) {
	workspaceID, pluginID = strings.TrimSpace(workspaceID), strings.TrimSpace(pluginID)
	if workspaceID == "" || pluginID == "" {
		return Installation{}, fmt.Errorf("%w: workspace and plugin id are required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	slot := activeSlot(workspaceID, pluginID)
	history := append([]string(nil), r.history[slot]...)
	if len(history) < 2 {
		return Installation{}, ErrNoRollback
	}
	currentKey := r.active[slot]
	if currentKey == "" || history[len(history)-1] != currentKey {
		return Installation{}, ErrNoRollback
	}
	current := r.items[currentKey]
	var targetKey string
	for index := len(history) - 2; index >= 0; index-- {
		candidateKey := history[index]
		candidate := r.items[candidateKey]
		key := r.keys[candidate.KeyID]
		if candidate.Legacy || key.State == KeyRevoked || !manifestsCompatible(current.Manifest, candidate.Manifest) {
			continue
		}
		targetKey = candidateKey
		history = history[:index+1]
		break
	}
	if targetKey == "" {
		return Installation{}, ErrNoRollback
	}
	previousCurrent, previousTarget := current, r.items[targetKey]
	previousActive := cloneStringMap(r.active)
	previousHistory := cloneStringSliceMap(r.history)
	now := r.now()
	current.State, current.UpdatedAt, current.SupersededAt = "rolled_back", now, now
	target := r.items[targetKey]
	target.State, target.UpdatedAt, target.ActivatedAt, target.SupersededAt = "active", now, now, time.Time{}
	r.items[currentKey], r.items[targetKey] = current, target
	r.active[slot], r.history[slot] = targetKey, history
	if err := r.persistLocked(); err != nil {
		r.items[currentKey], r.items[targetKey] = previousCurrent, previousTarget
		r.active, r.history = previousActive, previousHistory
		return Installation{}, fmt.Errorf("persist plugin rollback: %w", err)
	}
	return cloneInstallation(target), nil
}

func (r *Registry) ActiveForWorkspace(workspaceID, pluginID string) (Installation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	storageKey := r.active[activeSlot(strings.TrimSpace(workspaceID), strings.TrimSpace(pluginID))]
	item, ok := r.items[storageKey]
	if !ok || item.State != "active" {
		return Installation{}, ErrNotActive
	}
	return cloneInstallation(item), nil
}

// AuthorizeExecution resolves a caller-supplied installation identity to the
// canonical execution grant stored by the registry. Caller-supplied
// capabilities and permissions are never returned: the signed stored manifest
// is the only source of execution policy.
func (r *Registry) AuthorizeExecution(ctx context.Context, item extensionport.Installation) (extensionport.Installation, error) {
	if r == nil {
		return extensionport.Installation{}, ErrKeyNotTrusted
	}
	if err := contextError(ctx); err != nil {
		return extensionport.Installation{}, err
	}
	if strings.TrimSpace(item.Manifest.ID) == "" || strings.TrimSpace(item.Manifest.Version) == "" ||
		strings.TrimSpace(item.Digest) == "" || strings.TrimSpace(item.Signature) == "" || strings.TrimSpace(item.KeyID) == "" {
		return extensionport.Installation{}, ErrUnverified
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, stored, ok := r.lookupLocked(item.Manifest.ID+"@"+item.Manifest.Version, item.WorkspaceID)
	if !ok {
		return extensionport.Installation{}, ErrNotFound
	}
	if stored.Digest != item.Digest || stored.Signature != item.Signature || stored.KeyID != item.KeyID {
		return extensionport.Installation{}, ErrUnverified
	}
	if item.Manifest.ArtifactDigest != "" && stored.Manifest.ArtifactDigest != item.Manifest.ArtifactDigest {
		return extensionport.Installation{}, ErrUnverified
	}
	if stored.State != "active" {
		return extensionport.Installation{}, ErrNotActive
	}
	key, ok := r.keys[stored.KeyID]
	if !ok {
		return extensionport.Installation{}, ErrKeyNotTrusted
	}
	if key.State == KeyRevoked {
		return extensionport.Installation{}, ErrKeyRevoked
	}
	if err := r.validateInstallation(stored); err != nil {
		return extensionport.Installation{}, err
	}
	if stored.Manifest.ExecutionMode == ExecutionInProcess && !key.AllowInProcess {
		return extensionport.Installation{}, fmt.Errorf("%w: signing key is not approved for in-process adapters", ErrInvalid)
	}
	return toExecutionInstallation(stored), nil
}

// QuarantineExecution implements the runtime quarantine port without exposing
// registry persistence or mutable installation records to the runtime layer.
func (r *Registry) QuarantineExecution(ctx context.Context, item extensionport.Installation, reason string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	_, err := r.QuarantineForWorkspace(item.WorkspaceID, item.Manifest.ID+"@"+item.Manifest.Version, reason)
	return err
}

func (r *Registry) RecordHealth(id string, healthy bool, message string) (Installation, error) {
	return r.recordHealth(id, "", healthy, message)
}

func (r *Registry) RecordHealthForWorkspace(workspaceID, id string, healthy bool, message string) (Installation, error) {
	return r.recordHealth(id, strings.TrimSpace(workspaceID), healthy, message)
}

func (r *Registry) recordHealth(id, workspaceID string, healthy bool, message string) (Installation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	storageKey, item, ok := r.lookupLocked(id, workspaceID)
	if !ok {
		return Installation{}, ErrNotFound
	}
	previous := item
	previousActive := cloneStringMap(r.active)
	item.LastHealthAt = r.now()
	item.HealthMessage = strings.TrimSpace(message)
	if healthy {
		item.ConsecutiveErrors = 0
		if item.State == "degraded" {
			item.State = "active"
		}
	} else {
		item.ConsecutiveErrors++
		if item.ConsecutiveErrors >= 3 {
			item.State = "quarantined"
			delete(r.active, activeSlot(item.WorkspaceID, item.Manifest.ID))
		} else if item.State == "active" {
			item.State = "degraded"
		}
	}
	item.UpdatedAt = item.LastHealthAt
	r.items[storageKey] = item
	if err := r.persistLocked(); err != nil {
		r.items[storageKey] = previous
		r.active = previousActive
		return Installation{}, fmt.Errorf("persist plugin health: %w", err)
	}
	return cloneInstallation(item), nil
}

func (r *Registry) Quarantine(id, reason string) (Installation, error) {
	return r.quarantine(id, "", reason)
}

func (r *Registry) QuarantineForWorkspace(workspaceID, id, reason string) (Installation, error) {
	return r.quarantine(id, strings.TrimSpace(workspaceID), reason)
}

func (r *Registry) quarantine(id, workspaceID, reason string) (Installation, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Installation{}, fmt.Errorf("%w: quarantine reason is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	storageKey, item, ok := r.lookupLocked(id, workspaceID)
	if !ok {
		return Installation{}, ErrNotFound
	}
	previous := item
	previousActive := cloneStringMap(r.active)
	item.State, item.HealthMessage, item.UpdatedAt = "quarantined", reason, r.now()
	r.items[storageKey] = item
	delete(r.active, activeSlot(item.WorkspaceID, item.Manifest.ID))
	if err := r.persistLocked(); err != nil {
		r.items[storageKey] = previous
		r.active = previousActive
		return Installation{}, fmt.Errorf("persist plugin quarantine: %w", err)
	}
	return cloneInstallation(item), nil
}

func (r *Registry) Get(id string) (Installation, error) {
	return r.get(id, "")
}

func (r *Registry) GetForWorkspace(workspaceID, id string) (Installation, error) {
	return r.get(id, strings.TrimSpace(workspaceID))
}

func (r *Registry) get(id, workspaceID string) (Installation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, item, ok := r.lookupLocked(id, workspaceID)
	if !ok {
		return Installation{}, ErrNotFound
	}
	return cloneInstallation(item), nil
}

func (r *Registry) List() []Installation {
	return r.ListWorkspace("")
}

func (r *Registry) ListWorkspace(workspaceID string) []Installation {
	r.mu.RLock()
	defer r.mu.RUnlock()
	workspaceID = strings.TrimSpace(workspaceID)
	items := make([]Installation, 0, len(r.items))
	for _, item := range r.items {
		if workspaceID != "" && item.WorkspaceID != workspaceID {
			continue
		}
		items = append(items, cloneInstallation(item))
	}
	sort.Slice(items, func(i, j int) bool {
		left := items[i].Manifest.ID + "@" + items[i].Manifest.Version
		right := items[j].Manifest.ID + "@" + items[j].Manifest.Version
		return left < right
	})
	return items
}

func validateManifest(manifest Manifest) error {
	for name, value := range map[string]string{
		"id": manifest.ID, "name": manifest.Name, "publisher": manifest.Publisher,
		"version": manifest.Version, "protocol_version": manifest.ProtocolVersion,
		"adapter_version": manifest.AdapterVersion,
	} {
		if !canonicalIdentifier(value, 256) {
			return fmt.Errorf("%w: canonical %s is required", ErrInvalid, name)
		}
	}
	if !manifest.ExecutionMode.Valid() {
		return fmt.Errorf("%w: invalid execution mode", ErrInvalid)
	}
	if !validDigest(manifest.SchemaDigest) || !validDigest(manifest.ArtifactDigest) {
		return fmt.Errorf("%w: schema and artifact digests must be sha256 digests", ErrInvalid)
	}
	if manifest.MaxMessageBytes < 1024 || manifest.MaxMessageBytes > maxManifestMessageSize {
		return fmt.Errorf("%w: max_message_bytes is outside 1024..%d", ErrInvalid, maxManifestMessageSize)
	}
	if err := validateStringSet("capabilities", manifest.Capabilities, true); err != nil {
		return err
	}
	if err := validateStringSet("permissions", manifest.Permissions, false); err != nil {
		return err
	}
	for index, permission := range manifest.FilePermissions {
		if err := validateFilePermission(permission); err != nil {
			return fmt.Errorf("%w: file permission %d: %v", ErrInvalid, index, err)
		}
	}
	for index, permission := range manifest.NetworkPermissions {
		if err := validateNetworkPermission(permission); err != nil {
			return fmt.Errorf("%w: network permission %d: %v", ErrInvalid, index, err)
		}
	}
	for index, permission := range manifest.SecretPermissions {
		if !canonicalIdentifier(permission.Name, 256) || !canonicalIdentifier(permission.Destination, 256) || !canonicalIdentifier(permission.Purpose, 256) {
			return fmt.Errorf("%w: secret permission %d is incomplete", ErrInvalid, index)
		}
	}
	for index, permission := range manifest.DataEgressPermissions {
		if err := validateEgressPermission(permission); err != nil {
			return fmt.Errorf("%w: data egress permission %d: %v", ErrInvalid, index, err)
		}
	}
	if err := validateNetworkEgressBindings(manifest); err != nil {
		return err
	}
	if err := validateStringSet("compatible_schema_digests", manifest.CompatibleSchemaDigests, false); err != nil {
		return err
	}
	for _, digest := range manifest.CompatibleSchemaDigests {
		if !validDigest(digest) {
			return fmt.Errorf("%w: invalid compatible schema digest", ErrInvalid)
		}
	}
	return nil
}

func manifestDigest(manifest Manifest) (string, error) {
	if err := validateManifest(manifest); err != nil {
		return "", err
	}
	canonical := cloneManifest(manifest)
	sort.Strings(canonical.Capabilities)
	sort.Strings(canonical.Permissions)
	sort.Strings(canonical.CompatibleSchemaDigests)
	for index := range canonical.FilePermissions {
		sort.Strings(canonical.FilePermissions[index].Operations)
	}
	sort.Slice(canonical.FilePermissions, func(i, j int) bool { return canonical.FilePermissions[i].Path < canonical.FilePermissions[j].Path })
	sort.Slice(canonical.NetworkPermissions, func(i, j int) bool {
		return permissionKey(canonical.NetworkPermissions[i]) < permissionKey(canonical.NetworkPermissions[j])
	})
	sort.Slice(canonical.SecretPermissions, func(i, j int) bool {
		left, right := canonical.SecretPermissions[i], canonical.SecretPermissions[j]
		return left.Name+"\x00"+left.Destination+"\x00"+left.Purpose < right.Name+"\x00"+right.Destination+"\x00"+right.Purpose
	})
	sort.Slice(canonical.DataEgressPermissions, func(i, j int) bool {
		left, right := canonical.DataEgressPermissions[i], canonical.DataEgressPermissions[j]
		return left.Destination+"\x00"+left.Purpose < right.Destination+"\x00"+right.Purpose
	})
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ManifestDigest returns the canonical digest that publishers sign. Sorting
// and normalization are owned by the registry so clients do not need to
// reproduce implementation-specific JSON ordering.
func ManifestDigest(manifest Manifest) (string, error) {
	return manifestDigest(manifest)
}

// SigningKeyID derives the stable key identifier used by trust, install and
// revocation APIs from a base64-encoded Ed25519 public key.
func SigningKeyID(publicKey string) (string, error) {
	decoded, err := decodePublicKey(publicKey)
	if err != nil {
		return "", err
	}
	return keyID(decoded), nil
}

// KeyRotationDigest returns the canonical digest an active key must sign to
// authorize its replacement.
func KeyRotationDigest(publisher, currentKeyID, newKeyID, newPublicKey string, allowInProcess bool) (string, error) {
	if !canonicalIdentifier(publisher, 256) || !canonicalIdentifier(currentKeyID, 256) {
		return "", fmt.Errorf("%w: publisher and current key id are required", ErrInvalid)
	}
	decoded, err := decodePublicKey(newPublicKey)
	if err != nil {
		return "", err
	}
	derivedID := keyID(decoded)
	if strings.TrimSpace(newKeyID) == "" {
		newKeyID = derivedID
	}
	if newKeyID != derivedID {
		return "", fmt.Errorf("%w: new key id does not match public key", ErrInvalid)
	}
	return rotationDigest(TrustedKey{ID: currentKeyID, Publisher: publisher}, newKeyID, newPublicKey, allowInProcess), nil
}

func (r *Registry) validateInstallation(item Installation) error {
	if err := validateManifest(item.Manifest); err != nil {
		return err
	}
	if !canonicalIdentifier(item.TenantID, 256) || !canonicalIdentifier(item.WorkspaceID, 256) {
		return fmt.Errorf("%w: tenant_id and workspace_id are required", ErrInvalid)
	}
	digest, err := manifestDigest(item.Manifest)
	if err != nil || !strings.EqualFold(item.Digest, digest) {
		return ErrUnverified
	}
	key, ok := r.keys[item.KeyID]
	if !ok {
		return ErrKeyNotTrusted
	}
	if key.State == KeyRevoked && item.State != "quarantined" {
		return ErrKeyRevoked
	}
	publicKey, err := decodePublicKey(key.PublicKey)
	signature, signatureErr := decodeSignatureBytes(item.Signature)
	if err != nil || signatureErr != nil || !ed25519.Verify(publicKey, []byte(digest), signature) || key.Publisher != item.Manifest.Publisher {
		return ErrUnverified
	}
	switch item.State {
	case "verified", "active", "degraded", "quarantined", "superseded", "rolled_back":
		return nil
	default:
		return fmt.Errorf("%w: invalid state", ErrInvalid)
	}
}

func validateTrustedKey(key TrustedKey) error {
	if !canonicalIdentifier(key.ID, 256) || !canonicalIdentifier(key.Publisher, 256) || key.AddedAt.IsZero() || key.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: trusted key metadata is incomplete", ErrInvalid)
	}
	publicKey, err := decodePublicKey(key.PublicKey)
	if err != nil || key.ID != keyID(publicKey) {
		return fmt.Errorf("%w: trusted key material does not match id", ErrInvalid)
	}
	switch key.State {
	case KeyActive, KeyRetired:
		if !key.RevokedAt.IsZero() {
			return fmt.Errorf("%w: non-revoked key has revoked_at", ErrInvalid)
		}
	case KeyRevoked:
		if key.RevokedAt.IsZero() || strings.TrimSpace(key.Reason) == "" {
			return fmt.Errorf("%w: revoked key lacks evidence", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: invalid key state", ErrInvalid)
	}
	return nil
}

func validateFilePermission(permission FilePermission) error {
	value := strings.TrimSpace(permission.Path)
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return errors.New("path must be a portable workspace-relative path")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != value {
		return errors.New("path must be canonical and cannot escape the workspace")
	}
	if len(permission.Operations) == 0 {
		return errors.New("at least one file operation is required")
	}
	seen := map[string]struct{}{}
	for _, operation := range permission.Operations {
		operation = strings.TrimSpace(operation)
		switch operation {
		case "read", "write", "create", "delete", "execute":
		default:
			return fmt.Errorf("unsupported operation %q", operation)
		}
		if _, duplicate := seen[operation]; duplicate {
			return fmt.Errorf("duplicate operation %q", operation)
		}
		seen[operation] = struct{}{}
	}
	return nil
}

func validateNetworkPermission(permission NetworkPermission) error {
	selectors := 0
	if permission.Domain = strings.TrimSpace(strings.ToLower(permission.Domain)); permission.Domain != "" {
		selectors++
		if !validDomain(permission.Domain) {
			return errors.New("invalid domain")
		}
	}
	if permission.IP = strings.TrimSpace(permission.IP); permission.IP != "" {
		selectors++
		if net.ParseIP(permission.IP) == nil {
			return errors.New("invalid IP")
		}
	}
	if permission.CIDR = strings.TrimSpace(permission.CIDR); permission.CIDR != "" {
		selectors++
		if _, _, err := net.ParseCIDR(permission.CIDR); err != nil {
			return errors.New("invalid CIDR")
		}
	}
	if selectors != 1 || len(permission.Ports) == 0 || !canonicalIdentifier(permission.Purpose, 256) {
		return errors.New("exactly one selector, ports, and purpose are required")
	}
	switch strings.ToLower(strings.TrimSpace(permission.Protocol)) {
	case "tcp", "udp", "http", "https":
	default:
		return errors.New("invalid protocol")
	}
	seen := map[int]struct{}{}
	for _, port := range permission.Ports {
		if port < 1 || port > 65535 {
			return fmt.Errorf("port %d is outside 1..65535", port)
		}
		if _, duplicate := seen[port]; duplicate {
			return fmt.Errorf("duplicate port %d", port)
		}
		seen[port] = struct{}{}
	}
	return nil
}

func validateEgressPermission(permission DataEgressPermission) error {
	if !canonicalIdentifier(permission.Purpose, 256) || !permission.MaxSensitivity.Valid() {
		return errors.New("purpose and max sensitivity are required")
	}
	parsed, err := url.Parse(strings.TrimSpace(permission.Destination))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("destination must be an absolute credential-free https URL without query or fragment")
	}
	return nil
}

func validateNetworkEgressBindings(manifest Manifest) error {
	networkRules := make([]corepolicy.NetworkRule, len(manifest.NetworkPermissions))
	for index, permission := range manifest.NetworkPermissions {
		networkRules[index] = corepolicy.NetworkRule{
			Domain: permission.Domain, IP: permission.IP, CIDR: permission.CIDR,
			Ports: append([]int(nil), permission.Ports...), Protocol: permission.Protocol, Purpose: permission.Purpose,
		}
	}
	egressRules := make([]corepolicy.EgressRule, len(manifest.DataEgressPermissions))
	for index, permission := range manifest.DataEgressPermissions {
		egressRules[index] = corepolicy.EgressRule{
			Destination: permission.Destination, Purpose: permission.Purpose,
			MaxSensitivity: corepolicy.Sensitivity(permission.MaxSensitivity),
		}
	}
	if err := corepolicy.ValidateNetworkEgress(networkRules, egressRules); err != nil {
		return fmt.Errorf("%w: network/data-egress binding: %v", ErrInvalid, err)
	}
	return nil
}

func validateStringSet(name string, values []string, required bool) error {
	if required && len(values) == 0 {
		return fmt.Errorf("%w: at least one %s value is required", ErrInvalid, name)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !canonicalIdentifier(value, 256) {
			return fmt.Errorf("%w: %s contains an invalid value", ErrInvalid, name)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%w: %s contains duplicate %q", ErrInvalid, name, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func manifestsCompatible(current, candidate Manifest) bool {
	if current.ID != candidate.ID || current.ProtocolVersion != candidate.ProtocolVersion {
		return false
	}
	if current.SchemaDigest == candidate.SchemaDigest {
		return true
	}
	for _, digest := range current.CompatibleSchemaDigests {
		if digest == candidate.SchemaDigest {
			return true
		}
	}
	return false
}

func rotationDigest(current TrustedKey, newID, newPublicKey string, allowInProcess bool) string {
	payload, _ := json.Marshal(struct {
		Type           string `json:"type"`
		Publisher      string `json:"publisher"`
		CurrentKeyID   string `json:"current_key_id"`
		NewKeyID       string `json:"new_key_id"`
		NewPublicKey   string `json:"new_public_key"`
		AllowInProcess bool   `json:"allow_in_process"`
	}{"adro.plugin.key-rotation.v1", current.Publisher, current.ID, newID, strings.TrimSpace(newPublicKey), allowInProcess})
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func decodePublicKey(value string) (ed25519.PublicKey, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, ErrUnverified
	}
	return ed25519.PublicKey(key), nil
}

func decodeSignatureBytes(value string) ([]byte, error) {
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, ErrUnverified
	}
	return signature, nil
}

func keyID(publicKey ed25519.PublicKey) string { return keyIDForBytes(publicKey) }
func keyIDForBytes(publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	return "ed25519:" + hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func canonicalIdentifier(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}

func validDomain(value string) bool {
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
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

func permissionKey(permission NetworkPermission) string {
	ports := make([]int, len(permission.Ports))
	copy(ports, permission.Ports)
	sort.Ints(ports)
	parts := make([]string, len(ports))
	for index, port := range ports {
		parts[index] = fmt.Sprintf("%05d", port)
	}
	return permission.Domain + permission.IP + permission.CIDR + "\x00" + permission.Protocol + "\x00" + strings.Join(parts, ",") + "\x00" + permission.Purpose
}

func toExecutionInstallation(item Installation) extensionport.Installation {
	manifest := item.Manifest
	grant := extensionport.Installation{
		TenantID: item.TenantID, WorkspaceID: item.WorkspaceID, Digest: item.Digest,
		Signature: item.Signature, KeyID: item.KeyID, State: item.State, ActivatedAt: item.ActivatedAt,
		Manifest: extensionport.Manifest{
			ID: manifest.ID, Name: manifest.Name, Publisher: manifest.Publisher, Version: manifest.Version,
			ProtocolVersion: manifest.ProtocolVersion, AdapterVersion: manifest.AdapterVersion,
			SchemaDigest: manifest.SchemaDigest, ArtifactDigest: manifest.ArtifactDigest,
			ExecutionMode: extensionport.ExecutionMode(manifest.ExecutionMode), MaxMessageBytes: manifest.MaxMessageBytes,
			Capabilities: append([]string(nil), manifest.Capabilities...), Permissions: append([]string(nil), manifest.Permissions...),
			CompatibleSchemaDigests: append([]string(nil), manifest.CompatibleSchemaDigests...),
		},
	}
	grant.Manifest.FilePermissions = make([]extensionport.FilePermission, len(manifest.FilePermissions))
	for index, permission := range manifest.FilePermissions {
		grant.Manifest.FilePermissions[index] = extensionport.FilePermission{Path: permission.Path, Operations: append([]string(nil), permission.Operations...)}
	}
	grant.Manifest.NetworkPermissions = make([]extensionport.NetworkPermission, len(manifest.NetworkPermissions))
	for index, permission := range manifest.NetworkPermissions {
		grant.Manifest.NetworkPermissions[index] = extensionport.NetworkPermission{
			Domain: permission.Domain, IP: permission.IP, CIDR: permission.CIDR,
			Ports: append([]int(nil), permission.Ports...), Protocol: permission.Protocol, Purpose: permission.Purpose,
		}
	}
	grant.Manifest.SecretPermissions = make([]extensionport.SecretPermission, len(manifest.SecretPermissions))
	for index, permission := range manifest.SecretPermissions {
		grant.Manifest.SecretPermissions[index] = extensionport.SecretPermission{
			Name: permission.Name, Destination: permission.Destination, Purpose: permission.Purpose,
		}
	}
	grant.Manifest.DataEgressPermissions = make([]extensionport.DataEgressPermission, len(manifest.DataEgressPermissions))
	for index, permission := range manifest.DataEgressPermissions {
		grant.Manifest.DataEgressPermissions[index] = extensionport.DataEgressPermission{
			Destination: permission.Destination, Purpose: permission.Purpose, MaxSensitivity: string(permission.MaxSensitivity),
		}
	}
	return grant
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

func cloneManifest(manifest Manifest) Manifest {
	manifest.Capabilities = append([]string(nil), manifest.Capabilities...)
	manifest.Permissions = append([]string(nil), manifest.Permissions...)
	manifest.CompatibleSchemaDigests = append([]string(nil), manifest.CompatibleSchemaDigests...)
	manifest.FilePermissions = append([]FilePermission(nil), manifest.FilePermissions...)
	for index := range manifest.FilePermissions {
		manifest.FilePermissions[index].Operations = append([]string(nil), manifest.FilePermissions[index].Operations...)
	}
	manifest.NetworkPermissions = append([]NetworkPermission(nil), manifest.NetworkPermissions...)
	for index := range manifest.NetworkPermissions {
		manifest.NetworkPermissions[index].Ports = append([]int(nil), manifest.NetworkPermissions[index].Ports...)
	}
	manifest.SecretPermissions = append([]SecretPermission(nil), manifest.SecretPermissions...)
	manifest.DataEgressPermissions = append([]DataEgressPermission(nil), manifest.DataEgressPermissions...)
	return manifest
}

func cloneInstallation(item Installation) Installation {
	item.Manifest = cloneManifest(item.Manifest)
	return item
}

func (r *Registry) lookupLocked(id, workspaceID string) (string, Installation, bool) {
	id = strings.TrimSpace(id)
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID != "" {
		storageKey := workspaceID + "\x00" + id
		item, ok := r.items[storageKey]
		return storageKey, item, ok
	}
	if item, ok := r.items[id]; ok {
		return id, item, true
	}
	var foundKey string
	var found Installation
	for storageKey, item := range r.items {
		if item.Manifest.ID+"@"+item.Manifest.Version != id {
			continue
		}
		if foundKey != "" {
			return "", Installation{}, false
		}
		foundKey, found = storageKey, item
	}
	return foundKey, found, foundKey != ""
}

func installationKey(workspaceID, pluginID, version string) string {
	return workspaceID + "\x00" + pluginID + "@" + version
}

func activeSlot(workspaceID, pluginID string) string {
	return workspaceID + "\x00" + pluginID
}

func cloneStringMap(values map[string]string) map[string]string {
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func cloneStringSliceMap(values map[string][]string) map[string][]string {
	copy := make(map[string][]string, len(values))
	for key, value := range values {
		copy[key] = append([]string(nil), value...)
	}
	return copy
}

func (r *Registry) persistLocked() error {
	if r.path == "" {
		return nil
	}
	state := registryState{SchemaVersion: registrySchemaVersion, Items: r.items, Keys: r.keys, Active: r.active, History: r.history}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".adro-plugins-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
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
	if err := os.Rename(tmpName, r.path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

var _ extensionport.Authorizer = (*Registry)(nil)
var _ extensionport.Quarantiner = (*Registry)(nil)
