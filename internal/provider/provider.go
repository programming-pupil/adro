// Package provider defines the execution SPI. Business code depends on this
// interface, so the local executable boundary can be replaced by another
// execution backend without changing business code.
package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/events"
)

type Capabilities struct {
	Provider       string            `json:"provider"`
	AdapterVersion string            `json:"adapter_version"`
	ServerVersion  string            `json:"server_version"`
	Features       []string          `json:"capabilities"`
	FeatureStatus  map[string]string `json:"feature_status,omitempty"`
}

func (c Capabilities) Supports(feature string) bool {
	if c.FeatureStatus != nil {
		status, ok := c.FeatureStatus[feature]
		if ok {
			return status == "supported" || status == "verified" || status == "true"
		}
	}
	for _, candidate := range c.Features {
		if candidate == feature {
			return true
		}
	}
	return false
}

// UnmarshalJSON accepts the capability shapes used by deployed providers:
// arrays under "capabilities" or "features", and status maps such as
// {"features":{"run.snapshot.v1":"unsupported"}}. Keeping the wire
// compatibility here lets the API make capability decisions without guessing
// from reachability or adapter identity.
func (c *Capabilities) UnmarshalJSON(data []byte) error {
	type wire struct {
		Provider       string            `json:"provider"`
		AdapterVersion string            `json:"adapter_version"`
		ServerVersion  string            `json:"server_version"`
		Capabilities   json.RawMessage   `json:"capabilities"`
		Features       json.RawMessage   `json:"features"`
		FeatureStatus  map[string]string `json:"feature_status"`
	}
	var payload wire
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	features := []string{}
	statuses := map[string]string{}
	merge := func(raw json.RawMessage) error {
		if len(raw) == 0 || string(raw) == "null" {
			return nil
		}
		var list []string
		if err := json.Unmarshal(raw, &list); err == nil {
			for _, feature := range list {
				if feature == "" {
					continue
				}
				seen := false
				for _, existing := range features {
					if existing == feature {
						seen = true
						break
					}
				}
				if !seen {
					features = append(features, feature)
				}
			}
			return nil
		}
		var statusMap map[string]json.RawMessage
		if err := json.Unmarshal(raw, &statusMap); err != nil {
			return fmt.Errorf("capability list or status map: %w", err)
		}
		for feature, value := range statusMap {
			var status string
			if err := json.Unmarshal(value, &status); err == nil {
				statuses[feature] = status
				continue
			}
			var supported bool
			if err := json.Unmarshal(value, &supported); err == nil {
				if supported {
					statuses[feature] = "supported"
				} else {
					statuses[feature] = "unsupported"
				}
				continue
			}
			return fmt.Errorf("invalid status for capability %q", feature)
		}
		return nil
	}
	if err := merge(payload.Capabilities); err != nil {
		return err
	}
	if err := merge(payload.Features); err != nil {
		return err
	}
	for feature, status := range payload.FeatureStatus {
		statuses[feature] = status
	}
	*c = Capabilities{Provider: payload.Provider, AdapterVersion: payload.AdapterVersion, ServerVersion: payload.ServerVersion, Features: features}
	if len(statuses) > 0 {
		c.FeatureStatus = statuses
	}
	return nil
}

