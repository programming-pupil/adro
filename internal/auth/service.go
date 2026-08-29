// Package auth implements the local identity and menu-authorization profile.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/internal/domain"
)

const (
	passwordIterations = 210_000
	sessionLifetime    = 12 * time.Hour
)

var AllMenus = []string{
	"workbench", "requirements", "bugs", "humanQA", "designReview", "executions",
	"diffs", "testing", "repositories", "agents", "mcp", "skills", "automations",
	"integrations", "artifacts", "runners", "cost", "admin",
}

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrLocked             = errors.New("login temporarily locked")
	ErrNotFound           = errors.New("user not found")
	ErrLastAdmin          = errors.New("at least one active administrator is required")
)

type User struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	MenuIDs     []string  `json:"menu_ids"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Password    string    `json:"password,omitempty"`
	PasswordPHC string    `json:"password_phc,omitempty"`
}

type DirectoryEntry struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

type Session struct {
	User      User      `json:"user"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type sessionRecord struct {
	UserID    string
	ExpiresAt time.Time
}

type failures struct {
	Count       int
	WindowStart time.Time
	LockedUntil time.Time
}

type persistedState struct {
	Version int    `json:"version"`
	Users   []User `json:"users"`
}

type Service struct {
	mu       sync.RWMutex
	path     string
	users    map[string]User
	sessions map[string]sessionRecord
	failures map[string]failures
	now      func() time.Time
}

func NewService(path, adminUsername, adminPassword string) (*Service, error) {
	s := &Service{path: path, users: map[string]User{}, sessions: map[string]sessionRecord{}, failures: map[string]failures{}, now: func() time.Time { return time.Now().UTC() }}
	if path != "" {
		if err := s.load(); err != nil {
			return nil, err
		}
	}
	if len(s.users) == 0 && strings.TrimSpace(adminPassword) != "" {
		if strings.TrimSpace(adminUsername) == "" {
			adminUsername = "admin"
		}
		if _, err := s.createLocked(User{WorkspaceID: "local", Username: adminUsername, DisplayName: "ADRO Administrator", Role: "admin", MenuIDs: append([]string(nil), AllMenus...), Status: "active", Password: adminPassword}); err != nil {
			return nil, fmt.Errorf("seed administrator: %w", err)
		}
	}
	return s, nil
}

func (s *Service) Authenticate(username, password string) (Session, error) {
	username = normalizeUsername(username)
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.failures[username]
	if now.Before(f.LockedUntil) {
		return Session{}, ErrLocked
	}
	user, ok := s.userByUsernameLocked(username)
	if !ok || user.Status != "active" || !verifyPassword(user.PasswordPHC, password) {
		if f.WindowStart.IsZero() || now.Sub(f.WindowStart) > 10*time.Minute {
			f = failures{WindowStart: now}
		}
		f.Count++
		if f.Count >= 5 {
			f.LockedUntil = now.Add(5 * time.Minute)
		}
		s.failures[username] = f
		return Session{}, ErrInvalidCredentials
	}
	delete(s.failures, username)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return Session{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	expires := now.Add(sessionLifetime)
	s.sessions[hashToken(token)] = sessionRecord{UserID: user.ID, ExpiresAt: expires}
	return Session{User: publicUser(user), Token: token, ExpiresAt: expires}, nil
}

func (s *Service) AuthenticateToken(token string) (User, bool) {
	if strings.TrimSpace(token) == "" {
		return User{}, false
	}
	now := s.now()
	hash := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.sessions[hash]
	if !ok || now.After(record.ExpiresAt) {
		delete(s.sessions, hash)
		return User{}, false
	}
	user, ok := s.users[record.UserID]
	if !ok || user.Status != "active" {
		delete(s.sessions, hash)
		return User{}, false
	}
	return publicUser(user), true
}

func (s *Service) Logout(token string) {
	s.mu.Lock()
	delete(s.sessions, hashToken(token))
	s.mu.Unlock()
}

func (s *Service) ListUsers(workspaceID string) []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]User, 0, len(s.users))
	for _, user := range s.users {
		if workspaceID == "" || user.WorkspaceID == workspaceID {
			items = append(items, publicUser(user))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Username < items[j].Username })
	return items
}

func (s *Service) Directory(workspaceID string) []DirectoryEntry {
	users := s.ListUsers(workspaceID)
	items := make([]DirectoryEntry, 0, len(users))
	for _, user := range users {
		if user.Status == "active" {
			items = append(items, DirectoryEntry{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Role: user.Role})
		}
	}
	return items
}

func (s *Service) CreateUser(input User) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createLocked(input)
}

func (s *Service) createLocked(input User) (User, error) {
	input.Username = normalizeUsername(input.Username)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Role = strings.ToLower(strings.TrimSpace(input.Role))
	if input.WorkspaceID == "" {
		input.WorkspaceID = "local"
	}
	if input.Status == "" {
		input.Status = "active"
	}
	if err := validateUser(input, true); err != nil {
		return User{}, err
	}
	if _, exists := s.userByUsernameLocked(input.Username); exists {
		return User{}, errors.New("username already exists")
	}
	phc, err := hashPassword(input.Password)
	if err != nil {
		return User{}, err
	}
	now := s.now()
	input.ID = domain.NewID()
	input.Password = ""
	input.PasswordPHC = phc
	input.MenuIDs = normalizeMenus(input.Role, input.MenuIDs)
	input.CreatedAt = now
	input.UpdatedAt = now
	s.users[input.ID] = input
	if err := s.persistLocked(); err != nil {
		delete(s.users, input.ID)
		return User{}, err
	}
	return publicUser(input), nil
}

