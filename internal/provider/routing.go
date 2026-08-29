package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/adro-project/adro/internal/domain"
)

const (
	maxAgentRouteConfigBytes = 64 << 10
	maxAgentRoutes           = 1000
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// AgentRouteConfig is the immutable, workspace-scoped routing snapshot read
// at process startup. The native IDs are never returned by diagnostics or the
// browser workbench; they are used only while materializing a provider issue.
type AgentRouteConfig struct {
	Workspaces map[string]WorkspaceAgentRoutes `json:"workspaces"`
}

type WorkspaceAgentRoutes struct {
	DefaultAgentID string            `json:"default_agent_id,omitempty"`
	Members        map[string]string `json:"members,omitempty"`
	Roles          map[string]string `json:"roles,omitempty"`
}

// RouteDecision is the provider-neutral result of resolving one work item.
// ProviderAssigneeID is intentionally kept out of domain JSON responses.
type RouteDecision struct {
	Binding            domain.ProviderBinding
	ProviderAssigneeID string
	AssigneeType       string
	Source             string
	ConfigRevision     string
}

type RouteDiagnostics struct {
	Configured             bool
	DefaultAgentConfigured bool
	MemberRouteCount       int
	RoleRouteCount         int
	ConfigRevision         string
}

// ParseAgentRouteConfig validates the complete environment value. Unknown
// fields, trailing JSON values, invalid UUIDs, empty keys, normalized role
// collisions and oversized route tables are rejected before the server starts.
func ParseAgentRouteConfig(raw string) (AgentRouteConfig, error) {
	if strings.TrimSpace(raw) == "" {
		return AgentRouteConfig{}, nil
	}
	if len(raw) > maxAgentRouteConfigBytes {
		return AgentRouteConfig{}, errors.New("agent route configuration exceeds 64 KiB")
	}
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return AgentRouteConfig{}, errors.New("invalid agent route configuration")
	}
	if err := validateUniqueJSONObjectKeys(raw); err != nil {
		return AgentRouteConfig{}, errors.New("invalid agent route configuration")
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	var config AgentRouteConfig
	if err := dec.Decode(&config); err != nil {
		return AgentRouteConfig{}, errors.New("invalid agent route configuration")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return AgentRouteConfig{}, errors.New("agent route configuration must contain one JSON value")
	}
	if config.Workspaces == nil {
		config.Workspaces = map[string]WorkspaceAgentRoutes{}
	}
	normalizedWorkspaces := make(map[string]WorkspaceAgentRoutes, len(config.Workspaces))
	routeCount := 0
	for workspaceID, routes := range config.Workspaces {
		workspaceID = strings.ToLower(strings.TrimSpace(workspaceID))
		if !uuidPattern.MatchString(workspaceID) {
			return AgentRouteConfig{}, errors.New("agent route workspace key must be a UUID")
		}
		if _, exists := normalizedWorkspaces[workspaceID]; exists {
			return AgentRouteConfig{}, errors.New("agent route workspaces contain a normalized duplicate")
		}
		if routes.DefaultAgentID != "" {
			if !uuidPattern.MatchString(routes.DefaultAgentID) {
				return AgentRouteConfig{}, errors.New("default agent route must be a UUID")
			}
			routes.DefaultAgentID = canonicalUUID(routes.DefaultAgentID)
			routeCount++
		}
		members := map[string]string{}
		seenMembers := map[string]struct{}{}
		for memberID, agentID := range routes.Members {
			memberID = strings.TrimSpace(memberID)
			if memberID == "" || !uuidPattern.MatchString(agentID) {
				return AgentRouteConfig{}, errors.New("member agent route has an empty key or invalid UUID")
			}
			if _, exists := seenMembers[memberID]; exists {
				return AgentRouteConfig{}, errors.New("member agent routes contain a normalized duplicate")
			}
			seenMembers[memberID] = struct{}{}
			members[memberID] = canonicalUUID(agentID)
			routeCount++
		}
		seenRoles := map[string]struct{}{}
		roles := map[string]string{}
		for role, agentID := range routes.Roles {
			normalized := strings.ToLower(strings.TrimSpace(role))
			if normalized == "" || !uuidPattern.MatchString(agentID) {
				return AgentRouteConfig{}, errors.New("role agent route has an empty key or invalid UUID")
			}
			if _, exists := seenRoles[normalized]; exists {
				return AgentRouteConfig{}, errors.New("role agent routes contain a normalized duplicate")
			}
			seenRoles[normalized] = struct{}{}
			roles[normalized] = canonicalUUID(agentID)
			routeCount++
		}
		routes.Members = members
		routes.Roles = roles
		normalizedWorkspaces[workspaceID] = routes
	}
	config.Workspaces = normalizedWorkspaces
	if routeCount > maxAgentRoutes {
		return AgentRouteConfig{}, errors.New("agent route configuration exceeds 1000 routes")
	}
	return config, nil
}

// validateUniqueJSONObjectKeys walks the JSON token stream before decoding it
// into maps. encoding/json intentionally keeps the last value for duplicate
// object keys, which would make a route depend on key order rather than fail at
// startup.
func validateUniqueJSONObjectKeys(raw string) error {
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	if err := scanJSONValue(dec); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return errors.New("agent route configuration must contain one JSON value")
		}
		return err
	}
	return nil
}

func scanJSONValue(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	return scanJSONToken(dec, token)
}

