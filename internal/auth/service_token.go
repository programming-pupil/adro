package auth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	coreencoding "github.com/adro-project/adro/core/encoding"
	"github.com/adro-project/adro/core/identity"
)

const (
	ServiceCredentialStateVersion = 1
	ServiceTokenAudienceAPI       = "adro-api"
	DefaultServiceTokenTTL        = 15 * time.Minute
	serviceTokenPrefix            = "adro-st1"
	maxServiceTokenBytes          = 16 << 10
)

var (
	ErrServiceTokenInvalid       = errors.New("service credential is invalid")
	ErrServiceTokenExpired       = errors.New("service credential expired")
	ErrServiceTokenRevoked       = errors.New("service credential revoked")
	ErrServiceKeyUnavailable     = errors.New("service signing key is unavailable")
	ErrServiceCredentialFileMode = errors.New("service credential file permissions are unsafe")
)

type ServiceKeyStatus string

const (
	ServiceKeyActive  ServiceKeyStatus = "active"
	ServiceKeyRetired ServiceKeyStatus = "retired"
	ServiceKeyRevoked ServiceKeyStatus = "revoked"
)

func (s ServiceKeyStatus) Valid() bool {
	return s == ServiceKeyActive || s == ServiceKeyRetired || s == ServiceKeyRevoked
}

type ServiceKey struct {
	ID         string           `json:"id"`
	Status     ServiceKeyStatus `json:"status"`
	PublicKey  []byte           `json:"public_key"`
	PrivateKey []byte           `json:"private_key,omitempty"`
	CreatedAt  time.Time        `json:"created_at"`
	RetiredAt  time.Time        `json:"retired_at,omitempty"`
	RevokedAt  time.Time        `json:"revoked_at,omitempty"`
}

type ServiceCredentialState struct {
	Version            int          `json:"version"`
	Keys               []ServiceKey `json:"keys"`
	RevokedCredentials []string     `json:"revoked_credentials,omitempty"`
}

type ServiceTokenIssueRequest struct {
	Type        identity.ActorType
	ID          string
	TenantID    string
	WorkspaceID string
	Audience    string
	TTL         time.Duration
	Delegation  []identity.Transition
}

type serviceTokenClaims struct {
	Version int            `json:"version"`
	KeyID   string         `json:"key_id"`
	Actor   identity.Actor `json:"actor"`
}

type ServiceCredentialAuthority struct {
	mu     sync.RWMutex
	state  ServiceCredentialState
	now    func() time.Time
	maxTTL time.Duration
	newID  func() (string, error)
}

func GenerateServiceCredentialState(keyID string, now time.Time) (ServiceCredentialState, error) {
	keyID = strings.TrimSpace(keyID)
	if !canonicalCredentialValue(keyID, 128) || now.IsZero() {
		return ServiceCredentialState{}, fmt.Errorf("%w: key id and creation time are required", ErrServiceTokenInvalid)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return ServiceCredentialState{}, fmt.Errorf("generate service signing key: %w", err)
	}
	return ServiceCredentialState{Version: ServiceCredentialStateVersion, Keys: []ServiceKey{{
		ID: keyID, Status: ServiceKeyActive, PublicKey: append([]byte(nil), publicKey...),
		PrivateKey: append([]byte(nil), privateKey...), CreatedAt: now.UTC().Truncate(time.Microsecond),
	}}}, nil
}

func NewServiceCredentialAuthority(state ServiceCredentialState, now func() time.Time, maxTTL time.Duration) (*ServiceCredentialAuthority, error) {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if maxTTL <= 0 {
		maxTTL = DefaultServiceTokenTTL
	}
	if err := validateServiceCredentialState(state); err != nil {
		return nil, err
	}
	return &ServiceCredentialAuthority{state: cloneServiceCredentialState(state), now: now, maxTTL: maxTTL, newID: newServiceCredentialID}, nil
}

func LoadServiceCredentialAuthority(path string, now func() time.Time, maxTTL time.Duration) (*ServiceCredentialAuthority, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("%w: state path is required", ErrServiceTokenInvalid)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read service credential state: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read service credential state: %w", err)
	}
	var state ServiceCredentialState
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return nil, fmt.Errorf("decode service credential state: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("decode service credential state: %w", err)
	}
	for _, key := range state.Keys {
		if len(key.PrivateKey) > 0 && info.Mode().Perm()&0o077 != 0 {
			return nil, ErrServiceCredentialFileMode
		}
	}
	return NewServiceCredentialAuthority(state, now, maxTTL)
}

