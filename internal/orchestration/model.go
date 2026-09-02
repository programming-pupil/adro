// Package orchestration contains the provider-neutral, graph based execution
// contracts.  The package deliberately has no database or HTTP dependencies:
// facts can be persisted by any repository and projections can be rebuilt from
// the immutable plan, attempts and decisions.
package orchestration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/harness"
)

type AgentStatus string

const (
	AgentDraft    AgentStatus = "draft"
	AgentActive   AgentStatus = "active"
	AgentDisabled AgentStatus = "disabled"
	AgentArchived AgentStatus = "archived"
)

type CapabilityRef struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}
type SchemaRef struct {
	ID      string `json:"id"`
	Version int64  `json:"version,omitempty"`
}
type Budget struct {
	Tokens     int64         `json:"tokens,omitempty"`
	ToolCalls  int           `json:"tool_calls,omitempty"`
	CostCents  int64         `json:"cost_cents,omitempty"`
	Duration   time.Duration `json:"duration,omitempty"`
	Concurrent int           `json:"concurrent,omitempty"`
}
type ToolPolicy struct {
	Allowed      []string `json:"allowed,omitempty"`
	Denied       []string `json:"denied,omitempty"`
	Network      bool     `json:"network,omitempty"`
	SecretScopes []string `json:"secret_scopes,omitempty"`
}
type MemoryPolicy struct {
	ReadScopes      []string `json:"read_scopes,omitempty"`
	WriteScopes     []string `json:"write_scopes,omitempty"`
	RequireEvidence bool     `json:"require_evidence,omitempty"`
}
type ExecutorBinding struct {
	ProviderID      string   `json:"provider_id"`
	ProviderVersion string   `json:"provider_version,omitempty"`
	BinaryDigest    string   `json:"binary_digest,omitempty"`
	RequiredCaps    []string `json:"required_caps,omitempty"`
	ConfigVersion   string   `json:"config_version,omitempty"`
}

type AgentDefinition struct {
	ID                string          `json:"id"`
	WorkspaceID       string          `json:"workspace_id"`
	Revision          int64           `json:"revision"`
	Name              string          `json:"name"`
	Role              string          `json:"role,omitempty"`
	Instructions      string          `json:"instructions,omitempty"`
	Capabilities      []CapabilityRef `json:"capabilities,omitempty"`
	ToolPolicy        ToolPolicy      `json:"tool_policy"`
	MemoryPolicy      MemoryPolicy    `json:"memory_policy"`
	ExecutorBinding   ExecutorBinding `json:"executor_binding"`
	ConcurrencyBudget Budget          `json:"concurrency_budget"`
	InputSchema       SchemaRef       `json:"input_schema"`
	OutputSchema      SchemaRef       `json:"output_schema"`
	Status            AgentStatus     `json:"status"`
	CreatedBy         string          `json:"created_by,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

type SquadStatus string

const (
	SquadDraft     SquadStatus = "draft"
	SquadPublished SquadStatus = "published"
	SquadDisabled  SquadStatus = "disabled"
	SquadArchived  SquadStatus = "archived"
)

type VersionedRef struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision,omitempty"`
	Version  int64  `json:"version,omitempty"`
}
type CapabilityConstraint struct {
	Name     string `json:"name"`
	Required bool   `json:"required,omitempty"`
}
type SquadMember struct {
	ID                    string                 `json:"id"`
	AgentID               string                 `json:"agent_id"`
	Role                  string                 `json:"role"`
	InputSchema           SchemaRef              `json:"input_schema"`
	OutputSchema          SchemaRef              `json:"output_schema"`
	CapabilityConstraints []CapabilityConstraint `json:"capability_constraints,omitempty"`
	MaxAttempts           int                    `json:"max_attempts,omitempty"`
	Budget                Budget                 `json:"budget,omitempty"`
	Optional              bool                   `json:"optional,omitempty"`
}
type SquadPolicy struct {
	MaxNestingDepth   int        `json:"max_nesting_depth,omitempty"`
	Budget            Budget     `json:"budget,omitempty"`
	ToolPolicy        ToolPolicy `json:"tool_policy,omitempty"`
	HumanExitRequired bool       `json:"human_exit_required,omitempty"`
}
type SquadDefinition struct {
	ID               string        `json:"id"`
	WorkspaceID      string        `json:"workspace_id"`
	Name             string        `json:"name"`
	Description      string        `json:"description,omitempty"`
	Revision         int64         `json:"revision"`
	PublishedVersion int64         `json:"published_version,omitempty"`
	Members          []SquadMember `json:"members"`
	Graph            WorkflowGraph `json:"graph"`
	Policy           SquadPolicy   `json:"policy"`
	Status           SquadStatus   `json:"status"`
}

type NodeKind string

const (
	NodeAgent  NodeKind = "agent"
	NodeSquad  NodeKind = "squad"
	NodeGate   NodeKind = "gate"
	NodeHuman  NodeKind = "human"
	NodeMerge  NodeKind = "merge"
	NodeRepair NodeKind = "repair"
)

