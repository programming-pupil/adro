package api

// This file is the control-plane surface for the provider-neutral
// orchestration model.  It deliberately sits beside the legacy provider
// binding endpoints: old clients keep their response shape while new clients
// use immutable, revisioned Agent/Squad/Plan records.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/orchestration"
)

func (s *Server) orchestrationWorkspaceRoute(w http.ResponseWriter, r *http.Request, workspaceID, kind, tail string) {
	if s.Orchestration == nil {
		s.problem(w, r, http.StatusServiceUnavailable, "orchestration_unavailable", "orchestration repository is unavailable", nil)
		return
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" || (requestWorkspace(r, workspaceID) != workspaceID && requestWorkspace(r, "") != "") {
		s.problem(w, r, http.StatusNotFound, "not_found", "workspace resource not found", nil)
		return
	}
	if kind == "agents" {
		if tail != "" {
			s.orchestrationAgentResource(w, r, tail, workspaceID)
			return
		}
		if r.Method == http.MethodGet {
			s.writeJSON(w, http.StatusOK, map[string]any{"items": s.Orchestration.ListAgents(workspaceID, orchestration.AgentStatus(r.URL.Query().Get("status")))})
			return
		}
		if r.Method != http.MethodPost {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		var a orchestration.AgentDefinition
		if err := decodeJSON(r, &a); err != nil {
			s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
			return
		}
		a.WorkspaceID, a.ID = workspaceID, strings.TrimSpace(a.ID)
		if a.ID == "" {
			a.ID = domain.NewID()
		}
		if a.Revision == 0 {
			a.Revision = 1
		}
		if a.Status == "" {
			a.Status = orchestration.AgentDraft
		}
		now := time.Now().UTC()
		a.CreatedAt, a.UpdatedAt = now, now
		if err := s.Orchestration.SaveAgent(a, 0); err != nil {
			s.problem(w, r, http.StatusUnprocessableEntity, "agent_validation_failed", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusCreated, a)
		return
	}
	if tail != "" {
		s.orchestrationSquadResource(w, r, tail, workspaceID)
		return
	}
	if r.Method == http.MethodGet {
		s.writeJSON(w, http.StatusOK, map[string]any{"items": s.Orchestration.ListSquads(workspaceID, orchestration.SquadStatus(r.URL.Query().Get("status")))})
		return
	}
	if r.Method != http.MethodPost {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	var squad orchestration.SquadDefinition
	if err := decodeJSON(r, &squad); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	squad.WorkspaceID, squad.ID = workspaceID, strings.TrimSpace(squad.ID)
	if squad.ID == "" {
		squad.ID = domain.NewID()
	}
	if squad.Revision == 0 {
		squad.Revision = 1
	}
	if squad.Status == "" {
		squad.Status = orchestration.SquadDraft
	}
	if err := s.Orchestration.SaveSquad(squad, 0); err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "squad_validation_failed", err.Error(), nil)
		return
	}
	s.writeJSON(w, http.StatusCreated, squad)
}

func (s *Server) orchestrationAgentResource(w http.ResponseWriter, r *http.Request, id, workspaceID string) {
	base := strings.SplitN(id, "/", 2)[0]
	a, err := s.Orchestration.GetAgent(workspaceID, base, 0)
	if err != nil {
		s.problem(w, r, http.StatusNotFound, "agent_not_found", err.Error(), nil)
		return
	}
	if strings.HasSuffix(id, "/validate") {
		if r.Method != http.MethodPost {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"valid": a.Validate() == nil, "agent": a, "error": validationError(a.Validate())})
		return
	}
	if r.Method == http.MethodGet {
		s.writeJSON(w, http.StatusOK, a)
		return
	}
	if r.Method == http.MethodPatch {
		var patch map[string]json.RawMessage
		if err := decodeJSON(r, &patch); err != nil {
			s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
			return
		}
		var expected int64
		if raw := patch["expected_revision"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &expected)
		}
		if expected == 0 {
			s.problem(w, r, http.StatusBadRequest, "expected_revision_required", "expected_revision is required", nil)
			return
		}
		if raw := patch["name"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &a.Name)
		}
		if raw := patch["role"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &a.Role)
		}
		if raw := patch["instructions"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &a.Instructions)
		}
		if raw := patch["status"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &a.Status)
		}
		a.Revision++
		a.UpdatedAt = time.Now().UTC()
		if err := s.Orchestration.SaveAgent(a, expected); err != nil {
			s.problem(w, r, http.StatusConflict, "agent_update_conflict", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, a)
		return
	}
	if r.Method == http.MethodPost {
		if strings.HasSuffix(id, "/disable") || strings.HasSuffix(id, "/enable") || strings.HasSuffix(id, "/archive") {
			if strings.HasSuffix(id, "/disable") {
				a.Status = orchestration.AgentDisabled
			}
			if strings.HasSuffix(id, "/enable") {
				a.Status = orchestration.AgentActive
			}
			if strings.HasSuffix(id, "/archive") {
				a.Status = orchestration.AgentArchived
			}
			a.Revision++
			a.UpdatedAt = time.Now().UTC()
			if err := s.Orchestration.SaveAgent(a, a.Revision-1); err != nil {
				s.problem(w, r, http.StatusConflict, "agent_update_conflict", err.Error(), nil)
				return
			}
			s.writeJSON(w, http.StatusOK, a)
			return
		}
	}
	s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
}

