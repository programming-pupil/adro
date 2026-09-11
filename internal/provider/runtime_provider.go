package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/adro-project/adro/internal/events"
)

var runtimeEnvironmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type RuntimeSelection struct {
	RuntimeID             string
	Model                 string
	ThinkingLevel         string
	ServiceTier           string
	CustomArgs            []string
	RuntimeConfig         map[string]string
	Environment           map[string]string
	MCPServers            []RuntimeMCPServer
	DisabledRuntimeSkills []RuntimeSkillRef
}

type RuntimeMCPServer struct {
	Name              string
	Endpoint          string
	Protocol          string
	BearerTokenEnvVar string
}

type RuntimeSkillRef struct {
	RuntimeID string
	Provider  string
	Root      string
	Key       string
	Name      string
	Plugin    string
}

// RuntimeProviderPool keeps provider run/session state isolated by immutable
// launch configuration while preserving the legacy global provider for Agents
// created before runtime bindings were introduced.
type RuntimeProviderPool struct {
	fallback  ExecutionProvider
	workRoot  string
	stateRoot string
	bus       *events.Bus
	mu        sync.Mutex
	items     map[string]ExecutionProvider
	runs      map[string]ExecutionProvider
}

func NewRuntimeProviderPool(fallback ExecutionProvider, workRoot string, bus *events.Bus) *RuntimeProviderPool {
	stateRoot := ""
	if local, ok := fallback.(*LocalProvider); ok && strings.TrimSpace(local.StatePath) != "" {
		stateRoot = filepath.Join(filepath.Dir(local.StatePath), "runtime-runs")
	}
	return &RuntimeProviderPool{fallback: fallback, workRoot: workRoot, stateRoot: stateRoot, bus: bus, items: map[string]ExecutionProvider{}, runs: map[string]ExecutionProvider{}}
}

func (p *RuntimeProviderPool) Resolve(selection RuntimeSelection) (ExecutionProvider, error) {
	runtimeID := strings.TrimSpace(selection.RuntimeID)
	if runtimeID == "" || runtimeID == "local" {
		if p.fallback == nil {
			return nil, fmt.Errorf("runtime %q has no configured provider", runtimeID)
		}
		return p.fallback, nil
	}
	var runtime *DiscoveredRuntime
	for _, item := range DiscoverLocalRuntimes() {
		if item.ID == runtimeID {
			copy := item
			runtime = &copy
			break
		}
	}
	if runtime == nil {
		return nil, fmt.Errorf("unknown runtime %q", runtimeID)
	}
	if !runtime.AdapterAvailable {
		return nil, fmt.Errorf("runtime %q has no execution adapter", runtimeID)
	}
	if !runtime.Installed || strings.TrimSpace(runtime.ExecutablePath) == "" {
		return nil, fmt.Errorf("runtime %q is not installed", runtimeID)
	}
	if runtime.ModelSelectionUnsupported && (strings.TrimSpace(selection.Model) != "" || strings.TrimSpace(selection.ThinkingLevel) != "" || strings.TrimSpace(selection.ServiceTier) != "") {
		return nil, fmt.Errorf("runtime %q manages its model in the runtime profile and does not support per-Agent model options", runtimeID)
	}
	if err := validateRuntimeCustomArgs(runtimeID, selection.CustomArgs); err != nil {
		return nil, fmt.Errorf("runtime %q configuration: %w", runtimeID, err)
	}
	if err := validateRuntimeConfig(runtimeID, selection.RuntimeConfig); err != nil {
		return nil, fmt.Errorf("runtime %q configuration: %w", runtimeID, err)
	}
	executable := runtime.ExecutablePath
	var baseArgs []string
	if fallback, ok := p.fallback.(*LocalProvider); ok && fallback.executorKind() == runtime.Command {
		// An operator-configured command is authoritative for the matching
		// runtime. This retains wrappers and global launch flags when an Agent
		// selects that runtime explicitly in the control plane.
		executable = fallback.Executable
		baseArgs = append([]string(nil), fallback.Args...)
	}
	key := runtimeSelectionKey(executable, baseArgs, selection)
	p.mu.Lock()
	if existing := p.items[key]; existing != nil {
		p.mu.Unlock()
		return selectedRuntimeProvider{pool: p, provider: existing}, nil
	}
	p.mu.Unlock()
	catalog, err := DiscoverRuntimeModels(context.Background(), runtimeID)
	if err != nil {
		return nil, fmt.Errorf("discover runtime %q models: %w", runtimeID, err)
	}
	if err := validateRuntimeSelection(catalog, selection); err != nil {
		return nil, fmt.Errorf("runtime %q configuration: %w", runtimeID, err)
	}
	created := NewLocalProvider(executable, baseArgs, p.workRoot, p.bus).
		WithRuntimeID(runtimeID)
	if p.stateRoot != "" {
		created.StatePath = filepath.Join(p.stateRoot, key+".json")
		if err := created.loadState(); err != nil {
			return nil, fmt.Errorf("restore runtime %q state: %w", runtimeID, err)
		}
	}
	created.WithExecutionConfig(selection.Model, selection.ThinkingLevel, selection.ServiceTier, selection.CustomArgs).
		WithRuntimeConfig(selection.RuntimeConfig).
		WithRuntimeEnvironment(selection.Environment).
		WithMCPServers(selection.MCPServers).
		WithDisabledRuntimeSkills(selection.DisabledRuntimeSkills)
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing := p.items[key]; existing != nil {
		return selectedRuntimeProvider{pool: p, provider: existing}, nil
	}
	p.items[key] = created
	created.mu.RLock()
	for runID := range created.runs {
		p.runs[runID] = created
	}
	created.mu.RUnlock()
	return selectedRuntimeProvider{pool: p, provider: created}, nil
}

