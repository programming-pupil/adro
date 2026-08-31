// Package events defines the versioned event envelope and an in-process bus
// used by the reference implementation. A NATS adapter can implement the
// same publisher interface without leaking transport details into the domain.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/adro-project/adro/internal/domain"
)

type Envelope struct {
	EventID          string         `json:"event_id"`
	EventType        string         `json:"event_type"`
	AggregateType    string         `json:"aggregate_type"`
	AggregateID      string         `json:"aggregate_id"`
	AggregateVersion int64          `json:"aggregate_version"`
	TenantID         string         `json:"tenant_id"`
	WorkspaceID      string         `json:"workspace_id"`
	CorrelationID    string         `json:"correlation_id"`
	CausationID      string         `json:"causation_id,omitempty"`
	Provider         string         `json:"provider,omitempty"`
	ProviderEventID  string         `json:"provider_event_id,omitempty"`
	OccurredAt       time.Time      `json:"occurred_at"`
	Classification   string         `json:"classification"`
	Payload          map[string]any `json:"payload"`
}

func New(eventType, aggregateType, aggregateID, tenantID, workspaceID string, version int64, payload map[string]any) Envelope {
	return Envelope{
		EventID: domain.NewID(), EventType: eventType, AggregateType: aggregateType,
		AggregateID: aggregateID, AggregateVersion: version, TenantID: tenantID,
		WorkspaceID: workspaceID, CorrelationID: aggregateID, OccurredAt: time.Now().UTC(),
		Classification: "internal", Payload: payload,
	}
}

type Publisher interface {
	Publish(context.Context, Envelope) error
}

// Bus provides synchronous publication and replay-friendly history for the
// local profile. Subscribers receive a copy so a slow consumer cannot mutate
// the event retained by the bus.
type Bus struct {
	mu           sync.RWMutex
	statePath    string
	events       []Envelope
	seen         map[string]struct{}
	seenProvider map[string]struct{}
	subscribers  map[int]chan Envelope
	nextSubID    int
}

func NewBus() *Bus {
	return &Bus{seen: make(map[string]struct{}), seenProvider: make(map[string]struct{}), subscribers: make(map[int]chan Envelope)}
}

func NewPersistentBus(path string) (*Bus, error) {
	b := NewBus()
	b.statePath = path
	if path == "" {
		return b, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return b, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read event state: %w", err)
	}
	var stored []Envelope
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("decode event state: %w", err)
	}
	for _, event := range stored {
		b.events = append(b.events, event)
		b.seen[event.EventID] = struct{}{}
		if event.Provider != "" && event.ProviderEventID != "" {
			b.seenProvider[event.Provider+"\x00"+event.ProviderEventID] = struct{}{}
		}
	}
	return b, nil
}

func (b *Bus) Flush() error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.persistLocked()
}

func (b *Bus) persistLocked() error {
	if b.statePath == "" {
		return nil
	}
	data, err := json.Marshal(b.events)
	if err != nil {
		return err
	}
	dir := filepath.Dir(b.statePath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".adro-events-*")
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
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, b.statePath)
}

func (b *Bus) Publish(_ context.Context, event Envelope) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if event.EventID == "" {
		event.EventID = domain.NewID()
	}
	// Payload maps are mutable reference values. Clone before retaining or
	// delivering an envelope so a caller or subscriber cannot rewrite the
	// replay history after publication.
	event = cloneEnvelope(event)
	if _, ok := b.seen[event.EventID]; ok {
		return nil
	}
	providerKey := ""
	if event.Provider != "" && event.ProviderEventID != "" {
		providerKey = event.Provider + "\x00" + event.ProviderEventID
		if _, ok := b.seenProvider[providerKey]; ok {
			return nil
		}
		b.seenProvider[providerKey] = struct{}{}
	}
	b.seen[event.EventID] = struct{}{}
	b.events = append(b.events, event)
	if err := b.persistLocked(); err != nil {
		b.events = b.events[:len(b.events)-1]
		delete(b.seen, event.EventID)
		if providerKey != "" {
			delete(b.seenProvider, providerKey)
		}
		return fmt.Errorf("persist event: %w", err)
	}
	channels := make([]chan Envelope, 0, len(b.subscribers))
	for _, ch := range b.subscribers {
		channels = append(channels, ch)
	}
	for _, ch := range channels {
		select {
		case ch <- cloneEnvelope(event):
		default:
		}
	}
	return nil
}

func (b *Bus) List(aggregateID, cursor string, limit int) ([]Envelope, string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit <= 0 || limit > 250 {
		limit = 100
	}
	start := 0
	for i, e := range b.events {
		if cursor != "" && e.EventID == cursor {
			start = i + 1
			break
		}
	}
	items := make([]Envelope, 0, limit)
	for _, e := range b.events[start:] {
		if aggregateID != "" && e.AggregateID != aggregateID {
			continue
		}
		items = append(items, cloneEnvelope(e))
		if len(items) == limit {
			break
		}
	}
	next := ""
	if len(items) == limit {
		next = items[len(items)-1].EventID
	}
	return items, next
}

func cloneEnvelope(event Envelope) Envelope {
	if event.Payload == nil {
		return event
	}
	data, err := json.Marshal(event.Payload)
	if err != nil {
		// Event payloads are JSON contracts. Keep the value when a custom
		// in-process publisher supplies an unsupported value; persistence will
		// reject it with the same serialization error below.
		return event
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return event
	}
	event.Payload = payload
	return event
}

func (b *Bus) Subscribe(buffer int) (<-chan Envelope, func()) {
	if buffer < 1 {
		buffer = 16
	}
	ch := make(chan Envelope, buffer)
	b.mu.Lock()
	id := b.nextSubID
	b.nextSubID++
	b.subscribers[id] = ch
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		// Do not close the channel here: Publish may have copied it just before
		// cancellation. Removing it is enough to stop future delivery and avoids
		// a send-on-closed-channel race.
		delete(b.subscribers, id)
		b.mu.Unlock()
	}
}
