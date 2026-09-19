package runtime

import (
	"errors"
	"testing"
	"time"
)

func testModelCapabilities() ProviderCapabilities {
	return ProviderCapabilities{
		ProviderVersion: "provider-1", ProtocolVersion: 1, Models: []string{"model-a"},
		Streaming: true, Tools: true, StructuredOutput: true, Continuation: true,
	}
}

func TestClassifyModelFailureAndRetryPolicy(t *testing.T) {
	failure := ClassifyModelFailure("429", 429, false, false, false)
	if failure.Class != ModelFailureRateLimit {
		t.Fatalf("failure=%+v", failure)
	}
	failure.RetryAfter = 3 * time.Second
	policy := ModelRetryPolicy{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: 10 * time.Second, MaxCumulative: 20 * time.Second, Jitter: true}
	decision, err := DecideModelRetry(failure, ModelState{}, policy, 1, false, false, 7)
	if err != nil || decision.Action != ModelRetrySameRequest || !decision.Retryable || decision.NextAttempt != 2 || decision.Delay < 3*time.Second {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	invalid := ClassifyModelFailure("invalid_request", 400, false, false, false)
	decision, err = DecideModelRetry(invalid, ModelState{}, policy, 1, false, false, 0)
	if err != nil || decision.Action != ModelRetryNone || decision.Retryable {
		t.Fatalf("invalid decision=%+v err=%v", decision, err)
	}
}

func TestModelRetryNeverRerunsUnknownDispatch(t *testing.T) {
	policy := ModelRetryPolicy{MaxAttempts: 5, BaseDelay: time.Second, MaxDelay: time.Minute}
	failure := ModelFailure{Class: ModelFailureTransport, Dispatched: true, ProviderAccepted: false}
	decision, err := DecideModelRetry(failure, ModelState{Dispatched: true, OutcomeUnknown: true}, policy, 1, false, false, 0)
	if err != nil || decision.Action != ModelRetrySuspendUnknown || decision.Retryable {
		t.Fatalf("unknown dispatch decision=%+v err=%v", decision, err)
	}
	stream := ModelFailure{Class: ModelFailureStreamInterrupted, Dispatched: true, StreamStarted: true, ProviderAccepted: true}
	decision, err = DecideModelRetry(stream, ModelState{Dispatched: true}, policy, 1, true, false, 0)
	if err != nil || decision.Action != ModelRetryResume || decision.Retryable {
		t.Fatalf("resume decision=%+v err=%v", decision, err)
	}
}

func TestModelRoutePersistsCandidateEvidenceAndReusesHistoricalDecision(t *testing.T) {
	now := time.Date(2026, 9, 19, 3, 0, 0, 0, time.UTC)
	candidates := []ModelRouteCandidate{
		{Adapter: "slow", Model: "model-a", Capabilities: testModelCapabilities(), Healthy: true, CostPer1K: 0.02, Latency: 2 * time.Second},
		{Adapter: "fast", Model: "model-a", Capabilities: testModelCapabilities(), Healthy: true, CostPer1K: 0.01, Latency: time.Second},
		{Adapter: "bad", Model: "model-a", Capabilities: testModelCapabilities(), Healthy: false},
	}
	decision, err := SelectModelRoute(ModelRouteRequest{Model: "model-a", Required: []string{"streaming", "tools"}, Now: now, RequestDigest: "digest-1"}, candidates, nil)
	if err != nil || decision.SelectedAdapter != "fast" || len(decision.Candidates) != 3 {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	if decision.Candidates[len(decision.Candidates)-1].ExclusionReason == "" {
		t.Fatalf("missing exclusion evidence=%+v", decision.Candidates)
	}
	replayed, err := SelectModelRoute(ModelRouteRequest{Model: "model-a", Required: []string{"does-not-matter"}, Now: now.Add(time.Hour), RequestDigest: "digest-1"}, candidates[:1], &decision)
	if err == nil || !errors.Is(err, errors.New("historical route is unavailable; replay cannot reroute")) {
		// errors.Is cannot match a fresh sentinel; assert the stable message below.
		if err == nil || err.Error() != "historical route is unavailable; replay cannot reroute" {
			t.Fatalf("historical route error=%v", err)
		}
	}
	_ = replayed
	replayed, err = SelectModelRoute(ModelRouteRequest{Model: "model-a", Now: now.Add(time.Hour), RequestDigest: "digest-1"}, candidates, &decision)
	if err != nil || !replayed.Historical || replayed.SelectedAdapter != "fast" {
		t.Fatalf("historical decision=%+v err=%v", replayed, err)
	}
}

func TestModelCircuitBreakerSeparatesHalfOpenProbe(t *testing.T) {
	now := time.Date(2026, 9, 19, 3, 0, 0, 0, time.UTC)
	breaker, err := NewModelCircuitBreaker(CircuitPolicy{FailureThreshold: 2, OpenFor: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	allowed, probe, err := breaker.Allow(now)
	if err != nil || !allowed || probe {
		t.Fatalf("initial allow=%v probe=%v err=%v", allowed, probe, err)
	}
	_ = breaker.RecordFailure(now, ModelFailureTransport)
	_ = breaker.RecordFailure(now.Add(time.Second), ModelFailureServer)
	if allowed, _, _ := breaker.Allow(now.Add(2 * time.Second)); allowed {
		t.Fatal("open circuit admitted normal request")
	}
	allowed, probe, err = breaker.Allow(now.Add(time.Minute + time.Second))
	if err != nil || !allowed || !probe {
		t.Fatalf("half-open allow=%v probe=%v err=%v", allowed, probe, err)
	}
	if allowed, probe, _ := breaker.Allow(now.Add(time.Minute + 2*time.Second)); allowed || probe {
		t.Fatal("second half-open probe was admitted")
	}
	breaker.RecordSuccess()
	if snapshot := breaker.Snapshot(); snapshot.State != CircuitClosed || snapshot.Failures != 0 {
		t.Fatalf("closed snapshot=%+v", snapshot)
	}
}

func TestModelRetryTimerSpecBindsFrozenRequestAndAttempt(t *testing.T) {
	request := newModelRequest(t)
	request.Attempt = 1
	request, err := NewModelRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	decision := RetryDecision{Action: ModelRetrySameRequest, Retryable: true, Attempt: 1, NextAttempt: 2, Delay: 5 * time.Second, Reason: "pre_dispatch_retryable_failure"}
	now := time.Date(2026, 9, 19, 3, 0, 0, 0, time.UTC)
	spec, err := ModelRetryTimerSpec(request, decision, now)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Command.Name != "model.retry" || spec.Generation != 2 || !spec.DueAt.Equal(now.Add(5*time.Second)) || spec.Scope != request.Scope {
		t.Fatalf("spec=%+v", spec)
	}
	if _, err := NewTimerStore("", TimerStoreOptions{}); err == nil {
		t.Fatal("empty timer path unexpectedly accepted")
	}
}
