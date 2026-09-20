package auth

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adro-project/adro/core/identity"
)

func testServiceAuthority(t *testing.T, clock *time.Time) *ServiceCredentialAuthority {
	t.Helper()
	state, err := GenerateServiceCredentialState("key-1", *clock)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := NewServiceCredentialAuthority(state, func() time.Time { return *clock }, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func TestServiceCredentialBindsScopeAudienceAndExpiry(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	authority := testServiceAuthority(t, &now)
	token, issued, err := authority.Issue(ServiceTokenIssueRequest{
		Type: identity.ActorWorker, ID: "worker-1", TenantID: "tenant-1", WorkspaceID: "workspace-1",
		Audience: ServiceTokenAudienceAPI, TTL: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := authority.Verify(token, ServiceTokenAudienceAPI)
	if err != nil || !reflect.DeepEqual(verified, issued) {
		t.Fatalf("verified=%+v issued=%+v err=%v", verified, issued, err)
	}
	if _, err := authority.Verify(token, "other-api"); !errors.Is(err, ErrServiceTokenInvalid) {
		t.Fatalf("wrong audience returned %v", err)
	}
	now = issued.ExpiresAt
	if _, err := authority.Verify(token, ServiceTokenAudienceAPI); !errors.Is(err, ErrServiceTokenExpired) {
		t.Fatalf("expired token returned %v", err)
	}
}

func TestServiceCredentialRotationAndRevocation(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	authority := testServiceAuthority(t, &now)
	oldToken, oldActor, err := authority.Issue(ServiceTokenIssueRequest{Type: identity.ActorService, ID: "scheduler", TenantID: "tenant", WorkspaceID: "workspace", Audience: ServiceTokenAudienceAPI, TTL: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err := authority.Rotate("key-1", "key-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.Verify(oldToken, ServiceTokenAudienceAPI); err != nil {
		t.Fatalf("retired key rejected outstanding token: %v", err)
	}
	newToken, _, err := authority.Issue(ServiceTokenIssueRequest{Type: identity.ActorService, ID: "scheduler", TenantID: "tenant", WorkspaceID: "workspace", Audience: ServiceTokenAudienceAPI, TTL: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.RevokeCredential("  " + oldActor.CredentialID + "  "); err != nil {
		t.Fatal(err)
	}
	if err := authority.RevokeCredential(oldActor.CredentialID); err != nil {
		t.Fatalf("credential revocation was not idempotent: %v", err)
	}
	if _, err := authority.Verify(oldToken, ServiceTokenAudienceAPI); !errors.Is(err, ErrServiceTokenRevoked) {
		t.Fatalf("revoked credential returned %v", err)
	}
	if err := authority.RevokeKey("key-1"); err != nil {
		t.Fatal(err)
	}
	revokedAt := authority.Snapshot().Keys[0].RevokedAt
	if err := authority.RevokeKey("key-1"); err != nil || authority.Snapshot().Keys[0].RevokedAt != revokedAt {
		t.Fatalf("key revocation was not idempotent: err=%v", err)
	}
	if _, err := authority.Verify(newToken, ServiceTokenAudienceAPI); err != nil {
		t.Fatalf("new key token failed: %v", err)
	}
	if err := authority.RevokeKey("key-2"); !errors.Is(err, ErrServiceKeyUnavailable) {
		t.Fatalf("active key revocation returned %v", err)
	}
}

func TestServiceCredentialRejectsTamperingAndHumanTokens(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	authority := testServiceAuthority(t, &now)
	if _, _, err := authority.Issue(ServiceTokenIssueRequest{Type: identity.ActorHuman, ID: "human", TenantID: "tenant", WorkspaceID: "workspace", Audience: ServiceTokenAudienceAPI, TTL: time.Minute}); !errors.Is(err, ErrServiceTokenInvalid) {
		t.Fatalf("human service credential returned %v", err)
	}
	token, _, err := authority.Issue(ServiceTokenIssueRequest{Type: identity.ActorExtension, ID: "extension", TenantID: "tenant", WorkspaceID: "workspace", Audience: ServiceTokenAudienceAPI, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	parts[1] = parts[1][:len(parts[1])-1] + "A"
	if _, err := authority.Verify(strings.Join(parts, "."), ServiceTokenAudienceAPI); !errors.Is(err, ErrServiceTokenInvalid) {
		t.Fatalf("tampered token returned %v", err)
	}
}

func TestServiceCredentialStateRequiresPrivateFileModeAndRoundTrips(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	authority := testServiceAuthority(t, &now)
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := authority.Save(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	reloaded, err := LoadServiceCredentialAuthority(path, func() time.Time { return now }, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := reloaded.Issue(ServiceTokenIssueRequest{Type: identity.ActorService, ID: "service", TenantID: "tenant", WorkspaceID: "workspace", Audience: ServiceTokenAudienceAPI, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.Verify(token, ServiceTokenAudienceAPI); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadServiceCredentialAuthority(path, func() time.Time { return now }, 10*time.Minute); !errors.Is(err, ErrServiceCredentialFileMode) {
		t.Fatalf("unsafe mode returned %v", err)
	}
}
