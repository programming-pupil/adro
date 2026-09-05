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
	"github.com/adro-project/adro/internal/telemetry"
)

type Envelope struct {
	EventID       string `json:"event_id"`
	SchemaVersion int    `json:"schema_version"`
	// Sequence is the durable append position assigned by the event bus.
	Sequence         int64     `json:"sequence"`
	EventType        string    `json:"event_type"`
	AggregateType    string    `json:"aggregate_type"`
	AggregateID      string    `json:"aggregate_id"`
	AggregateVersion int64     `json:"aggregate_version"`
	TenantID         string    `json:"tenant_id"`
	WorkspaceID      string    `json:"workspace_id"`
	CorrelationID    string    `json:"correlation_id"`
	CausationID      string    `json:"causation_id,omitempty"`
	TraceParent      string    `json:"traceparent,omitempty"`
	TraceState       string    `json:"tracestate,omitempty"`
	Provider         string    `json:"provider,omitempty"`
	ProviderEventID  string    `json:"provider_event_id,omitempty"`
	OccurredAt       time.Time `json:"occurred_at"`
	Classification   string    `json:"classification"`
	PayloadHash      string    `json:"payload_hash"`
	// EnvelopeHash authenticates every field in the envelope, including
	// sequence and scope. PayloadHash remains separately useful for payload
	// addressing and backwards compatibility with older snapshots.
	PreviousHash string         `json:"previous_hash,omitempty"`
	EnvelopeHash string         `json:"envelope_hash,omitempty"`
	Payload      map[string]any `json:"payload"`
}

func New(eventType, aggregateType, aggregateID, tenantID, workspaceID string, version int64, payload map[string]any) Envelope {
	event := Envelope{
		EventID: domain.NewID(), SchemaVersion: 1, EventType: eventType, AggregateType: aggregateType,
		AggregateID: aggregateID, AggregateVersion: version, TenantID: tenantID,
		WorkspaceID: workspaceID, CorrelationID: aggregateID, OccurredAt: time.Now().UTC(),
		Classification: "internal", PayloadHash: payloadHash(payload), Payload: payload,
	}
	event.EnvelopeHash = envelopeHash(event)
	return event
}

// NewWithContext binds the immutable event to the current W3C trace without
// copying prompts, secrets, or arbitrary baggage into the event envelope.
func NewWithContext(ctx context.Context, eventType, aggregateType, aggregateID, tenantID, workspaceID string, version int64, payload map[string]any) Envelope {
	event := New(eventType, aggregateType, aggregateID, tenantID, workspaceID, version, payload)
	event.TraceParent, event.TraceState = telemetry.Carrier(ctx)
	event.EnvelopeHash = envelopeHash(event)
	return event
}

type Publisher interface {
	Publish(context.Context, Envelope) error
}

type RetentionPolicy struct {
	MaxEvents int           `json:"max_events,omitempty"`
	MaxAge    time.Duration `json:"max_age,omitempty"`
}

var ErrInvalidCursor = errors.New("event cursor is invalid or expired")

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
	retention    RetentionPolicy
}

type streamGap struct {
	count         int64
	from          int64
	to            int64
	tenantID      string
	workspaceID   string
	aggregateID   string
	correlationID string
}

type persistedEvents struct {
	Revision  int64             `json:"revision"`
	Events    []Envelope        `json:"events"`
	Acks      map[string]string `json:"acks,omitempty"`
	Retention RetentionPolicy   `json:"retention,omitempty"`
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
		b.retention = persisted.Retention
		if b.acks == nil {
			b.acks = make(map[string]string)
		}
	} else if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("decode event state: %w", err)
	}
	if err := validatePersistedEvents(stored); err != nil {
		return nil, err
	}
	for _, event := range stored {
		computed := payloadHash(event.Payload)
		if event.PayloadHash != "" && event.PayloadHash != computed {
			return nil, fmt.Errorf("event %s payload hash mismatch", event.EventID)
		}
		event.PayloadHash = computed
		if event.EnvelopeHash == "" {
			event.EnvelopeHash = envelopeHash(event)
		}
		b.events = append(b.events, event)
		b.seen[event.EventID] = struct{}{}
		if event.Provider != "" && event.ProviderEventID != "" {
			b.seenProvider[event.Provider+"\x00"+event.ProviderEventID] = struct{}{}
		}
	}
	return b, nil
}