type JoinPolicy string

const (
	JoinAll          JoinPolicy = "all"
	JoinQuorum       JoinPolicy = "quorum"
	JoinFirstSuccess JoinPolicy = "first_success"
)

type ContextPolicy struct {
	Required  []string `json:"required,omitempty"`
	Optional  []string `json:"optional,omitempty"`
	MaxTokens int64    `json:"max_tokens,omitempty"`
}
type RetryPolicy struct {
	MaxAttempts int           `json:"max_attempts,omitempty"`
	Backoff     time.Duration `json:"backoff,omitempty"`
	RetryOn     []string      `json:"retry_on,omitempty"`
}

type Predicate struct {
	Kind     string      `json:"kind"`
	Field    string      `json:"field,omitempty"`
	Op       string      `json:"op,omitempty"`
	Value    any         `json:"value,omitempty"`
	Children []Predicate `json:"children,omitempty"`
}
type EdgeEvent string

const (
	EdgeSuccess  EdgeEvent = "success"
	EdgeFailure  EdgeEvent = "failure"
	EdgeTimeout  EdgeEvent = "timeout"
	EdgeApproval EdgeEvent = "approval"
	EdgeBug      EdgeEvent = "bug"
	EdgeCancel   EdgeEvent = "cancel"
)

type WorkflowNode struct {
	ID             string        `json:"id"`
	Kind           NodeKind      `json:"kind"`
	AgentRef       *VersionedRef `json:"agent_ref,omitempty"`
	SquadRef       *VersionedRef `json:"squad_ref,omitempty"`
	InputContract  SchemaRef     `json:"input_contract"`
	OutputContract SchemaRef     `json:"output_contract"`
	ContextPolicy  ContextPolicy `json:"context_policy,omitempty"`
	ToolPolicy     ToolPolicy    `json:"tool_policy,omitempty"`
	RetryPolicy    RetryPolicy   `json:"retry_policy,omitempty"`
	Timeout        time.Duration `json:"timeout,omitempty"`
	Budget         Budget        `json:"budget,omitempty"`
	JoinPolicy     JoinPolicy    `json:"join_policy,omitempty"`
}
type WorkflowEdge struct {
	ID               string    `json:"id"`
	From             string    `json:"from"`
	To               string    `json:"to"`
	On               EdgeEvent `json:"on"`
	Predicate        Predicate `json:"predicate,omitempty"`
	Priority         int       `json:"priority,omitempty"`
	MaxTraversals    int       `json:"max_traversals,omitempty"`
	RequiredEvidence []string  `json:"required_evidence,omitempty"`
	LoopGroup        string    `json:"loop_group,omitempty"`
}
type WorkflowGraph struct {
	ID               string         `json:"id"`
	Version          int64          `json:"version"`
	EntryNodeIDs     []string       `json:"entry_node_ids"`
	ExitNodeIDs      []string       `json:"exit_node_ids"`
	Nodes            []WorkflowNode `json:"nodes"`
	Edges            []WorkflowEdge `json:"edges"`
	ValidationDigest string         `json:"validation_digest,omitempty"`
}

type PolicySnapshot struct {
	Digest     string     `json:"digest"`
	ToolPolicy ToolPolicy `json:"tool_policy,omitempty"`
	Budget     Budget     `json:"budget,omitempty"`
	CapturedAt time.Time  `json:"captured_at"`
}
type ContextRef struct {
	SessionID      string `json:"session_id"`
	ManifestDigest string `json:"manifest_digest"`
	ReplayKey      string `json:"replay_key,omitempty"`
}
type PlanStatus string

const (
	PlanDraft      PlanStatus = "draft"
	PlanValidating PlanStatus = "validating"
	PlanReady      PlanStatus = "ready"
	PlanWaiting    PlanStatus = "waiting"
	PlanRunning    PlanStatus = "running"
	PlanTerminal   PlanStatus = "terminal"
)

