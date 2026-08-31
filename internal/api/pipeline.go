package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
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
	caps, capsErr := s.Provider.Capabilities(r.Context())
	health, healthErr := s.Provider.Health(r.Context())
	if capsErr != nil || healthErr != nil || !health.Healthy || !caps.Supports("run.snapshot.v1") {
		detail := map[string]any{"capability": "run.snapshot.v1"}
		if capsErr != nil {
			detail["error_code"] = provider.ErrorCodeOf(capsErr)
		}
		s.problem(w, r, http.StatusServiceUnavailable, "executor_unavailable", "automated pipelines require a healthy local executor with run snapshot support", detail)
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
		s.problem(w, r, http.StatusBadGateway, "pipeline_dispatch_failed", "executor rejected the first pipeline stage", map[string]any{"pipeline_id": run.ID, "reason": run.SuspendReason})
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
	advanced, status, err := s.advancePipeline(run, result)
	if err != nil {
		s.problem(w, r, status, "pipeline_result_rejected", err.Error(), nil)
		return
	}
	s.writeJSON(w, http.StatusOK, advanced)
}

// advancePipeline is shared by the explicit result endpoint and the local
// process collector. Keeping both paths on one state transition prevents the
// native executor from gaining a second, subtly different workflow policy.
func (s *Server) advancePipeline(run domain.PipelineRun, result domain.PipelineStepResult) (domain.PipelineRun, int, error) {
	if result.ProviderIssueID == "" {
		result.ProviderIssueID = run.ActiveProviderIssueID
	}
	if run.ActiveProviderIssueID != "" && result.ProviderIssueID != run.ActiveProviderIssueID {
		return run, http.StatusConflict, errors.New("result does not belong to the active execution item")
	}
	advanced, err := pipelineengine.NewEngine().Apply(run, result)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, pipelineengine.ErrStaleStage) || errors.Is(err, pipelineengine.ErrWrongAgent) || errors.Is(err, pipelineengine.ErrSessionContinuity) {
			status = http.StatusConflict
		}
		return run, status, err
	}
	advanced.ActiveProviderIssueID, advanced.ActiveProviderTaskID = "", ""
	advanced, err = s.Store.UpdatePipeline(advanced, run.Version)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrConflict) {
			status = http.StatusConflict
		}
		return run, status, err
	}
	if run.PipelineStage == domain.PipelineIntegration && result.Outcome == "fail" {
		if bug, bugErr := s.materializePipelineBug(advanced, result); bugErr == nil && bug.ID != "" {
			advanced.BugID = bug.ID
			advanced.Version++
			advanced.UpdatedAt = time.Now().UTC()
			if saved, updateErr := s.Store.UpdatePipeline(advanced, advanced.Version-1); updateErr == nil {
				advanced = saved
			}
		}
	}
	if advanced.Status == domain.PipelineRunning {
		advanced, err = s.dispatchPipeline(advanced)
		if err != nil {
			advanced.Status, advanced.SuspendReason, advanced.UpdatedAt, advanced.Version = domain.PipelineSuspended, providerSafeError(err), time.Now().UTC(), advanced.Version+1
			advanced, _ = s.Store.UpdatePipeline(advanced, advanced.Version-1)
			return advanced, http.StatusBadGateway, fmt.Errorf("executor rejected the next pipeline stage: %s", advanced.SuspendReason)
		}
	}
	return advanced, http.StatusOK, nil
}

