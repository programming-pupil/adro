package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
