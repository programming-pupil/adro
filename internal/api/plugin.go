package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/adro-project/adro/internal/plugins"
)

func (s *Server) pluginRoute(w http.ResponseWriter, r *http.Request, tail string) {
	if s.Plugins == nil {
		s.problem(w, r, http.StatusServiceUnavailable, "plugin_registry_unavailable", "plugin registry is not configured", nil)
		return
	}
	if r.Method != http.MethodGet && !s.pluginAdminAllowed(r) {
		s.problem(w, r, http.StatusForbidden, "administrator_required", "plugin installation and lifecycle changes require administrator access", nil)
		return
	}
	workspaceID := requestWorkspace(r, "local")
	tail = strings.Trim(tail, "/")
	if tail == "" {
		if r.Method == http.MethodGet {
			items := s.Plugins.ListWorkspace(workspaceID)
			filtered := items[:0]
			for _, item := range items {
				if item.TenantID == tenant(r) {
					filtered = append(filtered, item)
				}
			}
			s.writeJSON(w, http.StatusOK, map[string]any{"items": filtered})
			return
		}
		if r.Method != http.MethodPost {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		var request plugins.InstallRequest
		if err := decodeJSON(r, &request); err != nil {
			s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
			return
		}
		request.TenantID = tenant(r)
		request.WorkspaceID = workspaceID
		item, err := s.Plugins.Install(request)
		if err != nil {
			status := http.StatusUnprocessableEntity
			if errors.Is(err, plugins.ErrConflict) {
				status = http.StatusConflict
			}
			s.problem(w, r, status, "plugin_install_failed", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusCreated, item)
		return
	}
	parts := strings.Split(tail, "/")
	item, err := s.Plugins.GetForWorkspace(workspaceID, parts[0])
	if err != nil || item.TenantID != tenant(r) {
		s.problem(w, r, http.StatusNotFound, "plugin_not_found", "plugin installation not found", nil)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		s.writeJSON(w, http.StatusOK, item)
		return
	}
	if len(parts) == 2 && parts[1] == "activate" && r.Method == http.MethodPost {
		item, err = s.Plugins.ActivateForWorkspace(workspaceID, parts[0])
		if err != nil {
			s.problem(w, r, http.StatusConflict, "plugin_activation_failed", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, item)
		return
	}
	if len(parts) == 2 && parts[1] == "health" && r.Method == http.MethodPost {
		var input struct {
			Healthy bool   `json:"healthy"`
			Message string `json:"message,omitempty"`
		}
		if err := decodeJSON(r, &input); err != nil {
			s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
			return
		}
		item, err = s.Plugins.RecordHealthForWorkspace(workspaceID, parts[0], input.Healthy, input.Message)
		if err != nil {
			s.problem(w, r, http.StatusConflict, "plugin_health_failed", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, item)
		return
	}
	if len(parts) == 2 && parts[1] == "quarantine" && r.Method == http.MethodPost {
		var input struct {
			Reason string `json:"reason"`
		}
		if err := decodeJSON(r, &input); err != nil {
			s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
			return
		}
		item, err = s.Plugins.QuarantineForWorkspace(workspaceID, parts[0], input.Reason)
		if err != nil {
			s.problem(w, r, http.StatusConflict, "plugin_quarantine_failed", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, item)
		return
	}
	s.problem(w, r, http.StatusNotFound, "not_found", "route not found", nil)
}

func (s *Server) pluginAdminAllowed(r *http.Request) bool {
	if authorizedMachine(r) {
		return true
	}
	user, authenticated := s.authenticateUser(r)
	if authenticated {
		return user.Role == "admin"
	}
	// A brand-new optional local profile has no identity source yet. Allowing
	// bootstrap installation keeps the single-node setup usable; once any user
	// exists, anonymous plugin mutations are denied fail-closed.
	return s.Auth == nil || len(s.Auth.ListUsers("")) == 0
}