type RequirementExecutionPlan struct {
	ID             string         `json:"id"`
	RequirementID  string         `json:"requirement_id"`
	WorkspaceID    string         `json:"workspace_id"`
	GraphSnapshot  WorkflowGraph  `json:"graph_snapshot"`
	SelectedRef    VersionedRef   `json:"selected_ref"`
	PolicySnapshot PolicySnapshot `json:"policy_snapshot"`
	ContextRoot    ContextRef     `json:"context_root"`
	PlanHash       string         `json:"plan_hash"`
	Status         PlanStatus     `json:"status"`
	Revision       int64          `json:"revision"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

type Lease struct {
	Key          string    `json:"key"`
	Owner        string    `json:"owner"`
	FencingToken int64     `json:"fencing_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}
type StructuredResult struct {
	Outcome     string         `json:"outcome"`
	Fields      map[string]any `json:"fields,omitempty"`
	Summary     string         `json:"summary,omitempty"`
	EvidenceIDs []string       `json:"evidence_ids,omitempty"`
}
type AttemptStatus string

const (
	AttemptPending   AttemptStatus = "pending"
	AttemptReady     AttemptStatus = "ready"
	AttemptRunning   AttemptStatus = "running"
	AttemptWaiting   AttemptStatus = "waiting"
	AttemptPassed    AttemptStatus = "passed"
	AttemptFailed    AttemptStatus = "failed"
	AttemptCancelled AttemptStatus = "cancelled"
	AttemptTimedOut  AttemptStatus = "timed_out"
)

type NodeAttempt struct {
	ID              string                  `json:"id"`
	PlanID          string                  `json:"plan_id"`
	NodeID          string                  `json:"node_id"`
	AttemptNo       int                     `json:"attempt_no"`
	RunID           string                  `json:"run_id,omitempty"`
	SessionID       string                  `json:"session_id,omitempty"`
	WorkDir         string                  `json:"workdir,omitempty"`
	Lease           Lease                   `json:"lease,omitempty"`
	IdempotencyKey  string                  `json:"idempotency_key,omitempty"`
	InputManifest   harness.ContextEnvelope `json:"input_manifest"`
	OutputArtifacts []string                `json:"output_artifacts,omitempty"`
	Result          StructuredResult        `json:"result,omitempty"`
	Status          AttemptStatus           `json:"status"`
	FailureReason   *FailureReason          `json:"failure_reason,omitempty"`
	StartedAt       *time.Time              `json:"started_at,omitempty"`
	FinishedAt      *time.Time              `json:"finished_at,omitempty"`
}
type FailureReason struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}
type FeedbackDecision struct {
	ID               string           `json:"id"`
	PlanID           string           `json:"plan_id"`
	SourceAttempt    string           `json:"source_attempt"`
	SourceNode       string           `json:"source_node"`
	TargetNode       string           `json:"target_node"`
	EdgeID           string           `json:"edge_id"`
	StructuredResult StructuredResult `json:"structured_result"`
	EvidenceIDs      []string         `json:"evidence_ids,omitempty"`
	Reason           string           `json:"reason"`
	LoopCount        int              `json:"loop_count"`
	IdempotencyKey   string           `json:"idempotency_key"`
}

func (g WorkflowGraph) CanonicalHash() (string, error) {
	b, err := json.Marshal(g)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func (p RequirementExecutionPlan) Freeze() (RequirementExecutionPlan, error) {
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.RequirementID) == "" || strings.TrimSpace(p.WorkspaceID) == "" {
		return p, errors.New("plan id, requirement_id and workspace_id are required")
	}
	if err := ValidateGraph(p.GraphSnapshot); err != nil {
		return p, err
	}
	if p.Status != PlanDraft && p.Status != PlanValidating {
		return p, errors.New("execution plan is immutable")
	}
	h, err := canonicalPlanHash(p)
	if err != nil {
		return p, err
	}
	p.PlanHash = h
	p.Status = PlanReady
	p.Revision = 1
	return p, nil
}
func canonicalPlanHash(p RequirementExecutionPlan) (string, error) {
	cp := p
	cp.PlanHash = ""
	cp.Status = ""
	cp.Revision = 0
	cp.CreatedAt = time.Time{}
	b, e := json.Marshal(cp)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func (a AgentDefinition) Validate() error {
	if strings.TrimSpace(a.ID) == "" || strings.TrimSpace(a.WorkspaceID) == "" || strings.TrimSpace(a.Name) == "" {
		return errors.New("agent id, workspace_id and name are required")
	}
	if a.Revision < 1 {
		return errors.New("agent revision must be positive")
	}
	if a.Status == AgentActive && (strings.TrimSpace(a.ExecutorBinding.ProviderID) == "" || strings.TrimSpace(a.InputSchema.ID) == "" || strings.TrimSpace(a.OutputSchema.ID) == "") {
		return errors.New("active agent requires executor binding and schemas")
	}
	return nil
}
func (m SquadMember) Validate() error {
	if strings.TrimSpace(m.ID) == "" || strings.TrimSpace(m.AgentID) == "" || strings.TrimSpace(m.Role) == "" {
		return errors.New("squad member id, agent_id and role are required")
	}
	if m.MaxAttempts < 0 {
		return errors.New("max_attempts cannot be negative")
	}
	return nil
}
func (s SquadDefinition) Validate() error {
	if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.WorkspaceID) == "" || strings.TrimSpace(s.Name) == "" {
		return errors.New("squad id, workspace_id and name are required")
	}
	seen := map[string]bool{}
	for i, m := range s.Members {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("members[%d]: %w", i, err)
		}
		if seen[m.ID] {
			return fmt.Errorf("members[%d].id.duplicate", i)
		}
		seen[m.ID] = true
	}
	return ValidateGraph(s.Graph)
}