func scanJSONToken(dec *json.Decoder, token json.Token) error {
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for {
			token, err := dec.Token()
			if err != nil {
				return err
			}
			if closing, ok := token.(json.Delim); ok && closing == '}' {
				return nil
			}
			key, ok := token.(string)
			if !ok {
				return errors.New("invalid JSON object key")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate JSON object key")
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(dec); err != nil {
				return err
			}
		}
	case '[':
		for {
			token, err := dec.Token()
			if err != nil {
				return err
			}
			if closing, ok := token.(json.Delim); ok {
				if closing == ']' {
					return nil
				}
				if closing == '}' {
					return errors.New("invalid JSON array")
				}
			}
			if err := scanJSONToken(dec, token); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
}

type AgentRouteResolver struct {
	config   AgentRouteConfig
	legacyID string
	revision string
}

func NewAgentRouteResolver(config AgentRouteConfig, legacyID string) *AgentRouteResolver {
	canonical, _ := json.Marshal(config)
	hash := sha256.Sum256(canonical)
	var snapshot AgentRouteConfig
	_ = json.Unmarshal(canonical, &snapshot)
	if snapshot.Workspaces == nil {
		snapshot.Workspaces = map[string]WorkspaceAgentRoutes{}
	}
	return &AgentRouteResolver{config: snapshot, legacyID: strings.TrimSpace(legacyID), revision: "cfg-" + hex.EncodeToString(hash[:8])}
}

func NewAgentRouteResolverFromEnv() (*AgentRouteResolver, error) {
	config, err := ParseAgentRouteConfig(strings.TrimSpace(getenv("ADRO_MULTICA_AGENT_MAP")))
	if err != nil {
		return nil, err
	}
	legacyID := getenv("ADRO_MULTICA_AGENT_ID")
	if legacyID != "" && !uuidPattern.MatchString(legacyID) {
		return nil, errors.New("ADRO_MULTICA_AGENT_ID must be a UUID")
	}
	return NewAgentRouteResolver(config, canonicalUUID(legacyID)), nil
}

// getenv is a variable to keep this package straightforward to test without
// mutating process-global environment in parser tests.
var getenv = func(key string) string { return strings.TrimSpace(os.Getenv(key)) }

func (r *AgentRouteResolver) Revision() string { return r.revision }

func (r *AgentRouteResolver) Resolve(workspaceID, memberID, role string, profileBinding *domain.ProviderBinding) RouteDecision {
	if profileBinding != nil && profileBinding.ID != "" && profileBinding.ProviderObjectID != "" &&
		profileBinding.Provider == "multica" && profileBinding.Kind == "agent" &&
		strings.EqualFold(strings.TrimSpace(profileBinding.WorkspaceID), strings.TrimSpace(workspaceID)) {
		return decisionFromBinding(*profileBinding, "profile")
	}
	routes, ok := r.config.Workspaces[strings.ToLower(strings.TrimSpace(workspaceID))]
	if ok {
		if agentID, exists := routes.Members[strings.TrimSpace(memberID)]; exists {
			return r.newDecision(workspaceID, agentID, "member")
		}
		normalizedRole := strings.ToLower(strings.TrimSpace(role))
		if agentID, exists := routes.Roles[normalizedRole]; exists {
			return r.newDecision(workspaceID, agentID, "role")
		}
		if routes.DefaultAgentID != "" {
			return r.newDecision(workspaceID, routes.DefaultAgentID, "workspace_default")
		}
	}
	if r.legacyID != "" {
		return r.newDecision(workspaceID, r.legacyID, "legacy_default")
	}
	return RouteDecision{Source: "unassigned", ConfigRevision: r.revision}
}

func (r *AgentRouteResolver) Diagnostics(workspaceID string) RouteDiagnostics {
	result := RouteDiagnostics{ConfigRevision: r.revision}
	routes, ok := r.config.Workspaces[strings.ToLower(strings.TrimSpace(workspaceID))]
	if !ok {
		result.DefaultAgentConfigured = r.legacyID != ""
		result.Configured = result.DefaultAgentConfigured
		return result
	}
	result.DefaultAgentConfigured = routes.DefaultAgentID != "" || r.legacyID != ""
	result.MemberRouteCount = len(routes.Members)
	result.RoleRouteCount = len(routes.Roles)
	result.Configured = result.DefaultAgentConfigured || result.MemberRouteCount > 0 || result.RoleRouteCount > 0
	return result
}

func (r *AgentRouteResolver) newDecision(workspaceID, nativeID, source string) RouteDecision {
	binding := NewProviderBinding("multica", workspaceID, "agent", nativeID, "configured", source, r.revision)
	return decisionFromBinding(binding, source)
}

func decisionFromBinding(binding domain.ProviderBinding, source string) RouteDecision {
	if source == "" {
		source = binding.Source
	}
	return RouteDecision{Binding: binding, ProviderAssigneeID: binding.ProviderObjectID, AssigneeType: "agent", Source: source, ConfigRevision: binding.ConfigRevision}
}

func NewProviderBinding(providerName, workspaceID, kind, nativeID, status, source, revision string) domain.ProviderBinding {
	providerName = strings.TrimSpace(providerName)
	workspaceID = canonicalUUID(workspaceID)
	kind = strings.TrimSpace(kind)
	nativeID = canonicalUUID(nativeID)
	canonical := strings.Join([]string{providerName, workspaceID, kind, nativeID}, "\x00")
	hash := sha256.Sum256([]byte(canonical))
	return domain.ProviderBinding{ID: "pb-" + hex.EncodeToString(hash[:16]), WorkspaceID: workspaceID, Provider: providerName, Kind: kind, ProviderObjectID: nativeID, Status: status, Source: source, ConfigRevision: revision}
}

func canonicalUUID(value string) string {
	value = strings.TrimSpace(value)
	if uuidPattern.MatchString(value) {
		return strings.ToLower(value)
	}
	return value
}

func (r *AgentRouteResolver) Validate() error {
	if r == nil {
		return fmt.Errorf("agent route resolver is nil")
	}
	return nil
}
