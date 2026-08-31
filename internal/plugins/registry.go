// Package plugins owns the control-plane registry for independently packaged
// adapters. A plugin is not executable merely because it is configured: its
// manifest digest and signature must verify before activation, and repeated
// health failures quarantine it so new work is not routed to a broken binary.
package plugins

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
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound   = errors.New("plugin not found")
	ErrConflict   = errors.New("plugin already installed")
	ErrUnverified = errors.New("plugin manifest signature is not verified")
	ErrInvalid    = errors.New("invalid plugin manifest")
	ErrNotActive  = errors.New("plugin is not active")
)

type Manifest struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	ProtocolVersion string   `json:"protocol_version"`
	MinPlatform     string   `json:"min_platform_version,omitempty"`
	MaxPlatform     string   `json:"max_platform_version,omitempty"`
	Capabilities    []string `json:"capabilities"`
	Permissions     []string `json:"permissions"`
}

type Installation struct {
	Manifest          Manifest  `json:"manifest"`
	TenantID          string    `json:"tenant_id"`
	WorkspaceID       string    `json:"workspace_id"`
	Digest            string    `json:"digest"`
	Signature         string    `json:"signature"`
	PublicKey         string    `json:"public_key"`
	State             string    `json:"state"`
	HealthMessage     string    `json:"health_message,omitempty"`
	ConsecutiveErrors int       `json:"consecutive_errors"`
	InstalledAt       time.Time `json:"installed_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	LastHealthAt      time.Time `json:"last_health_at,omitempty"`
}

type InstallRequest struct {
	Manifest    Manifest `json:"manifest"`
	TenantID    string   `json:"tenant_id,omitempty"`
	WorkspaceID string   `json:"workspace_id,omitempty"`
	Digest      string   `json:"digest"`
	Signature   string   `json:"signature"`
	PublicKey   string   `json:"public_key"`
}

type Registry struct {
	mu    sync.RWMutex
	path  string
	items map[string]Installation
}

// Durable reports whether installations survive an API restart.
func (r *Registry) Durable() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.path != ""
}

func New(path string) (*Registry, error) {
	r := &Registry{path: strings.TrimSpace(path), items: map[string]Installation{}}
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
	if err := json.Unmarshal(data, &r.items); err != nil {
		return nil, fmt.Errorf("decode plugin registry: %w", err)
	}
	if r.items == nil {
		r.items = map[string]Installation{}
	}
	for id, item := range r.items {
		// State files created before workspace scoping are safely migrated into
		// the local bootstrap workspace on first load.
		if item.WorkspaceID == "" {
			item.WorkspaceID = "local"
		}
		if item.TenantID == "" {
			item.TenantID = item.WorkspaceID
		}
		if !strings.Contains(id, "\x00") {
			delete(r.items, id)
			r.items[item.WorkspaceID+"\x00"+id] = item
		}
		if err := validateInstallation(item); err != nil {
			return nil, fmt.Errorf("validate plugin %s: %w", id, err)
		}
	}
	return r, nil
}

func (r *Registry) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.persistLocked()
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
	publicKey, signature, err := decodeSignature(request.PublicKey, request.Signature)
	if err != nil || !ed25519.Verify(publicKey, []byte(digest), signature) {
		return Installation{}, ErrUnverified
	}
	now := time.Now().UTC()
	tenantID := strings.TrimSpace(request.TenantID)
	workspaceID := strings.TrimSpace(request.WorkspaceID)
	if workspaceID == "" {
		workspaceID = "local"
	}
	if tenantID == "" {
		tenantID = workspaceID
	}
	item := Installation{Manifest: cloneManifest(request.Manifest), TenantID: tenantID, WorkspaceID: workspaceID, Digest: digest, Signature: request.Signature, PublicKey: request.PublicKey, State: "verified", InstalledAt: now, UpdatedAt: now}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := workspaceID + "\x00" + request.Manifest.ID + "@" + request.Manifest.Version
	if _, exists := r.items[key]; exists {
		return Installation{}, ErrConflict
	}
	r.items[key] = item
	if err := r.persistLocked(); err != nil {
		delete(r.items, key)
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
	key, item, ok := r.lookupLocked(id, workspaceID)
	if !ok {
		return Installation{}, ErrNotFound
	}
	if item.State != "verified" && item.State != "degraded" {
		return Installation{}, ErrNotActive
	}
	previous := item
	item.State, item.UpdatedAt = "active", time.Now().UTC()
	r.items[key] = item
	if err := r.persistLocked(); err != nil {
		r.items[key] = previous
		return Installation{}, fmt.Errorf("persist plugin activation: %w", err)
	}
	return cloneInstallation(item), nil
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
	key, item, ok := r.lookupLocked(id, workspaceID)
	if !ok {
		return Installation{}, ErrNotFound
	}
	previous := item
	item.LastHealthAt = time.Now().UTC()
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
		} else if item.State == "active" {
			item.State = "degraded"
		}
	}
	item.UpdatedAt = item.LastHealthAt
	r.items[key] = item
	if err := r.persistLocked(); err != nil {
		r.items[key] = previous
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
	r.mu.Lock()
	defer r.mu.Unlock()
	key, item, ok := r.lookupLocked(id, workspaceID)
	if !ok {
		return Installation{}, ErrNotFound
	}
	previous := item
	item.State, item.HealthMessage, item.UpdatedAt = "quarantined", strings.TrimSpace(reason), time.Now().UTC()
	r.items[key] = item
	if err := r.persistLocked(); err != nil {
		r.items[key] = previous
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
	if strings.TrimSpace(manifest.ID) == "" || strings.TrimSpace(manifest.Name) == "" || strings.TrimSpace(manifest.Version) == "" || strings.TrimSpace(manifest.ProtocolVersion) == "" {
		return fmt.Errorf("%w: id, name, version and protocol_version are required", ErrInvalid)
	}
	if len(manifest.Capabilities) == 0 {
		return fmt.Errorf("%w: at least one capability is required", ErrInvalid)
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
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func decodeSignature(publicKey, signature string) (ed25519.PublicKey, []byte, error) {
	key, keyErr := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKey))
	sig, sigErr := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if keyErr != nil || sigErr != nil || len(key) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize {
		return nil, nil, ErrUnverified
	}
	return ed25519.PublicKey(key), sig, nil
}

func validateInstallation(item Installation) error {
	if err := validateManifest(item.Manifest); err != nil {
		return err
	}
	if strings.TrimSpace(item.TenantID) == "" || strings.TrimSpace(item.WorkspaceID) == "" {
		return fmt.Errorf("%w: tenant_id and workspace_id are required", ErrInvalid)
	}
	digest, err := manifestDigest(item.Manifest)
	if err != nil || !strings.EqualFold(item.Digest, digest) {
		return ErrUnverified
	}
	publicKey, signature, err := decodeSignature(item.PublicKey, item.Signature)
	if err != nil || !ed25519.Verify(publicKey, []byte(digest), signature) {
		return ErrUnverified
	}
	switch item.State {
	case "verified", "active", "degraded", "quarantined":
		return nil
	default:
		return fmt.Errorf("%w: invalid state", ErrInvalid)
	}
}

func cloneManifest(manifest Manifest) Manifest {
	manifest.Capabilities = append([]string(nil), manifest.Capabilities...)
	manifest.Permissions = append([]string(nil), manifest.Permissions...)
	return manifest
}

func cloneInstallation(item Installation) Installation {
	item.Manifest = cloneManifest(item.Manifest)
	return item
}

// lookupLocked accepts the public "plugin@version" identifier while keeping
// storage keys workspace-scoped. Empty workspaceID is retained only for the
// registry's backwards-compatible in-process API; HTTP routes always scope it.
func (r *Registry) lookupLocked(id, workspaceID string) (string, Installation, bool) {
	id = strings.TrimSpace(id)
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID != "" {
		key := workspaceID + "\x00" + id
		item, ok := r.items[key]
		return key, item, ok
	}
	if item, ok := r.items[id]; ok {
		return id, item, true
	}
	var foundKey string
	var found Installation
	for key, item := range r.items {
		if item.Manifest.ID+"@"+item.Manifest.Version != id {
			continue
		}
		if foundKey != "" {
			// Never choose arbitrarily when the same plugin exists in multiple
			// workspaces.
			return "", Installation{}, false
		}
		foundKey, found = key, item
	}
	return foundKey, found, foundKey != ""
}

func (r *Registry) persistLocked() error {
	if r.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(r.items, "", "  ")
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
	dirFile, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer dirFile.Close()
	return dirFile.Sync()
}
