// Package store provides the reference control-plane repository. The storage
// interfaces are intentionally small and can later be backed by PostgreSQL.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/internal/domain"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

type Memory struct {
	mu               sync.RWMutex
	statePath        string
	requirements     map[string]domain.Requirement
	bugs             map[string]domain.Bug
	attachments      map[string]domain.EntityAttachment
	workItems        map[string]domain.WorkItem
	evidence         map[string]domain.EvidenceBundle
	provenance       map[string]domain.Provenance
	providerBindings map[string]domain.ProviderBinding
	impactReports    map[string][]domain.ImpactReport
	idempotency      map[string]any
	repositories     map[string]domain.Repository
	teamWorkspaces   map[string]domain.TeamWorkspace
	profiles         map[string]domain.DeveloperProfile
	mcpServers       map[string]domain.MCPServer
	skills           map[string]domain.Skill
	automations      map[string]domain.Automation
	approvals        map[string]domain.Approval
	diffs            map[string]domain.DiffSnapshot
	migrations       map[string]domain.ArtifactMigration
	invocations      map[string]domain.MCPInvocation
	bindings         map[string]domain.CapabilityBinding
	automationRuns   map[string]domain.AutomationRun
	contexts         map[string][]domain.ContextManifest
	repairAttempts   map[string][]domain.RepairAttempt
	pipelines        map[string]domain.PipelineRun
}

func NewMemory() *Memory {
	return newMemory("")
}

// NewPersistentMemory enables the single-node durable profile. The file is a
// versioned, atomically replaced snapshot; production deployments can replace
// this repository with PostgreSQL without changing API/domain contracts.
func NewPersistentMemory(path string) (*Memory, error) {
	m := newMemory(path)
	if strings.TrimSpace(path) == "" {
		return m, nil
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func newMemory(path string) *Memory {
	return &Memory{statePath: path, requirements: map[string]domain.Requirement{}, bugs: map[string]domain.Bug{}, attachments: map[string]domain.EntityAttachment{}, workItems: map[string]domain.WorkItem{}, evidence: map[string]domain.EvidenceBundle{}, provenance: map[string]domain.Provenance{}, providerBindings: map[string]domain.ProviderBinding{}, impactReports: map[string][]domain.ImpactReport{}, idempotency: map[string]any{}, repositories: map[string]domain.Repository{}, teamWorkspaces: map[string]domain.TeamWorkspace{}, profiles: map[string]domain.DeveloperProfile{}, mcpServers: map[string]domain.MCPServer{}, skills: map[string]domain.Skill{}, automations: map[string]domain.Automation{}, approvals: map[string]domain.Approval{}, diffs: map[string]domain.DiffSnapshot{}, migrations: map[string]domain.ArtifactMigration{}, invocations: map[string]domain.MCPInvocation{}, bindings: map[string]domain.CapabilityBinding{}, automationRuns: map[string]domain.AutomationRun{}, contexts: map[string][]domain.ContextManifest{}, repairAttempts: map[string][]domain.RepairAttempt{}, pipelines: map[string]domain.PipelineRun{}}
}

type persistedState struct {
	Version          int                                 `json:"version"`
	Requirements     map[string]domain.Requirement       `json:"requirements"`
	Bugs             map[string]domain.Bug               `json:"bugs"`
	Attachments      map[string]domain.EntityAttachment  `json:"attachments"`
	WorkItems        map[string]domain.WorkItem          `json:"work_items"`
	Evidence         map[string]domain.EvidenceBundle    `json:"evidence"`
	Provenance       map[string]domain.Provenance        `json:"provenance"`
	ProviderBindings map[string]domain.ProviderBinding   `json:"provider_bindings"`
	ImpactReports    map[string][]domain.ImpactReport    `json:"impact_reports"`
	Idempotency      map[string]json.RawMessage          `json:"idempotency"`
	Repositories     map[string]domain.Repository        `json:"repositories"`
	TeamWorkspaces   map[string]domain.TeamWorkspace     `json:"team_workspaces"`
	Profiles         map[string]domain.DeveloperProfile  `json:"profiles"`
	MCPServers       map[string]domain.MCPServer         `json:"mcp_servers"`
	Skills           map[string]domain.Skill             `json:"skills"`
	Automations      map[string]domain.Automation        `json:"automations"`
	Approvals        map[string]domain.Approval          `json:"approvals"`
	Diffs            map[string]domain.DiffSnapshot      `json:"diffs"`
	Migrations       map[string]domain.ArtifactMigration `json:"migrations"`
	Invocations      map[string]domain.MCPInvocation     `json:"invocations"`
	Bindings         map[string]domain.CapabilityBinding `json:"bindings"`
	AutomationRuns   map[string]domain.AutomationRun     `json:"automation_runs"`
	Contexts         map[string][]domain.ContextManifest `json:"contexts"`
	RepairAttempts   map[string][]domain.RepairAttempt   `json:"repair_attempts"`
	Pipelines        map[string]domain.PipelineRun       `json:"pipelines"`
}

// Flush makes the latest in-memory state durable. It is safe to call after
// every HTTP request and is a no-op for the default ephemeral profile.
func (m *Memory) Flush() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.persistLocked()
}

func (m *Memory) load() error {
	data, err := os.ReadFile(m.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read control-plane state: %w", err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode control-plane state: %w", err)
	}
	for name, value := range map[string]any{
		"requirements": state.Requirements, "bugs": state.Bugs, "attachments": state.Attachments,
		"work_items": state.WorkItems, "evidence": state.Evidence, "provenance": state.Provenance,
		"provider_bindings": state.ProviderBindings, "repositories": state.Repositories,
		"team_workspaces": state.TeamWorkspaces, "profiles": state.Profiles, "mcp_servers": state.MCPServers,
		"skills": state.Skills, "automations": state.Automations, "approvals": state.Approvals,
		"diffs": state.Diffs, "migrations": state.Migrations, "invocations": state.Invocations,
		"bindings": state.Bindings, "automation_runs": state.AutomationRuns,
	} {
		if value == nil {
			continue
		}
		switch name {
		case "requirements":
			m.requirements = value.(map[string]domain.Requirement)
		case "bugs":
			m.bugs = value.(map[string]domain.Bug)
		case "attachments":
			m.attachments = value.(map[string]domain.EntityAttachment)
		case "work_items":
			m.workItems = value.(map[string]domain.WorkItem)
		case "evidence":
			m.evidence = value.(map[string]domain.EvidenceBundle)
		case "provenance":
			m.provenance = value.(map[string]domain.Provenance)
		case "provider_bindings":
			m.providerBindings = value.(map[string]domain.ProviderBinding)
		case "repositories":
			m.repositories = value.(map[string]domain.Repository)
		case "team_workspaces":
			m.teamWorkspaces = value.(map[string]domain.TeamWorkspace)
		case "profiles":
			m.profiles = value.(map[string]domain.DeveloperProfile)
		case "mcp_servers":
			m.mcpServers = value.(map[string]domain.MCPServer)
		case "skills":
			m.skills = value.(map[string]domain.Skill)
		case "automations":
			m.automations = value.(map[string]domain.Automation)
		case "approvals":
			m.approvals = value.(map[string]domain.Approval)
		case "diffs":
			m.diffs = value.(map[string]domain.DiffSnapshot)
		case "migrations":
			m.migrations = value.(map[string]domain.ArtifactMigration)
		case "invocations":
			m.invocations = value.(map[string]domain.MCPInvocation)
		case "bindings":
			m.bindings = value.(map[string]domain.CapabilityBinding)
		case "automation_runs":
			m.automationRuns = value.(map[string]domain.AutomationRun)
		}
	}
	if state.ImpactReports != nil {
		m.impactReports = state.ImpactReports
	}
	if state.Contexts != nil {
		m.contexts = state.Contexts
	}
	if state.RepairAttempts != nil {
		m.repairAttempts = state.RepairAttempts
	}
	if state.Pipelines != nil {
		m.pipelines = state.Pipelines
	}
	for key, raw := range state.Idempotency {
		m.idempotency[key] = raw
	}
	return nil
}