func (a *ServiceCredentialAuthority) Save(path string) error {
	if a == nil {
		return ErrServiceKeyUnavailable
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("%w: state path is required", ErrServiceTokenInvalid)
	}
	data, err := encodeServiceCredentialState(a.Snapshot())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create service credential directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".service-credentials-*")
	if err != nil {
		return fmt.Errorf("create service credential state: %w", err)
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
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// SaveNew creates an authority file without an overwrite race. It is used for
// first-time key initialization, where replacing an authority created by a
// concurrent operator would permanently invalidate issued credentials.
func (a *ServiceCredentialAuthority) SaveNew(path string) error {
	if a == nil {
		return ErrServiceKeyUnavailable
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("%w: state path is required", ErrServiceTokenInvalid)
	}
	data, err := encodeServiceCredentialState(a.Snapshot())
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create service credential directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create service credential state: %w", err)
	}
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	remove = false
	return nil
}

func (a *ServiceCredentialAuthority) Issue(request ServiceTokenIssueRequest) (string, identity.Actor, error) {
	if a == nil {
		return "", identity.Actor{}, ErrServiceKeyUnavailable
	}
	if request.Type == identity.ActorHuman || !request.Type.Valid() {
		return "", identity.Actor{}, fmt.Errorf("%w: service credentials cannot represent a human", ErrServiceTokenInvalid)
	}
	if request.TTL <= 0 || request.TTL > a.maxTTL {
		return "", identity.Actor{}, fmt.Errorf("%w: ttl must be within 1..%s", ErrServiceTokenInvalid, a.maxTTL)
	}
	now := a.now().UTC().Truncate(time.Microsecond)
	credentialID, err := a.newID()
	if err != nil {
		return "", identity.Actor{}, err
	}
	actor := identity.Actor{
		Type: request.Type, ID: strings.TrimSpace(request.ID), TenantID: strings.TrimSpace(request.TenantID),
		WorkspaceID: strings.TrimSpace(request.WorkspaceID), AuthnMethod: "service_ed25519",
		CredentialID: credentialID, Audience: strings.TrimSpace(request.Audience), IssuedAt: now,
		ExpiresAt: now.Add(request.TTL).UTC().Truncate(time.Microsecond), Delegation: append([]identity.Transition(nil), request.Delegation...),
	}
	if err := actor.Validate(now, request.Audience); err != nil {
		return "", identity.Actor{}, err
	}

	a.mu.RLock()
	key, found := activeServiceKey(a.state.Keys)
	a.mu.RUnlock()
	if !found || len(key.PrivateKey) != ed25519.PrivateKeySize {
		return "", identity.Actor{}, ErrServiceKeyUnavailable
	}
	claims := serviceTokenClaims{Version: ServiceCredentialStateVersion, KeyID: key.ID, Actor: actor}
	payload, err := coreencoding.Marshal(claims)
	if err != nil {
		return "", identity.Actor{}, err
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(payload)
	signed := serviceTokenPrefix + "." + payloadPart
	signature := ed25519.Sign(ed25519.PrivateKey(key.PrivateKey), []byte(signed))
	token := signed + "." + base64.RawURLEncoding.EncodeToString(signature)
	return token, actor.Clone(), nil
}

func (a *ServiceCredentialAuthority) Verify(token, audience string) (identity.Actor, error) {
	if a == nil {
		return identity.Actor{}, ErrServiceKeyUnavailable
	}
	token = strings.TrimSpace(token)
	if token == "" || len(token) > maxServiceTokenBytes {
		return identity.Actor{}, ErrServiceTokenInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != serviceTokenPrefix {
		return identity.Actor{}, ErrServiceTokenInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) == 0 || len(payload) > maxServiceTokenBytes/2 {
		return identity.Actor{}, ErrServiceTokenInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != ed25519.SignatureSize {
		return identity.Actor{}, ErrServiceTokenInvalid
	}
	var claims serviceTokenClaims
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claims); err != nil || requireJSONEOF(decoder) != nil {
		return identity.Actor{}, ErrServiceTokenInvalid
	}
	canonical, err := coreencoding.Marshal(claims)
	if err != nil || !bytes.Equal(canonical, payload) || claims.Version != ServiceCredentialStateVersion {
		return identity.Actor{}, ErrServiceTokenInvalid
	}

	a.mu.RLock()
	key, found := serviceKeyByID(a.state.Keys, claims.KeyID)
	_, credentialRevoked := revokedCredentialSet(a.state.RevokedCredentials)[claims.Actor.CredentialID]
	a.mu.RUnlock()
	if !found || key.Status == ServiceKeyRevoked {
		return identity.Actor{}, ErrServiceTokenRevoked
	}
	if key.Status != ServiceKeyActive && key.Status != ServiceKeyRetired {
		return identity.Actor{}, ErrServiceTokenInvalid
	}
	if !ed25519.Verify(ed25519.PublicKey(key.PublicKey), []byte(parts[0]+"."+parts[1]), signature) {
		return identity.Actor{}, ErrServiceTokenInvalid
	}
	if credentialRevoked {
		return identity.Actor{}, ErrServiceTokenRevoked
	}
	now := a.now().UTC()
	if claims.Actor.AuthnMethod != "service_ed25519" || claims.Actor.Type == identity.ActorHuman ||
		claims.Actor.ExpiresAt.Sub(claims.Actor.IssuedAt) > a.maxTTL || claims.Actor.IssuedAt.Before(key.CreatedAt) ||
		(!key.RetiredAt.IsZero() && claims.Actor.IssuedAt.After(key.RetiredAt)) {
		return identity.Actor{}, ErrServiceTokenInvalid
	}
	if err := claims.Actor.Validate(now, strings.TrimSpace(audience)); err != nil {
		if errors.Is(err, identity.ErrCredentialExpired) {
			return identity.Actor{}, ErrServiceTokenExpired
		}
		return identity.Actor{}, ErrServiceTokenInvalid
	}
	return claims.Actor.Clone(), nil
}

func (a *ServiceCredentialAuthority) Rotate(currentKeyID, newKeyID string) error {
	if a == nil {
		return ErrServiceKeyUnavailable
	}
	state, err := GenerateServiceCredentialState(newKeyID, a.now())
	if err != nil {
		return err
	}
	newKey := state.Keys[0]
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, key := range a.state.Keys {
		if key.ID == newKey.ID {
			return fmt.Errorf("%w: duplicate key id", ErrServiceTokenInvalid)
		}
	}
	found := false
	now := a.now().UTC().Truncate(time.Microsecond)
	for index := range a.state.Keys {
		if a.state.Keys[index].ID != strings.TrimSpace(currentKeyID) {
			continue
		}
		if a.state.Keys[index].Status != ServiceKeyActive {
			return ErrServiceKeyUnavailable
		}
		a.state.Keys[index].Status = ServiceKeyRetired
		a.state.Keys[index].RetiredAt = now
		clear(a.state.Keys[index].PrivateKey)
		a.state.Keys[index].PrivateKey = nil
		found = true
		break
	}
	if !found {
		return ErrServiceKeyUnavailable
	}
	a.state.Keys = append(a.state.Keys, newKey)
	sort.Slice(a.state.Keys, func(i, j int) bool { return a.state.Keys[i].ID < a.state.Keys[j].ID })
	return nil
}

func (a *ServiceCredentialAuthority) RevokeKey(keyID string) error {
	if a == nil {
		return ErrServiceKeyUnavailable
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for index := range a.state.Keys {
		if a.state.Keys[index].ID == strings.TrimSpace(keyID) {
			if a.state.Keys[index].Status == ServiceKeyActive {
				return fmt.Errorf("%w: rotate the active key before revoking it", ErrServiceKeyUnavailable)
			}
			if a.state.Keys[index].Status == ServiceKeyRevoked {
				return nil
			}
			a.state.Keys[index].Status = ServiceKeyRevoked
			a.state.Keys[index].RevokedAt = a.now().UTC().Truncate(time.Microsecond)
			clear(a.state.Keys[index].PrivateKey)
			a.state.Keys[index].PrivateKey = nil
			return nil
		}
	}
	return ErrServiceKeyUnavailable
}

func (a *ServiceCredentialAuthority) RevokeCredential(credentialID string) error {
	credentialID = strings.TrimSpace(credentialID)
	if a == nil || !canonicalCredentialValue(credentialID, 256) {
		return ErrServiceTokenInvalid
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	set := revokedCredentialSet(a.state.RevokedCredentials)
	if _, exists := set[credentialID]; !exists {
		a.state.RevokedCredentials = append(a.state.RevokedCredentials, credentialID)
		sort.Strings(a.state.RevokedCredentials)
	}
	return nil
}

func (a *ServiceCredentialAuthority) Snapshot() ServiceCredentialState {
	if a == nil {
		return ServiceCredentialState{}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneServiceCredentialState(a.state)
}

func validateServiceCredentialState(state ServiceCredentialState) error {
	if state.Version != ServiceCredentialStateVersion || len(state.Keys) == 0 {
		return fmt.Errorf("%w: unsupported state version or empty key set", ErrServiceTokenInvalid)
	}
	seen := make(map[string]struct{}, len(state.Keys))
	active := 0
	for index, key := range state.Keys {
		if !canonicalCredentialValue(key.ID, 128) || !key.Status.Valid() || len(key.PublicKey) != ed25519.PublicKeySize || key.CreatedAt.IsZero() ||
			!key.CreatedAt.Equal(key.CreatedAt.UTC().Truncate(time.Microsecond)) {
			return fmt.Errorf("%w: key %d is invalid", ErrServiceTokenInvalid, index)
		}
		if _, duplicate := seen[key.ID]; duplicate {
			return fmt.Errorf("%w: duplicate key id", ErrServiceTokenInvalid)
		}
		seen[key.ID] = struct{}{}
		if len(key.PrivateKey) != 0 {
			if len(key.PrivateKey) != ed25519.PrivateKeySize || !bytes.Equal(ed25519.PrivateKey(key.PrivateKey).Public().(ed25519.PublicKey), key.PublicKey) || key.Status != ServiceKeyActive {
				return fmt.Errorf("%w: key %d private material is invalid", ErrServiceTokenInvalid, index)
			}
		}
		switch key.Status {
		case ServiceKeyActive:
			active++
			if !key.RetiredAt.IsZero() || !key.RevokedAt.IsZero() {
				return fmt.Errorf("%w: active key has terminal timestamps", ErrServiceTokenInvalid)
			}
		case ServiceKeyRetired:
			if key.RetiredAt.IsZero() || key.RetiredAt.Before(key.CreatedAt) || !key.RevokedAt.IsZero() {
				return fmt.Errorf("%w: retired key timestamps are invalid", ErrServiceTokenInvalid)
			}
		case ServiceKeyRevoked:
			if key.RevokedAt.IsZero() || key.RevokedAt.Before(key.CreatedAt) {
				return fmt.Errorf("%w: revoked key timestamp is invalid", ErrServiceTokenInvalid)
			}
		}
	}
	if active != 1 {
		return fmt.Errorf("%w: exactly one active key is required", ErrServiceTokenInvalid)
	}
	if len(revokedCredentialSet(state.RevokedCredentials)) != len(state.RevokedCredentials) {
		return fmt.Errorf("%w: duplicate revoked credential", ErrServiceTokenInvalid)
	}
	for _, id := range state.RevokedCredentials {
		if !canonicalCredentialValue(id, 256) {
			return fmt.Errorf("%w: invalid revoked credential", ErrServiceTokenInvalid)
		}
	}
	return nil
}

func activeServiceKey(keys []ServiceKey) (ServiceKey, bool) {
	for _, key := range keys {
		if key.Status == ServiceKeyActive {
			return cloneServiceKey(key), true
		}
	}
	return ServiceKey{}, false
}

func serviceKeyByID(keys []ServiceKey, id string) (ServiceKey, bool) {
	for _, key := range keys {
		if key.ID == id {
			return cloneServiceKey(key), true
		}
	}
	return ServiceKey{}, false
}

func revokedCredentialSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func cloneServiceCredentialState(state ServiceCredentialState) ServiceCredentialState {
	state.Keys = append([]ServiceKey(nil), state.Keys...)
	for index := range state.Keys {
		state.Keys[index] = cloneServiceKey(state.Keys[index])
	}
	state.RevokedCredentials = append([]string(nil), state.RevokedCredentials...)
	return state
}

func cloneServiceKey(key ServiceKey) ServiceKey {
	key.PublicKey = append([]byte(nil), key.PublicKey...)
	key.PrivateKey = append([]byte(nil), key.PrivateKey...)
	return key
}

func encodeServiceCredentialState(state ServiceCredentialState) ([]byte, error) {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode service credential state: %w", err)
	}
	return append(data, '\n'), nil
}

func newServiceCredentialID() (string, error) {
	var raw [16]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return "", fmt.Errorf("generate service credential id: %w", err)
	}
	return "credential:" + hex.EncodeToString(raw[:]), nil
}

func canonicalCredentialValue(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}
