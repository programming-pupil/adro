// Package domain contains ADRO's provider-independent business contracts.
package domain

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// NewID returns a sortable-enough opaque identifier without coupling the
// domain to a database or a particular UUID library.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

type RequirementStatus string

const (
	RequirementReceived             RequirementStatus = "RECEIVED"
	RequirementTriaged              RequirementStatus = "TRIAGED"
	RequirementAssigneesConfirmed   RequirementStatus = "ASSIGNEES_CONFIRMED"
	RequirementDesigning            RequirementStatus = "DESIGNING"
	RequirementDesignReview         RequirementStatus = "DESIGN_REVIEW"
	RequirementDeveloping           RequirementStatus = "DEVELOPING"
	RequirementUnitVerified         RequirementStatus = "UNIT_VERIFIED"
	RequirementAPIDocReady          RequirementStatus = "API_DOC_READY"
	RequirementTestDeploying        RequirementStatus = "TEST_DEPLOYING"
	RequirementTesting              RequirementStatus = "TESTING"
	RequirementReadyForHumanQA      RequirementStatus = "READY_FOR_HUMAN_QA"
	RequirementAccepted             RequirementStatus = "ACCEPTED"
	RequirementReleased             RequirementStatus = "RELEASED"
	RequirementDesignRework         RequirementStatus = "DESIGN_REWORK"
	RequirementTestFailed           RequirementStatus = "TEST_FAILED"
	RequirementAutoRepairing        RequirementStatus = "AUTO_REPAIRING"
	RequirementHumanTriageRequired  RequirementStatus = "HUMAN_TRIAGE_REQUIRED"
	RequirementHumanApprovalNeeded  RequirementStatus = "HUMAN_APPROVAL_REQUIRED"
	RequirementBlockedProvider      RequirementStatus = "BLOCKED_PROVIDER"
	RequirementBlockedEnvironment   RequirementStatus = "BLOCKED_ENVIRONMENT"
	RequirementBlockedArtifactStore RequirementStatus = "BLOCKED_ARTIFACT_STORE"
	RequirementCancelled            RequirementStatus = "CANCELLED"
)

var transitions = map[RequirementStatus]map[RequirementStatus]bool{
	RequirementReceived:             {RequirementTriaged: true, RequirementCancelled: true},
	RequirementTriaged:              {RequirementAssigneesConfirmed: true, RequirementCancelled: true},
	RequirementAssigneesConfirmed:   {RequirementDesigning: true, RequirementCancelled: true},
	RequirementDesigning:            {RequirementDesignReview: true, RequirementDesignRework: true, RequirementBlockedProvider: true},
	RequirementDesignReview:         {RequirementDeveloping: true, RequirementDesignRework: true, RequirementHumanApprovalNeeded: true},
	RequirementDesignRework:         {RequirementDesigning: true, RequirementCancelled: true},
	RequirementDeveloping:           {RequirementUnitVerified: true, RequirementTestFailed: true, RequirementBlockedProvider: true},
	RequirementUnitVerified:         {RequirementAPIDocReady: true, RequirementTestFailed: true},
	RequirementAPIDocReady:          {RequirementTestDeploying: true, RequirementBlockedEnvironment: true},
	RequirementTestDeploying:        {RequirementTesting: true, RequirementBlockedEnvironment: true},
	RequirementTesting:              {RequirementReadyForHumanQA: true, RequirementTestFailed: true, RequirementBlockedEnvironment: true},
	RequirementTestFailed:           {RequirementAutoRepairing: true, RequirementHumanTriageRequired: true},
	RequirementAutoRepairing:        {RequirementDeveloping: true, RequirementTestFailed: true, RequirementHumanTriageRequired: true},
	RequirementReadyForHumanQA:      {RequirementAccepted: true, RequirementDeveloping: true, RequirementHumanApprovalNeeded: true},
	RequirementAccepted:             {RequirementReleased: true},
	RequirementHumanTriageRequired:  {RequirementDeveloping: true, RequirementCancelled: true},
	RequirementHumanApprovalNeeded:  {RequirementDeveloping: true, RequirementAccepted: true, RequirementCancelled: true},
	RequirementBlockedProvider:      {RequirementDesigning: true, RequirementDeveloping: true, RequirementCancelled: true},
	RequirementBlockedEnvironment:   {RequirementTestDeploying: true, RequirementTesting: true, RequirementCancelled: true},
	RequirementBlockedArtifactStore: {RequirementTesting: true, RequirementReadyForHumanQA: true, RequirementCancelled: true},
}

