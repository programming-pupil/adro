package runtime

import (
	"encoding/json"
	"fmt"
	"time"
)

// HumanDeadlineResult tells a timer worker whether it committed the timeout or
// found that another durable terminal transition had already won.
type HumanDeadlineResult struct {
	Event   Event  `json:"event"`
	Expired bool   `json:"expired"`
	Reason  string `json:"reason"`
}

type humanDeadlinePayload struct {
	RequestID        string `json:"request_id"`
	RequestVersion   int64  `json:"request_version"`
	Class            string `json:"class"`
	DefinitionDigest string `json:"definition_digest"`
}

// HumanDeadlineTimerSpec converts a committed request projection into the
// idempotent one-shot TimerStore command used by deadline workers.
func HumanDeadlineTimerSpec(state HumanRequestState) (TimerSpec, error) {
	if !state.Scope.valid() || state.RequestID == "" || state.RequestVersion < 1 ||
		(state.Class != "interaction" && state.Class != "approval") || state.DefinitionDigest == "" || state.Deadline.IsZero() ||
		(state.Status != HumanRequestPending && state.Status != HumanRequestClaimed) {
		return TimerSpec{}, ErrHumanRequestInvalid
	}
	key := humanAggregateType(state.Class) + ":" + state.RequestID + ":deadline:v" + fmt.Sprintf("%d", state.RequestVersion)
	return TimerSpec{
		ScheduleKey: key,
		Scope:       state.Scope,
		DueAt:       state.Deadline,
		Command: TimerCommand{
			Name:           HumanDeadlineCommandName,
			IdempotencyKey: key,
			Payload: humanDeadlinePayload{
				RequestID: state.RequestID, RequestVersion: state.RequestVersion,
				Class: state.Class, DefinitionDigest: state.DefinitionDigest,
			},
		},
		StreamID:   "session:" + state.Scope.SessionID,
		Generation: state.RequestVersion,
		Policy:     TimerCatchUp,
		MaxCatchUp: 1,
	}, nil
}

// ApplyHumanDeadlineClaim is safe for at-least-once timer delivery. If the
// timeout event committed but timer acknowledgement was lost, the next claim
// returns the same timeout event. A response, apply, or withdrawal that won
// first converts the timer occurrence into a durable no-op that may be
// acknowledged.
func (j *Journal) ApplyHumanDeadlineClaim(claim TimerClaim, now time.Time, owner string, fencingToken int64) (HumanDeadlineResult, error) {
	now = canonicalHumanTime(now)
	if claim.Timer.Command.Name != HumanDeadlineCommandName || !claim.Timer.Scope.valid() || now.IsZero() ||
		claim.Timer.State != TimerClaimed || claim.Timer.FencingToken <= 0 || claim.OccurrenceKey == "" ||
		claim.Timer.ClaimKey != claim.OccurrenceKey || !claim.Timer.LeaseExpiresAt.After(now) {
		return HumanDeadlineResult{}, ErrTimerConflict
	}
	payloadData, err := json.Marshal(claim.Timer.Command.Payload)
	if err != nil {
		return HumanDeadlineResult{}, ErrTimerConflict
	}
	var payload humanDeadlinePayload
	if json.Unmarshal(payloadData, &payload) != nil || payload.RequestID == "" || payload.RequestVersion < 1 || payload.DefinitionDigest == "" {
		return HumanDeadlineResult{}, ErrTimerConflict
	}
	state, err := j.HumanRequestState(claim.Timer.Scope, payload.RequestID)
	if err != nil {
		return HumanDeadlineResult{}, err
	}
	if state.RequestVersion != payload.RequestVersion || state.Class != payload.Class || state.DefinitionDigest != payload.DefinitionDigest ||
		claim.Timer.Generation != payload.RequestVersion || !claim.Timer.DueAt.Equal(state.Deadline) {
		return HumanDeadlineResult{}, ErrTimerIdempotencyConflict
	}
	switch state.Status {
	case HumanRequestPending, HumanRequestClaimed:
		if now.Before(state.Deadline) {
			return HumanDeadlineResult{}, ErrTimerConflict
		}
		event, err := j.ExpireHumanRequest(claim.Timer.Scope, payload.RequestID, now, owner, fencingToken)
		if err != nil {
			return HumanDeadlineResult{}, err
		}
		return HumanDeadlineResult{Event: event, Expired: true, Reason: "deadline_expired"}, nil
	case HumanRequestTimedOut:
		j.mu.RLock()
		event, eventErr := j.humanTerminalEventLocked(claim.Timer.Scope, payload.RequestID, EventHumanInteractionTimedOut, EventApprovalTimedOut)
		j.mu.RUnlock()
		if eventErr != nil {
			return HumanDeadlineResult{}, eventErr
		}
		return HumanDeadlineResult{Event: event, Expired: true, Reason: "deadline_already_expired"}, nil
	case HumanRequestResponded, HumanRequestApplied:
		return HumanDeadlineResult{Reason: "response_won_before_deadline"}, nil
	case HumanRequestWithdrawn:
		return HumanDeadlineResult{Reason: "request_withdrawn"}, nil
	default:
		return HumanDeadlineResult{}, ErrHumanTransition
	}
}
