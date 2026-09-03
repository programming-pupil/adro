package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/harness"
	evidencememory "github.com/adro-project/adro/internal/memory"
	"github.com/adro-project/adro/internal/provider"
)

func (s *Server) ensureHarnessSession(run domain.PipelineRun) error {
	if s.Harness == nil {
		return errors.New("session harness is not configured")
	}
	_, err := s.Harness.EnsureSession(harness.Session{ID: run.SessionID, TenantID: run.WorkspaceID, WorkspaceID: run.WorkspaceID, BudgetTokens: harnessSessionBudget()})
	if err != nil {
		return fmt.Errorf("ensure durable session: %w", err)
	}
	return nil
}

func harnessSessionBudget() int64 {
	value := strings.TrimSpace(os.Getenv("ADRO_SESSION_BUDGET_TOKENS"))
	if value == "" {
		return 0
	}
	budget, err := strconv.ParseInt(value, 10, 64)
	if err != nil || budget <= 0 {
		return 0
	}
	return budget
}

// compiledHarnessEnvelope is the typed dispatch boundary shared by legacy
// pipeline adapters and graph-native callers. A zero session budget still gets
// a minimal one-token manifest so providers can verify session continuity and
// selection/replay digests instead of receiving only a legacy ContextID.
func (s *Server) compiledHarnessEnvelope(sessionID string) (harness.ContextEnvelope, error) {
	if s == nil || s.Harness == nil {
		return harness.ContextEnvelope{}, errors.New("session harness is not configured")
	}
	session, err := s.Harness.GetSession(sessionID)
	if err != nil {
		return harness.ContextEnvelope{}, err
	}
	budget := session.BudgetTokens
	if budget <= 0 {
		budget = 1
	}
	return s.Harness.CompileEnvelope(sessionID, budget)
}

func (s *Server) saveHarnessCheckpoint(sessionID string, phase harness.CheckpointPhase, eventHash string, contextVersion int64, outboxIDs, leaseIDs []string, state string) error {
	if s.Harness == nil {
		return errors.New("session harness is not configured")
	}
	status, err := s.Harness.ContextStatus(sessionID)
	if err != nil {
		return fmt.Errorf("read harness context status: %w", err)
	}
	_, err = s.Harness.SaveCheckpoint(sessionID, harness.Checkpoint{TurnSequence: int64(status.TurnCount), Phase: phase, EventHash: eventHash, ContextVersion: contextVersion, OutboxIDs: outboxIDs, LeaseIDs: leaseIDs, State: state})
	if err != nil {
		return fmt.Errorf("persist harness checkpoint: %w", err)
	}
	return nil
}

func (s *Server) recordHarnessResult(run domain.PipelineRun, result domain.PipelineStepResult) error {
	if s.Harness == nil {
		return errors.New("session harness is not configured")
	}
	if err := s.ensureHarnessSession(run); err != nil {
		return err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode pipeline result for harness: %w", err)
	}
	key := fmt.Sprintf("pipeline:%s:stage:%d:result:%s", run.ID, result.Stage, strings.TrimSpace(result.ProviderTaskID))
	if strings.HasSuffix(key, ":result:") {
		key = fmt.Sprintf("pipeline:%s:stage:%d:version:%d:result", run.ID, result.Stage, run.Version)
	}
	turn, err := s.Harness.AppendTurn(run.SessionID, harness.Turn{Role: harness.RoleAssistant, Content: string(data), AttemptID: result.ProviderTaskID, IdempotencyKey: key, Metadata: map[string]string{"stage": fmt.Sprintf("%d", result.Stage), "outcome": result.Outcome}})
	if err != nil {
		return fmt.Errorf("persist harness result turn: %w", err)
	}
	if err := s.saveHarnessCheckpoint(run.SessionID, harness.CheckpointToolAfter, turn.Hash, run.Version, nil, nil, "pipeline result recorded"); err != nil {
		return err
	}
	return nil
}

