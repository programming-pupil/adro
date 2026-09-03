package orchestration

import "time"

// GraphDiagnostics is the bounded, side-effect-free summary shown by the
// control plane before a graph is published or executed. It deliberately
// contains counts and identifiers rather than inferred execution promises.
type GraphDiagnostics struct {
	NodeCount             int           `json:"node_count"`
	EdgeCount             int           `json:"edge_count"`
	AgentNodeCount        int           `json:"agent_node_count"`
	SquadNodeCount        int           `json:"squad_node_count"`
	StructuralNodeCount   int           `json:"structural_node_count"`
	HumanNodeCount        int           `json:"human_node_count"`
	JoinNodeIDs           []string      `json:"join_node_ids,omitempty"`
	LoopEdgeIDs           []string      `json:"loop_edge_ids,omitempty"`
	RetryNodeIDs          []string      `json:"retry_node_ids,omitempty"`
	RequiredEvidenceEdges int           `json:"required_evidence_edges"`
	MaxConcurrency        int           `json:"max_concurrency"`
	TokenBudget           int64         `json:"token_budget"`
	ToolCallBudget        int           `json:"tool_call_budget"`
	CostBudgetCents       int64         `json:"cost_budget_cents"`
	DurationBudget        time.Duration `json:"duration_budget"`
	RequiresHuman         bool          `json:"requires_human"`
}

// DiagnoseGraph only summarizes declarative graph configuration. Callers must
// run ValidateGraph separately; this function is also useful for drafts that
// are intentionally incomplete and need a preview of what is configured.
func DiagnoseGraph(g WorkflowGraph) GraphDiagnostics {
	d := GraphDiagnostics{NodeCount: len(g.Nodes), EdgeCount: len(g.Edges), JoinNodeIDs: []string{}, LoopEdgeIDs: []string{}, RetryNodeIDs: []string{}}
	for _, node := range g.Nodes {
		switch node.Kind {
		case NodeAgent:
			d.AgentNodeCount++
		case NodeSquad:
			d.SquadNodeCount++
		case NodeHuman:
			d.HumanNodeCount++
			d.RequiresHuman = true
		default:
			d.StructuralNodeCount++
		}
		if node.JoinPolicy != "" {
			d.JoinNodeIDs = append(d.JoinNodeIDs, node.ID)
		}
		if node.RetryPolicy.MaxAttempts > 1 {
			d.RetryNodeIDs = append(d.RetryNodeIDs, node.ID)
		}
		if node.Budget.Concurrent > d.MaxConcurrency {
			d.MaxConcurrency = node.Budget.Concurrent
		}
		d.TokenBudget += node.Budget.Tokens
		d.ToolCallBudget += node.Budget.ToolCalls
		d.CostBudgetCents += node.Budget.CostCents
		d.DurationBudget += node.Budget.Duration
	}
	for _, edge := range g.Edges {
		if edge.LoopGroup != "" || edge.MaxTraversals > 0 {
			d.LoopEdgeIDs = append(d.LoopEdgeIDs, edge.ID)
		}
		if len(edge.RequiredEvidence) > 0 {
			d.RequiredEvidenceEdges++
		}
	}
	return d
}
