package mcp

import (
	"context"
	"strings"
	"time"

	"github.com/adro-project/adro/ports/secretstore"
)

// BrokerSecretResolver binds MCP secret material to one durable effect scope.
// The MCP transport receives only the resolved bytes for the lifetime of one
// connection; the reference, lease and scope never enter a JSON-RPC payload.
// A missing scope is rejected rather than treated as a wildcard.
type BrokerSecretResolver struct {
	Broker      secretstore.SecretBroker
	TenantID    string
	SessionID   string
	EffectID    string
	Destination string
	Purpose     string
	TTL         time.Duration
}

func (r BrokerSecretResolver) Resolve(ctx context.Context, reference string) (string, error) {
	ref := secretstore.SecretRef(strings.TrimSpace(reference))
	if r.Broker == nil || !ref.Valid() || strings.TrimSpace(r.TenantID) == "" || strings.TrimSpace(r.SessionID) == "" || strings.TrimSpace(r.EffectID) == "" || strings.TrimSpace(r.Destination) == "" || strings.TrimSpace(r.Purpose) == "" || r.TTL <= 0 {
		return "", ErrSecretUnavailable
	}
	lease, err := r.Broker.Resolve(ctx, secretstore.SecretRequest{
		Ref: ref, TenantID: r.TenantID, SessionID: r.SessionID, EffectID: r.EffectID,
		Destination: r.Destination, Purpose: r.Purpose, TTL: r.TTL,
	})
	if err != nil {
		return "", ErrSecretUnavailable
	}
	material, err := r.Broker.Material(ctx, secretstore.MaterialRequest{
		LeaseID: lease.ID, TenantID: r.TenantID, SessionID: r.SessionID, EffectID: r.EffectID,
		Destination: r.Destination, Purpose: r.Purpose,
	})
	if revokeErr := r.Broker.Revoke(context.Background(), lease.ID); revokeErr != nil && err == nil {
		return "", ErrSecretUnavailable
	}
	if err != nil || len(material) == 0 {
		return "", ErrSecretUnavailable
	}
	value := string(material)
	clear(material)
	return value, nil
}

var _ SecretResolver = BrokerSecretResolver{}
