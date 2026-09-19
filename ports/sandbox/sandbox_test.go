package sandbox

import (
	"errors"
	"testing"
	"time"

	"github.com/adro-project/adro/ports/secretstore"
)

func TestEnforcementLevelOrdering(t *testing.T) {
	levels := []EnforcementLevel{
		EnforcementNone,
		EnforcementProcess,
		EnforcementFilesystem,
		EnforcementNetwork,
		EnforcementContainer,
		EnforcementMicroVM,
		EnforcementRemote,
	}
	for actualIndex, actual := range levels {
		if !actual.Valid() {
			t.Fatalf("expected %q to be valid", actual)
		}
		for requiredIndex, required := range levels {
			if got, want := AtLeast(actual, required), actualIndex >= requiredIndex; got != want {
				t.Fatalf("AtLeast(%q, %q) = %v, want %v", actual, required, got, want)
			}
		}
	}
	if EnforcementLevel("future").Valid() || AtLeast(EnforcementRemote, EnforcementLevel("future")) {
		t.Fatal("unknown enforcement level was accepted")
	}
}

func TestNetworkGrantValidation(t *testing.T) {
	now := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	valid := NetworkGrant{Domain: "api.example.test", Ports: []int{443}, Protocol: "https", Purpose: "model", ExpiresAt: now.Add(time.Minute)}
	if err := valid.Validate(now); err != nil {
		t.Fatalf("valid grant rejected: %v", err)
	}
	for name, mutate := range map[string]func(*NetworkGrant){
		"missing selector":   func(g *NetworkGrant) { g.Domain = "" },
		"multiple selectors": func(g *NetworkGrant) { g.IP = "127.0.0.1" },
		"invalid domain":     func(g *NetworkGrant) { g.Domain = "bad domain" },
		"missing ports":      func(g *NetworkGrant) { g.Ports = nil },
		"invalid port":       func(g *NetworkGrant) { g.Ports = []int{0} },
		"duplicate port":     func(g *NetworkGrant) { g.Ports = []int{443, 443} },
		"invalid protocol":   func(g *NetworkGrant) { g.Protocol = "icmp" },
		"missing purpose":    func(g *NetworkGrant) { g.Purpose = "" },
		"expired":            func(g *NetworkGrant) { g.ExpiresAt = now },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			candidate.Ports = append([]int(nil), valid.Ports...)
			mutate(&candidate)
			if err := candidate.Validate(now); err == nil {
				t.Fatal("invalid network grant was accepted")
			}
		})
	}
	for name, grant := range map[string]NetworkGrant{
		"IP":   {IP: "192.0.2.1", Ports: []int{53}, Protocol: "udp", Purpose: "dns", ExpiresAt: now.Add(time.Minute)},
		"CIDR": {CIDR: "192.0.2.0/24", Ports: []int{443}, Protocol: "tcp", Purpose: "proxy", ExpiresAt: now.Add(time.Minute)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := grant.Validate(now); err != nil {
				t.Fatalf("valid %s grant rejected: %v", name, err)
			}
		})
	}
}

func TestSandboxRequestValidation(t *testing.T) {
	now := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	valid := SandboxRequest{
		TenantID: "tenant", SessionID: "session", EffectID: "effect",
		RequiredLevel: EnforcementProcess,
		Command:       []string{"tool", "arg"},
		WorkingDir:    ".",
		FileGrants:    []FileGrant{{Path: "input", Read: true}},
		SecretRefs:    []secretstore.SecretRef{"secret:credential"},
		Environment:   map[string]string{"LANG": "C"},
		Limits:        ResourceBudget{WallTimeout: time.Second, MaxOutputBytes: 1024},
	}
	if err := valid.Validate(now); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	tests := map[string]func(*SandboxRequest){
		"tenant":            func(r *SandboxRequest) { r.TenantID = "" },
		"session":           func(r *SandboxRequest) { r.SessionID = "" },
		"effect":            func(r *SandboxRequest) { r.EffectID = "" },
		"level":             func(r *SandboxRequest) { r.RequiredLevel = "future" },
		"command":           func(r *SandboxRequest) { r.Command = nil },
		"command NUL":       func(r *SandboxRequest) { r.Command = []string{"tool", "bad\x00arg"} },
		"working directory": func(r *SandboxRequest) { r.WorkingDir = "" },
		"file path":         func(r *SandboxRequest) { r.FileGrants = []FileGrant{{Read: true}} },
		"file operation":    func(r *SandboxRequest) { r.FileGrants = []FileGrant{{Path: "input"}} },
		"secret ref":        func(r *SandboxRequest) { r.SecretRefs = []secretstore.SecretRef{"plaintext"} },
		"environment key":   func(r *SandboxRequest) { r.Environment = map[string]string{"BAD=KEY": "value"} },
		"environment value": func(r *SandboxRequest) { r.Environment = map[string]string{"KEY": "bad\x00value"} },
		"negative limit":    func(r *SandboxRequest) { r.Limits.MemoryBytes = -1 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			candidate.Command = append([]string(nil), valid.Command...)
			candidate.FileGrants = append([]FileGrant(nil), valid.FileGrants...)
			candidate.SecretRefs = append([]secretstore.SecretRef(nil), valid.SecretRefs...)
			candidate.Environment = map[string]string{"LANG": "C"}
			mutate(&candidate)
			if err := candidate.Validate(now); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("invalid request returned %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestSandboxCapabilitiesFailClosed(t *testing.T) {
	capabilities := SandboxCapabilities{
		Backend: "process", Level: EnforcementProcess,
		SupportedLevels: []EnforcementLevel{EnforcementNone, EnforcementProcess},
	}
	if err := capabilities.Validate(); err != nil {
		t.Fatalf("valid capabilities rejected: %v", err)
	}
	if !capabilities.Supports(EnforcementNone) || !capabilities.Supports(EnforcementProcess) {
		t.Fatal("declared cumulative levels were not supported")
	}
	if capabilities.Supports(EnforcementFilesystem) || capabilities.Supports(EnforcementLevel("future")) {
		t.Fatal("undeclared or unknown level was supported")
	}
	invalid := capabilities
	invalid.SupportedLevels = append(invalid.SupportedLevels, EnforcementFilesystem)
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidRequest) || invalid.Supports(EnforcementProcess) {
		t.Fatalf("inconsistent capabilities did not fail closed: %v", err)
	}
	invalid = capabilities
	invalid.OutputLimitEnforcement = true
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("output enforcement without maximum returned %v", err)
	}
}
