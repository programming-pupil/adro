// Package filesystem implements a single-node content-addressed BlobStore.
package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/ports/blobstore"
)

type storedMetadata struct {
	Blob blobstore.BlobMetadata `json:"blob"`
}

type Store struct {
	root string
	mu   sync.RWMutex
}

func New(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("blob store root is required")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create blob store root: %w", err)
	}
	return &Store{root: root}, nil
}

func (s *Store) paths(ref blobstore.BlobRef) (string, string, error) {
	if s == nil || strings.TrimSpace(ref.TenantID) == "" || !validDigest(ref.Digest) || ref.Size < 0 {
		return "", "", errors.New("invalid blob reference")
	}
	tenant := digestComponent(ref.TenantID)
	digest := strings.ToLower(strings.TrimSpace(ref.Digest))
	dir := filepath.Join(s.root, tenant, digest[:2])
	return filepath.Join(dir, digest+".blob"), filepath.Join(dir, digest+".json"), nil
}

func (s *Store) Put(ctx context.Context, request blobstore.BlobPutRequest, source io.Reader) (blobstore.BlobRef, error) {
	if source == nil || strings.TrimSpace(request.TenantID) == "" {
		return blobstore.BlobRef{}, errors.New("tenant and reader are required")
	}
	if err := ctx.Err(); err != nil {
		return blobstore.BlobRef{}, err
	}
	max := request.MaxBytes
	if max <= 0 {
		max = 64 << 20
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmpDir, err := os.MkdirTemp(s.root, ".upload-")
	if err != nil {
		return blobstore.BlobRef{}, err
	}
	defer os.RemoveAll(tmpDir)
	tmpPath := filepath.Join(tmpDir, "payload")
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return blobstore.BlobRef{}, err
	}
	hash := sha256.New()
	limited := io.LimitReader(source, max+1)
	n, copyErr := io.Copy(io.MultiWriter(file, hash), limited)
	if syncErr := file.Sync(); copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := file.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return blobstore.BlobRef{}, copyErr
	}
	if n > max {
		return blobstore.BlobRef{}, blobstore.ErrLimit
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	ref := blobstore.BlobRef{TenantID: request.TenantID, Digest: digest, Size: n, MediaType: request.MediaType, EncryptionKey: request.EncryptionKey, Classification: request.Classification, RetainUntil: request.RetainUntil.UTC()}
	blobPath, metaPath, err := s.paths(ref)
	if err != nil {
		return blobstore.BlobRef{}, err
	}
	if existing, statErr := s.readMetadata(metaPath); statErr == nil {
		if existing.Tombstoned {
			return blobstore.BlobRef{}, blobstore.ErrTombstoned
		}
		if existing.BlobRef.Size != ref.Size || existing.BlobRef.MediaType != ref.MediaType || existing.BlobRef.Classification != ref.Classification || existing.BlobRef.EncryptionKey != ref.EncryptionKey {
			return blobstore.BlobRef{}, blobstore.ErrConflict
		}
		return existing.BlobRef, nil
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return blobstore.BlobRef{}, statErr
	}
	if err := os.MkdirAll(filepath.Dir(blobPath), 0o750); err != nil {
		return blobstore.BlobRef{}, err
	}
	if err := copyFile(tmpPath, blobPath); err != nil {
		return blobstore.BlobRef{}, err
	}
	metadata := blobstore.BlobMetadata{BlobRef: ref, CreatedAt: time.Now().UTC(), LegalHold: request.LegalHold}
	if err := writeMetadata(metaPath, storedMetadata{Blob: metadata}); err != nil {
		_ = os.Remove(blobPath)
		return blobstore.BlobRef{}, err
	}
	return ref, nil
}

func (s *Store) Open(ctx context.Context, ref blobstore.BlobRef) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	blobPath, metaPath, err := s.paths(ref)
	if err != nil {
		return nil, err
	}
	metadata, err := s.readMetadata(metaPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, blobstore.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if metadata.Tombstoned {
		return nil, blobstore.ErrTombstoned
	}
	if metadata.BlobRef.Size != ref.Size || metadata.BlobRef.Digest != ref.Digest {
		return nil, blobstore.ErrConflict
	}
	if err := verifyContent(blobPath, metadata.BlobRef); err != nil {
		return nil, err
	}
	file, err := os.Open(blobPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, blobstore.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (s *Store) Stat(ctx context.Context, ref blobstore.BlobRef) (blobstore.BlobMetadata, error) {
	if err := ctx.Err(); err != nil {
		return blobstore.BlobMetadata{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	blobPath, metaPath, err := s.paths(ref)
	if err != nil {
		return blobstore.BlobMetadata{}, err
	}
	metadata, err := s.readMetadata(metaPath)
	if errors.Is(err, fs.ErrNotExist) {
		return blobstore.BlobMetadata{}, blobstore.ErrNotFound
	}
	if err != nil {
		return blobstore.BlobMetadata{}, err
	}
	if err := verifyContent(blobPath, metadata.BlobRef); err != nil {
		return blobstore.BlobMetadata{}, err
	}
	return metadata, nil
}

func (s *Store) Tombstone(ctx context.Context, ref blobstore.BlobRef, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, metaPath, err := s.paths(ref)
	if err != nil {
		return err
	}
	metadata, err := s.readMetadata(metaPath)
	if errors.Is(err, fs.ErrNotExist) {
		return blobstore.ErrNotFound
	}
	if err != nil {
		return err
	}
	if metadata.LegalHold {
		return blobstore.ErrLegalHold
	}
	if metadata.Tombstoned {
		if metadata.Tombstone != strings.TrimSpace(reason) {
			return blobstore.ErrConflict
		}
		return nil
	}
	metadata.Tombstoned = true
	metadata.Tombstone = strings.TrimSpace(reason)
	return writeMetadata(metaPath, storedMetadata{Blob: metadata})
}

func verifyContent(path string, ref blobstore.BlobRef) error {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return blobstore.ErrNotFound
	}
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return err
	}
	if size != ref.Size || hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(ref.Digest) {
		return errors.New("blob integrity mismatch")
	}
	return nil
}

func (s *Store) readMetadata(path string) (blobstore.BlobMetadata, error) {
	file, err := os.Open(path)
	if err != nil {
		return blobstore.BlobMetadata{}, err
	}
	defer file.Close()
	var stored storedMetadata
	if err := json.NewDecoder(file).Decode(&stored); err != nil {
		return blobstore.BlobMetadata{}, err
	}
	if !validDigest(stored.Blob.BlobRef.Digest) || stored.Blob.BlobRef.Size < 0 {
		return blobstore.BlobMetadata{}, errors.New("blob metadata is corrupt")
	}
	return stored.Blob, nil
}

func writeMetadata(path string, value storedMetadata) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".metadata-")
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
	return os.Rename(tmpPath, path)
}

func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	if syncErr := out.Sync(); copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := out.Close(); copyErr == nil {
		copyErr = closeErr
	}
	return copyErr
}

func validDigest(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func digestComponent(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

// Compile-time contract assertion.
var _ blobstore.BlobStore = (*Store)(nil)
