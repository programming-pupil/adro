package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	coreencoding "github.com/adro-project/adro/core/encoding"
	"github.com/adro-project/adro/core/identity"
	"github.com/adro-project/adro/core/ids"
	"github.com/adro-project/adro/internal/security"
)

const (
	HumanInteractionVersion  = 1
	HumanDeadlineCommandName = "runtime.human_request.expire"
	maxHumanResponseBytes    = 1 << 20

	HumanKindQuestion       = "question"
	HumanKindChoice         = "choice"
	HumanKindFreeform       = "freeform"
	HumanKindArtifactReview = "artifact_review"
	HumanKindTakeover       = "takeover"

	HumanClaimNone     = "none"
	HumanClaimRequired = "required"

	HumanRequestPending   = "pending"
	HumanRequestClaimed   = "claimed"
	HumanRequestResponded = "responded"
	HumanRequestApplied   = "applied"
	HumanRequestTimedOut  = "timed_out"
	HumanRequestWithdrawn = "withdrawn"

	EventHumanInteractionRequested     = "human_interaction.requested"
	EventHumanInteractionClaimed       = "human_interaction.claimed"
	EventHumanInteractionClaimTakeover = "human_interaction.claim_taken_over"
	EventHumanInteractionResponded     = "human_interaction.responded"
	EventHumanInteractionApplied       = "human_interaction.applied"
	EventHumanInteractionTimedOut      = "human_interaction.timed_out"
	EventHumanInteractionWithdrawn     = "human_interaction.withdrawn"

	EventApprovalAsked         = "approval.asked"
	EventApprovalClaimed       = "approval.claimed"
	EventApprovalClaimTakeover = "approval.claim_taken_over"
	EventApprovalDecided       = "approval.decided"
	EventApprovalApplied       = "approval.applied"
	EventApprovalTimedOut      = "approval.timed_out"
	EventApprovalWithdrawn     = "approval.withdrawn"
)

const approvalResponseSchema = `{"additionalProperties":false,"properties":{"decision":{"enum":["approved","cancelled","denied"],"type":"string"},"reason":{"maxLength":1024,"type":"string"}},"required":["decision","reason"],"type":"object"}`

var (
	ErrHumanRequestInvalid   = errors.New("runtime human request is invalid")
	ErrHumanRequestNotFound  = errors.New("runtime human request not found")
	ErrHumanTransition       = errors.New("runtime human request transition is invalid")
	ErrHumanActorIneligible  = errors.New("runtime human actor is not eligible")
	ErrHumanClaimRequired    = errors.New("runtime human request must be claimed")
	ErrHumanClaimHeld        = errors.New("runtime human request claim is held by another actor")
	ErrHumanRequestExpired   = errors.New("runtime human request deadline expired")
	ErrHumanResponseInvalid  = errors.New("runtime human response is invalid")
	ErrHumanResponseConflict = errors.New("runtime human response conflicts with committed response")
	ErrUnsafeStepBoundary    = errors.New("runtime human response cannot be applied outside a safe step boundary")
)

// HumanInteractionRequest is the immutable contract for ordinary human input.
// High-risk authorization uses ApprovalRequest and different durable events.
type HumanInteractionRequest struct {
	SchemaVersion    int                  `json:"schema_version"`
	RequestID        string               `json:"request_id"`
	RequestVersion   int64                `json:"request_version"`
	TurnID           string               `json:"turn_id"`
	Kind             string               `json:"kind"`
	Prompt           string               `json:"prompt"`
	ResponseSchema   string               `json:"response_schema"`
	Deadline         time.Time            `json:"deadline"`
	EligibleActors   []identity.ActorRef  `json:"eligible_actors"`
	ClaimPolicy      string               `json:"claim_policy"`
	ClaimTTL         time.Duration        `json:"claim_ttl,omitempty"`
	ContextDigest    string               `json:"context_digest"`
	Sensitivity      security.Sensitivity `json:"sensitivity"`
	RequestedAt      time.Time            `json:"requested_at"`
	IdempotencyKey   string               `json:"idempotency_key"`
	DefinitionDigest string               `json:"definition_digest"`
}

