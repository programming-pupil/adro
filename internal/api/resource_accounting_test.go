package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/adro-project/adro/internal/orchestration"
)

func TestResourceAccountingAPIShowsScopedBurnReservationsAndAttribution(t *testing.T) {
	s := testServer(t)
	headers := map[string]string{"X-Tenant-ID": "tenant-a", "X-Workspace-ID": "workspace-a", "Idempotency-Key": "quota-a"}
	quotaBody := `{"scope":{"workspace_id":"workspace-a"},"soft_limit":{"tokens":8},"hard_limit":{"tokens":20,"concurrency_slots":2},"queue_weight":2,"emergency_priority_ceiling":80}`
	quotaResponse := request(t, s.Routes(), http.MethodPut, "/api/v1/resources/quotas", quotaBody, headers)
	if quotaResponse.Code != http.StatusOK || !strings.Contains(quotaResponse.Body.String(), `"queue_weight":2`) {
		t.Fatalf("quota status=%d body=%s", quotaResponse.Code, quotaResponse.Body.String())
	}
	now := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	scope := orchestration.ResourceScope{TenantID: "tenant-a", WorkspaceID: "workspace-a", AgentID: "agent-a", SessionID: "session-a", StepID: "step-a", ModelCallID: "model-a", CostCenter: "delivery-a"}
	reservation, _, _, err := s.ResourceLedger.Reserve(orchestration.ResourceReservationSpec{ID: "reservation-a", IdempotencyKey: "reservation-a", Scope: scope, Requested: orchestration.ResourceVector{Tokens: 10, ConcurrencySlots: 1}, CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	usage, err := orchestration.NewUsageRecord("usage-a", reservation.ID, scope, json.RawMessage(`{"input_tokens":6,"output_tokens":3}`), orchestration.ResourceVector{Tokens: 9, ConcurrencySlots: 1}, orchestration.ResourceVector{Tokens: 8, ConcurrencySlots: 1}, false, false, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.ResourceLedger.RecordUsage(usage); err != nil {
		t.Fatal(err)
	}

	dashboardResponse := request(t, s.Routes(), http.MethodGet, "/api/v1/resources", "", headers)
	if dashboardResponse.Code != http.StatusOK {
		t.Fatalf("dashboard status=%d body=%s", dashboardResponse.Code, dashboardResponse.Body.String())
	}
	var result struct {
		Dashboard orchestration.ResourceDashboard `json:"dashboard"`
		Quotas    []orchestration.ResourceQuota   `json:"quotas"`
	}
	if err := json.Unmarshal(dashboardResponse.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Dashboard.Exposure.Tokens != 10 || result.Dashboard.Consumed.Tokens != 9 || result.Dashboard.ChildAgentUsage["agent-a"].Tokens != 0 || len(result.Dashboard.ActiveReservations) != 1 || len(result.Quotas) != 1 {
		t.Fatalf("resource response=%+v", result)
	}
	if len(result.Dashboard.RecentUsage) != 1 || result.Dashboard.RecentUsage[0].Discrepancy.Tokens != 1 {
		t.Fatalf("usage response=%+v", result.Dashboard.RecentUsage)
	}

	otherHeaders := map[string]string{"X-Tenant-ID": "tenant-b", "X-Workspace-ID": "workspace-b"}
	other := request(t, s.Routes(), http.MethodGet, "/api/v1/resources", "", otherHeaders)
	if other.Code != http.StatusOK || strings.Contains(other.Body.String(), "reservation-a") || strings.Contains(other.Body.String(), "workspace-a") {
		t.Fatalf("cross-scope status=%d body=%s", other.Code, other.Body.String())
	}
}

func TestResourceQuotaAPIRejectsCrossWorkspaceAndInvalidLimits(t *testing.T) {
	s := testServer(t)
	headers := map[string]string{"X-Tenant-ID": "tenant-a", "X-Workspace-ID": "workspace-a", "Idempotency-Key": "quota-invalid"}
	cross := request(t, s.Routes(), http.MethodPut, "/api/v1/resources/quotas", `{"scope":{"workspace_id":"workspace-b"},"hard_limit":{"tokens":10}}`, headers)
	if cross.Code != http.StatusNotFound {
		t.Fatalf("cross workspace status=%d body=%s", cross.Code, cross.Body.String())
	}
	invalidHeaders := map[string]string{"X-Tenant-ID": "tenant-a", "X-Workspace-ID": "workspace-a", "Idempotency-Key": "quota-invalid-2"}
	invalid := request(t, s.Routes(), http.MethodPut, "/api/v1/resources/quotas", `{"scope":{"workspace_id":"workspace-a"},"soft_limit":{"tokens":11},"hard_limit":{"tokens":10}}`, invalidHeaders)
	if invalid.Code != http.StatusUnprocessableEntity || !strings.Contains(invalid.Body.String(), "soft tokens exceeds hard limit") {
		t.Fatalf("invalid status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}
