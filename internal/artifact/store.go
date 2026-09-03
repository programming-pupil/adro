// Package artifact implements the provider-neutral ArtifactStore contract.
package artifact

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
	"strconv"
	"strings"
	"sync"
	"time"
)

type Key struct {
	TenantID   string `json:"tenant_id"`
	ArtifactID string `json:"artifact_id"`
	Version    int64  `json:"version"`
}

func (k Key) URI() string {
	return fmt.Sprintf("artifact://%s/%s/%d", k.TenantID, k.ArtifactID, k.Version)
}

type ByteRange struct{ Start, End int64 } // End is inclusive; End < 0 means through EOF.
type PutOptions struct {
	MediaType      string
	Classification string
	Immutable      bool
}
type DeleteOptions struct{ LegalHold bool }
type Capabilities struct {
	Range      bool `json:"range"`
	Multipart  bool `json:"multipart"`
	ObjectLock bool `json:"object_lock"`
	Encryption bool `json:"encryption"`
}
type ObjectMeta struct {
	Key           Key       `json:"key"`
	MediaType     string    `json:"media_type"`
	SizeBytes     int64     `json:"size_bytes"`
	ContentSHA256 string    `json:"content_sha256"`
	CreatedAt     time.Time `json:"created_at"`
	Immutable     bool      `json:"immutable"`
}

type Store interface {
	Capabilities(context.Context) (Capabilities, error)
	Put(context.Context, Key, io.Reader, PutOptions) (ObjectMeta, error)
	Open(context.Context, Key, ByteRange) (io.ReadCloser, ObjectMeta, error)
	Stat(context.Context, Key) (ObjectMeta, error)
	Delete(context.Context, Key, DeleteOptions) error
	Health(context.Context) error
}

// FileStore is the zero-configuration single-node driver. Objects are written
// through a temporary file and atomically renamed only after the hash is known.
type FileStore struct {
	root string
	mu   sync.RWMutex
}

func NewFileStore(root string) (*FileStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("artifact root is required")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create artifact root: %w", err)
	}
	return &FileStore{root: root}, nil
}

func (s *FileStore) Capabilities(context.Context) (Capabilities, error) {
	return Capabilities{Range: true, Multipart: false, ObjectLock: false, Encryption: false}, nil
}

func (s *FileStore) path(k Key) (string, error) {
	if k.TenantID == "" || k.ArtifactID == "" || k.Version < 1 {
		return "", errors.New("invalid artifact key")
	}
	for _, part := range []string{k.TenantID, k.ArtifactID} {
		if part != filepath.Base(part) || strings.Contains(part, string(filepath.Separator)) || strings.Contains(part, "..") {
			return "", errors.New("invalid artifact key")
		}
	}
	dir := filepath.Join(s.root, k.TenantID, k.ArtifactID)
	return filepath.Join(dir, strconv.FormatInt(k.Version, 10)), nil
}

