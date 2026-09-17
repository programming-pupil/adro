package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	coreencoding "github.com/adro-project/adro/core/encoding"
	coreevent "github.com/adro-project/adro/core/event"
	"github.com/adro-project/adro/ports/eventstore"
)

const defaultShadowTimeout = 2 * time.Second

var ErrShadowDiverged = errors.New("runtime EventStore shadow projection diverged")

// ShadowReport compares the legacy runtime journal with its EventStore shadow.
// It is diagnostic evidence only and never authorizes execution or effects.
type ShadowReport struct {
	Scope        Scope     `json:"scope"`
	StreamID     string    `json:"stream_id"`
	LegacyCount  int64     `json:"legacy_count"`
	ShadowCount  int64     `json:"shadow_count"`
	LegacyDigest string    `json:"legacy_digest,omitempty"`
	ShadowDigest string    `json:"shadow_digest,omitempty"`
	Diverged     bool      `json:"diverged"`
	DivergenceAt int64     `json:"divergence_at,omitempty"`
	Error        string    `json:"error,omitempty"`
	CheckedAt    time.Time `json:"checked_at"`
}

func (r ShadowReport) Matched() bool {
	return r.Error == "" && !r.Diverged && r.LegacyCount == r.ShadowCount && r.LegacyDigest == r.ShadowDigest
}

// EventShadow is the migration-only sink used by the legacy journal. Mirror
// implementations must not publish outbox messages or execute side effects.
type EventShadow interface {
	Mirror(context.Context, Scope, []Event) ShadowReport
}

type EventStoreShadow struct {
	store eventstore.Store
	mu    sync.Mutex
}

func NewEventStoreShadow(store eventstore.Store) (*EventStoreShadow, error) {
	if store == nil {
		return nil, errors.New("runtime EventStore shadow requires a store")
	}
	return &EventStoreShadow{store: store}, nil
}

func (s *EventStoreShadow) Mirror(ctx context.Context, scope Scope, legacy []Event) ShadowReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	report := ShadowReport{
		Scope: scope, StreamID: ShadowStreamID(scope), LegacyCount: int64(len(legacy)), CheckedAt: time.Now().UTC(),
	}
	legacy = cloneEvents(legacy)
	report.LegacyDigest = projectionDigest(legacy, &report)
	if report.Error != "" {
		return report
	}

	head, _, err := s.store.Head(ctx, report.StreamID)
	if err != nil {
		report.Error = fmt.Sprintf("read EventStore shadow head: %v", err)
		return report
	}
	if head > int64(len(legacy)) {
		report.ShadowCount = head
		report.Diverged = true
		report.DivergenceAt = int64(len(legacy)) + 1
		report.Error = ErrShadowDiverged.Error()
		return report
	}
	if head > 0 {
		prefix, err := s.readProjection(ctx, scope, report.StreamID)
		if err != nil {
			report.Error = err.Error()
			return report
		}
		report.ShadowCount = int64(len(prefix))
		report.ShadowDigest = projectionDigest(prefix, &report)
		if report.Error != "" {
			return report
		}
		if divergence := firstDivergence(legacy[:head], prefix); divergence != 0 {
			report.Diverged = true
			report.DivergenceAt = divergence
			report.Error = ErrShadowDiverged.Error()
			return report
		}
	}
	if head < int64(len(legacy)) {
		request := eventstore.AppendRequest{StreamID: report.StreamID, ExpectedSequence: head}
		for _, item := range legacy[head:] {
			mapped, mapErr := mapShadowEvent(report.StreamID, item)
			if mapErr != nil {
				report.Error = mapErr.Error()
				return report
			}
			request.Events = append(request.Events, mapped)
		}
		if _, err := s.store.Append(ctx, request); err != nil {
			report.Error = fmt.Sprintf("append EventStore shadow: %v", err)
			return report
		}
	}

	shadow, err := s.readProjection(ctx, scope, report.StreamID)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	report.ShadowCount = int64(len(shadow))
	report.ShadowDigest = projectionDigest(shadow, &report)
	if report.Error != "" {
		return report
	}
	report.DivergenceAt = firstDivergence(legacy, shadow)
	if report.LegacyCount != report.ShadowCount || report.LegacyDigest != report.ShadowDigest || report.DivergenceAt != 0 {
		report.Diverged = true
		report.Error = ErrShadowDiverged.Error()
	}
	return report
}