func (s RequirementStatus) Valid() bool {
	_, ok := transitions[s]
	return ok || s == RequirementReleased || s == RequirementAccepted || s == RequirementCancelled
}

func CanTransition(from, to RequirementStatus) bool {
	if from == to {
		return true
	}
	return transitions[from][to]
}

func Transition(from, to RequirementStatus) error {
	if !from.Valid() || !to.Valid() {
		return fmt.Errorf("invalid requirement status transition %q -> %q", from, to)
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("invalid requirement status transition %q -> %q", from, to)
	}
	return nil
}

type Requirement struct {
	ID                 string            `json:"id"`
	WorkspaceID        string            `json:"workspace_id"`
	Key                string            `json:"key"`
	Title              string            `json:"title"`
	Description        string            `json:"description"`
	AcceptanceCriteria []string          `json:"acceptance_criteria"`
	Priority           string            `json:"priority"`
	Status             RequirementStatus `json:"status"`
	CreatedBy          string            `json:"created_by"`
	AssigneeMemberIDs  []string          `json:"assignee_member_ids"`
	RepositoryIDs      []string          `json:"repository_ids"`
	WorkflowTemplateID string            `json:"workflow_template_id,omitempty"`
	Version            int64             `json:"version"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

func (r Requirement) Validate() error {
	if strings.TrimSpace(r.Title) == "" {
		return errors.New("title is required")
	}
	if strings.TrimSpace(r.Description) == "" {
		return errors.New("description is required")
	}
	if strings.TrimSpace(r.WorkspaceID) == "" {
		return errors.New("workspace_id is required")
	}
	if len(r.AcceptanceCriteria) == 0 {
		return errors.New("at least one acceptance criterion is required")
	}
	if len(r.AssigneeMemberIDs) == 0 {
		return errors.New("at least one assignee_member_id is required")
	}
	return nil
}

type BugStatus string

const (
	BugOpen      BugStatus = "OPEN"
	BugRepairing BugStatus = "REPAIRING"
	BugVerified  BugStatus = "VERIFIED"
	BugEscalated BugStatus = "HUMAN_TRIAGE_REQUIRED"
	BugCancelled BugStatus = "CANCELLED"
)

type Bug struct {
	ID               string    `json:"id"`
	WorkspaceID      string    `json:"workspace_id"`
	RequirementID    string    `json:"requirement_id,omitempty"`
	WorkItemID       string    `json:"work_item_id,omitempty"`
	RepositoryID     string    `json:"repository_id"`
	AssigneeMemberID string    `json:"assignee_member_id,omitempty"`
	Fingerprint      string    `json:"fingerprint"`
	Title            string    `json:"title"`
	Steps            string    `json:"steps_to_reproduce,omitempty"`
	Expected         string    `json:"expected,omitempty"`
	Actual           string    `json:"actual,omitempty"`
	LogExcerpt       string    `json:"log_excerpt,omitempty"`
	Status           BugStatus `json:"status"`
	AttemptCount     int       `json:"attempt_count"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// EntityAttachment binds immutable ArtifactStore content to a product entity.
type EntityAttachment struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	OwnerType   string    `json:"owner_type"`
	OwnerID     string    `json:"owner_id"`
	Filename    string    `json:"filename"`
	MediaType   string    `json:"media_type"`
	SizeBytes   int64     `json:"size_bytes"`
	ArtifactURI string    `json:"artifact_uri"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

type WorkItem struct {
	ID                      string    `json:"id"`
	RequirementID           string    `json:"requirement_id,omitempty"`
	BugID                   string    `json:"bug_id,omitempty"`
	RepositoryID            string    `json:"repository_id"`
	MemberID                string    `json:"member_id"`
	DeveloperAgentBindingID string    `json:"developer_agent_binding_id,omitempty"`
	Role                    string    `json:"role,omitempty"`
	AgentRouteSource        string    `json:"agent_route_source,omitempty"`
	RoutingConfigRevision   string    `json:"routing_config_revision,omitempty"`
	ProviderIssueID         string    `json:"provider_issue_id,omitempty"`
	Status                  string    `json:"status"`
	Stage                   int       `json:"stage"`
	BaselineCommit          string    `json:"baseline_commit,omitempty"`
	HeadCommit              string    `json:"head_commit,omitempty"`
	BranchName              string    `json:"branch_name,omitempty"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

// ProviderBinding is the durable, provider-neutral identity of a native
// execution object. ProviderObjectID is controlled execution provenance and is
// intentionally omitted from ordinary WorkItem and diagnostics responses.
type ProviderBinding struct {
	ID               string    `json:"id"`
	WorkspaceID      string    `json:"workspace_id"`
	Provider         string    `json:"provider"`
	Kind             string    `json:"kind"`
	ProviderObjectID string    `json:"provider_object_id,omitempty"`
	Status           string    `json:"status"`
	Source           string    `json:"source,omitempty"`
	ConfigRevision   string    `json:"config_revision,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type EvidenceBundle struct {
	ID            string         `json:"id"`
	WorkspaceID   string         `json:"workspace_id"`
	WorkItemID    string         `json:"work_item_id"`
	Kind          string         `json:"kind"`
	Status        string         `json:"status"`
	Summary       map[string]any `json:"summary"`
	ArtifactURI   string         `json:"artifact_uri,omitempty"`
	ContentSHA256 string         `json:"content_sha256"`
	ProducerRunID string         `json:"producer_run_id"`
	CreatedAt     time.Time      `json:"created_at"`
}

type GateCheck struct {
	Name     string `json:"name"`
	Actual   any    `json:"actual"`
	Expected any    `json:"expected"`
}

type GateResult struct {
	Gate        string      `json:"gate"`
	Decision    string      `json:"decision"`
	Checks      []GateCheck `json:"checks"`
	EvidenceIDs []string    `json:"evidence_ids"`
}

type Provenance struct {
	ID                     string    `json:"id"`
	WorkItemID             string    `json:"work_item_id"`
	RequirementID          string    `json:"requirement_id,omitempty"`
	BugID                  string    `json:"bug_id,omitempty"`
	AgentBindingID         string    `json:"agent_binding_id"`
	Provider               string    `json:"provider"`
	ProviderAgentID        string    `json:"provider_agent_id,omitempty"`
	ProviderTaskID         string    `json:"provider_task_id,omitempty"`
	ProviderSessionID      string    `json:"provider_session_id,omitempty"`
	ProviderWorkDir        string    `json:"provider_work_dir,omitempty"`
	ProviderIdempotencyKey string    `json:"provider_idempotency_key,omitempty"`
	RepositoryID           string    `json:"repository_id"`
	BaselineCommit         string    `json:"baseline_commit,omitempty"`
	HeadCommit             string    `json:"head_commit,omitempty"`
	ContextVersion         int64     `json:"context_version"`
	CreatedAt              time.Time `json:"created_at"`
}

type ImpactAction string

const (
	ImpactMustChange ImpactAction = "must_change"
	ImpactMayChange  ImpactAction = "may_change"
	ImpactTestOnly   ImpactAction = "test_only"
	ImpactNoChange   ImpactAction = "no_change"
	ImpactUnknown    ImpactAction = "unknown"
)

func (a ImpactAction) Valid() bool {
	switch a {
	case ImpactMustChange, ImpactMayChange, ImpactTestOnly, ImpactNoChange, ImpactUnknown:
		return true
	default:
		return false
	}
}

type ImpactCandidate struct {
	RepositoryID      string       `json:"repository_id"`
	Relation          string       `json:"relation"`
	EvidenceRefs      []string     `json:"evidence_refs"`
	Confidence        float64      `json:"confidence"`
	RecommendedAction ImpactAction `json:"recommended_action"`
	WhyNotChange      string       `json:"why_not_change,omitempty"`
}

type ImpactReport struct {
	ID                    string            `json:"id"`
	RequirementID         string            `json:"requirement_id"`
	Version               int64             `json:"version"`
	InputSnapshot         map[string]any    `json:"input_snapshot"`
	Candidates            []ImpactCandidate `json:"candidate_repositories"`
	ConfirmedRepositories []string          `json:"confirmed_repositories"`
	UnresolvedRisks       []string          `json:"unresolved_risks"`
	Status                string            `json:"status"`
	CreatedAt             time.Time         `json:"created_at"`
}

type ContextRepository struct {
	ID       string `json:"id"`
	Baseline string `json:"baseline"`
	Head     string `json:"head,omitempty"`
}

type ContextManifest struct {
	ContextID                 string              `json:"context_id"`
	Version                   int64               `json:"version"`
	RequirementID             string              `json:"requirement_id,omitempty"`
	BugID                     string              `json:"bug_id,omitempty"`
	StableSummary             string              `json:"stable_summary"`
	ApprovedChangeContractID  string              `json:"approved_change_contract_id,omitempty"`
	Repositories              []ContextRepository `json:"repositories"`
	OriginalDeveloperMemberID string              `json:"original_developer_member_id,omitempty"`
	OriginalAgentBindingID    string              `json:"original_agent_binding_id,omitempty"`
	LatestEvidenceIDs         []string            `json:"latest_evidence_ids"`
	ArtifactRefs              []string            `json:"artifact_refs"`
	TokenBudget               int64               `json:"token_budget"`
}

type DeliveryRepository struct {
	RepositoryID string   `json:"repository_id"`
	Commit       string   `json:"commit"`
	PR           string   `json:"pr,omitempty"`
	Tests        []string `json:"tests"`
	Status       string   `json:"status"`
}

type DeliveryManifest struct {
	RequirementID string               `json:"requirement_id"`
	Version       int64                `json:"version"`
	Repositories  []DeliveryRepository `json:"repositories"`
	CreatedAt     time.Time            `json:"created_at"`
}

type RepairBrief struct {
	BugID          string   `json:"bug_id"`
	Fingerprint    string   `json:"fingerprint"`
	StableSummary  string   `json:"stable_summary"`
	BaselineCommit string   `json:"baseline_commit,omitempty"`
	FailedEvidence []string `json:"failed_evidence"`
	Attempt        int      `json:"attempt"`
}

// RepairAttempt is an append-only record linking a failed test back to the
// original delivery context. It is deliberately provider-neutral: provider
// IDs are evidence, while WorkItemID and ContextID remain ADRO identities.
type RepairAttempt struct {
	ID                string      `json:"id"`
	BugID             string      `json:"bug_id"`
	WorkItemID        string      `json:"work_item_id"`
	Attempt           int         `json:"attempt"`
	ContextID         string      `json:"context_id"`
	ContextVersion    int64       `json:"context_version"`
	ProviderIssueID   string      `json:"provider_issue_id,omitempty"`
	ProviderSessionID string      `json:"provider_session_id,omitempty"`
	ProviderWorkDir   string      `json:"provider_work_dir,omitempty"`
	ProviderTaskID    string      `json:"provider_task_id,omitempty"`
	Status            string      `json:"status"`
	Brief             RepairBrief `json:"brief"`
	CreatedAt         time.Time   `json:"created_at"`
}

type Repository struct {
	ID            string         `json:"id"`
	WorkspaceID   string         `json:"workspace_id"`
	CanonicalName string         `json:"canonical_name"`
	CloneURL      string         `json:"clone_url"`
	Provider      string         `json:"provider"`
	DefaultBranch string         `json:"default_branch"`
	LanguageSet   []string       `json:"language_set"`
	Metadata      map[string]any `json:"metadata"`
	IndexedCommit string         `json:"indexed_commit,omitempty"`
	IndexStatus   string         `json:"index_status"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type TeamWorkspace struct {
	ID            string         `json:"id"`
	WorkspaceID   string         `json:"workspace_id"`
	Name          string         `json:"name"`
	Version       int64          `json:"version"`
	RepositoryIDs []string       `json:"repository_ids"`
	Policy        map[string]any `json:"policy"`
	Status        string         `json:"status"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type DeveloperProfile struct {
	ID                    string         `json:"id"`
	WorkspaceID           string         `json:"workspace_id"`
	MemberID              string         `json:"member_id"`
	DefaultAgentBindingID string         `json:"default_agent_binding_id,omitempty"`
	DefaultRole           string         `json:"default_role,omitempty"`
	GitIdentity           map[string]any `json:"git_identity"`
	Status                string         `json:"status"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
}

type MCPServer struct {
	ID            string         `json:"id"`
	WorkspaceID   string         `json:"workspace_id"`
	Name          string         `json:"name"`
	Endpoint      string         `json:"endpoint"`
	Protocol      string         `json:"protocol"`
	SchemaDigest  string         `json:"schema_digest"`
	Scopes        []string       `json:"scopes"`
	SecretRef     string         `json:"secret_ref,omitempty"`
	Status        string         `json:"status"`
	Configuration map[string]any `json:"configuration"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type Skill struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id"`
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	Kind        string         `json:"kind"`
	Contract    map[string]any `json:"contract"`
	Digest      string         `json:"digest"`
	Status      string         `json:"status"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type Automation struct {
	ID          string           `json:"id"`
	WorkspaceID string           `json:"workspace_id"`
	Name        string           `json:"name"`
	Version     int64            `json:"version"`
	Trigger     map[string]any   `json:"trigger"`
	Nodes       []map[string]any `json:"nodes"`
	Enabled     bool             `json:"enabled"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

type Approval struct {
	ID            string     `json:"id"`
	WorkspaceID   string     `json:"workspace_id"`
	RequirementID string     `json:"requirement_id"`
	Kind          string     `json:"kind"`
	Decision      string     `json:"decision"`
	DecidedBy     string     `json:"decided_by,omitempty"`
	Reason        string     `json:"reason,omitempty"`
	EvidenceIDs   []string   `json:"evidence_ids"`
	CreatedAt     time.Time  `json:"created_at"`
	DecidedAt     *time.Time `json:"decided_at,omitempty"`
}

// DiffSnapshot is the provider-neutral changed-files view exposed to the
// workbench. Content is optional; a runner may publish only the stat first.
type DiffSnapshot struct {
	ID            string         `json:"id"`
	WorkItemID    string         `json:"work_item_id"`
	RepositoryID  string         `json:"repository_id"`
	BaseCommit    string         `json:"base_commit"`
	HeadCommit    string         `json:"head_commit"`
	Stat          map[string]any `json:"stat"`
	Files         []string       `json:"files"`
	Patch         string         `json:"patch,omitempty"`
	ContentSHA256 string         `json:"content_sha256"`
	CreatedAt     time.Time      `json:"created_at"`
}

type ArtifactMigration struct {
	ID              string     `json:"id"`
	WorkspaceID     string     `json:"workspace_id"`
	ArtifactID      string     `json:"artifact_id"`
	FromDriver      string     `json:"from_driver"`
	ToDriver        string     `json:"to_driver"`
	Status          string     `json:"status"`
	CopiedObjects   int64      `json:"copied_objects"`
	VerifiedObjects int64      `json:"verified_objects"`
	Error           string     `json:"error,omitempty"`
	RollbackUntil   *time.Time `json:"rollback_until,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type MCPInvocation struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id"`
	ServerID    string         `json:"server_id"`
	Tool        string         `json:"tool"`
	Status      string         `json:"status"`
	Request     map[string]any `json:"request,omitempty"`
	Response    map[string]any `json:"response,omitempty"`
	DurationMS  int64          `json:"duration_ms"`
	CreatedAt   time.Time      `json:"created_at"`
}

type CapabilityBinding struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspace_id"`
	AgentID      string    `json:"agent_id"`
	CapabilityID string    `json:"capability_id"`
	Kind         string    `json:"kind"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
}

type AutomationRun struct {
	ID           string         `json:"id"`
	AutomationID string         `json:"automation_id"`
	WorkspaceID  string         `json:"workspace_id"`
	Status       string         `json:"status"`
	Input        map[string]any `json:"input,omitempty"`
	Output       map[string]any `json:"output,omitempty"`
	StartedAt    time.Time      `json:"started_at"`
	FinishedAt   *time.Time     `json:"finished_at,omitempty"`
	TakenOverBy  string         `json:"taken_over_by,omitempty"`
}
