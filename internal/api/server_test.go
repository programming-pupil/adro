package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adro-project/adro/internal/artifact"
	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/provider"
	"github.com/adro-project/adro/internal/store"
	"github.com/gorilla/websocket"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	bus := events.NewBus()
	fs, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(store.NewMemory(), provider.NewMockProvider(bus), fs, bus, nil)
}

func request(t *testing.T, h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestRequirementCreationIsIdempotentAndStartsWorkItems(t *testing.T) {
	s := testServer(t)
	body := `{"workspace_id":"w1","title":"Invite API","description":"add invite","acceptance_criteria":["returns 200"],"assignee_member_ids":["alice","bob"],"repository_ids":["provider","caller"]}`
	headers := map[string]string{"Idempotency-Key": "same", "X-Tenant-ID": "tenant-1"}
	first := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements", body, headers)
	if first.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", first.Code, first.Body.String())
	}
	var req map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &req); err != nil {
		t.Fatal(err)
	}
	id := req["id"].(string)
	second := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements", body, headers)
	if second.Code != first.Code || second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("idempotent status=%d replay=%q", second.Code, second.Header().Get("Idempotency-Replayed"))
	}
	start := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+id+"/start", "", headers)
	if start.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", start.Code, start.Body.String())
	}
	items := request(t, s.Routes(), http.MethodGet, "/api/v1/requirements/"+id+"/work-items", "", nil)
	if items.Code != http.StatusOK {
		t.Fatal(items.Code)
	}
	var list map[string]any
	_ = json.Unmarshal(items.Body.Bytes(), &list)
	if got := len(list["items"].([]any)); got != 2 {
		t.Fatalf("work items=%d", got)
	}
	report := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+id+"/impact-reports", `{"candidate_repositories":[{"repository_id":"provider","relation":"api","confidence":0.95,"recommended_action":"must_change","evidence_refs":["scan:1"]}]}`, nil)
	if report.Code != http.StatusCreated {
		t.Fatalf("impact report status=%d body=%s", report.Code, report.Body.String())
	}
	confirm := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+id+"/impact-reports/1/confirm", `{"repository_ids":["provider"]}`, nil)
	if confirm.Code != http.StatusOK {
		t.Fatalf("impact confirmation status=%d body=%s", confirm.Code, confirm.Body.String())
	}
	var itemPage struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(items.Body.Bytes(), &itemPage); err != nil || len(itemPage.Items) != 2 {
		t.Fatalf("work item page: %v", err)
	}
	run := request(t, s.Routes(), http.MethodPost, "/api/v1/work-items/"+itemPage.Items[0].ID+"/run", `{"input":"design the change"}`, nil)
	if run.Code != http.StatusAccepted {
		t.Fatalf("run status=%d body=%s", run.Code, run.Body.String())
	}
}

func TestGenericMutationIdempotencyReplaysResponse(t *testing.T) {
	s := testServer(t)
	body := `{"workspace_id":"w1","canonical_name":"repo-one","clone_url":"https://example.test/repo-one.git","provider":"git","default_branch":"main"}`
	headers := map[string]string{"Idempotency-Key": "repository-create-1", "X-Workspace-ID": "w1"}
	first := request(t, s.Routes(), http.MethodPost, "/api/v1/repositories", body, headers)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	second := request(t, s.Routes(), http.MethodPost, "/api/v1/repositories", body, headers)
	if second.Code != first.Code || second.Body.String() != first.Body.String() {
		t.Fatalf("replay status/body mismatch: first=%d %s second=%d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	if second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("replayed response was not identified")
	}
	conflict := request(t, s.Routes(), http.MethodPost, "/api/v1/repositories", strings.Replace(body, "repo-one", "repo-two", 1), headers)
	if conflict.Code != http.StatusConflict || conflict.Header().Get("Content-Type") != "application/problem+json" || !strings.Contains(conflict.Body.String(), "idempotency_key_conflict") {
		t.Fatalf("conflict status=%d type=%q body=%s", conflict.Code, conflict.Header().Get("Content-Type"), conflict.Body.String())
	}
}

func TestProblemDetailsUsesStandardMediaTypeAndRequestID(t *testing.T) {
	s := testServer(t)
	response := request(t, s.Routes(), http.MethodGet, "/api/v1/requirements/missing", "", map[string]string{"X-Request-ID": "request-123"})
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("content type=%q", got)
	}
	if !strings.Contains(response.Body.String(), `"request_id":"request-123"`) {
		t.Fatalf("problem body=%s", response.Body.String())
	}
}

