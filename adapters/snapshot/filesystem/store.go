// Package filesystem implements a crash-safe single-node SnapshotStore.
package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/adro-project/adro/ports/scope"
	"github.com/adro-project/adro/ports/snapshot"
)

type Store struct {
	root string
	mu   sync.RWMutex
}

func New(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("snapshot store root is required")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create snapshot store root: %w", err)
	}
	return &Store{root: root}, nil
}

func (s *Store) Get(ctx context.Context, streamID string) (snapshot.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return snapshot.Snapshot{}, err
	}
	tenantID, err := scope.Tenant(ctx)
	if err != nil {
		return snapshot.Snapshot{}, err
	}
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return snapshot.Snapshot{}, errors.New("stream_id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	path := s.path(tenantID, streamID)
	value, err := readSnapshot(path)
	if errors.Is(err, fs.ErrNotExist) {
		// Read the pre-tenant-layout location only as a migration bridge. The
		// decoded tenant is checked before any value is returned, so a legacy
		// snapshot cannot become visible to a different tenant.
		value, err = readSnapshot(s.legacyPath(streamID))
	}
	if errors.Is(err, fs.ErrNotExist) {
		return snapshot.Snapshot{}, snapshot.ErrNotFound
	}
	if err != nil {
		return snapshot.Snapshot{}, err
	}
	if err := validate(value, tenantID, streamID); err != nil {
		if value.TenantID != tenantID {
			return snapshot.Snapshot{}, snapshot.ErrNotFound
		}
		return snapshot.Snapshot{}, err
	}
	return clone(value), nil
}

func (s *Store) Put(ctx context.Context, value snapshot.Snapshot, expectedSequence int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tenantID, err := scope.Tenant(ctx)
	if err != nil {
		return err
	}
	if err := validate(value, tenantID, value.StreamID); err != nil {
		return err
	}
	if expectedSequence < 0 {
		return errors.New("expected sequence cannot be negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.path(tenantID, value.StreamID)
	existingPath := path
	existing, err := readSnapshot(path)
	existingFound := err == nil
	if errors.Is(err, fs.ErrNotExist) {
		legacyPath := s.legacyPath(value.StreamID)
		existing, err = readSnapshot(legacyPath)
		if err == nil {
			existingFound = true
			existingPath = legacyPath
			if existing.TenantID != tenantID {
				return snapshot.ErrConflict
			}
		} else if errors.Is(err, fs.ErrNotExist) {
			if expectedSequence != 0 {
				return snapshot.ErrConflict
			}
		} else {
			return err
		}
	} else if err != nil {
		return err
	}
	if existingFound {
		if existing.Sequence == value.Sequence && existing.Digest == value.Digest && string(existing.Payload) == string(value.Payload) {
			return nil
		}
		if existing.Sequence != expectedSequence || value.Sequence <= existing.Sequence {
			return snapshot.ErrConflict
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".snapshot-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := json.NewEncoder(tmp).Encode(value); err != nil {
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
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if existingPath != path {
		// The new tenant-qualified copy is now authoritative. Failure to remove
		// the legacy copy is harmless; Get always prefers the qualified path.
		_ = os.Remove(existingPath)
	}
	return nil
}

func validate(value snapshot.Snapshot, expectedTenant, expectedStream string) error {
	if strings.TrimSpace(value.TenantID) == "" || (strings.TrimSpace(expectedTenant) != "" && value.TenantID != expectedTenant) || strings.TrimSpace(value.StreamID) == "" || value.StreamID != expectedStream || value.Sequence < 1 || value.SchemaVersion < 1 || value.EncodingVersion < 1 || strings.TrimSpace(value.HashAlgorithm) == "" || value.HashVersion < 1 || value.CreatedAt.IsZero() || len(value.Payload) == 0 {
		return snapshot.ErrCorrupt
	}
	digest := sha256.Sum256(value.Payload)
	if strings.ToLower(strings.TrimSpace(value.Digest)) != hex.EncodeToString(digest[:]) {
		return snapshot.ErrCorrupt
	}
	return nil
}

func readSnapshot(path string) (snapshot.Snapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return snapshot.Snapshot{}, err
	}
	defer file.Close()
	var value snapshot.Snapshot
	if err := json.NewDecoder(file).Decode(&value); err != nil {
		return snapshot.Snapshot{}, snapshot.ErrCorrupt
	}
	return value, validate(value, "", value.StreamID)
}

func clone(value snapshot.Snapshot) snapshot.Snapshot {
	value.Payload = append([]byte(nil), value.Payload...)
	return value
}

func (s *Store) path(tenantID, streamID string) string {
	tenantDigest := sha256.Sum256([]byte(tenantID))
	streamDigest := sha256.Sum256([]byte(streamID))
	return filepath.Join(s.root, hex.EncodeToString(tenantDigest[:]), hex.EncodeToString(streamDigest[:])+".json")
}

func (s *Store) legacyPath(streamID string) string {
	digest := sha256.Sum256([]byte(streamID))
	return filepath.Join(s.root, hex.EncodeToString(digest[:])+".json")
}

// Compile-time contract assertion.
var _ snapshot.Store = (*Store)(nil)
