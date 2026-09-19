package reducer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adro-project/adro/core"
	coreencoding "github.com/adro-project/adro/core/encoding"
	"github.com/adro-project/adro/core/event"
	"github.com/adro-project/adro/core/testkit"
)

type counterReducer struct{}

type counterCommand struct{ Delta int }

func (counterReducer) Decide(_ context.Context, state int, command counterCommand, dependencies core.Dependencies) ([]event.Uncommitted, error) {
	payload, err := json.Marshal(map[string]any{
		"delta": command.Delta,
		"state": state,
		"nonce": dependencies.Random.Uint64(),
	})
	if err != nil {
		return nil, err
	}
	return []event.Uncommitted{{
		StreamID: "session-1", EventType: "counter.changed", TenantID: "tenant-1", WorkspaceID: "workspace-1",
		Actor: event.Actor{Type: "system", ID: "runtime-1"}, CorrelationID: "session-1",
		OccurredAt: dependencies.Clock.Now(), Classification: "internal", Payload: payload,
	}}, nil
}

func (counterReducer) Apply(state int, envelope event.Envelope) (int, error) {
	var payload struct {
		Delta int `json:"delta"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return state, err
	}
	return state + payload.Delta, nil
}

func TestReducerIsByteDeterministicWithFixedDependencies(t *testing.T) {
	decide := func() []event.Uncommitted {
		dependencies := core.Dependencies{
			Clock: testkit.NewManualClock(time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)),
			IDs:   &testkit.SequenceIDs{}, Random: testkit.NewSequenceRandom(42),
			Sleeper: &testkit.RecordingSleeper{}, Backoff: testkit.FixedBackoff(time.Second),
		}
		events, err := (counterReducer{}).Decide(context.Background(), 3, counterCommand{Delta: 2}, dependencies)
		if err != nil {
			t.Fatal(err)
		}
		return events
	}
	first, err := coreencoding.Marshal(decide())
	if err != nil {
		t.Fatal(err)
	}
	second, err := coreencoding.Marshal(decide())
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("decisions differ:\n%s\n%s", first, second)
	}
}

func TestReplayAppliesEventsInSequence(t *testing.T) {
	now := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	committed, err := event.Commit(event.Uncommitted{
		StreamID: "session-1", EventType: "counter.changed", TenantID: "tenant-1", WorkspaceID: "workspace-1",
		Actor: event.Actor{Type: "system", ID: "runtime-1"}, CorrelationID: "session-1",
		OccurredAt: now, Classification: "internal", Payload: json.RawMessage(`{"delta":2}`),
	}, event.CommitMetadata{Sequence: 1, EventID: "event-1", CommittedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	state, err := Replay[int, counterCommand](counterReducer{}, 3, []event.Envelope{committed})
	if err != nil || state != 5 {
		t.Fatalf("state=%d err=%v", state, err)
	}
}

func TestReplayVerifiedUpcastsAndReportsImmutableEvidence(t *testing.T) {
	now := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	first, err := event.Commit(event.Uncommitted{
		StreamID: "session-1", EventType: "counter.changed", TenantID: "tenant-1", WorkspaceID: "workspace-1",
		Actor: event.Actor{Type: "system", ID: "runtime-1"}, CorrelationID: "session-1", OccurredAt: now,
		Classification: "internal", Payload: json.RawMessage(`{"delta":2}`),
	}, event.CommitMetadata{Sequence: 1, EventID: "event-1", CommittedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := event.NewRegistry(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("counter.changed", 1, 2, func(raw json.RawMessage) (json.RawMessage, error) {
		var old struct {
			Delta int `json:"delta"`
		}
		if err := json.Unmarshal(raw, &old); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"delta": old.Delta})
	}); err != nil {
		t.Fatal(err)
	}
	// The reducer intentionally accepts the normalized field after migration.
	reducer := normalizedCounterReducer{}
	state, evidence, err := ReplayVerified[int, struct{}](reducer, 3, []event.Envelope{first}, registry)
	if err != nil || state != 5 {
		t.Fatalf("state=%d evidence=%+v err=%v", state, evidence, err)
	}
	if evidence.LastSequence != 1 || evidence.StateDigest == "" || len(evidence.OriginalEventDigests) != 1 || len(evidence.UpcastPath["event-1"]) != 1 {
		t.Fatalf("evidence=%+v", evidence)
	}
	if err := first.Verify(); err != nil {
		t.Fatal(err)
	}
}

type normalizedCounterReducer struct{}

func (normalizedCounterReducer) Decide(context.Context, int, struct{}, core.Dependencies) ([]event.Uncommitted, error) {
	return nil, nil
}

func (normalizedCounterReducer) Apply(state int, envelope event.Envelope) (int, error) {
	var payload struct {
		Delta int `json:"delta"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return state, err
	}
	return state + payload.Delta, nil
}
