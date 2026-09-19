package memory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adro-project/adro/ports/secretstore"
)

func TestBrokerCopiesStoredAndReturnedMaterial(t *testing.T) {
	now := time.Date(2026, 9, 19, 2, 0, 0, 0, time.UTC)
	broker := newTestBroker(t, func() time.Time { return now })
	input := []byte("canary-secret-material")
	ref, err := broker.Put(PutRequest{
		TenantID: "tenant", Value: input,
		Destinations: []string{"tool:fetch"}, Purposes: []string{"authorization"},
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	clear(input)
	lease := resolveTestLease(t, broker, ref, time.Minute)
	first, err := broker.Material(context.Background(), materialRequest(lease))
	if err != nil {
		t.Fatalf("material: %v", err)
	}
	if got, want := string(first), "canary-secret-material"; got != want {
		t.Fatalf("material = %q, want %q", got, want)
	}
	clear(first)
	second, err := broker.Material(context.Background(), materialRequest(lease))
	if err != nil {
		t.Fatalf("material after caller mutation: %v", err)
	}
	if got, want := string(second), "canary-secret-material"; got != want {
		t.Fatalf("stored material mutated through caller slice: %q", got)
	}
}

// Threat ID: TM-SECRET-001
func TestBrokerRejectsUnauthorizedSecretScopes(t *testing.T) {
	now := time.Date(2026, 9, 19, 2, 0, 0, 0, time.UTC)
	broker := newTestBroker(t, func() time.Time { return now })
	ref, err := broker.Put(PutRequest{
		TenantID: "tenant", Value: []byte("canary"),
		Destinations: []string{"tool:fetch"}, Purposes: []string{"authorization"},
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	base := secretstore.SecretRequest{
		Ref: ref, TenantID: "tenant", SessionID: "session", EffectID: "effect",
		Destination: "tool:fetch", Purpose: "authorization", TTL: time.Minute,
	}
	for name, mutate := range map[string]func(*secretstore.SecretRequest){
		"tenant":      func(r *secretstore.SecretRequest) { r.TenantID = "other" },
		"destination": func(r *secretstore.SecretRequest) { r.Destination = "tool:send" },
		"purpose":     func(r *secretstore.SecretRequest) { r.Purpose = "exfiltration" },
	} {
		t.Run(name, func(t *testing.T) {
			request := base
			mutate(&request)
			if _, err := broker.Resolve(context.Background(), request); !errors.Is(err, secretstore.ErrScopeMismatch) {
				t.Fatalf("unauthorized scope returned %v", err)
			}
		})
	}
}

func TestLeaseMaterialIsBoundToCompleteScope(t *testing.T) {
	now := time.Date(2026, 9, 19, 2, 0, 0, 0, time.UTC)
	broker := newTestBroker(t, func() time.Time { return now })
	ref, err := broker.Put(PutRequest{
		TenantID: "tenant", Value: []byte("canary"),
		Destinations: []string{"tool:fetch"}, Purposes: []string{"authorization"},
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	lease := resolveTestLease(t, broker, ref, time.Minute)
	base := materialRequest(lease)
	for name, mutate := range map[string]func(*secretstore.MaterialRequest){
		"tenant":      func(r *secretstore.MaterialRequest) { r.TenantID = "other" },
		"session":     func(r *secretstore.MaterialRequest) { r.SessionID = "other" },
		"effect":      func(r *secretstore.MaterialRequest) { r.EffectID = "other" },
		"destination": func(r *secretstore.MaterialRequest) { r.Destination = "other" },
		"purpose":     func(r *secretstore.MaterialRequest) { r.Purpose = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			request := base
			mutate(&request)
			if _, err := broker.Material(context.Background(), request); !errors.Is(err, secretstore.ErrScopeMismatch) {
				t.Fatalf("scope mismatch returned %v", err)
			}
		})
	}
}

func TestLeaseExpiryAndRevocationEraseMaterial(t *testing.T) {
	now := time.Date(2026, 9, 19, 2, 0, 0, 0, time.UTC)
	current := now
	broker := newTestBroker(t, func() time.Time { return current })
	ref, err := broker.Put(PutRequest{
		TenantID: "tenant", Value: []byte("canary"),
		Destinations: []string{"tool:fetch"}, Purposes: []string{"authorization"},
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	expired := resolveTestLease(t, broker, ref, time.Minute)
	current = now.Add(time.Minute)
	if _, err := broker.Material(context.Background(), materialRequest(expired)); !errors.Is(err, secretstore.ErrLeaseExpired) {
		t.Fatalf("expired material returned %v", err)
	}
	assertLeaseErased(t, broker, expired.ID)
	metadata, err := broker.PublicLease(context.Background(), expired.ID)
	if err != nil {
		t.Fatalf("public expired lease: %v", err)
	}
	if !metadata.Revoked {
		t.Fatal("expired lease metadata was not marked revoked")
	}

	current = now
	revoked := resolveTestLease(t, broker, ref, time.Minute)
	if err := broker.Revoke(context.Background(), revoked.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := broker.Revoke(context.Background(), revoked.ID); err != nil {
		t.Fatalf("idempotent revoke: %v", err)
	}
	if _, err := broker.Material(context.Background(), materialRequest(revoked)); !errors.Is(err, secretstore.ErrLeaseRevoked) {
		t.Fatalf("revoked material returned %v", err)
	}
	assertLeaseErased(t, broker, revoked.ID)
}

// Threat ID: TM-SECRET-002
func TestPublicMetadataNeverSerializesPlaintext(t *testing.T) {
	now := time.Date(2026, 9, 19, 2, 0, 0, 0, time.UTC)
	broker := newTestBroker(t, func() time.Time { return now })
	const canary = "never-serialize-this-canary"
	ref, err := broker.Put(PutRequest{
		TenantID: "tenant", Value: []byte(canary),
		Destinations: []string{"tool:fetch"}, Purposes: []string{"authorization"},
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	lease := resolveTestLease(t, broker, ref, time.Minute)
	metadata, err := broker.PublicLease(context.Background(), lease.ID)
	if err != nil {
		t.Fatalf("public lease: %v", err)
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if strings.Contains(string(data), canary) || strings.Contains(string(data), "material") {
		t.Fatalf("public metadata leaked material: %s", data)
	}
}

func TestPutAndContextValidation(t *testing.T) {
	broker := New()
	for name, request := range map[string]PutRequest{
		"tenant":       {Value: []byte("value"), Destinations: []string{"tool"}, Purposes: []string{"auth"}},
		"value":        {TenantID: "tenant", Destinations: []string{"tool"}, Purposes: []string{"auth"}},
		"destinations": {TenantID: "tenant", Value: []byte("value"), Purposes: []string{"auth"}},
		"purposes":     {TenantID: "tenant", Value: []byte("value"), Destinations: []string{"tool"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := broker.Put(request); !errors.Is(err, secretstore.ErrInvalidRequest) {
				t.Fatalf("invalid put returned %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := broker.Resolve(ctx, secretstore.SecretRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled resolve returned %v", err)
	}
	if _, err := broker.Material(ctx, secretstore.MaterialRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled material returned %v", err)
	}
	if err := broker.Revoke(ctx, "lease:1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled revoke returned %v", err)
	}
}

func newTestBroker(t *testing.T, now func() time.Time) *Broker {
	t.Helper()
	broker, err := NewWithClock(now, 5*time.Minute)
	if err != nil {
		t.Fatalf("new broker: %v", err)
	}
	return broker
}

func resolveTestLease(t *testing.T, broker *Broker, ref secretstore.SecretRef, ttl time.Duration) secretstore.SecretLease {
	t.Helper()
	lease, err := broker.Resolve(context.Background(), secretstore.SecretRequest{
		Ref: ref, TenantID: "tenant", SessionID: "session", EffectID: "effect",
		Destination: "tool:fetch", Purpose: "authorization", TTL: ttl,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return lease
}

func materialRequest(lease secretstore.SecretLease) secretstore.MaterialRequest {
	return secretstore.MaterialRequest{
		LeaseID: lease.ID, TenantID: lease.TenantID, SessionID: lease.SessionID, EffectID: lease.EffectID,
		Destination: lease.Destination, Purpose: lease.Purpose,
	}
}

func assertLeaseErased(t *testing.T, broker *Broker, leaseID string) {
	t.Helper()
	broker.mu.Lock()
	defer broker.mu.Unlock()
	stored := broker.leases[leaseID]
	if len(stored.material) != 0 {
		t.Fatalf("lease %s retained %d plaintext bytes", leaseID, len(stored.material))
	}
}
