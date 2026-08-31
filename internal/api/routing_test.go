package api

import (
	"context"
	"encoding/json"
	"net/http"
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
	config, err := provider.ParseAgentRouteConfig(`{"workspaces":{"` + workspaceID + `":{"members":{"alice":"` + agentID + `"}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	fs, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	local := provider.NewLocalProvider("/usr/bin/true", nil, t.TempDir(), bus)
	s := NewWithRouting(store.NewMemory(), local, fs, bus, nil, provider.NewAgentRouteResolver(config, ""))
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

func TestProviderDiagnosticsReportsLocalExecutor(t *testing.T) {
	bus := events.NewBus()
	fs, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(store.NewMemory(), provider.NewLocalProvider("/usr/bin/true", nil, t.TempDir(), bus), fs, bus, nil)
	response := request(t, s.Routes(), http.MethodGet, "/api/v1/provider/diagnostics", "", nil)
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["provider"] != "local" || result["configuration_state"] != "configured" || result["reachability_state"] != "reachable" {
		t.Fatalf("diagnostics=%v", result)
	}
}