// ApprovalRequest is intentionally separate from ordinary questions. It binds
// the exact capability, risk, policy bundle and context being authorized.
type ApprovalRequest struct {
	SchemaVersion      int                  `json:"schema_version"`
	RequestID          string               `json:"request_id"`
	RequestVersion     int64                `json:"request_version"`
	TurnID             string               `json:"turn_id"`
	Prompt             string               `json:"prompt"`
	Capability         string               `json:"capability"`
	Risk               string               `json:"risk"`
	PolicyBundleDigest string               `json:"policy_bundle_digest"`
	Deadline           time.Time            `json:"deadline"`
	EligibleActors     []identity.ActorRef  `json:"eligible_actors"`
	ClaimPolicy        string               `json:"claim_policy"`
	ClaimTTL           time.Duration        `json:"claim_ttl,omitempty"`
	ContextDigest      string               `json:"context_digest"`
	Sensitivity        security.Sensitivity `json:"sensitivity"`
	RequestedAt        time.Time            `json:"requested_at"`
	IdempotencyKey     string               `json:"idempotency_key"`
	DefinitionDigest   string               `json:"definition_digest"`
}

type ApprovalDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

type HumanResponse struct {
	RequestID      string          `json:"request_id"`
	RequestVersion int64           `json:"request_version"`
	Actor          identity.Actor  `json:"actor"`
	Value          json.RawMessage `json:"value"`
	ResponseDigest string          `json:"response_digest"`
	IdempotencyKey string          `json:"idempotency_key"`
	RespondedAt    time.Time       `json:"responded_at"`
}

type HumanRequestState struct {
	Scope            Scope                    `json:"scope"`
	RequestID        string                   `json:"request_id"`
	RequestVersion   int64                    `json:"request_version"`
	Class            string                   `json:"class"`
	Kind             string                   `json:"kind"`
	TurnID           string                   `json:"turn_id"`
	Status           string                   `json:"status"`
	Deadline         time.Time                `json:"deadline"`
	EligibleActors   []identity.ActorRef      `json:"eligible_actors"`
	ClaimPolicy      string                   `json:"claim_policy"`
	ClaimTTL         time.Duration            `json:"claim_ttl,omitempty"`
	ContextDigest    string                   `json:"context_digest"`
	Sensitivity      security.Sensitivity     `json:"sensitivity"`
	DefinitionDigest string                   `json:"definition_digest"`
	Interaction      *HumanInteractionRequest `json:"interaction,omitempty"`
	Approval         *ApprovalRequest         `json:"approval,omitempty"`
	ClaimedBy        *identity.ActorRef       `json:"claimed_by,omitempty"`
	ClaimExpiresAt   time.Time                `json:"claim_expires_at,omitempty"`
	ClaimGeneration  int64                    `json:"claim_generation,omitempty"`
	Response         *HumanResponse           `json:"response,omitempty"`
	AppliedStepID    string                   `json:"applied_step_id,omitempty"`
	LastEventID      string                   `json:"last_event_id,omitempty"`
}

func (s HumanRequestState) Terminal() bool {
	return s.Status == HumanRequestApplied || s.Status == HumanRequestTimedOut || s.Status == HumanRequestWithdrawn
}

func FreezeHumanInteraction(request HumanInteractionRequest) (HumanInteractionRequest, error) {
	request.SchemaVersion = normalizeHumanSchemaVersion(request.SchemaVersion)
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.TurnID = strings.TrimSpace(request.TurnID)
	request.Kind = strings.TrimSpace(request.Kind)
	request.Prompt = strings.TrimSpace(request.Prompt)
	request.ContextDigest = strings.TrimSpace(request.ContextDigest)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.RequestedAt = canonicalHumanTime(request.RequestedAt)
	request.Deadline = canonicalHumanTime(request.Deadline)
	request.EligibleActors = canonicalHumanActors(request.EligibleActors)
	request.ClaimPolicy = normalizeHumanClaimPolicy(request.ClaimPolicy)
	canonicalSchema, err := canonicalToolSchema(request.ResponseSchema)
	if err != nil {
		return HumanInteractionRequest{}, fmt.Errorf("%w: response_schema: %v", ErrHumanRequestInvalid, err)
	}
	request.ResponseSchema = canonicalSchema
	request.DefinitionDigest = ""
	if err := validateHumanDefinition(request.SchemaVersion, request.RequestID, request.RequestVersion, request.TurnID, request.Prompt, request.ResponseSchema, request.Deadline, request.EligibleActors, request.ClaimPolicy, request.ClaimTTL, request.ContextDigest, request.Sensitivity, request.RequestedAt, request.IdempotencyKey); err != nil {
		return HumanInteractionRequest{}, err
	}
	if !validHumanKind(request.Kind) {
		return HumanInteractionRequest{}, fmt.Errorf("%w: unsupported interaction kind", ErrHumanRequestInvalid)
	}
	digest, err := coreencoding.Digest(request)
	if err != nil {
		return HumanInteractionRequest{}, fmt.Errorf("%w: digest: %v", ErrHumanRequestInvalid, err)
	}
	request.DefinitionDigest = digest
	return request, nil
}

