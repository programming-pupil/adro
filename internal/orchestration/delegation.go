package orchestration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ExecutionPlan is the neutral Runtime name. RequirementExecutionPlan remains
// as a wire-compatible migration alias while old application adapters are
// being moved out of the core orchestration path.
type ExecutionPlan = RequirementExecutionPlan

var (
	ErrDelegationInvalid    = errors.New("invalid delegation request")
	ErrDelegationEscalation = errors.New("delegation would expand parent authority")
)

// DelegationRequest is an explicit child-session request. Context selection is
// carried as identifiers, never as an implicit copy of the parent's prompt.
type DelegationRequest struct {
	RequestID       string          `json:"request_id"`
	ParentPlanID    string          `json:"parent_plan_id"`
	ParentAttemptID string          `json:"parent_attempt_id"`
	ChildPlanID     string          `json:"child_plan_id"`
	ChildAgentID    string          `json:"child_agent_id"`
	TenantID        string          `json:"tenant_id"`
	WorkspaceID     string          `json:"workspace_id"`
	Capabilities    []CapabilityRef `json:"capabilities,omitempty"`
	ContextBlockIDs []string        `json:"context_block_ids,omitempty"`
	Budget          Budget          `json:"budget"`
	Deadline        time.Time       `json:"deadline"`
	RecursionDepth  int             `json:"recursion_depth"`
	MaxChildDepth   int             `json:"max_child_depth"`
	IdempotencyKey  string          `json:"idempotency_key"`
}

// DelegationGrant is frozen into the child plan and becomes the only authority
// the child may use. Its digest is an immutable audit identity.
type DelegationGrant struct {
	RequestID       string          `json:"request_id"`
	ParentPlanID    string          `json:"parent_plan_id"`
	ParentAttemptID string          `json:"parent_attempt_id"`
	ChildPlanID     string          `json:"child_plan_id"`
	ChildAgentID    string          `json:"child_agent_id"`
	TenantID        string          `json:"tenant_id"`
	WorkspaceID     string          `json:"workspace_id"`
	Capabilities    []CapabilityRef `json:"capabilities,omitempty"`
	ContextBlockIDs []string        `json:"context_block_ids,omitempty"`
	Budget          Budget          `json:"budget"`
	Deadline        time.Time       `json:"deadline"`
	RecursionDepth  int             `json:"recursion_depth"`
	MaxChildDepth   int             `json:"max_child_depth"`
	Digest          string          `json:"digest"`
}

// FreezeDelegation proves that every child grant is a subset of its parent.
// Zero parent limits mean "no declared limit" only for that dimension; a
// child cannot turn an explicit parent limit into an unlimited one.
func FreezeDelegation(parent DelegationGrant, request DelegationRequest, now time.Time) (DelegationGrant, error) {
	if err := parent.Validate(); err != nil {
		return DelegationGrant{}, fmt.Errorf("parent grant: %w", err)
	}
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.ParentPlanID = strings.TrimSpace(request.ParentPlanID)
	request.ParentAttemptID = strings.TrimSpace(request.ParentAttemptID)
	request.ChildPlanID = strings.TrimSpace(request.ChildPlanID)
	request.ChildAgentID = strings.TrimSpace(request.ChildAgentID)
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.RequestID == "" || request.ParentPlanID == "" || request.ParentAttemptID == "" || request.ChildPlanID == "" || request.ChildAgentID == "" || request.TenantID == "" || request.WorkspaceID == "" || request.IdempotencyKey == "" || request.MaxChildDepth < 0 {
		return DelegationGrant{}, ErrDelegationInvalid
	}
	if request.ParentPlanID != parent.ChildPlanID || request.TenantID != parent.TenantID || request.WorkspaceID != parent.WorkspaceID {
		return DelegationGrant{}, ErrDelegationEscalation
	}
	if request.RecursionDepth != parent.RecursionDepth+1 || request.RecursionDepth > request.MaxChildDepth || request.RecursionDepth > parent.MaxChildDepth {
		return DelegationGrant{}, ErrDelegationEscalation
	}
	if !budgetWithin(request.Budget, parent.Budget) || !parent.Deadline.IsZero() && (request.Deadline.IsZero() || request.Deadline.After(parent.Deadline)) {
		return DelegationGrant{}, ErrDelegationEscalation
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !request.Deadline.IsZero() && !request.Deadline.After(now) {
		return DelegationGrant{}, ErrDelegationInvalid
	}
	if !capabilitySubset(request.Capabilities, parent.Capabilities) {
		return DelegationGrant{}, ErrDelegationEscalation
	}
	grant := DelegationGrant{RequestID: request.RequestID, ParentPlanID: request.ParentPlanID, ParentAttemptID: request.ParentAttemptID, ChildPlanID: request.ChildPlanID, ChildAgentID: request.ChildAgentID, TenantID: request.TenantID, WorkspaceID: request.WorkspaceID, Capabilities: cloneCapabilities(request.Capabilities), ContextBlockIDs: uniqueSorted(request.ContextBlockIDs), Budget: request.Budget, Deadline: request.Deadline.UTC(), RecursionDepth: request.RecursionDepth, MaxChildDepth: request.MaxChildDepth}
	grant.Digest = delegationDigest(grant)
	return grant, nil
}

func (g DelegationGrant) Validate() error {
	if strings.TrimSpace(g.RequestID) == "" || strings.TrimSpace(g.ParentPlanID) == "" || strings.TrimSpace(g.ParentAttemptID) == "" || strings.TrimSpace(g.ChildPlanID) == "" || strings.TrimSpace(g.ChildAgentID) == "" || strings.TrimSpace(g.TenantID) == "" || strings.TrimSpace(g.WorkspaceID) == "" || g.RecursionDepth < 1 || g.MaxChildDepth < g.RecursionDepth || strings.TrimSpace(g.Digest) == "" {
		return ErrDelegationInvalid
	}
	if err := validateBudget(g.Budget, "delegation budget"); err != nil {
		return err
	}
	if delegationDigest(g) != g.Digest {
		return ErrDelegationInvalid
	}
	return nil
}

func budgetWithin(child, parent Budget) bool {
	return boundedInt64(child.Tokens, parent.Tokens) && boundedInt(child.ToolCalls, parent.ToolCalls) && boundedInt64(child.CostCents, parent.CostCents) && boundedDuration(child.Duration, parent.Duration) && boundedInt(child.Concurrent, parent.Concurrent)
}

func boundedInt64(child, parent int64) bool { return parent <= 0 || child > 0 && child <= parent }
func boundedInt(child, parent int) bool     { return parent <= 0 || child > 0 && child <= parent }
func boundedDuration(child, parent time.Duration) bool {
	return parent <= 0 || child > 0 && child <= parent
}

func capabilitySubset(child, parent []CapabilityRef) bool {
	allowed := make(map[string]struct{}, len(parent))
	for _, item := range parent {
		allowed[strings.TrimSpace(item.Name)+"\x00"+strings.TrimSpace(item.Version)] = struct{}{}
	}
	for _, item := range child {
		if _, ok := allowed[strings.TrimSpace(item.Name)+"\x00"+strings.TrimSpace(item.Version)]; !ok {
			return false
		}
	}
	return true
}

func cloneCapabilities(items []CapabilityRef) []CapabilityRef {
	result := append([]CapabilityRef(nil), items...)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name+"\x00"+result[i].Version < result[j].Name+"\x00"+result[j].Version
	})
	return result
}

func uniqueSorted(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			seen[item] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for item := range seen {
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}

func delegationDigest(grant DelegationGrant) string {
	copy := grant
	copy.Digest = ""
	data, _ := json.Marshal(copy)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
