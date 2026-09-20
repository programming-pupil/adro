package scope

import (
	"context"
	"errors"
	"testing"
)

func TestTenantScopeFailsClosed(t *testing.T) {
	if _, err := Tenant(context.Background()); !errors.Is(err, ErrMissingTenant) {
		t.Fatalf("missing scope error=%v", err)
	}
	ctx := WithTenant(context.Background(), " tenant-a ")
	if got, err := Tenant(ctx); err != nil || got != "tenant-a" {
		t.Fatalf("tenant=%q err=%v", got, err)
	}
}
