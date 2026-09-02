package api

import (
	"net/http"
	"strings"

	"github.com/adro-project/adro/internal/mentions"
	"github.com/adro-project/adro/internal/orchestration"
)

// orchestrationRoute exposes the plan/graph contracts without coupling the
// legacy pipeline handler to numeric stages. The in-memory repository is a
// deliberate local profile; production wiring can replace it via Server's
// repository seam later.
func (s *Server) orchestrationRoute(w http.ResponseWriter, r *http.Request, path string) {
	if path == "/execution-plans" {
		if s.Orchestration == nil {
			s.problem(w, r, http.StatusServiceUnavailable, "orchestration_unavailable", "orchestration repository is unavailable", nil)
			return
		}
		if r.Method != http.MethodGet {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"items": s.Orchestration.ListPlans(requestWorkspace(r, r.URL.Query().Get("workspace_id")))})
		return
	}
	if strings.HasPrefix(path, "/execution-plans/") && path != "/execution-plans/validate" {
		id := strings.TrimPrefix(path, "/execution-plans/")
		if s.Orchestration == nil {
			s.problem(w, r, http.StatusServiceUnavailable, "orchestration_unavailable", "orchestration repository is unavailable", nil)
			return
		}
		if strings.HasSuffix(id, "/timeline") {
			realID := strings.TrimSuffix(id, "/timeline")
			plan, err := s.Orchestration.GetPlan(requestWorkspace(r, "local"), realID)
			if err != nil {
				s.problem(w, r, http.StatusNotFound, "plan_not_found", err.Error(), nil)
				return
			}
			s.writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "events": s.Orchestration.ListEvents(realID, int64(queryInt(r, "after", 0)))})
			return
		}
		plan, err := s.Orchestration.GetPlan(requestWorkspace(r, "local"), id)
		if err != nil {
			s.problem(w, r, http.StatusNotFound, "plan_not_found", err.Error(), nil)
			return
		}
		if r.Method != http.MethodGet {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		s.writeJSON(w, http.StatusOK, plan)
		return
	}
	if r.Method != http.MethodPost || path != "/execution-plans/validate" {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "only POST /execution-plans/validate is supported", nil)
		return
	}
	var input struct {
		Graph orchestration.WorkflowGraph             `json:"graph"`
		Plan  *orchestration.RequirementExecutionPlan `json:"plan,omitempty"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	if err := orchestration.ValidateGraph(input.Graph); err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "graph_validation_failed", err.Error(), map[string]any{"graph_id": input.Graph.ID})
		return
	}
	hash, err := input.Graph.CanonicalHash()
	if err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "graph_hash_failed", err.Error(), nil)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"valid": true, "validation_digest": hash, "graph": input.Graph})
}

func (s *Server) mentionPreviewRoute(w http.ResponseWriter, r *http.Request, requirementID string) {
	if r.Method != http.MethodPost {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	var input struct {
		Content        string            `json:"content"`
		CommentID      string            `json:"comment_id"`
		Revision       int64             `json:"revision"`
		Targets        []mentions.Target `json:"targets"`
		RuntimeHealthy bool              `json:"runtime_healthy"`
		PlanVersion    string            `json:"plan_version"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	workspace := requestWorkspace(r, "")
	plan, err := mentions.ComputeTriggers(r.Context(), mentions.TriggerInput{WorkspaceID: workspace, RequirementID: requirementID, CommentID: input.CommentID, CommentRevision: input.Revision, Content: input.Content, Targets: input.Targets, UserCanInvoke: true, RuntimeHealthy: input.RuntimeHealthy, PlanVersion: input.PlanVersion})
	if err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "trigger_preview_failed", err.Error(), nil)
		return
	}
	s.writeJSON(w, http.StatusOK, plan)
}
