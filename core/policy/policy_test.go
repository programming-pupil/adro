package policy

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testBundle(t *testing.T) FrozenBundle {
	t.Helper()
	bundle, err := FreezeBundle(Bundle{
		ID: "tenant-egress", Version: "v1", TenantID: "tenant-1", WorkspaceID: "workspace-1",
		Capabilities: []string{"events.publish", "events.read"},
		Egress:       []EgressRule{{Destination: "https://API.EXAMPLE.test:443/events", Purpose: "event delivery", MaxSensitivity: SensitivityInternal}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func testInput() Input {
	return Input{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", ActorID: "worker-1",
		Capability: "events.publish", Destination: "https://api.example.test/events",
		Purpose: "event delivery", Sensitivity: SensitivityInternal,
	}
}

func TestCapabilityAndEgressPolicyFailsClosed(t *testing.T) {
	bundle := testBundle(t)
	now := time.Date(2026, 9, 19, 1, 2, 3, 456789000, time.UTC)

	allowed, err := EvaluateFailClosed(context.Background(), BuiltinEvaluator{}, bundle, testInput(), now, time.Second, EngineVersion)
	if err != nil || allowed.Outcome != OutcomeAllow || allowed.ReasonCode != "egress_allowed" || allowed.InputDigest == "" || allowed.BundleDigest != bundle.Digest {
		t.Fatalf("allowed=%+v err=%v", allowed, err)
	}

	cases := map[string]struct {
		mutate func(*Input)
		reason string
	}{
		"tenant":      {func(input *Input) { input.TenantID = "tenant-2" }, "tenant_scope_mismatch"},
		"capability":  {func(input *Input) { input.Capability = "events.delete" }, "capability_denied"},
		"destination": {func(input *Input) { input.Destination = "https://other.example.test/events" }, "egress_destination_or_purpose_denied"},
		"purpose":     {func(input *Input) { input.Purpose = "analytics" }, "egress_destination_or_purpose_denied"},
		"sensitivity": {func(input *Input) { input.Sensitivity = SensitivitySecret }, "sensitivity_exceeds_grant"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			input := testInput()
			testCase.mutate(&input)
			decision, err := EvaluateFailClosed(context.Background(), BuiltinEvaluator{}, bundle, input, now, time.Second, EngineVersion)
			if err != nil || decision.Outcome != OutcomeDeny || decision.ReasonCode != testCase.reason {
				t.Fatalf("decision=%+v err=%v", decision, err)
			}
		})
	}
}

func TestEvaluatorFailureTimeoutAndVersionMismatchDeny(t *testing.T) {
	bundle, input := testBundle(t), testInput()
	now := time.Date(2026, 9, 19, 1, 2, 3, 0, time.UTC)
	for name, evaluator := range map[string]Evaluator{
		"failure": evaluatorFunc(func(context.Context, FrozenBundle, Input, time.Time) (DecisionRecord, error) {
			return DecisionRecord{}, errors.New("unavailable")
		}),
		"timeout": evaluatorFunc(func(ctx context.Context, _ FrozenBundle, _ Input, _ time.Time) (DecisionRecord, error) {
			<-ctx.Done()
			return DecisionRecord{}, ctx.Err()
		}),
		"ignores context": evaluatorFunc(func(context.Context, FrozenBundle, Input, time.Time) (DecisionRecord, error) {
			time.Sleep(50 * time.Millisecond)
			return DecisionRecord{}, errors.New("late result")
		}),
		"version": evaluatorFunc(func(ctx context.Context, bundle FrozenBundle, input Input, at time.Time) (DecisionRecord, error) {
			record, err := (BuiltinEvaluator{}).Evaluate(ctx, bundle, input, at)
			record.EngineVersion = "unexpected"
			return record, err
		}),
	} {
		t.Run(name, func(t *testing.T) {
			decision, err := EvaluateFailClosed(context.Background(), evaluator, bundle, input, now, 5*time.Millisecond, EngineVersion)
			if err != nil || decision.Outcome != OutcomeDeny {
				t.Fatalf("decision=%+v err=%v", decision, err)
			}
			if name == "version" && decision.ReasonCode != "policy_engine_invalid_result" {
				t.Fatalf("reason=%s", decision.ReasonCode)
			}
			if name != "version" && decision.ReasonCode != "policy_engine_unavailable" {
				t.Fatalf("reason=%s", decision.ReasonCode)
			}
		})
	}
}

func TestCanonicalDestinationRejectsAmbiguousHostsAndPaths(t *testing.T) {
	for name, destination := range map[string]string{
		"wildcard host":  "https://*.example.test/events",
		"unicode host":   "https://例.example/events",
		"escaped slash":  "https://api.example.test/a%2Fb",
		"escaped dot":    "https://api.example.test/%2e%2e/secret",
		"backslash path": "https://api.example.test/a\\b",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CanonicalDestination(destination); err == nil {
				t.Fatalf("destination %q was accepted", destination)
			}
		})
	}
	canonical, err := CanonicalDestination("https://API.EXAMPLE.TEST.:443/events")
	if err != nil || canonical != "https://api.example.test/events" {
		t.Fatalf("canonical=%q err=%v", canonical, err)
	}
}

func TestHistoricalReplayAndAuditDivergence(t *testing.T) {
	bundle, input := testBundle(t), testInput()
	now := time.Date(2026, 9, 19, 1, 2, 3, 0, time.UTC)
	historical, err := EvaluateFailClosed(context.Background(), BuiltinEvaluator{}, bundle, input, now, time.Second, EngineVersion)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := Replay(historical, bundle, input); err != nil || replay != historical {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	changed := input
	changed.Purpose = "other"
	if _, err := Replay(historical, bundle, changed); !errors.Is(err, ErrReplayConflict) {
		t.Fatalf("changed replay returned %v", err)
	}
	denier := evaluatorFunc(func(ctx context.Context, bundle FrozenBundle, input Input, at time.Time) (DecisionRecord, error) {
		record, _, err := newRecord(bundle, input, at, EngineVersion)
		if err == nil {
			record.Outcome, record.ReasonCode = OutcomeDeny, "operator_override"
		}
		return record, err
	})
	audit, err := Audit(context.Background(), denier, historical, bundle, input, time.Second)
	if err != nil || !audit.Diverged || audit.Recomputed.Outcome != OutcomeDeny {
		t.Fatalf("audit=%+v err=%v", audit, err)
	}
}

func TestChildPolicyCanOnlyNarrowAuthority(t *testing.T) {
	parent := testBundle(t)
	child, err := FreezeBundle(Bundle{
		ID: "child", Version: "v1", TenantID: "tenant-1", WorkspaceID: "workspace-1",
		Capabilities: []string{"events.publish"},
		Egress:       []EgressRule{{Destination: "https://api.example.test/events", Purpose: "event delivery", MaxSensitivity: SensitivityPublic}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateChild(parent, child); err != nil {
		t.Fatalf("narrow child: %v", err)
	}
	broader, err := FreezeBundle(Bundle{
		ID: "child", Version: "v2", TenantID: "tenant-1", WorkspaceID: "workspace-1",
		Capabilities: []string{"events.publish"},
		Egress:       []EgressRule{{Destination: "https://api.example.test/events", Purpose: "event delivery", MaxSensitivity: SensitivitySecret}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateChild(parent, broader); !errors.Is(err, ErrPrivilegeEscalate) {
		t.Fatalf("broader sensitivity returned %v", err)
	}
	capability, err := FreezeBundle(Bundle{ID: "child", Version: "v3", TenantID: "tenant-1", WorkspaceID: "workspace-1", Capabilities: []string{"events.delete"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateChild(parent, capability); !errors.Is(err, ErrPrivilegeEscalate) {
		t.Fatalf("broader capability returned %v", err)
	}
}

func TestFreezeBundleCanonicalizesAndRejectsAmbiguity(t *testing.T) {
	bundle := testBundle(t)
	if got := bundle.Egress[0].Destination; got != "https://api.example.test/events" {
		t.Fatalf("destination=%q", got)
	}
	_, err := FreezeBundle(Bundle{ID: "bad", Version: "v1", TenantID: "tenant", WorkspaceID: "workspace", Capabilities: []string{"read", "read"}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate capability returned %v", err)
	}
}

func TestNetworkAndEgressRulesMustCoverEachOther(t *testing.T) {
	egress := []EgressRule{{Destination: "https://api.example.test/events", Purpose: "delivery", MaxSensitivity: SensitivityInternal}}
	network := []NetworkRule{{Domain: "api.example.test", Ports: []int{443}, Protocol: "https", Purpose: "delivery"}}
	if err := ValidateNetworkEgress(network, egress); err != nil {
		t.Fatal(err)
	}
	network[0].Domain = "other.example.test"
	if err := ValidateNetworkEgress(network, egress); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatched destination returned %v", err)
	}
	if err := ValidateNetworkEgress(nil, egress); !errors.Is(err, ErrInvalid) {
		t.Fatalf("egress without network returned %v", err)
	}
	network[0].Domain = "*.example.test"
	if err := ValidateNetworkEgress(network, egress); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wildcard network domain returned %v", err)
	}
}

type evaluatorFunc func(context.Context, FrozenBundle, Input, time.Time) (DecisionRecord, error)

func (f evaluatorFunc) Evaluate(ctx context.Context, bundle FrozenBundle, input Input, at time.Time) (DecisionRecord, error) {
	return f(ctx, bundle, input, at)
}
