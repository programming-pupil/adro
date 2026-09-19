package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/orchestration"
)

// resourceAccountingRoute exposes bounded read models and authenticated quota
// management. It never exposes the ledger snapshot path or permits direct row
// mutation, so operator changes still pass through validation and durability.
func (s *Server) resourceAccountingRoute(w http.ResponseWriter, r *http.Request, quotasOnly bool) {
	if s.ResourceLedger == nil {
		s.problem(w, r, http.StatusServiceUnavailable, "resource_accounting_unavailable", "resource accounting is unavailable", nil)
		return
	}
	workspaceID := requestWorkspace(r, r.URL.Query().Get("workspace_id"))
	tenantID := tenant(r)
	if strings.TrimSpace(workspaceID) == "" {
		s.problem(w, r, http.StatusBadRequest, "workspace_required", "workspace scope is required", nil)
		return
	}
	if quotasOnly {
		s.resourceQuotaRoute(w, r, tenantID, workspaceID)
		return
	}
	if r.Method != http.MethodGet {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "GET is required", nil)
		return
	}
	dashboard, err := s.ResourceLedger.Dashboard(orchestration.ResourceScope{TenantID: tenantID, WorkspaceID: workspaceID}, queryInt(r, "usage_limit", 100))
	if err != nil {
		s.problem(w, r, http.StatusInternalServerError, "resource_dashboard_failed", err.Error(), nil)
		return
	}
	quotas, err := s.scopedResourceQuotas(tenantID, workspaceID)
	if err != nil {
		s.problem(w, r, http.StatusInternalServerError, "resource_quotas_failed", err.Error(), nil)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"dashboard": dashboard, "quotas": quotas})
}

func (s *Server) resourceQuotaRoute(w http.ResponseWriter, r *http.Request, tenantID, workspaceID string) {
	switch r.Method {
	case http.MethodGet:
		quotas, err := s.scopedResourceQuotas(tenantID, workspaceID)
		if err != nil {
			s.problem(w, r, http.StatusInternalServerError, "resource_quotas_failed", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"items": quotas})
	case http.MethodPut:
		if !s.requireOrchestrationManagePermission(w, r) {
			return
		}
		var input struct {
			Scope                    orchestration.ResourceScope  `json:"scope"`
			SoftLimit                orchestration.ResourceVector `json:"soft_limit"`
			HardLimit                orchestration.ResourceVector `json:"hard_limit"`
			QueueWeight              int64                        `json:"queue_weight"`
			EmergencyPriorityCeiling int                          `json:"emergency_priority_ceiling"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
			return
		}
		input.Scope.TenantID = tenantID
		if strings.TrimSpace(input.Scope.WorkspaceID) != "" && strings.TrimSpace(input.Scope.WorkspaceID) != workspaceID {
			s.problem(w, r, http.StatusNotFound, "not_found", "workspace quota not found", nil)
			return
		}
		if input.Scope.AgentID != "" && input.Scope.WorkspaceID == "" {
			input.Scope.WorkspaceID = workspaceID
		}
		quota := orchestration.ResourceQuota{
			Scope: input.Scope, SoftLimit: input.SoftLimit, HardLimit: input.HardLimit,
			QueueWeight: input.QueueWeight, EmergencyPriorityCeiling: input.EmergencyPriorityCeiling,
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.ResourceLedger.SetQuota(quota); err != nil {
			s.problem(w, r, http.StatusUnprocessableEntity, "resource_quota_invalid", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, quota)
	case http.MethodDelete:
		if !s.requireOrchestrationManagePermission(w, r) {
			return
		}
		scope := orchestration.ResourceScope{
			TenantID: tenantID, WorkspaceID: strings.TrimSpace(r.URL.Query().Get("quota_workspace_id")),
			AgentID: strings.TrimSpace(r.URL.Query().Get("agent_id")),
		}
		if scope.AgentID != "" && scope.WorkspaceID == "" {
			scope.WorkspaceID = workspaceID
		}
		if scope.WorkspaceID != "" && scope.WorkspaceID != workspaceID {
			s.problem(w, r, http.StatusNotFound, "not_found", "workspace quota not found", nil)
			return
		}
		if err := s.ResourceLedger.DeleteQuota(scope); err != nil {
			if errors.Is(err, orchestration.ErrResourceNotFound) {
				s.problem(w, r, http.StatusNotFound, "resource_quota_not_found", "resource quota not found", nil)
				return
			}
			s.problem(w, r, http.StatusUnprocessableEntity, "resource_quota_invalid", err.Error(), nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "GET, PUT or DELETE is required", nil)
	}
}

func (s *Server) scopedResourceQuotas(tenantID, workspaceID string) ([]orchestration.ResourceQuota, error) {
	quotas, err := s.ResourceLedger.ListQuotas()
	if err != nil {
		return nil, err
	}
	result := make([]orchestration.ResourceQuota, 0, len(quotas))
	for _, quota := range quotas {
		if quota.Scope.TenantID != tenantID {
			continue
		}
		if quota.Scope.WorkspaceID != "" && quota.Scope.WorkspaceID != workspaceID {
			continue
		}
		result = append(result, quota)
	}
	return result, nil
}
