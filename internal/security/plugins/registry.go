package plugins

import (
	"context"
	"crypto/ed25519"
	"fmt"
	extensionport "github.com/adro-project/adro/ports/extensions"
	"sort"
	"strings"
	"sync"
	"time"
)

func (m ExecutionMode) Valid() bool {
	switch m {
	case ExecutionInProcess, ExecutionOutOfProcess, ExecutionWASI:
		return true
	default:
		return false
	}
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
