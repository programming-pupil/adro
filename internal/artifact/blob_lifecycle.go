package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/ports/blobstore"
	"github.com/adro-project/adro/ports/scope"
)

// BlobLifecycleRoot is a durable mark that keeps a blob reachable even when
// its producing projection has not been rebuilt yet.
type BlobLifecycleRoot struct {
	TenantID    string    `json:"tenant_id"`
	Digest      string    `json:"digest"`
	Reason      string    `json:"reason"`
	RetainUntil time.Time `json:"retain_until,omitempty"`
	LegalHold   bool      `json:"legal_hold"`
}

// BlobGCReport is an auditable two-phase retention result. A live unrooted blob
// is tombstoned first; physical deletion happens only on a later run after the
// tombstone is observed, so a crash cannot silently erase the only recovery
// marker.
type BlobGCReport struct {
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	TenantID    string    `json:"tenant_id"`
	Scanned     int       `json:"scanned"`
	Tombstoned  []string  `json:"tombstoned,omitempty"`
	Deleted     []string  `json:"deleted,omitempty"`
	Protected   []string  `json:"protected,omitempty"`
	Failed      []string  `json:"failed,omitempty"`
	ProofDigest string    `json:"proof_digest"`
}

type blobLifecycleState struct {
	Version int                          `json:"version"`
	Roots   map[string]BlobLifecycleRoot `json:"roots"`
	Proofs  []BlobGCReport               `json:"proofs"`
}

// BlobLifecycle coordinates retention for any blobstore.Inventory backend.
type BlobLifecycle struct {
	mu     sync.Mutex
	store  blobstore.Inventory
	path   string
	roots  map[string]BlobLifecycleRoot
	proofs []BlobGCReport
	now    func() time.Time
}

type BlobLifecycleOptions struct {
	Path string
	Now  func() time.Time
}

func NewBlobLifecycle(store blobstore.Inventory, options BlobLifecycleOptions) (*BlobLifecycle, error) {
	if store == nil {
		return nil, errors.New("blob lifecycle requires an inventory store")
	}
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	manager := &BlobLifecycle{store: store, path: strings.TrimSpace(options.Path), roots: map[string]BlobLifecycleRoot{}, now: now}
	if manager.path == "" {
		return manager, nil
	}
	data, err := os.ReadFile(manager.path)
	if errors.Is(err, os.ErrNotExist) {
		return manager, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read blob lifecycle state: %w", err)
	}
	var state blobLifecycleState
	if err := json.Unmarshal(data, &state); err != nil || state.Version != 1 {
		return nil, errors.New("blob lifecycle state is corrupt")
	}
	if state.Roots != nil {
		manager.roots = state.Roots
	}
	manager.proofs = append([]BlobGCReport(nil), state.Proofs...)
	for _, proof := range manager.proofs {
		if err := proof.Validate(); err != nil {
			return nil, err
		}
	}
	return manager, nil
}

func (l *BlobLifecycle) AddRoot(root BlobLifecycleRoot) error {
	if l == nil || l.store == nil || strings.TrimSpace(root.TenantID) == "" || !validBlobDigest(root.Digest) || strings.TrimSpace(root.Reason) == "" {
		return errors.New("blob lifecycle root requires tenant, digest, and reason")
	}
	if root.RetainUntil.IsZero() && !root.LegalHold {
		return errors.New("blob lifecycle root requires retain_until or legal_hold")
	}
	root.TenantID = strings.TrimSpace(root.TenantID)
	root.Digest = strings.ToLower(strings.TrimSpace(root.Digest))
	root.Reason = strings.TrimSpace(root.Reason)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.roots[blobRootKey(root.TenantID, root.Digest)] = root
	return l.persistLocked()
}

func (l *BlobLifecycle) RemoveRoot(tenantID, digest string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.roots, blobRootKey(tenantID, digest))
	return l.persistLocked()
}

func (l *BlobLifecycle) Roots() []BlobLifecycleRoot {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make([]BlobLifecycleRoot, 0, len(l.roots))
	for _, root := range l.roots {
		result = append(result, root)
	}
	sort.Slice(result, func(i, j int) bool {
		return blobRootKey(result[i].TenantID, result[i].Digest) < blobRootKey(result[j].TenantID, result[j].Digest)
	})
	return result
}

