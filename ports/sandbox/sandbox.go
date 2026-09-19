// Package sandbox defines the narrow execution boundary used by untrusted
// tools and extensions. Implementations negotiate enforcement before starting
// a command and must never silently downgrade a request.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/adro-project/adro/ports/secretstore"
)

type EnforcementLevel string

const (
	EnforcementNone       EnforcementLevel = "none"
	EnforcementProcess    EnforcementLevel = "process"
	EnforcementFilesystem EnforcementLevel = "filesystem"
	EnforcementNetwork    EnforcementLevel = "network"
	EnforcementContainer  EnforcementLevel = "container"
	EnforcementMicroVM    EnforcementLevel = "microvm"
	EnforcementRemote     EnforcementLevel = "remote"
)

var enforcementOrder = map[EnforcementLevel]int{
	EnforcementNone:       0,
	EnforcementProcess:    1,
	EnforcementFilesystem: 2,
	EnforcementNetwork:    3,
	EnforcementContainer:  4,
	EnforcementMicroVM:    5,
	EnforcementRemote:     6,
}

var (
	ErrInvalidRequest         = errors.New("invalid sandbox request")
	ErrUnsupportedEnforcement = errors.New("sandbox enforcement level is unavailable")
	ErrBackendUnavailable     = errors.New("sandbox backend is unavailable")
	ErrPathEscape             = errors.New("sandbox path escapes workspace root")
	ErrPathSymlink            = errors.New("sandbox path uses an unsafe symlink")
	ErrNetworkDenied          = errors.New("sandbox network access is denied")
	ErrPermissionDenied       = errors.New("sandbox capability grant is insufficient")
	ErrInvalidHandle          = errors.New("sandbox handle is invalid")
	ErrHandleExpired          = errors.New("sandbox handle has expired")
	ErrOutputLimit            = errors.New("sandbox output limit exceeded")
	ErrExecutionTimeout       = errors.New("sandbox execution timed out")
	ErrExecutionCancelled     = errors.New("sandbox execution cancelled")
)

func (l EnforcementLevel) Valid() bool {
	_, ok := enforcementOrder[l]
	return ok
}

// AtLeast reports whether actual satisfies a cumulative requested minimum.
func AtLeast(actual, required EnforcementLevel) bool {
	a, aOK := enforcementOrder[actual]
	r, rOK := enforcementOrder[required]
	return aOK && rOK && a >= r
}

type FileGrant struct {
	Path    string `json:"path"`
	Read    bool   `json:"read,omitempty"`
	Write   bool   `json:"write,omitempty"`
	Create  bool   `json:"create,omitempty"`
	Delete  bool   `json:"delete,omitempty"`
	Execute bool   `json:"execute,omitempty"`
}

func (g FileGrant) HasOperation() bool {
	return g.Read || g.Write || g.Create || g.Delete || g.Execute
}

