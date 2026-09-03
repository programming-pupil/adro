package orchestration

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

const (
	maxWorkflowNodes = 1000
	maxWorkflowEdges = 5000
)

// ValidateGraph returns stable paths suitable for API and CI diagnostics.
// Predicates are a bounded data AST; no executable expression is accepted.
func ValidateGraph(g WorkflowGraph) error {
	if strings.TrimSpace(g.ID) == "" {
		return errors.New("graph.id.required")
	}
	if len(g.Nodes) == 0 {
		return errors.New("graph.nodes.empty")
	}
	if len(g.Nodes) > maxWorkflowNodes {
		return fmt.Errorf("graph.nodes.too_many: maximum is %d", maxWorkflowNodes)
	}
	if len(g.Edges) > maxWorkflowEdges {
		return fmt.Errorf("graph.edges.too_many: maximum is %d", maxWorkflowEdges)
	}
	if g.Version < 1 {
		return errors.New("graph.version.required")
	}
	nodes := map[string]WorkflowNode{}
	for i, n := range g.Nodes {
		if strings.TrimSpace(n.ID) == "" {
			return fmt.Errorf("graph.nodes[%d].id.required", i)
		}
		if _, ok := nodes[n.ID]; ok {
			return fmt.Errorf("graph.nodes[%d].id.duplicate", i)
		}
		if n.Kind == NodeAgent && n.AgentRef == nil {
			return fmt.Errorf("graph.nodes[%d].agent_ref.required", i)
		}
		if n.Kind == NodeSquad && n.SquadRef == nil {
			return fmt.Errorf("graph.nodes[%d].squad_ref.required", i)
		}
		switch n.Kind {
		case NodeAgent, NodeSquad, NodeGate, NodeHuman, NodeMerge, NodeRepair:
		default:
			return fmt.Errorf("graph.nodes[%d].kind.invalid", i)
		}
		if n.AgentRef != nil && strings.TrimSpace(n.AgentRef.ID) == "" {
			return fmt.Errorf("graph.nodes[%d].agent_ref.id.required", i)
		}
		if n.AgentRef != nil && n.AgentRef.Revision < 1 {
			return fmt.Errorf("graph.nodes[%d].agent_ref.revision.required", i)
		}
		if n.SquadRef != nil && strings.TrimSpace(n.SquadRef.ID) == "" {
			return fmt.Errorf("graph.nodes[%d].squad_ref.id.required", i)
		}
		if n.SquadRef != nil && n.SquadRef.Revision < 1 && n.SquadRef.Version < 1 {
			return fmt.Errorf("graph.nodes[%d].squad_ref.revision.required", i)
		}
		if n.Timeout < 0 || n.ContextPolicy.MaxTokens < 0 || n.Budget.Tokens < 0 || n.Budget.ToolCalls < 0 || n.Budget.CostCents < 0 || n.Budget.Concurrent < 0 || n.Budget.Duration < 0 || n.RetryPolicy.MaxAttempts < 0 || n.RetryPolicy.Backoff < 0 {
			return fmt.Errorf("graph.nodes[%d].budget_or_retry.invalid", i)
		}
		if n.JoinPolicy != "" && n.JoinPolicy != JoinAll && n.JoinPolicy != JoinQuorum && n.JoinPolicy != JoinFirstSuccess {
			return fmt.Errorf("graph.nodes[%d].join_policy.invalid", i)
		}
		if n.JoinQuorum < 0 {
			return fmt.Errorf("graph.nodes[%d].join_quorum.invalid", i)
		}
		if n.JoinFailurePolicy != "" && n.JoinFailurePolicy != "wait" && n.JoinFailurePolicy != "short_circuit" {
			return fmt.Errorf("graph.nodes[%d].join_failure_policy.invalid", i)
		}
		if err := ValidatePredicate(n.GatePolicy.Predicate, 0, fmt.Sprintf("graph.nodes[%d].gate_policy.predicate", i)); err != nil {
			return err
		}
		if n.Kind != NodeGate && (strings.TrimSpace(n.GatePolicy.Predicate.Kind) != "" || len(n.GatePolicy.RequiredEvidence) > 0 || strings.TrimSpace(n.GatePolicy.FailureCode) != "") {
			return fmt.Errorf("graph.nodes[%d].gate_policy.kind_mismatch", i)
		}
		for evidenceIndex, evidence := range n.GatePolicy.RequiredEvidence {
			if strings.TrimSpace(evidence) == "" {
				return fmt.Errorf("graph.nodes[%d].gate_policy.required_evidence[%d].required", i, evidenceIndex)
			}
		}
		if n.Kind != NodeMerge && (strings.TrimSpace(n.MergePolicy.ConflictPolicy) != "" || len(n.MergePolicy.KeyFields) > 0 || n.MergePolicy.RequireEvidence) {
			return fmt.Errorf("graph.nodes[%d].merge_policy.kind_mismatch", i)
		}
		if n.MergePolicy.ConflictPolicy != "" && n.MergePolicy.ConflictPolicy != "collect" && n.MergePolicy.ConflictPolicy != "prefer_priority" && n.MergePolicy.ConflictPolicy != "fail" {
			return fmt.Errorf("graph.nodes[%d].merge_policy.conflict_policy.invalid", i)
		}
		for keyIndex, key := range n.MergePolicy.KeyFields {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("graph.nodes[%d].merge_policy.key_fields[%d].required", i, keyIndex)
			}
		}
		if n.Kind != NodeRepair && (strings.TrimSpace(n.RepairPolicy.TargetNodeID) != "" || len(n.RepairPolicy.Scope) > 0 || len(n.RepairPolicy.VerificationNodeIDs) > 0 || n.RepairPolicy.MaxRounds != 0 || n.RepairPolicy.Budget != (Budget{})) {
			return fmt.Errorf("graph.nodes[%d].repair_policy.kind_mismatch", i)
		}
		if n.RepairPolicy.MaxRounds < 0 {
			return fmt.Errorf("graph.nodes[%d].repair_policy.max_rounds.invalid", i)
		}
		if err := validateBudget(n.RepairPolicy.Budget, fmt.Sprintf("graph.nodes[%d].repair_policy.budget", i)); err != nil {
			return err
		}
		for scopeIndex, scope := range n.RepairPolicy.Scope {
			if strings.TrimSpace(scope) == "" {
				return fmt.Errorf("graph.nodes[%d].repair_policy.scope[%d].required", i, scopeIndex)
			}
		}
		nodes[n.ID] = n
	}
	if len(g.EntryNodeIDs) == 0 || len(g.ExitNodeIDs) == 0 {
		return errors.New("graph.entry_exit.required")
	}
	entrySeen := map[string]struct{}{}
	for i, id := range g.EntryNodeIDs {
		if _, ok := nodes[id]; !ok {
			return fmt.Errorf("graph.entry_node_ids[%d].unknown", i)
		}
		if _, ok := entrySeen[id]; ok {
			return fmt.Errorf("graph.entry_node_ids[%d].duplicate", i)
		}
		entrySeen[id] = struct{}{}
	}
	exitSeen := map[string]struct{}{}
	for i, id := range g.ExitNodeIDs {
		if _, ok := nodes[id]; !ok {
			return fmt.Errorf("graph.exit_node_ids[%d].unknown", i)
		}
		if _, ok := exitSeen[id]; ok {
			return fmt.Errorf("graph.exit_node_ids[%d].duplicate", i)
		}
		exitSeen[id] = struct{}{}
	}
	edges := map[string]WorkflowEdge{}
	edgeByFromTo := map[string][]WorkflowEdge{}
	outgoing := map[string][]string{}
	incoming := map[string][]string{}
	for i, e := range g.Edges {
		if strings.TrimSpace(e.ID) == "" {
			return fmt.Errorf("graph.edges[%d].id.required", i)
		}
		if _, ok := edges[e.ID]; ok {
			return fmt.Errorf("graph.edges[%d].id.duplicate", i)
		}
		if _, ok := nodes[e.From]; !ok {
			return fmt.Errorf("graph.edges[%d].from.unknown", i)
		}
		if _, ok := nodes[e.To]; !ok {
			return fmt.Errorf("graph.edges[%d].to.unknown", i)
		}
		if e.From == e.To && e.MaxTraversals < 1 {
			return fmt.Errorf("graph.edges[%d].max_traversals.required", i)
		}
		switch e.On {
		case EdgeSuccess, EdgeFailure, EdgeTimeout, EdgeApproval, EdgeBug, EdgeCancel:
		default:
			return fmt.Errorf("graph.edges[%d].on.invalid", i)
		}
		if e.MaxTraversals < 0 {
			return fmt.Errorf("graph.edges[%d].max_traversals.invalid", i)
		}
		for evidenceIndex, evidence := range e.RequiredEvidence {
			if strings.TrimSpace(evidence) == "" {
				return fmt.Errorf("graph.edges[%d].required_evidence[%d].required", i, evidenceIndex)
			}
		}
		if e.LoopGroup != "" && e.MaxTraversals < 1 {
			return fmt.Errorf("graph.edges[%d].loop_group.max_traversals.required", i)
		}
		if e.FanOut && e.Priority < 0 {
			return fmt.Errorf("graph.edges[%d].fan_out.priority.invalid", i)
		}
		if err := ValidatePredicate(e.Predicate, 0, fmt.Sprintf("graph.edges[%d].predicate", i)); err != nil {
			return err
		}
		fromNode := nodes[e.From]
		toNode := nodes[e.To]
		if fromNode.OutputContract.ID != "" || toNode.InputContract.ID != "" {
			if fromNode.OutputContract.ID == "" || toNode.InputContract.ID == "" {
				return fmt.Errorf("graph.edges[%d].schema_contract.missing", i)
			}
			if fromNode.OutputContract.ID != toNode.InputContract.ID {
				return fmt.Errorf("graph.edges[%d].schema_contract.disconnected", i)
			}
			if fromNode.OutputContract.Version > 0 && toNode.InputContract.Version > 0 && fromNode.OutputContract.Version != toNode.InputContract.Version {
				return fmt.Errorf("graph.edges[%d].schema_contract.version_mismatch", i)
			}
		}
		edges[e.ID] = e
		edgeByFromTo[e.From+"\x00"+e.To] = append(edgeByFromTo[e.From+"\x00"+e.To], e)
		outgoing[e.From] = append(outgoing[e.From], e.To)
		incoming[e.To] = append(incoming[e.To], e.From)
	}
	for i, n := range g.Nodes {
		if n.JoinPolicy == JoinQuorum && n.JoinQuorum > 0 && n.JoinQuorum > len(incoming[n.ID]) {
			return fmt.Errorf("graph.nodes[%d].join_quorum.exceeds_incoming", i)
		}
		if n.RepairPolicy.TargetNodeID != "" {
			if _, ok := nodes[n.RepairPolicy.TargetNodeID]; !ok {
				return fmt.Errorf("graph.nodes[%d].repair_policy.target_node_id.unknown", i)
			}
		}
		for verificationIndex, id := range n.RepairPolicy.VerificationNodeIDs {
			if _, ok := nodes[id]; !ok {
				return fmt.Errorf("graph.nodes[%d].repair_policy.verification_node_ids[%d].unknown", i, verificationIndex)
			}
		}
	}
	// Reachability from entries and to exits prevents plans that can never run
	// or can never produce terminal evidence.
	reachable := map[string]bool{}
	q := append([]string(nil), g.EntryNodeIDs...)
	for len(q) > 0 {
		id := q[0]
		q = q[1:]
		if reachable[id] {
			continue
		}
		reachable[id] = true
		q = append(q, outgoing[id]...)
	}
	for id := range nodes {
		if !reachable[id] {
			return fmt.Errorf("graph.nodes.%s.unreachable", id)
		}
	}
	toExit := map[string]bool{}
	q = append(q, g.ExitNodeIDs...)
	for len(q) > 0 {
		id := q[0]
		q = q[1:]
		if toExit[id] {
			continue
		}
		toExit[id] = true
		q = append(q, incoming[id]...)
	}
	for id := range nodes {
		if !toExit[id] {
			return fmt.Errorf("graph.nodes.%s.no_exit_path", id)
		}
	}
	// Detect directed cycles and require an explicit traversal bound on the
	// back-edge. This catches multi-node loops as well as self-loops.
	color := map[string]uint8{}
	var visit func(string) error
	visit = func(id string) error {
		color[id] = 1
		for _, next := range outgoing[id] {
			if color[next] == 1 {
				for _, e := range edgeByFromTo[id+"\x00"+next] {
					if e.MaxTraversals < 1 {
						return fmt.Errorf("graph.edges.%s.max_traversals.required", e.ID)
					}
					if e.LoopGroup != "" && !hasHumanExit(nodes, outgoing, id) {
						return fmt.Errorf("graph.edges.%s.loop_group.human_exit.required", e.ID)
					}
				}
			}
			if color[next] == 0 {
				if err := visit(next); err != nil {
					return err
				}
			}
		}
		color[id] = 2
		return nil
	}
	for id := range nodes {
		if color[id] == 0 {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	return nil
}

func ValidatePredicate(p Predicate, depth int, path string) error {
	if depth > 8 {
		return fmt.Errorf("%s.depth_exceeded", path)
	}
	if strings.TrimSpace(p.Kind) == "" {
		return nil
	}
	switch p.Kind {
	case "field_eq", "number_cmp", "contains", "exists":
		if strings.TrimSpace(p.Field) == "" {
			return fmt.Errorf("%s.field.required", path)
		}
		if p.Kind == "contains" {
			if _, ok := p.Value.(string); !ok {
				return fmt.Errorf("%s.value.string_required", path)
			}
		}
		if p.Kind == "number_cmp" && p.Op != "eq" && p.Op != "ne" && p.Op != "lt" && p.Op != "lte" && p.Op != "gt" && p.Op != "gte" {
			return fmt.Errorf("%s.op.invalid", path)
		}
		if p.Kind == "number_cmp" {
			if _, ok := number(p.Value); !ok {
				return fmt.Errorf("%s.value.number_required", path)
			}
		}
	case "all", "any", "not":
		if len(p.Children) == 0 {
			return fmt.Errorf("%s.children.required", path)
		}
		if p.Kind == "not" && len(p.Children) != 1 {
			return fmt.Errorf("%s.children.one_required", path)
		}
		if len(p.Children) > 32 {
			return fmt.Errorf("%s.children.too_many", path)
		}
		for i, c := range p.Children {
			if err := ValidatePredicate(c, depth+1, fmt.Sprintf("%s.children[%d]", path, i)); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%s.kind.invalid", path)
	}
	return nil
}

func hasHumanExit(nodes map[string]WorkflowNode, outgoing map[string][]string, start string) bool {
	seen := map[string]struct{}{}
	queue := []string{start}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if nodes[id].Kind == NodeHuman {
			return true
		}
		queue = append(queue, outgoing[id]...)
	}
	return false
}

// EvaluatePredicate evaluates only the bounded predicate vocabulary against a
// JSON-like field map. Unknown fields are false (except exists), never errors.
func EvaluatePredicate(p Predicate, fields map[string]any) (bool, error) {
	if err := ValidatePredicate(p, 0, "predicate"); err != nil {
		return false, err
	}
	if strings.TrimSpace(p.Kind) == "" {
		return true, nil
	}
	return evalPredicate(p, fields), nil
}
func evalPredicate(p Predicate, f map[string]any) bool {
	switch p.Kind {
	case "all":
		for _, c := range p.Children {
			if !evalPredicate(c, f) {
				return false
			}
		}
		return true
	case "any":
		for _, c := range p.Children {
			if evalPredicate(c, f) {
				return true
			}
		}
		return false
	case "not":
		return !evalPredicate(p.Children[0], f)
	case "exists":
		_, ok := f[p.Field]
		return ok
	}
	v, ok := f[p.Field]
	if !ok {
		return false
	}
	if p.Kind == "contains" {
		sv, ok := v.(string)
		if !ok {
			return false
		}
		needle, ok := p.Value.(string)
		return ok && strings.Contains(sv, needle)
	}
	if p.Kind == "field_eq" {
		return reflect.DeepEqual(v, p.Value)
	}
	if p.Kind == "number_cmp" {
		a, aok := number(v)
		b, bok := number(p.Value)
		if !aok || !bok {
			return false
		}
		switch p.Op {
		case "eq":
			return a == b
		case "ne":
			return a != b
		case "lt":
			return a < b
		case "lte":
			return a <= b
		case "gt":
			return a > b
		case "gte":
			return a >= b
		}
	}
	return false
}
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	default:
		return 0, false
	}
}
