package plugins

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

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
