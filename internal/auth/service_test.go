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
