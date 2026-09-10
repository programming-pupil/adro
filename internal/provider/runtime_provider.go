package provider

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/adro-project/adro/internal/events"
)

type RuntimeSelection struct {
	RuntimeID     string
	Model         string
	ThinkingLevel string
	ServiceTier   string
	CustomArgs    []string
}

// RuntimeProviderPool keeps provider run/session state isolated by immutable
// launch configuration while preserving the legacy global provider for Agents
// created before runtime bindings were introduced.
type RuntimeProviderPool struct {
	fallback ExecutionProvider
	workRoot string
	bus      *events.Bus
	mu       sync.Mutex
	items    map[string]ExecutionProvider
	runs     map[string]ExecutionProvider
}

func NewRuntimeProviderPool(fallback ExecutionProvider, workRoot string, bus *events.Bus) *RuntimeProviderPool {
	return &RuntimeProviderPool{fallback: fallback, workRoot: workRoot, bus: bus, items: map[string]ExecutionProvider{}, runs: map[string]ExecutionProvider{}}
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
	key := strings.Join([]string{runtimeID, runtime.ExecutablePath, selection.Model, selection.ThinkingLevel, selection.ServiceTier, strings.Join(selection.CustomArgs, "\x00")}, "\x01")
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
	created := NewLocalProvider(runtime.ExecutablePath, nil, p.workRoot, p.bus).
		WithExecutionConfig(selection.Model, selection.ThinkingLevel, selection.ServiceTier, selection.CustomArgs)
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing := p.items[key]; existing != nil {
		return selectedRuntimeProvider{pool: p, provider: existing}, nil
	}
	p.items[key] = created
	return selectedRuntimeProvider{pool: p, provider: created}, nil
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
