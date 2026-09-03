package orchestration

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/internal/durable"
)

var ErrNotFound = errors.New("orchestration record not found")

// Repository is the persistence seam for SQLite/PostgreSQL/file profiles.
// Implementations must keep published plans immutable and compare revisions on
// mutable definitions.
type Repository interface {
	SaveAgent(AgentDefinition, int64) error
	GetAgent(workspaceID, id string, revision int64) (AgentDefinition, error)
	ListAgents(workspaceID string, status AgentStatus) []AgentDefinition
	SaveSquad(SquadDefinition, int64) error
	GetSquad(workspaceID, id string, revision int64) (SquadDefinition, error)
	ListSquads(workspaceID string, status SquadStatus) []SquadDefinition
	CreatePlan(RequirementExecutionPlan) error
	GetPlanByIdempotency(workspaceID, idempotencyKey string) (RequirementExecutionPlan, error)
	GetPlan(workspaceID, id string) (RequirementExecutionPlan, error)
	ListPlans(workspaceID string) []RequirementExecutionPlan
	SaveProjection(PlanProjection) error
	GetProjection(planID string) (PlanProjection, error)
	AppendEvent(Event) error
	ListEvents(planID string, after int64) []Event
}

// ControlRepository is the complete control-plane persistence contract used by
// the HTTP server and workers. Keeping it separate from Repository lets pure
// graph scheduling depend only on read/append primitives while production
// adapters add atomic projection, outbox and flush semantics.
type ControlRepository interface {
	Repository
	// CreatePlanWithEvent commits the immutable plan, its initial projection,
	// and the lifecycle event in one durable transaction.
	CreatePlanWithEvent(RequirementExecutionPlan, Event) error
	Flush() error
	CommitEventProjection(Event, PlanProjection) error
	EnqueueOutbox(OutboxRecord) (OutboxRecord, bool, error)
	ClaimOutbox(planID, owner string, ttl time.Duration, now time.Time) (OutboxRecord, error)
	ClaimOutboxByID(id, owner string, ttl time.Duration, now time.Time) (OutboxRecord, error)
	AckOutbox(id, owner string, now time.Time, deliveryErr error) error
	ListOutbox(planID, status string) []OutboxRecord
}