func (s *Server) materializePipelineBug(run domain.PipelineRun, result domain.PipelineStepResult) (domain.Bug, error) {
	repositoryID := "pipeline"
	if run.PipelineWorkItemID != "" {
		if item, err := s.Store.GetWorkItem(run.PipelineWorkItemID); err == nil {
			repositoryID = item.RepositoryID
		}
	}
	title := "Pipeline integration failure"
	if strings.TrimSpace(result.Summary) != "" {
		title = result.Summary
	}
	fingerprint := "pipeline:" + run.ID + ":integration"
	bug, _, err := s.Store.UpsertBug(domain.Bug{
		WorkspaceID: run.WorkspaceID, RequirementID: run.RequirementID, WorkItemID: run.PipelineWorkItemID,
		RepositoryID: repositoryID, AssigneeMemberID: run.Roles.Developer, Fingerprint: fingerprint,
		Title: title, Steps: "Run the integration stage for pipeline " + run.ID,
		Expected: "All integration checks pass", Actual: "Integration checks failed",
		LogExcerpt: result.ErrorLog, Status: domain.BugOpen,
	})
	return bug, err
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
		if run.PipelineWorkItemID != "" {
			if provenance, found := s.Store.FindProvenance(run.PipelineWorkItemID); found {
				provenance.ProviderTaskID = binding.ProviderRunID
				provenance.ProviderSessionID = binding.SessionID
				provenance.ProviderWorkDir = binding.WorkDir
				provenance.ContextVersion = run.Version
				_ = s.Store.SaveProvenance(provenance)
			}
		}
		run.ActiveProviderIssueID, run.ActiveProviderTaskID = issueID, binding.ProviderRunID
	} else {
		repositoryID, repositoryPath, cloneURL, defaultBranch := "", "", "", ""
		if requirement, requirementErr := s.Store.GetRequirement(run.RequirementID); requirementErr == nil && len(requirement.RepositoryIDs) > 0 {
			repositoryID = requirement.RepositoryIDs[0]
			if repository, repositoryErr := s.Store.GetRepository(repositoryID); repositoryErr == nil {
				cloneURL, defaultBranch = repository.CloneURL, repository.DefaultBranch
				if repository.Metadata != nil {
					if value, ok := repository.Metadata["local_path"].(string); ok {
						repositoryPath = strings.TrimSpace(value)
					}
				}
			}
		}
		providerWorkItemID := domain.NewID()
		if run.PipelineStage == domain.PipelineDevelopment && run.PipelineWorkItemID == "" {
			logicalRepositoryID := repositoryID
			if logicalRepositoryID == "" {
				logicalRepositoryID = "pipeline-" + run.ID
			}
			logical, logicalErr := s.Store.CreateWorkItem(domain.WorkItem{
				ID: logicalWorkItemID(run), RequirementID: run.RequirementID, RepositoryID: logicalRepositoryID,
				MemberID: agentID, DeveloperAgentBindingID: agentID, Role: "developer", Status: "in_progress", Stage: int(run.PipelineStage),
			})
			if logicalErr != nil {
				return run, logicalErr
			}
			providerWorkItemID = logical.ID
			run.PipelineWorkItemID = logical.ID
		}
		item := provider.ProviderWorkItem{ID: providerWorkItemID}
		if run.PipelineWorkItemID != "" && run.PipelineStage != domain.PipelineDevelopment {
			logical, logicalErr := s.Store.GetWorkItem(run.PipelineWorkItemID)
			if logicalErr != nil {
				return run, logicalErr
			}
			item = provider.ProviderWorkItem{ID: logical.ID, ProviderIssueID: logical.ProviderIssueID}
		}
		if item.ProviderIssueID == "" {
			item, err = s.Provider.CreateWorkItem(context.Background(), provider.WorkItemSpec{
				ID: providerWorkItemID, RequirementID: run.RequirementID, WorkspaceID: run.WorkspaceID,
				Title:       fmt.Sprintf("ADRO %s · %d/7 %s", run.SessionID[:8], run.PipelineStage, run.PipelineStage.String()),
				Description: prompt, ProviderAssigneeID: agentID, AssigneeType: "agent", Stage: int(run.PipelineStage),
				RepositoryID: repositoryID, RepositoryPath: repositoryPath, CloneURL: cloneURL, DefaultBranch: defaultBranch,
			})
			if err != nil {
				return run, err
			}
		}
		if run.PipelineStage == domain.PipelineDevelopment && run.PipelineWorkItemID != "" {
			if logical, logicalErr := s.Store.GetWorkItem(run.PipelineWorkItemID); logicalErr == nil {
				logical.ProviderIssueID = item.ProviderIssueID
				logical.UpdatedAt = time.Now().UTC()
				if logicalErr = s.Store.UpdateWorkItem(logical); logicalErr != nil {
					return run, logicalErr
				}
			}
		}
		run.ActiveProviderIssueID = item.ProviderIssueID
		binding, err := s.Provider.StartRun(context.Background(), provider.StartRunCommand{
			WorkItemID: item.ID, ProviderIssueID: item.ProviderIssueID, AgentBindingID: agentID, ProviderAssigneeID: agentID,
			Input: prompt, ContextID: "pipeline-" + run.ID, ContextVersion: run.Version,
		})
		if err != nil {
			return run, err
		}
		if run.PipelineStage == domain.PipelineDevelopment && run.PipelineWorkItemID != "" {
			_ = s.Store.SaveProvenance(domain.Provenance{
				WorkItemID: run.PipelineWorkItemID, RequirementID: run.RequirementID, AgentBindingID: agentID,
				Provider: "local", ProviderTaskID: binding.ProviderRunID, ProviderSessionID: binding.SessionID,
				ProviderWorkDir: binding.WorkDir, RepositoryID: repositoryID, ContextVersion: run.Version,
			})
		}
		run.ActiveProviderTaskID = binding.ProviderRunID
	}
	run.ActiveAgentID, run.Status = agentID, domain.PipelineWaiting
	run.Version++
	run.UpdatedAt = time.Now().UTC()
	updated, err := s.Store.UpdatePipeline(run, run.Version-1)
	if err != nil {
		return run, err
	}
	s.watchLocalPipelineRun(updated)
	return updated, nil
}

