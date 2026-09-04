package api

// This file is the control-plane surface for the provider-neutral
// orchestration model.  It deliberately sits beside the legacy provider
// binding endpoints: old clients keep their response shape while new clients
// use immutable, revisioned Agent/Squad/Plan records.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/harness"
	"github.com/adro-project/adro/internal/orchestration"
)

// orchestrationPermission keeps control-plane mutations behind the same
// workspace identity boundary as the rest of the API. Unauthenticated access
// remains available only in the explicit local optional-auth profile.
func (s *Server) orchestrationPermission(r *http.Request) bool {
	if authorizedMachine(r) {
		return true
	}
	if user, ok := s.authenticateUser(r); ok {
		return user.Can("agents") || user.Can("executions")
	}
	return !authRequired()
}

// Management and invocation are intentionally separate capabilities. A member
// allowed to run an existing plan must not be able to publish or mutate the
// Agent/Squad definitions that shape future executions.
func (s *Server) orchestrationManagePermission(r *http.Request) bool {
	if authorizedMachine(r) {
		return true
	}
	if user, ok := s.authenticateUser(r); ok {
		return user.Can("agents") || user.Can("admin")
	}
	return !authRequired()
}

func (s *Server) requireOrchestrationManagePermission(w http.ResponseWriter, r *http.Request) bool {
	if s.orchestrationManagePermission(r) {
		return true
	}
	s.problem(w, r, http.StatusForbidden, "orchestration_manage_permission_denied", "agent or squad management permission is required", nil)
	return false
}