func (m *Memory) persistLocked() error {
	if strings.TrimSpace(m.statePath) == "" {
		return nil
	}
	state := persistedState{Version: 2, Requirements: m.requirements, Bugs: m.bugs, Attachments: m.attachments, WorkItems: m.workItems, Evidence: m.evidence, Provenance: m.provenance, ProviderBindings: m.providerBindings, ImpactReports: m.impactReports, Idempotency: map[string]json.RawMessage{}, Repositories: m.repositories, TeamWorkspaces: m.teamWorkspaces, Profiles: m.profiles, MCPServers: m.mcpServers, Skills: m.skills, Automations: m.automations, Approvals: m.approvals, Diffs: m.diffs, Migrations: m.migrations, Invocations: m.invocations, Bindings: m.bindings, AutomationRuns: m.automationRuns, Contexts: m.contexts, RepairAttempts: m.repairAttempts, Pipelines: m.pipelines}
	for key, value := range m.idempotency {
		if raw, ok := value.(json.RawMessage); ok {
			state.Idempotency[key] = raw
			continue
		}
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		state.Idempotency[key] = data
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.statePath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".adro-state-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, m.statePath)
}

func (m *Memory) Idempotent(key string, value any) (any, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.idempotency[key]
	if raw, isRaw := v.(json.RawMessage); isRaw && value != nil {
		result := reflect.New(reflect.TypeOf(value))
		if err := json.Unmarshal(raw, result.Interface()); err == nil {
			return result.Elem().Interface(), true
		}
	}
	return v, ok
}
func (m *Memory) RememberIdempotency(key string, value any) {
	if key == "" {
		return
	}
	m.mu.Lock()
	m.idempotency[key] = value
	_ = m.persistLocked()
	m.mu.Unlock()
}

func (m *Memory) CreateRequirement(r domain.Requirement) (domain.Requirement, error) {
	if err := r.Validate(); err != nil {
		return domain.Requirement{}, err
	}
	now := time.Now().UTC()
	if r.ID == "" {
		r.ID = domain.NewID()
	}
	if r.Key == "" {
		r.Key = "REQ-" + strings.ToUpper(r.ID[:8])
	}
	if r.Priority == "" {
		r.Priority = "normal"
	}
	r.Status = domain.RequirementReceived
	r.Version = 1
	r.CreatedAt = now
	r.UpdatedAt = now
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.requirements {
		if existing.WorkspaceID == r.WorkspaceID && existing.Key == r.Key {
			return domain.Requirement{}, fmt.Errorf("requirement key %q already exists", r.Key)
		}
	}
	m.requirements[r.ID] = r
	_ = m.persistLocked()
	return r, nil
}
func (m *Memory) GetRequirement(id string) (domain.Requirement, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.requirements[id]
	if !ok {
		return domain.Requirement{}, ErrNotFound
	}
	return r, nil
}
func (m *Memory) ListRequirements(workspaceID, status, cursor string, limit int) ([]domain.Requirement, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 || limit > 250 {
		limit = 50
	}
	all := make([]domain.Requirement, 0, len(m.requirements))
	for _, r := range m.requirements {
		if workspaceID != "" && r.WorkspaceID != workspaceID {
			continue
		}
		if status != "" && string(r.Status) != status {
			continue
		}
		all = append(all, r)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].ID < all[j].ID
		}
		return all[i].CreatedAt.Before(all[j].CreatedAt)
	})
	start := 0
	for i, r := range all {
		if r.ID == cursor {
			start = i + 1
			break
		}
	}
	end := start + limit
	if end > len(all) {
		end = len(all)
	}
	next := ""
	if end < len(all) {
		next = all[end-1].ID
	}
	return all[start:end], next
}
func (m *Memory) UpdateRequirement(r domain.Requirement, expectedVersion int64) (domain.Requirement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.requirements[r.ID]
	if !ok {
		return domain.Requirement{}, ErrNotFound
	}
	if expectedVersion > 0 && current.Version != expectedVersion {
		return domain.Requirement{}, ErrConflict
	}
	if err := domain.Transition(current.Status, r.Status); err != nil {
		return domain.Requirement{}, err
	}
	r.Version = current.Version + 1
	r.CreatedAt = current.CreatedAt
	r.UpdatedAt = time.Now().UTC()
	m.requirements[r.ID] = r
	_ = m.persistLocked()
	return r, nil
}
func (m *Memory) TransitionRequirement(id string, to domain.RequirementStatus, expectedVersion int64) (domain.Requirement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.requirements[id]
	if !ok {
		return domain.Requirement{}, ErrNotFound
	}
	if expectedVersion > 0 && r.Version != expectedVersion {
		return domain.Requirement{}, ErrConflict
	}
	if err := domain.Transition(r.Status, to); err != nil {
		return domain.Requirement{}, err
	}
	r.Status = to
	r.Version++
	r.UpdatedAt = time.Now().UTC()
	m.requirements[id] = r
	_ = m.persistLocked()
	return r, nil
}

