package pipeline

import (
	"errors"
	"testing"

	"github.com/adro-project/adro/internal/domain"
)

func pipelineFixture() domain.PipelineRun {
	return domain.PipelineRun{
		ID: "pipe", WorkspaceID: "ws", RequirementID: "req", SessionID: "global-session",
		PipelineStage: domain.PipelineDesign, Status: domain.PipelineRunning,
		Roles:      domain.PipelineAgentRoles{Designer: "design", Developer: "dev", Tester: "test", Arbitrator: "arb"},
		MaxRetries: 3, CoverageThreshold: 80, Version: 1,
	}
}

func applyOK(t *testing.T, run domain.PipelineRun, result domain.PipelineStepResult) domain.PipelineRun {
	t.Helper()
	next, err := NewEngine().Apply(run, result)
	if err != nil {
		t.Fatalf("apply stage %d: %v", run.PipelineStage, err)
	}
	return next
}

func TestSevenStageHappyPath(t *testing.T) {
	run := pipelineFixture()
	run = applyOK(t, run, domain.PipelineStepResult{Stage: 1, AgentID: "design", Outcome: "pass", DesignDoc: "doc"})
	run = applyOK(t, run, domain.PipelineStepResult{Stage: 2, AgentID: "dev", Outcome: "pass", CodeVersion: "a1", ProviderSessionID: "native-1", ProviderWorkDir: "/repo"})
	run = applyOK(t, run, domain.PipelineStepResult{Stage: 3, AgentID: "test", Outcome: "pass", Coverage: 86, PassedTests: []string{"unit"}})
	run = applyOK(t, run, domain.PipelineStepResult{Stage: 4, AgentID: "test", Outcome: "pass", PassedTests: []string{"integration"}})
	if run.PipelineStage != domain.PipelineReport {
		t.Fatalf("stage=%d", run.PipelineStage)
	}
	run = applyOK(t, run, domain.PipelineStepResult{Stage: 7, AgentID: "test", Outcome: "pass", Report: "all green"})
	if run.Status != domain.PipelineCompleted || run.FinalReport != "all green" {
		t.Fatalf("completed run=%+v", run)
	}
	if run.SessionID != "global-session" || run.ParentSessionID != "native-1" {
		t.Fatalf("session identity changed: %+v", run)
	}
}

func TestIntegrationFailureReturnsToSameDeveloperSessionAndRevalidates(t *testing.T) {
	run := pipelineFixture()
	run = applyOK(t, run, domain.PipelineStepResult{Stage: 1, AgentID: "design", Outcome: "pass", DesignDoc: "doc"})
	run = applyOK(t, run, domain.PipelineStepResult{Stage: 2, AgentID: "dev", Outcome: "pass", CodeVersion: "a1", ProviderSessionID: "native-1", ProviderWorkDir: "/repo"})
	run = applyOK(t, run, domain.PipelineStepResult{Stage: 3, AgentID: "test", Outcome: "pass", Coverage: 90})
	run = applyOK(t, run, domain.PipelineStepResult{Stage: 4, AgentID: "test", Outcome: "fail", FailedTests: []string{"api"}, ErrorLog: "boom"})
	if run.PipelineStage != domain.PipelineArbitration {
		t.Fatalf("stage=%d", run.PipelineStage)
	}
	run = applyOK(t, run, domain.PipelineStepResult{Stage: 5, AgentID: "arb", Outcome: "pass", RepairNote: "fix incrementally"})
	if run.PipelineStage != domain.PipelineDevelopment || run.RetryCount != 1 {
		t.Fatalf("repair transition=%+v", run)
	}
	if _, err := NewEngine().Apply(run, domain.PipelineStepResult{Stage: 2, AgentID: "dev", Outcome: "pass", CodeVersion: "a2", ProviderSessionID: "new-session", ProviderWorkDir: "/repo"}); !errors.Is(err, ErrSessionContinuity) {
		t.Fatalf("fresh repair session was accepted: %v", err)
	}
	run = applyOK(t, run, domain.PipelineStepResult{Stage: 2, AgentID: "dev", Outcome: "pass", CodeVersion: "a2", ProviderSessionID: "native-1", ProviderWorkDir: "/repo"})
	run = applyOK(t, run, domain.PipelineStepResult{Stage: 3, AgentID: "test", Outcome: "pass", Coverage: 92})
	run = applyOK(t, run, domain.PipelineStepResult{Stage: 4, AgentID: "test", Outcome: "pass"})
	if run.PipelineStage != domain.PipelineRevalidation {
		t.Fatalf("stage=%d", run.PipelineStage)
	}
	run = applyOK(t, run, domain.PipelineStepResult{Stage: 6, AgentID: "test", Outcome: "pass"})
	if run.PipelineStage != domain.PipelineReport {
		t.Fatalf("stage=%d", run.PipelineStage)
	}
	if len(run.Context.ErrorLogs) != 1 || run.Context.BaselineCommit != "a1" || run.Context.CurrentCommit != "a2" {
		t.Fatalf("context was overwritten: %+v", run.Context)
	}
}

