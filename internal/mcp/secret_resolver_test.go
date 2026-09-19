package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	secretmemory "github.com/adro-project/adro/adapters/secret/memory"
)

func TestBrokerSecretResolverBindsScopeAndRevokesLease(t *testing.T) {
	broker := secretmemory.New()
	ref, err := broker.Put(secretmemory.PutRequest{TenantID: "tenant-1", Value: []byte("token-value"), Destinations: []string{"mcp:tools"}, Purposes: []string{"tool-call"}})
	if err != nil {
		t.Fatal(err)
	}
	resolver := BrokerSecretResolver{Broker: broker, TenantID: "tenant-1", SessionID: "session-1", EffectID: "effect-1", Destination: "mcp:tools", Purpose: "tool-call", TTL: time.Minute}
	value, err := resolver.Resolve(context.Background(), ref.String())
	if err != nil || value != "token-value" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	if _, err := resolver.Resolve(context.Background(), "plaintext-token"); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("invalid ref err=%v", err)
	}
	resolver.Destination = "mcp:other"
	if _, err := resolver.Resolve(context.Background(), ref.String()); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("scope mismatch err=%v", err)
	}
}
