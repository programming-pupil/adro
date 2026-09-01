// Package pipeline owns the strict, provider-neutral 1-7 delivery state
// machine. Provider adapters may execute a step, but they cannot choose or
// skip the next stage.
package pipeline

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/domain"
)

var (
	ErrStaleStage        = errors.New("pipeline result is for a stale stage")
	ErrWrongAgent        = errors.New("pipeline result is from the wrong role agent")
	ErrSessionContinuity = errors.New("development repair did not reuse the original provider session")
)

type Engine struct{}

func NewEngine() Engine { return Engine{} }

// Apply validates evidence, merges it into the immutable session context, and
// advances exactly one state-machine edge. It never replaces previous failure
// evidence or the original baseline.
func (Engine) Apply(run domain.PipelineRun, result domain.PipelineStepResult) (domain.PipelineRun, error) {
	if run.Status == domain.PipelineCompleted || run.Status == domain.PipelineSuspended || run.Status == domain.PipelineFailed {
		return run, fmt.Errorf("pipeline is terminal: %s", run.Status)
	}
	if result.Stage != run.PipelineStage {
		return run, ErrStaleStage
	}
	expectedAgent := run.AgentFor(run.PipelineStage)
	if strings.TrimSpace(result.AgentID) == "" || result.AgentID != expectedAgent {
		return run, ErrWrongAgent
	}
	if result.Outcome != "pass" && result.Outcome != "fail" {
		return run, errors.New("outcome must be pass or fail")
	}

	from := run.PipelineStage
	if result.DesignDoc != "" {
		run.Context.DesignDoc = result.DesignDoc
	}
	if result.CodeVersion != "" {
		if run.Context.BaselineCommit == "" {
			run.Context.BaselineCommit = result.CodeVersion
		}
		run.Context.CurrentCommit = result.CodeVersion
	}
	if result.Coverage > 0 {
		run.Context.Coverage = result.Coverage
	}
	run.Context.PassedTests = appendUnique(run.Context.PassedTests, result.PassedTests...)
	run.Context.FailedTests = appendUnique(run.Context.FailedTests, result.FailedTests...)
	if strings.TrimSpace(result.ErrorLog) != "" {
		run.Context.ErrorLogs = append(run.Context.ErrorLogs, result.ErrorLog)
	}
	if strings.TrimSpace(result.RepairNote) != "" {
		run.Context.RepairNotes = append(run.Context.RepairNotes, result.RepairNote)
	}

	// The first development task pins the native session even when the client
	// fails. Every retry must present that exact session and worktree; otherwise
	// a broken repair could silently start over with lost context.
	if from == domain.PipelineDevelopment {
		if strings.TrimSpace(result.ProviderSessionID) == "" {
			return run, errors.New("development result requires provider_session_id")
		}
		if strings.TrimSpace(result.ProviderWorkDir) == "" {
			return run, errors.New("development result requires provider_work_dir")
		}
		if run.ParentSessionID == "" {
			run.ParentSessionID = result.ProviderSessionID
			run.ProviderWorkDir = result.ProviderWorkDir
		} else if result.ProviderSessionID != run.ParentSessionID {
			return run, ErrSessionContinuity
		} else if result.ProviderWorkDir != run.ProviderWorkDir {
			return run, ErrSessionContinuity
		}
		if result.Outcome == "fail" {
			run.RetryCount++
		}
	}
	if from == domain.PipelineUnitTest && (result.Outcome == "fail" || result.Coverage < run.CoverageThreshold) {
		run.UnitRetryCount++
	}
	if from == domain.PipelineArbitration && result.Outcome == "pass" {
		run.RetryCount++
	}

	next, status, reason, err := nextStage(run, result)
	if err != nil {
		return run, err
	}
	run.PipelineStage = next
	run.Status = status
	run.SuspendReason = reason
	run.ActiveProviderIssueID = result.ProviderIssueID
	run.ActiveProviderTaskID = result.ProviderTaskID
	run.ActiveAgentID = run.AgentFor(next)
	if next == domain.PipelineReport && result.Report != "" {
		run.FinalReport = result.Report
	}
	if status == domain.PipelineCompleted {
		run.FinalReport = result.Report
		run.ActiveAgentID = ""
		run.ActiveProviderTaskID = ""
	}
	now := time.Now().UTC()
	run.History = append(run.History, domain.PipelineTransition{
		Sequence: len(run.History) + 1, From: from, To: next, Outcome: result.Outcome,
		AgentID: result.AgentID, ProviderIssueID: result.ProviderIssueID, ProviderTaskID: result.ProviderTaskID,
		ProviderSessionID: result.ProviderSessionID, ProviderWorkDir: result.ProviderWorkDir, Summary: result.Summary, CreatedAt: now,
	})
	run.Version++
	run.UpdatedAt = now
	return run, nil
}

