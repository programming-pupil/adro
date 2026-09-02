package orchestration

import (
	"errors"
	"fmt"
	"sync"
)

var ErrNotFound = errors.New("orchestration record not found")

// Repository is the persistence seam for SQLite/PostgreSQL/file profiles.
// Implementations must keep published plans immutable and compare revisions on
// mutable definitions.
type Repository interface {
	SaveAgent(AgentDefinition, int64) error
	GetAgent(workspaceID, id string, revision int64) (AgentDefinition, error)
	SaveSquad(SquadDefinition, int64) error
	GetSquad(workspaceID, id string, revision int64) (SquadDefinition, error)
	CreatePlan(RequirementExecutionPlan) error
	GetPlan(workspaceID, id string) (RequirementExecutionPlan, error)
	SaveProjection(PlanProjection) error
	GetProjection(planID string) (PlanProjection, error)
}

type MemoryRepository struct {
	mu          sync.RWMutex
	agents      map[string]AgentDefinition
	squads      map[string]SquadDefinition
	plans       map[string]RequirementExecutionPlan
	projections map[string]PlanProjection
	keys        map[string]string
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{agents: map[string]AgentDefinition{}, squads: map[string]SquadDefinition{}, plans: map[string]RequirementExecutionPlan{}, projections: map[string]PlanProjection{}, keys: map[string]string{}}
}
func key3(ws, id string, rev int64) string { return fmt.Sprintf("%s:%s:%d", ws, id, rev) }
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
	r.agents[key3(a.WorkspaceID, a.ID, a.Revision)] = a
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
		return a, nil
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
	return out, nil
}
func (r *MemoryRepository) SaveSquad(s SquadDefinition, expected int64) error {
	if err := s.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
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
	r.squads[key3(s.WorkspaceID, s.ID, s.Revision)] = s
	return nil
}
func (r *MemoryRepository) GetSquad(ws, id string, rev int64) (SquadDefinition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if rev > 0 {
		s, ok := r.squads[key3(ws, id, rev)]
		if !ok {
			return SquadDefinition{}, ErrNotFound
		}
		return s, nil
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
	return out, nil
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
	if p.IdempotencyKey != "" {
		k := p.WorkspaceID + ":" + p.IdempotencyKey
		if existing := r.keys[k]; existing != "" {
			if existing != p.PlanHash {
				return ErrIdempotencyConflict
			}
			return nil
		}
		r.keys[k] = p.PlanHash
	}
	if _, ok := r.plans[p.ID]; ok {
		return errors.New("plan already exists")
	}
	r.plans[p.ID] = p
	return nil
}
func (r *MemoryRepository) GetPlan(ws, id string) (RequirementExecutionPlan, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plans[id]
	if !ok || p.WorkspaceID != ws {
		return RequirementExecutionPlan{}, ErrNotFound
	}
	return p, nil
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
	// Detach maps/slices from the caller so a projection cannot be mutated after
	// a successful save without another repository operation.
	cp := p
	cp.Nodes = map[string]NodeProjection{}
	for k, v := range p.Nodes {
		cp.Nodes[k] = v
	}
	cp.Attempts = map[string]NodeAttempt{}
	for k, v := range p.Attempts {
		cp.Attempts[k] = v
	}
	cp.Traversals = map[string]int{}
	for k, v := range p.Traversals {
		cp.Traversals[k] = v
	}
	cp.Idempotency = map[string]string{}
	for k, v := range p.Idempotency {
		cp.Idempotency[k] = v
	}
	cp.Decisions = append([]FeedbackDecision(nil), p.Decisions...)
	r.projections[p.PlanID] = cp
	return nil
}
func (r *MemoryRepository) GetProjection(id string) (PlanProjection, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.projections[id]
	if !ok {
		return PlanProjection{}, ErrNotFound
	}
	return p, nil
}