// SetRetention configures bounded local history. Acknowledged cursors that
// fall outside the retention horizon intentionally return ErrInvalidCursor so
// consumers perform a full resync instead of silently skipping events.
func (b *Bus) SetRetention(policy RetentionPolicy) error {
	if policy.MaxEvents < 0 || policy.MaxAge < 0 {
		return errors.New("retention limits cannot be negative")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.retention = policy
	b.pruneLocked(time.Now().UTC())
	return b.persistLocked()
}

func (b *Bus) Retention() RetentionPolicy { b.mu.RLock(); defer b.mu.RUnlock(); return b.retention }

func (b *Bus) Prune(now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(now)
	return b.persistLocked()
}

func (b *Bus) pruneLocked(now time.Time) {
	if b.retention.MaxEvents <= 0 && b.retention.MaxAge <= 0 {
		return
	}
	start := 0
	if b.retention.MaxAge > 0 {
		cutoff := now.Add(-b.retention.MaxAge)
		for start < len(b.events) && b.events[start].OccurredAt.Before(cutoff) {
			start++
		}
	}
	if b.retention.MaxEvents > 0 && len(b.events)-start > b.retention.MaxEvents {
		start = len(b.events) - b.retention.MaxEvents
	}
	if start <= 0 {
		return
	}
	b.events = append([]Envelope(nil), b.events[start:]...)
	b.seen = make(map[string]struct{}, len(b.events))
	b.seenProvider = make(map[string]struct{})
	for _, event := range b.events {
		b.seen[event.EventID] = struct{}{}
		if event.Provider != "" && event.ProviderEventID != "" {
			b.seenProvider[event.Provider+"\x00"+event.ProviderEventID] = struct{}{}
		}
	}
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
		localEvents := append([]Envelope(nil), b.events...)
		if disk, err := readPersistedEvents(b.statePath); err != nil {
			return err
		} else if disk != nil && disk.revision != b.revision {
			if err := validatePersistedEvents(disk.events); err != nil {
				return err
			}
			// A local event may have been assigned a tentative sequence before a
			// peer committed. Rebase only those uncommitted records onto the peer
			// tail. Persisted events are an immutable prefix: their sequence,
			// previous_hash, and envelope_hash must survive a peer merge exactly.
			var mergeErr error
			b.events, mergeErr = mergePeerEventsPreservingCommitted(disk.events, localEvents)
			if mergeErr != nil {
				return mergeErr
			}
			if b.acks == nil {
				b.acks = make(map[string]string)
			}
			for consumer, cursor := range disk.acks {
				b.acks[consumer] = cursor
			}
			rebuildSeen(b)
			b.revision = disk.revision
		}
		if err := validatePersistedEvents(b.events); err != nil {
			return err
		}
		eventsCopy := append([]Envelope(nil), b.events...)
		if eventsCopy == nil {
			eventsCopy = []Envelope{}
		}
		next := persistedEvents{Revision: b.revision + 1, Events: eventsCopy, Acks: b.acks, Retention: b.retention}
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

// mergePeerEventsPreservingCommitted treats the peer snapshot as the committed
// prefix and appends only events that are not already present there. The local
// process may have calculated a provisional sequence/hash before discovering a
// peer write; those new tail events are resealed after rebasing. Existing
// committed events are never resealed, so their hashes remain suitable for
// audit references and external evidence archives.
func mergePeerEventsPreservingCommitted(committed, local []Envelope) ([]Envelope, error) {
	merged := make([]Envelope, 0, len(committed)+len(local))
	seen := make(map[string]struct{}, len(committed)+len(local))
	for _, event := range committed {
		if event.EventID == "" {
			return nil, errors.New("committed event has empty event_id")
		}
		if _, exists := seen[event.EventID]; exists {
			return nil, fmt.Errorf("duplicate committed event id %s", event.EventID)
		}
		seen[event.EventID] = struct{}{}
		merged = append(merged, cloneEnvelope(event))
	}
	sequence := maxSequence(committed)
	previousHash := ""
	if len(committed) > 0 {
		previousHash = committed[len(committed)-1].EnvelopeHash
	}
	for _, event := range local {
		if event.EventID == "" {
			return nil, errors.New("local event has empty event_id")
		}
		if _, exists := seen[event.EventID]; exists {
			continue
		}
		sequence++
		event = cloneEnvelope(event)
		event.Sequence = sequence
		event.PreviousHash = previousHash
		event.PayloadHash = payloadHash(event.Payload)
		if event.PayloadHash == "" {
			return nil, fmt.Errorf("local event %s payload is not JSON encodable", event.EventID)
		}
		event.EnvelopeHash = envelopeHash(event)
		merged = append(merged, event)
		seen[event.EventID] = struct{}{}
		previousHash = event.EnvelopeHash
	}
	return merged, nil
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
	b.retention = persisted.retention
	if err := validatePersistedEvents(b.events); err != nil {
		return err
	}
	rebuildSeen(b)
	return nil
}

func readPersistedEvents(path string) (*struct {
	revision  int64
	events    []Envelope
	acks      map[string]string
	retention RetentionPolicy
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
			revision  int64
			events    []Envelope
			acks      map[string]string
			retention RetentionPolicy
		}{persisted.Revision, persisted.Events, persisted.Acks, persisted.Retention}, nil
	}
	var events []Envelope
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("decode event state: %w", err)
	}
	return &struct {
		revision  int64
		events    []Envelope
		acks      map[string]string
		retention RetentionPolicy
	}{0, events, nil, RetentionPolicy{}}, nil
}

