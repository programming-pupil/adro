package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// PipelineStage is the durable seven-stage delivery state. Values are kept
// numeric on purpose: operators can compare the persisted value with the
// product workflow without translating provider-specific task states.
type PipelineStage int

const (
	PipelineDesign       PipelineStage = 1
	PipelineDevelopment  PipelineStage = 2
	PipelineUnitTest     PipelineStage = 3
	PipelineIntegration  PipelineStage = 4
	PipelineArbitration  PipelineStage = 5
	PipelineRevalidation PipelineStage = 6
	PipelineReport       PipelineStage = 7
)

func (s PipelineStage) Valid() bool { return s >= PipelineDesign && s <= PipelineReport }

func (s PipelineStage) String() string {
	return map[PipelineStage]string{
		PipelineDesign: "design", PipelineDevelopment: "development",
		PipelineUnitTest: "unit_test", PipelineIntegration: "integration_test",
		PipelineArbitration: "arbitration", PipelineRevalidation: "revalidation",
		PipelineReport: "report",
	}[s]
}

type PipelineStatus string

const (
	PipelineRunning   PipelineStatus = "running"
	PipelineWaiting   PipelineStatus = "waiting_provider"
	PipelineSuspended PipelineStatus = "suspended"
	PipelineCompleted PipelineStatus = "completed"
	PipelineFailed    PipelineStatus = "failed"
)

type PipelineAgentRoles struct {
	Designer   string `json:"designer_agent_id"`
	Developer  string `json:"developer_agent_id"`
	Tester     string `json:"tester_agent_id"`
	Arbitrator string `json:"arbitrator_agent_id"`
}

func (r PipelineAgentRoles) Validate() error {
	missing := make([]string, 0, 4)
	if strings.TrimSpace(r.Designer) == "" {
		missing = append(missing, "designer_agent_id")
	}
	if strings.TrimSpace(r.Developer) == "" {
		missing = append(missing, "developer_agent_id")
	}
	if strings.TrimSpace(r.Tester) == "" {
		missing = append(missing, "tester_agent_id")
	}
	if strings.TrimSpace(r.Arbitrator) == "" {
		missing = append(missing, "arbitrator_agent_id")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing pipeline agents: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (r PipelineAgentRoles) AgentFor(stage PipelineStage) string {
	switch stage {
	case PipelineDesign:
		return r.Designer
	case PipelineDevelopment:
		return r.Developer
	case PipelineUnitTest, PipelineIntegration, PipelineRevalidation, PipelineReport:
		return r.Tester
	case PipelineArbitration:
		return r.Arbitrator
	default:
		return ""
	}
}

// PipelineContext is the authoritative handoff payload. It is append-oriented:
// repairs add failure evidence and code versions instead of replacing the
// baseline or the conversation history.
type PipelineContext struct {
	RequirementText string   `json:"requirement_text"`
	DesignDoc       string   `json:"design_doc,omitempty"`
	BaselineCommit  string   `json:"baseline_commit,omitempty"`
	CurrentCommit   string   `json:"current_commit,omitempty"`
	PassedTests     []string `json:"passed_tests,omitempty"`
	FailedTests     []string `json:"failed_tests,omitempty"`
	ErrorLogs       []string `json:"error_logs,omitempty"`
	RepairNotes     []string `json:"repair_notes,omitempty"`
	Coverage        float64  `json:"coverage"`
}

type PipelineTransition struct {
	Sequence          int           `json:"sequence"`
	From              PipelineStage `json:"from_stage"`
	To                PipelineStage `json:"to_stage"`
	Outcome           string        `json:"outcome"`
	AgentID           string        `json:"agent_id"`
	ProviderIssueID   string        `json:"provider_issue_id,omitempty"`
	ProviderTaskID    string        `json:"provider_task_id,omitempty"`
	ProviderSessionID string        `json:"provider_session_id,omitempty"`
	ProviderWorkDir   string        `json:"provider_work_dir,omitempty"`
	Summary           string        `json:"summary,omitempty"`
	CreatedAt         time.Time     `json:"created_at"`
}

type PipelineRun struct {
	ID                    string               `json:"id"`
	WorkspaceID           string               `json:"workspace_id"`
	RequirementID         string               `json:"requirement_id"`
	SessionID             string               `json:"session_id"`
	ParentSessionID       string               `json:"parent_session_id,omitempty"`
	ProviderWorkDir       string               `json:"provider_work_dir,omitempty"`
	PipelineStage         PipelineStage        `json:"pipeline_stage"`
	Status                PipelineStatus       `json:"status"`
	Roles                 PipelineAgentRoles   `json:"roles"`
	Context               PipelineContext      `json:"context"`
	MaxRetries            int                  `json:"max_retries"`
	RetryCount            int                  `json:"retry_count"`
	UnitRetryCount        int                  `json:"unit_retry_count"`
	CoverageThreshold     float64              `json:"coverage_threshold"`
	ActiveProviderIssueID string               `json:"active_provider_issue_id,omitempty"`
	ActiveProviderTaskID  string               `json:"active_provider_task_id,omitempty"`
	ActiveAgentID         string               `json:"active_agent_id,omitempty"`
	FinalReport           string               `json:"final_report,omitempty"`
	SuspendReason         string               `json:"suspend_reason,omitempty"`
	Version               int64                `json:"version"`
	History               []PipelineTransition `json:"history"`
	CreatedAt             time.Time            `json:"created_at"`
	UpdatedAt             time.Time            `json:"updated_at"`
}

func (r PipelineRun) Validate() error {
	if strings.TrimSpace(r.WorkspaceID) == "" || strings.TrimSpace(r.RequirementID) == "" {
		return errors.New("workspace_id and requirement_id are required")
	}
	if strings.TrimSpace(r.SessionID) == "" {
		return errors.New("session_id is required")
	}
	if !r.PipelineStage.Valid() {
		return errors.New("pipeline_stage must be between 1 and 7")
	}
	if err := r.Roles.Validate(); err != nil {
		return err
	}
	if r.MaxRetries < 1 {
		return errors.New("max_retries must be at least 1")
	}
	if r.CoverageThreshold <= 0 || r.CoverageThreshold > 100 {
		return errors.New("coverage_threshold must be in (0,100]")
	}
	return nil
}

// PipelineStepResult is accepted only for the current stage and active agent.
// ProviderSessionID is mandatory for development so repair continuity can be
// verified instead of inferred from a provider's issue identifier.
type PipelineStepResult struct {
	Stage             PipelineStage `json:"stage"`
	AgentID           string        `json:"agent_id"`
	Outcome           string        `json:"outcome"`
	Summary           string        `json:"summary,omitempty"`
	DesignDoc         string        `json:"design_doc,omitempty"`
	CodeVersion       string        `json:"code_version,omitempty"`
	Coverage          float64       `json:"coverage,omitempty"`
	PassedTests       []string      `json:"passed_tests,omitempty"`
	FailedTests       []string      `json:"failed_tests,omitempty"`
	ErrorLog          string        `json:"error_log,omitempty"`
	RepairNote        string        `json:"repair_note,omitempty"`
	Report            string        `json:"report,omitempty"`
	ProviderIssueID   string        `json:"provider_issue_id,omitempty"`
	ProviderTaskID    string        `json:"provider_task_id,omitempty"`
	ProviderSessionID string        `json:"provider_session_id,omitempty"`
	ProviderWorkDir   string        `json:"provider_work_dir,omitempty"`
}
