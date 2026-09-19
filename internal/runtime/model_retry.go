package runtime

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// ModelFailureClass is a stable recovery taxonomy. Callers must branch on the
// class, never on provider error strings.
type ModelFailureClass string

const (
	ModelFailureTransport         ModelFailureClass = "transport"
	ModelFailureRateLimit         ModelFailureClass = "rate_limit"
	ModelFailureServer            ModelFailureClass = "server"
	ModelFailureInvalidRequest    ModelFailureClass = "invalid_request"
	ModelFailureContextOverflow   ModelFailureClass = "context_overflow"
	ModelFailureAuth              ModelFailureClass = "auth"
	ModelFailureCancelled         ModelFailureClass = "cancelled"
	ModelFailureStreamInterrupted ModelFailureClass = "stream_interrupted"
	ModelFailureUnknown           ModelFailureClass = "unknown"
)

func (c ModelFailureClass) valid() bool {
	switch c {
	case ModelFailureTransport, ModelFailureRateLimit, ModelFailureServer, ModelFailureInvalidRequest,
		ModelFailureContextOverflow, ModelFailureAuth, ModelFailureCancelled, ModelFailureStreamInterrupted, ModelFailureUnknown:
		return true
	default:
		return false
	}
}

// ModelFailure contains structured provider facts at the retry boundary. A
// provider may state that a request was not accepted; absent that proof, a
// dispatch boundary is treated as externally observable.
type ModelFailure struct {
	Class            ModelFailureClass `json:"class"`
	Code             string            `json:"code,omitempty"`
	Status           int               `json:"status,omitempty"`
	RetryAfter       time.Duration     `json:"retry_after,omitempty"`
	Dispatched       bool              `json:"dispatched"`
	StreamStarted    bool              `json:"stream_started"`
	ProviderAccepted bool              `json:"provider_accepted"`
}

func (f ModelFailure) Validate() error {
	if !f.Class.valid() || f.RetryAfter < 0 || f.Status < 0 {
		return errors.New("invalid model failure")
	}
	if f.StreamStarted && !f.Dispatched {
		return errors.New("stream_started requires dispatched")
	}
	return nil
}

// ClassifyModelFailure normalizes protocol/status facts into the stable
// taxonomy. It intentionally accepts only explicit codes and status classes.
func ClassifyModelFailure(code string, status int, dispatched, streamStarted, providerAccepted bool) ModelFailure {
	normalized := strings.ToLower(strings.TrimSpace(code))
	class := ModelFailureUnknown
	switch normalized {
	case "timeout", "connection", "connect", "dns", "transport", "network":
		class = ModelFailureTransport
	case "rate_limit", "rate-limited", "throttled", "429":
		class = ModelFailureRateLimit
	case "server", "upstream", "internal", "5xx":
		class = ModelFailureServer
	case "invalid_request", "invalid", "bad_request", "schema":
		class = ModelFailureInvalidRequest
	case "context_overflow", "context_length", "too_many_tokens":
		class = ModelFailureContextOverflow
	case "auth", "unauthorized", "forbidden", "401", "403":
		class = ModelFailureAuth
	case "cancelled", "canceled", "abort":
		class = ModelFailureCancelled
	case "stream_interrupted", "stream_reset", "eof":
		class = ModelFailureStreamInterrupted
	default:
		switch {
		case status == 401 || status == 403:
			class = ModelFailureAuth
		case status == 408 || status == 425 || status == 502 || status == 503 || status == 504:
			class = ModelFailureTransport
		case status == 429:
			class = ModelFailureRateLimit
		case status >= 500:
			class = ModelFailureServer
		case status >= 400:
			class = ModelFailureInvalidRequest
		}
	}
	return ModelFailure{Class: class, Code: normalized, Status: status, Dispatched: dispatched, StreamStarted: streamStarted, ProviderAccepted: providerAccepted}
}

type ModelRetryAction string