func FreezeApprovalRequest(request ApprovalRequest) (ApprovalRequest, error) {
	request.SchemaVersion = normalizeHumanSchemaVersion(request.SchemaVersion)
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.TurnID = strings.TrimSpace(request.TurnID)
	request.Prompt = strings.TrimSpace(request.Prompt)
	request.Capability = strings.TrimSpace(request.Capability)
	request.Risk = strings.TrimSpace(request.Risk)
	request.PolicyBundleDigest = strings.TrimSpace(request.PolicyBundleDigest)
	request.ContextDigest = strings.TrimSpace(request.ContextDigest)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.RequestedAt = canonicalHumanTime(request.RequestedAt)
	request.Deadline = canonicalHumanTime(request.Deadline)
	request.EligibleActors = canonicalHumanActors(request.EligibleActors)
	request.ClaimPolicy = normalizeHumanClaimPolicy(request.ClaimPolicy)
	request.DefinitionDigest = ""
	if err := validateHumanDefinition(request.SchemaVersion, request.RequestID, request.RequestVersion, request.TurnID, request.Prompt, approvalResponseSchema, request.Deadline, request.EligibleActors, request.ClaimPolicy, request.ClaimTTL, request.ContextDigest, request.Sensitivity, request.RequestedAt, request.IdempotencyKey); err != nil {
		return ApprovalRequest{}, err
	}
	if !boundedHumanText(request.Capability, 256) || !boundedHumanText(request.Risk, 256) || !boundedHumanText(request.PolicyBundleDigest, 256) {
		return ApprovalRequest{}, fmt.Errorf("%w: capability, risk and policy bundle digest are required", ErrHumanRequestInvalid)
	}
	digest, err := coreencoding.Digest(request)
	if err != nil {
		return ApprovalRequest{}, fmt.Errorf("%w: digest: %v", ErrHumanRequestInvalid, err)
	}
	request.DefinitionDigest = digest
	return request, nil
}

func validateHumanDefinition(schemaVersion int, requestID string, requestVersion int64, turnID, prompt, responseSchema string, deadline time.Time, eligible []identity.ActorRef, claimPolicy string, claimTTL time.Duration, contextDigest string, sensitivity security.Sensitivity, requestedAt time.Time, idempotencyKey string) error {
	if schemaVersion != HumanInteractionVersion || requestVersion < 1 || ids.Validate("human_request", requestID) != nil || ids.Validate("turn", turnID) != nil {
		return fmt.Errorf("%w: schema, request id, version or turn id", ErrHumanRequestInvalid)
	}
	if !boundedHumanText(prompt, 4096) || responseSchema == "" || !boundedHumanText(contextDigest, 256) || !boundedHumanText(idempotencyKey, 256) {
		return fmt.Errorf("%w: prompt, response schema, context digest and idempotency key are required", ErrHumanRequestInvalid)
	}
	if requestedAt.IsZero() || deadline.IsZero() || !deadline.After(requestedAt) {
		return fmt.Errorf("%w: deadline must follow requested_at", ErrHumanRequestInvalid)
	}
	if len(eligible) == 0 || len(eligible) > 128 {
		return fmt.Errorf("%w: eligible actors are required", ErrHumanRequestInvalid)
	}
	for _, actor := range eligible {
		if actor.Type != identity.ActorHuman || ids.Validate("human_actor", actor.ID) != nil {
			return fmt.Errorf("%w: eligible actors must be human", ErrHumanRequestInvalid)
		}
	}
	if claimPolicy != HumanClaimNone && claimPolicy != HumanClaimRequired {
		return fmt.Errorf("%w: unsupported claim policy", ErrHumanRequestInvalid)
	}
	if claimPolicy == HumanClaimRequired {
		if claimTTL <= 0 || claimTTL > deadline.Sub(requestedAt) {
			return fmt.Errorf("%w: required claims need a bounded ttl", ErrHumanRequestInvalid)
		}
	} else if claimTTL != 0 {
		return fmt.Errorf("%w: claim ttl requires claim policy", ErrHumanRequestInvalid)
	}
	if !sensitivity.Valid() {
		return fmt.Errorf("%w: sensitivity", ErrHumanRequestInvalid)
	}
	return nil
}

