package secretstore

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSecretRefJSONValidation(t *testing.T) {
	valid := SecretRef("secret:vault/path@v1")
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("marshal valid ref: %v", err)
	}
	var decoded SecretRef
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal valid ref: %v", err)
	}
	if decoded != valid {
		t.Fatalf("decoded ref = %q, want %q", decoded, valid)
	}
	for _, raw := range []string{`""`, `"plaintext"`, `"secret:"`, `"secret:contains space"`, `42`, `null`} {
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
			t.Fatalf("invalid secret ref %s was accepted", raw)
		}
	}
	if _, err := json.Marshal(SecretRef("plaintext")); err == nil {
		t.Fatal("invalid secret ref marshaled successfully")
	}
}

func TestSecretRequestValidation(t *testing.T) {
	valid := SecretRequest{
		Ref: "secret:credential", TenantID: "tenant", SessionID: "session", EffectID: "effect",
		Destination: "tool:fetch", Purpose: "authorization", TTL: time.Minute,
	}
	if err := valid.Validate(5 * time.Minute); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	for name, mutate := range map[string]func(*SecretRequest){
		"ref":                 func(r *SecretRequest) { r.Ref = "plaintext" },
		"tenant":              func(r *SecretRequest) { r.TenantID = "" },
		"noncanonical tenant": func(r *SecretRequest) { r.TenantID = " tenant" },
		"session":             func(r *SecretRequest) { r.SessionID = "" },
		"effect":              func(r *SecretRequest) { r.EffectID = "" },
		"destination":         func(r *SecretRequest) { r.Destination = "" },
		"purpose":             func(r *SecretRequest) { r.Purpose = "" },
		"zero ttl":            func(r *SecretRequest) { r.TTL = 0 },
		"excess ttl":          func(r *SecretRequest) { r.TTL = 6 * time.Minute },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if err := candidate.Validate(5 * time.Minute); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("invalid request returned %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestSecretLeaseValidationAndSerialization(t *testing.T) {
	now := time.Date(2026, 9, 19, 1, 0, 0, 0, time.UTC)
	lease := SecretLease{
		ID: "lease:1", Ref: "secret:credential", TenantID: "tenant", SessionID: "session", EffectID: "effect",
		Destination: "tool:fetch", Purpose: "authorization", IssuedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	if err := lease.Validate(now); err != nil {
		t.Fatalf("valid lease rejected: %v", err)
	}
	if err := lease.Validate(now.Add(time.Minute)); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expired lease returned %v", err)
	}
	lease.Revoked = true
	if err := lease.Validate(now); !errors.Is(err, ErrLeaseRevoked) {
		t.Fatalf("revoked lease returned %v", err)
	}
	lease.Revoked = false
	lease.Purpose = ""
	if err := lease.Validate(now); !errors.Is(err, ErrInvalidLease) {
		t.Fatalf("incomplete lease returned %v", err)
	}

	lease.Purpose = "authorization"
	data, err := json.Marshal(lease)
	if err != nil {
		t.Fatalf("marshal lease: %v", err)
	}
	if strings.Contains(string(data), "canary-plaintext") || strings.Contains(string(data), "material") || strings.Contains(string(data), "value") {
		t.Fatalf("serialized lease exposes a material-bearing field: %s", data)
	}
}

func TestMaterialRequestRequiresCanonicalCompleteScope(t *testing.T) {
	valid := MaterialRequest{LeaseID: "lease:1", TenantID: "tenant", SessionID: "session", EffectID: "effect", Destination: "tool", Purpose: "auth"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid material request rejected: %v", err)
	}
	invalid := valid
	invalid.Destination = " tool"
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("noncanonical scope returned %v", err)
	}
}