func runtimeSelectionKey(executable string, baseArgs []string, selection RuntimeSelection) string {
	parts := []string{selection.RuntimeID, executable, strings.Join(baseArgs, "\x00"), selection.Model, selection.ThinkingLevel, selection.ServiceTier, strings.Join(selection.CustomArgs, "\x00")}
	keys := make([]string, 0, len(selection.RuntimeConfig)+len(selection.Environment))
	for key := range selection.RuntimeConfig {
		keys = append(keys, "config:"+key)
	}
	for key := range selection.Environment {
		keys = append(keys, "env:"+key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if strings.HasPrefix(key, "config:") {
			parts = append(parts, key+"="+selection.RuntimeConfig[strings.TrimPrefix(key, "config:")])
		} else {
			parts = append(parts, key+"="+selection.Environment[strings.TrimPrefix(key, "env:")])
		}
	}
	servers := append([]RuntimeMCPServer(nil), selection.MCPServers...)
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	for _, server := range servers {
		parts = append(parts, "mcp:"+server.Name+":"+server.Endpoint+":"+server.Protocol+":"+server.BearerTokenEnvVar)
	}
	skills := append([]RuntimeSkillRef(nil), selection.DisabledRuntimeSkills...)
	sort.Slice(skills, func(i, j int) bool {
		left := strings.Join([]string{skills[i].RuntimeID, skills[i].Provider, skills[i].Root, skills[i].Key, skills[i].Plugin}, "\x00")
		right := strings.Join([]string{skills[j].RuntimeID, skills[j].Provider, skills[j].Root, skills[j].Key, skills[j].Plugin}, "\x00")
		return left < right
	})
	for _, skill := range skills {
		parts = append(parts, "disabled-skill:"+strings.Join([]string{skill.RuntimeID, skill.Provider, skill.Root, skill.Key, skill.Plugin}, ":"))
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x01")))
	return hex.EncodeToString(digest[:])
}

func validateRuntimeConfig(runtimeID string, config map[string]string) error {
	if runtimeID == "codex" {
		if value := strings.TrimSpace(config["sandbox_mode"]); value != "" && value != "read-only" && value != "workspace-write" && value != "danger-full-access" {
			return fmt.Errorf("runtime_config sandbox_mode %q is invalid", value)
		}
		if value := strings.TrimSpace(config["approval_policy"]); value != "" && value != "untrusted" && value != "on-failure" && value != "on-request" && value != "never" {
			return fmt.Errorf("runtime_config approval_policy %q is invalid", value)
		}
		return nil
	}
	if runtimeID != "openclaw" {
		return nil
	}
	allowed := map[string]bool{
		"mode": true, "gateway.host": true, "gateway.port": true,
		"gateway.tls": true, "gateway.auth_env": true,
	}
	for key := range config {
		if !allowed[key] {
			return fmt.Errorf("runtime_config key %q is not supported", key)
		}
	}
	mode := strings.TrimSpace(config["mode"])
	if mode != "" && mode != "local" && mode != "gateway" {
		return fmt.Errorf("runtime_config mode %q is invalid", mode)
	}
	if value := strings.TrimSpace(config["gateway.port"]); value != "" {
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("runtime_config gateway.port %q is invalid", value)
		}
	}
	if value := strings.TrimSpace(config["gateway.tls"]); value != "" && value != "true" && value != "false" {
		return fmt.Errorf("runtime_config gateway.tls %q is invalid", value)
	}
	if value := strings.TrimSpace(config["gateway.auth_env"]); value != "" && !runtimeEnvironmentNamePattern.MatchString(value) {
		return fmt.Errorf("runtime_config gateway.auth_env %q is invalid", value)
	}
	return nil
}

func validateRuntimeSelection(catalog RuntimeModelCatalog, selection RuntimeSelection) error {
	modelID := strings.TrimSpace(selection.Model)
	if modelID == "" {
		if strings.TrimSpace(selection.ThinkingLevel) != "" || strings.TrimSpace(selection.ServiceTier) != "" {
			return fmt.Errorf("model is required when thinking_level or service_tier is set")
		}
		return nil
	}
	var selected *RuntimeModel
	for i := range catalog.Models {
		if catalog.Models[i].ID == modelID {
			selected = &catalog.Models[i]
			break
		}
	}
	if selected == nil {
		if !catalog.Fallback && len(catalog.Models) > 0 {
			return fmt.Errorf("model %q is not advertised by the installed runtime", modelID)
		}
		if strings.TrimSpace(selection.ThinkingLevel) != "" || strings.TrimSpace(selection.ServiceTier) != "" {
			return fmt.Errorf("thinking_level and service_tier require an advertised model")
		}
		return nil
	}
	if level := strings.TrimSpace(selection.ThinkingLevel); level != "" {
		found := false
		if selected.Thinking != nil {
			for _, item := range selected.Thinking.SupportedLevels {
				if item.Value == level {
					found = true
					break
				}
			}
		}
		if !found {
			return fmt.Errorf("thinking_level %q is not supported by model %q", level, modelID)
		}
	}
	if tier := strings.TrimSpace(selection.ServiceTier); tier != "" {
		found := false
		for _, item := range selected.ServiceTiers {
			if item.ID == tier {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("service_tier %q is not supported by model %q", tier, modelID)
		}
	}
	return nil
}

type selectedRuntimeProvider struct {
	pool     *RuntimeProviderPool
	provider ExecutionProvider
}

func (p selectedRuntimeProvider) StartRun(ctx context.Context, cmd StartRunCommand) (RunBinding, error) {
	binding, err := p.provider.StartRun(ctx, cmd)
	if err == nil {
		p.pool.mu.Lock()
		p.pool.runs[binding.ProviderRunID] = p.provider
		p.pool.runs[binding.ID] = p.provider
		p.pool.mu.Unlock()
	}
	return binding, err
}
func (p selectedRuntimeProvider) Capabilities(ctx context.Context) (Capabilities, error) {
	return p.provider.Capabilities(ctx)
}
func (p selectedRuntimeProvider) EnsureAgent(ctx context.Context, v AgentSpec) (AgentBinding, error) {
	return p.provider.EnsureAgent(ctx, v)
}
func (p selectedRuntimeProvider) EnsureTeamWorkspace(ctx context.Context, v WorkspaceSpec) (WorkspaceBinding, error) {
	return p.provider.EnsureTeamWorkspace(ctx, v)
}
func (p selectedRuntimeProvider) CreateWorkItem(ctx context.Context, v WorkItemSpec) (ProviderWorkItem, error) {
	return p.provider.CreateWorkItem(ctx, v)
}
func (p selectedRuntimeProvider) AppendInput(ctx context.Context, id, input string) error {
	return p.provider.AppendInput(ctx, id, input)
}
func (p selectedRuntimeProvider) CancelRun(ctx context.Context, id string) error {
	return p.provider.CancelRun(ctx, id)
}
func (p selectedRuntimeProvider) GetRun(ctx context.Context, id string) (RunSnapshot, error) {
	return p.provider.GetRun(ctx, id)
}
func (p selectedRuntimeProvider) StreamEvents(ctx context.Context, id, cursor string) (EventStream, error) {
	return p.provider.StreamEvents(ctx, id, cursor)
}
func (p selectedRuntimeProvider) GetUsage(ctx context.Context, id string) (Usage, error) {
	return p.provider.GetUsage(ctx, id)
}
func (p selectedRuntimeProvider) Health(ctx context.Context) (ProviderHealth, error) {
	return p.provider.Health(ctx)
}

func (p *RuntimeProviderPool) runProvider(id string) ExecutionProvider {
	p.mu.Lock()
	defer p.mu.Unlock()
	if provider := p.runs[id]; provider != nil {
		return provider
	}
	return p.fallback
}
func (p *RuntimeProviderPool) Capabilities(ctx context.Context) (Capabilities, error) {
	return p.fallback.Capabilities(ctx)
}
func (p *RuntimeProviderPool) EnsureAgent(ctx context.Context, v AgentSpec) (AgentBinding, error) {
	return p.fallback.EnsureAgent(ctx, v)
}
func (p *RuntimeProviderPool) EnsureTeamWorkspace(ctx context.Context, v WorkspaceSpec) (WorkspaceBinding, error) {
	return p.fallback.EnsureTeamWorkspace(ctx, v)
}
func (p *RuntimeProviderPool) CreateWorkItem(ctx context.Context, v WorkItemSpec) (ProviderWorkItem, error) {
	return p.fallback.CreateWorkItem(ctx, v)
}
func (p *RuntimeProviderPool) StartRun(ctx context.Context, v StartRunCommand) (RunBinding, error) {
	return p.fallback.StartRun(ctx, v)
}
func (p *RuntimeProviderPool) AppendInput(ctx context.Context, id, input string) error {
	return p.runProvider(id).AppendInput(ctx, id, input)
}
func (p *RuntimeProviderPool) AppendInputWithKey(ctx context.Context, id, input, key string) error {
	runtime := p.runProvider(id)
	if keyed, ok := runtime.(InputKeyProvider); ok {
		return keyed.AppendInputWithKey(ctx, id, input, key)
	}
	return runtime.AppendInput(ctx, id, input)
}
func (p *RuntimeProviderPool) CancelRun(ctx context.Context, id string) error {
	return p.runProvider(id).CancelRun(ctx, id)
}
func (p *RuntimeProviderPool) GetRun(ctx context.Context, id string) (RunSnapshot, error) {
	return p.runProvider(id).GetRun(ctx, id)
}
func (p *RuntimeProviderPool) StreamEvents(ctx context.Context, id, cursor string) (EventStream, error) {
	return p.runProvider(id).StreamEvents(ctx, id, cursor)
}
func (p *RuntimeProviderPool) GetUsage(ctx context.Context, id string) (Usage, error) {
	return p.runProvider(id).GetUsage(ctx, id)
}
func (p *RuntimeProviderPool) Health(ctx context.Context) (ProviderHealth, error) {
	return p.fallback.Health(ctx)
}