type NetworkGrant struct {
	Domain    string    `json:"domain,omitempty"`
	IP        string    `json:"ip,omitempty"`
	CIDR      string    `json:"cidr,omitempty"`
	Ports     []int     `json:"ports"`
	Protocol  string    `json:"protocol"`
	Purpose   string    `json:"purpose"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (g NetworkGrant) Validate(now time.Time) error {
	selectors := 0
	if strings.TrimSpace(g.Domain) != "" {
		selectors++
		if !validDomain(g.Domain) {
			return fmt.Errorf("%w: invalid network domain", ErrInvalidRequest)
		}
	}
	if strings.TrimSpace(g.IP) != "" {
		selectors++
		if net.ParseIP(strings.TrimSpace(g.IP)) == nil {
			return fmt.Errorf("%w: invalid network IP", ErrInvalidRequest)
		}
	}
	if strings.TrimSpace(g.CIDR) != "" {
		selectors++
		if _, _, err := net.ParseCIDR(strings.TrimSpace(g.CIDR)); err != nil {
			return fmt.Errorf("%w: invalid network CIDR", ErrInvalidRequest)
		}
	}
	if selectors != 1 {
		return fmt.Errorf("%w: network grant requires exactly one domain, IP, or CIDR", ErrInvalidRequest)
	}
	protocol := strings.ToLower(strings.TrimSpace(g.Protocol))
	switch protocol {
	case "tcp", "udp", "http", "https":
	default:
		return fmt.Errorf("%w: unsupported network protocol %q", ErrInvalidRequest, g.Protocol)
	}
	if len(g.Ports) == 0 {
		return fmt.Errorf("%w: network grant needs at least one port", ErrInvalidRequest)
	}
	seen := make(map[int]struct{}, len(g.Ports))
	for _, port := range g.Ports {
		if port < 1 || port > 65535 {
			return fmt.Errorf("%w: network port %d is outside 1..65535", ErrInvalidRequest, port)
		}
		if _, duplicate := seen[port]; duplicate {
			return fmt.Errorf("%w: duplicate network port %d", ErrInvalidRequest, port)
		}
		seen[port] = struct{}{}
	}
	if g.ExpiresAt.IsZero() || !g.ExpiresAt.After(now) {
		return fmt.Errorf("%w: network grant is expired", ErrNetworkDenied)
	}
	if strings.TrimSpace(g.Purpose) == "" {
		return fmt.Errorf("%w: network purpose is required", ErrInvalidRequest)
	}
	return nil
}

func validDomain(value string) bool {
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if value == "" || len(value) > 253 || strings.ContainsAny(value, " /:@\\\t\r\n") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
				return false
			}
		}
	}
	return true
}

// ResourceBudget is finite. Zero means the backend default for optional
// limits. A backend must reject non-zero limits it cannot enforce.
type ResourceBudget struct {
	WallTimeout    time.Duration `json:"wall_timeout,omitempty"`
	CPUTime        time.Duration `json:"cpu_time,omitempty"`
	MemoryBytes    int64         `json:"memory_bytes,omitempty"`
	DiskBytes      int64         `json:"disk_bytes,omitempty"`
	MaxOutputBytes int64         `json:"max_output_bytes,omitempty"`
}

func (b ResourceBudget) Validate() error {
	if b.WallTimeout < 0 || b.CPUTime < 0 || b.MemoryBytes < 0 || b.DiskBytes < 0 || b.MaxOutputBytes < 0 {
		return fmt.Errorf("%w: resource limits cannot be negative", ErrInvalidRequest)
	}
	return nil
}

type SandboxRequest struct {
	TenantID      string                  `json:"tenant_id"`
	SessionID     string                  `json:"session_id"`
	EffectID      string                  `json:"effect_id"`
	RequiredLevel EnforcementLevel        `json:"required_level"`
	Command       []string                `json:"command"`
	WorkingDir    string                  `json:"working_dir"`
	FileGrants    []FileGrant             `json:"file_grants,omitempty"`
	NetworkGrants []NetworkGrant          `json:"network_grants,omitempty"`
	SecretRefs    []secretstore.SecretRef `json:"secret_refs,omitempty"`
	Limits        ResourceBudget          `json:"limits"`
	Environment   map[string]string       `json:"environment,omitempty"`
}

func (r SandboxRequest) Validate(now time.Time) error {
	if strings.TrimSpace(r.TenantID) == "" || strings.TrimSpace(r.SessionID) == "" || strings.TrimSpace(r.EffectID) == "" {
		return fmt.Errorf("%w: tenant, session, and effect are required", ErrInvalidRequest)
	}
	if !r.RequiredLevel.Valid() {
		return fmt.Errorf("%w: unknown required enforcement level %q", ErrInvalidRequest, r.RequiredLevel)
	}
	if len(r.Command) == 0 || strings.TrimSpace(r.Command[0]) == "" {
		return fmt.Errorf("%w: argv command is required", ErrInvalidRequest)
	}
	for _, arg := range r.Command {
		if strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("%w: command arguments cannot contain NUL", ErrInvalidRequest)
		}
	}
	if strings.TrimSpace(r.WorkingDir) == "" {
		return fmt.Errorf("%w: working directory is required", ErrInvalidRequest)
	}
	if err := r.Limits.Validate(); err != nil {
		return err
	}
	for _, grant := range r.FileGrants {
		if strings.TrimSpace(grant.Path) == "" || !grant.HasOperation() {
			return fmt.Errorf("%w: file grant needs a path and operation", ErrInvalidRequest)
		}
	}
	for _, grant := range r.NetworkGrants {
		if err := grant.Validate(now); err != nil {
			return err
		}
	}
	for _, ref := range r.SecretRefs {
		if !ref.Valid() {
			return fmt.Errorf("%w: secret reference is invalid", ErrInvalidRequest)
		}
	}
	for key, value := range r.Environment {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "=\x00\r\n") || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("%w: invalid environment entry", ErrInvalidRequest)
		}
	}
	return nil
}

type SandboxCapabilities struct {
	Backend                 string             `json:"backend"`
	Level                   EnforcementLevel   `json:"level"`
	SupportedLevels         []EnforcementLevel `json:"supported_levels"`
	SecureTenantIsolation   bool               `json:"secure_tenant_isolation"`
	NetworkEnforcement      bool               `json:"network_enforcement"`
	FilesystemEnforcement   bool               `json:"filesystem_enforcement"`
	ProcessTreeCancellation bool               `json:"process_tree_cancellation"`
	WallTimeoutEnforcement  bool               `json:"wall_timeout_enforcement"`
	OutputLimitEnforcement  bool               `json:"output_limit_enforcement"`
	CPUEnforcement          bool               `json:"cpu_enforcement"`
	MemoryEnforcement       bool               `json:"memory_enforcement"`
	DiskEnforcement         bool               `json:"disk_enforcement"`
	SecretInjection         bool               `json:"secret_injection"`
	MaxOutputBytes          int64              `json:"max_output_bytes"`
	Limitations             []string           `json:"limitations,omitempty"`
}

func (c SandboxCapabilities) Validate() error {
	if strings.TrimSpace(c.Backend) == "" || !c.Level.Valid() || len(c.SupportedLevels) == 0 {
		return fmt.Errorf("%w: backend, level, and supported levels are required", ErrInvalidRequest)
	}
	seen := make(map[EnforcementLevel]struct{}, len(c.SupportedLevels))
	for _, level := range c.SupportedLevels {
		if !level.Valid() || !AtLeast(c.Level, level) {
			return fmt.Errorf("%w: inconsistent supported enforcement level %q", ErrInvalidRequest, level)
		}
		if _, duplicate := seen[level]; duplicate {
			return fmt.Errorf("%w: duplicate supported enforcement level %q", ErrInvalidRequest, level)
		}
		seen[level] = struct{}{}
	}
	if c.FilesystemEnforcement && !AtLeast(c.Level, EnforcementFilesystem) {
		return fmt.Errorf("%w: filesystem enforcement exceeds backend level", ErrInvalidRequest)
	}
	if c.NetworkEnforcement && !AtLeast(c.Level, EnforcementNetwork) {
		return fmt.Errorf("%w: network enforcement exceeds backend level", ErrInvalidRequest)
	}
	if c.OutputLimitEnforcement && c.MaxOutputBytes <= 0 {
		return fmt.Errorf("%w: output enforcement requires a positive maximum", ErrInvalidRequest)
	}
	if !c.OutputLimitEnforcement && c.MaxOutputBytes != 0 {
		return fmt.Errorf("%w: output maximum requires output enforcement", ErrInvalidRequest)
	}
	return nil
}

func (c SandboxCapabilities) Supports(required EnforcementLevel) bool {
	if c.Validate() != nil || !required.Valid() {
		return false
	}
	for _, level := range c.SupportedLevels {
		if AtLeast(level, required) {
			return true
		}
	}
	return AtLeast(c.Level, required)
}

type SandboxHandle struct {
	ID            string           `json:"id"`
	TenantID      string           `json:"tenant_id"`
	SessionID     string           `json:"session_id"`
	EffectID      string           `json:"effect_id"`
	Backend       string           `json:"backend"`
	Enforcement   EnforcementLevel `json:"enforcement"`
	RequestDigest string           `json:"request_digest"`
	ExpiresAt     time.Time        `json:"expires_at"`
}

type ExecutionEvent struct {
	Stream string `json:"stream"`
	Data   []byte `json:"data"`
}

type ExecutionResult struct {
	ExitCode      int       `json:"exit_code"`
	Stdout        []byte    `json:"stdout,omitempty"`
	Stderr        []byte    `json:"stderr,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	TimedOut      bool      `json:"timed_out,omitempty"`
	Cancelled     bool      `json:"cancelled,omitempty"`
	OutputLimit   bool      `json:"output_limit,omitempty"`
	DroppedEvents int64     `json:"dropped_events,omitempty"`
}

type ExecutionStream interface {
	Events() <-chan ExecutionEvent
	Errors() <-chan error
	Wait(context.Context) (ExecutionResult, error)
	Close() error
}

type SandboxBroker interface {
	Capabilities(context.Context) (SandboxCapabilities, error)
	Prepare(context.Context, SandboxRequest) (SandboxHandle, error)
	Execute(context.Context, SandboxHandle) (ExecutionStream, error)
	Cancel(context.Context, SandboxHandle) error
	Dispose(context.Context, SandboxHandle) error
}
