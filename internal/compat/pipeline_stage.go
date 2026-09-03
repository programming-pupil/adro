// Package compat contains the only bridge from ADRO's historical seven-stage
// pipeline to graph orchestration. New runtime code must depend on the graph
// contracts; this package is for migration, shadow comparison and rollback.
package compat

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/orchestration"
)

const LegacyAdapterVersion = "pipeline-stage-compat-v1"

// DispatchScope is the typed identity carried by every legacy provider call.
// It deliberately contains graph terms even though the public PipelineRun
// still exposes a numeric stage for backwards compatibility.
type DispatchScope struct {
	PlanID    string
	NodeID    string
	AttemptID string
}

// PipelineDispatchScope derives a stable scope from the immutable migrated
// plan and the idempotent turn key.  The hash-shaped attempt id prevents a
// retry from accidentally reusing a previous attempt while remaining stable
// when a provider response is replayed after a lost HTTP response.
func PipelineDispatchScope(run domain.PipelineRun, turnKey string) (DispatchScope, error) {
	if strings.TrimSpace(run.ID) == "" || !run.PipelineStage.Valid() {
		return DispatchScope{}, fmt.Errorf("legacy pipeline id and valid stage are required")
	}
	planID := "legacy-plan-" + run.ID
	nodeID := PipelineNodeID(run.PipelineStage)
	key := strings.TrimSpace(turnKey)
	if key == "" {
		return DispatchScope{}, fmt.Errorf("legacy pipeline turn key is required")
	}
	digest := sha256.Sum256([]byte(planID + "\x00" + nodeID + "\x00" + key))
	return DispatchScope{PlanID: planID, NodeID: nodeID, AttemptID: "legacy-attempt-" + hex.EncodeToString(digest[:])}, nil
}

// PipelineNodeID is the stable graph identity for a historical stage. It is
// exported so the API adapter can describe the reducer-selected next node
// without reintroducing numeric-stage parsing outside this compatibility
// package.
func PipelineNodeID(stage domain.PipelineStage) string {
	return "legacy-node-" + strconv.Itoa(int(stage))
}

// WorkItemDispatchScope and CommentDispatchScope keep the old API surfaces
// typed without pretending they are graph-native plans.  They are explicit
// compatibility plans whose envelope is still validated by the provider.
func WorkItemDispatchScope(workItemID, turnKey string) (DispatchScope, error) {
	return genericDispatchScope("legacy-work-item-"+strings.TrimSpace(workItemID), "work-item", turnKey)
}

func BugDispatchScope(bugID, turnKey string) (DispatchScope, error) {
	return genericDispatchScope("legacy-bug-"+strings.TrimSpace(bugID), "bug-repair", turnKey)
}

func CommentDispatchScope(commentID, targetID, turnKey string) (DispatchScope, error) {
	plan := "legacy-comment-" + strings.TrimSpace(commentID)
	node := "target-" + strings.TrimSpace(targetID)
	return genericDispatchScope(plan, node, turnKey)
}

func genericDispatchScope(planID, nodeID, turnKey string) (DispatchScope, error) {
	if strings.TrimSpace(planID) == "" || strings.TrimSpace(nodeID) == "" || strings.TrimSpace(turnKey) == "" {
		return DispatchScope{}, fmt.Errorf("legacy dispatch scope requires plan, node and turn key")
	}
	digest := sha256.Sum256([]byte(planID + "\x00" + nodeID + "\x00" + turnKey))
	return DispatchScope{PlanID: planID, NodeID: nodeID, AttemptID: "legacy-attempt-" + hex.EncodeToString(digest[:])}, nil
}

type MigrationResult struct {
	Plan           orchestration.RequirementExecutionPlan `json:"plan"`
	LegacyVersion  string                                 `json:"legacy_adapter_version"`
	StageNodeMap   map[string]string                      `json:"stage_node_map"`
	ShadowWarnings []string                               `json:"shadow_warnings,omitempty"`
}