const (
	ModelRetryNone           ModelRetryAction = "none"
	ModelRetrySameRequest    ModelRetryAction = "retry_same_request"
	ModelRetryRecompile      ModelRetryAction = "recompile_context"
	ModelRetryResume         ModelRetryAction = "resume_stream"
	ModelRetryQuery          ModelRetryAction = "query_outcome"
	ModelRetrySuspendUnknown ModelRetryAction = "suspend_unknown"
)

type RetryDecision struct {
	Action      ModelRetryAction `json:"action"`
	Retryable   bool             `json:"retryable"`
	Attempt     int              `json:"attempt"`
	NextAttempt int              `json:"next_attempt,omitempty"`
	Delay       time.Duration    `json:"delay,omitempty"`
	Reason      string           `json:"reason"`
}

type ModelRetryPolicy struct {
	MaxAttempts   int           `json:"max_attempts"`
	BaseDelay     time.Duration `json:"base_delay"`
	MaxDelay      time.Duration `json:"max_delay"`
	MaxCumulative time.Duration `json:"max_cumulative"`
	Jitter        bool          `json:"jitter"`
}

func (p ModelRetryPolicy) Validate() error {
	if p.MaxAttempts < 1 || p.BaseDelay < 0 || p.MaxDelay < 0 || p.MaxCumulative < 0 {
		return errors.New("model retry policy requires positive attempts and non-negative durations")
	}
	if p.MaxDelay > 0 && p.BaseDelay > p.MaxDelay {
		return errors.New("model retry base delay cannot exceed max delay")
	}
	return nil
}

// Delay returns a bounded, deterministic delay. random is injected by the
// caller so reducers and tests never read process-global randomness.
func (p ModelRetryPolicy) Delay(attempt int, retryAfter time.Duration, random uint64) (time.Duration, error) {
	if err := p.Validate(); err != nil {
		return 0, err
	}
	if attempt < 1 {
		return 0, errors.New("retry attempt must be positive")
	}
	if p.BaseDelay == 0 && retryAfter <= 0 {
		return 0, nil
	}
	bound := p.BaseDelay
	if bound > 0 {
		for i := 1; i < attempt; i++ {
			if bound > time.Duration(math.MaxInt64/2) {
				bound = time.Duration(math.MaxInt64)
				break
			}
			bound *= 2
		}
	}
	if p.MaxDelay > 0 && bound > p.MaxDelay {
		bound = p.MaxDelay
	}
	if retryAfter > bound {
		bound = retryAfter
	}
	if p.MaxDelay > 0 && bound > p.MaxDelay {
		bound = p.MaxDelay
	}
	if p.Jitter && bound > 1 {
		// Full jitter stays within the server's Retry-After lower bound by
		// jittering only the exponential component.
		lower := retryAfter
		span := bound - lower
		if span > 0 {
			bound = lower + time.Duration(random%uint64(span+1))
		}
	}
	if p.MaxCumulative > 0 && bound > p.MaxCumulative {
		bound = p.MaxCumulative
	}
	return bound, nil
}

