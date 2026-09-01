package domain

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// WorkflowMode controls whether a generated design is allowed to advance
// without a human decision. The default remains fully automatic for backwards
// compatibility with the original pipeline API.
type WorkflowMode string

const (
	WorkflowAutomatic      WorkflowMode = "automatic"
	WorkflowDesignApproval WorkflowMode = "design_approval"
)

// WorkflowStep is an explicit, ordered delivery step. Stage values retain the
// legacy seven-stage vocabulary while the slice order determines which stages
// are selected for a particular requirement.
type WorkflowStep struct {
	ID         string         `json:"id"`
	Stage      PipelineStage  `json:"stage"`
	Name       string         `json:"name,omitempty"`
	Role       string         `json:"role,omitempty"`
	AgentID    string         `json:"agent_id"`
	Required   bool           `json:"required"`
	RetryLimit int            `json:"retry_limit,omitempty"`
	Config     map[string]any `json:"config,omitempty"`
}

func (s WorkflowStep) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return errors.New("workflow step id is required")
	}
	if !s.Stage.Valid() {
		return fmt.Errorf("workflow step %q has invalid stage", s.ID)
	}
	if strings.TrimSpace(s.AgentID) == "" {
		return fmt.Errorf("workflow step %q has no agent_id", s.ID)
	}
	if s.RetryLimit < 0 {
		return fmt.Errorf("workflow step %q retry_limit cannot be negative", s.ID)
	}
	return nil
}

type WorkflowTemplate struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Mode        WorkflowMode   `json:"mode"`
	Steps       []WorkflowStep `json:"steps"`
	Version     int64          `json:"version"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

func (w WorkflowTemplate) Validate() error {
	if strings.TrimSpace(w.WorkspaceID) == "" || strings.TrimSpace(w.Name) == "" {
		return errors.New("workspace_id and name are required")
	}
	if w.Mode == "" {
		w.Mode = WorkflowAutomatic
	}
	if w.Mode != WorkflowAutomatic && w.Mode != WorkflowDesignApproval {
		return errors.New("mode must be automatic or design_approval")
	}
	if len(w.Steps) == 0 {
		return errors.New("at least one workflow step is required")
	}
	seenIDs := map[string]bool{}
	seenStages := map[PipelineStage]bool{}
	for _, step := range w.Steps {
		if err := step.Validate(); err != nil {
			return err
		}
		if seenIDs[step.ID] {
			return fmt.Errorf("workflow step id %q is duplicated", step.ID)
		}
		if seenStages[step.Stage] {
			return fmt.Errorf("workflow stage %d is duplicated", step.Stage)
		}
		seenIDs[step.ID] = true
		seenStages[step.Stage] = true
	}
	// A terminal report is required for a workflow to produce an auditable
	// delivery artifact; design, integration, and unit stages remain optional.
	if !seenStages[PipelineReport] {
		return errors.New("workflow must include the report stage")
	}
	return nil
}

func (w WorkflowTemplate) Clone() WorkflowTemplate {
	w.Steps = append([]WorkflowStep(nil), w.Steps...)
	for i := range w.Steps {
		if w.Steps[i].Config != nil {
			w.Steps[i].Config = cloneAnyMap(w.Steps[i].Config)
		}
	}
	return w
}

func cloneAnyMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

// DefaultWorkflow is the compatibility plan used when no custom template is
// supplied. It deliberately keeps all historical stages and role bindings.
func DefaultWorkflow(roles PipelineAgentRoles) []WorkflowStep {
	return []WorkflowStep{
		{ID: "design", Stage: PipelineDesign, Name: "Design", Role: "designer", AgentID: roles.Designer, Required: true},
		{ID: "development", Stage: PipelineDevelopment, Name: "Development", Role: "developer", AgentID: roles.Developer, Required: true},
		{ID: "unit_test", Stage: PipelineUnitTest, Name: "Unit tests", Role: "tester", AgentID: roles.Tester, Required: true},
		{ID: "integration_test", Stage: PipelineIntegration, Name: "Integration tests", Role: "tester", AgentID: roles.Tester, Required: true},
		{ID: "arbitration", Stage: PipelineArbitration, Name: "Repair arbitration", Role: "arbitrator", AgentID: roles.Arbitrator, Required: true},
		{ID: "revalidation", Stage: PipelineRevalidation, Name: "Revalidation", Role: "tester", AgentID: roles.Tester, Required: true},
		{ID: "report", Stage: PipelineReport, Name: "Final report", Role: "tester", AgentID: roles.Tester, Required: true},
	}
}

func NormalizeWorkflow(steps []WorkflowStep) []WorkflowStep {
	result := append([]WorkflowStep(nil), steps...)
	sort.SliceStable(result, func(i, j int) bool { return result[i].Stage < result[j].Stage })
	return result
}

func (w WorkflowTemplate) StepsForRun() []WorkflowStep { return NormalizeWorkflow(w.Steps) }

type ChatSession struct {
	ID               string    `json:"id"`
	WorkspaceID      string    `json:"workspace_id"`
	ProjectID        string    `json:"project_id,omitempty"`
	Title            string    `json:"title"`
	HarnessSessionID string    `json:"harness_session_id"`
	Status           string    `json:"status"`
	CreatedBy        string    `json:"created_by,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (c ChatSession) Validate() error {
	if strings.TrimSpace(c.WorkspaceID) == "" || strings.TrimSpace(c.Title) == "" {
		return errors.New("workspace_id and title are required")
	}
	if strings.TrimSpace(c.HarnessSessionID) == "" {
		return errors.New("harness_session_id is required")
	}
	return nil
}

type ChatMessage struct {
	ID            string    `json:"id"`
	ChatSessionID string    `json:"chat_session_id"`
	WorkspaceID   string    `json:"workspace_id"`
	Role          string    `json:"role"`
	Content       string    `json:"content"`
	AttachmentIDs []string  `json:"attachment_ids,omitempty"`
	TurnID        string    `json:"turn_id,omitempty"`
	TurnHash      string    `json:"turn_hash,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

func (m ChatMessage) Validate() error {
	if strings.TrimSpace(m.ChatSessionID) == "" || strings.TrimSpace(m.WorkspaceID) == "" {
		return errors.New("chat_session_id and workspace_id are required")
	}
	if m.Role != "user" && m.Role != "assistant" && m.Role != "system" {
		return errors.New("role must be user, assistant, or system")
	}
	if strings.TrimSpace(m.Content) == "" {
		return errors.New("content is required")
	}
	return nil
}
