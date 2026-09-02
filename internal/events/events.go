// Package events defines the versioned event envelope and an in-process bus
// used by the reference implementation. A NATS adapter can implement the
// same publisher interface without leaking transport details into the domain.
package events

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/durable"
)

type Envelope struct {
	EventID string `json:"event_id"`
	// Sequence is the durable append position assigned by the event bus.
	Sequence         int64          `json:"sequence"`
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
	PayloadHash      string         `json:"payload_hash"`
	Payload          map[string]any `json:"payload"`
}

func New(eventType, aggregateType, aggregateID, tenantID, workspaceID string, version int64, payload map[string]any) Envelope {
	return Envelope{
		EventID: domain.NewID(), EventType: eventType, AggregateType: aggregateType,
		AggregateID: aggregateID, AggregateVersion: version, TenantID: tenantID,
		WorkspaceID: workspaceID, CorrelationID: aggregateID, OccurredAt: time.Now().UTC(),
		Classification: "internal", PayloadHash: payloadHash(payload), Payload: payload,
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
	dropped      map[int]streamGap
	nextSubID    int
	revision     int64
	acks         map[string]string
}

type streamGap struct {
	count int64
	from  int64
	to    int64
}

type persistedEvents struct {
	Revision int64             `json:"revision"`
	Events   []Envelope        `json:"events"`
	Acks     map[string]string `json:"acks,omitempty"`
}

func NewBus() *Bus {
	return &Bus{seen: make(map[string]struct{}), seenProvider: make(map[string]struct{}), subscribers: make(map[int]chan Envelope), dropped: make(map[int]streamGap), acks: make(map[string]string)}
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
	var persisted persistedEvents
	if err := json.Unmarshal(data, &persisted); err == nil && persisted.Events != nil {
		stored = persisted.Events
		b.revision = persisted.Revision
		b.acks = persisted.Acks
		if b.acks == nil {
			b.acks = make(map[string]string)
		}
	} else if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("decode event state: %w", err)
	}
	for _, event := range stored {
		computed := payloadHash(event.Payload)
		if event.PayloadHash != "" && event.PayloadHash != computed {
			return nil, fmt.Errorf("event %s payload hash mismatch", event.EventID)
		}
		event.PayloadHash = computed
		b.events = append(b.events, event)
		b.seen[event.EventID] = struct{}{}
		if event.Provider != "" && event.ProviderEventID != "" {
			b.seenProvider[event.Provider+"\x00"+event.ProviderEventID] = struct{}{}
		}
	}
	normalizeSequences(b.events)
	return b, nil
}

func (b *Bus) Flush() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.persistLocked()
}

func (b *Bus) persistLocked() error {
	if b.statePath == "" {
		return nil
	}
	return durable.WithExclusive(b.statePath, func() error {
		// Merge an external writer's history while holding the lock. This makes a
		// persistent local bus safe for multiple API processes without dropping
		// events that were appended by a peer between publishes.
		if disk, err := readPersistedEvents(b.statePath); err != nil {
			return err
		} else if disk != nil && disk.revision != b.revision {
			b.events = mergeEvents(disk.events, b.events)
			if b.acks == nil {
				b.acks = make(map[string]string)
			}
			for consumer, cursor := range disk.acks {
				b.acks[consumer] = cursor
			}
			rebuildSeen(b)
			b.revision = disk.revision
		}
		normalizeSequences(b.events)
		eventsCopy := append([]Envelope(nil), b.events...)
		if eventsCopy == nil {
			eventsCopy = []Envelope{}
		}
		next := persistedEvents{Revision: b.revision + 1, Events: eventsCopy, Acks: b.acks}
		data, err := json.Marshal(next)
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
			_ = tmp.Close()
			return err
		}
		if _, err := tmp.Write(data); err != nil {
			_ = tmp.Close()
			return err
		}
		if err := tmp.Sync(); err != nil {
			_ = tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := durable.Inject("events.snapshot.before_rename"); err != nil {
			return err
		}
		if err := os.Rename(tmpName, b.statePath); err != nil {
			return err
		}
		if dirFile, openErr := os.Open(dir); openErr == nil {
			if syncErr := dirFile.Sync(); syncErr != nil {
				_ = dirFile.Close()
				return syncErr
			}
			_ = dirFile.Close()
		}
		b.revision = next.Revision
		return nil
	})
}