func DecideModelRetry(failure ModelFailure, state ModelState, policy ModelRetryPolicy, attempt int, hasContinuation, supportsQuery bool, random uint64) (RetryDecision, error) {
	if err := failure.Validate(); err != nil {
		return RetryDecision{}, err
	}
	if err := policy.Validate(); err != nil {
		return RetryDecision{}, err
	}
	if attempt < 1 {
		return RetryDecision{}, errors.New("attempt must be positive")
	}
	decision := RetryDecision{Action: ModelRetryNone, Attempt: attempt, Reason: string(failure.Class)}
	if state.OutcomeUnknown || failure.Dispatched && failure.ProviderAccepted {
		if hasContinuation && failure.Class == ModelFailureStreamInterrupted {
			decision.Action, decision.Reason = ModelRetryResume, "stream_interrupted_resume_available"
		} else if supportsQuery && failure.Dispatched {
			decision.Action, decision.Reason = ModelRetryQuery, "dispatch_outcome_requires_query"
		} else if failure.Dispatched {
			decision.Action, decision.Reason = ModelRetrySuspendUnknown, "dispatch_outcome_unknown"
		}
		return decision, nil
	}
	switch failure.Class {
	case ModelFailureCancelled, ModelFailureInvalidRequest, ModelFailureAuth:
		return decision, nil
	case ModelFailureContextOverflow:
		if failure.Dispatched {
			decision.Action, decision.Reason = ModelRetrySuspendUnknown, "context_overflow_after_dispatch"
			return decision, nil
		}
		decision.Action, decision.Retryable, decision.Reason = ModelRetryRecompile, true, "context_recompile_required"
	case ModelFailureStreamInterrupted:
		if failure.StreamStarted {
			if hasContinuation {
				decision.Action, decision.Reason = ModelRetryResume, "stream_interrupted_resume_available"
			} else if supportsQuery {
				decision.Action, decision.Reason = ModelRetryQuery, "stream_interrupted_query_available"
			} else {
				decision.Action, decision.Reason = ModelRetrySuspendUnknown, "stream_interrupted_without_reconcile"
			}
			return decision, nil
		}
		fallthrough
	case ModelFailureTransport, ModelFailureRateLimit, ModelFailureServer, ModelFailureUnknown:
		if failure.Dispatched && !failure.ProviderAccepted {
			// A retry is safe only when the provider explicitly proves it did
			// not accept the request. Otherwise the outcome is unknown.
			decision.Action, decision.Reason = ModelRetrySuspendUnknown, "dispatch_acceptance_unproven"
			return decision, nil
		}
		decision.Action, decision.Retryable, decision.Reason = ModelRetrySameRequest, true, "pre_dispatch_retryable_failure"
	}
	if attempt >= policy.MaxAttempts {
		decision.Action, decision.Retryable, decision.Reason = ModelRetryNone, false, "retry_budget_exhausted"
		return decision, nil
	}
	decision.NextAttempt = attempt + 1
	var err error
	decision.Delay, err = policy.Delay(attempt, failure.RetryAfter, random)
	if err != nil {
		return RetryDecision{}, err
	}
	return decision, nil
}

// ModelRouteCandidate is an adapter snapshot captured for one route decision.
// It is not re-evaluated during replay; the persisted decision is authoritative.
type ModelRouteCandidate struct {
	Adapter          string               `json:"adapter"`
	Model            string               `json:"model"`
	Capabilities     ProviderCapabilities `json:"capabilities"`
	Healthy          bool                 `json:"healthy"`
	RateLimitedUntil time.Time            `json:"rate_limited_until,omitempty"`
	CostPer1K        float64              `json:"cost_per_1k,omitempty"`
	Latency          time.Duration        `json:"latency,omitempty"`
}

type ModelRouteRequest struct {
	Model         string    `json:"model,omitempty"`
	Required      []string  `json:"required,omitempty"`
	Now           time.Time `json:"now"`
	RequestDigest string    `json:"request_digest"`
}

type ModelRouteEvidence struct {
	Adapter          string    `json:"adapter"`
	Model            string    `json:"model"`
	Eligible         bool      `json:"eligible"`
	Score            float64   `json:"score,omitempty"`
	ExclusionReason  string    `json:"exclusion_reason,omitempty"`
	Health           string    `json:"health,omitempty"`
	RateLimitedUntil time.Time `json:"rate_limited_until,omitempty"`
}

type ModelRouteDecision struct {
	RequestDigest   string               `json:"request_digest"`
	SelectedAdapter string               `json:"selected_adapter,omitempty"`
	SelectedModel   string               `json:"selected_model,omitempty"`
	Candidates      []ModelRouteEvidence `json:"candidates"`
	Reason          string               `json:"reason"`
	Historical      bool                 `json:"historical"`
}

