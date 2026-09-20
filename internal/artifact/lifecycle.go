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

	"github.com/adro-project/adro/ports/scope"
)

type LifecycleRoot struct {
	URI         string    `json:"uri"`
	Reason      string    `json:"reason"`
	RetainUntil time.Time `json:"retain_until,omitempty"`
	LegalHold   bool      `json:"legal_hold"`
}

type GCReport struct {
	StartedAt             time.Time `json:"started_at"`
	CompletedAt           time.Time `json:"completed_at"`
	Scanned               int       `json:"scanned"`
	Deleted               []string  `json:"deleted,omitempty"`
	Protected             []string  `json:"protected,omitempty"`
	Failed                []string  `json:"failed,omitempty"`
	KeyDestructionPending []string  `json:"key_destruction_pending,omitempty"`
	ProofDigest           string    `json:"proof_digest"`
}

type lifecycleState struct {
	Version int                      `json:"version"`
	Roots   map[string]LifecycleRoot `json:"roots"`
	Proofs  []GCReport               `json:"proofs"`
}

type Lifecycle struct {
	mu     sync.Mutex
	store  *FileStore
	path   string
	roots  map[string]LifecycleRoot
	proofs []GCReport
	now    func() time.Time
}

type LifecycleOptions struct {
	Path string
	Now  func() time.Time
}

func NewLifecycle(store *FileStore, options LifecycleOptions) (*Lifecycle, error) {
	if store == nil {
		return nil, errors.New("artifact lifecycle requires a file store")
	}
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	lifecycle := &Lifecycle{store: store, path: strings.TrimSpace(options.Path), roots: map[string]LifecycleRoot{}, now: now}
	if lifecycle.path == "" {
		return lifecycle, nil
	}
	data, err := os.ReadFile(lifecycle.path)
	if errors.Is(err, os.ErrNotExist) {
		return lifecycle, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read artifact lifecycle state: %w", err)
	}
	var state lifecycleState
	if err := json.Unmarshal(data, &state); err != nil || state.Version != 1 {
		return nil, errors.New("artifact lifecycle state is corrupt")
	}
	if state.Roots != nil {
		lifecycle.roots = state.Roots
	}
	lifecycle.proofs = append([]GCReport(nil), state.Proofs...)
	return lifecycle, nil
}

func (l *Lifecycle) AddRoot(root LifecycleRoot) error {
	if l == nil || l.store == nil || strings.TrimSpace(root.URI) == "" || strings.TrimSpace(root.Reason) == "" {
		return errors.New("artifact lifecycle root requires uri and reason")
	}
	if root.RetainUntil.IsZero() && !root.LegalHold {
		return errors.New("artifact lifecycle root requires retain_until or legal_hold")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.roots[root.URI] = root
	return l.persistLocked()
}

func (l *Lifecycle) RemoveRoot(uri string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.roots, strings.TrimSpace(uri))
	return l.persistLocked()
}

func (l *Lifecycle) Roots() []LifecycleRoot {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make([]LifecycleRoot, 0, len(l.roots))
	for _, root := range l.roots {
		result = append(result, root)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].URI < result[j].URI })
	return result
}

// Collect performs bounded mark-and-sweep. Roots and legal holds are evaluated
// before deletion; each report is persisted so a later audit can prove which
// objects were scanned, removed or skipped.
func (l *Lifecycle) Collect(ctx context.Context, before time.Time, tenantID string) (GCReport, error) {
	if l == nil || l.store == nil {
		return GCReport{}, errors.New("artifact lifecycle is not configured")
	}
	if before.IsZero() {
		before = l.now().UTC()
	}
	if scoped, err := scope.Tenant(ctx); err != nil {
		ctx = scope.WithTenant(ctx, tenantID)
	} else if scoped != strings.TrimSpace(tenantID) {
		return GCReport{}, scope.ErrMissingTenant
	}
	before = before.UTC()
	started := l.now().UTC()
	objects, err := l.store.List(ctx, tenantID)
	if err != nil {
		return GCReport{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	report := GCReport{StartedAt: started, Scanned: len(objects)}
	for _, object := range objects {
		uri := object.Key.URI()
		root, protected := l.roots[uri]
		if protected && (root.LegalHold || root.RetainUntil.After(before)) {
			report.Protected = append(report.Protected, uri)
			continue
		}
		if object.CreatedAt.After(before) {
			report.Protected = append(report.Protected, uri)
			continue
		}
		if err := l.store.Delete(ctx, object.Key, DeleteOptions{}); err != nil {
			report.Failed = append(report.Failed, uri+": "+err.Error())
			continue
		}
		report.Deleted = append(report.Deleted, uri)
		if strings.TrimSpace(object.ContentSHA256) != "" {
			// The reference filesystem backend has no key manager. Keep this
			// explicit so a production key-destruction adapter cannot claim it
			// destroyed encryption keys merely because bytes were removed.
			report.KeyDestructionPending = append(report.KeyDestructionPending, uri)
		}
	}
	sort.Strings(report.Deleted)
	sort.Strings(report.Protected)
	sort.Strings(report.Failed)
	sort.Strings(report.KeyDestructionPending)
	report.CompletedAt = l.now().UTC()
	report.ProofDigest = proofDigest(report)
	l.proofs = append(l.proofs, report)
	if err := l.persistLocked(); err != nil {
		return GCReport{}, err
	}
	return cloneReport(report), nil
}

func (l *Lifecycle) Proofs() []GCReport {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make([]GCReport, len(l.proofs))
	for i := range l.proofs {
		result[i] = cloneReport(l.proofs[i])
	}
	return result
}

func (r GCReport) Validate() error {
	if r.StartedAt.IsZero() || r.CompletedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) || r.Scanned < 0 || r.ProofDigest == "" {
		return errors.New("invalid deletion proof")
	}
	if r.Scanned < len(r.Deleted)+len(r.Protected) {
		return errors.New("deletion proof counts overlap")
	}
	if proofDigest(r) != r.ProofDigest {
		return errors.New("deletion proof digest mismatch")
	}
	return nil
}

func proofDigest(report GCReport) string {
	copy := report
	copy.ProofDigest = ""
	data, _ := json.Marshal(copy)
	hash := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(hash[:])
}

func cloneReport(report GCReport) GCReport {
	report.Deleted = append([]string(nil), report.Deleted...)
	report.Protected = append([]string(nil), report.Protected...)
	report.Failed = append([]string(nil), report.Failed...)
	report.KeyDestructionPending = append([]string(nil), report.KeyDestructionPending...)
	return report
}

func (l *Lifecycle) persistLocked() error {
	if l.path == "" {
		return nil
	}
	state := lifecycleState{Version: 1, Roots: l.roots, Proofs: l.proofs}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(l.path), ".artifact-lifecycle-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
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
	return os.Rename(tmpPath, l.path)
}