func nextStage(run domain.PipelineRun, result domain.PipelineStepResult) (domain.PipelineStage, domain.PipelineStatus, string, error) {
	if len(run.Workflow) > 0 {
		return nextCustomStage(run, result)
	}
	pass := result.Outcome == "pass"
	switch run.PipelineStage {
	case domain.PipelineDesign:
		if !pass {
			return domain.PipelineDesign, domain.PipelineSuspended, "design generation failed", nil
		}
		if strings.TrimSpace(result.DesignDoc) == "" {
			return 0, "", "", errors.New("design stage requires design_doc")
		}
		if run.WorkflowMode == domain.WorkflowDesignApproval && run.DesignApprovalStatus != "approved" {
			return domain.PipelineDesign, domain.PipelineWaitingApproval, "design approval required", nil
		}
		return domain.PipelineDevelopment, domain.PipelineRunning, "", nil
	case domain.PipelineDevelopment:
		if !pass {
			if run.RetryCount >= run.MaxRetries {
				return domain.PipelineDevelopment, domain.PipelineSuspended, "development retry limit reached", nil
			}
			return domain.PipelineDevelopment, domain.PipelineRunning, "", nil
		}
		if strings.TrimSpace(result.CodeVersion) == "" {
			return 0, "", "", errors.New("development stage requires code_version")
		}
		return domain.PipelineUnitTest, domain.PipelineRunning, "", nil
	case domain.PipelineUnitTest:
		if !pass || result.Coverage < run.CoverageThreshold {
			if run.UnitRetryCount >= run.MaxRetries {
				return domain.PipelineUnitTest, domain.PipelineSuspended, "unit-test or coverage retry limit reached", nil
			}
			return domain.PipelineUnitTest, domain.PipelineRunning, "", nil
		}
		return domain.PipelineIntegration, domain.PipelineRunning, "", nil
	case domain.PipelineIntegration:
		if !pass {
			return domain.PipelineArbitration, domain.PipelineRunning, "", nil
		}
		if run.RetryCount > 0 {
			return domain.PipelineRevalidation, domain.PipelineRunning, "", nil
		}
		return domain.PipelineReport, domain.PipelineRunning, "", nil
	case domain.PipelineArbitration:
		if !pass {
			return domain.PipelineArbitration, domain.PipelineSuspended, "arbitrator rejected automatic repair", nil
		}
		if run.RetryCount > run.MaxRetries {
			return domain.PipelineArbitration, domain.PipelineSuspended, "repair retry limit reached", nil
		}
		return domain.PipelineDevelopment, domain.PipelineRunning, "", nil
	case domain.PipelineRevalidation:
		if !pass {
			return domain.PipelineArbitration, domain.PipelineRunning, "", nil
		}
		return domain.PipelineReport, domain.PipelineRunning, "", nil
	case domain.PipelineReport:
		if !pass {
			return domain.PipelineReport, domain.PipelineSuspended, "report generation failed", nil
		}
		if strings.TrimSpace(result.Report) == "" {
			return 0, "", "", errors.New("report stage requires report")
		}
		return domain.PipelineReport, domain.PipelineCompleted, "", nil
	default:
		return 0, "", "", errors.New("invalid pipeline stage")
	}
}