type AgentSpec struct {
	ID           string `json:"id,omitempty"`
	WorkspaceID  string `json:"workspace_id,omitempty"`
	RuntimeID    string `json:"runtime_id,omitempty"`
	Name         string `json:"name"`
	Instructions string `json:"instructions,omitempty"`
}
type AgentBinding struct {
	ID              string    `json:"id,omitempty"`
	Provider        string    `json:"provider,omitempty"`
	ProviderAgentID string    `json:"provider_agent_id,omitempty"`
	AgentSpec       AgentSpec `json:"agent_spec,omitempty"`
}
type WorkspaceSpec struct {
	ID            string   `json:"id,omitempty"`
	Name          string   `json:"name"`
	RepositoryIDs []string `json:"repository_ids,omitempty"`
}
type WorkspaceBinding struct {
	ID                  string `json:"id,omitempty"`
	Provider            string `json:"provider,omitempty"`
	ProviderWorkspaceID string `json:"provider_workspace_id,omitempty"`
}
type WorkItemSpec struct {
	ID, RequirementID, RepositoryID, MemberID string
	// WorkspaceID and the display fields are provider-neutral. An execution
	// backend may use the optional binding fields when it supports native IDs.
	WorkspaceID           string `json:"workspace_id,omitempty"`
	Title                 string `json:"title,omitempty"`
	Description           string `json:"description,omitempty"`
	ParentProviderIssueID string `json:"parent_provider_issue_id,omitempty"`
	ProjectProviderID     string `json:"project_provider_id,omitempty"`
	ProviderAssigneeID    string `json:"provider_assignee_id,omitempty"`
	AssigneeType          string `json:"assignee_type,omitempty"`
	Stage                 int    `json:"stage,omitempty"`
	// RepositoryPath and CloneURL are optional local execution inputs. They are
	// copied into an isolated workdir by LocalProvider; remote plugins may
	// ignore them and use their own source-control binding.
	RepositoryPath string `json:"repository_path,omitempty"`
	CloneURL       string `json:"clone_url,omitempty"`
	DefaultBranch  string `json:"default_branch,omitempty"`
}
type ProviderWorkItem struct {
	ID              string `json:"id,omitempty"`
	ProviderIssueID string `json:"provider_issue_id,omitempty"`
}
type StartRunCommand struct {
	WorkItemID         string `json:"work_item_id"`
	AgentBindingID     string `json:"agent_binding_id,omitempty"`
	ProviderAssigneeID string `json:"provider_assignee_id,omitempty"`
	Input              string `json:"input,omitempty"`
	// ProviderIssueID lets adapters use the provider's native issue/task
	// lifecycle when it does not expose a generic /runs endpoint.
	ProviderIssueID string `json:"provider_issue_id,omitempty"`
	// SessionID and ContextID are supplied for repairs. They make continuity an
	// explicit contract rather than relying on provider-side issue heuristics.
	SessionID      string `json:"session_id,omitempty"`
	ContextID      string `json:"context_id,omitempty"`
	ContextVersion int64  `json:"context_version,omitempty"`
	RepairAttempt  int    `json:"repair_attempt,omitempty"`
	// IdempotencyKey is owned by ADRO's durable harness. Providers must return
	// the original run for an identical key instead of creating a duplicate
	// side effect after a lost response or API restart.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}
type RunBinding struct {
	ID             string    `json:"id,omitempty"`
	ProviderRunID  string    `json:"provider_run_id,omitempty"`
	SessionID      string    `json:"session_id,omitempty"`
	WorkDir        string    `json:"work_dir,omitempty"`
	ContextID      string    `json:"context_id,omitempty"`
	ContextVersion int64     `json:"context_version,omitempty"`
	SessionReused  bool      `json:"session_reused,omitempty"`
	StartedAt      time.Time `json:"started_at"`
}

// ContinuationCommand describes an incremental follow-up on an existing
// provider work item. The expected session and work directory are supplied by
// ADRO's durable pipeline state; a provider must prove both values before the
// continuation is considered started.
type ContinuationCommand struct {
	IssueID           string `json:"issue_id"`
	AgentID           string `json:"agent_id"`
	Input             string `json:"input"`
	ExpectedSessionID string `json:"expected_session_id"`
	ExpectedWorkDir   string `json:"expected_work_dir"`
	IdempotencyKey    string `json:"idempotency_key,omitempty"`
}
type RunSnapshot struct {
	ID               string         `json:"id"`
	WorkItemID       string         `json:"work_item_id,omitempty"`
	ProviderIssueID  string         `json:"provider_issue_id,omitempty"`
	InputHash        string         `json:"input_hash,omitempty"`
	Status           string         `json:"status"`
	LastEventID      string         `json:"last_event_id,omitempty"`
	SessionID        string         `json:"session_id,omitempty"`
	SessionContinuity string         `json:"session_continuity,omitempty"`
	WorkDir          string         `json:"work_dir,omitempty"`
	BaselineCommit   string         `json:"baseline_commit,omitempty"`
	HeadCommit       string         `json:"head_commit,omitempty"`
	SubmissionURL    string         `json:"submission_url,omitempty"`
	ChecksConclusion string         `json:"checks_conclusion,omitempty"`
	Output           string         `json:"output,omitempty"`
	Error            string         `json:"error,omitempty"`
	WorkspaceDirty   bool           `json:"workspace_dirty,omitempty"`
	ChangedFiles     []string       `json:"changed_files,omitempty"`
	StartedAt        *time.Time     `json:"started_at,omitempty"`
	FinishedAt       *time.Time     `json:"finished_at,omitempty"`
	Usage            Usage          `json:"usage"`
	ToolEvents       []ToolEvent    `json:"tool_events,omitempty"`
	Interactions     []Interaction  `json:"interactions,omitempty"`
	Ledger           []RuntimeEvent `json:"ledger,omitempty"`
}

// ToolEvent is provider-neutral evidence extracted from a structured executor
// stream. A pair with the same CallID forms one automatic harness checkpoint
// boundary; unpaired events are retained as evidence but never acknowledged
// as a completed side effect.
type ToolEvent struct {
	CallID   string `json:"call_id"`
	Name     string `json:"name,omitempty"`
	Phase    string `json:"phase"`
	Payload  string `json:"payload,omitempty"`
	Sequence int    `json:"sequence"`
}

// Interaction is a durable user input accepted by a running provider. The
// sequence and hash make retries/restarts idempotent and auditable even when
// the underlying CLI does not support interactive stdin.
type Interaction struct {
	ID             string    `json:"id"`
	RunID          string    `json:"run_id"`
	Sequence       int64     `json:"sequence"`
	Input          string    `json:"input"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	Hash           string    `json:"hash"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

// RuntimeEvent is the provider runtime ledger. It is intentionally small and
// provider-neutral: adapters can replay lifecycle, interaction and recovery
// facts without trusting an ephemeral process or log stream.
type RuntimeEvent struct {
	ID        string         `json:"id"`
	RunID     string         `json:"run_id"`
	Sequence  int64          `json:"sequence"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload,omitempty"`
	PrevHash  string         `json:"prev_hash,omitempty"`
	Hash      string         `json:"hash"`
	CreatedAt time.Time      `json:"created_at"`
}
type Usage struct {
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	DurationMS       int64   `json:"duration_ms"`
	EstimatedCost    float64 `json:"estimated_cost"`
}
type ProviderHealth struct {
	Healthy bool   `json:"healthy"`
	Message string `json:"message,omitempty"`
}

// AttachmentSpec is the provider-neutral payload used when a UI or extension
// publishes an image or other evidence to an execution target.
// Content is kept in memory only for the duration of the request and is never
// persisted in the control-plane event stream.
type AttachmentSpec struct {
	TargetType  string `json:"target_type"`
	TargetID    string `json:"target_id"`
	Filename    string `json:"filename"`
	MediaType   string `json:"media_type"`
	ArtifactURI string `json:"artifact_uri"`
	Content     []byte `json:"-"`
}

type AttachmentReceipt struct {
	ID                   string `json:"id"`
	ProviderAttachmentID string `json:"provider_attachment_id,omitempty"`
	Status               string `json:"status"`
	ArtifactURI          string `json:"artifact_uri,omitempty"`
}

// AttachmentPublisher is optional so execution-only backends can omit remote
// delivery while providers that implement it expose evidence publishing.
type AttachmentPublisher interface {
	PublishAttachment(context.Context, AttachmentSpec) (AttachmentReceipt, error)
}

// ContinuityProvider starts an incremental follow-up on an existing native
// work item. Unlike a manual rerun, it must preserve the provider conversation
// and work directory. The pipeline verifies the returned stage result against
// the originally pinned session ID.
type ContinuityProvider interface {
	ContinueWorkItem(context.Context, ContinuationCommand) (RunBinding, error)
}

// InputKeyProvider optionally exposes idempotent interaction delivery. The
// base AppendInput method remains source-compatible with older adapters.
type InputKeyProvider interface {
	AppendInputWithKey(context.Context, string, string, string) error
}
type EventStream struct {
	Events <-chan events.Envelope
	Close  func()
}

type ExecutionProvider interface {
	Capabilities(context.Context) (Capabilities, error)
	EnsureAgent(context.Context, AgentSpec) (AgentBinding, error)
	EnsureTeamWorkspace(context.Context, WorkspaceSpec) (WorkspaceBinding, error)
	CreateWorkItem(context.Context, WorkItemSpec) (ProviderWorkItem, error)
	StartRun(context.Context, StartRunCommand) (RunBinding, error)
	AppendInput(context.Context, string, string) error
	CancelRun(context.Context, string) error
	GetRun(context.Context, string) (RunSnapshot, error)
	StreamEvents(context.Context, string, string) (EventStream, error)
	GetUsage(context.Context, string) (Usage, error)
	Health(context.Context) (ProviderHealth, error)
}

type mockRun struct {
	snapshot RunSnapshot
	cancel   context.CancelFunc
}

// MockProvider is deterministic and has no network or model dependency. It is
// used by `adroctl up --demo` and provider contract tests.
type MockProvider struct {
	mu          sync.Mutex
	runs        map[string]*mockRun
	runKeys     map[string]string
	bus         *events.Bus
	caps        Capabilities
	attachments []AttachmentSpec
}

func NewMockProvider(bus *events.Bus) *MockProvider {
	return &MockProvider{runs: make(map[string]*mockRun), runKeys: make(map[string]string), bus: bus, caps: Capabilities{
		Provider: "mock", AdapterVersion: "1.0.0", ServerVersion: "local",
		Features: []string{"agent.v1", "project.resources.v1", "issue.child.v1", "run.messages.v1", "runtime.worktree.v1", "usage.tokens.v1"},
	}}
}
func (p *MockProvider) Capabilities(context.Context) (Capabilities, error) { return p.caps, nil }
func (p *MockProvider) EnsureAgent(_ context.Context, s AgentSpec) (AgentBinding, error) {
	if s.ID == "" {
		s.ID = domain.NewID()
	}
	return AgentBinding{ID: s.ID, Provider: "mock", ProviderAgentID: "mock-agent-" + s.ID, AgentSpec: s}, nil
}
func (p *MockProvider) EnsureTeamWorkspace(_ context.Context, s WorkspaceSpec) (WorkspaceBinding, error) {
	if s.ID == "" {
		return WorkspaceBinding{}, errors.New("workspace id is required")
	}
	return WorkspaceBinding{ID: s.ID, Provider: "mock", ProviderWorkspaceID: "mock-workspace-" + s.ID}, nil
}
func (p *MockProvider) CreateWorkItem(_ context.Context, s WorkItemSpec) (ProviderWorkItem, error) {
	if s.ID == "" {
		return ProviderWorkItem{}, errors.New("work item id is required")
	}
	return ProviderWorkItem{ID: s.ID, ProviderIssueID: "mock-issue-" + s.ID}, nil
}
func (p *MockProvider) StartRun(ctx context.Context, cmd StartRunCommand) (RunBinding, error) {
	if cmd.WorkItemID == "" {
		return RunBinding{}, errors.New("work item id is required")
	}
	id := domain.NewID()
	now := time.Now().UTC()
	sessionID := cmd.SessionID
	if sessionID == "" {
		sessionID = "mock-session-" + id
	}
	p.mu.Lock()
	if key := strings.TrimSpace(cmd.IdempotencyKey); key != "" {
		if existingID := p.runKeys[cmd.WorkItemID+"\x00"+key]; existingID != "" {
			if existing := p.runs[existingID]; existing != nil {
				if existing.snapshot.InputHash != "" && existing.snapshot.InputHash != sha256Hex(cmd.Input) {
					p.mu.Unlock()
					return RunBinding{}, fmt.Errorf("%w: idempotency key maps to different input", ErrConflict)
				}
				p.mu.Unlock()
				return RunBinding{ID: existing.snapshot.ID, ProviderRunID: "mock-run-" + existing.snapshot.ID, SessionID: existing.snapshot.SessionID, ContextID: cmd.ContextID, ContextVersion: cmd.ContextVersion, SessionReused: cmd.SessionID != "", StartedAt: *existing.snapshot.StartedAt}, nil
			}
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	p.runs[id] = &mockRun{snapshot: RunSnapshot{ID: id, WorkItemID: cmd.WorkItemID, InputHash: sha256Hex(cmd.Input), SessionID: sessionID, Status: "running", StartedAt: &now}, cancel: cancel}
	if key := strings.TrimSpace(cmd.IdempotencyKey); key != "" {
		p.runKeys[cmd.WorkItemID+"\x00"+key] = id
	}
	p.mu.Unlock()
	inputDigest := sha256.Sum256([]byte(cmd.Input))
	_ = p.bus.Publish(runCtx, events.New("execution.started.v1", "execution_run", id, "", "", 1, map[string]any{"work_item_id": cmd.WorkItemID, "input_sha256": hex.EncodeToString(inputDigest[:]), "input_bytes": len(cmd.Input)}))
	go func() {
		select {
		case <-time.After(10 * time.Millisecond):
			p.mu.Lock()
			r := p.runs[id]
			if r != nil && r.snapshot.Status == "running" {
				done := time.Now().UTC()
				r.snapshot.Status = "completed"
				r.snapshot.FinishedAt = &done
				r.snapshot.LastEventID = domain.NewID()
			}
			p.mu.Unlock()
			_ = p.bus.Publish(context.Background(), events.New("execution.completed.v1", "execution_run", id, "", "", 2, map[string]any{"work_item_id": cmd.WorkItemID}))
		case <-runCtx.Done():
		}
	}()
	return RunBinding{ID: id, ProviderRunID: "mock-run-" + id, SessionID: sessionID, ContextID: cmd.ContextID, ContextVersion: cmd.ContextVersion, SessionReused: cmd.SessionID != "", StartedAt: now}, nil
}
func (p *MockProvider) AppendInput(_ context.Context, runID, input string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.runs[runID]
	if !ok {
		return errors.New("run not found")
	}
	if r.snapshot.Status != "running" {
		return errors.New("run is not running")
	}
	_ = input
	return nil
}
func (p *MockProvider) CancelRun(_ context.Context, runID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.runs[runID]
	if !ok {
		return errors.New("run not found")
	}
	r.cancel()
	r.snapshot.Status = "cancelled"
	now := time.Now().UTC()
	r.snapshot.FinishedAt = &now
	return nil
}
func (p *MockProvider) GetRun(_ context.Context, runID string) (RunSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	lookupID := runID
	if strings.HasPrefix(lookupID, "mock-run-") {
		lookupID = strings.TrimPrefix(lookupID, "mock-run-")
	}
	r, ok := p.runs[lookupID]
	if !ok {
		return RunSnapshot{}, fmt.Errorf("run %q not found", runID)
	}
	return r.snapshot, nil
}
func (p *MockProvider) StreamEvents(ctx context.Context, runID, cursor string) (EventStream, error) {
	if _, err := p.GetRun(ctx, runID); err != nil {
		return EventStream{}, err
	}
	ch, cancel := p.bus.Subscribe(32)
	_ = cursor
	return EventStream{Events: ch, Close: cancel}, nil
}
func (p *MockProvider) GetUsage(ctx context.Context, runID string) (Usage, error) {
	r, err := p.GetRun(ctx, runID)
	return r.Usage, err
}
func (p *MockProvider) Health(context.Context) (ProviderHealth, error) {
	return ProviderHealth{Healthy: true, Message: "mock provider ready"}, nil
}

func (p *MockProvider) PublishAttachment(_ context.Context, spec AttachmentSpec) (AttachmentReceipt, error) {
	if spec.TargetType == "" || spec.TargetID == "" {
		return AttachmentReceipt{}, errors.New("attachment target is required")
	}
	if len(spec.Content) == 0 {
		return AttachmentReceipt{}, errors.New("attachment content is required")
	}
	p.mu.Lock()
	p.attachments = append(p.attachments, AttachmentSpec{
		TargetType: spec.TargetType, TargetID: spec.TargetID, Filename: spec.Filename,
		MediaType: spec.MediaType, ArtifactURI: spec.ArtifactURI, Content: append([]byte(nil), spec.Content...),
	})
	p.mu.Unlock()
	return AttachmentReceipt{ID: domain.NewID(), ProviderAttachmentID: "mock-attachment-" + domain.NewID(), Status: "accepted", ArtifactURI: spec.ArtifactURI}, nil
}

// AttachmentCount lets contract tests verify that a screenshot reached the
// provider boundary without exposing attachment bytes to application code.
func (p *MockProvider) AttachmentCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.attachments)
}