func (s *FileStore) Put(ctx context.Context, k Key, r io.Reader, opts PutOptions) (ObjectMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(k)
	if err != nil {
		return ObjectMeta{}, err
	}
	if err := ctx.Err(); err != nil {
		return ObjectMeta{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return ObjectMeta{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".upload-*")
	if err != nil {
		return ObjectMeta{}, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(tmp, h), r)
	if closeErr := tmp.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return ObjectMeta{}, copyErr
	}
	if err := ctx.Err(); err != nil {
		return ObjectMeta{}, err
	}
	if existing, err := s.readMeta(path, k); err == nil && existing.Immutable {
		return ObjectMeta{}, errors.New("immutable artifact already exists")
	}
	meta := ObjectMeta{Key: k, MediaType: opts.MediaType, SizeBytes: n, ContentSHA256: hex.EncodeToString(h.Sum(nil)), CreatedAt: time.Now().UTC(), Immutable: opts.Immutable}
	metaTmp, err := os.CreateTemp(filepath.Dir(path), ".metadata-*")
	if err != nil {
		return ObjectMeta{}, err
	}
	metaTmpPath := metaTmp.Name()
	defer os.Remove(metaTmpPath)
	if err := json.NewEncoder(metaTmp).Encode(meta); err != nil {
		metaTmp.Close()
		return ObjectMeta{}, err
	}
	if err := metaTmp.Close(); err != nil {
		return ObjectMeta{}, err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return ObjectMeta{}, err
	}
	if err := os.Rename(metaTmpPath, path+".meta.json"); err != nil {
		_ = os.Remove(path)
		return ObjectMeta{}, err
	}
	return meta, nil
}

func (s *FileStore) Open(ctx context.Context, k Key, br ByteRange) (io.ReadCloser, ObjectMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	path, err := s.path(k)
	if err != nil {
		return nil, ObjectMeta{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, ObjectMeta{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ObjectMeta{}, os.ErrNotExist
		}
		return nil, ObjectMeta{}, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, ObjectMeta{}, err
	}
	var reader io.ReadCloser = f
	if br.Start > 0 || br.End >= 0 {
		if br.Start < 0 || br.Start >= st.Size() {
			f.Close()
			return nil, ObjectMeta{}, fmt.Errorf("range start out of bounds")
		}
		end := br.End
		if end < 0 || end >= st.Size() {
			end = st.Size() - 1
		}
		if end < br.Start {
			f.Close()
			return nil, ObjectMeta{}, fmt.Errorf("invalid range")
		}
		if _, err := f.Seek(br.Start, io.SeekStart); err != nil {
			f.Close()
			return nil, ObjectMeta{}, err
		}
		reader = &rangeReadCloser{Reader: io.LimitReader(f, end-br.Start+1), closer: f}
	}
	meta, err := s.readMeta(path, k)
	if err != nil {
		_ = f.Close()
		return nil, ObjectMeta{}, fmt.Errorf("artifact metadata unavailable: %w", err)
	} else if err := verifyObject(path, meta); err != nil {
		_ = f.Close()
		return nil, ObjectMeta{}, err
	}
	return reader, meta, nil
}

type rangeReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *rangeReadCloser) Close() error { return r.closer.Close() }

func (s *FileStore) Stat(ctx context.Context, k Key) (ObjectMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	path, err := s.path(k)
	if err != nil {
		return ObjectMeta{}, err
	}
	if err := ctx.Err(); err != nil {
		return ObjectMeta{}, err
	}
	_, err = os.Stat(path)
	if err != nil {
		return ObjectMeta{}, err
	}
	meta, err := s.readMeta(path, k)
	if err != nil {
		return ObjectMeta{}, fmt.Errorf("artifact metadata unavailable: %w", err)
	}
	if verifyErr := verifyObject(path, meta); verifyErr != nil {
		return ObjectMeta{}, verifyErr
	}
	return meta, nil
}

func verifyObject(path string, meta ObjectMeta) error {
	if meta.SizeBytes < 0 || strings.TrimSpace(meta.ContentSHA256) == "" {
		return errors.New("artifact integrity metadata is incomplete")
	}
	if decoded, err := hex.DecodeString(meta.ContentSHA256); err != nil || len(decoded) != sha256.Size {
		return errors.New("artifact integrity metadata has invalid content hash")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return fmt.Errorf("verify artifact content: %w", err)
	}
	if size != meta.SizeBytes {
		return fmt.Errorf("artifact integrity mismatch: size got %d want %d", size, meta.SizeBytes)
	}
	if hex.EncodeToString(h.Sum(nil)) != strings.TrimSpace(meta.ContentSHA256) {
		return errors.New("artifact integrity mismatch: content hash does not match metadata")
	}
	return nil
}

func (s *FileStore) Delete(ctx context.Context, k Key, opts DeleteOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if opts.LegalHold {
		return errors.New("artifact is under legal hold")
	}
	path, err := s.path(k)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err = os.Remove(path)
	if err != nil {
		return err
	}
	_ = os.Remove(path + ".meta.json")
	return nil
}
func (s *FileStore) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := os.Stat(s.root)
	return err
}

func (s *FileStore) readMeta(path string, key Key) (ObjectMeta, error) {
	f, err := os.Open(path + ".meta.json")
	if err != nil {
		return ObjectMeta{}, err
	}
	defer f.Close()
	var meta ObjectMeta
	if err := json.NewDecoder(f).Decode(&meta); err != nil {
		return ObjectMeta{}, err
	}
	meta.Key = key
	return meta, nil
}
