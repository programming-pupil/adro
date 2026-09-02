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
	GetPlan(workspaceID, id string) (RequirementExecutionPlan, error)
	ListPlans(workspaceID string) []RequirementExecutionPlan
	SaveProjection(PlanProjection) error
	GetProjection(planID string) (PlanProjection, error)
	AppendEvent(Event) error
	ListEvents(planID string, after int64) []Event
}

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

func newMemoryRepository(path string) *MemoryRepository {
	return &MemoryRepository{statePath: path, agents: map[string]AgentDefinition{}, squads: map[string]SquadDefinition{}, plans: map[string]RequirementExecutionPlan{}, projections: map[string]PlanProjection{}, keys: map[string]string{}, events: map[string][]Event{}}
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
		state := persistedRepository{Version: 1, Revision: r.revision + 1, Agents: r.agents, Squads: r.squads, Plans: r.plans, Projections: r.projections, Keys: r.keys, Events: r.events}
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
	if err := s.Validate(); err != nil {
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
	for i, member := range s.Members {
		if member.AgentID != "" {
			agent, ok := r.latestAgentLocked(s.WorkspaceID, member.AgentID)
			if !ok {
				return fmt.Errorf("members[%d].agent_id.unavailable", i)
			}
			if agent.Status != AgentActive {
				return fmt.Errorf("members[%d].agent_id.inactive", i)
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
	oldDirty, oldRevision := r.dirty, r.revision
	r.plans[p.ID] = p
	r.dirty = true
	if err := r.persistLocked(); err != nil {
		delete(r.plans, p.ID)
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
func (r *MemoryRepository) GetPlan(ws, id string) (RequirementExecutionPlan, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plans[id]
	if !ok || p.WorkspaceID != ws {
		return RequirementExecutionPlan{}, ErrNotFound
	}
	return cloneValue(p), nil
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
	if err := r.persistLocked(); err != nil {
		r.events[e.PlanID] = events
		r.dirty, r.revision = oldDirty, oldRevision
		return err
	}
	return nil
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
