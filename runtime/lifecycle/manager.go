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
	Ready     bool      `json:"ready"`
	Live      bool      `json:"live"`
}

// IsReady and IsLive preserve the original status-only component contract
// while allowing components to report readiness and liveness independently.
func (h Health) IsReady() bool {
	switch h.Status {
	case StatusReady:
		return true
	case StatusDegraded:
		return h.Ready
	default:
		return false
	}
}

func (h Health) IsLive() bool {
	switch h.Status {
	case StatusStarting, StatusReady, StatusStopping:
		return true
	case StatusDegraded:
		return h.Live
	default:
		return false
	}
}

type Snapshot struct {
	Status     Status            `json:"status"`
	Reason     string            `json:"reason,omitempty"`
	ChangedAt  time.Time         `json:"changed_at"`
	Ready      bool              `json:"ready"`
	Live       bool              `json:"live"`
	Components map[string]Health `json:"components"`
}

type Component interface {
	Name() string
	Dependencies() []string
	Start(context.Context) error
	Stop(context.Context) error
	Health(context.Context) Health
}

type Manager struct {
	mu           sync.Mutex
	healthMu     sync.Mutex
	components   map[string]Component
	order        []string
	started      []string
	running      bool
	cancel       context.CancelFunc
	startTimeout time.Duration
	stopTimeout  time.Duration
}

func NewManager(components []Component, stopTimeout time.Duration) (*Manager, error) {
	return NewManagerWithTimeouts(components, 5*time.Second, stopTimeout)
}

// NewManagerWithTimeouts constructs a lifecycle manager with independent
// startup and shutdown bounds. NewManager preserves its original shutdown-only
// argument and applies the default startup bound.
func NewManagerWithTimeouts(components []Component, startTimeout, stopTimeout time.Duration) (*Manager, error) {
	if startTimeout <= 0 {
		startTimeout = 5 * time.Second
	}
	if stopTimeout <= 0 {
		stopTimeout = 5 * time.Second
	}
	manager := &Manager{components: make(map[string]Component, len(components)), startTimeout: startTimeout, stopTimeout: stopTimeout}
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
	if m.running {
		return nil
	}
	if len(m.started) > 0 {
		return errors.New("lifecycle manager has components pending cleanup")
	}
	root, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	for _, name := range m.order {
		startCtx, startCancel := context.WithTimeout(root, m.startTimeout)
		err := m.components[name].Start(startCtx)
		startCancel()
		if err != nil {
			cancel()
			rollbackErr := m.stopStartedLocked(context.Background())
			return errors.Join(fmt.Errorf("start lifecycle component %s: %w", name, err), rollbackErr)
		}
		m.started = append(m.started, name)
	}
	m.running = true
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
	m.running = false
	return m.stopStartedLocked(ctx)
}

func (m *Manager) stopStartedLocked(parent context.Context) error {
	var result error
	failed := make([]string, 0)
	for index := len(m.started) - 1; index >= 0; index-- {
		name := m.started[index]
		stopCtx, cancel := context.WithTimeout(parent, m.stopTimeout)
		err := m.components[name].Stop(stopCtx)
		cancel()
		if err != nil {
			result = errors.Join(result, fmt.Errorf("stop lifecycle component %s: %w", name, err))
			failed = append(failed, name)
		}
	}
	for left, right := 0, len(failed)-1; left < right; left, right = left+1, right-1 {
		failed[left], failed[right] = failed[right], failed[left]
	}
	m.started = failed
	return result
}

func (m *Manager) Health(ctx context.Context) map[string]Health {
	return m.Snapshot(ctx).Components
}

// Snapshot aggregates component health without becoming a second business
// state source. Component Health remains authoritative; the manager only
// derives process-level readiness/liveness for probes and operators.
func (m *Manager) Snapshot(ctx context.Context) Snapshot {
	if ctx == nil {
		ctx = context.Background()
	}
	m.healthMu.Lock()
	defer m.healthMu.Unlock()
	m.mu.Lock()
	order := append([]string(nil), m.order...)
	registered := make(map[string]Component, len(m.components))
	for name, component := range m.components {
		registered[name] = component
	}
	m.mu.Unlock()

	components := make(map[string]Health, len(registered))
	var changedAt time.Time
	status := StatusStopped
	ready, live := true, true
	var reason string
	for _, name := range order {
		health := registered[name].Health(ctx)
		components[name] = health
		if health.ChangedAt.After(changedAt) {
			changedAt = health.ChangedAt
		}
		ready = ready && health.IsReady()
		live = live && health.IsLive()
		if reason == "" && health.Reason != "" {
			reason = name + ": " + health.Reason
		}
		if !validStatus(health.Status) && reason == "" {
			reason = name + ": invalid lifecycle status"
		}
		status = aggregateStatus(status, health.Status, len(components) == 1)
	}
	if len(components) == 0 {
		status = StatusReady
		ready, live = true, true
	}
	if !live && reason == "" {
		reason = "one or more lifecycle components are not live"
	}
	if !ready && reason == "" {
		reason = "one or more lifecycle components are not ready"
	}
	return Snapshot{Status: status, Reason: reason, ChangedAt: changedAt, Ready: ready, Live: live, Components: components}
}

func (m *Manager) Readiness(ctx context.Context) Health {
	snapshot := m.Snapshot(ctx)
	return Health{Status: snapshot.Status, Reason: snapshot.Reason, ChangedAt: snapshot.ChangedAt, Ready: snapshot.Ready, Live: snapshot.Live}
}

func (m *Manager) Liveness(ctx context.Context) Health {
	snapshot := m.Snapshot(ctx)
	return Health{Status: snapshot.Status, Reason: snapshot.Reason, ChangedAt: snapshot.ChangedAt, Ready: snapshot.Ready, Live: snapshot.Live}
}

func aggregateStatus(current, next Status, first bool) Status {
	if !validStatus(next) || (!first && !validStatus(current)) {
		return StatusFailed
	}
	if first {
		return next
	}
	if next == StatusFailed {
		return StatusFailed
	}
	if current == StatusFailed {
		return current
	}
	if next == StatusNew || current == StatusNew {
		return StatusNew
	}
	if next == StatusDegraded || current == StatusDegraded {
		return StatusDegraded
	}
	if next == StatusStopping || current == StatusStopping {
		return StatusStopping
	}
	if next == StatusStarting || current == StatusStarting {
		return StatusStarting
	}
	if next == StatusStopped || current == StatusStopped {
		return StatusStopped
	}
	return StatusReady
}

func validStatus(status Status) bool {
	switch status {
	case StatusNew, StatusStarting, StatusReady, StatusDegraded, StatusStopping, StatusStopped, StatusFailed:
		return true
	default:
		return false
	}
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