func (s *Server) requireOrchestrationPermission(w http.ResponseWriter, r *http.Request) bool {
	if s.orchestrationPermission(r) {
		return true
	}
	s.problem(w, r, http.StatusForbidden, "orchestration_permission_denied", "orchestration management or invoke permission is required", nil)
	return false
}

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
	if r.Method != http.MethodGet && !s.requireOrchestrationManagePermission(w, r) {
		return
	}
	if kind == "agents" {
		if tail != "" {
			s.orchestrationAgentResource(w, r, tail, workspaceID)
			return
		}
		if r.Method == http.MethodGet {
			agents := s.Orchestration.ListAgents(workspaceID, orchestration.AgentStatus(r.URL.Query().Get("status")))
			if capability := strings.TrimSpace(r.URL.Query().Get("capability")); capability != "" {
				agents = filterAgentsByCapability(agents, capability)
			}
			s.writeJSON(w, http.StatusOK, map[string]any{"items": agents})
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
			a.ID = orchestration.NewID()
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
		squad.ID = orchestration.NewID()
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
		if !s.requireOrchestrationManagePermission(w, r) {
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"valid": a.Validate() == nil, "agent": a, "error": validationError(a.Validate())})
		return
	}
	if strings.HasSuffix(id, "/capabilities") {
		if r.Method != http.MethodGet {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "GET is required", nil)
			return
		}
		caps := map[string]any{"agent": a, "required": a.ExecutorBinding.RequiredCaps, "available": map[string]any{}}
		if s.Provider != nil {
			if discovered, capErr := s.Provider.Capabilities(r.Context()); capErr == nil {
				caps["available"] = discovered
				missing := make([]string, 0)
				for _, required := range a.ExecutorBinding.RequiredCaps {
					if !discovered.Supports(required) {
						missing = append(missing, required)
					}
				}
				caps["missing"] = missing
			} else {
				caps["error"] = capErr.Error()
			}
		}
		s.writeJSON(w, http.StatusOK, caps)
		return
	}
	if r.Method != http.MethodGet && !s.requireOrchestrationManagePermission(w, r) {
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
	if strings.HasSuffix(id, "/graph") {
		if r.Method == http.MethodGet {
			digest, _ := sq.Graph.CanonicalHash()
			s.writeJSON(w, http.StatusOK, map[string]any{"format": "adro.workflow-graph.v1", "graph": sq.Graph, "validation_digest": digest, "squad_id": sq.ID, "squad_revision": sq.Revision})
			return
		}
		if r.Method != http.MethodPut {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "PUT is required", nil)
			return
		}
		if !s.requireOrchestrationManagePermission(w, r) {
			return
		}
		var input struct {
			ExpectedRevision int64                       `json:"expected_revision"`
			Graph            orchestration.WorkflowGraph `json:"graph"`
		}
		if err := decodeJSON(r, &input); err != nil {
			s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
			return
		}
		if input.ExpectedRevision == 0 {
			s.problem(w, r, http.StatusBadRequest, "expected_revision_required", "expected_revision is required", nil)
			return
		}
		if err := orchestration.ValidateGraph(input.Graph); err != nil {
			s.problem(w, r, http.StatusUnprocessableEntity, "graph_validation_failed", err.Error(), map[string]any{"diagnostics": orchestration.DiagnoseGraph(input.Graph)})
			return
		}
		input.Graph.ValidationDigest, _ = input.Graph.CanonicalHash()
		sq.Graph = input.Graph
		sq.Revision++
		if err := s.Orchestration.SaveSquad(sq, input.ExpectedRevision); err != nil {
			s.problem(w, r, http.StatusConflict, "squad_graph_update_conflict", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"squad": sq, "graph": sq.Graph, "validation_digest": sq.Graph.ValidationDigest, "diagnostics": orchestration.DiagnoseGraph(sq.Graph)})
		return
	}
	if strings.HasSuffix(id, "/fork") {
		if r.Method != http.MethodPost {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "POST is required", nil)
			return
		}
		if !s.requireOrchestrationManagePermission(w, r) {
			return
		}
		var input struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := decodeJSON(r, &input); err != nil && !errors.Is(err, io.EOF) {
			s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
			return
		}
		copyOf := sq
		copyOf.ID = orchestration.NewID()
		copyOf.Name = strings.TrimSpace(input.Name)
		if copyOf.Name == "" {
			copyOf.Name = "Copy of " + sq.Name
		}
		if strings.TrimSpace(input.Description) != "" {
			copyOf.Description = input.Description
		}
		copyOf.Revision = 1
		copyOf.PublishedVersion = 0
		copyOf.Status = orchestration.SquadDraft
		copyOf.Graph.ID = orchestration.NewID()
		copyOf.Graph.Version = 1
		copyOf.Graph.ValidationDigest = ""
		if err := s.Orchestration.SaveSquad(copyOf, 0); err != nil {
			s.problem(w, r, http.StatusUnprocessableEntity, "squad_fork_failed", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusCreated, map[string]any{"squad": copyOf, "source_squad_id": sq.ID, "source_revision": sq.Revision})
		return
	}
	if strings.Contains(id, "/validate") || strings.Contains(id, "/dry-run") {
		if r.Method != http.MethodPost {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		if !s.requireOrchestrationManagePermission(w, r) {
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
	if r.Method != http.MethodGet && !s.requireOrchestrationManagePermission(w, r) {
		return
	}
	if r.Method == http.MethodGet {
		s.writeJSON(w, http.StatusOK, sq)
		return
	}
	if r.Method == http.MethodPost && (strings.HasSuffix(id, "/publish") || strings.HasSuffix(id, "/disable") || strings.HasSuffix(id, "/archive")) {
		if strings.HasSuffix(id, "/publish") {
			if err := sq.Validate(); err != nil {
				s.problem(w, r, http.StatusUnprocessableEntity, "squad_validation_failed", err.Error(), map[string]any{"diagnostics": orchestration.DiagnoseGraph(sq.Graph)})
				return
			}
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

func filterAgentsByCapability(agents []orchestration.AgentDefinition, capability string) []orchestration.AgentDefinition {
	capability = strings.TrimSpace(capability)
	if capability == "" {
		return agents
	}
	filtered := make([]orchestration.AgentDefinition, 0, len(agents))
	for _, agent := range agents {
		for _, candidate := range agent.Capabilities {
			if candidate.Name == capability {
				filtered = append(filtered, agent)
				break
			}
		}
	}
	return filtered
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
		// Creating a plan from an already active Agent or published Squad is an
		// invocation operation. Definition mutations (quick-squad and publish)
		// remain management-only below.
		if !s.requireOrchestrationPermission(w, r) {
			return
		}
		s.createExecutionPlan(w, r, req)
		return
	}
	if tail == "quick-squad" && r.Method == http.MethodPost {
		if !s.requireOrchestrationManagePermission(w, r) {
			return
		}
		s.quickSquadPlan(w, r, req)
		return
	}
	if (tail == "validate" || tail == "dry-run") && r.Method == http.MethodPost {
		if !s.requireOrchestrationPermission(w, r) {
			return
		}
		s.validateExecutionPlanRequest(w, r, req, tail == "dry-run")
		return
	}
	if tail == "publish" && r.Method == http.MethodPost {
		if !s.requireOrchestrationManagePermission(w, r) {
			return
		}
		s.publishExecutionPlan(w, r, req)
		return
	}
	s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
}

type executionPlanRequest struct {
	AgentID        string                      `json:"agent_id"`
	AgentRevision  int64                       `json:"agent_revision"`
	SquadID        string                      `json:"squad_id"`
	SquadVersion   int64                       `json:"squad_version"`
	Graph          orchestration.WorkflowGraph `json:"graph"`
	GraphOverrides map[string]any              `json:"graph_overrides,omitempty"`
	IdempotencyKey string                      `json:"idempotency_key"`
}

func (s *Server) resolveExecutionGraph(workspaceID string, in executionPlanRequest) (orchestration.WorkflowGraph, orchestration.VersionedRef, error) {
	graph := in.Graph
	selected := orchestration.VersionedRef{}
	if strings.TrimSpace(in.SquadID) != "" {
		sq, err := s.getPublishedSquad(workspaceID, in.SquadID, in.SquadVersion)
		if err != nil {
			return orchestration.WorkflowGraph{}, selected, fmt.Errorf("published squad revision is required: %w", err)
		}
		selected = orchestration.VersionedRef{ID: sq.ID, Revision: sq.Revision, Version: sq.PublishedVersion}
		// A supplied graph is a deliberate temporary edit of the published
		// template. An empty graph selects the template unchanged.
		if len(graph.Nodes) == 0 {
			graph = sq.Graph
		}
	}
	if strings.TrimSpace(in.AgentID) != "" {
		a, err := s.Orchestration.GetAgent(workspaceID, in.AgentID, in.AgentRevision)
		if err != nil || a.Status != orchestration.AgentActive {
			return orchestration.WorkflowGraph{}, selected, errors.New("active agent revision is required")
		}
		selected = orchestration.VersionedRef{ID: a.ID, Revision: a.Revision}
		if len(graph.Nodes) == 0 {
			graph = singleAgentGraph(a)
		}
	}
	if len(graph.Nodes) == 0 {
		return orchestration.WorkflowGraph{}, selected, errors.New("graph or agent_id/squad_id is required")
	}
	if err := s.validateExecutionGraphReferences(workspaceID, graph); err != nil {
		return graph, selected, err
	}
	return graph, selected, nil
}

func (s *Server) validateExecutionPlanRequest(w http.ResponseWriter, r *http.Request, req domain.Requirement, dryRun bool) {
	var in executionPlanRequest
	if err := decodeJSON(r, &in); err != nil && !errors.Is(err, io.EOF) {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	graph, selected, resolveErr := s.resolveExecutionGraph(req.WorkspaceID, in)
	response := map[string]any{"valid": false, "requirement_id": req.ID, "selected_ref": selected, "diagnostics": orchestration.DiagnoseGraph(graph)}
	if resolveErr != nil {
		response["errors"] = []string{resolveErr.Error()}
		if dryRun {
			response["mode"] = "dry-run"
		}
		s.writeJSON(w, http.StatusOK, response)
		return
	}
	if err := orchestration.ValidateGraph(graph); err != nil {
		response["errors"] = []string{err.Error()}
		if dryRun {
			response["mode"] = "dry-run"
		}
		s.writeJSON(w, http.StatusOK, response)
		return
	}
	graph.ValidationDigest, _ = graph.CanonicalHash()
	response["valid"] = true
	response["graph"] = graph
	response["validation_digest"] = graph.ValidationDigest
	if dryRun {
		response["mode"] = "dry-run"
		pending := orchestration.PlanProjection{Nodes: map[string]orchestration.NodeProjection{}}
		for _, node := range graph.Nodes {
			pending.Nodes[node.ID] = orchestration.NodeProjection{NodeID: node.ID, Status: orchestration.AttemptPending}
		}
		for _, nodeID := range graph.EntryNodeIDs {
			if node, ok := pending.Nodes[nodeID]; ok {
				node.Status = orchestration.AttemptReady
				pending.Nodes[nodeID] = node
			}
		}
		response["ready_nodes"] = orchestration.ReadyNodes(orchestration.RequirementExecutionPlan{GraphSnapshot: graph}, pending)
	}
	s.writeJSON(w, http.StatusOK, response)
}

func (s *Server) createExecutionPlan(w http.ResponseWriter, r *http.Request, req domain.Requirement) {
	var in executionPlanRequest
	if err := decodeJSON(r, &in); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	graph, selected, resolveErr := s.resolveExecutionGraph(req.WorkspaceID, in)
	if resolveErr != nil {
		code := "graph_required"
		if strings.Contains(resolveErr.Error(), "published squad") {
			code = "squad_unavailable"
		} else if strings.Contains(resolveErr.Error(), "agent revision") {
			code = "agent_unavailable"
		} else if strings.Contains(resolveErr.Error(), "graph.nodes") {
			code = "graph_reference_unavailable"
		}
		s.problem(w, r, http.StatusUnprocessableEntity, code, resolveErr.Error(), nil)
		return
	}
	if key := strings.TrimSpace(in.IdempotencyKey); key != "" {
		if existing, lookupErr := s.Orchestration.GetPlanByIdempotency(req.WorkspaceID, key); lookupErr == nil {
			existingGraphHash, _ := existing.GraphSnapshot.CanonicalHash()
			requestedGraphHash, _ := graph.CanonicalHash()
			if existing.RequirementID != req.ID || existing.SelectedRef != selected || existingGraphHash != requestedGraphHash {
				s.problem(w, r, http.StatusConflict, "idempotency_key_conflict", "idempotency key was already used with a different execution plan", nil)
				return
			}
			s.writeJSON(w, http.StatusOK, existing)
			return
		}
	}
	now := time.Now().UTC()
	p := orchestration.RequirementExecutionPlan{ID: domain.NewID(), RequirementID: req.ID, WorkspaceID: req.WorkspaceID, GraphSnapshot: graph, SelectedRef: selected, PolicySnapshot: orchestration.PolicySnapshot{CapturedAt: now}, ContextRoot: orchestration.ContextRef{SessionID: "plan-" + req.ID, ManifestDigest: "pending"}, Status: orchestration.PlanDraft, IdempotencyKey: in.IdempotencyKey, CreatedAt: now}
	// Requirement publication owns a durable context root. This keeps browser
	// and API-created graphs on the same authoritative context compiler path;
	// callers may still provide a newer envelope at tick time, but a graph never
	// starts with an implicit, untracked prompt.
	if s.Harness != nil {
		contextSession, sessionErr := s.Harness.EnsureSession(harness.Session{ID: p.ContextRoot.SessionID, TenantID: req.WorkspaceID, WorkspaceID: req.WorkspaceID, BudgetTokens: harnessSessionBudget()})
		if sessionErr != nil && !errors.Is(sessionErr, harness.ErrConflict) {
			s.problem(w, r, http.StatusConflict, "context_session_failed", sessionErr.Error(), nil)
			return
		}
		if sessionErr == nil {
			objective := strings.TrimSpace(req.Title + "\n" + req.Description)
			if len(req.AcceptanceCriteria) > 0 {
				objective += "\nAcceptance criteria:\n- " + strings.Join(req.AcceptanceCriteria, "\n- ")
			}
			turnKey := "plan:" + p.ID + ":objective"
			if _, turnErr := s.Harness.AppendTurn(contextSession.ID, harness.Turn{Role: harness.RoleUser, Content: objective, IdempotencyKey: turnKey, Metadata: map[string]string{"requirement_id": req.ID, "graph_id": graph.ID}}); turnErr != nil && !errors.Is(turnErr, harness.ErrConflict) {
				s.problem(w, r, http.StatusConflict, "context_objective_failed", turnErr.Error(), nil)
				return
			}
			if envelope, envelopeErr := s.Harness.CompileEnvelope(contextSession.ID, 0); envelopeErr == nil {
				p.ContextRoot.ManifestDigest = envelope.Manifest.Digest
				p.ContextRoot.ReplayKey = envelope.ReplayKey
			}
		}
	}
	frozen, err := p.Freeze()
	if err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "plan_validation_failed", err.Error(), nil)
		return
	}
	event, eventErr := orchestration.NewEventWithContext(r.Context(), nil, frozen.ID, frozen.WorkspaceID, "plan.created", frozen.IdempotencyKey, frozen)
	if eventErr != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "plan_event_build_failed", eventErr.Error(), nil)
		return
	}
	if err := s.Orchestration.CreatePlanWithEvent(frozen, event); err != nil {
		status := http.StatusConflict
		if errors.Is(err, orchestration.ErrIdempotencyConflict) {
			status = http.StatusConflict
		}
		s.problem(w, r, status, "plan_create_failed", err.Error(), nil)
		return
	}
	// A concurrent request may have won the same idempotency key between the
	// preflight lookup and the atomic repository commit. Return that durable
	// winner instead of exposing a locally generated duplicate response.
	if key := strings.TrimSpace(frozen.IdempotencyKey); key != "" {
		if persisted, lookupErr := s.Orchestration.GetPlanByIdempotency(req.WorkspaceID, key); lookupErr == nil && persisted.ID != frozen.ID {
			s.writeJSON(w, http.StatusOK, persisted)
			return
		}
	}
	s.writeJSON(w, http.StatusCreated, frozen)
}

// getPublishedSquad accepts both the durable revision and the externally
// visible published_version. Clients should normally send published_version,
// while historical callers often sent revision; accepting either keeps the
// frozen plan selection unambiguous across upgrades.
func (s *Server) getPublishedSquad(workspaceID, squadID string, version int64) (orchestration.SquadDefinition, error) {
	if s.Orchestration == nil {
		return orchestration.SquadDefinition{}, orchestration.ErrNotFound
	}
	if version > 0 {
		if squad, err := s.Orchestration.GetSquad(workspaceID, squadID, version); err == nil && squad.Status == orchestration.SquadPublished && (squad.PublishedVersion == version || squad.Revision == version) {
			return squad, nil
		}
	}
	squad, err := s.Orchestration.GetSquad(workspaceID, squadID, 0)
	if err != nil || squad.Status != orchestration.SquadPublished {
		if err != nil {
			return orchestration.SquadDefinition{}, err
		}
		return orchestration.SquadDefinition{}, fmt.Errorf("squad %s is not published", squadID)
	}
	if version > 0 && squad.PublishedVersion != version && squad.Revision != version {
		return orchestration.SquadDefinition{}, fmt.Errorf("squad %s published version %d is not available", squadID, version)
	}
	return squad, nil
}

func singleAgentGraph(a orchestration.AgentDefinition) orchestration.WorkflowGraph {
	return orchestration.WorkflowGraph{ID: "graph-" + a.ID, Version: a.Revision, EntryNodeIDs: []string{"agent"}, ExitNodeIDs: []string{"agent"}, Nodes: []orchestration.WorkflowNode{{ID: "agent", Kind: orchestration.NodeAgent, AgentRef: &orchestration.VersionedRef{ID: a.ID, Revision: a.Revision}}}}
}

func (s *Server) quickSquadPlan(w http.ResponseWriter, r *http.Request, req domain.Requirement) {
	var in struct {
		SquadID     string                      `json:"squad_id"`
		Name        string                      `json:"name"`
		Description string                      `json:"description,omitempty"`
		Members     []orchestration.SquadMember `json:"members,omitempty"`
		Graph       orchestration.WorkflowGraph `json:"graph,omitempty"`
		Policy      orchestration.SquadPolicy   `json:"policy,omitempty"`
	}
	if err := decodeJSON(r, &in); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	var sq orchestration.SquadDefinition
	if strings.TrimSpace(in.SquadID) != "" {
		var err error
		sq, err = s.Orchestration.GetSquad(req.WorkspaceID, in.SquadID, 0)
		if err != nil {
			s.problem(w, r, http.StatusNotFound, "squad_not_found", err.Error(), nil)
			return
		}
	} else {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			name = "Requirement " + req.ID + " squad"
		}
		sq = orchestration.SquadDefinition{ID: orchestration.NewID(), WorkspaceID: req.WorkspaceID, Name: name, Description: in.Description, Revision: 1, Members: in.Members, Graph: in.Graph, Policy: in.Policy, Status: orchestration.SquadDraft}
		if err := s.Orchestration.SaveSquad(sq, 0); err != nil {
			s.problem(w, r, http.StatusUnprocessableEntity, "squad_validation_failed", err.Error(), nil)
			return
		}
	}
	validErr := sq.Validate()
	draft := map[string]any{"id": domain.NewID(), "requirement_id": req.ID, "squad": sq, "status": orchestration.PlanDraft, "valid": validErr == nil, "persisted": true}
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
		p.ID = orchestration.NewID()
	}
	p.Status = orchestration.PlanDraft
	if err := s.validateExecutionGraphReferences(req.WorkspaceID, p.GraphSnapshot); err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "graph_reference_unavailable", err.Error(), nil)
		return
	}
	frozen, err := p.Freeze()
	if err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "plan_validation_failed", err.Error(), nil)
		return
	}
	event, eventErr := orchestration.NewEventWithContext(r.Context(), nil, frozen.ID, frozen.WorkspaceID, "plan.published", frozen.IdempotencyKey, frozen)
	if eventErr != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "plan_event_build_failed", eventErr.Error(), nil)
		return
	}
	if err := s.Orchestration.CreatePlanWithEvent(frozen, event); err != nil {
		s.problem(w, r, http.StatusConflict, "plan_publish_failed", fmt.Sprintf("%v", err), nil)
		return
	}
	s.writeJSON(w, http.StatusOK, frozen)
}

func (s *Server) validateExecutionGraphReferences(workspaceID string, graph orchestration.WorkflowGraph) error {
	if s.Orchestration == nil {
		return errors.New("orchestration repository is unavailable")
	}
	for i, node := range graph.Nodes {
		if node.AgentRef != nil {
			agent, err := s.Orchestration.GetAgent(workspaceID, node.AgentRef.ID, node.AgentRef.Revision)
			if err != nil {
				return fmt.Errorf("graph.nodes[%d].agent_ref.unavailable", i)
			}
			if agent.Status != orchestration.AgentActive {
				return fmt.Errorf("graph.nodes[%d].agent_ref.inactive", i)
			}
		}
		if node.SquadRef != nil {
			version := node.SquadRef.Revision
			if version == 0 {
				version = node.SquadRef.Version
			}
			if _, err := s.getPublishedSquad(workspaceID, node.SquadRef.ID, version); err != nil {
				return fmt.Errorf("graph.nodes[%d].squad_ref.unavailable", i)
			}
		}
	}
	return nil
}