func SelectModelRoute(request ModelRouteRequest, candidates []ModelRouteCandidate, historical *ModelRouteDecision) (ModelRouteDecision, error) {
	if strings.TrimSpace(request.RequestDigest) == "" || request.Now.IsZero() {
		return ModelRouteDecision{}, errors.New("request digest and route time are required")
	}
	if historical != nil {
		if historical.RequestDigest != request.RequestDigest || historical.SelectedAdapter == "" || historical.SelectedModel == "" {
			return ModelRouteDecision{}, errors.New("historical route decision does not match request")
		}
		for _, candidate := range candidates {
			if candidate.Adapter == historical.SelectedAdapter && candidate.Model == historical.SelectedModel {
				result := cloneRouteDecision(*historical)
				result.Historical = true
				return result, nil
			}
		}
		return ModelRouteDecision{}, errors.New("historical route is unavailable; replay cannot reroute")
	}
	result := ModelRouteDecision{RequestDigest: request.RequestDigest, Candidates: make([]ModelRouteEvidence, 0, len(candidates))}
	for _, candidate := range candidates {
		evidence := ModelRouteEvidence{Adapter: strings.TrimSpace(candidate.Adapter), Model: strings.TrimSpace(candidate.Model), RateLimitedUntil: candidate.RateLimitedUntil}
		switch {
		case evidence.Adapter == "" || evidence.Model == "":
			evidence.ExclusionReason = "identity_missing"
		case !candidate.Healthy:
			evidence.ExclusionReason, evidence.Health = "unhealthy", "unhealthy"
		case !candidate.RateLimitedUntil.IsZero() && request.Now.Before(candidate.RateLimitedUntil):
			evidence.ExclusionReason, evidence.Health = "rate_limited", "rate_limited"
		case request.Model != "" && candidate.Model != request.Model:
			evidence.ExclusionReason = "model_mismatch"
		default:
			missing := missingModelCapabilities(candidate.Capabilities, evidence.Model, request.Required)
			if len(missing) > 0 {
				evidence.ExclusionReason = "capability_missing:" + strings.Join(missing, ",")
			} else {
				evidence.Eligible = true
				evidence.Score = routeScore(candidate)
			}
		}
		result.Candidates = append(result.Candidates, evidence)
	}
	sort.SliceStable(result.Candidates, func(i, j int) bool {
		if result.Candidates[i].Eligible != result.Candidates[j].Eligible {
			return result.Candidates[i].Eligible
		}
		if result.Candidates[i].Score != result.Candidates[j].Score {
			return result.Candidates[i].Score < result.Candidates[j].Score
		}
		if result.Candidates[i].Adapter != result.Candidates[j].Adapter {
			return result.Candidates[i].Adapter < result.Candidates[j].Adapter
		}
		return result.Candidates[i].Model < result.Candidates[j].Model
	})
	for _, candidate := range result.Candidates {
		if candidate.Eligible {
			result.SelectedAdapter, result.SelectedModel = candidate.Adapter, candidate.Model
			result.Reason = "lowest_deterministic_score"
			return result, nil
		}
	}
	result.Reason = "no_eligible_candidate"
	return result, errors.New("no eligible model route")
}

func missingModelCapabilities(capabilities ProviderCapabilities, model string, required []string) []string {
	missing := make([]string, 0)
	for _, feature := range required {
		feature = strings.TrimSpace(feature)
		if feature != "" && !capabilities.Supports(model, feature) {
			missing = append(missing, feature)
		}
	}
	return missing
}

func routeScore(candidate ModelRouteCandidate) float64 {
	cost, latency := candidate.CostPer1K, float64(candidate.Latency.Milliseconds())
	if cost < 0 {
		cost = math.MaxFloat64 / 4
	}
	if latency < 0 {
		latency = math.MaxFloat64 / 4
	}
	return cost*1000 + latency
}

func cloneRouteDecision(input ModelRouteDecision) ModelRouteDecision {
	input.Candidates = append([]ModelRouteEvidence(nil), input.Candidates...)
	return input
}

// CircuitState is independent from request retry state. A half-open probe is
// explicitly separate from normal admission so health checks cannot consume a
// tenant's session quota.
type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"
	CircuitOpen     CircuitState = "open"
	CircuitHalfOpen CircuitState = "half_open"
)

type CircuitPolicy struct {
	FailureThreshold int
	OpenFor          time.Duration
}

