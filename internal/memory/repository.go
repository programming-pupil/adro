// Package memory stores evidence-backed facts used by agent context. It keeps
// memory separate from the mutable session transcript so unreviewed or
// conflicting claims cannot silently become stable system context.
package memory

import (
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
)

type Status string

const (
	Candidate   Status = "candidate"
	Quarantined Status = "quarantined"
	Confirmed   Status = "confirmed"
	Superseded  Status = "superseded"
	Forgotten   Status = "forgotten"
	Rejected    Status = "rejected"
)

var (
	ErrNotFound   = errors.New("memory item not found")
	ErrScope      = errors.New("memory scope mismatch")
	ErrEvidence   = errors.New("memory evidence is required")
	ErrTransition = errors.New("invalid memory transition")
	ErrConflict   = errors.New("memory conflict")
)

// Scope is carried by every memory fact and is compared exactly on reads.
// SessionID and AttemptID may be empty for project-scoped facts, but tenant
// and workspace are always required.
type Scope struct {
	TenantID    string `json:"tenant_id"`
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	AttemptID   string `json:"attempt_id,omitempty"`
}

func (s Scope) valid() bool {
	return strings.TrimSpace(s.TenantID) != "" && strings.TrimSpace(s.WorkspaceID) != ""
}

func (s Scope) equal(other Scope) bool {
	return s.TenantID == other.TenantID && s.WorkspaceID == other.WorkspaceID &&
		s.ProjectID == other.ProjectID && s.SessionID == other.SessionID && s.AttemptID == other.AttemptID
}