func humanResponseSchema(state HumanRequestState) string {
	if state.Class == "approval" {
		return approvalResponseSchema
	}
	if state.Interaction == nil {
		return ""
	}
	return state.Interaction.ResponseSchema
}

func humanAggregateType(class string) string {
	if class == "approval" {
		return "approval"
	}
	return "human_interaction"
}

func humanEventType(class, interactionType, approvalType string) string {
	if class == "approval" {
		return approvalType
	}
	return interactionType
}

func humanEventKey(class, requestID, action, key string) string {
	return humanAggregateType(class) + ":" + requestID + ":" + action + ":" + strings.TrimSpace(key)
}

func validateHumanActor(scope Scope, actor identity.Actor, now time.Time) error {
	if actor.Type != identity.ActorHuman || actor.TenantID != scope.TenantID || actor.WorkspaceID != scope.WorkspaceID {
		return ErrHumanActorIneligible
	}
	if err := actor.Validate(now, actor.Audience); err != nil {
		return fmt.Errorf("%w: %v", ErrHumanActorIneligible, err)
	}
	return nil
}

func actorEligible(eligible []identity.ActorRef, actor identity.Actor) bool {
	ref := identity.ActorRef{Type: actor.Type, ID: actor.ID}
	for _, candidate := range eligible {
		if candidate == ref {
			return true
		}
	}
	return false
}

func normalizeHumanSchemaVersion(version int) int {
	if version == 0 {
		return HumanInteractionVersion
	}
	return version
}

func normalizeHumanClaimPolicy(policy string) string {
	policy = strings.TrimSpace(policy)
	if policy == "" {
		return HumanClaimNone
	}
	return policy
}

func validHumanKind(kind string) bool {
	switch kind {
	case HumanKindQuestion, HumanKindChoice, HumanKindFreeform, HumanKindArtifactReview, HumanKindTakeover:
		return true
	default:
		return false
	}
}

func canonicalHumanActors(actors []identity.ActorRef) []identity.ActorRef {
	result := append([]identity.ActorRef(nil), actors...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Type == result[j].Type {
			return result[i].ID < result[j].ID
		}
		return result[i].Type < result[j].Type
	})
	canonical := result[:0]
	for _, actor := range result {
		actor.ID = strings.TrimSpace(actor.ID)
		if len(canonical) > 0 && canonical[len(canonical)-1] == actor {
			continue
		}
		canonical = append(canonical, actor)
	}
	return canonical
}

func canonicalHumanTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.UTC().Truncate(time.Microsecond)
}

func boundedHumanText(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum && !strings.ContainsRune(value, '\x00')
}

func cloneHumanRequestState(state HumanRequestState) HumanRequestState {
	state.EligibleActors = append([]identity.ActorRef(nil), state.EligibleActors...)
	if state.Interaction != nil {
		copyRequest := *state.Interaction
		copyRequest.EligibleActors = append([]identity.ActorRef(nil), copyRequest.EligibleActors...)
		state.Interaction = &copyRequest
	}
	if state.Approval != nil {
		copyRequest := *state.Approval
		copyRequest.EligibleActors = append([]identity.ActorRef(nil), copyRequest.EligibleActors...)
		state.Approval = &copyRequest
	}
	if state.ClaimedBy != nil {
		copyActor := *state.ClaimedBy
		state.ClaimedBy = &copyActor
	}
	if state.Response != nil {
		copyResponse := *state.Response
		copyResponse.Actor = copyResponse.Actor.Clone()
		copyResponse.Value = append(json.RawMessage(nil), copyResponse.Value...)
		state.Response = &copyResponse
	}
	return state
}
