package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coreidentity "github.com/adro-project/adro/core/identity"
	"github.com/adro-project/adro/internal/artifact"
	"github.com/adro-project/adro/internal/audit"
	adroauth "github.com/adro-project/adro/internal/auth"
	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/provider"
	"github.com/adro-project/adro/internal/runner"
	"github.com/gorilla/websocket"
)

// Threat ID: TM-IDENT-001
func TestServiceCredentialRejectsScopeSpoofingAndBindsActor(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "required")
	t.Setenv("ADRO_ADMIN_PASSWORD", "")
	s := testServer(t)
	token := testServiceToken(t, s, coreidentity.ActorWorker, "worker-1", "tenant-1", "workspace-1")
	base := map[string]string{"Authorization": "Bearer " + token}
	for _, spoof := range []map[string]string{
		{"X-Tenant-ID": "tenant-2"},
		{"X-Workspace-ID": "workspace-2"},
	} {
		headers := map[string]string{"Authorization": base["Authorization"]}
		for key, value := range spoof {
			headers[key] = value
		}
		response := request(t, s.Routes(), http.MethodGet, "/api/v1/bugs", "", headers)
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "identity_scope_mismatch") {
			t.Fatalf("scope spoof headers=%v status=%d body=%s", spoof, response.Code, response.Body.String())
		}
	}
	headers := map[string]string{"Authorization": base["Authorization"], "X-Member-ID": "spoofed", "X-Agent-ID": "spoofed-agent"}
	created := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements", `{"workspace_id":"foreign","created_by":"spoofed","title":"Service identity","description":"verified actor owns the mutation","acceptance_criteria":["bound scope"],"assignee_member_ids":["member-1"]}`, headers)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var requirement domain.Requirement
	if err := json.Unmarshal(created.Body.Bytes(), &requirement); err != nil {
		t.Fatal(err)
	}
	if requirement.WorkspaceID != "workspace-1" || requirement.CreatedBy != "worker-1" {
		t.Fatalf("service identity was not authoritative: %+v", requirement)
	}

	sharedBody := `{"title":"Tenant idempotency","description":"keys are tenant scoped","acceptance_criteria":["no cross-tenant replay"],"assignee_member_ids":["member-1"]}`
	first := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements", sharedBody, map[string]string{"Authorization": "Bearer " + token, "Idempotency-Key": "same-key"})
	if first.Code != http.StatusCreated {
		t.Fatalf("first tenant mutation status=%d body=%s", first.Code, first.Body.String())
	}
	secondToken, _, err := s.ServiceCredentials.Issue(adroauth.ServiceTokenIssueRequest{Type: coreidentity.ActorWorker, ID: "worker-2", TenantID: "tenant-2", WorkspaceID: "workspace-1", Audience: adroauth.ServiceTokenAudienceAPI, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	second := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements", sharedBody, map[string]string{"Authorization": "Bearer " + secondToken, "Idempotency-Key": "same-key"})
	if second.Code != http.StatusCreated || second.Header().Get("Idempotency-Replayed") != "" {
		t.Fatalf("second tenant mutation status=%d replay=%q body=%s", second.Code, second.Header().Get("Idempotency-Replayed"), second.Body.String())
	}
	var firstRequirement, secondRequirement domain.Requirement
	if json.Unmarshal(first.Body.Bytes(), &firstRequirement) != nil || json.Unmarshal(second.Body.Bytes(), &secondRequirement) != nil || firstRequirement.ID == secondRequirement.ID {
		t.Fatalf("tenant idempotency was shared: first=%+v second=%+v", firstRequirement, secondRequirement)
	}
}

func TestInvalidBearerIsRejectedInOptionalAuthMode(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "optional")
	t.Setenv("ADRO_ADMIN_PASSWORD", "")
	s := testServer(t)
	if got := request(t, s.Routes(), http.MethodGet, "/api/v1/bugs", "", nil).Code; got != http.StatusOK {
		t.Fatalf("anonymous optional-auth request status=%d", got)
	}
	for _, path := range []string{"/api/v1/bugs", "/api/v1/auth/me"} {
		response := request(t, s.Routes(), http.MethodGet, path, "", map[string]string{"Authorization": "Bearer invalid"})
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("invalid bearer path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

// Threat ID: TM-IDENT-002
func TestRevokedAndExpiredServiceCredentialsAreRejected(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "required")
	t.Setenv("ADRO_ADMIN_PASSWORD", "")
	s := testServer(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	state, err := adroauth.GenerateServiceCredentialState("key-1", now)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := adroauth.NewServiceCredentialAuthority(state, func() time.Time { return now }, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	s.ServiceCredentials = authority
	issue := func(id string) (string, coreidentity.Actor) {
		t.Helper()
		token, actor, issueErr := authority.Issue(adroauth.ServiceTokenIssueRequest{Type: coreidentity.ActorService, ID: id, TenantID: "tenant", WorkspaceID: "workspace", Audience: adroauth.ServiceTokenAudienceAPI, TTL: time.Minute})
		if issueErr != nil {
			t.Fatal(issueErr)
		}
		return token, actor
	}
	revokedToken, revokedActor := issue("revoked-service")
	if err := authority.RevokeCredential(revokedActor.CredentialID); err != nil {
		t.Fatal(err)
	}
	if got := request(t, s.Routes(), http.MethodGet, "/api/v1/bugs", "", map[string]string{"Authorization": "Bearer " + revokedToken}).Code; got != http.StatusUnauthorized {
		t.Fatalf("revoked credential status=%d", got)
	}
	expiredToken, expiredActor := issue("expired-service")
	now = expiredActor.ExpiresAt
	if got := request(t, s.Routes(), http.MethodGet, "/api/v1/bugs", "", map[string]string{"Authorization": "Bearer " + expiredToken}).Code; got != http.StatusUnauthorized {
		t.Fatalf("expired credential status=%d", got)
	}
}

// Threat ID: TM-IDENT-002
func TestLegacyStaticAPITokenFailsClosedAtStartup(t *testing.T) {
	t.Setenv("ADRO_API_TOKEN", "legacy-static-secret")
	t.Setenv("ADRO_AUTH_MODE", "optional")
	s := testServer(t)
	response := request(t, s.Routes(), http.MethodGet, "/readyz", "", nil)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "state_load_failed") || strings.Contains(response.Body.String(), "legacy-static-secret") {
		t.Fatalf("legacy token readiness status=%d body=%s", response.Code, response.Body.String())
	}
}

// Threat ID: TM-IDENT-003
func TestAuditPreservesOriginalActorAndDelegationChain(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "required")
	t.Setenv("ADRO_ADMIN_PASSWORD", "")
	s := testServer(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	state, err := adroauth.GenerateServiceCredentialState("key-1", now)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := adroauth.NewServiceCredentialAuthority(state, func() time.Time { return now }, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	s.ServiceCredentials = authority
	delegation := []coreidentity.Transition{{
		From: coreidentity.ActorRef{Type: coreidentity.ActorHuman, ID: "human-owner"},
		Mode: coreidentity.TransitionDelegation, Reason: "execute approved runtime action", At: now,
	}}
	token, actor, err := authority.Issue(adroauth.ServiceTokenIssueRequest{Type: coreidentity.ActorAgent, ID: "agent-1", TenantID: "tenant-1", WorkspaceID: "workspace-1", Audience: adroauth.ServiceTokenAudienceAPI, TTL: time.Minute, Delegation: delegation})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements", `{"title":"Delegated mutation","description":"audit the chain","acceptance_criteria":["identity retained"],"assignee_member_ids":["member-1"]}`, map[string]string{"Authorization": "Bearer " + token})
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	events := s.Audit.List()
	if len(events) == 0 {
		t.Fatal("delegated mutation produced no audit event")
	}
	event := events[len(events)-1]
	identityEvidence, ok := event.Payload["identity"].(map[string]any)
	if !ok {
		t.Fatalf("identity evidence=%#v", event.Payload["identity"])
	}
	original, originalOK := identityEvidence["original"].(coreidentity.ActorRef)
	chain, chainOK := identityEvidence["delegation"].([]coreidentity.Transition)
	if event.ActorType != "agent" || event.ActorID != "agent-1" || identityEvidence["credential"] != actor.CredentialID || !originalOK || original.ID != "human-owner" || !chainOK || len(chain) != 1 {
		t.Fatalf("audit actor=%s/%s identity=%#v", event.ActorType, event.ActorID, identityEvidence)
	}
}

func TestInteractiveLoginMenuAuthorizationAndRevocation(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "required")
	t.Setenv("ADRO_ADMIN_USERNAME", "admin")
	t.Setenv("ADRO_ADMIN_PASSWORD", "AdminPass123!")
	t.Setenv("ADRO_AUTH_STATE_FILE", "")
	s := testServer(t)
	adminToken := loginToken(t, s, "admin", "AdminPass123!")
	create := request(t, s.Routes(), http.MethodPost, "/api/v1/users", `{"username":"delivery.dev","display_name":"Delivery Developer","password":"Developer123!","role":"member","status":"active","menu_ids":["delivery"]}`, bearer(adminToken))
	if create.Code != http.StatusCreated {
		t.Fatalf("create user status=%d body=%s", create.Code, create.Body.String())
	}
	var user struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	memberToken := loginToken(t, s, "delivery.dev", "Developer123!")
	if got := request(t, s.Routes(), http.MethodGet, "/api/v1/requirements", "", bearer(memberToken)).Code; got != http.StatusOK {
		t.Fatalf("allowed menu status=%d", got)
	}
	if got := request(t, s.Routes(), http.MethodGet, "/api/v1/bugs", "", bearer(memberToken)).Code; got != http.StatusOK {
		t.Fatalf("unified bug menu status=%d", got)
	}
	denied := request(t, s.Routes(), http.MethodGet, "/api/v1/repositories", "", bearer(memberToken))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied menu status=%d body=%s", denied.Code, denied.Body.String())
	}
	disable := request(t, s.Routes(), http.MethodPatch, "/api/v1/users/"+user.ID, `{"status":"disabled"}`, bearer(adminToken))
	if disable.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", disable.Code, disable.Body.String())
	}
	if got := request(t, s.Routes(), http.MethodGet, "/api/v1/requirements", "", bearer(memberToken)).Code; got != http.StatusUnauthorized {
		t.Fatalf("revoked session status=%d", got)
	}
}

func TestInteractiveIdentityCannotSpoofWorkspaceOrActor(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "required")
	t.Setenv("ADRO_ADMIN_USERNAME", "admin")
	t.Setenv("ADRO_ADMIN_PASSWORD", "AdminPass123!")
	t.Setenv("ADRO_AUTH_STATE_FILE", "")
	s := testServer(t)
	adminToken := loginToken(t, s, "admin", "AdminPass123!")
	admin := s.Auth.ListUsers("local")[0]

	headers := bearer(adminToken)
	headers["X-Workspace-ID"] = "other-workspace"
	headers["X-Member-ID"] = "spoofed-member"
	created := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements", `{"workspace_id":"body-workspace","created_by":"body-member","title":"Bound identity","description":"must use the authenticated identity","acceptance_criteria":["workspace and actor are server controlled"],"assignee_member_ids":["member-1"],"repository_ids":["repo-1"]}`, headers)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var requirement domain.Requirement
	if err := json.Unmarshal(created.Body.Bytes(), &requirement); err != nil {
		t.Fatal(err)
	}
	if requirement.WorkspaceID != "local" || requirement.CreatedBy != admin.ID {
		t.Fatalf("interactive identity was not authoritative: %+v", requirement)
	}

	foreign, err := s.Store.CreateRequirement(domain.Requirement{WorkspaceID: "foreign", Title: "Foreign", Description: "must stay hidden", AcceptanceCriteria: []string{"not visible"}, AssigneeMemberIDs: []string{"member-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := request(t, s.Routes(), http.MethodGet, "/api/v1/requirements/"+foreign.ID, "", bearer(adminToken)); got.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace requirement status=%d body=%s", got.Code, got.Body.String())
	}
}

func TestAuthenticatedCommentEditRevisionRecordsAuthenticatedActor(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "required")
	t.Setenv("ADRO_ADMIN_USERNAME", "admin")
	t.Setenv("ADRO_ADMIN_PASSWORD", "AdminPass123!")
	t.Setenv("ADRO_AUTH_STATE_FILE", "")
	s := testServer(t)
	token := loginToken(t, s, "admin", "AdminPass123!")
	admin := s.Auth.ListUsers("local")[0]
	requirement, err := s.Store.CreateRequirement(domain.Requirement{
		WorkspaceID: "local", Title: "Authenticated comment revision", Description: "record the real editor",
		AcceptanceCriteria: []string{"revision actor is authenticated"}, AssigneeMemberIDs: []string{admin.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	created := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+requirement.ID+"/comments", `{"content":"original"}`, bearer(token))
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var payload struct {
		Comment domain.Comment `json:"comment"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	headers := bearer(token)
	headers["X-Member-ID"] = "spoofed-editor"
	edited := request(t, s.Routes(), http.MethodPatch, "/api/v1/comments/"+payload.Comment.ID, `{"content":"edited","expected_revision":1}`, headers)
	if edited.Code != http.StatusOK {
		t.Fatalf("edit status=%d body=%s", edited.Code, edited.Body.String())
	}
	revisions := request(t, s.Routes(), http.MethodGet, "/api/v1/comments/"+payload.Comment.ID+"/revisions", "", bearer(token))
	var history struct {
		Items []domain.CommentRevision `json:"items"`
	}
	if revisions.Code != http.StatusOK || json.Unmarshal(revisions.Body.Bytes(), &history) != nil || len(history.Items) != 2 {
		t.Fatalf("revisions status=%d body=%s", revisions.Code, revisions.Body.String())
	}
	if history.Items[1].EditorID != admin.ID || history.Items[1].EditorType != "human" {
		t.Fatalf("revision editor was not authenticated identity: %+v", history.Items[1])
	}
}

func TestRequirementRejectsForeignRegisteredRepository(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "optional")
	s := testServer(t)
	foreign, err := s.Store.UpsertRepository(domain.Repository{
		WorkspaceID: "foreign-workspace", CanonicalName: "private-repository", CloneURL: "file:///srv/private-repository",
		Metadata: map[string]any{"local_path": "/srv/private-repository"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements", `{"workspace_id":"local","title":"cross-workspace","description":"must be rejected","acceptance_criteria":["deny foreign repository"],"assignee_member_ids":["member-1"],"repository_ids":["`+foreign.ID+`"]}`, nil)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "invalid_repository_relation") {
		t.Fatalf("foreign repository status=%d body=%s", response.Code, response.Body.String())
	}
	if items, _ := s.Store.ListRequirements("local", "", "", 10); len(items) != 0 {
		t.Fatalf("foreign repository request created a requirement: %+v", items)
	}
}

func TestInteractiveIdentityCannotSpoofArtifactTenant(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "required")
	t.Setenv("ADRO_ADMIN_USERNAME", "admin")
	t.Setenv("ADRO_ADMIN_PASSWORD", "AdminPass123!")
	t.Setenv("ADRO_AUTH_STATE_FILE", "")
	s := testServer(t)
	key := artifact.Key{TenantID: "tenant-secret", ArtifactID: "tenant-proof", Version: 1}
	if _, err := s.Artifacts.Put(context.Background(), key, strings.NewReader("foreign-data"), artifact.PutOptions{MediaType: "text/plain", Immutable: true}); err != nil {
		t.Fatal(err)
	}
	token := loginToken(t, s, "admin", "AdminPass123!")
	headers := bearer(token)
	headers["X-Tenant-ID"] = "tenant-secret"
	response := request(t, s.Routes(), http.MethodGet, "/api/v1/artifacts/tenant-proof/versions/1/content", "", headers)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant artifact status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRunnerRoutesEnforceWorkspaceAndTenantOwnership(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "required")
	t.Setenv("ADRO_ADMIN_USERNAME", "admin")
	t.Setenv("ADRO_ADMIN_PASSWORD", "AdminPass123!")
	t.Setenv("ADRO_AUTH_STATE_FILE", "")
	s := testServer(t)
	machineToken := testServiceToken(t, s, coreidentity.ActorWorker, "runner-worker", "workspace-a", "workspace-a")
	root := t.TempDir()
	registered := request(t, s.Routes(), http.MethodPost, "/api/v1/runners", `{"name":"owned","provider":"test","version":"1","workspace_root":"`+root+`","concurrency":1}`, map[string]string{"Authorization": "Bearer " + machineToken, "X-Workspace-ID": "workspace-a"})
	if registered.Code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", registered.Code, registered.Body.String())
	}
	var item runner.Runner
	if err := json.Unmarshal(registered.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.WorkspaceID != "workspace-a" || item.TenantID != "workspace-a" {
		t.Fatalf("runner ownership not persisted: %+v", item)
	}
	token := loginToken(t, s, "admin", "AdminPass123!")
	foreign := bearer(token)
	foreign["X-Workspace-ID"] = "workspace-b"
	for _, operation := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/runners", ""},
		{http.MethodGet, "/api/v1/runners/" + item.ID, ""},
		{http.MethodPost, "/api/v1/runners/" + item.ID + "/heartbeat", `{"active_runs":0}`},
		{http.MethodPost, "/api/v1/runners/" + item.ID + "/drain", ""},
		{http.MethodPost, "/api/v1/runners/" + item.ID + "/quarantine", ""},
		{http.MethodPost, "/api/v1/runners/" + item.ID + "/execute", `{"command":["/bin/echo","cross-tenant"]}`},
	} {
		response := request(t, s.Routes(), operation.method, operation.path, operation.body, foreign)
		if response.Code != http.StatusNotFound {
			t.Fatalf("foreign runner operation %s %s status=%d body=%s", operation.method, operation.path, response.Code, response.Body.String())
		}
	}
}

func TestInteractiveWorkspaceIsolationForStreamsAndResources(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "required")
	t.Setenv("ADRO_ADMIN_USERNAME", "admin")
	t.Setenv("ADRO_ADMIN_PASSWORD", "AdminPass123!")
	t.Setenv("ADRO_AUTH_STATE_FILE", "")
	s := testServer(t)
	token := loginToken(t, s, "admin", "AdminPass123!")
	headers := bearer(token)

	foreignRepo, err := s.Store.UpsertRepository(domain.Repository{WorkspaceID: "foreign-workspace", CanonicalName: "foreign-repo", CloneURL: "https://example.test/foreign"})
	if err != nil {
		t.Fatal(err)
	}
	foreignTeam, err := s.Store.UpsertTeamWorkspace(domain.TeamWorkspace{WorkspaceID: "foreign-workspace", Name: "foreign-team"})
	if err != nil {
		t.Fatal(err)
	}
	if got := request(t, s.Routes(), http.MethodGet, "/api/v1/repositories/"+foreignRepo.ID, "", headers).Code; got != http.StatusNotFound {
		t.Fatalf("foreign repository detail status=%d", got)
	}
	if got := request(t, s.Routes(), http.MethodGet, "/api/v1/team-workspaces/"+foreignTeam.ID, "", headers).Code; got != http.StatusNotFound {
		t.Fatalf("foreign team detail status=%d", got)
	}

	createdRepo := request(t, s.Routes(), http.MethodPost, "/api/v1/repositories", `{"workspace_id":"foreign-workspace","canonical_name":"local-repo","clone_url":"https://example.test/local"}`, headers)
	if createdRepo.Code != http.StatusCreated {
		t.Fatalf("repository create status=%d body=%s", createdRepo.Code, createdRepo.Body.String())
	}
	var repo domain.Repository
	if err := json.Unmarshal(createdRepo.Body.Bytes(), &repo); err != nil {
		t.Fatal(err)
	}
	if repo.WorkspaceID != "local" {
		t.Fatalf("repository escaped authenticated workspace: %+v", repo)
	}
	createdTeam := request(t, s.Routes(), http.MethodPost, "/api/v1/team-workspaces", `{"workspace_id":"foreign-workspace","name":"local-team"}`, headers)
	if createdTeam.Code != http.StatusCreated {
		t.Fatalf("team create status=%d body=%s", createdTeam.Code, createdTeam.Body.String())
	}
	var team domain.TeamWorkspace
	if err := json.Unmarshal(createdTeam.Body.Bytes(), &team); err != nil {
		t.Fatal(err)
	}
	if team.WorkspaceID != "local" {
		t.Fatalf("team workspace escaped authenticated workspace: %+v", team)
	}

	if err := s.Events.Publish(context.Background(), events.New("test.foreign.v1", "requirement", "foreign-event", "local", "foreign-workspace", 1, nil)); err != nil {
		t.Fatal(err)
	}
	stream := request(t, s.Routes(), http.MethodGet, "/api/v1/streams/workspaces/foreign-workspace", "", headers)
	if stream.Code != http.StatusNotFound {
		t.Fatalf("foreign stream status=%d body=%s", stream.Code, stream.Body.String())
	}

	server := httptest.NewServer(s.Routes())
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/streams/workspaces/foreign-workspace"
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Authorization": []string{"Bearer " + token}})
	if err == nil {
		conn.Close()
		t.Fatal("foreign WebSocket unexpectedly upgraded")
	}
	if response == nil || response.StatusCode != http.StatusNotFound {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("foreign WebSocket status=%d err=%v", status, err)
	}
}

func TestInteractiveWorkspaceIsolationForRunsEvidenceAndAudit(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "required")
	t.Setenv("ADRO_ADMIN_USERNAME", "admin")
	t.Setenv("ADRO_ADMIN_PASSWORD", "AdminPass123!")
	t.Setenv("ADRO_AUTH_STATE_FILE", "")
	s := testServer(t)
	token := loginToken(t, s, "admin", "AdminPass123!")
	headers := bearer(token)

	foreignRequirement, err := s.Store.CreateRequirement(domain.Requirement{
		ID: "foreign-requirement", WorkspaceID: "foreign-workspace", Title: "Foreign requirement",
		Description: "must stay hidden", AcceptanceCriteria: []string{"never visible"},
		AssigneeMemberIDs: []string{"foreign-member"}, RepositoryIDs: []string{"foreign-repo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	localRequirement, err := s.Store.CreateRequirement(domain.Requirement{
		ID: "local-requirement", WorkspaceID: "local", Title: "Local requirement",
		Description: "must remain visible", AcceptanceCriteria: []string{"visible"},
		AssigneeMemberIDs: []string{"local-member"}, RepositoryIDs: []string{"local-repo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	foreignItem, _, err := s.Store.CreateWorkItemIfAbsent(domain.WorkItem{ID: "foreign-work-item", RequirementID: foreignRequirement.ID, RepositoryID: "foreign-repo", MemberID: "foreign-member"})
	if err != nil {
		t.Fatal(err)
	}
	localItem, _, err := s.Store.CreateWorkItemIfAbsent(domain.WorkItem{ID: "local-work-item", RequirementID: localRequirement.ID, RepositoryID: "local-repo", MemberID: "local-member"})
	if err != nil {
		t.Fatal(err)
	}
	foreignRun, err := s.Provider.StartRun(context.Background(), provider.StartRunCommand{WorkItemID: foreignItem.ID, Input: "foreign secret"})
	if err != nil {
		t.Fatal(err)
	}
	localRun, err := s.Provider.StartRun(context.Background(), provider.StartRunCommand{WorkItemID: localItem.ID, Input: "local work"})
	if err != nil {
		t.Fatal(err)
	}

	operations := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/runs/" + foreignRun.ID, ""},
		{http.MethodGet, "/api/v1/runs/" + foreignRun.ID + "/events", ""},
		{http.MethodGet, "/api/v1/runs/" + foreignRun.ID + "/usage", ""},
		{http.MethodPost, "/api/v1/runs/" + foreignRun.ID + "/cancel", ""},
		{http.MethodPost, "/api/v1/runs/" + foreignRun.ID + "/messages", `{"input":"unauthorized"}`},
	}
	for _, operation := range operations {
		response := request(t, s.Routes(), operation.method, operation.path, operation.body, headers)
		if response.Code != http.StatusNotFound {
			t.Fatalf("foreign run operation %s %s status=%d body=%s", operation.method, operation.path, response.Code, response.Body.String())
		}
	}
	if snapshot, err := s.Provider.GetRun(context.Background(), foreignRun.ID); err != nil || snapshot.Status == "cancelled" {
		t.Fatalf("foreign run was mutated by unauthorized operation: snapshot=%+v err=%v", snapshot, err)
	}
	if response := request(t, s.Routes(), http.MethodGet, "/api/v1/runs/"+localRun.ID, "", headers); response.Code != http.StatusOK {
		t.Fatalf("local run status=%d body=%s", response.Code, response.Body.String())
	}

	if err := s.Store.SaveEvidence(domain.EvidenceBundle{ID: "foreign-evidence", WorkspaceID: "foreign-workspace", WorkItemID: foreignItem.ID, Kind: "test", Status: "created", Summary: map[string]any{"secret": "foreign"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SaveEvidence(domain.EvidenceBundle{ID: "local-evidence", WorkspaceID: "local", WorkItemID: localItem.ID, Kind: "test", Status: "created", Summary: map[string]any{"result": "local"}}); err != nil {
		t.Fatal(err)
	}
	evidenceResponse := request(t, s.Routes(), http.MethodGet, "/api/v1/evidence", "", headers)
	if evidenceResponse.Code != http.StatusOK {
		t.Fatalf("evidence status=%d body=%s", evidenceResponse.Code, evidenceResponse.Body.String())
	}
	var evidencePage struct {
		Items []domain.EvidenceBundle `json:"items"`
	}
	if err := json.Unmarshal(evidenceResponse.Body.Bytes(), &evidencePage); err != nil {
		t.Fatal(err)
	}
	if len(evidencePage.Items) != 1 || evidencePage.Items[0].ID != "local-evidence" {
		t.Fatalf("workspace evidence filter returned %+v", evidencePage.Items)
	}

	for _, event := range []audit.Event{
		{TenantID: "local", WorkspaceID: "foreign-workspace", ActorID: "foreign", Action: "foreign.secret"},
		{TenantID: "local", WorkspaceID: "local", ActorID: "local", Action: "local.action"},
	} {
		if _, err := s.Audit.Append(event); err != nil {
			t.Fatal(err)
		}
	}
	auditResponse := request(t, s.Routes(), http.MethodGet, "/api/v1/audit", "", headers)
	if auditResponse.Code != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", auditResponse.Code, auditResponse.Body.String())
	}
	var auditPage struct {
		Items []audit.Event `json:"items"`
	}
	if err := json.Unmarshal(auditResponse.Body.Bytes(), &auditPage); err != nil {
		t.Fatal(err)
	}
	if len(auditPage.Items) != 1 || auditPage.Items[0].Action != "local.action" {
		t.Fatalf("workspace audit filter returned %+v", auditPage.Items)
	}
}

func TestInteractiveWorkspaceIsolationForSyntheticCommentRuns(t *testing.T) {
	s := testServer(t)
	requirement, err := s.Store.CreateRequirement(domain.Requirement{
		WorkspaceID: "w1", Title: "Synthetic comment run", Description: "comment follow-up ownership",
		AcceptanceCriteria: []string{"run remains visible to its workspace"}, AssigneeMemberIDs: []string{"member"},
	})
	if err != nil {
		t.Fatal(err)
	}
	const syntheticWorkItemID = "comment-follow-up"
	binding, err := s.Provider.StartRun(context.Background(), provider.StartRunCommand{WorkItemID: syntheticWorkItemID, Input: "follow up"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SaveProvenance(domain.Provenance{
		WorkItemID: syntheticWorkItemID, RequirementID: requirement.ID, ProviderTaskID: binding.ID,
	}); err != nil {
		t.Fatal(err)
	}

	local := request(t, s.Routes(), http.MethodGet, "/api/v1/runs/"+binding.ID, "", map[string]string{"X-Workspace-ID": "w1"})
	if local.Code != http.StatusOK {
		t.Fatalf("synthetic comment run status=%d body=%s", local.Code, local.Body.String())
	}
	foreign := request(t, s.Routes(), http.MethodGet, "/api/v1/runs/"+binding.ID, "", map[string]string{"X-Workspace-ID": "w2"})
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("synthetic comment run escaped workspace boundary: status=%d body=%s", foreign.Code, foreign.Body.String())
	}
}

func TestRequirementAndBugAttachmentsAreEntityLinked(t *testing.T) {
	s := testServer(t)
	requirementResponse := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements", `{"workspace_id":"local","title":"Attachment requirement","description":"verify entity files","acceptance_criteria":["file is listed"],"assignee_member_ids":["member-1"],"repository_ids":["repo-1"]}`, nil)
	if requirementResponse.Code != http.StatusCreated {
		t.Fatalf("requirement status=%d body=%s", requirementResponse.Code, requirementResponse.Body.String())
	}
	var requirement struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(requirementResponse.Body.Bytes(), &requirement)
	requirementAttachment := multipartRequest(t, s.Routes(), "/api/v1/attachments", map[string]string{"owner_type": "requirement", "owner_id": requirement.ID}, "brief.txt", []byte("requirement evidence"))
	if requirementAttachment.Code != http.StatusCreated {
		t.Fatalf("requirement attachment status=%d body=%s", requirementAttachment.Code, requirementAttachment.Body.String())
	}
	list := request(t, s.Routes(), http.MethodGet, "/api/v1/attachments?owner_type=requirement&owner_id="+requirement.ID, "", nil)
	var attachmentPage struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &attachmentPage)
	if list.Code != http.StatusOK || len(attachmentPage.Items) != 1 || attachmentPage.Items[0]["artifact_uri"] == "" {
		t.Fatalf("attachment list status=%d body=%s", list.Code, list.Body.String())
	}
	bugResponse := request(t, s.Routes(), http.MethodPost, "/api/v1/bugs", `{"workspace_id":"local","title":"Linked regression","requirement_id":"`+requirement.ID+`","repository_id":"ignored-repo","assignee_member_id":"ignored-member","steps_to_reproduce":"run acceptance","expected":"pass","actual":"fail"}`, nil)
	if bugResponse.Code != http.StatusCreated {
		t.Fatalf("bug status=%d body=%s", bugResponse.Code, bugResponse.Body.String())
	}
	var bug struct {
		ID               string `json:"id"`
		RequirementID    string `json:"requirement_id"`
		AssigneeMemberID string `json:"assignee_member_id"`
	}
	_ = json.Unmarshal(bugResponse.Body.Bytes(), &bug)
	if bug.RequirementID != requirement.ID || bug.AssigneeMemberID != "member-1" {
		t.Fatalf("bug relation=%+v", bug)
	}
	bugAttachment := multipartRequest(t, s.Routes(), "/api/v1/attachments", map[string]string{"owner_type": "bug", "owner_id": bug.ID}, "trace.log", []byte("stack trace"))
	if bugAttachment.Code != http.StatusCreated {
		t.Fatalf("bug attachment status=%d body=%s", bugAttachment.Code, bugAttachment.Body.String())
	}
}

func loginToken(t *testing.T, s *Server, username, password string) string {
	t.Helper()
	response := request(t, s.Routes(), http.MethodPost, "/api/v1/auth/login", `{"username":"`+username+`","password":"`+password+`"}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", response.Code, response.Body.String())
	}
	var session struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	return session.Token
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

func multipartRequest(t *testing.T, handler http.Handler, path string, fields map[string]string, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}