func (s *Server) recordProviderToolEvents(run domain.PipelineRun, snapshot provider.RunSnapshot) error {
	if s.Harness == nil || run.SessionID == "" || len(snapshot.ToolEvents) == 0 {
		return nil
	}
	type pendingTool struct{ name, input string }
	pending := map[string]pendingTool{}
	for _, event := range snapshot.ToolEvents {
		callID := strings.TrimSpace(event.CallID)
		if callID == "" {
			continue
		}
		// Codex item ids restart at item_1 for each provider turn. Harness tool
		// checkpoints live in the longer ADRO session, so the provider run/attempt
		// id is part of the durable identity; otherwise two legitimate stages can
		// collide and the second tool transaction is rejected as different content
		// under the same idempotency key.
		if runID := strings.TrimSpace(snapshot.ID); runID != "" {
			callID = runID + ":" + callID
		}
		switch event.Phase {
		case "before":
			if _, exists := pending[callID]; !exists {
				pending[callID] = pendingTool{name: event.Name, input: event.Payload}
			}
		case "after":
			before, exists := pending[callID]
			if !exists {
				// An incomplete stream is retained in the provider snapshot but
				// cannot be acknowledged as a side effect without a before phase.
				continue
			}
			if _, err := s.Harness.RecordToolCall(run.SessionID, callID, before.name, before.input, event.Payload, run.Version); err != nil {
				return fmt.Errorf("record tool checkpoint %s: %w", callID, err)
			}
			delete(pending, callID)
		}
	}
	return nil
}

