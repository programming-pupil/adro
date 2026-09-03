package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/adro-project/adro/internal/orchestration"
)

// planTimeline returns the immutable event chain together with the current
// projection. Consumers can rebuild the exact state from plan hash + events.
func (s *Server) planTimeline(w http.ResponseWriter, r *http.Request, planID string) {
	if r.Method != http.MethodGet || s.Orchestration == nil {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "GET is required", nil)
		return
	}
	workspace := requestWorkspace(r, "")
	plan, err := s.Orchestration.GetPlan(workspace, planID)
	if err != nil && workspace == "" { // machine callers may omit a scope header
		for _, candidate := range s.Orchestration.ListPlans("") {
			if candidate.ID == planID {
				plan, err = candidate, nil
				break
			}
		}
	}
	if err != nil {
		s.problem(w, r, http.StatusNotFound, "plan_not_found", err.Error(), nil)
		return
	}
	events := s.Orchestration.ListEvents(planID, int64(queryInt(r, "after", 0)))
	projection, projectionErr := s.Orchestration.GetProjection(planID)
	if projectionErr != nil && !errors.Is(projectionErr, orchestration.ErrNotFound) {
		s.problem(w, r, http.StatusInternalServerError, "projection_unavailable", projectionErr.Error(), nil)
		return
	}
	response := map[string]any{"plan": plan, "events": redactOrchestrationEvents(events), "plan_hash": plan.PlanHash}
	if projectionErr == nil {
		response["projection"] = projection
		response["timeline"] = planTimelineItems(plan, projection, events)
	}
	s.writeJSON(w, http.StatusOK, response)
}

// planReplay rebuilds a projection directly from a frozen plan and its event
// chain. This is useful before the first provider attempt exists (for example
// immediately after a plan is created from the web control plane); the
// run-scoped replay route remains available once an attempt/run ID exists.
func (s *Server) planReplay(w http.ResponseWriter, r *http.Request, planID, workspace string) {
	if r.Method != http.MethodGet || s.Orchestration == nil {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "GET is required", nil)
		return
	}
	plan, err := s.getOrchestrationPlan(workspace, planID)
	if err != nil {
		s.problem(w, r, http.StatusNotFound, "plan_not_found", err.Error(), nil)
		return
	}
	events := s.Orchestration.ListEvents(plan.ID, 0)
	projection, err := orchestration.ReplayProjection(plan, events)
	if err != nil {
		s.problem(w, r, http.StatusConflict, "replay_failed", err.Error(), map[string]any{"plan_id": plan.ID})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"plan_id": plan.ID, "plan": plan, "projection": projection, "events": redactOrchestrationEvents(events), "cursor": lastSequence(events)})
}

func (s *Server) runReplay(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodGet || s.Orchestration == nil {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "GET is required", nil)
		return
	}
	workspace := requestWorkspace(r, "")
	var plan orchestration.RequirementExecutionPlan
	var found bool
	for _, candidate := range s.Orchestration.ListPlans(workspace) {
		projection, projectionErr := s.Orchestration.GetProjection(candidate.ID)
		if projectionErr != nil {
			continue
		}
		for _, attempt := range projection.Attempts {
			if attempt.ID == runID || attempt.RunID == runID {
				plan, found = candidate, true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		s.problem(w, r, http.StatusNotFound, "run_not_found", "run is not present in orchestration event history", nil)
		return
	}
	events := s.Orchestration.ListEvents(plan.ID, 0)
	projection, err := orchestration.ReplayProjection(plan, events)
	if err != nil {
		s.problem(w, r, http.StatusConflict, "replay_failed", err.Error(), map[string]any{"plan_id": plan.ID})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"run_id": runID, "plan": plan, "projection": projection, "events": redactOrchestrationEvents(events), "cursor": lastSequence(events)})
}

func (s *Server) runDiagnostics(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodGet || s.Orchestration == nil {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "GET is required", nil)
		return
	}
	workspace := requestWorkspace(r, "")
	for _, plan := range s.Orchestration.ListPlans(workspace) {
		events := s.Orchestration.ListEvents(plan.ID, 0)
		projection, projectionErr := s.Orchestration.GetProjection(plan.ID)
		if projectionErr != nil {
			continue
		}
		matched := false
		for _, attempt := range projection.Attempts {
			if attempt.ID == runID || attempt.RunID == runID {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		s.writeJSON(w, http.StatusOK, orchestrationDiagnostics(runID, plan, projection, events, s.Orchestration.ListOutbox(plan.ID, "")))
		return
	}
	s.problem(w, r, http.StatusNotFound, "run_not_found", "run is not present in orchestration event history", nil)
}

func planTimelineItems(plan orchestration.RequirementExecutionPlan, projection orchestration.PlanProjection, events []orchestration.Event) []map[string]any {
	items := make([]map[string]any, 0, len(events)+len(projection.Attempts)+len(projection.Decisions))
	for _, event := range events {
		items = append(items, map[string]any{"sequence": event.Sequence, "kind": "event", "event_type": event.Type, "event_id": event.ID, "attempt_id": event.AttemptID, "node_id": event.NodeID, "reason_code": eventReasonCode(event)})
	}
	for _, attempt := range projection.Attempts {
		item := map[string]any{"sequence": int64(0), "kind": "attempt", "attempt_id": attempt.ID, "node_id": attempt.NodeID, "status": attempt.Status, "run_id": attempt.RunID, "session_id": attempt.SessionID, "workdir": attempt.WorkDir}
		if attempt.FinishedAt != nil {
			item["finished_at"] = attempt.FinishedAt
		}
		if attempt.FailureReason != nil {
			item["reason_code"] = attempt.FailureReason.Code
			item["reason"] = attempt.FailureReason.Message
		}
		items = append(items, item)
	}
	for _, decision := range projection.Decisions {
		items = append(items, map[string]any{"sequence": int64(0), "kind": "edge", "decision_id": decision.ID, "source_attempt": decision.SourceAttempt, "source_node": decision.SourceNode, "target_node": decision.TargetNode, "edge_id": decision.EdgeID, "reason": decision.Reason, "loop_count": decision.LoopCount})
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, _ := items[i]["sequence"].(int64)
		right, _ := items[j]["sequence"].(int64)
		if left != right {
			if left == 0 {
				return false
			}
			if right == 0 {
				return true
			}
			return left < right
		}
		return fmt.Sprint(items[i]["kind"], items[i]["attempt_id"], items[i]["event_id"]) < fmt.Sprint(items[j]["kind"], items[j]["attempt_id"], items[j]["event_id"])
	})
	_ = plan
	return items
}