func logicalWorkItemID(run domain.PipelineRun) string {
	return "pipeline-work-" + run.ID
}

// watchLocalPipelineRun closes the gap between a real local process finishing
// and a client having to call ADRO's result endpoint. The prompt asks the
// client to print one ADRO_RESULT_JSON marker; ADRO remains the authority that
// validates and advances the state machine. Providers without this native
// process boundary keep the explicit result endpoint and are never inferred.
func (s *Server) watchLocalPipelineRun(run domain.PipelineRun) {
	caps, err := s.Provider.Capabilities(context.Background())
	if err != nil || caps.Provider != "local" || run.ActiveProviderTaskID == "" {
		return
	}
	go func(pipelineID, taskID string) {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.NewTimer(30 * time.Minute)
		defer deadline.Stop()
		for {
			select {
			case <-ticker.C:
			case <-deadline.C:
				return
			}
			current, getErr := s.Store.GetPipeline(pipelineID)
			if getErr != nil || current.Status != domain.PipelineWaiting || current.ActiveProviderTaskID != taskID {
				return
			}
			snapshot, snapshotErr := s.Provider.GetRun(context.Background(), taskID)
			if snapshotErr != nil || snapshot.Status == "running" {
				continue
			}
			result, ok := pipelineResultFromSnapshot(current, snapshot)
			if !ok {
				// A completed client that did not emit the marker can still be
				// completed by an explicit plugin callback. Do not invent evidence.
				return
			}
			advanced, _, advanceErr := s.advancePipeline(current, result)
			if advanceErr != nil {
				// Invalid process evidence must be visible and terminal rather
				// than leaving an operator staring at an eternal waiting state.
				current.Status = domain.PipelineSuspended
				current.SuspendReason = advanceErr.Error()
				current.UpdatedAt = time.Now().UTC()
				current.Version++
				_, _ = s.Store.UpdatePipeline(current, current.Version-1)
				return
			}
			if advanced.Status != domain.PipelineWaiting || advanced.ActiveProviderTaskID == "" {
				return
			}
			taskID = advanced.ActiveProviderTaskID
		}
	}(run.ID, run.ActiveProviderTaskID)
}

