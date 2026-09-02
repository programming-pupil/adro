package orchestration

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// ValidateGraph returns stable paths suitable for API and CI diagnostics.
// Predicates are a bounded data AST; no executable expression is accepted.
func ValidateGraph(g WorkflowGraph) error {
	if len(g.Nodes) == 0 {
		return errors.New("graph.nodes.empty")
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
		nodes[n.ID] = n
	}
	if len(g.EntryNodeIDs) == 0 || len(g.ExitNodeIDs) == 0 {
		return errors.New("graph.entry_exit.required")
	}
	for i, id := range g.EntryNodeIDs {
		if _, ok := nodes[id]; !ok {
			return fmt.Errorf("graph.entry_node_ids[%d].unknown", i)
		}
	}
	for i, id := range g.ExitNodeIDs {
		if _, ok := nodes[id]; !ok {
			return fmt.Errorf("graph.exit_node_ids[%d].unknown", i)
		}
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
		if e.LoopGroup != "" && e.MaxTraversals < 1 {
			return fmt.Errorf("graph.edges[%d].loop_group.max_traversals.required", i)
		}
		if err := ValidatePredicate(e.Predicate, 0, fmt.Sprintf("graph.edges[%d].predicate", i)); err != nil {
			return err
		}
		edges[e.ID] = e
		edgeByFromTo[e.From+"\x00"+e.To] = append(edgeByFromTo[e.From+"\x00"+e.To], e)
		outgoing[e.From] = append(outgoing[e.From], e.To)
		incoming[e.To] = append(incoming[e.To], e.From)
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
		if p.Kind != "exists" && strings.TrimSpace(p.Field) == "" {
			return fmt.Errorf("%s.field.required", path)
		}
		if p.Kind == "number_cmp" && p.Op != "eq" && p.Op != "ne" && p.Op != "lt" && p.Op != "lte" && p.Op != "gt" && p.Op != "gte" {
			return fmt.Errorf("%s.op.invalid", path)
		}
	case "all", "any", "not":
		if len(p.Children) == 0 {
			return fmt.Errorf("%s.children.required", path)
		}
		if p.Kind == "not" && len(p.Children) != 1 {
			return fmt.Errorf("%s.children.one_required", path)
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