func TestSessionHarnessRoutesExposeDurableTurnAndRecoveryContracts(t *testing.T) {
	s := testServer(t)
	headers := map[string]string{"X-Workspace-ID": "workspace-1", "X-Tenant-ID": "tenant-1"}
	created := request(t, s.Routes(), http.MethodPost, "/api/v1/sessions", `{"id":"session-1","budget_tokens":1000}`, headers)
	if created.Code != http.StatusCreated {
		t.Fatalf("session status=%d body=%s", created.Code, created.Body.String())
	}
	turnResponse := request(t, s.Routes(), http.MethodPost, "/api/v1/sessions/session-1/turns", `{"role":"user","content":"preserve this turn","idempotency_key":"turn-1"}`, headers)
	if turnResponse.Code != http.StatusCreated {
		t.Fatalf("turn status=%d body=%s", turnResponse.Code, turnResponse.Body.String())
	}
	var turn struct {
		ID   string `json:"id"`
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(turnResponse.Body.Bytes(), &turn); err != nil || turn.Hash == "" {
		t.Fatalf("turn=%s err=%v", turnResponse.Body.String(), err)
	}
	checkpoint := request(t, s.Routes(), http.MethodPost, "/api/v1/sessions/session-1/checkpoints", `{"turn_sequence":1,"phase":"turn_started","event_hash":"`+turn.Hash+`","context_version":1}`, headers)
	if checkpoint.Code != http.StatusCreated {
		t.Fatalf("checkpoint status=%d body=%s", checkpoint.Code, checkpoint.Body.String())
	}
	memory := request(t, s.Routes(), http.MethodPost, "/api/v1/sessions/session-1/memory", `{"kind":"decision","content":"keep the original session","source_ids":["`+turn.ID+`"],"confidence":0.9}`, headers)
	if memory.Code != http.StatusCreated {
		t.Fatalf("memory status=%d body=%s", memory.Code, memory.Body.String())
	}
	compact := request(t, s.Routes(), http.MethodPost, "/api/v1/sessions/session-1/compact", `{"start_sequence":1,"end_sequence":1,"summary":"turn preserved as archive"}`, headers)
	if compact.Code != http.StatusCreated {
		t.Fatalf("compact status=%d body=%s", compact.Code, compact.Body.String())
	}
	recovery := request(t, s.Routes(), http.MethodGet, "/api/v1/sessions/session-1/recover", "", headers)
	if recovery.Code != http.StatusOK || !strings.Contains(recovery.Body.String(), "session-1") {
		t.Fatalf("recovery status=%d body=%s", recovery.Code, recovery.Body.String())
	}
	compiled := request(t, s.Routes(), http.MethodGet, "/api/v1/sessions/session-1/context/compile", "", headers)
	if compiled.Code != http.StatusOK || !strings.Contains(compiled.Body.String(), "turn preserved as archive") {
		t.Fatalf("compiled status=%d body=%s", compiled.Code, compiled.Body.String())
	}
}

func TestSystemDiagnosticsIsSecretFreeAndReportsDurability(t *testing.T) {
	s := testServer(t)
	response := request(t, s.Routes(), http.MethodGet, "/api/v1/system/diagnostics", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("diagnostics status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "ADRO_API_TOKEN") || !strings.Contains(response.Body.String(), "harness_durable") {
		t.Fatalf("diagnostics leaked secret or omitted harness state: %s", response.Body.String())
	}
}

func TestPluginRegistryRouteIsVisibleAndFailClosedForUnsignedInstall(t *testing.T) {
	s := testServer(t)
	list := request(t, s.Routes(), http.MethodGet, "/api/v1/plugins", "", nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"items"`) {
		t.Fatalf("plugin list status=%d body=%s", list.Code, list.Body.String())
	}
	unsigned := request(t, s.Routes(), http.MethodPost, "/api/v1/plugins", `{"manifest":{"id":"example","name":"Example","version":"1.0.0","protocol_version":"adro.plugin.v1","capabilities":["events.publish"]},"digest":"sha256:bad"}`, nil)
	if unsigned.Code != http.StatusUnprocessableEntity || !strings.Contains(unsigned.Body.String(), "plugin_install_failed") {
		t.Fatalf("unsigned install status=%d body=%s", unsigned.Code, unsigned.Body.String())
	}
}

func TestRunnerExecuteRouteAuditsCommandWithoutEchoingIt(t *testing.T) {
	s := testServer(t)
	root := t.TempDir()
	registered := request(t, s.Routes(), http.MethodPost, "/api/v1/runners", `{"name":"local","provider":"test","version":"1","workspace_root":"`+root+`","concurrency":1}`, nil)
	if registered.Code != http.StatusCreated {
		t.Fatal(registered.Code, registered.Body.String())
	}
	var runnerBody struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(registered.Body.Bytes(), &runnerBody)
	if heartbeat := request(t, s.Routes(), http.MethodPost, "/api/v1/runners/"+runnerBody.ID+"/heartbeat", `{"active_runs":0}`, nil); heartbeat.Code != http.StatusOK {
		t.Fatal(heartbeat.Code, heartbeat.Body.String())
	}
	executed := request(t, s.Routes(), http.MethodPost, "/api/v1/runners/"+runnerBody.ID+"/execute", `{"command":["/bin/echo","ready"],"timeout_ms":1000}`, nil)
	if executed.Code != http.StatusOK || !strings.Contains(executed.Body.String(), `"stdout":"ready`) {
		t.Fatalf("execute status=%d body=%s", executed.Code, executed.Body.String())
	}
	for _, record := range s.Audit.List() {
		if record.Action == "runner.execution.completed" && strings.Contains(mustJSON(record.Payload), "ready") {
			t.Fatal("runner audit leaked command text")
		}
	}
}

func TestBugFingerprintDeduplicatesAndRepairLimit(t *testing.T) {
	s := testServer(t)
	body := `{"workspace_id":"w1","title":"failed test","repository_id":"repo","work_item_id":"work","actual":"500"}`
	a := request(t, s.Routes(), http.MethodPost, "/api/v1/bugs", body, nil)
	b := request(t, s.Routes(), http.MethodPost, "/api/v1/bugs", body, nil)
	if a.Code != 201 || b.Code != 200 {
		t.Fatalf("statuses %d/%d", a.Code, b.Code)
	}
	var bug map[string]any
	_ = json.Unmarshal(a.Body.Bytes(), &bug)
	id := bug["id"].(string)
	for i := 0; i < 3; i++ {
		rr := request(t, s.Routes(), http.MethodPost, "/api/v1/bugs/"+id+"/repair", "", nil)
		if rr.Code != 202 {
			t.Fatalf("repair %d status=%d body=%s", i, rr.Code, rr.Body.String())
		}
	}
	rr := request(t, s.Routes(), http.MethodPost, "/api/v1/bugs/"+id+"/repair", "", nil)
	if rr.Code != 409 {
		t.Fatalf("expected limit, got %d", rr.Code)
	}
}

func TestStandaloneBugRepairUsesStableSyntheticWorkItem(t *testing.T) {
	s := testServer(t)
	created := request(t, s.Routes(), http.MethodPost, "/api/v1/bugs", `{"workspace_id":"w1","title":"standalone failure","repository_id":"repo","actual":"500"}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatal(created.Code, created.Body.String())
	}
	var bug struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &bug); err != nil || bug.ID == "" {
		t.Fatalf("bug=%s err=%v", created.Body.String(), err)
	}
	repair := request(t, s.Routes(), http.MethodPost, "/api/v1/bugs/"+bug.ID+"/repair", "", nil)
	if repair.Code != http.StatusAccepted {
		t.Fatalf("repair status=%d body=%s", repair.Code, repair.Body.String())
	}
	if attempts := s.Store.ListRepairAttempts(bug.ID); len(attempts) != 1 || attempts[0].WorkItemID != "bug-"+bug.ID || attempts[0].ContextID != "context-bug-"+bug.ID {
		t.Fatalf("attempts=%+v", attempts)
	}
	if provenance, ok := s.Store.FindProvenance("bug-" + bug.ID); !ok || provenance.ProviderTaskID == "" {
		t.Fatalf("provenance=%+v ok=%v", provenance, ok)
	}
}

func TestRepairReusesWorkItemContextAndMockSession(t *testing.T) {
	s := testServer(t)
	created := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements", `{"workspace_id":"w","title":"Context flow","description":"preserve repair context","acceptance_criteria":["pass"],"assignee_member_ids":["dev"],"repository_ids":["repo"]}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatal(created.Code, created.Body.String())
	}
	var requirement struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(created.Body.Bytes(), &requirement)
	if got := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+requirement.ID+"/start", "", nil); got.Code != http.StatusOK {
		t.Fatal(got.Code, got.Body.String())
	}
	items := request(t, s.Routes(), http.MethodGet, "/api/v1/requirements/"+requirement.ID+"/work-items", "", nil)
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	_ = json.Unmarshal(items.Body.Bytes(), &page)
	if len(page.Items) != 1 {
		t.Fatalf("items=%s", items.Body.String())
	}
	run := request(t, s.Routes(), http.MethodPost, "/api/v1/work-items/"+page.Items[0].ID+"/run", `{"input":"implement"}`, nil)
	if run.Code != http.StatusAccepted {
		t.Fatal(run.Code, run.Body.String())
	}
	contextResponse := request(t, s.Routes(), http.MethodGet, "/api/v1/work-items/"+page.Items[0].ID+"/context", "", nil)
	if contextResponse.Code != http.StatusOK {
		t.Fatalf("context status=%d body=%s", contextResponse.Code, contextResponse.Body.String())
	}
	var contextBody struct {
		Context struct {
			ContextID string `json:"context_id"`
		} `json:"context"`
		Provenance map[string]any `json:"provenance"`
	}
	if err := json.Unmarshal(contextResponse.Body.Bytes(), &contextBody); err != nil || contextBody.Context.ContextID == "" || len(contextBody.Provenance) == 0 {
		t.Fatalf("context response=%s err=%v", contextResponse.Body.String(), err)
	}
	bug := request(t, s.Routes(), http.MethodPost, "/api/v1/bugs", `{"workspace_id":"w","requirement_id":"`+requirement.ID+`","work_item_id":"`+page.Items[0].ID+`","repository_id":"repo","title":"test failure","actual":"failed"}`, nil)
	if bug.Code != http.StatusCreated {
		t.Fatal(bug.Code, bug.Body.String())
	}
	var bugBody struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(bug.Body.Bytes(), &bugBody)
	repair := request(t, s.Routes(), http.MethodPost, "/api/v1/bugs/"+bugBody.ID+"/repair", "", nil)
	if repair.Code != http.StatusAccepted {
		t.Fatal(repair.Code, repair.Body.String())
	}
	var repairBody struct {
		SessionReused    bool   `json:"session_reused"`
		ContextID        string `json:"context_id"`
		ContextAvailable bool   `json:"context_available"`
	}
	_ = json.Unmarshal(repair.Body.Bytes(), &repairBody)
	if !repairBody.SessionReused || !repairBody.ContextAvailable || repairBody.ContextID == "" {
		t.Fatalf("repair continuity=%+v body=%s", repairBody, repair.Body.String())
	}
	if attempts := s.Store.ListRepairAttempts(bugBody.ID); len(attempts) != 1 || attempts[0].ContextID != repairBody.ContextID {
		t.Fatalf("attempts=%+v", attempts)
	}
	status, statusErr := s.Harness.ContextStatus("session-" + page.Items[0].ID)
	if statusErr != nil || status.TurnCount == 0 || status.CheckpointCount < 2 {
		t.Fatalf("repair harness state=%+v err=%v", status, statusErr)
	}
	recovery, recoveryErr := s.Harness.Recover("session-"+page.Items[0].ID, time.Now().UTC())
	if recoveryErr != nil || len(recovery.PendingEffects) != 0 {
		t.Fatalf("repair outbox recovery=%+v err=%v", recovery, recoveryErr)
	}
	attemptsResponse := request(t, s.Routes(), http.MethodGet, "/api/v1/work-items/"+page.Items[0].ID+"/repair-attempts", "", nil)
	if attemptsResponse.Code != http.StatusOK {
		t.Fatalf("repair attempts status=%d body=%s", attemptsResponse.Code, attemptsResponse.Body.String())
	}
	var attemptsBody struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(attemptsResponse.Body.Bytes(), &attemptsBody); err != nil || len(attemptsBody.Items) != 1 {
		t.Fatalf("repair attempts response=%s err=%v", attemptsResponse.Body.String(), err)
	}
}

func TestArtifactUploadSupportsRange(t *testing.T) {
	s := testServer(t)
	create := request(t, s.Routes(), http.MethodPost, "/api/v1/artifacts/uploads", `{"artifact_id":"report","media_type":"text/plain","immutable":true}`, map[string]string{"X-Tenant-ID": "t1"})
	if create.Code != 201 {
		t.Fatal(create.Code, create.Body.String())
	}
	var u map[string]any
	_ = json.Unmarshal(create.Body.Bytes(), &u)
	uploadID := u["upload_id"].(string)
	part := request(t, s.Routes(), http.MethodPut, "/api/v1/artifacts/uploads/"+uploadID+"/parts/1", "abcdef", nil)
	if part.Code != 200 {
		t.Fatal(part.Code)
	}
	complete := request(t, s.Routes(), http.MethodPost, "/api/v1/artifacts/uploads/"+uploadID+"/complete", "", map[string]string{"X-Tenant-ID": "t1"})
	if complete.Code != 201 {
		t.Fatal(complete.Code, complete.Body.String())
	}
	content := request(t, s.Routes(), http.MethodGet, "/api/v1/artifacts/report/versions/1/content", "", map[string]string{"X-Tenant-ID": "t1", "Range": "bytes=1-3"})
	if content.Code != 206 || content.Header().Get("Content-Length") != "3" {
		t.Fatalf("range status=%d headers=%v", content.Code, content.Header())
	}
	data, _ := io.ReadAll(content.Body)
	if string(data) != "bcd" {
		t.Fatalf("range body=%q", data)
	}
}

func TestScreenshotUploadStoresArtifactAndDeliversToProvider(t *testing.T) {
	s := testServer(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("target_type", "comment"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("target_id", "comment-1"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "review.png")
	if err != nil {
		t.Fatal(err)
	}
	// A minimal valid PNG signature is sufficient for the transport contract;
	// media type is supplied explicitly by the multipart part.
	part.Write([]byte("\x89PNG\r\n\x1a\n"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/screenshots", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Tenant-ID", "tenant-1")
	req.Header.Set("X-Workspace-ID", "workspace-1")
	resp := httptest.NewRecorder()
	s.Routes().ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("screenshot status=%d body=%s", resp.Code, resp.Body.String())
	}
	var result struct {
		URI      string `json:"uri"`
		Delivery string `json:"delivery"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.URI, "artifact://tenant-1/screenshot-") || result.Delivery != "delivered" {
		t.Fatalf("result=%+v", result)
	}
	if got := s.Provider.(*provider.MockProvider).AttachmentCount(); got != 1 {
		t.Fatalf("provider attachments=%d", got)
	}
	diagnostics := request(t, s.Routes(), http.MethodGet, "/api/v1/provider/diagnostics", "", nil)
	if diagnostics.Code != http.StatusOK || !strings.Contains(diagnostics.Body.String(), "attachment_delivery_supported") {
		t.Fatalf("diagnostics status=%d body=%s", diagnostics.Code, diagnostics.Body.String())
	}
}

func TestControlPlaneResourcesAndAudit(t *testing.T) {
	s := testServer(t)
	register := request(t, s.Routes(), http.MethodPost, "/api/v1/repositories", `{"workspace_id":"w","canonical_name":"service","clone_url":"https://git.example/service"}`, nil)
	if register.Code != http.StatusCreated {
		t.Fatalf("repository status=%d body=%s", register.Code, register.Body.String())
	}
	var repository struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(register.Body.Bytes(), &repository)
	indexed := request(t, s.Routes(), http.MethodPost, "/api/v1/repositories/"+repository.ID+"/index", `{"commit":"abc123"}`, nil)
	if indexed.Code != http.StatusOK {
		t.Fatalf("index status=%d", indexed.Code)
	}
	runnerResponse := request(t, s.Routes(), http.MethodPost, "/api/v1/runners", `{"name":"runner-1","provider":"mock","version":"1.0","security_domain":"test"}`, nil)
	if runnerResponse.Code != http.StatusCreated {
		t.Fatalf("runner status=%d", runnerResponse.Code)
	}
	profile := request(t, s.Routes(), http.MethodPost, "/api/v1/developer-profiles/alice", `{"workspace_id":"w","git_identity":{"name":"ADRO Agent"}}`, map[string]string{"X-Workspace-ID": "w"})
	if profile.Code != http.StatusOK {
		t.Fatalf("profile status=%d body=%s", profile.Code, profile.Body.String())
	}
	mcp := request(t, s.Routes(), http.MethodPost, "/api/v1/mcp", `{"workspace_id":"w","name":"search","endpoint":"https://mcp.example"}`, nil)
	if mcp.Code != http.StatusCreated {
		t.Fatalf("mcp status=%d", mcp.Code)
	}
	skill := request(t, s.Routes(), http.MethodPost, "/api/v1/skills", `{"workspace_id":"w","name":"verify","version":"1.0.0"}`, nil)
	if skill.Code != http.StatusCreated {
		t.Fatalf("skill status=%d", skill.Code)
	}
	automation := request(t, s.Routes(), http.MethodPost, "/api/v1/automations", `{"workspace_id":"w","name":"on-failure","trigger":{"event":"test.failed"}}`, nil)
	if automation.Code != http.StatusCreated {
		t.Fatalf("automation status=%d", automation.Code)
	}
	approval := request(t, s.Routes(), http.MethodPost, "/api/v1/approvals", `{"workspace_id":"w","requirement_id":"req","kind":"design"}`, nil)
	if approval.Code != http.StatusCreated {
		t.Fatalf("approval status=%d", approval.Code)
	}
	var a struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(approval.Body.Bytes(), &a)
	decision := request(t, s.Routes(), http.MethodPost, "/api/v1/approvals/"+a.ID+"/decide", `{"decision":"approved","reason":"reviewed"}`, map[string]string{"X-Member-ID": "reviewer"})
	if decision.Code != http.StatusOK {
		t.Fatalf("decision status=%d body=%s", decision.Code, decision.Body.String())
	}
	auditResponse := request(t, s.Routes(), http.MethodGet, "/api/v1/audit", "", nil)
	if auditResponse.Code != http.StatusOK {
		t.Fatalf("audit status=%d", auditResponse.Code)
	}
	var auditPage struct {
		ChainValid bool `json:"chain_valid"`
	}
	_ = json.Unmarshal(auditResponse.Body.Bytes(), &auditPage)
	if !auditPage.ChainValid {
		t.Fatal("audit chain invalid")
	}
}

func TestWorkflowGatesDiffAndGovernanceActions(t *testing.T) {
	s := testServer(t)
	create := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements", `{"workspace_id":"w","title":"Gate me","description":"exercise gates","acceptance_criteria":["pass"],"assignee_member_ids":["alice"],"repository_ids":["repo"]}`, nil)
	if create.Code != http.StatusCreated {
		t.Fatal(create.Code, create.Body.String())
	}
	var req struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(create.Body.Bytes(), &req)
	for _, action := range []string{"start", "confirm-assignees", "begin-design"} {
		if got := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+req.ID+"/"+action, "", nil).Code; got != http.StatusOK {
			t.Fatalf("%s status=%d", action, got)
		}
	}
	gate := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+req.ID+"/gates", `{"gate":"design","decision":"fail","bug":{"title":"review failed","repository_id":"repo","actual":"missing contract"}}`, nil)
	if gate.Code != http.StatusOK {
		t.Fatal(gate.Code, gate.Body.String())
	}
	design := request(t, s.Routes(), http.MethodPost, "/api/v1/requirements/"+req.ID+"/transition", `{"status":"DESIGNING"}`, nil)
	if design.Code != http.StatusOK {
		t.Fatal(design.Code, design.Body.String())
	}
	itemPage := request(t, s.Routes(), http.MethodGet, "/api/v1/requirements/"+req.ID+"/work-items", "", nil)
	var items struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	_ = json.Unmarshal(itemPage.Body.Bytes(), &items)
	if len(items.Items) != 1 {
		t.Fatalf("items=%d", len(items.Items))
	}
	diff := request(t, s.Routes(), http.MethodPost, "/api/v1/work-items/"+items.Items[0].ID+"/diff", `{"base_commit":"a","head_commit":"b","files":["api.go"],"patch":"+ok"}`, nil)
	if diff.Code != http.StatusCreated {
		t.Fatal(diff.Code, diff.Body.String())
	}
	if got := request(t, s.Routes(), http.MethodGet, "/api/v1/work-items/"+items.Items[0].ID+"/diff", "", nil).Code; got != http.StatusOK {
		t.Fatal(got)
	}
	migration := request(t, s.Routes(), http.MethodPost, "/api/v1/artifact-migrations", `{"workspace_id":"w","artifact_id":"a","from_driver":"filesystem","to_driver":"s3-compatible"}`, nil)
	if migration.Code != http.StatusAccepted {
		t.Fatal(migration.Code, migration.Body.String())
	}
	var mig struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(migration.Body.Bytes(), &mig)
	if got := request(t, s.Routes(), http.MethodPost, "/api/v1/artifact-migrations/"+mig.ID+"/pause", "", nil).Code; got != http.StatusOK {
		t.Fatal(got)
	}
	mcp := request(t, s.Routes(), http.MethodPost, "/api/v1/mcp/servers", `{"workspace_id":"w","name":"search","endpoint":"https://mcp.example","secret_ref":"secret/mcp/search"}`, nil)
	if mcp.Code != http.StatusCreated {
		t.Fatal(mcp.Code, mcp.Body.String())
	}
	var server struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(mcp.Body.Bytes(), &server)
	if got := request(t, s.Routes(), http.MethodPost, "/api/v1/mcp/servers/"+server.ID+"/discover", "", nil).Code; got != http.StatusOK {
		t.Fatal(got)
	}
	healthCheck := request(t, s.Routes(), http.MethodPost, "/api/v1/mcp/servers/"+server.ID+"/health-check", "", nil)
	if healthCheck.Code != http.StatusOK || !strings.Contains(healthCheck.Body.String(), `"reachable":false`) || !strings.Contains(healthCheck.Body.String(), `"status":"unreachable"`) {
		t.Fatalf("health check status=%d body=%s", healthCheck.Code, healthCheck.Body.String())
	}
	if got := request(t, s.Routes(), http.MethodPost, "/api/v1/agents/agent-1/mcp-bindings", `{"workspace_id":"w","capability_id":"`+server.ID+`"}`, nil).Code; got != http.StatusCreated {
		t.Fatal(got)
	}
	skill := request(t, s.Routes(), http.MethodPost, "/api/v1/skills", `{"workspace_id":"w","name":"verify","version":"1.0.0"}`, nil)
	if skill.Code != http.StatusCreated {
		t.Fatal(skill.Code, skill.Body.String())
	}
	var skillID struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(skill.Body.Bytes(), &skillID)
	if got := request(t, s.Routes(), http.MethodPost, "/api/v1/skills/"+skillID.ID+"/publish", "", nil).Code; got != http.StatusOK {
		t.Fatal(got)
	}
	automation := request(t, s.Routes(), http.MethodPost, "/api/v1/automations", `{"workspace_id":"w","name":"on-failure","enabled":true}`, nil)
	if automation.Code != http.StatusCreated {
		t.Fatal(automation.Code, automation.Body.String())
	}
	var automationID struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(automation.Body.Bytes(), &automationID)
	trigger := request(t, s.Routes(), http.MethodPost, "/api/v1/automations/"+automationID.ID+"/trigger", `{}`, nil)
	if trigger.Code != http.StatusAccepted {
		t.Fatal(trigger.Code, trigger.Body.String())
	}
}

func TestWorkspaceWebSocketReplaysAndStreamsEvents(t *testing.T) {
	s := testServer(t)
	if err := s.Events.Publish(context.Background(), events.New("test.event.v1", "requirement", "req-1", "tenant", "w", 1, map[string]any{"ok": true})); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(s.Routes())
	defer httpServer.Close()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/api/v1/streams/workspaces/w"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var envelope events.Envelope
	if err := conn.ReadJSON(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.EventType != "test.event.v1" || envelope.WorkspaceID != "w" {
		t.Fatalf("event=%+v", envelope)
	}
	// A cursor replay should include the event only when it is before the cursor.
	replayURL := wsURL + "?cursor=" + envelope.EventID
	replay, _, err := websocket.DefaultDialer.Dial(replayURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	_ = replay.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, _, err := replay.ReadMessage(); err == nil {
		t.Fatal("cursor replay returned an already-consumed event")
	}
	if err := s.Events.Publish(context.Background(), events.New("test.event.2.v1", "requirement", "req-2", "tenant", "w", 1, map[string]any{"ok": true})); err != nil {
		t.Fatal(err)
	}
	var live events.Envelope
	if err := conn.ReadJSON(&live); err != nil {
		t.Fatal(err)
	}
	if live.EventType != "test.event.2.v1" {
		t.Fatalf("live event=%+v", live)
	}
}

func TestOptionalBearerAuthMode(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "required")
	t.Setenv("ADRO_API_TOKEN", "test-token")
	t.Setenv("ADRO_ADMIN_PASSWORD", "AdminPass123!")
	s := testServer(t)
	if got := request(t, s.Routes(), http.MethodGet, "/api/v1/bugs", "", nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("without token status=%d", got)
	}
	if got := request(t, s.Routes(), http.MethodGet, "/api/v1/bugs", "", map[string]string{"Authorization": "Bearer test-token"}).Code; got != http.StatusOK {
		t.Fatalf("with token status=%d", got)
	}
	if got := request(t, s.Routes(), http.MethodGet, "/readyz", "", nil).Code; got != http.StatusOK {
		t.Fatalf("health status=%d", got)
	}
}

func TestUnknownAuthModeFailsClosed(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "requred")
	s := testServer(t)
	for _, path := range []string{"/api/v1/requirements", "/readyz"} {
		response := request(t, s.Routes(), http.MethodGet, path, "", nil)
		if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "invalid_auth_mode") {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestRequiredLocalAuthReadinessNeedsIdentitySource(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "required")
	t.Setenv("ADRO_AUTH_BACKEND", "local")
	t.Setenv("ADRO_ADMIN_USERNAME", "")
	t.Setenv("ADRO_ADMIN_PASSWORD", "")
	t.Setenv("ADRO_AUTH_STATE_FILE", "")
	s := testServer(t)
	response := request(t, s.Routes(), http.MethodGet, "/readyz", "", nil)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "auth_not_configured") {
		t.Fatalf("readiness status=%d body=%s", response.Code, response.Body.String())
	}
}
