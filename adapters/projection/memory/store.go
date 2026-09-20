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
	mu      sync.RWMutex
	now     Clock
	items   map[string]projection.Record
	offsets map[string]projection.Offset
}

func New(now Clock) *Store {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Store{now: now, items: map[string]projection.Record{}, offsets: map[string]projection.Offset{}}
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

func (s *Store) ApplyBatch(ctx context.Context, batch projection.Batch, expectedSequence int64) (projection.BatchResult, error) {
	if err := ctx.Err(); err != nil {
		return projection.BatchResult{}, err
	}
	if expectedSequence < 0 {
		return projection.BatchResult{}, projection.ErrOffsetConflict
	}
	if err := contract.RequireTenant(ctx, batch.TenantID); err != nil {
		return projection.BatchResult{}, err
	}
	if err := contract.ValidateBatch(batch, batch.TenantID, contract.DefaultMaxPayload); err != nil {
		return projection.BatchResult{}, err
	}
	if err := contract.ValidateIdentity(batch.TenantID, batch.Projection, batch.PartitionID); err != nil {
		return projection.BatchResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make(map[string]projection.Record, len(s.items))
	for key, item := range s.items {
		items[key] = clone(item)
	}
	offsets := make(map[string]projection.Offset, len(s.offsets))
	for key, item := range s.offsets {
		offsets[key] = item
	}
	offsetKeyValue, err := offsetKey(batch.TenantID, batch.Projection, batch.PartitionID)
	if err != nil {
		return projection.BatchResult{}, err
	}
	currentOffset, hasOffset := offsets[offsetKeyValue]
	if hasOffset {
		if err := contract.ValidateOffset(currentOffset, batch.TenantID, true); err != nil {
			return projection.BatchResult{}, err
		}
		if currentOffset.LastSequence != expectedSequence {
			return projection.BatchResult{}, projection.ErrOffsetConflict
		}
		currentRecords, err := recordsForDigest(items, batch.TenantID, batch.Projection)
		if err != nil {
			return projection.BatchResult{}, err
		}
		if digest, err := projection.ProjectionDigest(batch.TenantID, batch.Projection, batch.PartitionID, currentRecords); err != nil {
			return projection.BatchResult{}, err
		} else if !strings.EqualFold(digest, currentOffset.ProjectionDigest) {
			return projection.BatchResult{}, projection.ErrDiverged
		}
	} else if expectedSequence != 0 {
		return projection.BatchResult{}, projection.ErrOffsetConflict
	}
	result := projection.BatchResult{}
	for _, mutation := range batch.Mutations {
		key, err := storageKey(batch.TenantID, batch.Projection, mutation.Key)
		if err != nil {
			return projection.BatchResult{}, err
		}
		current, found := items[key]
		if found {
			if err := contract.ValidateStored(current, batch.TenantID, contract.DefaultMaxPayload); err != nil {
				return projection.BatchResult{}, err
			}
			if current.SourceStream != batch.PartitionID {
				return projection.BatchResult{}, projection.ErrDiverged
			}
			if current.SourceSequence > mutation.SourceSequence {
				result.SkippedMutations++
				continue
			}
			if current.SourceSequence == mutation.SourceSequence {
				if mutation.Delete {
					delete(items, key)
					result.DeletedRecords++
					continue
				}
				if string(current.Payload) != string(mutation.Payload) || current.Digest != contract.Digest(mutation.Payload) {
					return projection.BatchResult{}, projection.ErrDiverged
				}
				result.SkippedMutations++
				continue
			}
		}
		if mutation.Delete {
			if !found {
				result.SkippedMutations++
				continue
			}
			delete(items, key)
			result.DeletedRecords++
			continue
		}
		version := int64(1)
		if found {
			version = current.Version + 1
		}
		item := projection.Record{TenantID: batch.TenantID, Projection: batch.Projection, Key: mutation.Key,
			Version: version, SourceStream: batch.PartitionID, SourceSequence: mutation.SourceSequence,
			Digest: contract.Digest(mutation.Payload), Payload: append([]byte(nil), mutation.Payload...), UpdatedAt: s.now().UTC()}
		items[key] = item
		result.UpdatedRecords++
	}
	records, err := recordsForDigest(items, batch.TenantID, batch.Projection)
	if err != nil {
		return projection.BatchResult{}, err
	}
	digest, err := projection.ProjectionDigest(batch.TenantID, batch.Projection, batch.PartitionID, records)
	if err != nil {
		return projection.BatchResult{}, err
	}
	if hasOffset && currentOffset.LastSequence == batch.LastSequence && strings.EqualFold(currentOffset.ProjectionDigest, digest) && expectedSequence == currentOffset.LastSequence {
		result.ProjectionDigest = digest
		return result, nil
	}
	if hasOffset && batch.LastSequence <= currentOffset.LastSequence {
		return projection.BatchResult{}, projection.ErrOffsetConflict
	}
	offsets[offsetKeyValue] = projection.Offset{TenantID: batch.TenantID, Projection: batch.Projection, PartitionID: batch.PartitionID,
		LastSequence: batch.LastSequence, ProjectionDigest: digest, UpdatedAt: s.now().UTC()}
	s.items = items
	s.offsets = offsets
	result.ProjectionDigest = digest
	return result, nil
}

func (s *Store) GetOffset(ctx context.Context, tenantID, projectionName, partitionID string) (projection.Offset, error) {
	if err := ctx.Err(); err != nil {
		return projection.Offset{}, err
	}
	if err := contract.RequireTenant(ctx, tenantID); err != nil {
		return projection.Offset{}, err
	}
	key, err := offsetKey(tenantID, projectionName, partitionID)
	if err != nil {
		return projection.Offset{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.offsets[key]
	if !ok {
		return projection.Offset{}, projection.ErrOffsetNotFound
	}
	if err := contract.ValidateOffset(item, tenantID, true); err != nil {
		return projection.Offset{}, err
	}
	return item, nil
}

func (s *Store) PutOffset(ctx context.Context, item projection.Offset, expectedSequence int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if expectedSequence < 0 {
		return projection.ErrOffsetConflict
	}
	if err := contract.RequireTenant(ctx, item.TenantID); err != nil {
		return err
	}
	item = contract.NormalizeOffset(item, s.now())
	if err := contract.ValidateOffset(item, item.TenantID, false); err != nil {
		return err
	}
	key, err := offsetKey(item.TenantID, item.Projection, item.PartitionID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.offsets[key]
	if !exists {
		if expectedSequence != 0 {
			return projection.ErrOffsetConflict
		}
		s.offsets[key] = item
		return nil
	}
	if current.TenantID != item.TenantID {
		return projection.ErrTenantMismatch
	}
	if current.LastSequence == item.LastSequence && current.ProjectionDigest == item.ProjectionDigest && expectedSequence == current.LastSequence {
		return nil
	}
	if expectedSequence != current.LastSequence || item.LastSequence <= current.LastSequence {
		return projection.ErrOffsetConflict
	}
	s.offsets[key] = item
	return nil
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

func offsetKey(tenantID, projectionName, partitionID string) (string, error) {
	if err := contract.ValidateIdentity(tenantID, projectionName, partitionID); err != nil {
		return "", err
	}
	return strings.TrimSpace(tenantID) + "\x00" + strings.TrimSpace(projectionName) + "\x00" + strings.TrimSpace(partitionID), nil
}

func recordsForDigest(items map[string]projection.Record, tenantID, projectionName string) ([]projection.Record, error) {
	result := make([]projection.Record, 0, len(items))
	for _, item := range items {
		if item.TenantID != tenantID || item.Projection != projectionName {
			continue
		}
		if err := contract.ValidateStored(item, tenantID, contract.DefaultMaxPayload); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func clone(item projection.Record) projection.Record {
	return contract.Clone(item)
}

var _ projection.Store = (*Store)(nil)
var _ projection.AtomicStore = (*Store)(nil)
