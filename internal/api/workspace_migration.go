package api

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/adro-project/adro/internal/workspacebundle"
)

const maxWorkspaceBundleRequest = 512 << 20

func (s *Server) workspaceMigrationRoute(w http.ResponseWriter, r *http.Request, route string) {
	rest := strings.TrimPrefix(route, "/api/v1/workspaces/")
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[1] != "migration" {
		s.problem(w, r, http.StatusNotFound, "not_found", "route not found", nil)
		return
	}
	workspaceID, err := url.PathUnescape(parts[0])
	if err != nil || strings.TrimSpace(workspaceID) == "" {
		s.problem(w, r, http.StatusBadRequest, "invalid_workspace", "workspace ID is invalid", nil)
		return
	}
	if requested := requestWorkspace(r, workspaceID); requested != workspaceID {
		s.problem(w, r, http.StatusForbidden, "workspace_access_denied", "the requested workspace is outside the authenticated workspace", nil)
		return
	}
	service := workspacebundle.Service{Control: s.Store, Definitions: s.Orchestration, Artifacts: s.Artifacts}
	switch parts[2] {
	case "export":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		archive, manifest, exportErr := service.Export(r.Context(), workspaceID)
		if exportErr != nil {
			s.problem(w, r, http.StatusUnprocessableEntity, "workspace_export_failed", exportErr.Error(), nil)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.adro.workspace+zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="adro-workspace-%s.zip"`, safeDownloadName(workspaceID)))
		w.Header().Set("Digest", "sha-256="+manifest.Digest)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(archive)
	case "preflight", "import":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWorkspaceBundleRequest))
		if readErr != nil {
			s.problem(w, r, http.StatusRequestEntityTooLarge, "workspace_bundle_too_large", "workspace bundle exceeds the HTTP import limit", nil)
			return
		}
		policy := r.URL.Query().Get("conflict")
		if parts[2] == "preflight" {
			report, preflightErr := service.Preflight(r.Context(), body, workspaceID, policy)
			if preflightErr != nil {
				s.problem(w, r, http.StatusUnprocessableEntity, "workspace_preflight_failed", preflightErr.Error(), nil)
				return
			}
			s.writeJSON(w, http.StatusOK, report)
			return
		}
		dryRun, parseErr := strconv.ParseBool(defaultString(r.URL.Query().Get("dry_run"), "false"))
		if parseErr != nil {
			s.problem(w, r, http.StatusBadRequest, "invalid_dry_run", "dry_run must be true or false", nil)
			return
		}
		report, importErr := service.Import(r.Context(), body, workspaceID, policy, dryRun)
		if importErr != nil {
			s.problem(w, r, http.StatusConflict, "workspace_import_failed", importErr.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, report)
	default:
		s.problem(w, r, http.StatusNotFound, "not_found", "route not found", nil)
	}
}

func safeDownloadName(value string) string {
	var out strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			out.WriteRune(r)
		}
	}
	if out.Len() == 0 {
		return "workspace"
	}
	return out.String()
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