func eventReasonCode(event orchestration.Event) string {
	var payload map[string]any
	if json.Unmarshal(event.Payload, &payload) == nil {
		if value, ok := payload["reason_code"].(string); ok {
			return value
		}
		if value, ok := payload["failure"].(map[string]any); ok {
			if code, ok := value["code"].(string); ok {
				return code
			}
		}
	}
	return ""
}

func orchestrationDiagnostics(runID string, plan orchestration.RequirementExecutionPlan, projection orchestration.PlanProjection, events []orchestration.Event, outbox []orchestration.OutboxRecord) map[string]any {
	leases := make([]map[string]any, 0)
	failures := make([]map[string]any, 0)
	for _, attempt := range projection.Attempts {
		if attempt.Lease.Owner != "" {
			leases = append(leases, map[string]any{"attempt_id": attempt.ID, "node_id": attempt.NodeID, "owner": attempt.Lease.Owner, "fencing_token": attempt.Lease.FencingToken, "expires_at": attempt.Lease.ExpiresAt})
		}
		if attempt.FailureReason != nil {
			failures = append(failures, map[string]any{"attempt_id": attempt.ID, "node_id": attempt.NodeID, "status": attempt.Status, "reason_code": attempt.FailureReason.Code, "message": attempt.FailureReason.Message, "retryable": attempt.FailureReason.Retryable})
		}
	}
	return map[string]any{
		"run_id": runID, "plan_id": plan.ID, "workspace_id": plan.WorkspaceID, "status": plan.Status,
		"terminal_outcome": projection.TerminalOutcome, "plan_hash": plan.PlanHash, "projection": projection,
		"events": redactOrchestrationEvents(events), "outbox": outbox,
		"capabilities": map[string]any{"selected_ref": plan.SelectedRef, "provider_contract": "typed-context-envelope"},
		"budget":       map[string]any{"policy": plan.PolicySnapshot.Budget, "tokens_used": projection.TokenUsage, "tool_calls": projection.ToolCalls, "cost_cents": projection.CostCents},
		"leases":       leases, "failures": failures, "tool_calls": projection.ToolCalls,
		"context":           map[string]any{"session_id": plan.ContextRoot.SessionID, "manifest_digest": plan.ContextRoot.ManifestDigest, "replay_key": plan.ContextRoot.ReplayKey},
		"artifact_evidence": evidenceIDs(projection), "cursor": lastSequence(events),
	}
}

func evidenceIDs(projection orchestration.PlanProjection) []string {
	seen := map[string]struct{}{}
	items := make([]string, 0)
	for _, attempt := range projection.Attempts {
		for _, id := range attempt.Result.EvidenceIDs {
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			items = append(items, id)
		}
	}
	sort.Strings(items)
	return items
}

// redactOrchestrationEvents keeps replay/diagnostic metadata while stripping
// prompt, input, secret and context-block content from provider-facing event
// payloads. Hashes and sequence remain available for audit without exposing
// private context through a read-only endpoint.
func redactOrchestrationEvents(events []orchestration.Event) []map[string]any {
	result := make([]map[string]any, 0, len(events))
	for _, event := range events {
		var item any
		if err := json.Unmarshal(event.Payload, &item); err != nil {
			item = map[string]any{"redacted": true}
		} else {
			item = redactOrchestrationValue(item, "")
		}
		data := map[string]any{
			"event_id": event.ID, "plan_id": event.PlanID, "workspace_id": event.WorkspaceID,
			"run_id": event.RunID, "node_id": event.NodeID, "attempt_id": event.AttemptID,
			"sequence": event.Sequence, "event_type": event.Type, "payload_hash": event.PayloadHash,
			"previous_hash": event.PreviousHash, "envelope_hash": event.EnvelopeHash,
			"idempotency_key": event.IdempotencyKey, "fencing_token": event.FencingToken,
			"traceparent": event.TraceParent, "tracestate": event.TraceState,
			"created_at": event.CreatedAt, "payload": item, "payload_redacted": true,
		}
		result = append(result, data)
	}
	return result
}

func redactOrchestrationValue(value any, key string) any {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "prompt" || lower == "input" || lower == "content" || lower == "secret" || strings.Contains(lower, "secret") || strings.Contains(lower, "token_value") {
		return "[redacted]"
	}
	switch typed := value.(type) {
	case map[string]any:
		copy := make(map[string]any, len(typed))
		for childKey, childValue := range typed {
			copy[childKey] = redactOrchestrationValue(childValue, childKey)
		}
		return copy
	case []any:
		copy := make([]any, len(typed))
		for i, child := range typed {
			copy[i] = redactOrchestrationValue(child, key)
		}
		return copy
	default:
		return value
	}
}

func lastSequence(events []orchestration.Event) int64 {
	if len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Sequence
}