func payloadHash(payload map[string]any) string {
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func envelopeHash(event Envelope) string {
	event.EnvelopeHash = ""
	data, err := json.Marshal(event)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func validatePersistedEvents(items []Envelope) error {
	if len(items) == 0 {
		return nil
	}
	legacySequences := true
	for _, event := range items {
		if event.Sequence != 0 {
			legacySequences = false
			break
		}
	}
	if legacySequences {
		for i := range items {
			items[i].Sequence = int64(i + 1)
		}
	}
	seen := make(map[string]struct{}, len(items))
	chainMetadata := false
	for _, event := range items {
		if event.PreviousHash != "" {
			chainMetadata = true
			break
		}
	}
	previousHash := ""
	baseSequence := items[0].Sequence
	if baseSequence < 1 {
		baseSequence = 1
	}
	for i := range items {
		expected := baseSequence + int64(i)
		if items[i].Sequence != expected {
			return fmt.Errorf("event sequence corruption at index %d: got %d want %d", i, items[i].Sequence, expected)
		}
		if strings.TrimSpace(items[i].EventID) == "" {
			return fmt.Errorf("event at sequence %d has empty event_id", expected)
		}
		if _, duplicate := seen[items[i].EventID]; duplicate {
			return fmt.Errorf("duplicate event id %s", items[i].EventID)
		}
		seen[items[i].EventID] = struct{}{}
		if items[i].SchemaVersion == 0 {
			items[i].SchemaVersion = 1
		}
		if items[i].SchemaVersion != 1 {
			return fmt.Errorf("event %s has unsupported schema_version %d", items[i].EventID, items[i].SchemaVersion)
		}
		if items[i].PayloadHash != "" && items[i].PayloadHash != payloadHash(items[i].Payload) {
			return fmt.Errorf("event %s payload hash mismatch", items[i].EventID)
		}
		if items[i].TraceParent != "" || items[i].TraceState != "" {
			if _, err := telemetry.ParseTraceParent(items[i].TraceParent, items[i].TraceState); err != nil {
				return fmt.Errorf("event %s trace context: %w", items[i].EventID, err)
			}
		}
		if items[i].EnvelopeHash != "" && items[i].EnvelopeHash != envelopeHash(items[i]) {
			return fmt.Errorf("event %s envelope hash mismatch", items[i].EventID)
		}
		if items[i].EnvelopeHash == "" {
			items[i].EnvelopeHash = envelopeHash(items[i])
		}
		if chainMetadata && i > 0 && items[i].PreviousHash != previousHash {
			return fmt.Errorf("event previous hash mismatch at sequence %d", expected)
		}
		previousHash = items[i].EnvelopeHash
	}
	return nil
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
	if cursor != "" {
		b.mu.RLock()
		found := false
		for _, event := range b.events {
			if event.EventID == cursor {
				found = true
				if aggregateID != "" && event.AggregateID != aggregateID {
					b.mu.RUnlock()
					return nil, "", fmt.Errorf("%w: cursor belongs to a different aggregate", ErrInvalidCursor)
				}
				break
			}
		}
		b.mu.RUnlock()
		if !found {
			return nil, "", ErrInvalidCursor
		}
	}
	return b.ListChecked(aggregateID, cursor, limit)
}

// AckScoped records a cursor under an explicit stream scope. The legacy Ack
// API remains available for callers that already namespace consumer IDs.
func (b *Bus) AckScoped(consumerID, tenantID, workspaceID, aggregateID, eventID string) error {
	if b.statePath != "" {
		if err := b.Reload(); err != nil {
			return err
		}
	}
	b.mu.RLock()
	var matched bool
	for _, event := range b.events {
		if event.EventID == eventID {
			matched = (tenantID == "" || event.TenantID == tenantID) && event.WorkspaceID == workspaceID && (aggregateID == "" || event.AggregateID == aggregateID)
			break
		}
	}
	b.mu.RUnlock()
	if !matched {
		return fmt.Errorf("%w: event does not belong to scoped stream", ErrInvalidCursor)
	}
	consumerID = scopedConsumerID(consumerID, tenantID, workspaceID, aggregateID)
	return b.Ack(consumerID, eventID)
}

// ReplayScoped prevents a cursor acknowledged for one tenant/workspace/
// aggregate from being reused on another stream.
func (b *Bus) ReplayScoped(consumerID, tenantID, workspaceID, aggregateID, cursor string, limit int) ([]Envelope, string, error) {
	if b.statePath != "" {
		if err := b.Reload(); err != nil {
			return nil, "", err
		}
	}
	if limit <= 0 || limit > 250 {
		limit = 100
	}
	scopedConsumer := scopedConsumerID(consumerID, tenantID, workspaceID, aggregateID)
	b.mu.RLock()
	defer b.mu.RUnlock()
	effectiveCursor := strings.TrimSpace(cursor)
	if effectiveCursor == "" && consumerID != "" {
		effectiveCursor = b.acks[scopedConsumer]
	}
	start := -1
	if effectiveCursor != "" {
		for i, event := range b.events {
			if event.EventID != effectiveCursor {
				continue
			}
			if event.WorkspaceID != workspaceID || (tenantID != "" && event.TenantID != tenantID) || (aggregateID != "" && event.AggregateID != aggregateID) {
				return nil, "", fmt.Errorf("%w: cursor does not belong to scoped stream", ErrInvalidCursor)
			}
			start = i
			break
		}
		if start < 0 {
			return nil, "", ErrInvalidCursor
		}
	}
	items := make([]Envelope, 0, limit)
	for _, event := range b.events[start+1:] {
		if event.WorkspaceID != workspaceID || (tenantID != "" && event.TenantID != tenantID) || (aggregateID != "" && event.AggregateID != aggregateID) {
			continue
		}
		items = append(items, cloneEnvelope(event))
		if len(items) == limit {
			break
		}
	}
	next := ""
	if len(items) == limit {
		next = items[len(items)-1].EventID
	}
	return items, next, nil
}

func scopedConsumerID(consumerID, tenantID, workspaceID, aggregateID string) string {
	return strings.Join([]string{consumerID, tenantID, workspaceID, aggregateID}, "\x00")
}

func (b *Bus) Publish(_ context.Context, event Envelope) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	oldEvents := append([]Envelope(nil), b.events...)
	if event.EventID == "" {
		event.EventID = domain.NewID()
	}
	// Payload maps are mutable reference values. Clone before retaining or
	// delivering an envelope so a caller or subscriber cannot rewrite the
	// replay history after publication.
	event = cloneEnvelope(event)
	if event.SchemaVersion == 0 {
		event.SchemaVersion = 1
	}
	if event.SchemaVersion != 1 {
		return fmt.Errorf("unsupported event schema_version %d", event.SchemaVersion)
	}
	computedPayloadHash := payloadHash(event.Payload)
	if computedPayloadHash == "" {
		return errors.New("event payload is not JSON encodable")
	}
	if event.PayloadHash != "" && event.PayloadHash != computedPayloadHash {
		return errors.New("event payload hash does not match payload")
	}
	event.PayloadHash = computedPayloadHash
	if event.EnvelopeHash != "" && event.EnvelopeHash != envelopeHash(event) {
		return errors.New("event envelope hash does not match envelope")
	}
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
	if len(b.events) > 0 {
		event.PreviousHash = b.events[len(b.events)-1].EnvelopeHash
	}
	event.EnvelopeHash = envelopeHash(event)
	b.events = append(b.events, event)
	b.pruneLocked(time.Now().UTC())
	if err := b.persistLocked(); err != nil {
		b.events = oldEvents
		b.rebuildSeenLocked()
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
				AggregateType: "stream", AggregateID: gap.aggregateID, TenantID: gap.tenantID,
				WorkspaceID: gap.workspaceID, CorrelationID: gap.correlationID, OccurredAt: time.Now().UTC(),
				Classification: "system", Payload: map[string]any{"dropped_count": gap.count, "from_sequence": gap.from, "to_sequence": gap.to},
			}
			gapEvent.PayloadHash = payloadHash(gapEvent.Payload)
			gapEvent.EnvelopeHash = envelopeHash(gapEvent)
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
				gap.tenantID = event.TenantID
				gap.workspaceID = event.WorkspaceID
				gap.aggregateID = event.AggregateID
				gap.correlationID = event.CorrelationID
			}
			gap.to = event.Sequence
			b.dropped[id] = gap
		}
	}
	return nil
}