// Reload refreshes a persistent bus from a peer process. Subscribers are not
// replayed automatically; callers can use List with the last cursor to catch
// up deterministically.
func (b *Bus) Reload() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reloadLocked()
}

func (b *Bus) reloadLocked() error {
	if b.statePath == "" {
		return nil
	}
	persisted, err := readPersistedEvents(b.statePath)
	if err != nil || persisted == nil || persisted.revision <= b.revision {
		return err
	}
	b.events = append([]Envelope(nil), persisted.events...)
	b.acks = make(map[string]string, len(persisted.acks))
	for consumer, cursor := range persisted.acks {
		b.acks[consumer] = cursor
	}
	b.revision = persisted.revision
	normalizeSequences(b.events)
	rebuildSeen(b)
	return nil
}

func readPersistedEvents(path string) (*struct {
	revision int64
	events   []Envelope
	acks     map[string]string
}, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read event state: %w", err)
	}
	var persisted persistedEvents
	if err := json.Unmarshal(data, &persisted); err == nil && persisted.Events != nil {
		return &struct {
			revision int64
			events   []Envelope
			acks     map[string]string
		}{persisted.Revision, persisted.Events, persisted.Acks}, nil
	}
	var events []Envelope
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("decode event state: %w", err)
	}
	return &struct {
		revision int64
		events   []Envelope
		acks     map[string]string
	}{0, events, nil}, nil
}

