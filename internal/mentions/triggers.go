package mentions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type OutcomeStatus string

const (
	StatusQueued    OutcomeStatus = "queued"
	StatusCoalesced OutcomeStatus = "coalesced"
	StatusDeferred  OutcomeStatus = "deferred"
	StatusBlocked   OutcomeStatus = "blocked"
)

type Target struct {
	Type        TargetType `json:"target_type"`
	ID          string     `json:"target_id"`
	WorkspaceID string     `json:"workspace_id"`
	Active      bool       `json:"active"`
	Version     int64      `json:"version,omitempty"`
	LeaderID    string     `json:"leader_id,omitempty"`
	CanInvoke   bool       `json:"can_invoke,omitempty"`
}
type PendingTask struct {
	DedupeKey  string     `json:"dedupe_key"`
	TargetType TargetType `json:"target_type"`
	TargetID   string     `json:"target_id"`
}
type TriggerInput struct {
	WorkspaceID      string
	RequirementID    string
	CommentID        string
	CommentRevision  int64
	Content          string
	ParentThreadID   string
	EditingCommentID string
	SuppressAgentIDs []string
	UserCanInvoke    bool
	Targets          []Target
	Pending          []PendingTask
	RuntimeHealthy   bool
	PlanVersion      string
}
type TriggerOutcome struct {
	TargetType        TargetType    `json:"target_type"`
	TargetID          string        `json:"target_id"`
	Status            OutcomeStatus `json:"status"`
	ReasonCode        string        `json:"reason_code"`
	Reason            string        `json:"reason,omitempty"`
	AuthoritySnapshot string        `json:"authority_snapshot,omitempty"`
	DedupeKey         string        `json:"dedupe_key"`
	SourceCommentID   string        `json:"source_comment_id"`
	ParentTaskID      string        `json:"parent_task_id,omitempty"`
}
type TriggerPlan struct {
	Parser   ParseResult      `json:"parser"`
	Outcomes []TriggerOutcome `json:"trigger_outcomes"`
}

