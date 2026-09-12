package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserLifecyclePersistsPasswordHashAndRevokesSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth", "users.json")
	service, err := NewService(path, "admin", "AdminPass123!")
	if err != nil {
		t.Fatal(err)
	}
	adminSession, err := service.Authenticate("admin", "AdminPass123!")
	if err != nil || adminSession.User.Role != "admin" || len(adminSession.User.MenuIDs) != len(AllMenus) {
		t.Fatalf("administrator login: session=%+v err=%v", adminSession, err)
	}
	member, err := service.CreateUser(User{WorkspaceID: "local", Username: "developer.one", DisplayName: "Developer One", Role: "member", Status: "active", MenuIDs: []string{"requirements", "bugs"}, Password: "Developer123!"})
	if err != nil {
		t.Fatal(err)
	}
	memberSession, err := service.Authenticate(member.Username, "Developer123!")
	if err != nil || !memberSession.User.Can("requirements") || memberSession.User.Can("admin") {
		t.Fatalf("member login: session=%+v err=%v", memberSession, err)
	}
	if _, err := service.UpdateUser(member.ID, User{Status: "disabled"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.AuthenticateToken(memberSession.Token); ok {
		t.Fatal("disabled user's existing session remained active")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "AdminPass123!") || strings.Contains(string(data), "Developer123!") || !strings.Contains(string(data), "pbkdf2-sha256") {
		t.Fatal("persisted identity state did not contain only password derivations")
	}
	reloaded, err := NewService(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(reloaded.ListUsers("local")); got != 2 {
		t.Fatalf("reloaded users=%d", got)
	}
}

func TestCannotDisableLastAdministrator(t *testing.T) {
	service, err := NewService("", "admin", "AdminPass123!")
	if err != nil {
		t.Fatal(err)
	}
	admin := service.ListUsers("local")[0]
	if _, err := service.UpdateUser(admin.ID, User{Status: "disabled"}); err != ErrLastAdmin {
		t.Fatalf("expected ErrLastAdmin, got %v", err)
	}
}

func TestNewServiceRejectsShortInitialAdministratorPassword(t *testing.T) {
	_, err := NewService(filepath.Join(t.TempDir(), "auth.json"), "admin", "111111")
	if err == nil || !strings.Contains(err.Error(), "at least 10 characters") {
		t.Fatalf("expected short initial password error, got %v", err)
	}
}

func TestExistingAuthStateDoesNotChangeWithSeedEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	service, err := NewService(path, "admin", "OriginalPass123!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate("admin", "OriginalPass123!"); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewService(path, "admin", "ReplacementPass123!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.Authenticate("admin", "OriginalPass123!"); err != nil {
		t.Fatalf("existing password was not preserved: %v", err)
	}
	if _, err := reloaded.Authenticate("admin", "ReplacementPass123!"); err != ErrInvalidCredentials {
		t.Fatalf("seed password unexpectedly replaced existing password: %v", err)
	}
}
