// Package secretstore defines opaque secret references and short-lived,
// revocable leases. Secret material is never part of a serializable lease.
package secretstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidRequest = errors.New("invalid secret request")
	ErrNotFound       = errors.New("secret reference not found")
	ErrLeaseExpired   = errors.New("secret lease expired")
	ErrLeaseRevoked   = errors.New("secret lease revoked")
	ErrScopeMismatch  = errors.New("secret lease scope mismatch")
	ErrInvalidLease   = errors.New("invalid secret lease")
)

// SecretRef is an opaque identifier. It never contains secret material.
type SecretRef string

func (r SecretRef) String() string { return string(r) }

func (r SecretRef) Valid() bool {
	value := string(r)
	if !strings.HasPrefix(value, "secret:") || len(value) <= len("secret:") || len(value) > 256 {
		return false
	}
	for _, ch := range value[len("secret:"):] {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			continue
		}
		switch ch {
		case '.', '_', '~', ':', '/', '@', '-':
			continue
		default:
			return false
		}
	}
	return true
}

func (r SecretRef) MarshalJSON() ([]byte, error) {
	if !r.Valid() {
		return nil, fmt.Errorf("%w: invalid secret reference", ErrInvalidRequest)
	}
	return json.Marshal(string(r))
}

func (r *SecretRef) UnmarshalJSON(data []byte) error {
	if r == nil {
		return fmt.Errorf("%w: nil secret reference", ErrInvalidRequest)
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("%w: secret reference must be a string", ErrInvalidRequest)
	}
	candidate := SecretRef(value)
	if !candidate.Valid() {
		return fmt.Errorf("%w: invalid secret reference", ErrInvalidRequest)
	}
	*r = candidate
	return nil
}

type SecretRequest struct {
	Ref         SecretRef     `json:"ref"`
	TenantID    string        `json:"tenant_id"`
	SessionID   string        `json:"session_id"`
	EffectID    string        `json:"effect_id"`
	Destination string        `json:"destination"`
	Purpose     string        `json:"purpose"`
	TTL         time.Duration `json:"ttl"`
}

func (r SecretRequest) Validate(maxTTL time.Duration) error {
	if !r.Ref.Valid() {
		return fmt.Errorf("%w: secret ref is invalid", ErrInvalidRequest)
	}
	if !canonicalNonempty(r.TenantID) || !canonicalNonempty(r.SessionID) || !canonicalNonempty(r.EffectID) {
		return fmt.Errorf("%w: canonical tenant, session, and effect are required", ErrInvalidRequest)
	}
	if !canonicalNonempty(r.Destination) || !canonicalNonempty(r.Purpose) {
		return fmt.Errorf("%w: canonical destination and purpose are required", ErrInvalidRequest)
	}
	if r.TTL <= 0 || (maxTTL > 0 && r.TTL > maxTTL) {
		return fmt.Errorf("%w: ttl is outside the allowed range", ErrInvalidRequest)
	}
	return nil
}

// SecretLease contains only public metadata. Material can be obtained only by
// presenting the complete bound scope to SecretBroker.Material.
type SecretLease struct {
	ID          string    `json:"id"`
	Ref         SecretRef `json:"ref"`
	TenantID    string    `json:"tenant_id"`
	SessionID   string    `json:"session_id"`
	EffectID    string    `json:"effect_id"`
	Destination string    `json:"destination"`
	Purpose     string    `json:"purpose"`
	IssuedAt    time.Time `json:"issued_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Revoked     bool      `json:"revoked,omitempty"`
}

func (l SecretLease) Validate(now time.Time) error {
	if !canonicalNonempty(l.ID) || !l.Ref.Valid() || !canonicalNonempty(l.TenantID) ||
		!canonicalNonempty(l.SessionID) || !canonicalNonempty(l.EffectID) ||
		!canonicalNonempty(l.Destination) || !canonicalNonempty(l.Purpose) ||
		l.IssuedAt.IsZero() || l.ExpiresAt.IsZero() || !l.ExpiresAt.After(l.IssuedAt) || now.IsZero() || now.Before(l.IssuedAt) {
		return ErrInvalidLease
	}
	if l.Revoked {
		return ErrLeaseRevoked
	}
	if !now.Before(l.ExpiresAt) {
		return ErrLeaseExpired
	}
	return nil
}

type MaterialRequest struct {
	LeaseID     string `json:"lease_id"`
	TenantID    string `json:"tenant_id"`
	SessionID   string `json:"session_id"`
	EffectID    string `json:"effect_id"`
	Destination string `json:"destination"`
	Purpose     string `json:"purpose"`
}

func (r MaterialRequest) Validate() error {
	if !canonicalNonempty(r.LeaseID) || !canonicalNonempty(r.TenantID) || !canonicalNonempty(r.SessionID) ||
		!canonicalNonempty(r.EffectID) || !canonicalNonempty(r.Destination) || !canonicalNonempty(r.Purpose) {
		return fmt.Errorf("%w: lease and complete canonical scope are required", ErrInvalidRequest)
	}
	return nil
}

func canonicalNonempty(value string) bool {
	return value != "" && value == strings.TrimSpace(value)
}

type SecretBroker interface {
	Resolve(context.Context, SecretRequest) (SecretLease, error)
	Material(context.Context, MaterialRequest) ([]byte, error)
	Revoke(context.Context, string) error
}

func NewRef() (SecretRef, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate secret reference: %w", err)
	}
	return SecretRef("secret:" + hex.EncodeToString(raw[:])), nil
}
