package runtime

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/adro-project/adro/core/identity"
)

func (j *Journal) humanRequestStateLocked(scope Scope, requestID string) HumanRequestState {
	state := HumanRequestState{Scope: scope, RequestID: requestID}
	for _, event := range j.events {
		if event.Scope != scope || event.AggregateID != requestID || (event.AggregateType != "human_interaction" && event.AggregateType != "approval") {
			continue
		}
		state.LastEventID = event.EventID
		switch event.EventType {
		case EventHumanInteractionRequested:
			var payload struct {
				Request HumanInteractionRequest `json:"request"`
			}
			if json.Unmarshal(event.Payload, &payload) == nil {
				request := payload.Request
				state.Class, state.Kind, state.TurnID, state.Status = "interaction", request.Kind, request.TurnID, HumanRequestPending
				state.RequestVersion, state.Deadline, state.EligibleActors = request.RequestVersion, request.Deadline, append([]identity.ActorRef(nil), request.EligibleActors...)
				state.ClaimPolicy, state.ClaimTTL, state.ContextDigest, state.Sensitivity, state.DefinitionDigest = request.ClaimPolicy, request.ClaimTTL, request.ContextDigest, request.Sensitivity, request.DefinitionDigest
				state.Interaction = &request
			}
		case EventApprovalAsked:
			var payload struct {
				Request ApprovalRequest `json:"request"`
			}
			if json.Unmarshal(event.Payload, &payload) == nil {
				request := payload.Request
				state.Class, state.Kind, state.TurnID, state.Status = "approval", "approval", request.TurnID, HumanRequestPending
				state.RequestVersion, state.Deadline, state.EligibleActors = request.RequestVersion, request.Deadline, append([]identity.ActorRef(nil), request.EligibleActors...)
				state.ClaimPolicy, state.ClaimTTL, state.ContextDigest, state.Sensitivity, state.DefinitionDigest = request.ClaimPolicy, request.ClaimTTL, request.ContextDigest, request.Sensitivity, request.DefinitionDigest
				state.Approval = &request
			}
		case EventHumanInteractionClaimed, EventHumanInteractionClaimTakeover, EventApprovalClaimed, EventApprovalClaimTakeover:
			var payload struct {
				Actor           identity.Actor `json:"actor"`
				ClaimExpiresAt  time.Time      `json:"claim_expires_at"`
				ClaimGeneration int64          `json:"claim_generation"`
			}
			if json.Unmarshal(event.Payload, &payload) == nil {
				ref := identity.ActorRef{Type: payload.Actor.Type, ID: payload.Actor.ID}
				state.Status, state.ClaimedBy, state.ClaimExpiresAt, state.ClaimGeneration = HumanRequestClaimed, &ref, payload.ClaimExpiresAt, payload.ClaimGeneration
			}
		case EventHumanInteractionResponded, EventApprovalDecided:
			var payload struct {
				Response HumanResponse `json:"response"`
			}
			if json.Unmarshal(event.Payload, &payload) == nil {
				response := payload.Response
				state.Status, state.Response = HumanRequestResponded, &response
			}
		case EventHumanInteractionApplied, EventApprovalApplied:
			state.Status, state.AppliedStepID = HumanRequestApplied, payloadString(event.Payload, "step_id")
		case EventHumanInteractionTimedOut, EventApprovalTimedOut:
			state.Status = HumanRequestTimedOut
		case EventHumanInteractionWithdrawn, EventApprovalWithdrawn:
			state.Status = HumanRequestWithdrawn
		}
	}
	return state
}

func (j *Journal) humanRequestInitialEventLocked(scope Scope, requestID string) (Event, error) {
	for _, event := range j.events {
		if event.Scope == scope && event.AggregateID == requestID && (event.EventType == EventHumanInteractionRequested || event.EventType == EventApprovalAsked) {
			return cloneEvent(event), nil
		}
	}
	return Event{}, ErrCorrupt
}

func (j *Journal) humanResponseEventLocked(scope Scope, requestID string) (Event, error) {
	for _, event := range j.events {
		if event.Scope == scope && event.AggregateID == requestID && (event.EventType == EventHumanInteractionResponded || event.EventType == EventApprovalDecided) {
			return cloneEvent(event), nil
		}
	}
	return Event{}, ErrCorrupt
}

func (j *Journal) humanAppliedEventLocked(scope Scope, requestID string) (Event, error) {
	for _, event := range j.events {
		if event.Scope == scope && event.AggregateID == requestID && (event.EventType == EventHumanInteractionApplied || event.EventType == EventApprovalApplied) {
			return cloneEvent(event), nil
		}
	}
	return Event{}, ErrCorrupt
}

func (j *Journal) humanTerminalEventLocked(scope Scope, requestID, interactionType, approvalType string) (Event, error) {
	for _, event := range j.events {
		if event.Scope == scope && event.AggregateID == requestID && (event.EventType == interactionType || event.EventType == approvalType) {
			return cloneEvent(event), nil
		}
	}
	return Event{}, ErrCorrupt
}

func (j *Journal) humanEventByKeyLocked(scope Scope, key string) (Event, bool) {
	for _, event := range j.events {
		if event.Scope == scope && event.IdempotencyKey == key {
			return event, true
		}
	}
	return Event{}, false
}

func humanPayloadActor(raw []byte) identity.ActorRef {
	var payload struct {
		Actor identity.Actor `json:"actor"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return identity.ActorRef{}
	}
	return identity.ActorRef{Type: payload.Actor.Type, ID: payload.Actor.ID}
}

func payloadInt64(raw []byte, key string) int64 {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload map[string]any
	if decoder.Decode(&payload) != nil {
		return 0
	}
	number, ok := payload[key].(json.Number)
	if !ok {
		return 0
	}
	value, _ := number.Int64()
	return value
}