// GraphFromPipeline converts the selected legacy workflow order into a graph.
// It preserves the configured step order and agent bindings but does not infer
// feedback edges or invent evidence; operators must add those explicitly.
func GraphFromPipeline(run domain.PipelineRun) (orchestration.WorkflowGraph, map[string]string, error) {
	steps := run.Steps()
	if len(steps) == 0 {
		return orchestration.WorkflowGraph{}, nil, fmt.Errorf("legacy pipeline has no workflow steps")
	}
	nodes := make([]orchestration.WorkflowNode, 0, len(steps))
	stageNode := make(map[string]string, len(steps))
	for _, step := range steps {
		if !step.Stage.Valid() || strings.TrimSpace(step.AgentID) == "" {
			return orchestration.WorkflowGraph{}, nil, fmt.Errorf("legacy workflow step %d requires a valid stage and agent", step.Stage)
		}
		id := "legacy-node-" + strconv.Itoa(int(step.Stage))
		if _, exists := stageNode[strconv.Itoa(int(step.Stage))]; exists {
			return orchestration.WorkflowGraph{}, nil, fmt.Errorf("legacy workflow stage %d is duplicated", step.Stage)
		}
		stageNode[strconv.Itoa(int(step.Stage))] = id
		nodes = append(nodes, orchestration.WorkflowNode{ID: id, Kind: orchestration.NodeAgent, AgentRef: &orchestration.VersionedRef{ID: step.AgentID, Revision: 1}, RetryPolicy: orchestration.RetryPolicy{MaxAttempts: step.RetryLimit}})
	}
	// The old engine has feedback edges (test -> development, arbitration ->
	// development), optional stages and same-stage retries. Encode the actual
	// next node in the result fields and let bounded predicates select exactly
	// one graph edge. Marking every compatibility node as an exit means a
	// suspended legacy transition with continue=false terminates fail-closed,
	// while a normal transition follows its explicit edge.
	loopLimit := run.MaxRetries + 1
	if loopLimit < 1 {
		loopLimit = 1
	}
	edges := make([]orchestration.WorkflowEdge, 0, len(nodes)*len(nodes)*3)
	for _, from := range nodes {
		for _, to := range nodes {
			predicate := orchestration.Predicate{Kind: "all", Children: []orchestration.Predicate{
				{Kind: "field_eq", Field: "continue", Value: true},
				{Kind: "field_eq", Field: "next_node_id", Value: to.ID},
			}}
			for _, event := range []orchestration.EdgeEvent{orchestration.EdgeSuccess, orchestration.EdgeFailure, orchestration.EdgeApproval} {
				edges = append(edges, orchestration.WorkflowEdge{
					ID:            fmt.Sprintf("legacy-edge-%s-%s-%s", from.ID, to.ID, event),
					From:          from.ID,
					To:            to.ID,
					On:            event,
					Predicate:     predicate,
					MaxTraversals: loopLimit,
				})
			}
		}
	}
	exits := make([]string, 0, len(nodes))
	for _, node := range nodes {
		exits = append(exits, node.ID)
	}
	graph := orchestration.WorkflowGraph{ID: "legacy-graph-" + run.ID, Version: run.Version, EntryNodeIDs: []string{nodes[0].ID}, ExitNodeIDs: exits, Nodes: nodes, Edges: edges}
	if graph.Version < 1 {
		graph.Version = 1
	}
	if err := orchestration.ValidateGraph(graph); err != nil {
		return orchestration.WorkflowGraph{}, nil, err
	}
	return graph, stageNode, nil
}

func MigratePipeline(run domain.PipelineRun) (MigrationResult, error) {
	graph, mapping, err := GraphFromPipeline(run)
	if err != nil {
		return MigrationResult{}, err
	}
	plan := orchestration.RequirementExecutionPlan{ID: "legacy-plan-" + run.ID, RequirementID: run.RequirementID, WorkspaceID: run.WorkspaceID, GraphSnapshot: graph, ContextRoot: orchestration.ContextRef{SessionID: run.SessionID, ManifestDigest: "legacy:" + run.SessionID}, Status: orchestration.PlanDraft}
	if run.Version > 0 {
		plan.Revision = run.Version
	}
	frozen, err := plan.Freeze()
	if err != nil {
		return MigrationResult{}, err
	}
	warnings := []string{"legacy pipeline feedback and evidence are not inferred; configure graph edges explicitly"}
	return MigrationResult{Plan: frozen, LegacyVersion: LegacyAdapterVersion, StageNodeMap: mapping, ShadowWarnings: warnings}, nil
}