// sessionRoute exposes ADRO-owned transcript and recovery primitives. Provider
// runs remain separate resources; this route is the stable API surface used by
// UI clients and external execution plugins when a provider-native session is
// unavailable or cannot be trusted for continuity.
func (s *Server) sessionRoute(w http.ResponseWriter, r *http.Request, tail string) {
	if s.Harness == nil {
		s.problem(w, r, http.StatusServiceUnavailable, "harness_unavailable", "session harness is not configured", nil)
		return
	}
	tail = strings.Trim(tail, "/")
	if tail == "" {
		if r.Method != http.MethodPost {
			s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		var input struct {
			ID                   string  `json:"id,omitempty"`
			TenantID             string  `json:"tenant_id,omitempty"`
			WorkspaceID          string  `json:"workspace_id"`
			ProjectID            string  `json:"project_id,omitempty"`
			BudgetTokens         int64   `json:"budget_tokens,omitempty"`
			AutoCompaction       *bool   `json:"auto_compaction,omitempty"`
			CompactionThreshold  float64 `json:"compaction_threshold,omitempty"`
			CompactionRetainTail int     `json:"compaction_retain_tail,omitempty"`
		}
		if err := decodeJSON(r, &input); err != nil {
			s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
			return
		}
		input.WorkspaceID = requestWorkspace(r, input.WorkspaceID)
		// Tenant identity is derived from the authenticated request boundary. A
		// JSON body must never be able to mint a session in another tenant.
		input.TenantID = tenant(r)
		if input.WorkspaceID == "" {
			s.problem(w, r, http.StatusUnprocessableEntity, "validation_error", "workspace_id is required", nil)
			return
		}
		autoCompaction := input.BudgetTokens > 0
		if input.AutoCompaction != nil {
			autoCompaction = *input.AutoCompaction
		}
		created, err := s.Harness.CreateSession(harness.Session{ID: strings.TrimSpace(input.ID), TenantID: strings.TrimSpace(input.TenantID), WorkspaceID: strings.TrimSpace(input.WorkspaceID), ProjectID: strings.TrimSpace(input.ProjectID), BudgetTokens: input.BudgetTokens, AutoCompaction: autoCompaction, AutoCompactionSet: input.AutoCompaction != nil, CompactionThreshold: input.CompactionThreshold, CompactionRetainTail: input.CompactionRetainTail})
		if err != nil {
			status := http.StatusUnprocessableEntity
			if errors.Is(err, harness.ErrConflict) {
				status = http.StatusConflict
			}
			s.problem(w, r, status, "session_create_failed", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusCreated, created)
		return
	}
	parts := strings.Split(tail, "/")
	session, err := s.Harness.GetSession(parts[0])
	if err != nil {
		s.problem(w, r, http.StatusNotFound, "session_not_found", "session not found", nil)
		return
	}
	if workspaceID := requestWorkspace(r, ""); workspaceID != "" && session.WorkspaceID != workspaceID {
		s.problem(w, r, http.StatusNotFound, "session_not_found", "session not found", nil)
		return
	}
	if requestedTenant := strings.TrimSpace(r.Header.Get("X-Tenant-ID")); requestedTenant != "" && session.TenantID != requestedTenant {
		s.problem(w, r, http.StatusNotFound, "session_not_found", "session not found", nil)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		status, statusErr := s.Harness.ContextStatus(session.ID)
		if statusErr != nil {
			s.problem(w, r, http.StatusInternalServerError, "session_status_failed", statusErr.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"session": session, "context": status})
		return
	}
	if len(parts) == 2 && parts[1] == "turns" {
		s.sessionTurns(w, r, session)
		return
	}
	if len(parts) == 2 && parts[1] == "checkpoints" {
		s.sessionCheckpoints(w, r, session)
		return
	}
	if len(parts) == 2 && parts[1] == "memory" {
		s.sessionMemory(w, r, session)
		return
	}
	if len(parts) == 3 && parts[1] == "memory" && r.Method == http.MethodPatch {
		var input struct {
			Status   string `json:"status"`
			Reviewer string `json:"reviewer,omitempty"`
			Reason   string `json:"reason,omitempty"`
		}
		if err := decodeJSON(r, &input); err != nil {
			s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
			return
		}
		item, transitionErr := s.Harness.TransitionMemoryWithReview(session.ID, parts[2], input.Status, input.Reviewer, input.Reason)
		if transitionErr != nil {
			status := http.StatusUnprocessableEntity
			if errors.Is(transitionErr, harness.ErrConflict) {
				status = http.StatusConflict
			}
			s.problem(w, r, status, "memory_transition_failed", transitionErr.Error(), nil)
			return
		}
		response := map[string]any{}
		if encoded, encodeErr := json.Marshal(item); encodeErr == nil {
			_ = json.Unmarshal(encoded, &response)
		}
		if s.Memory != nil {
			actor := strings.TrimSpace(input.Reviewer)
			if actor == "" {
				actor = strings.TrimSpace(r.Header.Get("X-Member-ID"))
			}
			if actor == "" {
				actor = "api"
			}
			reason := strings.TrimSpace(input.Reason)
			if reason == "" {
				reason = "memory lifecycle update"
			}
			evidence, memoryErr := transitionEvidenceMemory(s.Memory, evidenceMemoryScope(session), parts[2], evidencememory.Status(input.Status), actor, reason)
			if memoryErr != nil && !errors.Is(memoryErr, evidencememory.ErrNotFound) {
				s.problem(w, r, http.StatusConflict, "evidence_memory_transition_failed", memoryErr.Error(), nil)
				return
			}
			if memoryErr == nil {
				response["evidence_item"] = evidence
			}
		}
		s.writeJSON(w, http.StatusOK, response)
		return
	}
	if len(parts) == 3 && parts[1] == "memory" && parts[2] == "reduce" && r.Method == http.MethodPost {
		s.sessionMemoryReduce(w, r, session)
		return
	}
	if len(parts) == 3 && parts[1] == "context" && parts[2] == "archives" && r.Method == http.MethodGet {
		items, listErr := s.Harness.ListArchives(session.ID)
		if listErr != nil {
			s.problem(w, r, http.StatusInternalServerError, "archive_list_failed", listErr.Error(), nil)
			return
		}
		response := map[string]any{"items": items}
		if s.Memory != nil {
			response["evidence_items"] = s.Memory.List(evidenceMemoryScope(session), "", time.Now().UTC())
		}
		s.writeJSON(w, http.StatusOK, response)
		return
	}
	if len(parts) == 3 && parts[1] == "context" && parts[2] == "status" && r.Method == http.MethodGet {
		status, statusErr := s.Harness.ContextStatus(session.ID)
		if statusErr != nil {
			s.problem(w, r, http.StatusInternalServerError, "session_status_failed", statusErr.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, status)
		return
	}
	if len(parts) == 3 && parts[1] == "context" && parts[2] == "compile" && r.Method == http.MethodGet {
		maxTokens, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("max_tokens")), 10, 64)
		manifest, compileErr := s.Harness.CompileManifest(session.ID, maxTokens)
		if compileErr != nil {
			s.problem(w, r, http.StatusInternalServerError, "context_compile_failed", compileErr.Error(), nil)
			return
		}
		parts := make([]string, 0, len(manifest.Blocks))
		for _, block := range manifest.Blocks {
			parts = append(parts, block.Content)
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"session_id": session.ID, "context_version": session.ContextVersion, "compiled": strings.TrimSpace(strings.Join(parts, "")), "manifest": manifest, "manifest_digest": manifest.Digest})
		return
	}
	if len(parts) == 3 && parts[1] == "context" && parts[2] == "integrity" && r.Method == http.MethodGet {
		integrity, integrityErr := s.Harness.VerifyCompaction(session.ID)
		if integrityErr != nil {
			s.problem(w, r, http.StatusConflict, "context_integrity_failed", integrityErr.Error(), map[string]any{"integrity": integrity})
			return
		}
		s.writeJSON(w, http.StatusOK, integrity)
		return
	}
	if len(parts) == 2 && parts[1] == "compact" && r.Method == http.MethodPost {
		s.sessionCompact(w, r, session)
		return
	}
	if len(parts) == 2 && parts[1] == "recover" && r.Method == http.MethodGet {
		recovery, recoveryErr := s.Harness.Recover(session.ID, time.Now().UTC())
		if recoveryErr != nil {
			s.problem(w, r, http.StatusInternalServerError, "session_recovery_failed", recoveryErr.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, recovery)
		return
	}
	s.problem(w, r, http.StatusNotFound, "not_found", "route not found", nil)
}

func (s *Server) sessionTurns(w http.ResponseWriter, r *http.Request, session harness.Session) {
	if r.Method == http.MethodGet {
		after, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("after")), 10, 64)
		limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
		items, next, err := s.Harness.ListTurns(session.ID, after, limit)
		if err != nil {
			s.problem(w, r, http.StatusInternalServerError, "turn_list_failed", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"items": items, "next": next})
		return
	}
	if r.Method != http.MethodPost {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	var input struct {
		ID             string            `json:"id,omitempty"`
		AttemptID      string            `json:"attempt_id,omitempty"`
		Role           string            `json:"role"`
		Content        string            `json:"content"`
		ToolName       string            `json:"tool_name,omitempty"`
		ToolCallID     string            `json:"tool_call_id,omitempty"`
		ToolStatus     string            `json:"tool_status,omitempty"`
		IdempotencyKey string            `json:"idempotency_key,omitempty"`
		Metadata       map[string]string `json:"metadata,omitempty"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	turn, err := s.Harness.AppendTurn(session.ID, harness.Turn{ID: input.ID, AttemptID: input.AttemptID, Role: harness.Role(strings.ToLower(strings.TrimSpace(input.Role))), Content: input.Content, ToolName: input.ToolName, ToolCallID: input.ToolCallID, ToolStatus: input.ToolStatus, IdempotencyKey: input.IdempotencyKey, Metadata: input.Metadata})
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, harness.ErrConflict) {
			status = http.StatusConflict
		}
		s.problem(w, r, status, "turn_append_failed", err.Error(), nil)
		return
	}
	s.writeJSON(w, http.StatusCreated, turn)
}

func (s *Server) sessionCheckpoints(w http.ResponseWriter, r *http.Request, session harness.Session) {
	if r.Method == http.MethodGet {
		items, err := s.Harness.ListCheckpoints(session.ID)
		if err != nil {
			s.problem(w, r, http.StatusInternalServerError, "checkpoint_list_failed", err.Error(), nil)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}
	if r.Method != http.MethodPost {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	var checkpoint harness.Checkpoint
	if err := decodeJSON(r, &checkpoint); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	saved, err := s.Harness.SaveCheckpoint(session.ID, checkpoint)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, harness.ErrConflict) || errors.Is(err, harness.ErrCorrupt) {
			status = http.StatusConflict
		}
		s.problem(w, r, status, "checkpoint_save_failed", err.Error(), nil)
		return
	}
	s.writeJSON(w, http.StatusCreated, saved)
}

func evidenceMemoryScope(session harness.Session) evidencememory.Scope {
	return evidencememory.Scope{TenantID: session.TenantID, WorkspaceID: session.WorkspaceID, ProjectID: session.ProjectID, SessionID: session.ID}
}

func transitionEvidenceMemory(repo *evidencememory.Repository, scope evidencememory.Scope, id string, target evidencememory.Status, actor, reason string) (evidencememory.Item, error) {
	item, err := repo.Get(scope, id)
	if err != nil {
		return evidencememory.Item{}, err
	}
	if item.Status == target {
		return item, nil
	}
	if item.Status == evidencememory.Candidate && target == evidencememory.Confirmed {
		if item, err = repo.Transition(scope, id, evidencememory.Quarantined, actor, reason); err != nil {
			return evidencememory.Item{}, err
		}
	}
	return repo.Transition(scope, id, target, actor, reason)
}

func (s *Server) sessionMemory(w http.ResponseWriter, r *http.Request, session harness.Session) {
	if r.Method == http.MethodGet {
		items, err := s.Harness.ListMemories(session.ID)
		if err != nil {
			s.problem(w, r, http.StatusInternalServerError, "memory_list_failed", err.Error(), nil)
			return
		}
		response := map[string]any{"items": items}
		if s.Memory != nil {
			response["evidence_items"] = s.Memory.List(evidenceMemoryScope(session), "", time.Now().UTC())
		}
		s.writeJSON(w, http.StatusOK, response)
		return
	}
	if r.Method != http.MethodPost {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	var item harness.MemoryItem
	if err := decodeJSON(r, &item); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	item.SessionID = session.ID
	saved, err := s.Harness.AddMemory(item)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, harness.ErrConflict) || errors.Is(err, harness.ErrCorrupt) {
			status = http.StatusConflict
		}
		s.problem(w, r, status, "memory_save_failed", err.Error(), nil)
		return
	}
	response := map[string]any{}
	if encoded, encodeErr := json.Marshal(saved); encodeErr == nil {
		_ = json.Unmarshal(encoded, &response)
	}
	if s.Memory != nil {
		scope := evidenceMemoryScope(session)
		evidence, memoryErr := s.Memory.Add(evidencememory.AddInput{ID: saved.ID, Scope: scope, Kind: saved.Kind, Claim: saved.Content, Content: saved.Content, SourceIDs: saved.SourceIDs, Sensitivity: saved.Sensitivity, PollutionLineage: saved.PollutionLineage, ExpiresAt: saved.ExpiresAt})
		if memoryErr != nil && !errors.Is(memoryErr, evidencememory.ErrConflict) {
			s.problem(w, r, http.StatusConflict, "evidence_memory_save_failed", memoryErr.Error(), nil)
			return
		}
		if memoryErr == nil {
			actor := strings.TrimSpace(r.Header.Get("X-Member-ID"))
			if actor == "" {
				actor = "api"
			}
			switch saved.Status {
			case "confirmed":
				if evidence, memoryErr = s.Memory.Transition(scope, evidence.ID, evidencememory.Quarantined, actor, "compatibility review"); memoryErr == nil {
					evidence, memoryErr = s.Memory.Transition(scope, evidence.ID, evidencememory.Confirmed, actor, "harness confirmation")
				}
			case "quarantined":
				evidence, memoryErr = s.Memory.Transition(scope, evidence.ID, evidencememory.Quarantined, actor, "harness quarantine")
			case "rejected":
				evidence, memoryErr = s.Memory.Transition(scope, evidence.ID, evidencememory.Rejected, actor, "harness rejection")
			}
			if memoryErr == nil {
				response["evidence_item"] = evidence
			}
		}
	}
	s.writeJSON(w, http.StatusCreated, response)
}

func (s *Server) sessionMemoryReduce(w http.ResponseWriter, r *http.Request, session harness.Session) {
	var input struct {
		SourceIDs []string `json:"source_ids"`
		Content   string   `json:"content"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	reduction, err := s.Harness.ReduceMemories(session.ID, input.SourceIDs, input.Content)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, harness.ErrConflict) || errors.Is(err, harness.ErrCorrupt) {
			status = http.StatusConflict
		}
		s.problem(w, r, status, "memory_reduce_failed", err.Error(), nil)
		return
	}
	s.writeJSON(w, http.StatusCreated, reduction)
}

func (s *Server) sessionCompact(w http.ResponseWriter, r *http.Request, session harness.Session) {
	var request harness.CompactRequest
	if err := decodeJSON(r, &request); err != nil {
		s.problem(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	archive, err := s.Harness.Compact(session.ID, request)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, harness.ErrWindowUsed) || errors.Is(err, harness.ErrConflict) {
			status = http.StatusConflict
		}
		s.problem(w, r, status, "context_compaction_failed", err.Error(), nil)
		return
	}
	s.writeJSON(w, http.StatusCreated, archive)
}
