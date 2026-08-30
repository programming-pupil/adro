package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/domain"
	pipelineengine "github.com/adro-project/adro/internal/pipeline"
	"github.com/adro-project/adro/internal/provider"
	"github.com/adro-project/adro/internal/store"
)

func (s *Server) pipelineRoute(w http.ResponseWriter, r *http.Request, tail string) {
	tail = strings.Trim(tail, "/")
	if tail == "" {
		switch r.Method {
		case http.MethodGet:
			s.writeJSON(w, http.StatusOK, map[string]any{"items": s.Store.ListPipelines(requestWorkspace(r, ""), r.URL.Query().Get("requirement_id"))})
		case http.MethodPost:
			s.createPipeline(w, r)
		default:
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		}
		return
	}
	parts := strings.Split(tail, "/")
	run, err := s.Store.GetPipeline(parts[0])
	if err != nil {
		s.problem(w, r, http.StatusNotFound, "pipeline_not_found", "pipeline not found", nil)
		return
	}
	if workspaceID := requestWorkspace(r, ""); workspaceID != "" && run.WorkspaceID != workspaceID {
		s.problem(w, r, http.StatusNotFound, "pipeline_not_found", "pipeline not found", nil)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		s.writeJSON(w, http.StatusOK, run)
		return
	}
	if len(parts) == 2 && parts[1] == "results" && r.Method == http.MethodPost {
		s.applyPipelineResult(w, r, run)
		return
	}
	s.problem(w, r, http.StatusNotFound, "not_found", "route not found", nil)
}

