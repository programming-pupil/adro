// Package scope carries the authenticated tenant boundary across storage
// adapter calls.  Adapters fail closed when a caller omits the scope.
package scope

import (
	"context"
	"errors"
	"strings"
)

var ErrMissingTenant = errors.New("tenant scope is required")

type contextKey struct{}

// WithTenant returns a context carrying the caller's verified tenant ID.
// The value must be non-empty and trimmed; adapters never infer it from a
// stream, blob digest, or caller-controlled path.
func WithTenant(ctx context.Context, tenantID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, contextKey{}, strings.TrimSpace(tenantID))
}

// Tenant extracts the verified tenant boundary from ctx.
func Tenant(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", ErrMissingTenant
	}
	tenantID, ok := ctx.Value(contextKey{}).(string)
	if !ok || strings.TrimSpace(tenantID) == "" {
		return "", ErrMissingTenant
	}
	return tenantID, nil
}