func (s *Service) UpdateUser(id string, patch User) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	previous := current
	if strings.TrimSpace(patch.DisplayName) != "" {
		current.DisplayName = strings.TrimSpace(patch.DisplayName)
	}
	if strings.TrimSpace(patch.Role) != "" {
		current.Role = strings.ToLower(strings.TrimSpace(patch.Role))
	}
	if strings.TrimSpace(patch.Status) != "" {
		current.Status = strings.ToLower(strings.TrimSpace(patch.Status))
	}
	if patch.MenuIDs != nil {
		current.MenuIDs = patch.MenuIDs
	}
	if patch.Password != "" {
		phc, err := hashPassword(patch.Password)
		if err != nil {
			return User{}, err
		}
		current.PasswordPHC = phc
	}
	current.MenuIDs = normalizeMenus(current.Role, current.MenuIDs)
	if err := validateUser(current, false); err != nil {
		return User{}, err
	}
	if previous.Role == "admin" && previous.Status == "active" && (current.Role != "admin" || current.Status != "active") && s.activeAdminCountLocked() == 1 {
		return User{}, ErrLastAdmin
	}
	current.UpdatedAt = s.now()
	s.users[id] = current
	if current.Status != "active" {
		s.revokeUserSessionsLocked(id)
	}
	if err := s.persistLocked(); err != nil {
		s.users[id] = previous
		return User{}, err
	}
	return publicUser(current), nil
}

func (u User) Can(menu string) bool {
	if u.Role == "admin" {
		return true
	}
	for _, item := range u.MenuIDs {
		if item == menu {
			return true
		}
	}
	return false
}

func (s *Service) userByUsernameLocked(username string) (User, bool) {
	for _, user := range s.users {
		if user.Username == username {
			return user, true
		}
	}
	return User{}, false
}

func (s *Service) activeAdminCountLocked() int {
	count := 0
	for _, user := range s.users {
		if user.Role == "admin" && user.Status == "active" {
			count++
		}
	}
	return count
}

func (s *Service) revokeUserSessionsLocked(id string) {
	for token, session := range s.sessions {
		if session.UserID == id {
			delete(s.sessions, token)
		}
	}
}

func (s *Service) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read auth state: %w", err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode auth state: %w", err)
	}
	for _, user := range state.Users {
		if user.ID == "" || user.PasswordPHC == "" {
			return errors.New("auth state contains an invalid user")
		}
		s.users[user.ID] = user
	}
	return nil
}

func (s *Service) persistLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return err
	}
	users := make([]User, 0, len(s.users))
	for _, user := range s.users {
		users = append(users, user)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	data, err := json.MarshalIndent(persistedState{Version: 1, Users: users}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".auth-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

func validateUser(user User, passwordRequired bool) error {
	if len(user.Username) < 3 || len(user.Username) > 64 {
		return errors.New("username must contain 3 to 64 lowercase letters, digits, dots, dashes, or underscores")
	}
	for _, r := range user.Username {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r)) {
			return errors.New("username contains unsupported characters")
		}
	}
	if user.DisplayName == "" || len(user.DisplayName) > 100 {
		return errors.New("display_name is required and must not exceed 100 characters")
	}
	if user.Role != "admin" && user.Role != "member" && user.Role != "viewer" {
		return errors.New("role must be admin, member, or viewer")
	}
	if user.Status != "active" && user.Status != "disabled" {
		return errors.New("status must be active or disabled")
	}
	if passwordRequired && len(user.Password) < 10 {
		return errors.New("password must contain at least 10 characters")
	}
	for _, menu := range user.MenuIDs {
		if !isMenu(menu) {
			return fmt.Errorf("unknown menu %q", menu)
		}
	}
	return nil
}

func normalizeMenus(role string, menus []string) []string {
	if role == "admin" {
		return append([]string(nil), AllMenus...)
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(menus))
	for _, menu := range AllMenus {
		for _, candidate := range menus {
			if candidate == menu && !seen[menu] {
				seen[menu] = true
				result = append(result, menu)
			}
		}
	}
	return result
}

func isMenu(value string) bool {
	for _, menu := range AllMenus {
		if value == menu {
			return true
		}
	}
	return false
}

func normalizeUsername(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func publicUser(user User) User {
	user.Password = ""
	user.PasswordPHC = ""
	user.MenuIDs = append([]string(nil), user.MenuIDs...)
	return user
}

func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func hashPassword(password string) (string, error) {
	if len(password) < 10 {
		return "", errors.New("password must contain at least 10 characters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	digest := pbkdf2SHA256([]byte(password), salt, passwordIterations, 32)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", passwordIterations, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(digest)), nil
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	var iterations int
	if _, err := fmt.Sscanf(parts[1], "%d", &iterations); err != nil || iterations < 100_000 || iterations > 1_000_000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(expected) != 32 {
		return false
	}
	actual := pbkdf2SHA256([]byte(password), salt, iterations, len(expected))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func pbkdf2SHA256(password, salt []byte, iterations, keyLen int) []byte {
	result := make([]byte, 0, keyLen)
	for block := uint32(1); len(result) < keyLen; block++ {
		counter := []byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)}
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write(counter)
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iterations; i++ {
			mac = hmac.New(sha256.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		result = append(result, t...)
	}
	return result[:keyLen]
}