func (s *Server) createPipeline(w http.ResponseWriter, r *http.Request) {
	multica, ok := s.Provider.(*provider.MulticaProvider)
	if !ok || strings.TrimSpace(multica.BaseURL) == "" || strings.TrimSpace(multica.Token) == "" {
		s.problem(w, r, http.StatusServiceUnavailable, "multica_required", "automated pipelines require a configured real Multica provider; mock execution is forbidden", nil)
		return
	}
	var input struct {
		RequirementID     string                    `json:"requirement_id"`
		Roles             domain.PipelineAgentRoles `json:"roles"`
		MaxRetries        int                       `json:"max_retries"`
		CoverageThreshold float64                   `json:"coverage_threshold"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	requirement, err := s.Store.GetRequirement(strings.TrimSpace(input.RequirementID))
	if err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "requirement_not_found", "requirement not found", nil)
		return
	}
	if workspaceID := requestWorkspace(r, requirement.WorkspaceID); workspaceID != requirement.WorkspaceID {
		s.problem(w, r, http.StatusForbidden, "workspace_access_denied", "requirement is outside the requested workspace", nil)
		return
	}
	contextText := requirement.Title + "\n\n" + requirement.Description
	if len(requirement.AcceptanceCriteria) > 0 {
		contextText += "\n\nAcceptance criteria:\n- " + strings.Join(requirement.AcceptanceCriteria, "\n- ")
	}
	run, err := s.Store.CreatePipeline(domain.PipelineRun{
		WorkspaceID: requirement.WorkspaceID, RequirementID: requirement.ID,
		Roles: input.Roles, MaxRetries: input.MaxRetries, CoverageThreshold: input.CoverageThreshold,
		Context: domain.PipelineContext{RequirementText: contextText},
	})
	if err != nil {
		s.problem(w, r, http.StatusUnprocessableEntity, "pipeline_invalid", err.Error(), nil)
		return
	}
	dispatched, err := s.dispatchPipeline(run)
	if err != nil {
		run.Status, run.SuspendReason, run.UpdatedAt, run.Version = domain.PipelineSuspended, providerSafeError(err), time.Now().UTC(), run.Version+1
		updated, updateErr := s.Store.UpdatePipeline(run, run.Version-1)
		if updateErr == nil {
			run = updated
		}
		s.problem(w, r, http.StatusBadGateway, "pipeline_dispatch_failed", "Multica rejected the first pipeline stage", map[string]any{"pipeline_id": run.ID, "reason": run.SuspendReason})
		return
	}
	s.writeJSON(w, http.StatusCreated, dispatched)
}

func (s *Server) applyPipelineResult(w http.ResponseWriter, r *http.Request, run domain.PipelineRun) {
	var result domain.PipelineStepResult
	if err := decodeJSON(r, &result); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	if result.ProviderIssueID == "" {
		result.ProviderIssueID = run.ActiveProviderIssueID
	}
	if run.ActiveProviderIssueID != "" && result.ProviderIssueID != run.ActiveProviderIssueID {
		s.problem(w, r, http.StatusConflict, "provider_issue_mismatch", "result does not belong to the active Multica issue", nil)
		return
	}
	advanced, err := pipelineengine.NewEngine().Apply(run, result)
	if err != nil {
		code := "pipeline_result_rejected"
		status := http.StatusUnprocessableEntity
		if errors.Is(err, pipelineengine.ErrStaleStage) || errors.Is(err, pipelineengine.ErrWrongAgent) || errors.Is(err, pipelineengine.ErrSessionContinuity) {
			status = http.StatusConflict
		}
		s.problem(w, r, status, code, err.Error(), nil)
		return
	}
	advanced.ActiveProviderIssueID, advanced.ActiveProviderTaskID = "", ""
	advanced, err = s.Store.UpdatePipeline(advanced, run.Version)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrConflict) {
			status = http.StatusConflict
		}
		s.problem(w, r, status, "pipeline_update_failed", err.Error(), nil)
		return
	}
	if advanced.Status == domain.PipelineRunning {
		advanced, err = s.dispatchPipeline(advanced)
		if err != nil {
			advanced.Status, advanced.SuspendReason, advanced.UpdatedAt, advanced.Version = domain.PipelineSuspended, providerSafeError(err), time.Now().UTC(), advanced.Version+1
			advanced, _ = s.Store.UpdatePipeline(advanced, advanced.Version-1)
			s.problem(w, r, http.StatusBadGateway, "pipeline_dispatch_failed", "Multica rejected the next pipeline stage", map[string]any{"pipeline_id": advanced.ID, "reason": advanced.SuspendReason})
			return
		}
	}
	s.writeJSON(w, http.StatusOK, advanced)
}

func (s *Server) dispatchPipeline(run domain.PipelineRun) (domain.PipelineRun, error) {
	agentID := run.Roles.AgentFor(run.PipelineStage)
	prompt, err := pipelinePrompt(run)
	if err != nil {
		return run, err
	}
	if run.PipelineStage == domain.PipelineDevelopment && run.RetryCount > 0 {
		issueID := originalDevelopmentIssue(run)
		continuity, ok := s.Provider.(provider.ContinuityProvider)
		if !ok || issueID == "" {
			return run, errors.New("provider cannot continue the original development session")
		}
		binding, err := continuity.ContinueWorkItem(context.Background(), provider.ContinuationCommand{
			IssueID: issueID, AgentID: agentID, Input: prompt,
			ExpectedSessionID: run.ParentSessionID, ExpectedWorkDir: run.ProviderWorkDir,
		})
		if err != nil {
			return run, err
		}
		if !binding.SessionReused || binding.SessionID != run.ParentSessionID || binding.WorkDir != run.ProviderWorkDir || binding.ProviderRunID == "" {
			return run, errors.New("provider did not confirm the original development task session and workdir")
		}
		run.ActiveProviderIssueID, run.ActiveProviderTaskID = issueID, binding.ProviderRunID
	} else {
		item, err := s.Provider.CreateWorkItem(context.Background(), provider.WorkItemSpec{
			ID: domain.NewID(), RequirementID: run.RequirementID, WorkspaceID: run.WorkspaceID,
			Title:       fmt.Sprintf("ADRO %s · %d/7 %s", run.SessionID[:8], run.PipelineStage, run.PipelineStage.String()),
			Description: prompt, ProviderAssigneeID: agentID, AssigneeType: "agent", Stage: int(run.PipelineStage),
		})
		if err != nil {
			return run, err
		}
		run.ActiveProviderIssueID = item.ProviderIssueID
	}
	run.ActiveAgentID, run.Status = agentID, domain.PipelineWaiting
	run.Version++
	run.UpdatedAt = time.Now().UTC()
	return s.Store.UpdatePipeline(run, run.Version-1)
}

func originalDevelopmentIssue(run domain.PipelineRun) string {
	for _, item := range run.History {
		if item.From == domain.PipelineDevelopment && item.ProviderIssueID != "" {
			return item.ProviderIssueID
		}
	}
	return ""
}

func pipelinePrompt(run domain.PipelineRun) (string, error) {
	contextJSON, err := json.MarshalIndent(run.Context, "", "  ")
	if err != nil {
		return "", err
	}
	callback := strings.TrimRight(os.Getenv("ADRO_PUBLIC_API_URL"), "/")
	if callback == "" {
		callback = "<ADRO_PUBLIC_API_URL>"
	}
	return fmt.Sprintf(`You are the dedicated %s role in ADRO pipeline %s.

Global session_id: %s
parent_session_id: %s
pipeline_stage: %d
retry: %d/%d

Work only on this stage. Read and preserve the complete context below. Development repairs must be incremental in the existing checkout and provider session; never replace the repository baseline.

Context:
%s

When the stage is finished, publish one PipelineStepResult JSON to POST %s/api/v1/pipelines/%s/results using the injected ADRO machine credential. Include stage=%d, agent_id=%q, outcome, provider_issue_id, provider_task_id and provider_session_id. Development also includes code_version; unit tests include coverage; failures include failed_tests and error_log; stage 7 includes the final report.`, run.PipelineStage.String(), run.ID, run.SessionID, run.ParentSessionID, run.PipelineStage, run.RetryCount, run.MaxRetries, contextJSON, callback, run.ID, run.PipelineStage, run.Roles.AgentFor(run.PipelineStage)), nil
}
