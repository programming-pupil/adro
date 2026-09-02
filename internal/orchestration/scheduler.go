package orchestration

import "sort"

// ReadyNodes computes a deterministic ready queue from the graph snapshot and
// projection. It is safe to call repeatedly after a crash because it derives
// state from attempts rather than maintaining a hidden queue.
func ReadyNodes(plan RequirementExecutionPlan, projection PlanProjection) []WorkflowNode {
	nodes := make(map[string]WorkflowNode, len(plan.GraphSnapshot.Nodes))
	for _, n := range plan.GraphSnapshot.Nodes {
		nodes[n.ID] = n
	}
	incoming := make(map[string][]WorkflowEdge)
	for _, e := range plan.GraphSnapshot.Edges {
		incoming[e.To] = append(incoming[e.To], e)
	}
	ready := make([]WorkflowNode, 0)
	for id, n := range nodes {
		state := projection.Nodes[id]
		if state.Status != AttemptReady {
			continue
		}
		edges := incoming[id]
		if len(edges) == 0 {
			ready = append(ready, n)
			continue
		}
		// A node is ready only after an incoming edge's source attempt has
		// committed. Pending/running sources cannot be inferred as success.
		passed := 0
		for _, e := range edges {
			source := projection.Nodes[e.From]
			if source.Status == AttemptPassed {
				fields := map[string]any{}
				if source.CurrentAttempt != "" {
					if attempt, ok := projection.Attempts[source.CurrentAttempt]; ok {
						fields = resultFields(attempt.Result)
					}
				}
				if ok, _ := EvaluatePredicate(e.Predicate, fields); ok {
					passed++
				}
			}
		}
		need := 1
		switch n.JoinPolicy {
		case JoinAll:
			need = len(edges)
		case JoinQuorum:
			need = len(edges)/2 + 1
		case JoinFirstSuccess:
			need = 1
		}
		if passed >= need {
			ready = append(ready, n)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].ID < ready[j].ID })
	return ready
}