type CircuitSnapshot struct {
	State         CircuitState `json:"state"`
	Failures      int          `json:"failures"`
	OpenedAt      time.Time    `json:"opened_at,omitempty"`
	ProbeInFlight bool         `json:"probe_in_flight"`
}

type ModelCircuitBreaker struct {
	mu            sync.Mutex
	policy        CircuitPolicy
	state         CircuitState
	failures      int
	openedAt      time.Time
	probeInFlight bool
}

func NewModelCircuitBreaker(policy CircuitPolicy) (*ModelCircuitBreaker, error) {
	if policy.FailureThreshold < 1 || policy.OpenFor <= 0 {
		return nil, errors.New("circuit policy requires positive threshold and open duration")
	}
	return &ModelCircuitBreaker{policy: policy, state: CircuitClosed}, nil
}

func (b *ModelCircuitBreaker) Allow(now time.Time) (bool, bool, error) {
	if b == nil || now.IsZero() {
		return false, false, errors.New("circuit and time are required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case CircuitClosed:
		return true, false, nil
	case CircuitOpen:
		if now.Before(b.openedAt.Add(b.policy.OpenFor)) {
			return false, false, nil
		}
		if b.probeInFlight {
			return false, false, nil
		}
		b.state, b.probeInFlight = CircuitHalfOpen, true
		return true, true, nil
	case CircuitHalfOpen:
		if b.probeInFlight {
			return false, false, nil
		}
		b.probeInFlight = true
		return true, true, nil
	default:
		return false, false, errors.New("invalid circuit state")
	}
}

func (b *ModelCircuitBreaker) RecordSuccess() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state, b.failures, b.openedAt, b.probeInFlight = CircuitClosed, 0, time.Time{}, false
}

func (b *ModelCircuitBreaker) RecordFailure(now time.Time, class ModelFailureClass) error {
	if b == nil || now.IsZero() || !class.valid() {
		return errors.New("circuit, time and valid failure class are required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if class == ModelFailureInvalidRequest || class == ModelFailureAuth || class == ModelFailureCancelled {
		b.probeInFlight = false
		return nil
	}
	b.failures++
	b.probeInFlight = false
	if b.failures >= b.policy.FailureThreshold {
		b.state, b.openedAt = CircuitOpen, now
	}
	return nil
}

func (b *ModelCircuitBreaker) Snapshot() CircuitSnapshot {
	if b == nil {
		return CircuitSnapshot{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return CircuitSnapshot{State: b.state, Failures: b.failures, OpenedAt: b.openedAt, ProbeInFlight: b.probeInFlight}
}

// ModelRetryTimerSpec turns a retry decision into a durable timer command.
// The timer payload carries the frozen request digest and next attempt so a
// worker can reject stale or replayed retry commands before dispatch.
func ModelRetryTimerSpec(request ModelRequest, decision RetryDecision, now time.Time) (TimerSpec, error) {
	if err := request.Validate(); err != nil {
		return TimerSpec{}, err
	}
	if !decision.Retryable || decision.NextAttempt <= request.Attempt || decision.Action == ModelRetryNone {
		return TimerSpec{}, errors.New("retry decision does not schedule a next model attempt")
	}
	if now.IsZero() {
		return TimerSpec{}, errors.New("retry timer time is required")
	}
	key := fmt.Sprintf("model:%s:retry:%d", request.RequestID, decision.NextAttempt)
	return TimerSpec{
		ScheduleKey: key,
		Scope:       request.Scope,
		DueAt:       now.UTC().Add(decision.Delay),
		Command: TimerCommand{
			Name:           "model.retry",
			IdempotencyKey: key,
			Payload: map[string]any{
				"request_id": request.RequestID, "request_digest": request.RequestDigest,
				"attempt": decision.NextAttempt, "action": decision.Action, "failure_reason": decision.Reason,
			},
		},
		StreamID:   "session:" + request.Scope.SessionID,
		Generation: int64(decision.NextAttempt),
		Policy:     TimerCatchUp,
		MaxCatchUp: 1,
	}, nil
}