func (s *EventStoreShadow) readProjection(ctx context.Context, scope Scope, streamID string) ([]Event, error) {
	const pageSize = 1000
	result := make([]Event, 0)
	for after := int64(0); ; {
		batch, err := s.store.Read(ctx, streamID, after, pageSize)
		if err != nil {
			return nil, fmt.Errorf("read EventStore shadow projection: %w", err)
		}
		if len(batch) == 0 {
			return result, nil
		}
		for _, envelope := range batch {
			if envelope.TenantID != scope.TenantID || envelope.WorkspaceID != scope.WorkspaceID {
				return nil, fmt.Errorf("%w: shadow event scope mismatch at sequence %d", ErrShadowDiverged, envelope.Sequence)
			}
			var item Event
			if err := json.Unmarshal(envelope.Payload, &item); err != nil {
				return nil, fmt.Errorf("decode EventStore shadow payload at sequence %d: %w", envelope.Sequence, err)
			}
			if item.Scope != scope {
				return nil, fmt.Errorf("%w: shadow payload scope mismatch at sequence %d", ErrShadowDiverged, envelope.Sequence)
			}
			result = append(result, item)
			after = envelope.Sequence
		}
		if len(batch) < pageSize {
			return result, nil
		}
	}
}

func mapShadowEvent(streamID string, legacy Event) (coreevent.Uncommitted, error) {
	payload, err := coreencoding.Marshal(legacy)
	if err != nil {
		return coreevent.Uncommitted{}, fmt.Errorf("encode legacy runtime shadow event: %w", err)
	}
	occurredAt := legacy.CreatedAt
	if occurredAt.IsZero() {
		occurredAt = legacy.CommittedAt
	}
	correlationID := legacy.CorrelationID
	if correlationID == "" {
		correlationID = legacy.EventID
	}
	return coreevent.Uncommitted{
		StreamID: streamID, EventType: legacy.EventType,
		TenantID: legacy.TenantID, WorkspaceID: legacy.WorkspaceID,
		Actor:         coreevent.Actor{Type: "migration", ID: "shadow-migrator"},
		CorrelationID: correlationID, CausationID: legacy.CausationID,
		IdempotencyKey: "legacy-event:" + legacy.EventID, FencingToken: legacy.FencingToken,
		OccurredAt: occurredAt, Classification: "internal", Payload: payload,
	}, nil
}

func ShadowStreamID(scope Scope) string {
	digest := sha256.Sum256([]byte(scope.TenantID + "\x00" + scope.WorkspaceID + "\x00" + scope.SessionID + "\x00" + scope.RunID))
	return "runtime-shadow-" + hex.EncodeToString(digest[:16])
}

func projectionDigest(events []Event, report *ShadowReport) string {
	digest, err := coreencoding.Digest(events)
	if err != nil {
		report.Error = fmt.Sprintf("digest runtime shadow projection: %v", err)
		return ""
	}
	return digest
}

func firstDivergence(legacy, shadow []Event) int64 {
	limit := len(legacy)
	if len(shadow) < limit {
		limit = len(shadow)
	}
	for index := 0; index < limit; index++ {
		left, leftErr := coreencoding.Marshal(legacy[index])
		right, rightErr := coreencoding.Marshal(shadow[index])
		if leftErr != nil || rightErr != nil || string(left) != string(right) {
			return int64(index + 1)
		}
	}
	if len(legacy) != len(shadow) {
		return int64(limit + 1)
	}
	return 0
}

func cloneEvents(events []Event) []Event {
	result := make([]Event, len(events))
	for index, item := range events {
		result[index] = cloneEvent(item)
	}
	return result
}

func sortedScopes(events []Event) []Scope {
	byKey := make(map[string]Scope)
	for _, item := range events {
		byKey[shadowScopeKey(item.Scope)] = item.Scope
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]Scope, 0, len(keys))
	for _, key := range keys {
		result = append(result, byKey[key])
	}
	return result
}

func shadowScopeKey(scope Scope) string {
	return scope.TenantID + "\x00" + scope.WorkspaceID + "\x00" + scope.SessionID + "\x00" + scope.RunID
}
