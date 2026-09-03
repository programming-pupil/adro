package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/adro-project/adro/internal/harness"
)

const (
	GateReasonPassed             = "gate_predicate_passed"
	GateReasonPredicateFailed    = "gate_predicate_failed"
	GateReasonEvidenceMissing    = "gate_evidence_missing"
	MergeReasonCollected         = "merge_collected"
	MergeReasonPreferredPriority = "merge_preferred_priority"
	MergeReasonConflict          = "merge_conflict"
	MergeReasonEvidenceMissing   = "merge_evidence_missing"
	RepairReasonPlanned          = "repair_planned"
	RepairReasonBudgetExhausted  = "repair_budget_exhausted"
	RepairReasonEvidenceMissing  = "repair_evidence_missing"
)

type StructuralInput struct {
	Plan       RequirementExecutionPlan
	Projection PlanProjection
	Node       WorkflowNode
	Attempt    NodeAttempt
	Envelope   harness.ContextEnvelope
	Incoming   []StructuralSource
}

type StructuralSource struct {
	Edge    WorkflowEdge `json:"edge"`
	Attempt NodeAttempt  `json:"attempt"`
}

type StructuralDecision struct {
	Event       string           `json:"event"`
	Result      StructuredResult `json:"result"`
	Failure     *FailureReason   `json:"failure,omitempty"`
	ArtifactIDs []string         `json:"artifact_ids,omitempty"`
}

type GateEvaluator interface {
	EvaluateGate(context.Context, StructuralInput) (StructuralDecision, error)
}

type MergeReducer interface {
	ReduceMerge(context.Context, StructuralInput) (StructuralDecision, error)
}

type RepairController interface {
	PlanRepair(context.Context, StructuralInput) (StructuralDecision, error)
}

type DefaultGateEvaluator struct{}
type DefaultMergeReducer struct{}
type DefaultRepairController struct{}

func (DefaultGateEvaluator) EvaluateGate(_ context.Context, in StructuralInput) (StructuralDecision, error) {
	fields := aggregateStructuralFields(in.Incoming)
	matched, err := EvaluatePredicate(in.Node.GatePolicy.Predicate, fields)
	if err != nil {
		return StructuralDecision{}, err
	}
	evidence, missing := structuralEvidence(in.Incoming, in.Node.GatePolicy.RequiredEvidence)
	if len(missing) > 0 {
		return failedStructuralDecision("gate", GateReasonEvidenceMissing, "gate is missing required evidence", evidence, map[string]any{"missing_evidence": missing}), nil
	}
	if !matched {
		code := strings.TrimSpace(in.Node.GatePolicy.FailureCode)
		if code == "" {
			code = GateReasonPredicateFailed
		}
		return failedStructuralDecision("gate", code, "gate predicate did not match", evidence, fields), nil
	}
	return passedStructuralDecision("gate", GateReasonPassed, "gate predicate matched", evidence, fields), nil
}

