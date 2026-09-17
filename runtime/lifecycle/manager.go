// Package lifecycle owns component startup, shutdown, and health aggregation.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	StatusNew      Status = "new"
	StatusStarting Status = "starting"
	StatusReady    Status = "ready"
	StatusDegraded Status = "degraded"
	StatusStopping Status = "stopping"
	StatusStopped  Status = "stopped"
	StatusFailed   Status = "failed"
)

type Health struct {
	Status    Status    `json:"status"`
	Reason    string    `json:"reason,omitempty"`
	ChangedAt time.Time `json:"changed_at"`
}

type Component interface {
	Name() string
	Dependencies() []string
	Start(context.Context) error
	Stop(context.Context) error
	Health(context.Context) Health
}

type Manager struct {
	mu          sync.Mutex
	components  map[string]Component
	order       []string
	started     []string
	cancel      context.CancelFunc
	stopTimeout time.Duration
}

func NewManager(components []Component, stopTimeout time.Duration) (*Manager, error) {
	if stopTimeout <= 0 {
		stopTimeout = 5 * time.Second
	}
	manager := &Manager{components: make(map[string]Component, len(components)), stopTimeout: stopTimeout}
	for _, component := range components {
		if component == nil {
			return nil, errors.New("lifecycle component is nil")
		}
		name := strings.TrimSpace(component.Name())
		if name == "" {
			return nil, errors.New("lifecycle component name is required")
		}
		if _, exists := manager.components[name]; exists {
			return nil, fmt.Errorf("duplicate lifecycle component %q", name)
		}
		manager.components[name] = component
	}
	order, err := stableTopologicalOrder(manager.components)
	if err != nil {
		return nil, err
	}
	manager.order = order
	return manager, nil
}

func (m *Manager) Order() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.order...)
}

func (m *Manager) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.started) > 0 {
		return nil
	}
	root, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	for _, name := range m.order {
		if err := m.components[name].Start(root); err != nil {
			cancel()
			rollbackErr := m.stopStartedLocked(context.Background())
			return errors.Join(fmt.Errorf("start lifecycle component %s: %w", name, err), rollbackErr)
		}
		m.started = append(m.started, name)
	}
	return nil
}

func (m *Manager) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	return m.stopStartedLocked(ctx)
}

func (m *Manager) stopStartedLocked(parent context.Context) error {
	var result error
	for index := len(m.started) - 1; index >= 0; index-- {
		name := m.started[index]
		stopCtx, cancel := context.WithTimeout(parent, m.stopTimeout)
		err := m.components[name].Stop(stopCtx)
		cancel()
		if err != nil {
			result = errors.Join(result, fmt.Errorf("stop lifecycle component %s: %w", name, err))
		}
	}
	m.started = nil
	return result
}

func (m *Manager) Health(ctx context.Context) map[string]Health {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]Health, len(m.components))
	for _, name := range m.order {
		result[name] = m.components[name].Health(ctx)
	}
	return result
}

func stableTopologicalOrder(components map[string]Component) ([]string, error) {
	indegree := make(map[string]int, len(components))
	dependents := make(map[string][]string, len(components))
	for name := range components {
		indegree[name] = 0
	}
	for name, component := range components {
		seen := map[string]bool{}
		for _, dependency := range component.Dependencies() {
			dependency = strings.TrimSpace(dependency)
			if dependency == "" || seen[dependency] {
				return nil, fmt.Errorf("component %s has an empty or duplicate dependency", name)
			}
			seen[dependency] = true
			if _, exists := components[dependency]; !exists {
				return nil, fmt.Errorf("component %s depends on missing component %s", name, dependency)
			}
			indegree[name]++
			dependents[dependency] = append(dependents[dependency], name)
		}
	}
	ready := make([]string, 0, len(components))
	for name, degree := range indegree {
		if degree == 0 {
			ready = append(ready, name)
		}
	}
	sort.Strings(ready)
	order := make([]string, 0, len(components))
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		order = append(order, name)
		sort.Strings(dependents[name])
		for _, dependent := range dependents[name] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
				sort.Strings(ready)
			}
		}
	}
	if len(order) != len(components) {
		return nil, errors.New("lifecycle dependency graph contains a cycle")
	}
	return order, nil
}
