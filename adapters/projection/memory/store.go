// Package memory provides a deterministic reference ProjectionStore. It is a
// read-model adapter only; callers must supply source stream and sequence.
package memory

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/adapters/projection/internal/contract"
	"github.com/adro-project/adro/ports/projection"
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
	if err := contract.RequireTenant(ctx, tenantID); err != nil {
		return projection.Record{}, err
	}
	storageKey, err := storageKey(tenantID, projectionName, key)
	if err != nil {
		return projection.Record{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.items[storageKey]
	if !ok {
		return projection.Record{}, projection.ErrNotFound
	}
	if err := contract.ValidateStored(item, tenantID, contract.DefaultMaxPayload); err != nil {
		return projection.Record{}, err
	}
	return clone(item), nil
}

func (s *Store) Put(ctx context.Context, item projection.Record, expectedVersion int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := contract.RequireTenant(ctx, item.TenantID); err != nil {
		return err
	}
	storageKey, err := storageKey(item.TenantID, item.Projection, item.Key)
	if err != nil {
		return err
	}
	if expectedVersion < 0 {
		return errors.New("projection source and expected version are required")
	}
	item = contract.Normalize(item, s.now())
	if item.Digest == "" {
		item.Digest = contract.Digest(item.Payload)
	}
	if err := contract.ValidateForWrite(item, item.TenantID, contract.DefaultMaxPayload); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.items[storageKey]
	if !exists {
		if expectedVersion != 0 || item.Version != 1 {
			return projection.ErrConflict
		}
	} else {
		if current.TenantID != item.TenantID {
			return projection.ErrTenantMismatch
		}
		if contract.Equivalent(current, item) && expectedVersion == current.Version {
			return nil
		}
		if current.SourceStream != item.SourceStream || item.SourceSequence <= current.SourceSequence {
			return projection.ErrConflict
		}
		if current.Version != expectedVersion || item.Version != current.Version+1 {
			return projection.ErrConflict
		}
	}
	item.UpdatedAt = s.now().UTC()
	s.items[storageKey] = clone(item)
	return nil
}

func (s *Store) Delete(ctx context.Context, tenantID, projectionName, key string, expectedVersion int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if expectedVersion < 1 {
		return projection.ErrConflict
	}
	if err := contract.RequireTenant(ctx, tenantID); err != nil {
		return err
	}
	storageKey, err := storageKey(tenantID, projectionName, key)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[storageKey]
	if !ok {
		return projection.ErrNotFound
	}
	if err := contract.ValidateStored(item, tenantID, contract.DefaultMaxPayload); err != nil {
		return err
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
	if err := contract.RequireTenant(ctx, tenantID); err != nil {
		return nil, err
	}
	if err := contract.ValidateIdentity(tenantID, projectionName, "list-key"); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]projection.Record, 0)
	prefix := strings.TrimSpace(tenantID) + "\x00" + strings.TrimSpace(projectionName) + "\x00"
	for key, item := range s.items {
		if strings.HasPrefix(key, prefix) {
			if err := contract.ValidateStored(item, tenantID, contract.DefaultMaxPayload); err != nil {
				return nil, err
			}
			result = append(result, clone(item))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}

// Close satisfies the common conformance backend contract. The in-memory
// adapter owns no external resources.
func (s *Store) Close() error { return nil }

func storageKey(tenantID, projectionName, key string) (string, error) {
	if err := contract.ValidateIdentity(tenantID, projectionName, key); err != nil {
		return "", err
	}
	return strings.TrimSpace(tenantID) + "\x00" + strings.TrimSpace(projectionName) + "\x00" + strings.TrimSpace(key), nil
}

func clone(item projection.Record) projection.Record {
	return contract.Clone(item)
}

var _ projection.Store = (*Store)(nil)