func (DefaultMergeReducer) ReduceMerge(_ context.Context, in StructuralInput) (StructuralDecision, error) {
	evidence, _ := structuralEvidence(in.Incoming, nil)
	if in.Node.MergePolicy.RequireEvidence && len(evidence) == 0 {
		return failedStructuralDecision("merge", MergeReasonEvidenceMissing, "merge has no committed branch evidence", evidence, nil), nil
	}
	policy := strings.TrimSpace(in.Node.MergePolicy.ConflictPolicy)
	if policy == "" {
		policy = "collect"
	}
	branches := make([]map[string]any, 0, len(in.Incoming))
	for _, source := range in.Incoming {
		branches = append(branches, map[string]any{
			"edge_id":      source.Edge.ID,
			"node_id":      source.Attempt.NodeID,
			"attempt_id":   source.Attempt.ID,
			"priority":     source.Edge.Priority,
			"outcome":      source.Attempt.Result.Outcome,
			"reason_code":  source.Attempt.Result.ReasonCode,
			"fields":       source.Attempt.Result.Fields,
			"evidence_ids": source.Attempt.Result.EvidenceIDs,
		})
	}
	conflicts := mergeConflicts(in.Incoming, in.Node.MergePolicy.KeyFields)
	fields := map[string]any{"branches": branches, "conflicts": conflicts, "conflict_policy": policy}
	switch policy {
	case "collect":
		return passedStructuralDecision("merge", MergeReasonCollected, "merge collected branch results", evidence, fields), nil
	case "prefer_priority":
		if len(in.Incoming) == 0 {
			return failedStructuralDecision("merge", MergeReasonEvidenceMissing, "merge has no completed branches", evidence, fields), nil
		}
		selected := append([]StructuralSource(nil), in.Incoming...)
		sort.SliceStable(selected, func(i, j int) bool {
			if selected[i].Edge.Priority == selected[j].Edge.Priority {
				return selected[i].Attempt.ID < selected[j].Attempt.ID
			}
			return selected[i].Edge.Priority > selected[j].Edge.Priority
		})
		fields["selected_attempt_id"] = selected[0].Attempt.ID
		fields["selected_fields"] = selected[0].Attempt.Result.Fields
		return passedStructuralDecision("merge", MergeReasonPreferredPriority, "merge selected the highest priority branch", evidence, fields), nil
	case "fail":
		if len(conflicts) > 0 {
			return failedStructuralDecision("merge", MergeReasonConflict, "merge conflict requires a repair or human route", evidence, fields), nil
		}
		return passedStructuralDecision("merge", MergeReasonCollected, "merge completed without conflicts", evidence, fields), nil
	default:
		return StructuralDecision{}, fmt.Errorf("unsupported merge conflict policy %q", policy)
	}
}

func (DefaultRepairController) PlanRepair(_ context.Context, in StructuralInput) (StructuralDecision, error) {
	evidence, _ := structuralEvidence(in.Incoming, nil)
	if len(evidence) == 0 {
		return failedStructuralDecision("repair", RepairReasonEvidenceMissing, "repair requires failed evidence", evidence, nil), nil
	}
	maxRounds := in.Node.RepairPolicy.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 1
	}
	if in.Attempt.AttemptNo > maxRounds {
		return failedStructuralDecision("repair", RepairReasonBudgetExhausted, "repair round budget exhausted", evidence, map[string]any{"max_rounds": maxRounds, "attempt_no": in.Attempt.AttemptNo}), nil
	}
	target := strings.TrimSpace(in.Node.RepairPolicy.TargetNodeID)
	if target == "" {
		for _, edge := range in.Plan.GraphSnapshot.Edges {
			if edge.From == in.Node.ID && edge.On == EdgeSuccess {
				target = edge.To
				break
			}
		}
	}
	fields := map[string]any{
		"target_node_id":        target,
		"scope":                 append([]string(nil), in.Node.RepairPolicy.Scope...),
		"verification_node_ids": append([]string(nil), in.Node.RepairPolicy.VerificationNodeIDs...),
		"round":                 in.Attempt.AttemptNo,
		"max_rounds":            maxRounds,
		"budget":                in.Node.RepairPolicy.Budget,
		"source_attempt_ids":    sourceAttemptIDs(in.Incoming),
	}
	return passedStructuralDecision("repair", RepairReasonPlanned, "repair plan created", evidence, fields), nil
}

func incomingStructuralSources(plan RequirementExecutionPlan, projection PlanProjection, nodeID string) []StructuralSource {
	items := make([]StructuralSource, 0)
	for _, edge := range plan.GraphSnapshot.Edges {
		if edge.To != nodeID {
			continue
		}
		node, ok := projection.Nodes[edge.From]
		if !ok || node.CurrentAttempt == "" {
			continue
		}
		attempt, ok := projection.Attempts[node.CurrentAttempt]
		if !ok || !edgeSatisfied(edge, attempt) {
			continue
		}
		items = append(items, StructuralSource{Edge: edge, Attempt: attempt})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Edge.Priority == items[j].Edge.Priority {
			return items[i].Edge.ID < items[j].Edge.ID
		}
		return items[i].Edge.Priority > items[j].Edge.Priority
	})
	return items
}

