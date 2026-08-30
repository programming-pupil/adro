package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/adro-project/adro/internal/events"
	"github.com/gorilla/websocket"
)

// MulticaProvider is the reference HTTP adapter. It deliberately keeps
// Multica identifiers inside provider bindings and never exposes them as
// domain primary keys.
type MulticaProvider struct {
	BaseURL string
	Token   string
	Client  *http.Client
	// authVerified is set only after an authenticated mutation succeeds. A
	// public /api/config or health response is not evidence that credentials
	// were accepted by the upstream API.
	authVerified atomic.Bool
	// DefaultAgentID is an optional provider-native agent UUID used when a
	// WorkItem does not carry an explicit binding. It is intentionally opt-in:
	// Multica rejects unknown or non-UUID assignees rather than accepting a
	// local ADRO member identifier.
	DefaultAgentID string
	// DefaultWorkspaceID, DefaultRuntimeID, and DefaultProjectID select
	// provider-native resources
	// when ADRO's local workspace identifier cannot be sent upstream. If they
	// are empty, the adapter may auto-select only when exactly one eligible
	// resource is visible to the configured Multica identity.
	DefaultWorkspaceID string
	DefaultRuntimeID   string
	DefaultProjectID   string
	// API paths are configurable because Multica daemon and hosted API may
	// expose the same versioned contract under different gateways.
	CapabilitiesPath string
	AttachmentPath   string
	WebSocketURL     string
}

func (p *MulticaProvider) AuthenticationVerified() bool {
	return p != nil && p.authVerified.Load()
}

func (p *MulticaProvider) markAuthenticated() {
	if p != nil && p.Token != "" {
		p.authVerified.Store(true)
	}
}

// multicaConfigResponse is the public, anonymous handshake exposed by the
// current Multica backend. Multica deliberately does not expose a generic
// capabilities endpoint, so this is only an identity/version probe; feature
// claims still come from an explicit capabilities endpoint when one exists.
type multicaConfigResponse struct {
	ServerVersion string `json:"server_version"`
}