const MaxTargetsPerComment = 32

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// ComputeTriggers is the sole trigger decision function for preview, create,
// and edit. It performs no side effects and returns one independent outcome
// for every unique explicit target.
func ComputeTriggers(_ context.Context, in TriggerInput) (TriggerPlan, error) {
	if strings.TrimSpace(in.CommentID) == "" {
		return TriggerPlan{}, errors.New("comment_id is required")
	}
	parsed, err := Parse(in.Content)
	if err != nil {
		return TriggerPlan{}, err
	}
	targets := map[string]Target{}
	for _, t := range in.Targets {
		targets[string(t.Type)+":"+t.ID] = t
	}
	suppressed := map[string]bool{}
	for _, id := range in.SuppressAgentIDs {
		suppressed[id] = true
	}
	out := TriggerPlan{Parser: parsed, Outcomes: []TriggerOutcome{}}
	// ParseResult.Targets already de-duplicates by target type/id. Apply the
	// limit to unique invocation targets; repeated markup for the same agent
	// must not consume the comment's fan-out budget or block a later target.
	mentions := parsed.InvocationTargets()
	tooMany := len(mentions) > MaxTargetsPerComment
	for index, m := range mentions {
		key := string(m.TargetType) + ":" + m.TargetID
		t, ok := targets[key]
		o := TriggerOutcome{TargetType: m.TargetType, TargetID: m.TargetID, ReasonCode: "explicit_mention", SourceCommentID: in.CommentID, ParentTaskID: strings.TrimSpace(in.ParentThreadID), DedupeKey: fmt.Sprintf("%s:%s:%s:%s:%d", in.CommentID, m.TargetType, m.TargetID, in.PlanVersion, in.CommentRevision)}
		o.AuthoritySnapshot = authoritySnapshot(in, t, ok)
		if tooMany || index >= MaxTargetsPerComment {
			o.Status = StatusBlocked
			o.ReasonCode = "mention_limit_exceeded"
			o.Reason = fmt.Sprintf("a comment may target at most %d unique agents or squads", MaxTargetsPerComment)
			out.Outcomes = append(out.Outcomes, o)
			continue
		}
		if m.TargetType == TargetAll {
			if !in.UserCanInvoke {
				o.Status, o.ReasonCode, o.Reason = StatusBlocked, "invoke_forbidden", "caller is not allowed to invoke @all"
			} else if !in.RuntimeHealthy {
				o.Status, o.ReasonCode, o.Reason = StatusDeferred, "runtime_unavailable", "runtime health check is unavailable"
			} else {
				o.Status, o.ReasonCode = StatusQueued, "explicit_all"
			}
			out.Outcomes = append(out.Outcomes, o)
			continue
		}
		if !uuidPattern.MatchString(m.TargetID) && m.TargetType != TargetAll {
			o.Status = StatusBlocked
			o.ReasonCode = "invalid_target_id"
			o.Reason = "target id must be a UUID"
			out.Outcomes = append(out.Outcomes, o)
			continue
		}
		if !ok {
			o.Status = StatusBlocked
			o.ReasonCode = "target_not_found"
			o.Reason = "target is not in the workspace roster"
			out.Outcomes = append(out.Outcomes, o)
			continue
		}
		if t.WorkspaceID != "" && t.WorkspaceID != in.WorkspaceID {
			o.Status = StatusBlocked
			o.ReasonCode = "cross_workspace"
			o.Reason = "target belongs to another workspace"
			out.Outcomes = append(out.Outcomes, o)
			continue
		}
		if !t.Active {
			o.Status = StatusBlocked
			o.ReasonCode = "target_inactive"
			o.Reason = "target is disabled or archived"
			out.Outcomes = append(out.Outcomes, o)
			continue
		}
		if !in.UserCanInvoke || !t.CanInvoke {
			o.Status = StatusBlocked
			o.ReasonCode = "invoke_forbidden"
			o.Reason = "caller is not allowed to invoke target"
			out.Outcomes = append(out.Outcomes, o)
			continue
		}
		if m.TargetType == TargetSquad && t.LeaderID == "" {
			o.Status = StatusBlocked
			o.ReasonCode = "squad_leader_unavailable"
			o.Reason = "active squad has no leader"
			out.Outcomes = append(out.Outcomes, o)
			continue
		}
		if !in.RuntimeHealthy {
			o.Status = StatusDeferred
			o.ReasonCode = "runtime_unavailable"
			o.Reason = "runtime health check is unavailable"
			out.Outcomes = append(out.Outcomes, o)
			continue
		}
		duplicate := false
		for _, p := range in.Pending {
			if p.DedupeKey == o.DedupeKey || p.TargetType == m.TargetType && p.TargetID == m.TargetID {
				duplicate = true
				break
			}
		}
		if duplicate {
			o.Status = StatusCoalesced
			o.ReasonCode = "duplicate_pending"
		} else {
			o.Status = StatusQueued
		}
		out.Outcomes = append(out.Outcomes, o)
	}
	return out, nil
}

func authoritySnapshot(in TriggerInput, target Target, found bool) string {
	payload := map[string]any{"workspace_id": in.WorkspaceID, "target_type": target.Type, "target_id": target.ID, "found": found, "active": target.Active, "version": target.Version, "leader_id": target.LeaderID, "can_invoke": target.CanInvoke, "runtime_healthy": in.RuntimeHealthy}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(data)
}

// ImplicitOutcome describes an assignee/thread-owner route. @all suppresses
// these routes; explicit agent and squad mentions are never suppressed.
func ImplicitOutcome(in TriggerInput, agentID string) (TriggerOutcome, bool) {
	parsed, err := Parse(in.Content)
	if err != nil {
		return TriggerOutcome{}, false
	}
	for _, m := range parsed.Targets() {
		if m.TargetType == TargetAll {
			return TriggerOutcome{}, false
		}
		if m.TargetType == TargetAgent && m.TargetID == agentID {
			return TriggerOutcome{}, false
		}
	}
	if strings.TrimSpace(agentID) == "" {
		return TriggerOutcome{}, false
	}
	if in.SuppressAgentIDs != nil {
		for _, id := range in.SuppressAgentIDs {
			if id == agentID {
				return TriggerOutcome{}, false
			}
		}
	}
	return TriggerOutcome{TargetType: TargetAgent, TargetID: agentID, Status: StatusQueued, ReasonCode: "implicit_route", SourceCommentID: in.CommentID, DedupeKey: fmt.Sprintf("%s:agent:%s:%s:%d", in.CommentID, agentID, in.PlanVersion, in.CommentRevision)}, true
}