func pipelineResultFromSnapshot(run domain.PipelineRun, snapshot provider.RunSnapshot) (domain.PipelineStepResult, bool) {
	if snapshot.Status != "completed" && snapshot.Status != "failed" && snapshot.Status != "cancelled" {
		return domain.PipelineStepResult{}, false
	}
	result := domain.PipelineStepResult{
		Stage:             run.PipelineStage,
		AgentID:           run.Roles.AgentFor(run.PipelineStage),
		Outcome:           "pass",
		Summary:           "local executor completed the stage",
		ProviderIssueID:   snapshot.ProviderIssueID,
		ProviderTaskID:    snapshot.ID,
		ProviderSessionID: snapshot.SessionID,
		ProviderWorkDir:   snapshot.WorkDir,
	}
	if snapshot.Status == "failed" || snapshot.Status == "cancelled" {
		result.Outcome = "fail"
		result.Summary = "local executor " + snapshot.Status + " the stage"
		result.ErrorLog = strings.TrimSpace(snapshot.Error + "\n" + snapshot.Output)
		if result.ErrorLog == "" {
			result.ErrorLog = "local executor " + snapshot.Status + " the stage"
		}
	}
	for _, candidate := range []string{snapshot.Output, extractProviderResult(snapshot.Output)} {
		if marker := parsePipelineResultMarker(candidate); marker != nil {
			// Provider provenance is measured by the adapter, never trusted from
			// model text. A model can describe the result, but it cannot forge the
			// execution item or session that produced it.
			marker.ProviderIssueID = result.ProviderIssueID
			marker.ProviderTaskID = result.ProviderTaskID
			marker.ProviderSessionID = result.ProviderSessionID
			marker.ProviderWorkDir = result.ProviderWorkDir
			switch strings.ToLower(strings.TrimSpace(marker.Outcome)) {
			case "success", "succeeded", "ok":
				marker.Outcome = "pass"
			case "failure", "failed", "error":
				marker.Outcome = "fail"
			}
			if marker.Stage == 0 {
				marker.Stage = result.Stage
			}
			if marker.AgentID == "" {
				marker.AgentID = result.AgentID
			}
			if marker.CodeVersion == "" && snapshot.HeadCommit != "" {
				marker.CodeVersion = snapshot.HeadCommit
			}
			if marker.CodeVersion == "" && snapshot.WorkspaceDirty {
				marker.CodeVersion = "working-tree"
			}
			// A non-zero process exit is authoritative. A client must not be able
			// to print a successful marker while its command actually failed.
			if snapshot.Status == "failed" || snapshot.Status == "cancelled" {
				marker.Outcome = "fail"
				if strings.TrimSpace(marker.ErrorLog) == "" {
					marker.ErrorLog = result.ErrorLog
				}
			}
			providerText := providerNarrative(snapshot.Output)
			if marker.DesignDoc == "" && marker.Stage == domain.PipelineDesign {
				marker.DesignDoc = strings.TrimSpace(providerText)
			}
			if marker.Report == "" && marker.Stage == domain.PipelineReport {
				marker.Report = strings.TrimSpace(providerText)
			}
			return *marker, true
		}
	}
	// A completed local process without the ADRO marker is still a failed
	// execution: accepting an unstructured exit would make the pipeline wait
	// forever and would provide no stage evidence. This also covers clients
	// that report an API/authentication error while exiting with status 0.
	if strings.TrimSpace(result.ErrorLog) == "" {
		result.ErrorLog = "local executor completed without ADRO_RESULT_JSON evidence"
	}
	result.Outcome = "fail"
	result.Summary = "local executor returned no ADRO_RESULT_JSON evidence"
	return result, true
}

func extractProviderResult(output string) string {
	var envelope struct {
		Result string `json:"result"`
	}
	if json.Unmarshal([]byte(output), &envelope) == nil {
		return envelope.Result
	}
	return ""
}

func providerNarrative(output string) string {
	if narrative := codexAgentNarrative(output); narrative != "" {
		return narrative
	}
	if result := strings.TrimSpace(extractProviderResult(output)); result != "" {
		return result
	}
	if marker := strings.LastIndex(output, "ADRO_RESULT_JSON"); marker >= 0 {
		return strings.TrimSpace(output[:marker])
	}
	return strings.TrimSpace(output)
}

func parsePipelineResultMarker(output string) *domain.PipelineStepResult {
	// Codex --json is a JSONL stream. Only agent_message items are eligible
	// evidence; command output can contain old marker strings (for example
	// when an agent searches a state file) and must never advance a pipeline.
	if isJSONLEventStream(output) {
		return parseCodexAgentMessageMarker(output)
	}
	return parseMarkerText(output)
}

func isJSONLEventStream(output string) bool {
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(line), &event) == nil && strings.TrimSpace(event.Type) != "" {
			return true
		}
	}
	return false
}

func parseCodexAgentMessageMarker(output string) *domain.PipelineStepResult {
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	var marker *domain.PipelineStepResult
	for scanner.Scan() {
		itemType, itemText, ok := codexAgentMessage(scanner.Bytes())
		if !ok || !isCodexAgentMessageType(itemType) {
			continue
		}
		if candidate := parseMarkerText(itemText); candidate != nil {
			marker = candidate
		}
	}
	return marker
}

func isCodexAgentMessageType(value string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", ""))
	return normalized == "agentmessage"
}