type multicaHealthResponse struct {
	Healthy *bool  `json:"healthy"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Checks  struct {
		DB         string `json:"db"`
		Migrations string `json:"migrations"`
	} `json:"checks"`
}

type multicaCreateIssueRequest struct {
	Title         string `json:"title"`
	Description   string `json:"description,omitempty"`
	Status        string `json:"status,omitempty"`
	Priority      string `json:"priority,omitempty"`
	AssigneeType  string `json:"assignee_type,omitempty"`
	AssigneeID    string `json:"assignee_id,omitempty"`
	ParentIssueID string `json:"parent_issue_id,omitempty"`
	ProjectID     string `json:"project_id,omitempty"`
	Stage         *int   `json:"stage,omitempty"`
}

type multicaCreateAgentRequest struct {
	Name               string `json:"name"`
	Description        string `json:"description,omitempty"`
	Instructions       string `json:"instructions,omitempty"`
	RuntimeID          string `json:"runtime_id"`
	Visibility         string `json:"visibility"`
	MaxConcurrentTasks int    `json:"max_concurrent_tasks"`
}

type multicaResourceResponse struct {
	ID     string `json:"id"`
	Status string `json:"status,omitempty"`
}

type multicaIssueResponse struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
}

func NewMulticaProvider(baseURL, token string) *MulticaProvider {
	return &MulticaProvider{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, Client: &http.Client{Timeout: 20 * time.Second}, CapabilitiesPath: "/api/capabilities", AttachmentPath: "/api/attachments"}
}
func (p *MulticaProvider) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}
func (p *MulticaProvider) do(ctx context.Context, method, path string, input, output any) error {
	if p.BaseURL == "" {
		return &UpstreamError{Code: ErrorConfiguration}
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(data))
	}
	req, err := http.NewRequestWithContext(ctx, method, p.BaseURL+path, body)
	if err != nil {
		return &UpstreamError{Code: ErrorConfiguration}
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.Token)
	}
	req.Header.Set("X-Request-ID", fmt.Sprintf("adro-%d", time.Now().UnixNano()))
	resp, err := p.client().Do(req)
	if err != nil {
		return transportError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errorForStatus(resp.StatusCode)
	}
	if output == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(output); err != nil {
		return &UpstreamError{Code: ErrorInvalidResponse}
	}
	return nil
}
func (p *MulticaProvider) Capabilities(ctx context.Context) (Capabilities, error) {
	var out Capabilities
	path := p.CapabilitiesPath
	if path == "" {
		path = "/api/capabilities"
	}
	err := p.do(ctx, http.MethodGet, path, nil, &out)
	// Hosted gateways have historically exposed the capability endpoint under
	// /api/v1. A 404 fallback makes the adapter explicit and testable without
	// silently accepting an unrelated endpoint.
	if err != nil && path == "/api/capabilities" && ErrorCodeOf(err) == ErrorNotFound {
		err = p.do(ctx, http.MethodGet, "/api/v1/capabilities", nil, &out)
	}
	// The upstream Multica server currently has no public capabilities route.
	// /api/config is intentionally limited to deployment-safe fields, but a
	// successful response proves that the configured URL and credentials reach
	// a Multica API. Keep the feature list conservative so attachment/run
	// support is never inferred from reachability alone.
	if err != nil && ErrorCodeOf(err) == ErrorNotFound {
		var config multicaConfigResponse
		if configErr := p.do(ctx, http.MethodGet, "/api/config", nil, &config); configErr == nil {
			return Capabilities{
				Provider:       "multica",
				AdapterVersion: "api-config-v1",
				ServerVersion:  config.ServerVersion,
				Features:       []string{"api.config.v1"},
			}, nil
		} else if ErrorCodeOf(configErr) != ErrorNotFound {
			return out, configErr
		}
	}
	if err == nil && out.Provider == "" {
		out.Provider = "multica"
	}
	return out, err
}
func (p *MulticaProvider) EnsureAgent(ctx context.Context, s AgentSpec) (AgentBinding, error) {
	workspaceID, err := p.resolveWorkspaceID(ctx, s.WorkspaceID)
	if err != nil {
		return AgentBinding{}, err
	}
	runtimeID, err := p.resolveRuntimeID(ctx, workspaceID, s.RuntimeID)
	if err != nil {
		return AgentBinding{}, err
	}
	query := url.Values{}
	query.Set("workspace_id", workspaceID)
	var out multicaResourceResponse
	err = p.do(ctx, http.MethodPost, "/api/agents?"+query.Encode(), multicaCreateAgentRequest{
		Name: strings.TrimSpace(s.Name), Instructions: strings.TrimSpace(s.Instructions), RuntimeID: runtimeID,
		Visibility: "private", MaxConcurrentTasks: 1,
	}, &out)
	if err != nil {
		return AgentBinding{}, err
	}
	if out.ID == "" {
		return AgentBinding{}, errors.New("multica agent create returned no id")
	}
	p.markAuthenticated()
	return AgentBinding{ID: out.ID, Provider: "multica", ProviderAgentID: out.ID, AgentSpec: s}, nil
}
func (p *MulticaProvider) EnsureTeamWorkspace(ctx context.Context, s WorkspaceSpec) (WorkspaceBinding, error) {
	var out WorkspaceBinding
	err := p.do(ctx, http.MethodPost, "/api/projects", s, &out)
	if err != nil {
		return WorkspaceBinding{}, err
	}
	p.markAuthenticated()
	out.Provider = "multica"
	return out, nil
}
func (p *MulticaProvider) CreateWorkItem(ctx context.Context, s WorkItemSpec) (ProviderWorkItem, error) {
	if strings.TrimSpace(s.Title) == "" {
		return ProviderWorkItem{}, errors.New("work item title is required")
	}
	assigneeType := s.AssigneeType
	assigneeID := s.ProviderAssigneeID
	if assigneeID != "" && assigneeType == "" {
		assigneeType = "agent"
	}
	if assigneeID == "" && p.DefaultAgentID != "" {
		assigneeType = "agent"
		assigneeID = p.DefaultAgentID
	}
	projectID := strings.TrimSpace(s.ProjectProviderID)
	if projectID == "" {
		projectID = strings.TrimSpace(p.DefaultProjectID)
	}
	request := multicaCreateIssueRequest{
		Title:         s.Title,
		Description:   s.Description,
		Status:        "todo",
		Priority:      "none",
		AssigneeType:  assigneeType,
		AssigneeID:    assigneeID,
		ParentIssueID: s.ParentProviderIssueID,
		ProjectID:     projectID,
	}
	if s.Stage > 0 {
		stage := s.Stage
		request.Stage = &stage
	}
	workspaceID, err := p.resolveWorkspaceID(ctx, s.WorkspaceID)
	if err != nil {
		return ProviderWorkItem{}, err
	}
	query := url.Values{}
	query.Set("workspace_id", workspaceID)
	path := "/api/issues?" + query.Encode()
	var out multicaIssueResponse
	if err := p.do(ctx, http.MethodPost, path, request, &out); err != nil {
		return ProviderWorkItem{}, err
	}
	providerID := out.ID
	if providerID == "" {
		providerID = out.Identifier
	}
	if providerID == "" {
		return ProviderWorkItem{}, errors.New("multica issue create returned no id")
	}
	p.markAuthenticated()
	return ProviderWorkItem{ID: s.ID, ProviderIssueID: providerID}, nil
}

func (p *MulticaProvider) resolveWorkspaceID(ctx context.Context, candidate string) (string, error) {
	if configured := strings.TrimSpace(p.DefaultWorkspaceID); configured != "" {
		return configured, nil
	}
	candidate = strings.TrimSpace(candidate)
	if uuidPattern.MatchString(candidate) {
		return canonicalUUID(candidate), nil
	}
	var workspaces []multicaResourceResponse
	if err := p.do(ctx, http.MethodGet, "/api/workspaces", nil, &workspaces); err != nil {
		return "", err
	}
	if len(workspaces) != 1 || strings.TrimSpace(workspaces[0].ID) == "" {
		return "", errors.New("multica workspace selection is ambiguous; set ADRO_MULTICA_WORKSPACE_ID")
	}
	return strings.TrimSpace(workspaces[0].ID), nil
}

// workspaceScopedPath adds the tenant selector required by Multica's
// workspace-scoped issue routes. Keep this in one place so native issue/task
// fallbacks cannot accidentally send an unscoped request (the server rejects
// those with a 400 before it reaches the actual handler).
func (p *MulticaProvider) workspaceScopedPath(ctx context.Context, path string) (string, error) {
	workspaceID, err := p.resolveWorkspaceID(ctx, "")
	if err != nil {
		return "", err
	}
	u, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	query := u.Query()
	query.Set("workspace_id", workspaceID)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

// waitForIssueTask waits for the daemon to claim a native issue task and pin
// the session/work directory. The rerun endpoint responds as soon as the row
// is queued, so returning that acknowledgement as a run binding would lose
// the provenance required by ADRO's durable pipeline.
func (p *MulticaProvider) waitForIssueTask(ctx context.Context, issueID, taskID string) (multicaIssueTask, error) {
	if strings.TrimSpace(issueID) == "" || strings.TrimSpace(taskID) == "" {
		return multicaIssueTask{}, errors.New("multica task provenance requires issue and task ids")
	}
	pollCtx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		pollCtx, cancel = context.WithTimeout(ctx, 45*time.Second)
	}
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		path, err := p.workspaceScopedPath(pollCtx, "/api/issues/"+url.PathEscape(issueID)+"/task-runs")
		if err != nil {
			return multicaIssueTask{}, err
		}
		var tasks []multicaIssueTask
		if err := p.do(pollCtx, http.MethodGet, path, nil, &tasks); err != nil {
			return multicaIssueTask{}, err
		}
		for _, task := range tasks {
			if strings.TrimSpace(task.ID) != strings.TrimSpace(taskID) {
				continue
			}
			sessionID, workDir := task.continuationSession()
			if sessionID != "" && workDir != "" {
				return task, nil
			}
			if strings.EqualFold(task.Status, "failed") || strings.EqualFold(task.Status, "cancelled") || strings.EqualFold(task.Status, "canceled") {
				return multicaIssueTask{}, fmt.Errorf("multica task %s ended %s before session/workdir were pinned", task.ID, task.Status)
			}
		}
		select {
		case <-pollCtx.Done():
			return multicaIssueTask{}, fmt.Errorf("multica task %s session/workdir were not pinned before timeout: %w", taskID, pollCtx.Err())
		case <-ticker.C:
		}
	}
}

func (p *MulticaProvider) resolveRuntimeID(ctx context.Context, workspaceID, candidate string) (string, error) {
	if configured := strings.TrimSpace(p.DefaultRuntimeID); configured != "" {
		return configured, nil
	}
	if candidate = strings.TrimSpace(candidate); candidate != "" {
		return candidate, nil
	}
	query := url.Values{}
	query.Set("workspace_id", workspaceID)
	var runtimes []multicaResourceResponse
	if err := p.do(ctx, http.MethodGet, "/api/runtimes?"+query.Encode(), nil, &runtimes); err != nil {
		return "", err
	}
	online := make([]string, 0, len(runtimes))
	for _, runtime := range runtimes {
		if strings.EqualFold(runtime.Status, "online") && strings.TrimSpace(runtime.ID) != "" {
			online = append(online, strings.TrimSpace(runtime.ID))
		}
	}
	if len(online) == 1 {
		return online[0], nil
	}
	if len(online) > 1 {
		return "", errors.New("multiple online Multica runtimes are available; set ADRO_MULTICA_RUNTIME_ID")
	}
	if len(runtimes) == 1 && strings.TrimSpace(runtimes[0].ID) != "" {
		return strings.TrimSpace(runtimes[0].ID), nil
	}
	return "", errors.New("no unambiguous Multica runtime is available; set ADRO_MULTICA_RUNTIME_ID")
}

func (p *MulticaProvider) requireFeature(ctx context.Context, feature string) error {
	caps, err := p.Capabilities(ctx)
	if err != nil {
		return err
	}
	if caps.Supports(feature) {
		return nil
	}
	return &CapabilityError{Capability: feature, AdapterVersion: caps.AdapterVersion}
}
func (p *MulticaProvider) StartRun(ctx context.Context, c StartRunCommand) (RunBinding, error) {
	var out RunBinding
	err := p.do(ctx, http.MethodPost, "/api/runs", c, &out)
	// Multica's public API models execution as an issue task and exposes the
	// native rerun action, not a generic /api/runs resource. Keep the generic
	// contract for compatible gateways, then fall back only on a 404 and only
	// when the caller supplied the provider issue binding.
	if err != nil && ErrorCodeOf(err) == ErrorNotFound && c.ProviderIssueID != "" {
		var task struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			SessionID string `json:"session_id,omitempty"`
			WorkDir   string `json:"work_dir,omitempty"`
		}
		path, pathErr := p.workspaceScopedPath(ctx, "/api/issues/"+url.PathEscape(c.ProviderIssueID)+"/rerun")
		if pathErr != nil {
			return RunBinding{}, pathErr
		}
		request := map[string]any{}
		if c.Input != "" {
			request["input"] = c.Input
		}
		if c.SessionID != "" {
			request["session_id"] = c.SessionID
		}
		if c.ContextID != "" {
			request["context_id"] = c.ContextID
		}
		if c.ContextVersion > 0 {
			request["context_version"] = c.ContextVersion
		}
		if c.RepairAttempt > 0 {
			request["repair_attempt"] = c.RepairAttempt
		}
		if fallbackErr := p.do(ctx, http.MethodPost, path, request, &task); fallbackErr != nil {
			return RunBinding{}, fallbackErr
		}
		if task.ID == "" {
			return RunBinding{}, errors.New("multica rerun returned no task id")
		}
		confirmed, confirmErr := p.waitForIssueTask(ctx, c.ProviderIssueID, task.ID)
		if confirmErr != nil {
			return RunBinding{}, confirmErr
		}
		p.markAuthenticated()
		now := time.Now().UTC()
		sessionID, workDir := confirmed.continuationSession()
		return RunBinding{ID: task.ID, ProviderRunID: task.ID, SessionID: sessionID, WorkDir: workDir, ContextID: c.ContextID, ContextVersion: c.ContextVersion, SessionReused: c.SessionID != "" && sessionID == c.SessionID, StartedAt: now}, nil
	}
	if err == nil {
		p.markAuthenticated()
	}
	return out, err
}

// multicaIssueTask is the provider metadata needed to prove that an
// asynchronously enqueued comment task will resume the original checkout.
// Current Multica deployments expose the resume pointer as prior_* while the
// task is queued; compatible gateways may expose the live session/work_dir
// directly once the daemon has claimed it.
type multicaIssueTask struct {
	ID               string `json:"id"`
	AgentID          string `json:"agent_id"`
	TriggerCommentID string `json:"trigger_comment_id"`
	Status           string `json:"status"`
	SessionID        string `json:"session_id"`
	WorkDir          string `json:"work_dir"`
	PriorSessionID   string `json:"prior_session_id"`
	PriorWorkDir     string `json:"prior_work_dir"`
}

func (t multicaIssueTask) continuationSession() (string, string) {
	sessionID, workDir := strings.TrimSpace(t.SessionID), strings.TrimSpace(t.WorkDir)
	if sessionID == "" {
		sessionID = strings.TrimSpace(t.PriorSessionID)
	}
	if workDir == "" {
		workDir = strings.TrimSpace(t.PriorWorkDir)
	}
	return sessionID, workDir
}

// ContinueWorkItem uses a real Multica comment-triggered task. Multica's
// manual /rerun endpoint intentionally forces a fresh session, while an agent
// mention on the same issue follows its normal (agent, issue) resume lookup.
// The comment response is only an acknowledgement: this method polls the
// issue task history and returns only after the actual task, session, and
// workdir have been verified.
func (p *MulticaProvider) ContinueWorkItem(ctx context.Context, command ContinuationCommand) (RunBinding, error) {
	issueID := strings.TrimSpace(command.IssueID)
	agentID := strings.TrimSpace(command.AgentID)
	input := strings.TrimSpace(command.Input)
	expectedSessionID := strings.TrimSpace(command.ExpectedSessionID)
	expectedWorkDir := strings.TrimSpace(command.ExpectedWorkDir)
	if issueID == "" || agentID == "" || input == "" || expectedSessionID == "" || expectedWorkDir == "" {
		return RunBinding{}, errors.New("multica continuation requires issue id, agent id, input, expected session and expected workdir")
	}
	if !uuidPattern.MatchString(agentID) {
		return RunBinding{}, errors.New("multica continuation agent id must be a UUID")
	}
	request := map[string]any{
		"type":    "comment",
		"content": fmt.Sprintf("[@ADRO development agent](mention://agent/%s)\n\n%s", agentID, input),
	}
	var out struct {
		ID string `json:"id"`
	}
	commentPath, err := p.workspaceScopedPath(ctx, "/api/issues/"+url.PathEscape(issueID)+"/comments")
	if err != nil {
		return RunBinding{}, err
	}
	if err := p.do(ctx, http.MethodPost, commentPath, request, &out); err != nil {
		return RunBinding{}, err
	}
	if strings.TrimSpace(out.ID) == "" {
		return RunBinding{}, errors.New("multica continuation comment returned no id")
	}
	p.markAuthenticated()

	pollCtx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		pollCtx, cancel = context.WithTimeout(ctx, 45*time.Second)
	}
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var tasks []multicaIssueTask
		taskPath, pathErr := p.workspaceScopedPath(pollCtx, "/api/issues/"+url.PathEscape(issueID)+"/task-runs")
		if pathErr != nil {
			return RunBinding{}, pathErr
		}
		err := p.do(pollCtx, http.MethodGet, taskPath, nil, &tasks)
		if err != nil {
			return RunBinding{}, err
		}
		for _, task := range tasks {
			if strings.TrimSpace(task.ID) == "" || strings.TrimSpace(task.ID) == strings.TrimSpace(out.ID) {
				continue
			}
			if strings.TrimSpace(task.TriggerCommentID) != strings.TrimSpace(out.ID) || strings.TrimSpace(task.AgentID) != agentID {
				continue
			}
			actualSessionID, actualWorkDir := task.continuationSession()
			if actualSessionID == "" || actualWorkDir == "" {
				if strings.EqualFold(task.Status, "failed") || strings.EqualFold(task.Status, "cancelled") || strings.EqualFold(task.Status, "canceled") {
					return RunBinding{}, fmt.Errorf("multica continuation task %s ended %s before session/workdir were pinned", task.ID, task.Status)
				}
				continue
			}
			if actualSessionID != expectedSessionID || actualWorkDir != expectedWorkDir {
				return RunBinding{}, fmt.Errorf("multica continuation task %s selected session/workdir different from the original", task.ID)
			}
			now := time.Now().UTC()
			return RunBinding{ID: task.ID, ProviderRunID: task.ID, SessionID: actualSessionID, WorkDir: actualWorkDir, SessionReused: true, StartedAt: now}, nil
		}
		select {
		case <-pollCtx.Done():
			return RunBinding{}, fmt.Errorf("multica continuation task for comment %s was not confirmed before timeout: %w", out.ID, pollCtx.Err())
		case <-ticker.C:
		}
	}
}
func (p *MulticaProvider) AppendInput(ctx context.Context, id, input string) error {
	if err := p.requireFeature(ctx, "run.messages.v1"); err != nil {
		return err
	}
	err := p.do(ctx, http.MethodPost, "/api/runs/"+id+"/messages", map[string]string{"input": input}, nil)
	if err == nil {
		p.markAuthenticated()
	}
	return err
}
func (p *MulticaProvider) CancelRun(ctx context.Context, id string) error {
	if err := p.requireFeature(ctx, "run.cancel.v1"); err != nil {
		return err
	}
	err := p.do(ctx, http.MethodPost, "/api/runs/"+id+"/cancel", nil, nil)
	if err == nil {
		p.markAuthenticated()
	}
	return err
}
func (p *MulticaProvider) GetRun(ctx context.Context, id string) (RunSnapshot, error) {
	if err := p.requireFeature(ctx, "run.snapshot.v1"); err != nil {
		return RunSnapshot{}, err
	}
	var out RunSnapshot
	err := p.do(ctx, http.MethodGet, "/api/runs/"+id, nil, &out)
	return out, err
}
func (p *MulticaProvider) StreamEvents(ctx context.Context, id, cursor string) (EventStream, error) {
	if p.WebSocketURL != "" {
		return p.streamWebSocket(ctx, id, cursor)
	}
	if err := p.requireFeature(ctx, "events.message.v1"); err != nil {
		return EventStream{}, err
	}
	var out struct {
		Items []events.Envelope `json:"items"`
	}
	path := "/api/runs/" + id + "/events"
	if cursor != "" {
		path += "?cursor=" + cursor
	}
	if err := p.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return EventStream{}, err
	}
	ch := make(chan events.Envelope, len(out.Items))
	for _, e := range out.Items {
		ch <- e
	}
	close(ch)
	return EventStream{Events: ch, Close: func() {}}, nil
}

func (p *MulticaProvider) streamWebSocket(ctx context.Context, id, cursor string) (EventStream, error) {
	target, err := url.Parse(p.WebSocketURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return EventStream{}, errors.New("invalid multica websocket URL")
	}
	query := target.Query()
	// The daemon WebSocket is shared by runtimes. When the caller configured a
	// default runtime but the URL is otherwise unscoped, keep the subscription
	// tenant-specific so events from another online runtime cannot be treated
	// as this run's provenance.
	if !query.Has("runtime_id") && !query.Has("runtime_ids") {
		if runtimeID := strings.TrimSpace(p.DefaultRuntimeID); runtimeID != "" {
			query.Set("runtime_ids", runtimeID)
		}
	}
	query.Set("run_id", id)
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	target.RawQuery = query.Encode()
	header := http.Header{}
	if p.Token != "" {
		header.Set("Authorization", "Bearer "+p.Token)
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, target.String(), header)
	if err != nil {
		return EventStream{}, err
	}
	ch := make(chan events.Envelope, 32)
	streamCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(ch)
		defer conn.Close()
		for {
			_, data, readErr := conn.ReadMessage()
			if readErr != nil {
				return
			}
			var event events.Envelope
			if json.Unmarshal(data, &event) != nil || event.EventID == "" {
				continue
			}
			select {
			case ch <- event:
			case <-streamCtx.Done():
				return
			}
		}
	}()
	go func() {
		<-streamCtx.Done()
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "context canceled"), time.Now().Add(time.Second))
		_ = conn.Close()
	}()
	return EventStream{Events: ch, Close: cancel}, nil
}
func (p *MulticaProvider) GetUsage(ctx context.Context, id string) (Usage, error) {
	if err := p.requireFeature(ctx, "run.usage.v1"); err != nil {
		return Usage{}, err
	}
	var out Usage
	err := p.do(ctx, http.MethodGet, "/api/runs/"+id+"/usage", nil, &out)
	return out, err
}
func (p *MulticaProvider) Health(ctx context.Context) (ProviderHealth, error) {
	paths := []string{"/readyz", "/healthz", "/health"}
	var lastErr error
	for _, path := range paths {
		var raw multicaHealthResponse
		err := p.do(ctx, http.MethodGet, path, nil, &raw)
		if err != nil {
			lastErr = err
			if ErrorCodeOf(err) == ErrorNotFound {
				continue
			}
			return ProviderHealth{}, err
		}

		healthy := raw.Healthy != nil && *raw.Healthy
		if raw.Healthy == nil {
			healthy = raw.Status == "ok" || raw.Status == "ready"
		}
		message := ""
		if !healthy {
			message = "provider health check failed"
		}
		return ProviderHealth{Healthy: healthy, Message: message}, nil
	}
	return ProviderHealth{}, lastErr
}

func (p *MulticaProvider) PublishAttachment(ctx context.Context, spec AttachmentSpec) (AttachmentReceipt, error) {
	if spec.TargetType == "" || spec.TargetID == "" {
		return AttachmentReceipt{}, errors.New("attachment target is required")
	}
	if len(spec.Content) == 0 {
		return AttachmentReceipt{}, errors.New("attachment content is required")
	}
	path := p.AttachmentPath
	if path == "" {
		path = "/api/attachments"
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("target_type", spec.TargetType)
	_ = writer.WriteField("target_id", spec.TargetID)
	_ = writer.WriteField("artifact_uri", spec.ArtifactURI)
	part, err := writer.CreateFormFile("file", safeFilename(spec.Filename))
	if err != nil {
		return AttachmentReceipt{}, err
	}
	if _, err := part.Write(spec.Content); err != nil {
		return AttachmentReceipt{}, err
	}
	if err := writer.Close(); err != nil {
		return AttachmentReceipt{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+path, &body)
	if err != nil {
		return AttachmentReceipt{}, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if p.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.Token)
	}
	req.Header.Set("X-Request-ID", fmt.Sprintf("adro-%d", time.Now().UnixNano()))
	resp, err := p.client().Do(req)
	if err != nil {
		return AttachmentReceipt{}, transportError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AttachmentReceipt{}, errorForStatus(resp.StatusCode)
	}
	var receipt AttachmentReceipt
	if err := json.NewDecoder(resp.Body).Decode(&receipt); err != nil && err != io.EOF {
		return AttachmentReceipt{}, &UpstreamError{Code: ErrorInvalidResponse}
	}
	if receipt.Status == "" {
		receipt.Status = "accepted"
	}
	p.markAuthenticated()
	receipt.ArtifactURI = spec.ArtifactURI
	return receipt, nil
}

func safeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "screenshot.png"
	}
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	return name
}
