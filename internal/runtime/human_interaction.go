package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	coreencoding "github.com/adro-project/adro/core/encoding"
	"github.com/adro-project/adro/core/identity"
)

func (j *Journal) RequestHumanInteraction(scope Scope, request HumanInteractionRequest, owner string, fencingToken int64) (Event, error) {
	frozen, err := FreezeHumanInteraction(request)
	if err != nil {
		return Event{}, err
	}
	return j.createHumanRequest(scope, "interaction", EventHumanInteractionRequested, frozen.RequestID, frozen.TurnID, frozen.IdempotencyKey, frozen.DefinitionDigest, map[string]any{"request": frozen}, owner, fencingToken)
}

func (j *Journal) AskApproval(scope Scope, request ApprovalRequest, owner string, fencingToken int64) (Event, error) {
	frozen, err := FreezeApprovalRequest(request)
	if err != nil {
		return Event{}, err
	}
	return j.createHumanRequest(scope, "approval", EventApprovalAsked, frozen.RequestID, frozen.TurnID, frozen.IdempotencyKey, frozen.DefinitionDigest, map[string]any{"request": frozen}, owner, fencingToken)
}

func (j *Journal) createHumanRequest(scope Scope, class, eventType, requestID, turnID, key, definitionDigest string, payload map[string]any, owner string, fencingToken int64) (Event, error) {
	if !scope.valid() || strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrLeaseLost
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	if err := j.validateLifecycleLeaseLocked(scope, owner, fencingToken); err != nil {
		return Event{}, err
	}
	turn := j.turnStateLocked(scope, turnID)
	if turn.Status != TurnRunning && turn.Status != TurnWaitingInput && turn.Status != TurnWaitingApproval {
		return Event{}, ErrHumanTransition
	}
	if (turn.Status == TurnWaitingInput && class != "interaction") || (turn.Status == TurnWaitingApproval && class != "approval") {
		return Event{}, ErrHumanTransition
	}
	state := j.humanRequestStateLocked(scope, requestID)
	if state.Status != "" {
		if state.Class != class || state.DefinitionDigest != definitionDigest {
			return Event{}, ErrIdempotencyConflict
		}
		return j.humanRequestInitialEventLocked(scope, requestID)
	}
	input := Input{EventType: eventType, AggregateType: humanAggregateType(class), AggregateID: requestID, Scope: scope, IdempotencyKey: humanEventKey(class, requestID, "request", key), WriterID: owner, FencingToken: fencingToken, Status: StatusPending, Payload: payload}
	// The request and its durable deadline intent share one journal commit.
	// TimerStore materialization may lag or retry, but the authoritative event
	// cannot exist without a replayable deadline specification.
	deadlineState := HumanRequestState{Scope: scope, RequestID: requestID, RequestVersion: payloadRequestVersion(class, payload), Class: class, DefinitionDigest: definitionDigest, Deadline: payloadDeadline(class, payload), Status: HumanRequestPending}
	deadlineSpec, err := HumanDeadlineTimerSpec(deadlineState)
	if err != nil {
		return Event{}, err
	}
	schedule, err := NewTimerScheduleRequest(deadlineSpec)
	if err != nil {
		return Event{}, err
	}
	scheduleInput := Input{EventType: EventTimerScheduleRequested, AggregateType: "timer", AggregateID: deadlineSpec.ScheduleKey, Scope: scope, IdempotencyKey: "timer:schedule:" + deadlineSpec.ScheduleKey, WriterID: owner, FencingToken: fencingToken, Status: StatusPending, Payload: map[string]any{"request": schedule}}
	if _, err := j.appendBatchLocked([]Input{input, scheduleInput}); err != nil {
		return Event{}, err
	}
	// The public request API returns the human request event; the timer intent
	// is an atomic companion event and must not change the caller's identity.
	return j.humanRequestInitialEventLocked(scope, requestID)
}

func payloadRequestVersion(class string, payload map[string]any) int64 {
	if request, ok := payload["request"]; ok {
		data, _ := json.Marshal(request)
		var common struct {
			RequestVersion int64 `json:"request_version"`
		}
		_ = json.Unmarshal(data, &common)
		return common.RequestVersion
	}
	return 0
}

func payloadDeadline(class string, payload map[string]any) time.Time {
	if request, ok := payload["request"]; ok {
		data, _ := json.Marshal(request)
		var common struct {
			Deadline time.Time `json:"deadline"`
		}
		_ = json.Unmarshal(data, &common)
		return common.Deadline
	}
	return time.Time{}
}