// codexAgentMessage extracts assistant text from both Codex JSONL envelopes
// seen in the wild. Older clients emit item.completed with a lower-case
// agent_message/text item; current clients wrap item_completed inside an
// event_msg payload and expose an AgentMessage content array.
func codexAgentMessage(line []byte) (itemType, text string, ok bool) {
	var event struct {
		Type    string           `json:"type"`
		Item    codexMessageItem `json:"item"`
		Payload struct {
			Type string           `json:"type"`
			Item codexMessageItem `json:"item"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &event) != nil {
		return "", "", false
	}
	item := event.Item
	if item.Type != "" {
		if !isCodexCompletedEvent(event.Type) {
			return "", "", false
		}
	} else {
		item = event.Payload.Item
		if item.Type == "" || !isCodexCompletedEvent(event.Payload.Type) {
			return "", "", false
		}
	}
	if item.Text != "" {
		return item.Type, item.Text, true
	}
	var parts []string
	for _, part := range item.Content {
		if strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
	}
	return item.Type, strings.Join(parts, "\n"), true
}

func isCodexCompletedEvent(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return normalized == "item.completed" || normalized == "item_completed"
}

type codexMessageItem struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func parseMarkerText(output string) *domain.PipelineStepResult {
	idx := strings.LastIndex(output, "ADRO_RESULT_JSON")
	if idx < 0 {
		return nil
	}
	fragment := output[idx+len("ADRO_RESULT_JSON"):]
	start := strings.IndexByte(fragment, '{')
	if start < 0 {
		return nil
	}
	if result, err := decodePipelineStepResult([]byte(fragment[start:])); err == nil {
		return result
	}
	// Codex --json emits the agent message as a JSON string, so the marker
	// appears as {\"stage\":...} in the raw JSONL stream. Decode the escaped
	// object as a second pass while keeping the provider output auditable.
	unescaped := strings.ReplaceAll(fragment[start:], `\"`, `"`)
	result, err := decodePipelineStepResult([]byte(unescaped))
	if err != nil {
		return nil
	}
	return result
}

type pipelineStepResultWire struct {
	Stage             domain.PipelineStage `json:"stage"`
	AgentID           string               `json:"agent_id"`
	Outcome           string               `json:"outcome"`
	Summary           string               `json:"summary"`
	DesignDoc         string               `json:"design_doc"`
	CodeVersion       string               `json:"code_version"`
	Coverage          json.RawMessage      `json:"coverage"`
	PassedTests       json.RawMessage      `json:"passed_tests"`
	Tests             json.RawMessage      `json:"tests"`
	FailedTests       json.RawMessage      `json:"failed_tests"`
	ErrorLog          json.RawMessage      `json:"error_log"`
	RepairNote        string               `json:"repair_note"`
	Report            string               `json:"report"`
	FinalReport       string               `json:"final_report"`
	ProviderIssueID   string               `json:"provider_issue_id"`
	ProviderTaskID    string               `json:"provider_task_id"`
	ProviderSessionID string               `json:"provider_session_id"`
	ProviderWorkDir   string               `json:"provider_work_dir"`
}

func decodePipelineStepResult(data []byte) (*domain.PipelineStepResult, error) {
	var wire pipelineStepResultWire
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&wire); err != nil {
		return nil, err
	}
	result := &domain.PipelineStepResult{
		Stage: wire.Stage, AgentID: wire.AgentID, Outcome: wire.Outcome, Summary: wire.Summary,
		DesignDoc: wire.DesignDoc, CodeVersion: wire.CodeVersion, RepairNote: wire.RepairNote,
		Report: wire.Report, ProviderIssueID: wire.ProviderIssueID, ProviderTaskID: wire.ProviderTaskID,
		ProviderSessionID: wire.ProviderSessionID, ProviderWorkDir: wire.ProviderWorkDir,
		Coverage: pipelineCoverage(wire.Coverage), PassedTests: pipelineStrings(wire.PassedTests),
		FailedTests: pipelineStrings(wire.FailedTests), ErrorLog: pipelineErrorLog(wire.ErrorLog),
	}
	if len(result.PassedTests) == 0 {
		result.PassedTests = pipelineStrings(wire.Tests)
	}
	if result.Report == "" {
		result.Report = wire.FinalReport
	}
	return result, nil
}

func pipelineCoverage(raw json.RawMessage) float64 {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var number float64
	if json.Unmarshal(raw, &number) == nil {
		return number
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		// CLIs commonly report coverage as "100.0% of statements". Keep the
		// wire contract provider-neutral while accepting that human-readable
		// form from real executors.
		for _, token := range strings.Fields(text) {
			token = strings.Trim(token, ":,;()[]")
			token = strings.TrimSuffix(token, "%")
			if value, err := strconv.ParseFloat(token, 64); err == nil && value >= 0 && value <= 100 {
				return value
			}
		}
		return 0
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return 0
	}
	for _, key := range []string{"percent", "percentage", "value", "coverage"} {
		if value := pipelineCoverage(object[key]); value > 0 {
			return value
		}
	}
	return 0
}

func pipelineStrings(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var values []string
	if json.Unmarshal(raw, &values) == nil {
		return values
	}
	return nil
}

func pipelineErrorLog(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var message string
	if json.Unmarshal(raw, &message) == nil {
		return message
	}
	var messages []string
	if json.Unmarshal(raw, &messages) == nil {
		return strings.Join(messages, "; ")
	}
	return ""
}

func codexAgentNarrative(output string) string {
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	var messages []string
	for scanner.Scan() {
		itemType, itemText, ok := codexAgentMessage(scanner.Bytes())
		if !ok || !isCodexAgentMessageType(itemType) {
			continue
		}
		text := strings.TrimSpace(itemText)
		if text == "" {
			continue
		}
		if marker := strings.Index(text, "ADRO_RESULT_JSON"); marker >= 0 {
			text = strings.TrimSpace(text[:marker])
		}
		if text != "" {
			messages = append(messages, text)
		}
	}
	if len(messages) == 0 {
		return ""
	}
	// Keep handoff context bounded even when a client emits many progress
	// messages. The latest messages carry the most useful stage narrative.
	const maxNarrative = 12000
	var selected []string
	length := 0
	for i := len(messages) - 1; i >= 0; i-- {
		part := messages[i]
		if length+len(part)+2 > maxNarrative {
			break
		}
		selected = append([]string{part}, selected...)
		length += len(part) + 2
	}
	return strings.TrimSpace(strings.Join(selected, "\n\n"))
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
		callback = "http://127.0.0.1:8080"
	}
	stageInstructions := map[domain.PipelineStage]string{
		domain.PipelineDesign:       "Describe the implementation plan only; do not modify files or run later-stage checks.",
		domain.PipelineDevelopment:  "Implement the requested change in the existing checkout and add focused tests. Keep all prior work and make an incremental change.",
		domain.PipelineUnitTest:     "Run the requested unit tests (go test ./... when the checkout is Go). Report measured coverage and fix failures within this stage.",
		domain.PipelineIntegration:  "Run ADRO_E2E_INTEGRATION_COUNTER=.adro-e2e-integration-counter ./integration-check.sh exactly once in this stage. If it exits non-zero, stop immediately and emit outcome=fail with the real exit code and error output; do not rerun it, repair files, or report pass. ADRO will create the Bug and schedule arbitration and repair. Only the revalidation stage may rerun checks after repair.",
		domain.PipelineArbitration:  "Review the recorded integration failure and approve a focused repair back to the original development session when it is actionable.",
		domain.PipelineRevalidation: "After repair, rerun go test ./... and then run ADRO_E2E_INTEGRATION_COUNTER=.adro-e2e-integration-counter ./integration-check.sh. Report both real exit codes and evidence; this is the only stage allowed to rerun the integration check after the recorded failure.",
		domain.PipelineReport:       "Summarize the complete run with coverage, test pass/fail evidence, failure and repair history.",
	}[run.PipelineStage]
	return fmt.Sprintf(`You are the dedicated %s role in ADRO pipeline %s.

Global session_id: %s
parent_session_id: %s
pipeline_stage: %d
retry: %d/%d

Work only on this stage. Read and preserve the complete context below. Development repairs must be incremental in the existing checkout and provider session; never replace the repository baseline.
Stage instruction: %s

Do not inspect ADRO's state files or search for provider IDs. The adapter supplies authoritative provider_issue_id, provider_task_id, provider_session_id and provider_work_dir; those fields may be omitted from your marker. Do not call an ADRO callback yourself.

Context:
%s

When the stage is finished, your final assistant message MUST contain exactly one line beginning with ADRO_RESULT_JSON= followed by a single JSON object. Emit that line even when a requested command fails, using outcome=fail and error_log. ADRO collects this marker from the real process output and validates it before advancing the pipeline; do not claim success without running the requested checks. Include stage=%d, agent_id=%q and outcome. Development also includes code_version; unit tests include coverage; failures include failed_tests and error_log; stage 7 includes the final report. An external plugin may instead POST the same PipelineStepResult JSON to %s/api/v1/pipelines/%s/results.`, run.PipelineStage.String(), run.ID, run.SessionID, run.ParentSessionID, run.PipelineStage, run.RetryCount, run.MaxRetries, stageInstructions, contextJSON, run.PipelineStage, run.Roles.AgentFor(run.PipelineStage), callback, run.ID), nil
}