func nextCustomStage(run domain.PipelineRun, result domain.PipelineStepResult) (domain.PipelineStage, domain.PipelineStatus, string, error) {
	pass := result.Outcome == "pass"
	current := run.PipelineStage
	if current == domain.PipelineDesign {
		if !pass {
			return current, domain.PipelineSuspended, "design generation failed", nil
		}
		if strings.TrimSpace(result.DesignDoc) == "" {
			return 0, "", "", errors.New("design stage requires design_doc")
		}
		if run.WorkflowMode == domain.WorkflowDesignApproval && run.DesignApprovalStatus != "approved" {
			return current, domain.PipelineWaitingApproval, "design approval required", nil
		}
	}
	if current == domain.PipelineDevelopment {
		if !pass {
			if run.RetryCount >= retryLimit(run, current) {
				return current, domain.PipelineSuspended, "development retry limit reached", nil
			}
			return current, domain.PipelineRunning, "", nil
		}
		if strings.TrimSpace(result.CodeVersion) == "" {
			return 0, "", "", errors.New("development stage requires code_version")
		}
	}
	if current == domain.PipelineUnitTest && (!pass || result.Coverage < run.CoverageThreshold) {
		if run.UnitRetryCount >= retryLimit(run, current) {
			return current, domain.PipelineSuspended, "unit-test or coverage retry limit reached", nil
		}
		return current, domain.PipelineRunning, "", nil
	}
	if current == domain.PipelineReport {
		if !pass {
			return current, domain.PipelineSuspended, "report generation failed", nil
		}
		if strings.TrimSpace(result.Report) == "" {
			return 0, "", "", errors.New("report stage requires report")
		}
		return current, domain.PipelineCompleted, "", nil
	}
	if current == domain.PipelineIntegration && !pass {
		if run.HasStage(domain.PipelineArbitration) {
			return domain.PipelineArbitration, domain.PipelineRunning, "", nil
		}
		if run.HasStage(domain.PipelineDevelopment) && run.RetryCount < retryLimit(run, domain.PipelineDevelopment) {
			return domain.PipelineDevelopment, domain.PipelineRunning, "", nil
		}
		return current, domain.PipelineSuspended, "integration failed and no repair stage is configured", nil
	}
	if current == domain.PipelineArbitration {
		if !pass {
			return current, domain.PipelineSuspended, "arbitrator rejected automatic repair", nil
		}
		if run.RetryCount > retryLimit(run, current) {
			return current, domain.PipelineSuspended, "repair retry limit reached", nil
		}
		if run.HasStage(domain.PipelineDevelopment) {
			return domain.PipelineDevelopment, domain.PipelineRunning, "", nil
		}
	}
	if current == domain.PipelineRevalidation && !pass {
		if run.HasStage(domain.PipelineArbitration) {
			return domain.PipelineArbitration, domain.PipelineRunning, "", nil
		}
		return current, domain.PipelineSuspended, "revalidation failed and no arbitration stage is configured", nil
	}
	next := run.NextSelectedStage(current)
	if next == current && current != domain.PipelineReport {
		return current, domain.PipelineSuspended, "workflow has no subsequent stage", nil
	}
	return next, domain.PipelineRunning, "", nil
}

func retryLimit(run domain.PipelineRun, stage domain.PipelineStage) int {
	if step := run.StepFor(stage); step.RetryLimit > 0 {
		return step.RetryLimit
	}
	return run.MaxRetries
}

func appendUnique(existing []string, values ...string) []string {
	seen := make(map[string]struct{}, len(existing)+len(values))
	for _, item := range existing {
		seen[item] = struct{}{}
	}
	for _, item := range values {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		existing = append(existing, item)
	}
	return existing
}