// OutboxRecord is an immutable intent plus a mutable delivery lease. It is
// persisted with orchestration state so a restart can resume delivery without
// executing an external side effect twice.
type OutboxRecord struct {
	ID             string         `json:"id"`
	PlanID         string         `json:"plan_id"`
	WorkspaceID    string         `json:"workspace_id"`
	Kind           string         `json:"kind"`
	IdempotencyKey string         `json:"idempotency_key"`
	TraceParent    string         `json:"traceparent,omitempty"`
	TraceState     string         `json:"tracestate,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`
	Status         string         `json:"status"` // pending, leased, acked, failed
	Owner          string         `json:"owner,omitempty"`
	LeaseExpiresAt time.Time      `json:"lease_expires_at,omitempty"`
	Attempts       int            `json:"attempts"`
	// MaxAttempts bounds delivery retries. Zero is normalized to
	// DefaultOutboxMaxAttempts when an intent is enqueued, preventing an
	// unavailable provider from leaving an intent retryable forever.
	MaxAttempts int       `json:"max_attempts"`
	LastError   string    `json:"last_error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const DefaultOutboxMaxAttempts = 8

var (
	ErrOutboxConflict     = errors.New("orchestration outbox conflict")
	ErrUnsupportedProfile = errors.New("orchestration production profile is unavailable")
)

type MemoryRepository struct {
	mu          sync.RWMutex
	statePath   string
	revision    int64
	dirty       bool
	agents      map[string]AgentDefinition
	squads      map[string]SquadDefinition
	plans       map[string]RequirementExecutionPlan
	projections map[string]PlanProjection
	keys        map[string]string
	events      map[string][]Event
	outbox      map[string]OutboxRecord
}

func NewMemoryRepository() *MemoryRepository {
	return newMemoryRepository("")
}

// NewPersistentRepository enables the durable single-node orchestration
// profile. The snapshot contains immutable definitions, plans, projections
// and the append-only event chains, so a process restart can resume and replay
// a graph without silently losing orchestration state.
func NewPersistentRepository(path string) (*MemoryRepository, error) {
	r := newMemoryRepository(strings.TrimSpace(path))
	if r.statePath == "" {
		return r, nil
	}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

// SQLiteRepository is the single-node SQLite-compatible profile. The domain
// contract intentionally remains identical to the file profile; deployments
// can replace the storage engine without changing orchestration events. The
// snapshot uses SQLite-safe atomic rename and inter-process locking semantics.
type SQLiteRepository struct {
	*MemoryRepository
	ProfileName string `json:"profile"`
}

func NewSQLiteRepository(path string) (*SQLiteRepository, error) {
	r, err := NewPersistentRepository(path)
	if err != nil {
		return nil, err
	}
	return &SQLiteRepository{MemoryRepository: r, ProfileName: "sqlite-single-node"}, nil
}

// PostgresRepository exposes the production repository boundary. A local file
// path is accepted for deterministic contract tests; a network DSN is
// rejected until a configured SQL driver is supplied rather than silently
// pretending that an in-memory implementation is PostgreSQL durable.
type PostgresRepository struct {
	*MemoryRepository
	ProfileName string `json:"profile"`
	DSN         string `json:"dsn,omitempty"`
}

func NewPostgresRepository(dsn string) (*PostgresRepository, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, fmt.Errorf("%w: postgres DSN is required", ErrUnsupportedProfile)
	}
	if strings.Contains(dsn, "://") && !strings.HasPrefix(dsn, "file://") {
		return nil, fmt.Errorf("%w: configure a PostgreSQL driver for DSN", ErrUnsupportedProfile)
	}
	path := strings.TrimPrefix(dsn, "file://")
	r, err := NewPersistentRepository(path)
	if err != nil {
		return nil, err
	}
	return &PostgresRepository{MemoryRepository: r, ProfileName: "postgres-contract-test", DSN: dsn}, nil
}

func (r *MemoryRepository) Profile() string {
	if r == nil {
		return ""
	}
	if r.statePath == "" {
		return "memory"
	}
	return "file-single-node"
}

func newMemoryRepository(path string) *MemoryRepository {
	return &MemoryRepository{statePath: path, agents: map[string]AgentDefinition{}, squads: map[string]SquadDefinition{}, plans: map[string]RequirementExecutionPlan{}, projections: map[string]PlanProjection{}, keys: map[string]string{}, events: map[string][]Event{}, outbox: map[string]OutboxRecord{}}
}

type persistedRepository struct {
	Version     int                                 `json:"version"`
	Revision    int64                               `json:"revision"`
	Agents      map[string]AgentDefinition          `json:"agents"`
	Squads      map[string]SquadDefinition          `json:"squads"`
	Plans       map[string]RequirementExecutionPlan `json:"plans"`
	Projections map[string]PlanProjection           `json:"projections"`
	Keys        map[string]string                   `json:"keys"`
	Events      map[string][]Event                  `json:"events"`
	Outbox      map[string]OutboxRecord             `json:"outbox,omitempty"`
}

func (r *MemoryRepository) load() error {
	data, err := os.ReadFile(r.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read orchestration state: %w", err)
	}
	var state persistedRepository
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode orchestration state: %w", err)
	}
	r.revision = state.Revision
	if state.Agents != nil {
		r.agents = state.Agents
	}
	if state.Squads != nil {
		r.squads = state.Squads
	}
	if state.Plans != nil {
		r.plans = state.Plans
	}
	if state.Projections != nil {
		r.projections = state.Projections
	}
	if state.Keys != nil {
		r.keys = state.Keys
	}
	if state.Events != nil {
		r.events = state.Events
	}
	if state.Outbox != nil {
		r.outbox = state.Outbox
	}
	for id, plan := range r.plans {
		if plan.ID != id {
			return fmt.Errorf("orchestration plan key mismatch %s", id)
		}
		hash, hashErr := canonicalPlanHash(plan)
		if hashErr != nil || hash != plan.PlanHash {
			return fmt.Errorf("orchestration plan %s hash mismatch", id)
		}
	}
	for id, projection := range r.projections {
		if projection.PlanID != id {
			return fmt.Errorf("orchestration projection key mismatch %s", id)
		}
		if err := projection.Validate(); err != nil {
			return fmt.Errorf("orchestration projection %s: %w", id, err)
		}
	}
	for planID, events := range r.events {
		if len(events) == 0 {
			continue
		}
		if err := ValidateEventChain(events, planID, events[0].WorkspaceID); err != nil {
			// Keep the persisted bytes available in the returned error; callers can
			// distinguish corruption from a missing optional profile.
			return fmt.Errorf("orchestration event chain %s: %w", planID, err)
		}
	}
	return nil
}

// Flush atomically writes a dirty durable snapshot. It is safe to call after
// every HTTP request; ephemeral repositories are a no-op.
func (r *MemoryRepository) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.persistLocked()
}

func (r *MemoryRepository) persistLocked() error {
	if r.statePath == "" || !r.dirty {
		return nil
	}
	return durable.WithExclusive(r.statePath, func() error {
		diskRevision, err := orchestrationRevision(r.statePath)
		if err != nil {
			return err
		}
		if diskRevision != r.revision {
			return fmt.Errorf("stale orchestration state: expected revision %d, found %d", r.revision, diskRevision)
		}
		state := persistedRepository{Version: 1, Revision: r.revision + 1, Agents: r.agents, Squads: r.squads, Plans: r.plans, Projections: r.projections, Keys: r.keys, Events: r.events, Outbox: r.outbox}
		data, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return err
		}
		dir := filepath.Dir(r.statePath)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(dir, ".adro-orchestration-*")
		if err != nil {
			return err
		}
		tmpName := tmp.Name()
		defer os.Remove(tmpName)
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
		if err := durable.Inject("orchestration.snapshot.before_rename"); err != nil {
			return err
		}
		if err := os.Rename(tmpName, r.statePath); err != nil {
			return err
		}
		if dirFile, err := os.Open(dir); err == nil {
			syncErr := dirFile.Sync()
			_ = dirFile.Close()
			if syncErr != nil {
				return syncErr
			}
		}
		r.revision = state.Revision
		r.dirty = false
		return nil
	})
}

// Backup writes a self-validating copy of the complete orchestration state.
// The copy is never exposed before fsync and uses the same lock/rename rules
// as the primary snapshot.
func (r *MemoryRepository) Backup(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("backup path is required")
	}
	r.mu.RLock()
	state := persistedRepository{Version: 1, Revision: r.revision, Agents: cloneValue(r.agents), Squads: cloneValue(r.squads), Plans: cloneValue(r.plans), Projections: cloneValue(r.projections), Keys: cloneValue(r.keys), Events: cloneValue(r.events), Outbox: cloneValue(r.outbox)}
	r.mu.RUnlock()
	return writeRepositorySnapshot(path, state)
}

// Restore replaces all mutable state only after the backup has passed plan,
// projection and event-chain validation. Existing state remains untouched if
// decoding, validation or the atomic rename fails.
func (r *MemoryRepository) Restore(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("restore path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read orchestration backup: %w", err)
	}
	var state persistedRepository
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode orchestration backup: %w", err)
	}
	if err := validateRepositoryState(state); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old := persistedRepository{Version: 1, Revision: r.revision, Agents: r.agents, Squads: r.squads, Plans: r.plans, Projections: r.projections, Keys: r.keys, Events: r.events, Outbox: r.outbox}
	r.revision, r.agents, r.squads, r.plans = state.Revision, state.Agents, state.Squads, state.Plans
	r.projections, r.keys, r.events, r.outbox = state.Projections, state.Keys, state.Events, state.Outbox
	if r.agents == nil {
		r.agents = map[string]AgentDefinition{}
	}
	if r.squads == nil {
		r.squads = map[string]SquadDefinition{}
	}
	if r.plans == nil {
		r.plans = map[string]RequirementExecutionPlan{}
	}
	if r.projections == nil {
		r.projections = map[string]PlanProjection{}
	}
	if r.keys == nil {
		r.keys = map[string]string{}
	}
	if r.events == nil {
		r.events = map[string][]Event{}
	}
	if r.outbox == nil {
		r.outbox = map[string]OutboxRecord{}
	}
	r.dirty = true
	if err := r.persistLocked(); err != nil {
		r.revision, r.agents, r.squads, r.plans = old.Revision, old.Agents, old.Squads, old.Plans
		r.projections, r.keys, r.events, r.outbox, r.dirty = old.Projections, old.Keys, old.Events, old.Outbox, false
		return fmt.Errorf("persist restored orchestration state: %w", err)
	}
	return nil
}

func validateRepositoryState(state persistedRepository) error {
	if state.Version != 0 && state.Version != 1 {
		return fmt.Errorf("unsupported orchestration snapshot version %d", state.Version)
	}
	for id, plan := range state.Plans {
		if plan.ID != id {
			return fmt.Errorf("orchestration plan key mismatch %s", id)
		}
		hash, err := canonicalPlanHash(plan)
		if err != nil || hash != plan.PlanHash {
			return fmt.Errorf("orchestration plan %s hash mismatch", id)
		}
	}
	for id, projection := range state.Projections {
		if projection.PlanID != id {
			return fmt.Errorf("orchestration projection key mismatch %s", id)
		}
		if err := projection.Validate(); err != nil {
			return fmt.Errorf("orchestration projection %s: %w", id, err)
		}
	}
	for planID, chain := range state.Events {
		if len(chain) == 0 {
			continue
		}
		if err := ValidateEventChain(chain, planID, chain[0].WorkspaceID); err != nil {
			return fmt.Errorf("orchestration event chain %s: %w", planID, err)
		}
	}
	for id, record := range state.Outbox {
		if id == "" || record.ID != id || record.PlanID == "" || record.WorkspaceID == "" || record.IdempotencyKey == "" {
			return fmt.Errorf("invalid orchestration outbox record %s", id)
		}
	}
	return nil
}

func writeRepositorySnapshot(path string, state persistedRepository) error {
	return durable.WithExclusive(path, func() error {
		data, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return err
		}
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(dir, ".adro-orchestration-backup-*")
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
		if err := durable.Inject("orchestration.backup.before_rename"); err != nil {
			return err
		}
		if err := os.Rename(name, path); err != nil {
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
	})
}

func orchestrationRevision(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var state persistedRepository
	if err := json.Unmarshal(data, &state); err != nil {
		return 0, err
	}
	return state.Revision, nil
}
func key3(ws, id string, rev int64) string { return fmt.Sprintf("%s:%s:%d", ws, id, rev) }

func cloneValue[T any](in T) T {
	data, err := json.Marshal(in)
	if err != nil {
		return in
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		return in
	}
	return out
}
func (r *MemoryRepository) SaveAgent(a AgentDefinition, expected int64) error {
	if err := a.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.agents {
		if x.ID == a.ID && x.WorkspaceID == a.WorkspaceID && x.Revision == a.Revision {
			return errors.New("agent revision already exists")
		}
	}
	if expected > 0 {
		latest := int64(0)
		for _, x := range r.agents {
			if x.ID == a.ID && x.WorkspaceID == a.WorkspaceID && x.Revision > latest {
				latest = x.Revision
			}
		}
		if latest != expected {
			return fmt.Errorf("agent revision conflict: expected %d, found %d", expected, latest)
		}
	}
	k := key3(a.WorkspaceID, a.ID, a.Revision)
	old, existed := r.agents[k]
	oldDirty, oldRevision := r.dirty, r.revision
	r.agents[k] = a
	r.dirty = true
	if err := r.persistLocked(); err != nil {
		if existed {
			r.agents[k] = old
		} else {
			delete(r.agents, k)
		}
		r.dirty, r.revision = oldDirty, oldRevision
		return err
	}
	return nil
}
func (r *MemoryRepository) GetAgent(ws, id string, rev int64) (AgentDefinition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if rev > 0 {
		a, ok := r.agents[key3(ws, id, rev)]
		if !ok {
			return AgentDefinition{}, ErrNotFound
		}
		return cloneValue(a), nil
	}
	var out AgentDefinition
	for _, a := range r.agents {
		if a.WorkspaceID == ws && a.ID == id && a.Revision > out.Revision {
			out = a
		}
	}
	if out.ID == "" {
		return out, ErrNotFound
	}
	return cloneValue(out), nil
}
func (r *MemoryRepository) ListAgents(ws string, status AgentStatus) []AgentDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AgentDefinition, 0)
	latest := map[string]AgentDefinition{}
	for _, a := range r.agents {
		if ws != "" && a.WorkspaceID != ws {
			continue
		}
		if status != "" && a.Status != status {
			continue
		}
		key := a.WorkspaceID + ":" + a.ID
		if old, ok := latest[key]; !ok || a.Revision > old.Revision {
			latest[key] = a
		}
	}
	for _, a := range latest {
		out = append(out, cloneValue(a))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out
}
func (r *MemoryRepository) SaveSquad(s SquadDefinition, expected int64) error {
	if s.Status == SquadDraft {
		if err := s.ValidateDraft(); err != nil {
			return err
		}
	} else if err := s.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.Status == SquadPublished {
		if err := r.validatePublishedSquadLocked(s); err != nil {
			return err
		}
	}
	for _, x := range r.squads {
		if x.ID == s.ID && x.WorkspaceID == s.WorkspaceID && x.Revision == s.Revision {
			return errors.New("squad revision already exists")
		}
	}
	if expected > 0 {
		latest := int64(0)
		for _, x := range r.squads {
			if x.ID == s.ID && x.WorkspaceID == s.WorkspaceID && x.Revision > latest {
				latest = x.Revision
			}
		}
		if latest != expected {
			return fmt.Errorf("squad revision conflict: expected %d, found %d", expected, latest)
		}
	}
	k := key3(s.WorkspaceID, s.ID, s.Revision)
	old, existed := r.squads[k]
	oldDirty, oldRevision := r.dirty, r.revision
	r.squads[k] = s
	r.dirty = true
	if err := r.persistLocked(); err != nil {
		if existed {
			r.squads[k] = old
		} else {
			delete(r.squads, k)
		}
		r.dirty, r.revision = oldDirty, oldRevision
		return err
	}
	return nil
}

func (r *MemoryRepository) validatePublishedSquadLocked(s SquadDefinition) error {
	if leaders := countSquadLeaders(s.Members); leaders != 1 {
		return fmt.Errorf("published squad requires exactly one leader agent")
	}
	if err := r.validateSquadGraphRefsLocked(s, 0, map[string]bool{}); err != nil {
		return err
	}
	for i, member := range s.Members {
		if member.AgentID != "" {
			agent, ok := r.latestAgentLocked(s.WorkspaceID, member.AgentID)
			if !ok {
				return fmt.Errorf("members[%d].agent_id.unavailable", i)
			}
			if agent.Status != AgentActive {
				return fmt.Errorf("members[%d].agent_id.inactive", i)
			}
			for _, required := range member.CapabilityConstraints {
				if !required.Required {
					continue
				}
				found := false
				for _, capability := range agent.Capabilities {
					if capability.Name == required.Name {
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("members[%d].capability_constraints.%s.unavailable", i, required.Name)
				}
			}
		}
		if member.SquadID != "" {
			if member.SquadID == s.ID {
				return fmt.Errorf("members[%d].squad_id.self_reference", i)
			}
			nested, ok := r.latestSquadLocked(s.WorkspaceID, member.SquadID)
			if !ok || nested.Status != SquadPublished {
				return fmt.Errorf("members[%d].squad_id.unavailable", i)
			}
			if s.Policy.MaxNestingDepth > 0 && nested.Policy.MaxNestingDepth >= s.Policy.MaxNestingDepth {
				return fmt.Errorf("members[%d].squad_id.nesting_depth_exceeded", i)
			}
		}
	}
	for i, node := range s.Graph.Nodes {
		if node.AgentRef != nil {
			agent, ok := r.agentLocked(s.WorkspaceID, node.AgentRef.ID, node.AgentRef.Revision)
			if !ok || agent.Status != AgentActive {
				return fmt.Errorf("graph.nodes[%d].agent_ref.unavailable", i)
			}
		}
		if node.SquadRef != nil {
			nested, ok := r.squadLocked(s.WorkspaceID, node.SquadRef.ID, node.SquadRef.Revision)
			if !ok || nested.Status != SquadPublished {
				return fmt.Errorf("graph.nodes[%d].squad_ref.unavailable", i)
			}
		}
	}
	return nil
}

func countSquadLeaders(members []SquadMember) int {
	count := 0
	for _, member := range members {
		if member.Leader {
			count++
		}
	}
	return count
}

// validateSquadGraphRefsLocked walks the complete nested member and graph
// reference tree. Comparing only adjacent policy values lets a deeply nested
// published squad bypass the root's depth limit; this traversal computes the
// actual depth and rejects cycles before a revision can be published.
func (r *MemoryRepository) validateSquadGraphRefsLocked(s SquadDefinition, depth int, visiting map[string]bool) error {
	if s.Policy.MaxNestingDepth > 0 && depth > s.Policy.MaxNestingDepth {
		return fmt.Errorf("squad %s nesting depth exceeded", s.ID)
	}
	key := s.WorkspaceID + ":" + s.ID + ":" + fmt.Sprint(s.Revision)
	if visiting[key] {
		return fmt.Errorf("squad %s nested cycle detected", s.ID)
	}
	visiting[key] = true
	defer delete(visiting, key)
	refs := make([]string, 0)
	for _, member := range s.Members {
		if member.SquadID != "" {
			refs = append(refs, member.SquadID)
		}
	}
	for _, node := range s.Graph.Nodes {
		if node.SquadRef != nil {
			refs = append(refs, node.SquadRef.ID)
		}
	}
	for _, nestedID := range refs {
		nested, ok := r.latestSquadLocked(s.WorkspaceID, nestedID)
		if !ok || nested.Status != SquadPublished {
			return fmt.Errorf("squad %s nested squad %s unavailable", s.ID, nestedID)
		}
		if s.Policy.MaxNestingDepth > 0 && depth+1 > s.Policy.MaxNestingDepth {
			return fmt.Errorf("squad %s nested squad %s exceeds max_nesting_depth", s.ID, nestedID)
		}
		if err := r.validateSquadGraphRefsLocked(nested, depth+1, visiting); err != nil {
			return err
		}
	}
	return nil
}

func (r *MemoryRepository) agentLocked(ws, id string, rev int64) (AgentDefinition, bool) {
	if rev > 0 {
		a, ok := r.agents[key3(ws, id, rev)]
		return a, ok
	}
	return r.latestAgentLocked(ws, id)
}
func (r *MemoryRepository) latestAgentLocked(ws, id string) (AgentDefinition, bool) {
	var out AgentDefinition
	for _, a := range r.agents {
		if a.WorkspaceID == ws && a.ID == id && a.Revision > out.Revision {
			out = a
		}
	}
	return out, out.ID != ""
}
func (r *MemoryRepository) squadLocked(ws, id string, rev int64) (SquadDefinition, bool) {
	if rev > 0 {
		s, ok := r.squads[key3(ws, id, rev)]
		return s, ok
	}
	return r.latestSquadLocked(ws, id)
}
func (r *MemoryRepository) latestSquadLocked(ws, id string) (SquadDefinition, bool) {
	var out SquadDefinition
	for _, s := range r.squads {
		if s.WorkspaceID == ws && s.ID == id && s.Revision > out.Revision {
			out = s
		}
	}
	return out, out.ID != ""
}
func (r *MemoryRepository) GetSquad(ws, id string, rev int64) (SquadDefinition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if rev > 0 {
		s, ok := r.squads[key3(ws, id, rev)]
		if !ok {
			return SquadDefinition{}, ErrNotFound
		}
		return cloneValue(s), nil
	}
	var out SquadDefinition
	for _, s := range r.squads {
		if s.WorkspaceID == ws && s.ID == id && s.Revision > out.Revision {
			out = s
		}
	}
	if out.ID == "" {
		return out, ErrNotFound
	}
	return cloneValue(out), nil
}
func (r *MemoryRepository) ListSquads(ws string, status SquadStatus) []SquadDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SquadDefinition, 0)
	latest := map[string]SquadDefinition{}
	for _, s := range r.squads {
		if ws != "" && s.WorkspaceID != ws {
			continue
		}
		if status != "" && s.Status != status {
			continue
		}
		key := s.WorkspaceID + ":" + s.ID
		if old, ok := latest[key]; !ok || s.Revision > old.Revision {
			latest[key] = s
		}
	}
	for _, s := range latest {
		out = append(out, cloneValue(s))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out
}
func (r *MemoryRepository) CreatePlan(p RequirementExecutionPlan) error {
	if p.ID == "" || p.WorkspaceID == "" || p.RequirementID == "" || p.PlanHash == "" {
		return errors.New("plan id, workspace, requirement and hash are required")
	}
	if p.Status != PlanReady && p.Status != PlanRunning && p.Status != PlanWaiting && p.Status != PlanTerminal {
		return errors.New("plan must be frozen before persistence")
	}
	hash, err := canonicalPlanHash(p)
	if err != nil || hash != p.PlanHash {
		return errors.New("plan hash does not match frozen snapshot")
	}
	projection, err := NewProjection(p)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	oldKey, hadKey := "", false
	if p.IdempotencyKey != "" {
		k := p.WorkspaceID + ":" + p.IdempotencyKey
		oldKey, hadKey = r.keys[k]
		if existing := r.keys[k]; existing != "" {
			if existing != p.PlanHash {
				return ErrIdempotencyConflict
			}
			for _, existingPlan := range r.plans {
				if existingPlan.WorkspaceID == p.WorkspaceID && existingPlan.IdempotencyKey == p.IdempotencyKey && existingPlan.PlanHash == p.PlanHash {
					return nil
				}
			}
			return nil
		}
		r.keys[k] = p.PlanHash
	}
	if _, ok := r.plans[p.ID]; ok {
		if p.IdempotencyKey != "" {
			k := p.WorkspaceID + ":" + p.IdempotencyKey
			if hadKey {
				r.keys[k] = oldKey
			} else {
				delete(r.keys, k)
			}
		}
		return errors.New("plan already exists")
	}
	oldProjection, hadProjection := r.projections[p.ID]
	oldDirty, oldRevision := r.dirty, r.revision
	r.plans[p.ID] = p
	// A frozen plan is recoverable immediately. Callers may still replace this
	// initial projection with a later reducer state, but no plan can exist
	// durably without a projection seed.
	r.projections[p.ID] = projection
	r.dirty = true
	if err := r.persistLocked(); err != nil {
		delete(r.plans, p.ID)
		if hadProjection {
			r.projections[p.ID] = oldProjection
		} else {
			delete(r.projections, p.ID)
		}
		if p.IdempotencyKey != "" {
			k := p.WorkspaceID + ":" + p.IdempotencyKey
			if hadKey {
				r.keys[k] = oldKey
			} else {
				delete(r.keys, k)
			}
		}
		r.dirty, r.revision = oldDirty, oldRevision
		return err
	}
	return nil
}

// CreatePlanWithEvent is the plan creation transaction boundary. Keeping the
// plan, seeded projection, and lifecycle event under one repository lock
// prevents a durable plan from becoming visible without the event needed for
// replay and audit.
func (r *MemoryRepository) CreatePlanWithEvent(p RequirementExecutionPlan, event Event) error {
	if p.ID == "" || p.WorkspaceID == "" || p.RequirementID == "" || p.PlanHash == "" {
		return errors.New("plan id, workspace, requirement and hash are required")
	}
	if p.Status != PlanReady && p.Status != PlanRunning && p.Status != PlanWaiting && p.Status != PlanTerminal {
		return errors.New("plan must be frozen before persistence")
	}
	hash, err := canonicalPlanHash(p)
	if err != nil || hash != p.PlanHash {
		return errors.New("plan hash does not match frozen snapshot")
	}
	projection, err := NewProjection(p)
	if err != nil {
		return err
	}
	if event.PlanID != p.ID || event.WorkspaceID != p.WorkspaceID || event.Type == "" {
		return errors.New("plan event scope does not match plan")
	}
	if strings.TrimSpace(event.IdempotencyKey) == "" {
		event.IdempotencyKey = p.ID + ":" + event.Type
		event.EnvelopeHash = eventDigest(event)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.plans[p.ID]; ok {
		if existing.PlanHash != p.PlanHash {
			return ErrIdempotencyConflict
		}
		// Idempotent retries repair only a missing lifecycle event; an existing
		// equal event is accepted by appendEventLocked without duplication.
		oldEvents := append([]Event(nil), r.events[p.ID]...)
		oldDirty, oldRevision := r.dirty, r.revision
		if err := r.appendEventLocked(event); err != nil {
			return err
		}
		if err := r.persistLocked(); err != nil {
			r.events[p.ID] = oldEvents
			r.dirty, r.revision = oldDirty, oldRevision
			return err
		}
		return nil
	}
	if p.IdempotencyKey != "" {
		key := p.WorkspaceID + ":" + p.IdempotencyKey
		if existingHash := r.keys[key]; existingHash != "" && existingHash != p.PlanHash {
			return ErrIdempotencyConflict
		}
		r.keys[key] = p.PlanHash
	}
	oldDirty, oldRevision := r.dirty, r.revision
	r.plans[p.ID] = p
	r.projections[p.ID] = projection
	if err := r.appendEventLocked(event); err != nil {
		delete(r.plans, p.ID)
		delete(r.projections, p.ID)
		if p.IdempotencyKey != "" {
			delete(r.keys, p.WorkspaceID+":"+p.IdempotencyKey)
		}
		r.dirty, r.revision = oldDirty, oldRevision
		return err
	}
	r.dirty = true
	if err := r.persistLocked(); err != nil {
		delete(r.plans, p.ID)
		delete(r.projections, p.ID)
		delete(r.events, p.ID)
		if p.IdempotencyKey != "" {
			delete(r.keys, p.WorkspaceID+":"+p.IdempotencyKey)
		}
		r.dirty, r.revision = oldDirty, oldRevision
		return err
	}
	return nil
}

func (r *MemoryRepository) validatePlanReferencesLocked(p RequirementExecutionPlan) error {
	for i, node := range p.GraphSnapshot.Nodes {
		switch node.Kind {
		case NodeAgent:
			if node.AgentRef == nil {
				continue
			}
			agent, ok := r.agents[key3(p.WorkspaceID, node.AgentRef.ID, node.AgentRef.Revision)]
			if !ok {
				return fmt.Errorf("graph.nodes[%d].agent_ref.unavailable", i)
			}
			if agent.Status != AgentActive {
				return fmt.Errorf("graph.nodes[%d].agent_ref.inactive", i)
			}
		case NodeSquad:
			if node.SquadRef == nil {
				continue
			}
			var squad SquadDefinition
			var found bool
			if node.SquadRef.Revision > 0 {
				squad, found = r.squads[key3(p.WorkspaceID, node.SquadRef.ID, node.SquadRef.Revision)]
			} else {
				squad, found = r.latestSquadLocked(p.WorkspaceID, node.SquadRef.ID)
			}
			if !found || squad.Status != SquadPublished {
				return fmt.Errorf("graph.nodes[%d].squad_ref.unavailable", i)
			}
			if node.SquadRef.Version > 0 && squad.PublishedVersion != node.SquadRef.Version {
				return fmt.Errorf("graph.nodes[%d].squad_ref.version_mismatch", i)
			}
		}
	}
	return nil
}
func (r *MemoryRepository) GetPlan(ws, id string) (RequirementExecutionPlan, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plans[id]
	if !ok || p.WorkspaceID != ws {
		return RequirementExecutionPlan{}, ErrNotFound
	}
	return cloneValue(p), nil
}

// GetPlanByIdempotency returns the original frozen plan for a scoped key. The
// caller can use this before generating a new plan ID so retries return the
// exact durable result rather than a semantically equivalent duplicate.
func (r *MemoryRepository) GetPlanByIdempotency(ws, idempotencyKey string) (RequirementExecutionPlan, error) {
	ws, idempotencyKey = strings.TrimSpace(ws), strings.TrimSpace(idempotencyKey)
	if ws == "" || idempotencyKey == "" {
		return RequirementExecutionPlan{}, ErrNotFound
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if hash := r.keys[ws+":"+idempotencyKey]; hash != "" {
		for _, plan := range r.plans {
			if plan.WorkspaceID == ws && plan.IdempotencyKey == idempotencyKey && plan.PlanHash == hash {
				return cloneValue(plan), nil
			}
		}
	}
	return RequirementExecutionPlan{}, ErrNotFound
}

func (r *MemoryRepository) ListPlans(ws string) []RequirementExecutionPlan {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RequirementExecutionPlan, 0)
	for _, p := range r.plans {
		if ws == "" || p.WorkspaceID == ws {
			out = append(out, cloneValue(p))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}
func (r *MemoryRepository) SaveProjection(p PlanProjection) error {
	if p.PlanID == "" {
		return errors.New("plan id is required")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	plan, ok := r.plans[p.PlanID]
	if !ok {
		return ErrNotFound
	}
	if p.Revision != plan.Revision {
		return fmt.Errorf("projection revision %d does not match plan revision %d", p.Revision, plan.Revision)
	}
	if old, ok := r.projections[p.PlanID]; ok && p.Revision < old.Revision {
		return errors.New("projection revision regressed")
	}
	oldProjection, hadProjection := r.projections[p.PlanID]
	oldDirty, oldRevision := r.dirty, r.revision
	// Detach maps/slices from the caller so a projection cannot be mutated after
	// a successful save without another repository operation.
	cp := p
	cp.Nodes = map[string]NodeProjection{}
	for k, v := range p.Nodes {
		cp.Nodes[k] = cloneValue(v)
	}
	cp.Attempts = map[string]NodeAttempt{}
	for k, v := range p.Attempts {
		cp.Attempts[k] = cloneValue(v)
	}
	cp.Traversals = map[string]int{}
	for k, v := range p.Traversals {
		cp.Traversals[k] = v
	}
	cp.Idempotency = map[string]string{}
	for k, v := range p.Idempotency {
		cp.Idempotency[k] = v
	}
	cp.Decisions = cloneValue(append([]FeedbackDecision(nil), p.Decisions...))
	r.projections[p.PlanID] = cp
	r.dirty = true
	if err := r.persistLocked(); err != nil {
		if hadProjection {
			r.projections[p.PlanID] = oldProjection
		} else {
			delete(r.projections, p.PlanID)
		}
		r.dirty, r.revision = oldDirty, oldRevision
		return err
	}
	return nil
}
func (r *MemoryRepository) GetProjection(id string) (PlanProjection, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.projections[id]
	if !ok {
		return PlanProjection{}, ErrNotFound
	}
	return cloneProjection(p), nil
}

// AppendEvent is the local-profile transaction boundary: sequence, chain hash,
// idempotency and fencing are checked before the event becomes visible.
func (r *MemoryRepository) AppendEvent(e Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	oldDirty, oldRevision := r.dirty, r.revision
	beforeLen := len(r.events[e.PlanID])
	if err := r.appendEventLocked(e); err != nil {
		return err
	}
	if err := r.persistLocked(); err != nil {
		// appendEventLocked updates only the in-memory chain. Restore the old
		// tail when the durable transaction fails.
		if len(r.events[e.PlanID]) > beforeLen {
			r.events[e.PlanID] = r.events[e.PlanID][:beforeLen]
			if beforeLen == 0 {
				delete(r.events, e.PlanID)
			}
		}
		r.dirty, r.revision = oldDirty, oldRevision
		return err
	}
	return nil
}

func (r *MemoryRepository) appendEventLocked(e Event) error {
	plan, ok := r.plans[e.PlanID]
	if !ok {
		return ErrNotFound
	}
	if e.WorkspaceID != plan.WorkspaceID {
		return errors.New("event workspace does not match plan workspace")
	}
	events := r.events[e.PlanID]
	for _, old := range events {
		if e.IdempotencyKey != "" && old.IdempotencyKey == e.IdempotencyKey {
			if old.PayloadHash == e.PayloadHash {
				return nil
			}
			return ErrIdempotencyConflict
		}
	}
	expected := int64(len(events) + 1)
	// A zero sequence (or a freshly-created event with sequence one and no
	// predecessor) means "append at the current tail". Assigning the sequence
	// while holding the repository lock makes concurrent workers deterministic.
	if e.Sequence == 0 || (e.Sequence == 1 && expected > 1 && e.PreviousHash == "") {
		e.Sequence = expected
		if len(events) > 0 {
			e.PreviousHash = events[len(events)-1].EnvelopeHash
		}
		e.EnvelopeHash = eventDigest(e)
	}
	if e.Sequence != expected {
		return fmt.Errorf("event sequence conflict: got %d", e.Sequence)
	}
	if len(events) > 0 && e.PreviousHash != events[len(events)-1].EnvelopeHash {
		return errors.New("event previous hash conflict")
	}
	if err := ValidateEventChain(append(append([]Event(nil), events...), e), e.PlanID, e.WorkspaceID); err != nil {
		return err
	}
	r.events[e.PlanID] = append(events, e)
	r.dirty = true
	return nil
}

// CommitEventProjection atomically commits a reducer projection and its
// immutable event. It is the local equivalent of a database transaction and
// is used by workers at the attempt completion boundary.
func (r *MemoryRepository) CommitEventProjection(e Event, p PlanProjection) error {
	if p.PlanID == "" || e.PlanID != p.PlanID {
		return errors.New("event and projection plan scope mismatch")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	plan, ok := r.plans[p.PlanID]
	if !ok {
		return ErrNotFound
	}
	if p.Revision != plan.Revision || e.WorkspaceID != plan.WorkspaceID {
		return errors.New("event or projection does not match frozen plan scope")
	}
	oldEvents := append([]Event(nil), r.events[e.PlanID]...)
	oldProjection, hadProjection := r.projections[p.PlanID]
	oldDirty, oldRevision := r.dirty, r.revision
	if err := r.appendEventLocked(e); err != nil {
		return err
	}
	r.projections[p.PlanID] = cloneProjection(p)
	r.dirty = true
	if err := r.persistLocked(); err != nil {
		r.events[e.PlanID] = oldEvents
		if hadProjection {
			r.projections[p.PlanID] = oldProjection
		} else {
			delete(r.projections, p.PlanID)
		}
		r.dirty, r.revision = oldDirty, oldRevision
		return err
	}
	return nil
}

// EnqueueOutbox inserts an intent exactly once by scoped idempotency key.
func (r *MemoryRepository) EnqueueOutbox(record OutboxRecord) (OutboxRecord, bool, error) {
	record.PlanID = strings.TrimSpace(record.PlanID)
	record.WorkspaceID = strings.TrimSpace(record.WorkspaceID)
	record.IdempotencyKey = strings.TrimSpace(record.IdempotencyKey)
	if record.PlanID == "" || record.WorkspaceID == "" || record.IdempotencyKey == "" || strings.TrimSpace(record.Kind) == "" {
		return OutboxRecord{}, false, errors.New("outbox plan, workspace, kind and idempotency key are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.outbox {
		if existing.PlanID == record.PlanID && existing.IdempotencyKey == record.IdempotencyKey {
			if !jsonEqual(existing.Payload, record.Payload) || existing.Kind != record.Kind {
				return OutboxRecord{}, false, ErrOutboxConflict
			}
			return cloneValue(existing), false, nil
		}
	}
	if record.ID == "" {
		record.ID = fmt.Sprintf("outbox-%d", time.Now().UnixNano())
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	record.UpdatedAt = record.CreatedAt
	if record.Status == "" {
		record.Status = "pending"
	}
	if record.MaxAttempts <= 0 {
		record.MaxAttempts = DefaultOutboxMaxAttempts
	}
	if record.MaxAttempts < 1 {
		return OutboxRecord{}, false, errors.New("outbox max_attempts must be positive")
	}
	oldDirty, oldRevision := r.dirty, r.revision
	if r.outbox == nil {
		r.outbox = map[string]OutboxRecord{}
	}
	r.outbox[record.ID] = cloneValue(record)
	r.dirty = true
	if err := r.persistLocked(); err != nil {
		delete(r.outbox, record.ID)
		r.dirty, r.revision = oldDirty, oldRevision
		return OutboxRecord{}, false, err
	}
	return cloneValue(record), true, nil
}

// ClaimOutbox leases the next pending or expired record. Acknowledgement is
// fenced by owner and lease expiry, so only one worker may deliver an intent.
func (r *MemoryRepository) ClaimOutbox(planID, owner string, ttl time.Duration, now time.Time) (OutboxRecord, error) {
	if strings.TrimSpace(owner) == "" || ttl <= 0 {
		return OutboxRecord{}, errors.New("outbox owner and positive ttl are required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var selected OutboxRecord
	for _, item := range r.outbox {
		if planID != "" && item.PlanID != planID {
			continue
		}
		if item.Status == "acked" || item.Status == "failed" || item.MaxAttempts > 0 && item.Attempts >= item.MaxAttempts {
			continue
		}
		if item.Status == "leased" && item.LeaseExpiresAt.After(now) {
			continue
		}
		if selected.ID == "" || item.CreatedAt.Before(selected.CreatedAt) || item.ID < selected.ID {
			selected = item
		}
	}
	if selected.ID == "" {
		return OutboxRecord{}, ErrNotFound
	}
	return r.claimOutboxLocked(selected.ID, owner, ttl, now)
}

// ClaimOutboxByID leases one specific intent. Consumers that have already
// selected an id from a durable queue must use this form; claiming "the next"
// record can accidentally lease a different intent when multiple workers share
// a plan queue.
func (r *MemoryRepository) ClaimOutboxByID(id, owner string, ttl time.Duration, now time.Time) (OutboxRecord, error) {
	if strings.TrimSpace(id) == "" {
		return OutboxRecord{}, errors.New("outbox id is required")
	}
	if strings.TrimSpace(owner) == "" || ttl <= 0 {
		return OutboxRecord{}, errors.New("outbox owner and positive ttl are required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.claimOutboxLocked(strings.TrimSpace(id), owner, ttl, now)
}

func (r *MemoryRepository) claimOutboxLocked(id, owner string, ttl time.Duration, now time.Time) (OutboxRecord, error) {
	selected, ok := r.outbox[id]
	if !ok || selected.Status == "acked" || selected.Status == "failed" {
		return OutboxRecord{}, ErrNotFound
	}
	if selected.Status == "leased" && selected.LeaseExpiresAt.After(now) {
		return OutboxRecord{}, ErrLeaseLost
	}
	selected.Status, selected.Owner = "leased", owner
	selected.LeaseExpiresAt, selected.UpdatedAt = now.Add(ttl), now
	selected.Attempts++
	old := r.outbox[selected.ID]
	r.outbox[selected.ID] = selected
	r.dirty = true
	if err := r.persistLocked(); err != nil {
		r.outbox[selected.ID] = old
		return OutboxRecord{}, err
	}
	return cloneValue(selected), nil
}

func (r *MemoryRepository) AckOutbox(id, owner string, now time.Time, deliveryErr error) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.outbox[strings.TrimSpace(id)]
	if !ok {
		return ErrNotFound
	}
	if item.Status != "leased" || item.Owner != owner || !item.LeaseExpiresAt.After(now) {
		return ErrLeaseLost
	}
	old := item
	if deliveryErr == nil {
		item.Status, item.LastError = "acked", ""
	} else {
		item.LastError = deliveryErr.Error()
		if item.MaxAttempts > 0 && item.Attempts >= item.MaxAttempts {
			item.Status = "failed"
		} else {
			item.Status = "pending"
		}
	}
	item.Owner, item.LeaseExpiresAt, item.UpdatedAt = "", time.Time{}, now
	r.outbox[item.ID] = item
	r.dirty = true
	if err := r.persistLocked(); err != nil {
		r.outbox[item.ID] = old
		return err
	}
	return nil
}

// FailOutbox permanently rejects a leased intent after an operator or worker
// determines that retrying cannot succeed. The lease owner and expiry are
// checked exactly like AckOutbox, so an expired/taken-over worker cannot mark a
// newer delivery attempt failed.
func (r *MemoryRepository) FailOutbox(id, owner string, now time.Time, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("outbox failure reason is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.outbox[strings.TrimSpace(id)]
	if !ok {
		return ErrNotFound
	}
	if item.Status != "leased" || item.Owner != owner || !item.LeaseExpiresAt.After(now) {
		return ErrLeaseLost
	}
	old := item
	item.Status, item.LastError = "failed", reason
	item.Owner, item.LeaseExpiresAt, item.UpdatedAt = "", time.Time{}, now
	r.outbox[item.ID] = item
	r.dirty = true
	if err := r.persistLocked(); err != nil {
		r.outbox[item.ID] = old
		return err
	}
	return nil
}

func (r *MemoryRepository) ListOutbox(planID, status string) []OutboxRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]OutboxRecord, 0)
	for _, item := range r.outbox {
		if planID != "" && item.PlanID != planID {
			continue
		}
		if status != "" && item.Status != status {
			continue
		}
		out = append(out, cloneValue(item))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func jsonEqual(a, b any) bool {
	left, le := json.Marshal(a)
	right, re := json.Marshal(b)
	return le == nil && re == nil && string(left) == string(right)
}

func (r *MemoryRepository) ListEvents(planID string, after int64) []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := r.events[planID]
	out := make([]Event, 0, len(items))
	for _, e := range items {
		if e.Sequence > after {
			out = append(out, cloneValue(e))
		}
	}
	return out
}