func (j *Journal) HumanRequestState(scope Scope, requestID string) (HumanRequestState, error) {
	requestID = strings.TrimSpace(requestID)
	if !scope.valid() || requestID == "" {
		return HumanRequestState{}, ErrHumanRequestInvalid
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	state := j.humanRequestStateLocked(scope, requestID)
	if state.Status == "" {
		return HumanRequestState{}, ErrHumanRequestNotFound
	}
	return cloneHumanRequestState(state), nil
}

func (j *Journal) ListHumanRequests(scope Scope, includeTerminal bool) []HumanRequestState {
	j.mu.RLock()
	defer j.mu.RUnlock()
	idsByKey := map[string]struct{}{}
	for _, event := range j.events {
		if event.Scope != scope || (event.AggregateType != "human_interaction" && event.AggregateType != "approval") {
			continue
		}
		idsByKey[event.AggregateID] = struct{}{}
	}
	requestIDs := make([]string, 0, len(idsByKey))
	for requestID := range idsByKey {
		requestIDs = append(requestIDs, requestID)
	}
	sort.Strings(requestIDs)
	result := make([]HumanRequestState, 0, len(requestIDs))
	for _, requestID := range requestIDs {
		state := j.humanRequestStateLocked(scope, requestID)
		if !includeTerminal && state.Terminal() {
			continue
		}
		result = append(result, cloneHumanRequestState(state))
	}
	return result
}

func (j *Journal) ClaimHumanRequest(scope Scope, requestID string, actor identity.Actor, requestVersion int64, idempotencyKey string, now time.Time, owner string, fencingToken int64) (Event, error) {
	return j.claimHumanRequest(scope, requestID, actor, requestVersion, idempotencyKey, now, "", "", false, owner, fencingToken)
}

func (j *Journal) TakeOverHumanRequest(scope Scope, requestID string, actor identity.Actor, requestVersion int64, idempotencyKey, reason, approvalID string, now time.Time, owner string, fencingToken int64) (Event, error) {
	return j.claimHumanRequest(scope, requestID, actor, requestVersion, idempotencyKey, now, reason, approvalID, true, owner, fencingToken)
}

func (j *Journal) claimHumanRequest(scope Scope, requestID string, actor identity.Actor, requestVersion int64, idempotencyKey string, now time.Time, reason, approvalID string, takeover bool, owner string, fencingToken int64) (Event, error) {
	requestID, idempotencyKey = strings.TrimSpace(requestID), strings.TrimSpace(idempotencyKey)
	now = canonicalHumanTime(now)
	if !scope.valid() || requestID == "" || requestVersion < 1 || !boundedHumanText(idempotencyKey, 256) || now.IsZero() || strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrHumanRequestInvalid
	}
	if err := validateHumanActor(scope, actor, now); err != nil {
		return Event{}, err
	}
	if takeover && (!boundedHumanText(strings.TrimSpace(reason), 512) || !boundedHumanText(strings.TrimSpace(approvalID), 256)) {
		return Event{}, fmt.Errorf("%w: takeover requires reason and approval", ErrHumanRequestInvalid)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	if err := j.validateLifecycleLeaseLocked(scope, owner, fencingToken); err != nil {
		return Event{}, err
	}
	state := j.humanRequestStateLocked(scope, requestID)
	if state.Status == "" {
		return Event{}, ErrHumanRequestNotFound
	}
	action := "claim"
	eventType := humanEventType(state.Class, EventHumanInteractionClaimed, EventApprovalClaimed)
	if takeover {
		action = "takeover"
		eventType = humanEventType(state.Class, EventHumanInteractionClaimTakeover, EventApprovalClaimTakeover)
	}
	fullKey := humanEventKey(state.Class, requestID, action, idempotencyKey)
	if prior, ok := j.humanEventByKeyLocked(scope, fullKey); ok {
		matches := prior.EventType == eventType && humanPayloadActor(prior.Payload) == (identity.ActorRef{Type: actor.Type, ID: actor.ID}) && payloadInt64(prior.Payload, "request_version") == requestVersion
		if takeover {
			matches = matches && payloadString(prior.Payload, "reason") == strings.TrimSpace(reason) && payloadString(prior.Payload, "approval_id") == strings.TrimSpace(approvalID)
		}
		if matches {
			return cloneEvent(prior), nil
		}
		return Event{}, ErrIdempotencyConflict
	}
	if state.Terminal() || state.Status == HumanRequestResponded || state.RequestVersion != requestVersion {
		return Event{}, ErrHumanTransition
	}
	if !actorEligible(state.EligibleActors, actor) {
		return Event{}, ErrHumanActorIneligible
	}
	if !now.Before(state.Deadline) {
		return Event{}, ErrHumanRequestExpired
	}
	if state.ClaimPolicy != HumanClaimRequired {
		return Event{}, ErrHumanTransition
	}
	if takeover {
		if state.Status != HumanRequestClaimed || state.ClaimedBy == nil || *state.ClaimedBy == (identity.ActorRef{Type: actor.Type, ID: actor.ID}) {
			return Event{}, ErrHumanTransition
		}
	} else if state.Status != HumanRequestPending {
		return Event{}, ErrHumanClaimHeld
	}
	claimExpiresAt := now.Add(state.ClaimTTL)
	if claimExpiresAt.After(state.Deadline) {
		claimExpiresAt = state.Deadline
	}
	payload := map[string]any{
		"request_id": requestID, "request_version": requestVersion, "actor": actor,
		"claimed_at": now, "claim_expires_at": claimExpiresAt, "claim_generation": state.ClaimGeneration + 1,
	}
	if takeover {
		payload["previous_actor"] = state.ClaimedBy
		payload["reason"] = strings.TrimSpace(reason)
		payload["approval_id"] = strings.TrimSpace(approvalID)
	}
	return j.appendBatchLocked([]Input{{EventType: eventType, AggregateType: humanAggregateType(state.Class), AggregateID: requestID, Scope: scope, IdempotencyKey: fullKey, WriterID: owner, FencingToken: fencingToken, Status: StatusPending, Payload: payload}})
}

func (j *Journal) RespondHumanInteraction(scope Scope, requestID string, actor identity.Actor, requestVersion int64, idempotencyKey string, value json.RawMessage, now time.Time, owner string, fencingToken int64) (Event, error) {
	return j.respondHumanRequest(scope, requestID, actor, requestVersion, idempotencyKey, value, now, false, owner, fencingToken)
}

func (j *Journal) DecideApproval(scope Scope, requestID string, actor identity.Actor, requestVersion int64, idempotencyKey string, decision ApprovalDecision, now time.Time, owner string, fencingToken int64) (Event, error) {
	decision.Decision = strings.ToLower(strings.TrimSpace(decision.Decision))
	decision.Reason = strings.TrimSpace(decision.Reason)
	if decision.Decision != "approved" && decision.Decision != "denied" && decision.Decision != "cancelled" {
		return Event{}, fmt.Errorf("%w: unsupported approval decision", ErrHumanResponseInvalid)
	}
	if !boundedHumanText(decision.Reason, 1024) {
		return Event{}, fmt.Errorf("%w: approval reason is required", ErrHumanResponseInvalid)
	}
	value, err := coreencoding.Marshal(decision)
	if err != nil {
		return Event{}, err
	}
	return j.respondHumanRequest(scope, requestID, actor, requestVersion, idempotencyKey, value, now, true, owner, fencingToken)
}

func (j *Journal) respondHumanRequest(scope Scope, requestID string, actor identity.Actor, requestVersion int64, idempotencyKey string, value json.RawMessage, now time.Time, approval bool, owner string, fencingToken int64) (Event, error) {
	requestID, idempotencyKey = strings.TrimSpace(requestID), strings.TrimSpace(idempotencyKey)
	now = canonicalHumanTime(now)
	if !scope.valid() || requestID == "" || requestVersion < 1 || !boundedHumanText(idempotencyKey, 256) || now.IsZero() || strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrHumanResponseInvalid
	}
	if err := validateHumanActor(scope, actor, now); err != nil {
		return Event{}, err
	}
	if len(value) == 0 || len(value) > maxHumanResponseBytes {
		return Event{}, ErrHumanResponseInvalid
	}
	canonical, err := coreencoding.Canonicalize(value)
	if err != nil {
		return Event{}, fmt.Errorf("%w: %v", ErrHumanResponseInvalid, err)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	if err := j.validateLifecycleLeaseLocked(scope, owner, fencingToken); err != nil {
		return Event{}, err
	}
	state := j.humanRequestStateLocked(scope, requestID)
	if state.Status == "" {
		return Event{}, ErrHumanRequestNotFound
	}
	if approval != (state.Class == "approval") {
		return Event{}, ErrHumanTransition
	}
	if state.RequestVersion != requestVersion {
		return Event{}, ErrHumanResponseConflict
	}
	responseDigest, err := coreencoding.Digest(struct {
		RequestID      string          `json:"request_id"`
		RequestVersion int64           `json:"request_version"`
		Actor          identity.Actor  `json:"actor"`
		Value          json.RawMessage `json:"value"`
	}{requestID, requestVersion, actor, canonical})
	if err != nil {
		return Event{}, err
	}
	fullKey := humanEventKey(state.Class, requestID, "response", idempotencyKey)
	if prior, ok := j.humanEventByKeyLocked(scope, fullKey); ok {
		if payloadString(prior.Payload, "response_digest") == responseDigest {
			return cloneEvent(prior), nil
		}
		return Event{}, ErrHumanResponseConflict
	}
	if state.Status == HumanRequestResponded {
		if state.Response != nil && state.Response.ResponseDigest == responseDigest && state.Response.IdempotencyKey == idempotencyKey {
			return j.humanResponseEventLocked(scope, requestID)
		}
		return Event{}, ErrHumanResponseConflict
	}
	if state.Status == HumanRequestTimedOut || !now.Before(state.Deadline) {
		return Event{}, ErrHumanRequestExpired
	}
	if state.Terminal() {
		return Event{}, ErrHumanTransition
	}
	if !actorEligible(state.EligibleActors, actor) {
		return Event{}, ErrHumanActorIneligible
	}
	if state.ClaimPolicy == HumanClaimRequired {
		ref := identity.ActorRef{Type: actor.Type, ID: actor.ID}
		if state.Status != HumanRequestClaimed || state.ClaimedBy == nil || *state.ClaimedBy != ref {
			return Event{}, ErrHumanClaimRequired
		}
		if !now.Before(state.ClaimExpiresAt) {
			return Event{}, ErrHumanClaimHeld
		}
	} else if state.Status != HumanRequestPending {
		return Event{}, ErrHumanTransition
	}
	schema, err := parseToolSchema(humanResponseSchema(state))
	if err != nil {
		return Event{}, fmt.Errorf("%w: schema: %v", ErrHumanResponseInvalid, err)
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return Event{}, fmt.Errorf("%w: %v", ErrHumanResponseInvalid, err)
	}
	if schema == nil || schema.validate("$", decoded) != nil {
		return Event{}, ErrHumanResponseInvalid
	}
	response := HumanResponse{RequestID: requestID, RequestVersion: requestVersion, Actor: actor.Clone(), Value: canonical, ResponseDigest: responseDigest, IdempotencyKey: idempotencyKey, RespondedAt: now}
	eventType := humanEventType(state.Class, EventHumanInteractionResponded, EventApprovalDecided)
	return j.appendBatchLocked([]Input{{EventType: eventType, AggregateType: humanAggregateType(state.Class), AggregateID: requestID, Scope: scope, IdempotencyKey: fullKey, WriterID: owner, FencingToken: fencingToken, Status: StatusCommitted, Payload: map[string]any{"response": response, "response_digest": responseDigest, "request_version": requestVersion}}})
}

// ApplyHumanResponseAtBoundary makes a committed response visible to the next
// context freeze. The target step must still be pending and its turn must be in
// the corresponding waiting state, so input cannot alter an active model stream
// or a dispatched effect.
func (j *Journal) ApplyHumanResponseAtBoundary(scope Scope, requestID, stepID string, now time.Time, owner string, fencingToken int64) (Event, error) {
	requestID, stepID = strings.TrimSpace(requestID), strings.TrimSpace(stepID)
	now = canonicalHumanTime(now)
	if !scope.valid() || requestID == "" || stepID == "" || now.IsZero() || strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrHumanRequestInvalid
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	if err := j.validateLifecycleLeaseLocked(scope, owner, fencingToken); err != nil {
		return Event{}, err
	}
	state := j.humanRequestStateLocked(scope, requestID)
	if state.Status == "" {
		return Event{}, ErrHumanRequestNotFound
	}
	if state.Status == HumanRequestApplied {
		if state.AppliedStepID == stepID {
			return j.humanAppliedEventLocked(scope, requestID)
		}
		return Event{}, ErrHumanResponseConflict
	}
	if state.Status != HumanRequestResponded || state.Response == nil {
		return Event{}, ErrHumanTransition
	}
	step := j.stepStateLocked(scope, stepID)
	turn := j.turnStateLocked(scope, state.TurnID)
	expectedTurnStatus := TurnWaitingInput
	if state.Class == "approval" {
		expectedTurnStatus = TurnWaitingApproval
	}
	if step.Status != StepPending || step.TurnID != state.TurnID || turn.Status != expectedTurnStatus {
		return Event{}, ErrUnsafeStepBoundary
	}
	eventType := humanEventType(state.Class, EventHumanInteractionApplied, EventApprovalApplied)
	key := humanEventKey(state.Class, requestID, "apply", state.Response.ResponseDigest+":"+stepID)
	return j.appendBatchLocked([]Input{{EventType: eventType, AggregateType: humanAggregateType(state.Class), AggregateID: requestID, Scope: scope, IdempotencyKey: key, WriterID: owner, FencingToken: fencingToken, Status: StatusCommitted, Payload: map[string]any{"request_id": requestID, "request_version": state.RequestVersion, "response_digest": state.Response.ResponseDigest, "step_id": stepID, "applied_at": now}}})
}

func (j *Journal) ExpireHumanRequest(scope Scope, requestID string, now time.Time, owner string, fencingToken int64) (Event, error) {
	requestID, now = strings.TrimSpace(requestID), canonicalHumanTime(now)
	if !scope.valid() || requestID == "" || now.IsZero() || strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrHumanRequestInvalid
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	if err := j.validateLifecycleLeaseLocked(scope, owner, fencingToken); err != nil {
		return Event{}, err
	}
	state := j.humanRequestStateLocked(scope, requestID)
	if state.Status == "" {
		return Event{}, ErrHumanRequestNotFound
	}
	if state.Status == HumanRequestTimedOut {
		return j.humanTerminalEventLocked(scope, requestID, EventHumanInteractionTimedOut, EventApprovalTimedOut)
	}
	if state.Status != HumanRequestPending && state.Status != HumanRequestClaimed {
		return Event{}, ErrHumanTransition
	}
	if now.Before(state.Deadline) {
		return Event{}, ErrHumanTransition
	}
	eventType := humanEventType(state.Class, EventHumanInteractionTimedOut, EventApprovalTimedOut)
	key := humanEventKey(state.Class, requestID, "timeout", fmt.Sprintf("v%d", state.RequestVersion))
	return j.appendBatchLocked([]Input{{EventType: eventType, AggregateType: humanAggregateType(state.Class), AggregateID: requestID, Scope: scope, IdempotencyKey: key, WriterID: owner, FencingToken: fencingToken, Status: StatusRejected, Payload: map[string]any{"request_id": requestID, "request_version": state.RequestVersion, "deadline": state.Deadline, "timed_out_at": now}}})
}

func (j *Journal) WithdrawHumanRequest(scope Scope, requestID string, actor identity.Actor, requestVersion int64, idempotencyKey, reason string, now time.Time, owner string, fencingToken int64) (Event, error) {
	requestID, idempotencyKey, reason = strings.TrimSpace(requestID), strings.TrimSpace(idempotencyKey), strings.TrimSpace(reason)
	now = canonicalHumanTime(now)
	if !scope.valid() || requestID == "" || requestVersion < 1 || !boundedHumanText(idempotencyKey, 256) || !boundedHumanText(reason, 512) || now.IsZero() || strings.TrimSpace(owner) == "" || fencingToken <= 0 {
		return Event{}, ErrHumanRequestInvalid
	}
	if err := validateHumanActor(scope, actor, now); err != nil {
		return Event{}, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.reloadLocked(); err != nil {
		return Event{}, err
	}
	if err := j.validateLifecycleLeaseLocked(scope, owner, fencingToken); err != nil {
		return Event{}, err
	}
	state := j.humanRequestStateLocked(scope, requestID)
	if state.Status == "" {
		return Event{}, ErrHumanRequestNotFound
	}
	fullKey := humanEventKey(state.Class, requestID, "withdraw", idempotencyKey)
	if prior, ok := j.humanEventByKeyLocked(scope, fullKey); ok {
		if prior.EventType == humanEventType(state.Class, EventHumanInteractionWithdrawn, EventApprovalWithdrawn) &&
			humanPayloadActor(prior.Payload) == (identity.ActorRef{Type: actor.Type, ID: actor.ID}) &&
			payloadInt64(prior.Payload, "request_version") == requestVersion && payloadString(prior.Payload, "reason") == reason {
			return cloneEvent(prior), nil
		}
		return Event{}, ErrIdempotencyConflict
	}
	if state.RequestVersion != requestVersion || (state.Status != HumanRequestPending && state.Status != HumanRequestClaimed) {
		return Event{}, ErrHumanTransition
	}
	if !actorEligible(state.EligibleActors, actor) {
		return Event{}, ErrHumanActorIneligible
	}
	eventType := humanEventType(state.Class, EventHumanInteractionWithdrawn, EventApprovalWithdrawn)
	return j.appendBatchLocked([]Input{{EventType: eventType, AggregateType: humanAggregateType(state.Class), AggregateID: requestID, Scope: scope, IdempotencyKey: fullKey, WriterID: owner, FencingToken: fencingToken, Status: StatusRejected, Payload: map[string]any{"request_id": requestID, "request_version": requestVersion, "actor": actor, "reason": reason, "withdrawn_at": now}}})
}
