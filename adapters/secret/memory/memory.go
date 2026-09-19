// Package memory implements a process-local SecretBroker for development and
// tests. It is deliberately not a production secret manager.
package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/ports/secretstore"
)

const defaultMaxTTL = 15 * time.Minute

// PutRequest binds stored material to one tenant and explicit destination and
// purpose allowlists. Empty allowlists are rejected rather than interpreted as
// wildcards.
type PutRequest struct {
	TenantID     string
	Value        []byte
	Destinations []string
	Purposes     []string
}

type storedSecret struct {
	tenantID     string
	value        []byte
	destinations map[string]struct{}
	purposes     map[string]struct{}
}

type storedLease struct {
	lease    secretstore.SecretLease
	material []byte
}

type Broker struct {
	mu      sync.Mutex
	secrets map[secretstore.SecretRef]storedSecret
	leases  map[string]storedLease
	now     func() time.Time
	maxTTL  time.Duration
}

func New() *Broker {
	return &Broker{
		secrets: make(map[secretstore.SecretRef]storedSecret),
		leases:  make(map[string]storedLease),
		now:     func() time.Time { return time.Now().UTC() },
		maxTTL:  defaultMaxTTL,
	}
}

// NewWithClock makes expiry and revocation tests deterministic.
func NewWithClock(now func() time.Time, maxTTL time.Duration) (*Broker, error) {
	if now == nil || maxTTL <= 0 {
		return nil, errors.New("clock and positive max ttl are required")
	}
	return &Broker{
		secrets: make(map[secretstore.SecretRef]storedSecret),
		leases:  make(map[string]storedLease),
		now:     now,
		maxTTL:  maxTTL,
	}, nil
}

// Put registers copied material under a newly generated opaque reference.
func (b *Broker) Put(request PutRequest) (secretstore.SecretRef, error) {
	tenantID := strings.TrimSpace(request.TenantID)
	if tenantID == "" || len(request.Value) == 0 {
		return "", fmt.Errorf("%w: tenant and secret value are required", secretstore.ErrInvalidRequest)
	}
	destinations, err := normalizedSet(request.Destinations)
	if err != nil {
		return "", fmt.Errorf("%w: destinations: %v", secretstore.ErrInvalidRequest, err)
	}
	purposes, err := normalizedSet(request.Purposes)
	if err != nil {
		return "", fmt.Errorf("%w: purposes: %v", secretstore.ErrInvalidRequest, err)
	}
	ref, err := secretstore.NewRef()
	if err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.secrets[ref] = storedSecret{
		tenantID: tenantID, value: append([]byte(nil), request.Value...),
		destinations: destinations, purposes: purposes,
	}
	return ref, nil
}

func (b *Broker) Resolve(ctx context.Context, request secretstore.SecretRequest) (secretstore.SecretLease, error) {
	if err := contextErr(ctx); err != nil {
		return secretstore.SecretLease{}, err
	}
	if err := request.Validate(b.maxTTL); err != nil {
		return secretstore.SecretLease{}, err
	}
	now := b.now().UTC()
	b.mu.Lock()
	defer b.mu.Unlock()
	stored, ok := b.secrets[request.Ref]
	if !ok {
		return secretstore.SecretLease{}, secretstore.ErrNotFound
	}
	if stored.tenantID != request.TenantID || !setContains(stored.destinations, request.Destination) || !setContains(stored.purposes, request.Purpose) {
		return secretstore.SecretLease{}, secretstore.ErrScopeMismatch
	}
	id, err := leaseID()
	if err != nil {
		return secretstore.SecretLease{}, err
	}
	lease := secretstore.SecretLease{
		ID: id, Ref: request.Ref, TenantID: request.TenantID, SessionID: request.SessionID,
		EffectID: request.EffectID, Destination: request.Destination, Purpose: request.Purpose,
		IssuedAt: now, ExpiresAt: now.Add(request.TTL),
	}
	b.leases[id] = storedLease{lease: lease, material: append([]byte(nil), stored.value...)}
	return lease, nil
}

func (b *Broker) Material(ctx context.Context, request secretstore.MaterialRequest) ([]byte, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	stored, ok := b.leases[request.LeaseID]
	if !ok {
		return nil, secretstore.ErrNotFound
	}
	if stored.lease.Revoked {
		return nil, secretstore.ErrLeaseRevoked
	}
	if !b.now().UTC().Before(stored.lease.ExpiresAt) {
		stored.lease.Revoked = true
		clear(stored.material)
		stored.material = nil
		b.leases[request.LeaseID] = stored
		return nil, secretstore.ErrLeaseExpired
	}
	lease := stored.lease
	if lease.TenantID != request.TenantID || lease.SessionID != request.SessionID || lease.EffectID != request.EffectID || lease.Destination != request.Destination || lease.Purpose != request.Purpose {
		return nil, secretstore.ErrScopeMismatch
	}
	if len(stored.material) == 0 {
		return nil, secretstore.ErrInvalidLease
	}
	return append([]byte(nil), stored.material...), nil
}

func (b *Broker) Revoke(ctx context.Context, id string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: lease id is required", secretstore.ErrInvalidRequest)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	stored, ok := b.leases[id]
	if !ok {
		return secretstore.ErrNotFound
	}
	stored.lease.Revoked = true
	clear(stored.material)
	stored.material = nil
	b.leases[id] = stored
	return nil
}

// PublicLease returns serializable metadata without material. Revoked and
// expired leases remain visible for audit, with Revoked set to true.
func (b *Broker) PublicLease(ctx context.Context, leaseID string) (secretstore.SecretLease, error) {
	if err := contextErr(ctx); err != nil {
		return secretstore.SecretLease{}, err
	}
	if strings.TrimSpace(leaseID) == "" {
		return secretstore.SecretLease{}, fmt.Errorf("%w: lease id is required", secretstore.ErrInvalidRequest)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	stored, ok := b.leases[leaseID]
	if !ok {
		return secretstore.SecretLease{}, secretstore.ErrNotFound
	}
	if !stored.lease.Revoked && !b.now().UTC().Before(stored.lease.ExpiresAt) {
		stored.lease.Revoked = true
		clear(stored.material)
		stored.material = nil
		b.leases[leaseID] = stored
	}
	return stored.lease, nil
}

func normalizedSet(values []string) (map[string]struct{}, error) {
	if len(values) == 0 {
		return nil, errors.New("at least one value is required")
	}
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, errors.New("empty value")
		}
		result[value] = struct{}{}
	}
	return result, nil
}

func setContains(values map[string]struct{}, value string) bool {
	_, ok := values[strings.TrimSpace(value)]
	return ok
}

func contextErr(ctx context.Context) error {
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

func leaseID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate secret lease id: %w", err)
	}
	return "lease:" + hex.EncodeToString(raw[:]), nil
}

// SortedScope is a diagnostics helper that never returns material.
func (b *Broker) SortedScope(ref secretstore.SecretRef) (destinations, purposes []string, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	stored, ok := b.secrets[ref]
	if !ok {
		return nil, nil, false
	}
	for value := range stored.destinations {
		destinations = append(destinations, value)
	}
	for value := range stored.purposes {
		purposes = append(purposes, value)
	}
	sort.Strings(destinations)
	sort.Strings(purposes)
	return destinations, purposes, true
}

var _ secretstore.SecretBroker = (*Broker)(nil)