func structuralEvidence(incoming []StructuralSource, required []string) ([]string, []string) {
	seen := map[string]struct{}{}
	evidence := make([]string, 0)
	for _, source := range incoming {
		for _, id := range source.Attempt.Result.EvidenceIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			evidence = append(evidence, id)
		}
	}
	missing := make([]string, 0)
	for _, id := range required {
		if _, ok := seen[id]; !ok {
			missing = append(missing, id)
		}
	}
	return evidence, missing
}

func aggregateStructuralFields(incoming []StructuralSource) map[string]any {
	fields := map[string]any{}
	branches := make([]map[string]any, 0, len(incoming))
	for _, source := range incoming {
		for key, value := range source.Attempt.Result.Fields {
			fields[key] = value
		}
		branches = append(branches, map[string]any{
			"node_id":     source.Attempt.NodeID,
			"attempt_id":  source.Attempt.ID,
			"outcome":     source.Attempt.Result.Outcome,
			"reason_code": source.Attempt.Result.ReasonCode,
			"fields":      source.Attempt.Result.Fields,
		})
	}
	fields["branches"] = branches
	fields["branch_count"] = len(branches)
	return fields
}

func mergeConflicts(incoming []StructuralSource, keys []string) map[string][]any {
	conflicts := map[string][]any{}
	if len(keys) == 0 {
		keySet := map[string]struct{}{}
		for _, source := range incoming {
			for key := range source.Attempt.Result.Fields {
				keySet[key] = struct{}{}
			}
		}
		for key := range keySet {
			keys = append(keys, key)
		}
		sort.Strings(keys)
	}
	for _, key := range keys {
		values := make([]any, 0)
		seen := map[string]struct{}{}
		for _, source := range incoming {
			value, ok := source.Attempt.Result.Fields[key]
			if !ok {
				continue
			}
			data, _ := json.Marshal(value)
			digest := string(data)
			if _, ok := seen[digest]; ok {
				continue
			}
			seen[digest] = struct{}{}
			values = append(values, value)
		}
		if len(values) > 1 {
			conflicts[key] = values
		}
	}
	return conflicts
}

func sourceAttemptIDs(incoming []StructuralSource) []string {
	ids := make([]string, 0, len(incoming))
	for _, source := range incoming {
		ids = append(ids, source.Attempt.ID)
	}
	return ids
}

func passedStructuralDecision(kind, code, summary string, evidence []string, fields map[string]any) StructuralDecision {
	if len(evidence) == 0 {
		evidence = []string{structuralDecisionEvidence(kind, code, fields)}
	}
	artifact := structuralDecisionEvidence(kind, code, fields)
	evidence = appendUniqueEvidence(evidence, artifact)
	return StructuralDecision{Event: "success", Result: StructuredResult{Outcome: "pass", ReasonCode: code, Summary: summary, Fields: fields, EvidenceIDs: evidence}, ArtifactIDs: []string{artifact}}
}

func failedStructuralDecision(kind, code, summary string, evidence []string, fields map[string]any) StructuralDecision {
	artifact := structuralDecisionEvidence(kind, code, fields)
	evidence = appendUniqueEvidence(evidence, artifact)
	return StructuralDecision{Event: "failure", Result: StructuredResult{Outcome: "failure", ReasonCode: code, Summary: summary, Fields: fields, EvidenceIDs: evidence}, Failure: &FailureReason{Code: code, Message: summary}, ArtifactIDs: []string{artifact}}
}

func appendUniqueEvidence(evidence []string, id string) []string {
	for _, existing := range evidence {
		if existing == id {
			return evidence
		}
	}
	return append(evidence, id)
}

func structuralDecisionEvidence(kind, code string, fields map[string]any) string {
	data, _ := json.Marshal(struct {
		Kind   string         `json:"kind"`
		Code   string         `json:"code"`
		Fields map[string]any `json:"fields,omitempty"`
	}{Kind: kind, Code: code, Fields: fields})
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%s-decision:%s", kind, hex.EncodeToString(digest[:]))
}