func (s *Server) orchestrationSquadResource(w http.ResponseWriter, r *http.Request, id, workspaceID string) {
	base := strings.SplitN(id, "/", 2)[0]
	sq, err := s.Orchestration.GetSquad(workspaceID, base, 0)
	if err != nil {
		s.problem(w, r, http.StatusNotFound, "squad_not_found", err.Error(), nil)
		return
	}
	if strings.Contains(id, "/validate") || strings.Contains(id, "/dry-run") {
		if r.Method != http.MethodPost {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		err := sq.Validate()
		resp := map[string]any{"valid": err == nil, "squad": sq}
		if err != nil {
			resp["error"] = err.Error()
		}
		if strings.Contains(id, "/dry-run") {
			resp["mode"] = "dry-run"
			nodes := map[string]orchestration.NodeProjection{}
			for _, n := range sq.Graph.Nodes {
				nodes[n.ID] = orchestration.NodeProjection{NodeID: n.ID, Status: orchestration.AttemptPending}
			}
			for _, id := range sq.Graph.EntryNodeIDs {
				if n, ok := nodes[id]; ok {
					n.Status = orchestration.AttemptReady
					nodes[id] = n
				}
			}
			resp["ready_nodes"] = orchestration.ReadyNodes(orchestration.RequirementExecutionPlan{GraphSnapshot: sq.Graph}, orchestration.PlanProjection{Nodes: nodes})
		}
		s.writeJSON(w, http.StatusOK, resp)
		return
	}
	if r.Method == http.MethodGet {
		s.writeJSON(w, http.StatusOK, sq)
		return
	}
	if r.Method == http.MethodPost && (strings.HasSuffix(id, "/publish") || strings.HasSuffix(id, "/disable") || strings.HasSuffix(id, "/archive")) {
		if strings.HasSuffix(id, "/publish") {
			sq.Status = orchestration.SquadPublished
			sq.PublishedVersion++
		}
		if strings.HasSuffix(id, "/disable") {
			sq.Status = orchestration.SquadDisabled
		}
		if strings.HasSuffix(id, "/archive") {
			sq.Status = orchestration.SquadArchived
		}
		sq.Revision++
		if err := s.Orchestration.SaveSquad(sq, sq.Revision-1); err != nil {
			s.problem(w, r, http.StatusConflict, "squad_update_conflict", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, sq)
		return
	}
	if r.Method == http.MethodPatch {
		var patch struct {
			ExpectedRevision int64                        `json:"expected_revision"`
			Name             *string                      `json:"name"`
			Description      *string                      `json:"description"`
			Graph            *orchestration.WorkflowGraph `json:"graph"`
			Policy           *orchestration.SquadPolicy   `json:"policy"`
		}
		if err := decodeJSON(r, &patch); err != nil {
			s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
			return
		}
		if patch.ExpectedRevision == 0 {
			s.problem(w, r, http.StatusBadRequest, "expected_revision_required", "expected_revision is required", nil)
			return
		}
		if patch.Name != nil {
			sq.Name = *patch.Name
		}
		if patch.Description != nil {
			sq.Description = *patch.Description
		}
		if patch.Graph != nil {
			sq.Graph = *patch.Graph
		}
		if patch.Policy != nil {
			sq.Policy = *patch.Policy
		}
		sq.Revision++
		if err := s.Orchestration.SaveSquad(sq, patch.ExpectedRevision); err != nil {
			s.problem(w, r, http.StatusConflict, "squad_update_conflict", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, sq)
		return
	}
	s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
}

func validationError(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}

func (s *Server) executionPlanRequirementRoute(w http.ResponseWriter, r *http.Request, requirementID, tail string) {
	if s.Orchestration == nil {
		s.problem(w, r, http.StatusServiceUnavailable, "orchestration_unavailable", "orchestration repository is unavailable", nil)
		return
	}
	req, err := s.Store.GetRequirement(requirementID)
	if err != nil {
		s.problem(w, r, http.StatusNotFound, "requirement_not_found", err.Error(), nil)
		return
	}
	if ws := requestWorkspace(r, ""); ws != "" && ws != req.WorkspaceID {
		s.problem(w, r, http.StatusNotFound, "requirement_not_found", "requirement not found", nil)
		return
	}
	if tail == "" && r.Method == http.MethodPost {
		s.createExecutionPlan(w, r, req)
		return
	}
	if tail == "quick-squad" && r.Method == http.MethodPost {
		s.quickSquadPlan(w, r, req)
		return
	}
	if tail == "publish" && r.Method == http.MethodPost {
		s.publishExecutionPlan(w, r, req)
		return
	}
	s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
}

func (s *Server) createExecutionPlan(w http.ResponseWriter, r *http.Request, req domain.Requirement) {
	var in struct {
		AgentID        string                      `json:"agent_id"`
		AgentRevision  int64                       `json:"agent_revision"`
		SquadID        string                      `json:"squad_id"`
		SquadVersion   int64                       `json:"squad_version"`
		Graph          orchestration.WorkflowGraph `json:"graph"`
		GraphOverrides map[string]any              `json:"graph_overrides,omitempty"`
		IdempotencyKey string                      `json:"idempotency_key"`
	}
	if err := decodeJSON(r, &in); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	graph := in.Graph
	selected := orchestration.VersionedRef{}
	if in.SquadID != "" {
		sq, err := s.Orchestration.GetSquad(req.WorkspaceID, in.SquadID, in.SquadVersion)
		if err != nil || sq.Status != orchestration.SquadPublished {
			s.problem(w, r, http.StatusUnprocessableEntity, "squad_unavailable", "published squad revision is required", nil)
			return
		}
		graph, selected = sq.Graph, orchestration.VersionedRef{ID: sq.ID, Revision: sq.Revision, Version: sq.PublishedVersion}
	}
	if in.AgentID != "" {
		a, err := s.Orchestration.GetAgent(req.WorkspaceID, in.AgentID, in.AgentRevision)
		if err != nil || a.Status != orchestration.AgentActive {
			s.problem(w, r, http.StatusUnprocessableEntity, "agent_unavailable", "active agent revision is required", nil)
			return
		}
		selected = orchestration.VersionedRef{ID: a.ID, Revision: a.Revision}
		if len(graph.Nodes) == 0 {
			graph = singleAgentGraph(a)
		}
	}
	if len(graph.Nodes) == 0 {
		s.problem(w, r, http.StatusUnprocessableEntity, "graph_required", "graph or agent_id/squad_id is required", nil)
		return
	}
	now := time.Now().UTC()
	p := orchestration.RequirementExecutionPlan{ID: domain.NewID(), RequirementID: req.ID, WorkspaceID: req.WorkspaceID, GraphSnapshot: graph, SelectedRef: selected, PolicySnapshot: orchestration.PolicySnapshot{CapturedAt: now}, ContextRoot: orchestration.ContextRef{SessionID: "plan-" + req.ID, ManifestDigest: "pending"}, Status: orchestration.PlanDraft, IdempotencyKey: in.IdempotencyKey, CreatedAt: now}
	frozen, err := p.Freeze()
	if err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "plan_validation_failed", err.Error(), nil)
		return
	}
	if err := s.Orchestration.CreatePlan(frozen); err != nil {
		status := http.StatusConflict
		if errors.Is(err, orchestration.ErrIdempotencyConflict) {
			status = http.StatusConflict
		}
		s.problem(w, r, status, "plan_create_failed", err.Error(), nil)
		return
	}
	if event, eventErr := orchestration.NewEvent(nil, frozen.ID, frozen.WorkspaceID, "plan.created", frozen.IdempotencyKey, frozen); eventErr == nil {
		if appendErr := s.Orchestration.AppendEvent(event); appendErr != nil {
			s.problem(w, r, http.StatusServiceUnavailable, "plan_event_commit_failed", appendErr.Error(), nil)
			return
		}
	}
	s.writeJSON(w, http.StatusCreated, frozen)
}

func singleAgentGraph(a orchestration.AgentDefinition) orchestration.WorkflowGraph {
	return orchestration.WorkflowGraph{ID: "graph-" + a.ID, Version: a.Revision, EntryNodeIDs: []string{"agent"}, ExitNodeIDs: []string{"agent"}, Nodes: []orchestration.WorkflowNode{{ID: "agent", Kind: orchestration.NodeAgent, AgentRef: &orchestration.VersionedRef{ID: a.ID, Revision: a.Revision}}}}
}

func (s *Server) quickSquadPlan(w http.ResponseWriter, r *http.Request, req domain.Requirement) {
	var in struct {
		SquadID string `json:"squad_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	sq, err := s.Orchestration.GetSquad(req.WorkspaceID, in.SquadID, 0)
	if err != nil {
		s.problem(w, r, http.StatusNotFound, "squad_not_found", err.Error(), nil)
		return
	}
	validErr := sq.Validate()
	draft := map[string]any{"id": domain.NewID(), "requirement_id": req.ID, "squad": sq, "status": orchestration.PlanDraft, "valid": validErr == nil}
	if validErr != nil {
		draft["validation_error"] = validErr.Error()
	}
	s.writeJSON(w, http.StatusOK, draft)
}

func (s *Server) publishExecutionPlan(w http.ResponseWriter, r *http.Request, req domain.Requirement) {
	var p orchestration.RequirementExecutionPlan
	if err := decodeJSON(r, &p); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	p.RequirementID, p.WorkspaceID = req.ID, req.WorkspaceID
	if p.ID == "" {
		p.ID = domain.NewID()
	}
	p.Status = orchestration.PlanDraft
	frozen, err := p.Freeze()
	if err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "plan_validation_failed", err.Error(), nil)
		return
	}
	if err := s.Orchestration.CreatePlan(frozen); err != nil {
		s.problem(w, r, http.StatusConflict, "plan_publish_failed", fmt.Sprintf("%v", err), nil)
		return
	}
	if event, eventErr := orchestration.NewEvent(nil, frozen.ID, frozen.WorkspaceID, "plan.published", frozen.IdempotencyKey, frozen); eventErr == nil {
		if appendErr := s.Orchestration.AppendEvent(event); appendErr != nil {
			s.problem(w, r, http.StatusServiceUnavailable, "plan_event_commit_failed", appendErr.Error(), nil)
			return
		}
	}
	s.writeJSON(w, http.StatusOK, frozen)
}