type Item struct {
	ID               string     `json:"id"`
	Scope            Scope      `json:"scope"`
	Kind             string     `json:"kind"`
	Claim            string     `json:"claim"`
	Content          string     `json:"content"`
	Fingerprint      string     `json:"fingerprint"`
	SourceIDs        []string   `json:"source_ids"`
	EvidenceHash     string     `json:"evidence_hash"`
	Sensitivity      string     `json:"sensitivity,omitempty"`
	PollutionLineage []string   `json:"pollution_lineage,omitempty"`
	ConflictPackage  []string   `json:"conflict_package,omitempty"`
	EmbeddingScore   float64    `json:"embedding_score,omitempty"`
	LexicalScore     float64    `json:"lexical_score,omitempty"`
	Reviewer         string     `json:"reviewer,omitempty"`
	Status           Status     `json:"status"`
	Reason           string     `json:"reason,omitempty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	Supersedes       []string   `json:"supersedes,omitempty"`
	Revision         int64      `json:"revision"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// MemoryItem is retained as an explicit alias for callers migrating from the
// harness memory shape.
type MemoryItem = Item

type AddInput struct {
	ID               string
	Scope            Scope
	Kind             string
	Claim            string
	Content          string
	Fingerprint      string
	SourceIDs        []string
	EvidenceHash     string
	Sensitivity      string
	PollutionLineage []string
	ExpiresAt        *time.Time
	EmbeddingScore   float64
	LexicalScore     float64
}

type QueryInput struct {
	Scope              Scope
	Claim              string
	Limit              int
	IncludeUnconfirmed bool
}

type AuditEvent struct {
	ID           string    `json:"id"`
	ItemID       string    `json:"item_id"`
	Scope        Scope     `json:"scope"`
	Action       string    `json:"action"`
	From         Status    `json:"from,omitempty"`
	To           Status    `json:"to,omitempty"`
	Actor        string    `json:"actor,omitempty"`
	Reason       string    `json:"reason"`
	EvidenceHash string    `json:"evidence_hash,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type persisted struct {
	Version  int             `json:"version"`
	Revision int64           `json:"revision"`
	Items    map[string]Item `json:"items"`
	Audit    []AuditEvent    `json:"audit"`
}

type Repository struct {
	mu       sync.RWMutex
	path     string
	revision int64
	items    map[string]Item
	audit    []AuditEvent
}

func NewRepository() *Repository { return &Repository{items: map[string]Item{}} }

func NewPersistentRepository(path string) (*Repository, error) {
	r := NewRepository()
	r.path = strings.TrimSpace(path)
	if r.path == "" {
		return r, nil
	}
	data, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read memory state: %w", err)
	}
	var state persisted
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode memory state: %w", err)
	}
	if state.Version != 0 && state.Version != 1 {
		return nil, fmt.Errorf("unsupported memory state version %d", state.Version)
	}
	r.revision, r.items, r.audit = state.Revision, state.Items, state.Audit
	if r.items == nil {
		r.items = map[string]Item{}
	}
	if err := r.Verify(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Repository) Add(input AddInput) (Item, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	beforeItems := make(map[string]Item, len(r.items))
	for id, existing := range r.items {
		beforeItems[id] = clone(existing)
	}
	beforeAudit := append([]AuditEvent(nil), r.audit...)
	beforeRevision := r.revision
	item, err := r.addLocked(input, time.Now().UTC())
	if err != nil {
		return Item{}, err
	}
	if err := r.persistLocked(); err != nil {
		r.items = beforeItems
		r.audit = beforeAudit
		r.revision = beforeRevision
		return Item{}, err
	}
	return clone(item), nil
}

// addLocked validates and adds a fact while the repository mutex is held. It
// does not persist, allowing compound lifecycle operations to commit as one
// snapshot and roll back all in-memory changes on an I/O failure.
func (r *Repository) addLocked(input AddInput, now time.Time) (Item, error) {
	if !input.Scope.valid() {
		return Item{}, ErrScope
	}
	if strings.TrimSpace(input.Claim) == "" || strings.TrimSpace(input.Content) == "" || len(input.SourceIDs) == 0 {
		return Item{}, errors.New("claim, content and source_ids are required")
	}
	for i := range input.SourceIDs {
		input.SourceIDs[i] = strings.TrimSpace(input.SourceIDs[i])
		if input.SourceIDs[i] == "" {
			return Item{}, errors.New("source_ids cannot contain empty values")
		}
	}
	sort.Strings(input.SourceIDs)
	if input.ID == "" {
		input.ID = newID(input.Scope, input.Claim, input.Content)
	}
	if input.Fingerprint == "" {
		input.Fingerprint = fingerprint(input.Claim)
	}
	expectedEvidence := evidenceHash(input.Scope, input.Claim, input.Content, input.SourceIDs)
	if input.EvidenceHash != "" && input.EvidenceHash != expectedEvidence {
		return Item{}, ErrEvidence
	}
	input.EvidenceHash = expectedEvidence
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if input.EmbeddingScore < 0 || input.EmbeddingScore > 1 || input.LexicalScore < 0 || input.LexicalScore > 1 {
		return Item{}, errors.New("memory evidence scores must be between 0 and 1")
	}
	item := Item{ID: input.ID, Scope: input.Scope, Kind: strings.TrimSpace(input.Kind), Claim: input.Claim, Content: input.Content, Fingerprint: input.Fingerprint, SourceIDs: append([]string(nil), input.SourceIDs...), EvidenceHash: input.EvidenceHash, Sensitivity: input.Sensitivity, PollutionLineage: append([]string(nil), input.PollutionLineage...), EmbeddingScore: input.EmbeddingScore, LexicalScore: input.LexicalScore, Status: Candidate, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if input.ExpiresAt != nil {
		expiry := input.ExpiresAt.UTC()
		if !expiry.After(now) {
			return Item{}, errors.New("expires_at must be in the future")
		}
		item.ExpiresAt = &expiry
	}
	if _, exists := r.items[item.ID]; exists {
		return Item{}, fmt.Errorf("memory item %s already exists", item.ID)
	}
	for id, existing := range r.items {
		if !sameScope(existing.Scope, item.Scope) || existing.Fingerprint == "" || existing.Fingerprint != item.Fingerprint || existing.Status == Forgotten || existing.Status == Rejected {
			continue
		}
		if existing.EvidenceHash == item.EvidenceHash {
			return clone(existing), nil
		}
		item.Status = Quarantined
		item.ConflictPackage = unique(append(append([]string{}, existing.ConflictPackage...), id, item.ID))
		existing.ConflictPackage = unique(append(existing.ConflictPackage, id, item.ID))
		existing.Revision++
		existing.UpdatedAt = now
		r.items[id] = existing
		r.audit = append(r.audit, AuditEvent{ID: newID(item.Scope, id, "conflict"), ItemID: id, Scope: item.Scope, Action: "conflict_detected", From: existing.Status, To: existing.Status, Reason: "conflicting evidence", EvidenceHash: item.EvidenceHash, CreatedAt: now})
	}
	r.items[item.ID] = item
	r.revision++
	r.audit = append(r.audit, AuditEvent{ID: newID(item.Scope, item.ID, "candidate"), ItemID: item.ID, Scope: item.Scope, Action: "created", To: item.Status, Reason: "evidence submitted", EvidenceHash: item.EvidenceHash, CreatedAt: now})
	return item, nil
}

func (r *Repository) Get(scope Scope, id string) (Item, error) {
	if !scope.valid() {
		return Item{}, ErrScope
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.items[strings.TrimSpace(id)]
	if !ok {
		return Item{}, ErrNotFound
	}
	if !sameScope(scope, item.Scope) {
		return Item{}, ErrScope
	}
	if expired(item, time.Now().UTC()) {
		return Item{}, ErrNotFound
	}
	return clone(item), nil
}

func (r *Repository) List(scope Scope, status Status, now time.Time) []Item {
	if !scope.valid() {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Item, 0)
	for _, item := range r.items {
		if !sameScope(scope, item.Scope) || (status != "" && item.Status != status) || expired(item, now) {
			continue
		}
		out = append(out, clone(item))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Repository) Stable(scope Scope, now time.Time) []Item { return r.List(scope, Confirmed, now) }

// Query returns a deterministic, scope-isolated retrieval frontier. The
// caller supplies scores from its embedding/lexical index; the repository
// persists those scores but never allows unconfirmed facts into the stable
// default result set.
func (r *Repository) Query(input QueryInput) []Item {
	if !input.Scope.valid() {
		return nil
	}
	if input.Limit <= 0 || input.Limit > 500 {
		input.Limit = 100
	}
	status := Confirmed
	if input.IncludeUnconfirmed {
		status = ""
	}
	items := r.List(input.Scope, status, time.Now().UTC())
	needle := strings.ToLower(strings.TrimSpace(input.Claim))
	sort.SliceStable(items, func(i, j int) bool {
		score := items[i].EmbeddingScore + items[i].LexicalScore
		other := items[j].EmbeddingScore + items[j].LexicalScore
		if score == other {
			return items[i].ID < items[j].ID
		}
		return score > other
	})
	if needle != "" {
		filtered := items[:0]
		for _, item := range items {
			if strings.Contains(strings.ToLower(item.Claim), needle) || strings.Contains(strings.ToLower(item.Content), needle) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	if len(items) > input.Limit {
		items = items[:input.Limit]
	}
	return items
}

func (r *Repository) Transition(scope Scope, id string, to Status, actor, reason string) (Item, error) {
	if !scope.valid() {
		return Item{}, ErrScope
	}
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return Item{}, errors.New("actor and reason are required")
	}
	if !validStatus(to) {
		return Item{}, ErrTransition
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[strings.TrimSpace(id)]
	if !ok {
		return Item{}, ErrNotFound
	}
	if !sameScope(scope, item.Scope) {
		return Item{}, ErrScope
	}
	if expired(item, time.Now().UTC()) {
		return Item{}, ErrNotFound
	}
	if !allowedTransition(item.Status, to) {
		return Item{}, fmt.Errorf("%w: %s to %s", ErrTransition, item.Status, to)
	}
	if to == Confirmed && len(item.ConflictPackage) > 0 {
		return Item{}, fmt.Errorf("%w: conflict package requires resolution", ErrConflict)
	}
	before := clone(item)
	beforeAuditLen := len(r.audit)
	beforeRevision := r.revision
	from := item.Status
	item.Status, item.Reviewer, item.Reason = to, actor, reason
	item.Revision++
	item.UpdatedAt = time.Now().UTC()
	r.items[item.ID] = item
	r.revision++
	r.audit = append(r.audit, AuditEvent{ID: newID(scope, item.ID, string(to)), ItemID: item.ID, Scope: scope, Action: "transition", From: from, To: to, Actor: actor, Reason: reason, EvidenceHash: item.EvidenceHash, CreatedAt: item.UpdatedAt})
	if err := r.persistLocked(); err != nil {
		r.items[item.ID] = before
		r.audit = r.audit[:beforeAuditLen]
		r.revision = beforeRevision
		return Item{}, err
	}
	return clone(item), nil
}

func (r *Repository) Confirm(scope Scope, id, reviewer, reason string) (Item, error) {
	return r.Transition(scope, id, Confirmed, reviewer, reason)
}
func (r *Repository) Reject(scope Scope, id, reviewer, reason string) (Item, error) {
	return r.Transition(scope, id, Rejected, reviewer, reason)
}
func (r *Repository) Forget(scope Scope, id, actor, reason string) (Item, error) {
	return r.Transition(scope, id, Forgotten, actor, reason)
}

// ResolveConflict records an explicit reviewer choice and clears the conflict
// package only after every competing item is in the same scope. Losers are
// rejected in the same snapshot, so a later stable read cannot observe two
// contradictory confirmed facts.
func (r *Repository) ResolveConflict(scope Scope, winnerID string, loserIDs []string, reviewer, reason string) (Item, error) {
	if !scope.valid() {
		return Item{}, ErrScope
	}
	if strings.TrimSpace(reviewer) == "" || strings.TrimSpace(reason) == "" {
		return Item{}, errors.New("reviewer and reason are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	winner, ok := r.items[strings.TrimSpace(winnerID)]
	if !ok {
		return Item{}, ErrNotFound
	}
	if !sameScope(scope, winner.Scope) {
		return Item{}, ErrScope
	}
	if winner.Status != Quarantined && winner.Status != Candidate {
		return Item{}, fmt.Errorf("%w: conflict winner must be candidate or quarantined", ErrTransition)
	}
	losers := unique(loserIDs)
	if len(losers) == 0 {
		for _, id := range winner.ConflictPackage {
			if id != winner.ID {
				losers = append(losers, id)
			}
		}
	}
	for _, id := range losers {
		item, exists := r.items[id]
		if !exists {
			return Item{}, ErrNotFound
		}
		if !sameScope(scope, item.Scope) {
			return Item{}, ErrScope
		}
		if item.Fingerprint != winner.Fingerprint {
			return Item{}, fmt.Errorf("%w: conflict package contains a different claim", ErrConflict)
		}
	}
	beforeItems := make(map[string]Item, len(r.items))
	for id, item := range r.items {
		beforeItems[id] = clone(item)
	}
	beforeAudit := append([]AuditEvent(nil), r.audit...)
	beforeRevision := r.revision
	now := time.Now().UTC()
	winner.ConflictPackage = nil
	winner.Reviewer, winner.Reason = reviewer, reason
	winner.Revision++
	winner.UpdatedAt = now
	r.items[winner.ID] = winner
	for _, id := range losers {
		item := r.items[id]
		if item.ID == winner.ID {
			continue
		}
		from := item.Status
		item.Status, item.Reviewer, item.Reason = Rejected, reviewer, "rejected by conflict resolution: "+reason
		item.ConflictPackage = nil
		item.Revision++
		item.UpdatedAt = now
		r.items[id] = item
		r.audit = append(r.audit, AuditEvent{ID: newID(scope, id, "conflict_resolved"), ItemID: id, Scope: scope, Action: "conflict_resolved", From: from, To: Rejected, Actor: reviewer, Reason: reason, EvidenceHash: item.EvidenceHash, CreatedAt: now})
	}
	r.revision++
	r.audit = append(r.audit, AuditEvent{ID: newID(scope, winner.ID, "conflict_winner"), ItemID: winner.ID, Scope: scope, Action: "conflict_winner", From: winner.Status, To: winner.Status, Actor: reviewer, Reason: reason, EvidenceHash: winner.EvidenceHash, CreatedAt: now})
	if err := r.persistLocked(); err != nil {
		r.items, r.audit, r.revision = beforeItems, beforeAudit, beforeRevision
		return Item{}, err
	}
	return clone(winner), nil
}

func (r *Repository) Supersede(scope Scope, oldID string, replacement AddInput, actor, reason string) (Item, error) {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return Item{}, errors.New("actor and reason are required")
	}
	if !scope.valid() {
		return Item{}, ErrScope
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.items[strings.TrimSpace(oldID)]
	if !ok {
		return Item{}, ErrNotFound
	}
	if !sameScope(scope, old.Scope) {
		return Item{}, ErrScope
	}
	if expired(old, time.Now().UTC()) {
		return Item{}, ErrNotFound
	}
	if old.Status != Confirmed {
		return Item{}, fmt.Errorf("%w: only confirmed facts can be superseded", ErrTransition)
	}
	beforeItems := make(map[string]Item, len(r.items))
	for id, item := range r.items {
		beforeItems[id] = clone(item)
	}
	beforeAudit := append([]AuditEvent(nil), r.audit...)
	beforeRevision := r.revision
	replacement.Scope = scope
	newItem, err := r.addLocked(replacement, time.Now().UTC())
	if err != nil {
		return Item{}, err
	}
	old.Status, old.Reviewer, old.Reason = Superseded, actor, reason
	old.Revision++
	old.UpdatedAt = time.Now().UTC()
	r.items[old.ID] = old
	newItem = r.items[newItem.ID]
	newItem.Supersedes = unique(append(newItem.Supersedes, old.ID))
	newItem.Revision++
	newItem.UpdatedAt = old.UpdatedAt
	r.items[newItem.ID] = newItem
	r.revision++
	r.audit = append(r.audit, AuditEvent{ID: newID(scope, old.ID, "superseded"), ItemID: old.ID, Scope: scope, Action: "superseded", From: Confirmed, To: Superseded, Actor: actor, Reason: reason, EvidenceHash: old.EvidenceHash, CreatedAt: old.UpdatedAt})
	if err = r.persistLocked(); err != nil {
		r.items = beforeItems
		r.audit = beforeAudit
		r.revision = beforeRevision
		return Item{}, err
	}
	return clone(newItem), nil
}

func (r *Repository) Audit(scope Scope) []AuditEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AuditEvent, 0)
	for _, event := range r.audit {
		if scope.valid() && !sameScope(scope, event.Scope) {
			continue
		}
		out = append(out, event)
	}
	return out
}

func (r *Repository) Verify() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for id, item := range r.items {
		if id == "" || item.ID != id || !item.Scope.valid() || item.EvidenceHash != evidenceHash(item.Scope, item.Claim, item.Content, item.SourceIDs) || !validStatus(item.Status) {
			return fmt.Errorf("invalid memory item %s", id)
		}
	}
	return nil
}

func (r *Repository) Flush() error { r.mu.Lock(); defer r.mu.Unlock(); return r.persistLocked() }

func (r *Repository) persistLocked() error {
	if r.path == "" {
		return nil
	}
	state := persisted{Version: 1, Revision: r.revision, Items: make(map[string]Item, len(r.items)), Audit: append([]AuditEvent(nil), r.audit...)}
	for id, item := range r.items {
		state.Items[id] = clone(item)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".adro-memory-*")
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
	if err := os.Rename(name, r.path); err != nil {
		return err
	}
	if f, err := os.Open(dir); err == nil {
		syncErr := f.Sync()
		_ = f.Close()
		if syncErr != nil {
			return syncErr
		}
	}
	return nil
}

func validStatus(s Status) bool {
	switch s {
	case Candidate, Quarantined, Confirmed, Superseded, Forgotten, Rejected:
		return true
	default:
		return false
	}
}
func allowedTransition(from, to Status) bool {
	switch from {
	case Candidate:
		return to == Quarantined || to == Rejected
	case Quarantined:
		return to == Confirmed || to == Rejected
	case Confirmed:
		return to == Superseded || to == Forgotten
	default:
		return false
	}
}
func expired(item Item, now time.Time) bool {
	return item.ExpiresAt != nil && !item.ExpiresAt.After(now)
}
func sameScope(a, b Scope) bool { return a.equal(b) }
func clone(item Item) Item {
	item.SourceIDs = append([]string(nil), item.SourceIDs...)
	item.PollutionLineage = append([]string(nil), item.PollutionLineage...)
	item.ConflictPackage = append([]string(nil), item.ConflictPackage...)
	item.Supersedes = append([]string(nil), item.Supersedes...)
	if item.ExpiresAt != nil {
		expiry := *item.ExpiresAt
		item.ExpiresAt = &expiry
	}
	return item
}
func unique(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
func fingerprint(claim string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(claim))))
	return "sha256:" + hex.EncodeToString(sum[:])
}
func evidenceHash(scope Scope, claim, content string, sources []string) string {
	payload := struct {
		Scope   Scope    `json:"scope"`
		Claim   string   `json:"claim"`
		Content string   `json:"content"`
		Sources []string `json:"sources"`
	}{scope, claim, content, append([]string(nil), sources...)}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func newID(scope Scope, parts ...string) string {
	data := scope.TenantID + "\x00" + scope.WorkspaceID + "\x00" + strings.Join(parts, "\x00") + fmt.Sprintf("\x00%d", time.Now().UnixNano())
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:16])
}
