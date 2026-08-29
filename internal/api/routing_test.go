package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/adro-project/adro/internal/artifact"
	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/provider"
	"github.com/adro-project/adro/internal/store"
)

func TestMaterializationRoutesOnceAndPersistsBinding(t *testing.T) {
	const workspaceID = "00000000-0000-0000-0000-000000000001"
	const agentID = "00000000-0000-0000-0000-00000000000a"
	var mu sync.Mutex
	createCalls := 0
	var assigneeType, assigneeID string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/issues" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var input struct {
			AssigneeType string `json:"assignee_type"`
			AssigneeID   string `json:"assignee_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		mu.Lock()
		createCalls++
		assigneeType, assigneeID = input.AssigneeType, input.AssigneeID
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "issue-1"})
	}))
	defer upstream.Close()
	config, err := provider.ParseAgentRouteConfig(`{"workspaces":{"` + workspaceID + `":{"members":{"alice":"` + agentID + `"}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	fs, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithRouting(store.NewMemory(), provider.NewMulticaProvider(upstream.URL, "secret"), fs, bus, nil, provider.NewAgentRouteResolver(config, ""))
	requirement := domain.Requirement{ID: "req-1", Key: "REQ-1", WorkspaceID: workspaceID, Description: "route", RepositoryIDs: []string{"repo"}, AssigneeMemberIDs: []string{"alice"}}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.materializeWorkItems(context.Background(), requirement); err != nil {
				t.Errorf("materialize: %v", err)
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	if createCalls != 1 || assigneeType != "agent" || assigneeID != agentID {
		t.Fatalf("provider calls=%d assignee=%s/%s", createCalls, assigneeType, assigneeID)
	}
	mu.Unlock()
	items := s.Store.ListWorkItems(requirement.ID)
	if len(items) != 1 || items[0].DeveloperAgentBindingID == "" || items[0].AgentRouteSource != "member" {
		t.Fatalf("items=%+v", items)
	}
	if strings.Contains(items[0].DeveloperAgentBindingID, agentID) {
		t.Fatal("binding id contains native agent id")
	}
	if _, err := s.Store.GetProviderBinding(items[0].DeveloperAgentBindingID); err != nil {
		t.Fatalf("provider binding not persisted: %v", err)
	}
}

func TestMaterializationRetriesIncompleteItemWithPersistedRoute(t *testing.T) {
	const workspaceID = "00000000-0000-0000-0000-000000000001"
	const agentID = "00000000-0000-0000-0000-00000000000a"
	calls := 0
	var assignees []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/issues" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var input struct {
			AssigneeID string `json:"assignee_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		calls++
		assignees = append(assignees, input.AssigneeID)
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "issue-retry"})
	}))
	defer upstream.Close()
	config, err := provider.ParseAgentRouteConfig(`{"workspaces":{"` + workspaceID + `":{"members":{"alice":"` + agentID + `"}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	fs, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithRouting(store.NewMemory(), provider.NewMulticaProvider(upstream.URL, "secret"), fs, bus, nil, provider.NewAgentRouteResolver(config, ""))
	requirement := domain.Requirement{ID: "req-retry", Key: "REQ-RETRY", WorkspaceID: workspaceID, Description: "retry", RepositoryIDs: []string{"repo"}, AssigneeMemberIDs: []string{"alice"}}
	if err := s.materializeWorkItems(context.Background(), requirement); err == nil {
		t.Fatal("first provider failure unexpectedly succeeded")
	}
	items := s.Store.ListWorkItems(requirement.ID)
	if len(items) != 1 || items[0].ProviderIssueID != "" || items[0].DeveloperAgentBindingID == "" {
		t.Fatalf("incomplete item=%+v", items)
	}
	if err := s.materializeWorkItems(context.Background(), requirement); err != nil {
		t.Fatalf("retry materialization: %v", err)
	}
	items = s.Store.ListWorkItems(requirement.ID)
	if calls != 2 || len(items) != 1 || items[0].ProviderIssueID != "issue-retry" || len(assignees) != 2 || assignees[0] != agentID || assignees[1] != agentID {
		t.Fatalf("calls=%d assignees=%v items=%+v", calls, assignees, items)
	}
}

func TestProviderDiagnosticsUsesIndependentStatesAndRedactsUpstream(t *testing.T) {
	const workspaceID = "00000000-0000-0000-0000-000000000001"
	const agentID = "00000000-0000-0000-0000-00000000000a"
	secret := "sensitive upstream body"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(secret))
	}))
	defer upstream.Close()
	config, err := provider.ParseAgentRouteConfig(`{"workspaces":{"` + workspaceID + `":{"members":{"alice":"` + agentID + `"}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	fs, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithRouting(store.NewMemory(), provider.NewMulticaProvider(upstream.URL, "token"), fs, bus, nil, provider.NewAgentRouteResolver(config, ""))
	response := request(t, s.Routes(), http.MethodGet, "/api/v1/provider/diagnostics", "", map[string]string{"X-Workspace-ID": workspaceID})
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, secret) || strings.Contains(body, agentID) || strings.Contains(body, "Bearer token") {
		t.Fatalf("diagnostics leaked sensitive data: %s", body)
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["configuration_state"] != "configured" || result["reachability_state"] != "unreachable" || result["authentication_state"] != "failed" || result["routing_state"] != "configured_unverified" {
		t.Fatalf("diagnostics=%v", result)
	}
	if int(result["member_route_count"].(float64)) != 1 {
		t.Fatalf("route count=%v", result["member_route_count"])
	}
	withoutWorkspace := request(t, s.Routes(), http.MethodGet, "/api/v1/provider/diagnostics", "", nil)
	if strings.Contains(withoutWorkspace.Body.String(), "member_route_count") || strings.Contains(withoutWorkspace.Body.String(), "role_route_count") {
		t.Fatalf("workspace route stats returned without workspace header: %s", withoutWorkspace.Body.String())
	}
}