func (l *BlobLifecycle) Collect(ctx context.Context, tenantID string, before time.Time) (BlobGCReport, error) {
	if l == nil || l.store == nil {
		return BlobGCReport{}, errors.New("blob lifecycle is not configured")
	}
	tenantID = strings.TrimSpace(tenantID)
	scoped, err := scope.Tenant(ctx)
	if err != nil || tenantID == "" || scoped != tenantID {
		if err != nil {
			return BlobGCReport{}, err
		}
		return BlobGCReport{}, scope.ErrMissingTenant
	}
	if before.IsZero() {
		before = l.now().UTC()
	}
	before = before.UTC()
	started := l.now().UTC()
	objects, err := l.store.List(ctx, tenantID)
	if err != nil {
		return BlobGCReport{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	report := BlobGCReport{StartedAt: started, TenantID: tenantID, Scanned: len(objects)}
	for _, object := range objects {
		key := blobRootKey(object.TenantID, object.Digest)
		root, rooted := l.roots[key]
		if (rooted && (root.LegalHold || root.RetainUntil.After(before))) || object.LegalHold || object.RetainUntil.After(before) {
			report.Protected = append(report.Protected, key)
			continue
		}
		ref := object.BlobRef
		if object.Tombstoned {
			if object.Tombstone == "" {
				report.Failed = append(report.Failed, key+": missing tombstone reason")
				continue
			}
			if err := l.store.Purge(ctx, ref, object.Tombstone); err != nil {
				report.Failed = append(report.Failed, key+": "+err.Error())
				continue
			}
			report.Deleted = append(report.Deleted, key)
			continue
		}
		if err := l.store.Tombstone(ctx, ref, "gc-unreferenced"); err != nil {
			report.Failed = append(report.Failed, key+": "+err.Error())
			continue
		}
		report.Tombstoned = append(report.Tombstoned, key)
	}
	sort.Strings(report.Tombstoned)
	sort.Strings(report.Deleted)
	sort.Strings(report.Protected)
	sort.Strings(report.Failed)
	report.CompletedAt = l.now().UTC()
	report.ProofDigest = blobProofDigest(report)
	l.proofs = append(l.proofs, report)
	if err := l.persistLocked(); err != nil {
		return BlobGCReport{}, err
	}
	return cloneBlobGCReport(report), nil
}

func (l *BlobLifecycle) Proofs() []BlobGCReport {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make([]BlobGCReport, len(l.proofs))
	for i := range l.proofs {
		result[i] = cloneBlobGCReport(l.proofs[i])
	}
	return result
}

func (r BlobGCReport) Validate() error {
	if strings.TrimSpace(r.TenantID) == "" || r.StartedAt.IsZero() || r.CompletedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) || r.Scanned < 0 || r.ProofDigest == "" {
		return errors.New("invalid blob deletion proof")
	}
	if r.Scanned < len(r.Tombstoned)+len(r.Deleted)+len(r.Protected) {
		return errors.New("blob deletion proof counts overlap")
	}
	if blobProofDigest(r) != r.ProofDigest {
		return errors.New("blob deletion proof digest mismatch")
	}
	return nil
}

func blobRootKey(tenantID, digest string) string {
	return strings.TrimSpace(tenantID) + ":" + strings.ToLower(strings.TrimSpace(digest))
}

func validBlobDigest(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func blobProofDigest(report BlobGCReport) string {
	copy := report
	copy.ProofDigest = ""
	data, _ := json.Marshal(copy)
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func cloneBlobGCReport(report BlobGCReport) BlobGCReport {
	report.Tombstoned = append([]string(nil), report.Tombstoned...)
	report.Deleted = append([]string(nil), report.Deleted...)
	report.Protected = append([]string(nil), report.Protected...)
	report.Failed = append([]string(nil), report.Failed...)
	return report
}

func (l *BlobLifecycle) persistLocked() error {
	if l.path == "" {
		return nil
	}
	state := blobLifecycleState{Version: 1, Roots: l.roots, Proofs: l.proofs}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(l.path), ".blob-lifecycle-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
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
	return os.Rename(name, l.path)
}