func (m *Memory) CreateWorkItem(w domain.WorkItem) (domain.WorkItem, error) {
	item, _, err := m.CreateWorkItemIfAbsent(w)
	return item, err
}

// CreateWorkItemIfAbsent gives materialization an atomic created bit so a
// concurrent start cannot create the same provider issue twice.
func (m *Memory) CreateWorkItemIfAbsent(w domain.WorkItem) (domain.WorkItem, bool, error) {
	if w.RequirementID == "" && w.BugID == "" {
		return domain.WorkItem{}, false, errors.New("requirement_id or bug_id is required")
	}
	if w.RepositoryID == "" || w.MemberID == "" {
		return domain.WorkItem{}, false, errors.New("repository_id and member_id are required")
	}
	if w.ID == "" {
		w.ID = domain.NewID()
	}
	if w.Status == "" {
		w.Status = "todo"
	}
	now := time.Now().UTC()
	w.CreatedAt = now
	w.UpdatedAt = now
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.workItems {
		if w.RequirementID != "" && existing.RequirementID == w.RequirementID && existing.RepositoryID == w.RepositoryID {
			return existing, false, nil
		}
	}
	m.workItems[w.ID] = w
	_ = m.persistLocked()
	return w, true, nil
}
func (m *Memory) ListWorkItems(requirementID string) []domain.WorkItem {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := []domain.WorkItem{}
	for _, w := range m.workItems {
		if requirementID == "" || w.RequirementID == requirementID {
			result = append(result, w)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result
}
func (m *Memory) GetWorkItem(id string) (domain.WorkItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	w, ok := m.workItems[id]
	if !ok {
		return domain.WorkItem{}, ErrNotFound
	}
	return w, nil
}

func (m *Memory) UpdateWorkItem(w domain.WorkItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.workItems[w.ID]
	if !ok {
		return ErrNotFound
	}
	w.CreatedAt = current.CreatedAt
	w.UpdatedAt = time.Now().UTC()
	m.workItems[w.ID] = w
	_ = m.persistLocked()
	return nil
}

func (m *Memory) UpsertBug(b domain.Bug) (domain.Bug, bool, error) {
	if strings.TrimSpace(b.Title) == "" {
		return domain.Bug{}, false, errors.New("title is required")
	}
	if b.Fingerprint == "" {
		return domain.Bug{}, false, errors.New("fingerprint is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.bugs {
		if existing.WorkspaceID == b.WorkspaceID && existing.RequirementID == b.RequirementID && existing.WorkItemID == b.WorkItemID && existing.Fingerprint == b.Fingerprint && existing.Status != domain.BugCancelled {
			return existing, true, nil
		}
	}
	if b.ID == "" {
		b.ID = domain.NewID()
	}
	if b.Status == "" {
		b.Status = domain.BugOpen
	}
	now := time.Now().UTC()
	b.CreatedAt = now
	b.UpdatedAt = now
	m.bugs[b.ID] = b
	_ = m.persistLocked()
	return b, false, nil
}
func (m *Memory) GetBug(id string) (domain.Bug, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.bugs[id]
	if !ok {
		return domain.Bug{}, ErrNotFound
	}
	return b, nil
}
func (m *Memory) ListBugs(workspaceID, status string) []domain.Bug {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := []domain.Bug{}
	for _, b := range m.bugs {
		if workspaceID != "" && b.WorkspaceID != workspaceID {
			continue
		}
		if status != "" && string(b.Status) != status {
			continue
		}
		result = append(result, b)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result
}
func (m *Memory) UpdateBug(b domain.Bug) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bugs[b.ID]; !ok {
		return ErrNotFound
	}
	b.UpdatedAt = time.Now().UTC()
	m.bugs[b.ID] = b
	_ = m.persistLocked()
	return nil
}

func (m *Memory) SaveAttachment(item domain.EntityAttachment) (domain.EntityAttachment, error) {
	if strings.TrimSpace(item.OwnerID) == "" || (item.OwnerType != "requirement" && item.OwnerType != "bug") {
		return domain.EntityAttachment{}, errors.New("attachment owner_type and owner_id are required")
	}
	if strings.TrimSpace(item.ArtifactURI) == "" || strings.TrimSpace(item.Filename) == "" {
		return domain.EntityAttachment{}, errors.New("attachment filename and artifact_uri are required")
	}
	if item.ID == "" {
		item.ID = domain.NewID()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	m.mu.Lock()
	m.attachments[item.ID] = item
	_ = m.persistLocked()
	m.mu.Unlock()
	return item, nil
}

func (m *Memory) ListAttachments(workspaceID, ownerType, ownerID string) []domain.EntityAttachment {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]domain.EntityAttachment, 0)
	for _, item := range m.attachments {
		if workspaceID != "" && item.WorkspaceID != workspaceID {
			continue
		}
		if ownerType != "" && item.OwnerType != ownerType {
			continue
		}
		if ownerID != "" && item.OwnerID != ownerID {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items
}

func (m *Memory) SaveEvidence(e domain.EvidenceBundle) error {
	if e.ID == "" {
		e.ID = domain.NewID()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	m.mu.Lock()
	m.evidence[e.ID] = e
	_ = m.persistLocked()
	m.mu.Unlock()
	return nil
}
func (m *Memory) ListEvidence(workItemID string) []domain.EvidenceBundle {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r := []domain.EvidenceBundle{}
	for _, e := range m.evidence {
		if workItemID == "" || e.WorkItemID == workItemID {
			r = append(r, e)
		}
	}
	return r
}
func (m *Memory) SaveProvenance(p domain.Provenance) error {
	if p.ID == "" {
		p.ID = domain.NewID()
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	m.mu.Lock()
	m.provenance[p.ID] = p
	_ = m.persistLocked()
	m.mu.Unlock()
	return nil
}
func (m *Memory) FindProvenance(workItemID string) (domain.Provenance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var found domain.Provenance
	ok := false
	for _, p := range m.provenance {
		if p.WorkItemID == workItemID && (!ok || p.CreatedAt.After(found.CreatedAt)) {
			found = p
			ok = true
		}
	}
	return found, ok
}

func (m *Memory) SaveContextManifest(manifest domain.ContextManifest) (domain.ContextManifest, error) {
	if strings.TrimSpace(manifest.ContextID) == "" || len(manifest.Repositories) == 0 {
		return domain.ContextManifest{}, errors.New("context_id and at least one repository are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	items := m.contexts[manifest.ContextID]
	if manifest.Version == 0 {
		manifest.Version = int64(len(items) + 1)
	}
	if manifest.StableSummary == "" {
		return domain.ContextManifest{}, errors.New("stable_summary is required")
	}
	items = append(items, manifest)
	m.contexts[manifest.ContextID] = items
	_ = m.persistLocked()
	return manifest, nil
}

func (m *Memory) GetContextManifest(contextID string, version int64) (domain.ContextManifest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := m.contexts[contextID]
	if len(items) == 0 {
		return domain.ContextManifest{}, ErrNotFound
	}
	if version <= 0 {
		return items[len(items)-1], nil
	}
	for _, item := range items {
		if item.Version == version {
			return item, nil
		}
	}
	return domain.ContextManifest{}, ErrNotFound
}

func (m *Memory) SaveRepairAttempt(attempt domain.RepairAttempt) (domain.RepairAttempt, error) {
	if strings.TrimSpace(attempt.BugID) == "" || strings.TrimSpace(attempt.WorkItemID) == "" || attempt.Attempt < 1 {
		return domain.RepairAttempt{}, errors.New("bug_id, work_item_id and positive attempt are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if attempt.ID == "" {
		attempt.ID = domain.NewID()
	}
	if attempt.Status == "" {
		attempt.Status = "started"
	}
	if attempt.CreatedAt.IsZero() {
		attempt.CreatedAt = time.Now().UTC()
	}
	m.repairAttempts[attempt.BugID] = append(m.repairAttempts[attempt.BugID], attempt)
	_ = m.persistLocked()
	return attempt, nil
}

func (m *Memory) ListRepairAttempts(bugID string) []domain.RepairAttempt {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]domain.RepairAttempt(nil), m.repairAttempts[bugID]...)
}

// ListRepairAttemptsForWorkItem returns append-only repair records associated
// with a work item, regardless of which Bug created each record.
func (m *Memory) ListRepairAttemptsForWorkItem(workItemID string) []domain.RepairAttempt {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]domain.RepairAttempt, 0)
	for _, attempts := range m.repairAttempts {
		for _, attempt := range attempts {
			if attempt.WorkItemID == workItemID {
				items = append(items, attempt)
			}
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items
}

func (m *Memory) SaveProviderBinding(binding domain.ProviderBinding) (domain.ProviderBinding, error) {
	if binding.ID == "" || binding.WorkspaceID == "" || binding.Provider == "" || binding.Kind == "" {
		return domain.ProviderBinding{}, errors.New("provider binding id, workspace_id, provider and kind are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.providerBindings[binding.ID]; ok {
		return existing, nil
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = time.Now().UTC()
	}
	m.providerBindings[binding.ID] = binding
	_ = m.persistLocked()
	return binding, nil
}

func (m *Memory) GetProviderBinding(id string) (domain.ProviderBinding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	binding, ok := m.providerBindings[id]
	if !ok {
		return domain.ProviderBinding{}, ErrNotFound
	}
	return binding, nil
}

func (m *Memory) CreateImpactReport(report domain.ImpactReport) (domain.ImpactReport, error) {
	if report.RequirementID == "" {
		return domain.ImpactReport{}, errors.New("requirement_id is required")
	}
	for i := range report.Candidates {
		candidate := &report.Candidates[i]
		if candidate.RepositoryID == "" || !candidate.RecommendedAction.Valid() {
			return domain.ImpactReport{}, errors.New("each impact candidate needs repository_id and a valid recommended_action")
		}
		if candidate.Confidence < 0 || candidate.Confidence > 1 {
			return domain.ImpactReport{}, errors.New("candidate confidence must be between 0 and 1")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	reports := m.impactReports[report.RequirementID]
	report.Version = int64(len(reports) + 1)
	report.ID = domain.NewID()
	report.CreatedAt = time.Now().UTC()
	if report.Status == "" {
		report.Status = "generated"
	}
	report.Candidates = append([]domain.ImpactCandidate(nil), report.Candidates...)
	m.impactReports[report.RequirementID] = append(reports, report)
	_ = m.persistLocked()
	return report, nil
}

func (m *Memory) GetImpactReport(requirementID string, version int64) (domain.ImpactReport, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, report := range m.impactReports[requirementID] {
		if report.Version == version {
			return report, nil
		}
	}
	return domain.ImpactReport{}, ErrNotFound
}

func (m *Memory) ConfirmImpactReport(requirementID string, version int64, repositories []string) (domain.ImpactReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	reports := m.impactReports[requirementID]
	for i := range reports {
		if reports[i].Version != version {
			continue
		}
		allowed := make(map[string]bool, len(reports[i].Candidates))
		for _, candidate := range reports[i].Candidates {
			allowed[candidate.RepositoryID] = true
		}
		for _, repositoryID := range repositories {
			if !allowed[repositoryID] {
				return domain.ImpactReport{}, fmt.Errorf("repository %q is not a candidate in impact report", repositoryID)
			}
		}
		reports[i].ConfirmedRepositories = append([]string(nil), repositories...)
		reports[i].Status = "confirmed"
		m.impactReports[requirementID] = reports
		_ = m.persistLocked()
		return reports[i], nil
	}
	return domain.ImpactReport{}, ErrNotFound
}

func (m *Memory) UpsertRepository(repository domain.Repository) (domain.Repository, error) {
	if strings.TrimSpace(repository.WorkspaceID) == "" || strings.TrimSpace(repository.CanonicalName) == "" || strings.TrimSpace(repository.CloneURL) == "" {
		return domain.Repository{}, errors.New("workspace_id, canonical_name and clone_url are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if repository.ID == "" {
		repository.ID = domain.NewID()
	}
	if repository.DefaultBranch == "" {
		repository.DefaultBranch = "main"
	}
	if repository.Provider == "" {
		repository.Provider = "git"
	}
	if repository.IndexStatus == "" {
		repository.IndexStatus = "pending"
	}
	if repository.Metadata == nil {
		repository.Metadata = map[string]any{}
	}
	now := time.Now().UTC()
	if existing, ok := m.repositories[repository.ID]; ok {
		repository.CreatedAt = existing.CreatedAt
	} else {
		repository.CreatedAt = now
	}
	repository.UpdatedAt = now
	for id, existing := range m.repositories {
		if id != repository.ID && existing.WorkspaceID == repository.WorkspaceID && existing.CanonicalName == repository.CanonicalName {
			return domain.Repository{}, fmt.Errorf("repository %q already exists", repository.CanonicalName)
		}
	}
	m.repositories[repository.ID] = repository
	_ = m.persistLocked()
	return repository, nil
}

func (m *Memory) GetRepository(id string) (domain.Repository, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	repository, ok := m.repositories[id]
	if !ok {
		return domain.Repository{}, ErrNotFound
	}
	return repository, nil
}

func (m *Memory) ListRepositories(workspaceID string) []domain.Repository {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]domain.Repository, 0, len(m.repositories))
	for _, repository := range m.repositories {
		if workspaceID == "" || repository.WorkspaceID == workspaceID {
			items = append(items, repository)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CanonicalName < items[j].CanonicalName })
	return items
}

func (m *Memory) MarkRepositoryIndexed(id, commit string) (domain.Repository, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	repository, ok := m.repositories[id]
	if !ok {
		return domain.Repository{}, ErrNotFound
	}
	repository.IndexedCommit = commit
	repository.IndexStatus = "ready"
	repository.UpdatedAt = time.Now().UTC()
	m.repositories[id] = repository
	_ = m.persistLocked()
	return repository, nil
}

func (m *Memory) DeleteRepository(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.repositories[id]; !ok {
		return ErrNotFound
	}
	delete(m.repositories, id)
	_ = m.persistLocked()
	return nil
}

func (m *Memory) UpsertTeamWorkspace(workspace domain.TeamWorkspace) (domain.TeamWorkspace, error) {
	if strings.TrimSpace(workspace.WorkspaceID) == "" || strings.TrimSpace(workspace.Name) == "" {
		return domain.TeamWorkspace{}, errors.New("workspace_id and name are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if workspace.ID == "" {
		workspace.ID = domain.NewID()
	}
	if workspace.Version == 0 {
		workspace.Version = 1
	}
	if workspace.Policy == nil {
		workspace.Policy = map[string]any{}
	}
	if workspace.Status == "" {
		workspace.Status = "active"
	}
	now := time.Now().UTC()
	if existing, ok := m.teamWorkspaces[workspace.ID]; ok {
		workspace.CreatedAt = existing.CreatedAt
		workspace.Version = existing.Version + 1
	} else {
		workspace.CreatedAt = now
	}
	workspace.UpdatedAt = now
	m.teamWorkspaces[workspace.ID] = workspace
	_ = m.persistLocked()
	return workspace, nil
}

func (m *Memory) GetTeamWorkspace(id string) (domain.TeamWorkspace, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	workspace, ok := m.teamWorkspaces[id]
	if !ok {
		return domain.TeamWorkspace{}, ErrNotFound
	}
	return workspace, nil
}
func (m *Memory) ListTeamWorkspaces(workspaceID string) []domain.TeamWorkspace {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := []domain.TeamWorkspace{}
	for _, item := range m.teamWorkspaces {
		if workspaceID == "" || item.WorkspaceID == workspaceID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (m *Memory) UpsertDeveloperProfile(profile domain.DeveloperProfile) (domain.DeveloperProfile, error) {
	if profile.WorkspaceID == "" || profile.MemberID == "" {
		return domain.DeveloperProfile{}, errors.New("workspace_id and member_id are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if profile.ID == "" {
		for _, existing := range m.profiles {
			if existing.WorkspaceID == profile.WorkspaceID && existing.MemberID == profile.MemberID {
				profile.ID = existing.ID
				break
			}
		}
	}
	if profile.ID == "" {
		profile.ID = domain.NewID()
	}
	if profile.Status == "" {
		profile.Status = "active"
	}
	if profile.GitIdentity == nil {
		profile.GitIdentity = map[string]any{}
	}
	now := time.Now().UTC()
	if existing, ok := m.profiles[profile.ID]; ok {
		profile.CreatedAt = existing.CreatedAt
	} else {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now
	m.profiles[profile.ID] = profile
	_ = m.persistLocked()
	return profile, nil
}
func (m *Memory) GetDeveloperProfile(workspaceID, memberID string) (domain.DeveloperProfile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, profile := range m.profiles {
		if profile.WorkspaceID == workspaceID && profile.MemberID == memberID {
			return profile, nil
		}
	}
	return domain.DeveloperProfile{}, ErrNotFound
}

func (m *Memory) ListDeveloperProfiles(workspaceID string) []domain.DeveloperProfile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]domain.DeveloperProfile, 0, len(m.profiles))
	for _, profile := range m.profiles {
		if workspaceID == "" || profile.WorkspaceID == workspaceID {
			items = append(items, profile)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].MemberID == items[j].MemberID {
			return items[i].ID < items[j].ID
		}
		return items[i].MemberID < items[j].MemberID
	})
	return items
}

func (m *Memory) UpsertMCPServer(server domain.MCPServer) (domain.MCPServer, error) {
	return upsertMCP(m, server)
}
func upsertMCP(m *Memory, server domain.MCPServer) (domain.MCPServer, error) {
	if server.WorkspaceID == "" || server.Name == "" || server.Endpoint == "" {
		return domain.MCPServer{}, errors.New("workspace_id, name and endpoint are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if server.ID == "" {
		server.ID = domain.NewID()
	}
	if server.Protocol == "" {
		server.Protocol = "mcp.v1"
	}
	if server.Status == "" {
		server.Status = "configured"
	}
	if server.Configuration == nil {
		server.Configuration = map[string]any{}
	}
	now := time.Now().UTC()
	if existing, ok := m.mcpServers[server.ID]; ok {
		server.CreatedAt = existing.CreatedAt
	} else {
		server.CreatedAt = now
	}
	server.UpdatedAt = now
	m.mcpServers[server.ID] = server
	_ = m.persistLocked()
	return server, nil
}
func (m *Memory) ListMCPServers(workspaceID string) []domain.MCPServer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := []domain.MCPServer{}
	for _, server := range m.mcpServers {
		if workspaceID == "" || server.WorkspaceID == workspaceID {
			items = append(items, server)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}
func (m *Memory) DeleteMCPServer(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.mcpServers[id]; !ok {
		return ErrNotFound
	}
	delete(m.mcpServers, id)
	_ = m.persistLocked()
	return nil
}

func (m *Memory) UpsertSkill(skill domain.Skill) (domain.Skill, error) {
	if skill.WorkspaceID == "" || skill.Name == "" || skill.Version == "" {
		return domain.Skill{}, errors.New("workspace_id, name and version are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if skill.ID == "" {
		skill.ID = domain.NewID()
	}
	if skill.Status == "" {
		skill.Status = "installed"
	}
	if skill.Contract == nil {
		skill.Contract = map[string]any{}
	}
	now := time.Now().UTC()
	if existing, ok := m.skills[skill.ID]; ok {
		skill.CreatedAt = existing.CreatedAt
	} else {
		skill.CreatedAt = now
	}
	skill.UpdatedAt = now
	m.skills[skill.ID] = skill
	_ = m.persistLocked()
	return skill, nil
}
func (m *Memory) ListSkills(workspaceID string) []domain.Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := []domain.Skill{}
	for _, skill := range m.skills {
		if workspaceID == "" || skill.WorkspaceID == workspaceID {
			items = append(items, skill)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}
func (m *Memory) DeleteSkill(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.skills[id]; !ok {
		return ErrNotFound
	}
	delete(m.skills, id)
	_ = m.persistLocked()
	return nil
}

func (m *Memory) UpsertAutomation(automation domain.Automation) (domain.Automation, error) {
	if automation.WorkspaceID == "" || automation.Name == "" {
		return domain.Automation{}, errors.New("workspace_id and name are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if automation.ID == "" {
		automation.ID = domain.NewID()
	}
	if automation.Version == 0 {
		automation.Version = 1
	}
	now := time.Now().UTC()
	if existing, ok := m.automations[automation.ID]; ok {
		automation.CreatedAt = existing.CreatedAt
		automation.Version = existing.Version + 1
	} else {
		automation.CreatedAt = now
	}
	automation.UpdatedAt = now
	m.automations[automation.ID] = automation
	_ = m.persistLocked()
	return automation, nil
}
func (m *Memory) ListAutomations(workspaceID string) []domain.Automation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := []domain.Automation{}
	for _, automation := range m.automations {
		if workspaceID == "" || automation.WorkspaceID == workspaceID {
			items = append(items, automation)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}
func (m *Memory) DeleteAutomation(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.automations[id]; !ok {
		return ErrNotFound
	}
	delete(m.automations, id)
	_ = m.persistLocked()
	return nil
}

func (m *Memory) CreateApproval(approval domain.Approval) (domain.Approval, error) {
	if approval.WorkspaceID == "" || approval.RequirementID == "" || approval.Kind == "" {
		return domain.Approval{}, errors.New("workspace_id, requirement_id and kind are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if approval.ID == "" {
		approval.ID = domain.NewID()
	}
	if approval.Decision == "" {
		approval.Decision = "pending"
	}
	if approval.CreatedAt.IsZero() {
		approval.CreatedAt = time.Now().UTC()
	}
	m.approvals[approval.ID] = approval
	_ = m.persistLocked()
	return approval, nil
}
func (m *Memory) DecideApproval(id, decision, member, reason string) (domain.Approval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	approval, ok := m.approvals[id]
	if !ok {
		return domain.Approval{}, ErrNotFound
	}
	if approval.Decision != "pending" {
		return domain.Approval{}, ErrConflict
	}
	approval.Decision, approval.DecidedBy, approval.Reason = decision, member, reason
	now := time.Now().UTC()
	approval.DecidedAt = &now
	m.approvals[id] = approval
	_ = m.persistLocked()
	return approval, nil
}

func (m *Memory) GetApproval(id string) (domain.Approval, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	approval, ok := m.approvals[id]
	if !ok {
		return domain.Approval{}, ErrNotFound
	}
	return approval, nil
}

func (m *Memory) SaveDiff(diff domain.DiffSnapshot) (domain.DiffSnapshot, error) {
	if diff.WorkItemID == "" || diff.RepositoryID == "" {
		return domain.DiffSnapshot{}, errors.New("work_item_id and repository_id are required")
	}
	if diff.ID == "" {
		diff.ID = domain.NewID()
	}
	if diff.CreatedAt.IsZero() {
		diff.CreatedAt = time.Now().UTC()
	}
	if diff.Stat == nil {
		diff.Stat = map[string]any{}
	}
	diff.Files = append([]string(nil), diff.Files...)
	m.mu.Lock()
	m.diffs[diff.WorkItemID] = diff
	m.mu.Unlock()
	_ = m.Flush()
	return diff, nil
}

func (m *Memory) GetDiff(workItemID string) (domain.DiffSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	diff, ok := m.diffs[workItemID]
	if !ok {
		return domain.DiffSnapshot{}, ErrNotFound
	}
	return diff, nil
}

func (m *Memory) CreateArtifactMigration(migration domain.ArtifactMigration) (domain.ArtifactMigration, error) {
	if migration.WorkspaceID == "" || migration.ArtifactID == "" || migration.FromDriver == "" || migration.ToDriver == "" {
		return domain.ArtifactMigration{}, errors.New("workspace_id, artifact_id, from_driver and to_driver are required")
	}
	if migration.FromDriver == migration.ToDriver {
		return domain.ArtifactMigration{}, errors.New("from_driver and to_driver must differ")
	}
	if migration.ID == "" {
		migration.ID = domain.NewID()
	}
	if migration.Status == "" {
		migration.Status = "running"
	}
	now := time.Now().UTC()
	migration.CreatedAt, migration.UpdatedAt = now, now
	m.mu.Lock()
	m.migrations[migration.ID] = migration
	m.mu.Unlock()
	_ = m.Flush()
	return migration, nil
}

func (m *Memory) GetArtifactMigration(id string) (domain.ArtifactMigration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	migration, ok := m.migrations[id]
	if !ok {
		return domain.ArtifactMigration{}, ErrNotFound
	}
	return migration, nil
}

func (m *Memory) UpdateArtifactMigration(id, status string) (domain.ArtifactMigration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	migration, ok := m.migrations[id]
	if !ok {
		return domain.ArtifactMigration{}, ErrNotFound
	}
	if status != "running" && status != "paused" && status != "completed" && status != "rolled_back" && status != "failed" {
		return domain.ArtifactMigration{}, errors.New("invalid migration status")
	}
	migration.Status = status
	migration.UpdatedAt = time.Now().UTC()
	m.migrations[id] = migration
	_ = m.persistLocked()
	return migration, nil
}

func (m *Memory) SaveMCPInvocation(invocation domain.MCPInvocation) (domain.MCPInvocation, error) {
	if invocation.WorkspaceID == "" || invocation.ServerID == "" || invocation.Tool == "" {
		return domain.MCPInvocation{}, errors.New("workspace_id, server_id and tool are required")
	}
	if invocation.ID == "" {
		invocation.ID = domain.NewID()
	}
	if invocation.CreatedAt.IsZero() {
		invocation.CreatedAt = time.Now().UTC()
	}
	if invocation.Status == "" {
		invocation.Status = "completed"
	}
	m.mu.Lock()
	m.invocations[invocation.ID] = invocation
	m.mu.Unlock()
	_ = m.Flush()
	return invocation, nil
}

func (m *Memory) ListMCPInvocations(workspaceID string) []domain.MCPInvocation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]domain.MCPInvocation, 0, len(m.invocations))
	for _, invocation := range m.invocations {
		if workspaceID == "" || invocation.WorkspaceID == workspaceID {
			items = append(items, invocation)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items
}

func (m *Memory) SaveBinding(binding domain.CapabilityBinding) (domain.CapabilityBinding, error) {
	if binding.WorkspaceID == "" || binding.AgentID == "" || binding.CapabilityID == "" || binding.Kind == "" {
		return domain.CapabilityBinding{}, errors.New("workspace_id, agent_id, capability_id and kind are required")
	}
	if binding.ID == "" {
		binding.ID = domain.NewID()
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = time.Now().UTC()
	}
	m.mu.Lock()
	m.bindings[binding.ID] = binding
	m.mu.Unlock()
	_ = m.Flush()
	return binding, nil
}

func (m *Memory) ListBindings(workspaceID, agentID, kind string) []domain.CapabilityBinding {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]domain.CapabilityBinding, 0, len(m.bindings))
	for _, binding := range m.bindings {
		if workspaceID != "" && binding.WorkspaceID != workspaceID {
			continue
		}
		if agentID != "" && binding.AgentID != agentID {
			continue
		}
		if kind != "" && binding.Kind != kind {
			continue
		}
		items = append(items, binding)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items
}

func (m *Memory) DeleteBinding(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bindings[id]; !ok {
		return ErrNotFound
	}
	delete(m.bindings, id)
	_ = m.persistLocked()
	return nil
}

func (m *Memory) CreateAutomationRun(run domain.AutomationRun) (domain.AutomationRun, error) {
	if run.AutomationID == "" || run.WorkspaceID == "" {
		return domain.AutomationRun{}, errors.New("automation_id and workspace_id are required")
	}
	if run.ID == "" {
		run.ID = domain.NewID()
	}
	if run.Status == "" {
		run.Status = "running"
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	m.mu.Lock()
	m.automationRuns[run.ID] = run
	m.mu.Unlock()
	_ = m.Flush()
	return run, nil
}

func (m *Memory) GetAutomationRun(id string) (domain.AutomationRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok := m.automationRuns[id]
	if !ok {
		return domain.AutomationRun{}, ErrNotFound
	}
	return run, nil
}

func (m *Memory) ListAutomationRuns(automationID string) []domain.AutomationRun {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]domain.AutomationRun, 0, len(m.automationRuns))
	for _, run := range m.automationRuns {
		if automationID == "" || run.AutomationID == automationID {
			items = append(items, run)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].StartedAt.Before(items[j].StartedAt) })
	return items
}

func (m *Memory) UpdateAutomationRun(id, status, takenOverBy string) (domain.AutomationRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.automationRuns[id]
	if !ok {
		return domain.AutomationRun{}, ErrNotFound
	}
	if status != "running" && status != "completed" && status != "failed" && status != "cancelled" {
		return domain.AutomationRun{}, errors.New("invalid automation run status")
	}
	run.Status = status
	if takenOverBy != "" {
		run.TakenOverBy = takenOverBy
	}
	if status != "running" {
		now := time.Now().UTC()
		run.FinishedAt = &now
	}
	m.automationRuns[id] = run
	_ = m.persistLocked()
	return run, nil
}

func (m *Memory) CreatePipeline(run domain.PipelineRun) (domain.PipelineRun, error) {
	if run.ID == "" {
		run.ID = domain.NewID()
	}
	if run.SessionID == "" {
		run.SessionID = domain.NewID()
	}
	if run.PipelineStage == 0 {
		run.PipelineStage = domain.PipelineDesign
	}
	if run.Status == "" {
		run.Status = domain.PipelineRunning
	}
	if run.MaxRetries == 0 {
		run.MaxRetries = 3
	}
	if run.CoverageThreshold == 0 {
		run.CoverageThreshold = 80
	}
	if err := run.Validate(); err != nil {
		return domain.PipelineRun{}, err
	}
	now := time.Now().UTC()
	run.CreatedAt, run.UpdatedAt, run.Version = now, now, 1
	run.ActiveAgentID = run.Roles.AgentFor(run.PipelineStage)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.pipelines {
		if existing.RequirementID == run.RequirementID && existing.Status != domain.PipelineCompleted && existing.Status != domain.PipelineFailed {
			return domain.PipelineRun{}, fmt.Errorf("an active pipeline already exists for requirement %s", run.RequirementID)
		}
	}
	m.pipelines[run.ID] = run
	_ = m.persistLocked()
	return run, nil
}

func (m *Memory) GetPipeline(id string) (domain.PipelineRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok := m.pipelines[id]
	if !ok {
		return domain.PipelineRun{}, ErrNotFound
	}
	return run, nil
}

func (m *Memory) ListPipelines(workspaceID, requirementID string) []domain.PipelineRun {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]domain.PipelineRun, 0, len(m.pipelines))
	for _, run := range m.pipelines {
		if workspaceID != "" && run.WorkspaceID != workspaceID {
			continue
		}
		if requirementID != "" && run.RequirementID != requirementID {
			continue
		}
		items = append(items, run)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items
}

func (m *Memory) UpdatePipeline(run domain.PipelineRun, expectedVersion int64) (domain.PipelineRun, error) {
	if err := run.Validate(); err != nil {
		return domain.PipelineRun{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.pipelines[run.ID]
	if !ok {
		return domain.PipelineRun{}, ErrNotFound
	}
	if expectedVersion > 0 && current.Version != expectedVersion {
		return domain.PipelineRun{}, ErrConflict
	}
	if run.SessionID != current.SessionID || run.RequirementID != current.RequirementID || run.WorkspaceID != current.WorkspaceID {
		return domain.PipelineRun{}, errors.New("pipeline identity fields are immutable")
	}
	run.CreatedAt = current.CreatedAt
	m.pipelines[run.ID] = run
	_ = m.persistLocked()
	return run, nil
}
