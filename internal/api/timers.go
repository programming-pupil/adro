package api

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/adro-project/adro/internal/runtime"
)

// timerRoute exposes the durable timer read model and the operator cancellation
// command. Timer records are never mutated through a database-shaped endpoint:
// cancellation goes through TimerStore so the state transition, timestamps and
// durable snapshot share one transaction boundary.
func (s *Server) timerRoute(w http.ResponseWriter, r *http.Request, tail string) {
	if s.Timers == nil {
		s.problem(w, r, http.StatusServiceUnavailable, "timer_store_unavailable", "durable timer store is unavailable", nil)
		return
	}
	tenantID := strings.TrimSpace(tenant(r))
	workspaceID := strings.TrimSpace(requestWorkspace(r, r.URL.Query().Get("workspace_id")))
	if workspaceID == "" {
		workspaceID = "local"
	}

	tail = strings.Trim(strings.TrimSpace(tail), "/")
	if tail == "" {
		if r.Method != http.MethodGet {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "GET is required", nil)
			return
		}
		includeTerminal := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_terminal")), "true")
		items, err := s.Timers.List(runtime.Scope{}, includeTerminal)
		if err != nil {
			s.problem(w, r, http.StatusInternalServerError, "timer_list_failed", "could not read durable timers", nil)
			return
		}
		sessionID, runID := strings.TrimSpace(r.URL.Query().Get("session_id")), strings.TrimSpace(r.URL.Query().Get("run_id"))
		filtered := make([]runtime.Timer, 0, len(items))
		for _, item := range items {
			if !timerInRequestScope(item, tenantID, workspaceID, sessionID, runID) {
				continue
			}
			filtered = append(filtered, item)
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"items": filtered, "include_terminal": includeTerminal})
		return
	}

	parts := strings.Split(tail, "/")
	id := strings.TrimSpace(parts[0])
	if id == "" {
		s.problem(w, r, http.StatusNotFound, "timer_not_found", "timer not found", nil)
		return
	}
	timer, err := s.Timers.Get(id)
	if err != nil || !timerInRequestScope(timer, tenantID, workspaceID, "", "") {
		if errors.Is(err, runtime.ErrTimerNotFound) || err == nil {
			s.problem(w, r, http.StatusNotFound, "timer_not_found", "timer not found", nil)
		} else {
			s.problem(w, r, http.StatusInternalServerError, "timer_read_failed", "could not read durable timer", nil)
		}
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "GET is required", nil)
			return
		}
		s.writeJSON(w, http.StatusOK, timer)
		return
	}
	if len(parts) != 2 {
		s.problem(w, r, http.StatusNotFound, "not_found", "timer route not found", nil)
		return
	}
	switch parts[1] {
	case "explain":
		if r.Method != http.MethodGet {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "GET is required", nil)
			return
		}
		explanation, explainErr := s.Timers.Explain(id)
		if explainErr != nil {
			s.problem(w, r, http.StatusInternalServerError, "timer_explain_failed", "could not explain durable timer", nil)
			return
		}
		s.writeJSON(w, http.StatusOK, explanation)
	case "cancel":
		if r.Method != http.MethodPost {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "POST is required", nil)
			return
		}
		if !s.requireOrchestrationManagePermission(w, r) {
			return
		}
		var input struct {
			Reason string `json:"reason,omitempty"`
		}
		if err := decodeJSON(r, &input); err != nil && !errors.Is(err, io.EOF) {
			s.problem(w, r, http.StatusBadRequest, "invalid_timer_cancel", err.Error(), nil)
			return
		}
		cancelled, cancelErr := s.Timers.Cancel(id, input.Reason)
		if cancelErr != nil {
			switch {
			case errors.Is(cancelErr, runtime.ErrTimerNotFound):
				s.problem(w, r, http.StatusNotFound, "timer_not_found", "timer not found", nil)
			case errors.Is(cancelErr, runtime.ErrTimerConflict):
				s.problem(w, r, http.StatusConflict, "timer_terminal", "terminal timer cannot be cancelled", nil)
			default:
				s.problem(w, r, http.StatusInternalServerError, "timer_cancel_failed", "could not cancel durable timer", nil)
			}
			return
		}
		s.writeJSON(w, http.StatusOK, cancelled)
	default:
		s.problem(w, r, http.StatusNotFound, "not_found", "timer route not found", nil)
	}
}

func timerInRequestScope(timer runtime.Timer, tenantID, workspaceID, sessionID, runID string) bool {
	if strings.TrimSpace(timer.Scope.TenantID) != strings.TrimSpace(tenantID) || strings.TrimSpace(timer.Scope.WorkspaceID) != strings.TrimSpace(workspaceID) {
		return false
	}
	if sessionID != "" && timer.Scope.SessionID != sessionID {
		return false
	}
	if runID != "" && timer.Scope.RunID != runID {
		return false
	}
	return true
}
