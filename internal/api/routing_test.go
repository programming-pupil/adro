package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/adro-project/adro/internal/artifact"
	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/orchestration"
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

func TestMaterializationUsesNativeAgentAndSquadAssignments(t *testing.T) {
	for _, test := range []struct {
		name        string
		targetType  string
		targetID    string
		wantAgentID string
		wantSource  string
	}{
		{name: "agent", targetType: "agent", targetID: "agent-1", wantAgentID: "agent-1", wantSource: "requirement-agent"},
		{name: "squad", targetType: "squad", targetID: "squad-1", wantAgentID: "agent-1", wantSource: "requirement-squad"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bus := events.NewBus()
			fs, err := artifact.NewFileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			s := New(store.NewMemory(), provider.NewLocalProvider("/usr/bin/true", nil, t.TempDir(), bus), fs, bus, nil)
			agent := orchestration.AgentDefinition{
				ID: "agent-1", WorkspaceID: "workspace", Revision: 1, Name: "Builder", Status: orchestration.AgentActive,
				ExecutorBinding: orchestration.ExecutorBinding{ProviderID: "local", RuntimeID: "codex"},
				InputSchema:     orchestration.SchemaRef{ID: "input", Version: 1}, OutputSchema: orchestration.SchemaRef{ID: "output", Version: 1},
			}
			if err := s.Orchestration.SaveAgent(agent, 0); err != nil {
				t.Fatal(err)
			}
			if test.targetType == "squad" {
				graph := orchestration.WorkflowGraph{ID: "squad-graph", Version: 1, EntryNodeIDs: []string{"node-1"}, ExitNodeIDs: []string{"node-1"}, Nodes: []orchestration.WorkflowNode{{ID: "node-1", Kind: orchestration.NodeAgent, AgentRef: &orchestration.VersionedRef{ID: agent.ID, Revision: 1}}}}
				squad := orchestration.SquadDefinition{ID: "squad-1", WorkspaceID: "workspace", Name: "Delivery", Revision: 1, PublishedVersion: 1, Members: []orchestration.SquadMember{{ID: "leader", AgentID: agent.ID, Role: "leader", Leader: true}}, Graph: graph, Policy: orchestration.SquadPolicy{MaxNestingDepth: 1}, Status: orchestration.SquadPublished}
				if err := s.Orchestration.SaveSquad(squad, 0); err != nil {
					t.Fatal(err)
				}
			}
			requirement := domain.Requirement{ID: "req-1", Key: "REQ-1", WorkspaceID: "workspace", Description: "native assignment", RepositoryIDs: []string{"repo"}, AssigneeTargetType: test.targetType, AssigneeTargetID: test.targetID}
			if err := s.materializeWorkItems(context.Background(), requirement); err != nil {
				t.Fatal(err)
			}
			items := s.Store.ListWorkItems(requirement.ID)
			if len(items) != 1 || items[0].MemberID != test.wantAgentID || items[0].AgentRouteSource != test.wantSource || items[0].ProviderIssueID == "" {
				t.Fatalf("items=%+v", items)
			}
			if err := s.materializeWorkItems(context.Background(), requirement); err != nil {
				t.Fatal(err)
			}
			if got := len(s.Store.ListWorkItems(requirement.ID)); got != 1 {
				t.Fatalf("idempotent materialization created %d items", got)
			}
		})
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

func TestRuntimeSkillsRouteReturnsMetadataAndRejectsUnknownRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	directory := filepath.Join(home, ".codex", "skills", "release-review")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\nname: Release Review\ndescription: Review release evidence\n---\nsecret body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := testServer(t)
	response := request(t, s.Routes(), http.MethodGet, "/api/v1/runtimes/codex/skills", "", map[string]string{"X-Workspace-ID": "local"})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"Release Review"`) || strings.Contains(response.Body.String(), "secret body") {
		t.Fatalf("runtime Skills status=%d body=%s", response.Code, response.Body.String())
	}
	missing := request(t, s.Routes(), http.MethodGet, "/api/v1/runtimes/unknown/skills", "", map[string]string{"X-Workspace-ID": "local"})
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown runtime status=%d body=%s", missing.Code, missing.Body.String())
	}
	local := request(t, s.Routes(), http.MethodGet, "/api/v1/runtimes/local/skills", "", map[string]string{"X-Workspace-ID": "local"})
	if local.Code != http.StatusOK || !strings.Contains(local.Body.String(), `"runtime_id":"local"`) || !strings.Contains(local.Body.String(), `"items":[]`) {
		t.Fatalf("local runtime status=%d body=%s", local.Code, local.Body.String())
	}
}