func payloadHash(payload map[string]any) string {
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func maxSequence(items []Envelope) int64 {
	var max int64
	for _, event := range items {
		if event.Sequence > max {
			max = event.Sequence
		}
	}
	return max
}

// normalizeSequences migrates legacy snapshots and ensures merged local
// writers never expose duplicate durable cursor positions.
func normalizeSequences(items []Envelope) {
	used := make(map[int64]struct{}, len(items))
	var previous int64
	for i := range items {
		sequence := items[i].Sequence
		if sequence <= 0 || sequence <= previous {
			sequence = previous + 1
		}
		if _, exists := used[sequence]; exists {
			sequence = previous + 1
		}
		if sequence <= 0 {
			sequence = int64(i + 1)
		}
		items[i].Sequence = sequence
		used[sequence] = struct{}{}
		previous = sequence
	}
}

func mergeEvents(primary, secondary []Envelope) []Envelope {
	result := make([]Envelope, 0, len(primary)+len(secondary))
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	for _, event := range append(append([]Envelope(nil), primary...), secondary...) {
		if event.EventID == "" {
			continue
		}
		if _, ok := seen[event.EventID]; ok {
			continue
		}
		seen[event.EventID] = struct{}{}
		result = append(result, cloneEnvelope(event))
	}
	return result
}

func rebuildSeen(b *Bus) {
	b.seen = make(map[string]struct{}, len(b.events))
	b.seenProvider = make(map[string]struct{})
	for _, event := range b.events {
		b.seen[event.EventID] = struct{}{}
		if event.Provider != "" && event.ProviderEventID != "" {
			b.seenProvider[event.Provider+"\x00"+event.ProviderEventID] = struct{}{}
		}
	}
}

// Ack durably records the last event consumed by consumerID. Acknowledging an
// unknown event is rejected so a typo cannot advance a stream past retained
// history. Acks are monotonic within the event history.
func (b *Bus) Ack(consumerID, eventID string) error {
	consumerID, eventID = strings.TrimSpace(consumerID), strings.TrimSpace(eventID)
	if consumerID == "" || eventID == "" {
		return errors.New("consumer_id and event_id are required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.reloadLocked(); err != nil {
		return err
	}
	index := -1
	for i, event := range b.events {
		if event.EventID == eventID {
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("event is not retained")
	}
	if previous := b.acks[consumerID]; previous != "" {
		previousIndex := -1
		for i, event := range b.events {
			if event.EventID == previous {
				previousIndex = i
				break
			}
		}
		if previousIndex >= 0 && index < previousIndex {
			return errors.New("event acknowledgement would move backwards")
		}
	}
	previous := b.acks[consumerID]
	b.acks[consumerID] = eventID
	if err := b.persistLocked(); err != nil {
		if previous == "" {
			delete(b.acks, consumerID)
		} else {
			b.acks[consumerID] = previous
		}
		return fmt.Errorf("persist event acknowledgement: %w", err)
	}
	return nil
}

// Replay returns events after the consumer's durable acknowledgement. An
// explicit cursor overrides the stored ack and is useful for stateless clients.
func (b *Bus) Replay(consumerID, aggregateID, cursor string, limit int) ([]Envelope, string, error) {
	if b.statePath != "" {
		if err := b.Reload(); err != nil {
			return nil, "", err
		}
	}
	if strings.TrimSpace(cursor) == "" && strings.TrimSpace(consumerID) != "" {
		b.mu.RLock()
		cursor = b.acks[consumerID]
		b.mu.RUnlock()
	}
	items, next := b.List(aggregateID, cursor, limit)
	return items, next, nil
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
	computedPayloadHash := payloadHash(event.Payload)
	if computedPayloadHash == "" {
		return errors.New("event payload is not JSON encodable")
	}
	if event.PayloadHash != "" && event.PayloadHash != computedPayloadHash {
		return errors.New("event payload hash does not match payload")
	}
	event.PayloadHash = computedPayloadHash
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
	event.Sequence = maxSequence(b.events) + 1
	b.events = append(b.events, event)
	if err := b.persistLocked(); err != nil {
		b.events = b.events[:len(b.events)-1]
		delete(b.seen, event.EventID)
		if providerKey != "" {
			delete(b.seenProvider, providerKey)
		}
		return fmt.Errorf("persist event: %w", err)
	}
	// A peer writer may have occupied the sequence we tentatively assigned.
	// Publish the canonical post-merge envelope to live subscribers.
	for _, retained := range b.events {
		if retained.EventID == event.EventID {
			event = cloneEnvelope(retained)
			break
		}
	}
	for id, ch := range b.subscribers {
		gap := b.dropped[id]
		if gap.count > 0 {
			// A bounded live subscriber must never lose an event silently. Emit a
			// history replay hint as soon as the queue has room; consumers can
			// replay from their last acknowledged cursor to recover the exact range.
			gapEvent := Envelope{
				EventID: domain.NewID(), Sequence: gap.from, EventType: "stream.gap.v1",
				AggregateType: "stream", CorrelationID: "stream", OccurredAt: time.Now().UTC(),
				Classification: "system", Payload: map[string]any{"dropped_count": gap.count, "from_sequence": gap.from, "to_sequence": gap.to},
			}
			gapEvent.PayloadHash = payloadHash(gapEvent.Payload)
			select {
			case ch <- gapEvent:
				delete(b.dropped, id)
			default:
				gap.count++
				gap.to = event.Sequence
				b.dropped[id] = gap
				continue
			}
		}
		select {
		case ch <- cloneEnvelope(event):
		default:
			gap := b.dropped[id]
			gap.count++
			if gap.from == 0 {
				gap.from = event.Sequence
			}
			gap.to = event.Sequence
			b.dropped[id] = gap
		}
	}
	return nil
}

func (b *Bus) List(aggregateID, cursor string, limit int) ([]Envelope, string) {
	if b.statePath != "" {
		b.mu.Lock()
		_ = b.reloadLocked()
		b.mu.Unlock()
	}
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
	b.dropped[id] = streamGap{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		// Do not close the channel here: Publish may have copied it just before
		// cancellation. Removing it is enough to stop future delivery and avoids
		// a send-on-closed-channel race.
		delete(b.subscribers, id)
		delete(b.dropped, id)
		b.mu.Unlock()
	}
}
