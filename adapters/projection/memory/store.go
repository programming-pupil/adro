// Package memory provides a deterministic reference ProjectionStore. It is a
// read-model adapter only; callers must supply source stream and sequence.
package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/ports/projection"
	"github.com/adro-project/adro/ports/scope"
)

type Clock func() time.Time

type Store struct {
	mu    sync.RWMutex
	now   Clock
	items map[string]projection.Record
}

func New(now Clock) *Store {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Store{now: now, items: map[string]projection.Record{}}
}

func (s *Store) Get(ctx context.Context, tenantID, projectionName, key string) (projection.Record, error) {
	if err := ctx.Err(); err != nil {
		return projection.Record{}, err
	}
	if err := requireTenant(ctx, tenantID); err != nil {
		return projection.Record{}, err
	}
	storageKey, err := validateKey(tenantID, projectionName, key)
	if err != nil {
		return projection.Record{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.items[storageKey]
	if !ok {
		return projection.Record{}, projection.ErrNotFound
	}
	return clone(item), nil
}

func (s *Store) Put(ctx context.Context, item projection.Record, expectedVersion int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := requireTenant(ctx, item.TenantID); err != nil {
		return err
	}
	key, err := validateKey(item.TenantID, item.Projection, item.Key)
	if err != nil {
		return err
	}
	if item.SourceSequence < 1 || strings.TrimSpace(item.SourceStream) == "" || expectedVersion < 0 {
		return errors.New("projection source and expected version are required")
	}
	if item.Digest == "" {
		hash := sha256.Sum256(item.Payload)
		item.Digest = hex.EncodeToString(hash[:])
	}
	if !validDigest(item.Digest) {
		return errors.New("projection digest is invalid")
	}
	hash := sha256.Sum256(item.Payload)
	if item.Digest != hex.EncodeToString(hash[:]) {
		return errors.New("projection digest mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.items[key]
	if !exists {
		if expectedVersion != 0 || item.Version != 1 {
			return projection.ErrConflict
		}
	} else {
		if current.TenantID != item.TenantID {
			return projection.ErrTenantMismatch
		}
		if current.Version != expectedVersion || item.Version != current.Version+1 {
			return projection.ErrConflict
		}
	}
	item.UpdatedAt = s.now().UTC()
	s.items[key] = clone(item)
	return nil
}

func (s *Store) Delete(ctx context.Context, tenantID, projectionName, key string, expectedVersion int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := requireTenant(ctx, tenantID); err != nil {
		return err
	}
	storageKey, err := validateKey(tenantID, projectionName, key)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[storageKey]
	if !ok {
		return projection.ErrNotFound
	}
	if item.Version != expectedVersion {
		return projection.ErrConflict
	}
	delete(s.items, storageKey)
	return nil
}

func (s *Store) List(ctx context.Context, tenantID, projectionName string) ([]projection.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := requireTenant(ctx, tenantID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(projectionName) == "" {
		return nil, errors.New("tenant and projection are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]projection.Record, 0)
	prefix := strings.TrimSpace(tenantID) + "\x00" + strings.TrimSpace(projectionName) + "\x00"
	for key, item := range s.items {
		if strings.HasPrefix(key, prefix) {
			result = append(result, clone(item))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}

func validateKey(tenantID, projectionName, key string) (string, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(projectionName) == "" || strings.TrimSpace(key) == "" || strings.ContainsAny(tenantID+projectionName+key, "\x00\r\n") {
		return "", errors.New("projection tenant, name and key are required")
	}
	return strings.TrimSpace(tenantID) + "\x00" + strings.TrimSpace(projectionName) + "\x00" + strings.TrimSpace(key), nil
}

func requireTenant(ctx context.Context, tenantID string) error {
	scoped, err := scope.Tenant(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(tenantID) != scoped {
		return projection.ErrTenantMismatch
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func clone(item projection.Record) projection.Record {
	item.Payload = append([]byte(nil), item.Payload...)
	return item
}

var _ projection.Store = (*Store)(nil)