func TestCoverageRetriesInStageThreeAndSuspends(t *testing.T) {
	run := pipelineFixture()
	run.PipelineStage, run.ActiveAgentID = domain.PipelineUnitTest, "test"
	for attempt := 1; attempt <= 3; attempt++ {
		run = applyOK(t, run, domain.PipelineStepResult{Stage: 3, AgentID: "test", Outcome: "pass", Coverage: 70})
	}
	if run.PipelineStage != domain.PipelineUnitTest || run.Status != domain.PipelineSuspended || run.UnitRetryCount != 3 {
		t.Fatalf("coverage cap=%+v", run)
	}
}

func TestCustomWorkflowIntegrationFailureWithoutArbitrationHonorsRetryLimit(t *testing.T) {
	run := pipelineFixture()
	run.Workflow = []domain.WorkflowStep{
		{ID: "development", Stage: domain.PipelineDevelopment, AgentID: "dev", Required: true, RetryLimit: 2},
		{ID: "integration", Stage: domain.PipelineIntegration, AgentID: "test", Required: true},
		{ID: "report", Stage: domain.PipelineReport, AgentID: "test", Required: true},
	}
	run.PipelineStage = domain.PipelineIntegration
	run.ActiveAgentID = "test"
	run.RetryCount = 0
	first := applyOK(t, run, domain.PipelineStepResult{Stage: domain.PipelineIntegration, AgentID: "test", Outcome: "fail"})
	if first.PipelineStage != domain.PipelineDevelopment || first.RetryCount != 1 || first.Status != domain.PipelineRunning {
		t.Fatalf("first integration failure=%+v", first)
	}
	second := applyOK(t, first, domain.PipelineStepResult{Stage: domain.PipelineDevelopment, AgentID: "dev", Outcome: "pass", CodeVersion: "v2", ProviderSessionID: "native", ProviderWorkDir: "/repo"})
	if second.PipelineStage != domain.PipelineIntegration {
		t.Fatalf("development did not return to integration=%+v", second)
	}
	third := applyOK(t, second, domain.PipelineStepResult{Stage: domain.PipelineIntegration, AgentID: "test", Outcome: "fail"})
	if third.Status != domain.PipelineSuspended || third.RetryCount != 2 {
		t.Fatalf("retry limit was not enforced=%+v", third)
	}
}

func TestWrongRoleAndStaleStageAreRejected(t *testing.T) {
	run := pipelineFixture()
	if _, err := NewEngine().Apply(run, domain.PipelineStepResult{Stage: 2, AgentID: "dev", Outcome: "pass"}); !errors.Is(err, ErrStaleStage) {
		t.Fatalf("stale stage: %v", err)
	}
	if _, err := NewEngine().Apply(run, domain.PipelineStepResult{Stage: 1, AgentID: "dev", Outcome: "pass"}); !errors.Is(err, ErrWrongAgent) {
		t.Fatalf("wrong role: %v", err)
	}
}
