package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/orchestration"
)

func TestExecutionPlanGraphValidationRoute(t *testing.T) {
	s := testServer(t)
	body := `{"graph":{"id":"g","version":1,"entry_node_ids":["a"],"exit_node_ids":["a"],"nodes":[{"id":"a","kind":"gate"}],"edges":[]}}`
	r := request(t, s.Routes(), http.MethodPost, "/api/v1/execution-plans/validate", body, map[string]string{"X-Workspace-ID": "w1"})
	if r.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestQuickSquadPersistsIncompleteDraftAndReturnsValidationErrors(t *testing.T) {
	s := testServer(t)
	requirement, err := s.Store.CreateRequirement(domain.Requirement{WorkspaceID: "w1", Title: "quick draft", Description: "draft", AcceptanceCriteria: []string{"works"}, AssigneeMemberIDs: []string{"member"}, RepositoryIDs: []string{"repo"}})
	if err != nil {
		t.Fatal(err)
	}
	r := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+requirement.ID+"/execution-plan/quick-squad", `{"name":"incomplete"}`, map[string]string{"X-Workspace-ID": "w1"})
	if r.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
	var response struct {
		Valid      bool                          `json:"valid"`
		Persisted  bool                          `json:"persisted"`
		Validation string                        `json:"validation_error"`
		Squad      orchestration.SquadDefinition `json:"squad"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Valid || !response.Persisted || response.Validation == "" || response.Squad.Status != orchestration.SquadDraft {
		t.Fatalf("incomplete draft was not persisted with diagnostics: %+v", response)
	}
	if got := s.Orchestration.ListSquads("w1", orchestration.SquadDraft); len(got) != 1 {
		t.Fatalf("expected one persisted draft, got %d", len(got))
	}
}

func TestMentionPreviewRoute(t *testing.T) {
	s := testServer(t)
	if _, err := s.Store.CreateRequirement(domain.Requirement{ID: "req-0001", WorkspaceID: "w1", Title: "preview", Description: "preview mentions", AcceptanceCriteria: []string{"works"}, AssigneeMemberIDs: []string{"member"}, RepositoryIDs: []string{"repo"}}); err != nil {
		t.Fatal(err)
	}
	body := `{"comment_id":"c1","revision":1,"content":"[@agent](mention://agent/550e8400-e29b-41d4-a716-446655440000)"}`
	r := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/req-0001/comments/trigger-preview", body, map[string]string{"X-Workspace-ID": "w1"})
	if r.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestCommentBodyCannotSpoofAuthorIdentity(t *testing.T) {
	s := testServer(t)
	requirement, err := s.Store.CreateRequirement(domain.Requirement{WorkspaceID: "w1", Title: "identity", Description: "identity", AcceptanceCriteria: []string{"works"}, AssigneeMemberIDs: []string{"member"}, RepositoryIDs: []string{"repo"}})
	if err != nil {
		t.Fatal(err)
	}
	r := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+requirement.ID+"/comments", `{"author_id":"spoofed","author_type":"agent","content":"body identity must not be trusted"}`, map[string]string{"X-Workspace-ID": "w1"})
	if r.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
	var payload struct {
		Comment domain.Comment `json:"comment"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Comment.AuthorID == "spoofed" || payload.Comment.AuthorType == "agent" {
		t.Fatalf("comment accepted body identity: %+v", payload.Comment)
	}
}

func TestStructuredSquadMentionPersistsIndependentSquadReceipt(t *testing.T) {
	s := testServer(t)
	requirement, err := s.Store.CreateRequirement(domain.Requirement{WorkspaceID: "w1", Title: "squad mention", Description: "route a squad comment", AcceptanceCriteria: []string{"works"}, AssigneeMemberIDs: []string{"member"}, RepositoryIDs: []string{"repo"}})
	if err != nil {
		t.Fatal(err)
	}
	agentID := "550e8400-e29b-41d4-a716-446655440000"
	if err := s.Orchestration.SaveAgent(orchestration.AgentDefinition{ID: agentID, WorkspaceID: "w1", Revision: 1, Name: "leader", Status: orchestration.AgentActive, ExecutorBinding: orchestration.ExecutorBinding{ProviderID: "mock"}, InputSchema: orchestration.SchemaRef{ID: "input"}, OutputSchema: orchestration.SchemaRef{ID: "output"}}, 0); err != nil {
		t.Fatal(err)
	}
	squadID := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	squad := orchestration.SquadDefinition{ID: squadID, WorkspaceID: "w1", Name: "delivery", Revision: 1, PublishedVersion: 1, Status: orchestration.SquadPublished, Members: []orchestration.SquadMember{{ID: "leader-member", AgentID: agentID, Role: "leader", Leader: true}}, Graph: orchestration.WorkflowGraph{ID: "squad-graph", Version: 1, EntryNodeIDs: []string{"agent"}, ExitNodeIDs: []string{"agent"}, Nodes: []orchestration.WorkflowNode{{ID: "agent", Kind: orchestration.NodeAgent, AgentRef: &orchestration.VersionedRef{ID: agentID, Revision: 1}}}}}
	if err := s.Orchestration.SaveSquad(squad, 0); err != nil {
		t.Fatal(err)
	}
	content := "请交付 [@交付小队](mention://squad/" + squadID + ")"
	r := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+requirement.ID+"/comments", `{"content":"`+content+`"}`, map[string]string{"X-Workspace-ID": "w1", "X-Member-ID": "reviewer"})
	if r.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
	var raw struct {
		Comment struct {
			ID string `json:"id"`
		} `json:"comment"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	followUps := s.Store.ListCommentFollowUps(raw.Comment.ID)
	if len(followUps) != 1 || followUps[0].DispatchTargetType != "squad" || followUps[0].DispatchTargetID != squadID || !strings.Contains(followUps[0].DedupeKey, squadID) {
		t.Fatalf("follow-ups=%+v body=%s", followUps, r.Body.String())
	}
}

func TestExecutionPlanBodyIdempotencyReturnsOriginalPlan(t *testing.T) {
	s := testServer(t)
	requirement, err := s.Store.CreateRequirement(domain.Requirement{WorkspaceID: "w1", Title: "idempotent plan", Description: "create exactly one plan", AcceptanceCriteria: []string{"works"}, AssigneeMemberIDs: []string{"member"}, RepositoryIDs: []string{"repo"}})
	if err != nil {
		t.Fatal(err)
	}
	agentID := "550e8400-e29b-41d4-a716-446655440000"
	if err := s.Orchestration.SaveAgent(orchestration.AgentDefinition{ID: agentID, WorkspaceID: "w1", Revision: 1, Name: "planner", Status: orchestration.AgentActive, ExecutorBinding: orchestration.ExecutorBinding{ProviderID: "mock"}, InputSchema: orchestration.SchemaRef{ID: "input"}, OutputSchema: orchestration.SchemaRef{ID: "output"}}, 0); err != nil {
		t.Fatal(err)
	}
	body := `{"agent_id":"` + agentID + `","idempotency_key":"plan-create-1"}`
	first := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+requirement.ID+"/execution-plan", body, map[string]string{"X-Workspace-ID": "w1"})
	if first.Code != http.StatusCreated {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	var firstPlan orchestration.RequirementExecutionPlan
	if err := json.Unmarshal(first.Body.Bytes(), &firstPlan); err != nil {
		t.Fatal(err)
	}
	second := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+requirement.ID+"/execution-plan", body, map[string]string{"X-Workspace-ID": "w1"})
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	var secondPlan orchestration.RequirementExecutionPlan
	if err := json.Unmarshal(second.Body.Bytes(), &secondPlan); err != nil {
		t.Fatal(err)
	}
	if secondPlan.ID != firstPlan.ID || secondPlan.PlanHash != firstPlan.PlanHash {
		t.Fatalf("idempotent retry returned a different plan: first=%+v second=%+v", firstPlan, secondPlan)
	}
	if got := s.Orchestration.ListPlans("w1"); len(got) != 1 {
		t.Fatalf("expected one persisted plan, got %d", len(got))
	}
	conflictBody := `{"agent_id":"` + agentID + `","graph":{"id":"different","version":1,"entry_node_ids":["a"],"exit_node_ids":["a"],"nodes":[{"id":"a","kind":"gate"}]},"idempotency_key":"plan-create-1"}`
	conflict := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+requirement.ID+"/execution-plan", conflictBody, map[string]string{"X-Workspace-ID": "w1"})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflicting status=%d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestExecutionPlanInvocationDoesNotRequireManagementPermission(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "required")
	t.Setenv("ADRO_ADMIN_USERNAME", "admin")
	t.Setenv("ADRO_ADMIN_PASSWORD", "AdminPass123!")
	t.Setenv("ADRO_AUTH_STATE_FILE", "")
	s := testServer(t)
	adminToken := loginToken(t, s, "admin", "AdminPass123!")
	create := request(t, s.Routes(), http.MethodPost, "/api/v1/users", `{"username":"executor","display_name":"Executor","password":"Executor123!","role":"member","status":"active","menu_ids":["requirements","executions"]}`, bearer(adminToken))
	if create.Code != http.StatusCreated {
		t.Fatalf("create member status=%d body=%s", create.Code, create.Body.String())
	}
	memberToken := loginToken(t, s, "executor", "Executor123!")
	requirement, err := s.Store.CreateRequirement(domain.Requirement{WorkspaceID: "local", Title: "invoke existing agent", Description: "run an approved agent", AcceptanceCriteria: []string{"plan created"}, AssigneeMemberIDs: []string{"executor"}})
	if err != nil {
		t.Fatal(err)
	}
	agentID := "550e8400-e29b-41d4-a716-446655440000"
	if err := s.Orchestration.SaveAgent(orchestration.AgentDefinition{
		ID: agentID, WorkspaceID: "local", Revision: 1, Name: "approved-agent", Status: orchestration.AgentActive,
		ExecutorBinding: orchestration.ExecutorBinding{ProviderID: "mock"},
		InputSchema:     orchestration.SchemaRef{ID: "input"}, OutputSchema: orchestration.SchemaRef{ID: "output"},
	}, 0); err != nil {
		t.Fatal(err)
	}
	plan := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+requirement.ID+"/execution-plan", `{"agent_id":"`+agentID+`"}`, bearer(memberToken))
	if plan.Code != http.StatusCreated {
		t.Fatalf("member invocation status=%d body=%s", plan.Code, plan.Body.String())
	}
	quick := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+requirement.ID+"/execution-plan/quick-squad", `{"name":"should be denied"}`, bearer(memberToken))
	if quick.Code != http.StatusForbidden {
		t.Fatalf("member quick-squad status=%d body=%s", quick.Code, quick.Body.String())
	}
}