func (b *Bus) rebuildSeenLocked() {
	b.seen = make(map[string]struct{}, len(b.events))
	b.seenProvider = make(map[string]struct{})
	for _, retained := range b.events {
		b.seen[retained.EventID] = struct{}{}
		if retained.Provider != "" && retained.ProviderEventID != "" {
			b.seenProvider[retained.Provider+"\x00"+retained.ProviderEventID] = struct{}{}
		}
	}
}

func (b *Bus) List(aggregateID, cursor string, limit int) ([]Envelope, string) {
	items, next, _ := b.ListChecked(aggregateID, cursor, limit)
	return items, next
}

// Count returns the exact number of retained events in a stream. It is used
// for gauges and aggregation and intentionally does not impose replay's page
// limit.
func (b *Bus) Count(aggregateID string) int {
	if b == nil {
		return 0
	}
	if b.statePath != "" {
		_ = b.Reload()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if aggregateID == "" {
		return len(b.events)
	}
	count := 0
	for _, event := range b.events {
		if event.AggregateID == aggregateID {
			count++
		}
	}
	return count
}

// CountByType returns an exact retained-event count without replay pagination.
func (b *Bus) CountByType(eventType string) int {
	if b == nil {
		return 0
	}
	if b.statePath != "" {
		_ = b.Reload()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	count := 0
	for _, event := range b.events {
		if event.EventType == eventType {
			count++
		}
	}
	return count
}

// HasEvent reports whether a scoped event with the supplied dedupe key was
// already committed. It is used by render-only projections whose retry must
// not rebroadcast the same immutable comment revision.
func (b *Bus) HasEvent(eventType, aggregateID, dedupeKey string) bool {
	if b == nil {
		return false
	}
	if b.statePath != "" {
		_ = b.Reload()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, event := range b.events {
		if event.EventType != eventType || event.AggregateID != aggregateID {
			continue
		}
		if value, ok := event.Payload["dedupe_key"].(string); ok && value == dedupeKey {
			return true
		}
	}
	return false
}

// ListChecked is the fail-closed cursor variant used by API/replay callers.
// An unknown cursor is never treated as an instruction to replay from the
// beginning, which would otherwise hide retention gaps or stale clients.
func (b *Bus) ListChecked(aggregateID, cursor string, limit int) ([]Envelope, string, error) {
	if b.statePath != "" {
		b.mu.Lock()
		if err := b.reloadLocked(); err != nil {
			b.mu.Unlock()
			return nil, "", err
		}
		b.mu.Unlock()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit <= 0 || limit > 250 {
		limit = 100
	}
	start := 0
	if cursor != "" {
		found := false
		for i, e := range b.events {
			if e.EventID == cursor {
				if aggregateID != "" && e.AggregateID != aggregateID {
					return nil, "", fmt.Errorf("%w: cursor belongs to a different aggregate", ErrInvalidCursor)
				}
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return nil, "", ErrInvalidCursor
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
	return items, next, nil
}

// ReplayRange returns an explicit sequence interval for gap repair. Sequence
// numbers are global and retained history is never synthesized when a range
// has already expired.
func (b *Bus) ReplayRange(tenantID, workspaceID, aggregateID string, from, to int64) ([]Envelope, error) {
	if from < 1 || to < from {
		return nil, ErrInvalidCursor
	}
	if b.statePath != "" {
		if err := b.Reload(); err != nil {
			return nil, err
		}
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]Envelope, 0)
	retained := 0
	for _, event := range b.events {
		if event.Sequence < from || event.Sequence > to {
			continue
		}
		retained++
		if tenantID != "" && event.TenantID != tenantID || workspaceID != "" && event.WorkspaceID != workspaceID || aggregateID != "" && event.AggregateID != aggregateID {
			continue
		}
		result = append(result, cloneEnvelope(event))
	}
	// Sequence numbers are global to the bus. A workspace/aggregate stream may
	// legitimately have other scopes interleaved in the requested interval, so
	// validate retention against the unfiltered global count and return only the
	// requested scope.
	if retained != int(to-from+1) {
		return nil, ErrInvalidCursor
	}
	return result, nil
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
