package event

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	coreencoding "github.com/adro-project/adro/core/encoding"
)

func committedEvent(t *testing.T, sequence int64, previous string, payload string) Envelope {
	t.Helper()
	now := time.Date(2026, 9, 19, 1, 2, 3, 4_000, time.UTC).Add(time.Duration(sequence) * time.Second)
	item, err := Commit(Uncommitted{
		StreamID: "session-1", EventType: "counter.changed", TenantID: "tenant-1", WorkspaceID: "workspace-1",
		Actor: Actor{Type: "system", ID: "runtime-1"}, CorrelationID: "session-1", OccurredAt: now,
		Classification: "internal", Payload: json.RawMessage(payload),
	}, CommitMetadata{Sequence: sequence, EventID: "event-" + string(rune('0'+sequence)), CommittedAt: now, PreviousDigest: previous})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func TestValidateChainRejectsGapAndDigestTampering(t *testing.T) {
	first := committedEvent(t, 1, "", `{"delta":1}`)
	second := committedEvent(t, 2, first.EnvelopeDigest, `{"delta":2}`)
	if err := ValidateChain([]Envelope{first, second}); err != nil {
		t.Fatal(err)
	}
	gap := second
	gap.Sequence = 3
	if err := ValidateChain([]Envelope{first, gap}); err == nil {
		t.Fatal("sequence gap was accepted")
	}
	bad := second
	bad.Payload = json.RawMessage(`{"delta":9}`)
	if err := ValidateChain([]Envelope{first, bad}); err == nil {
		t.Fatal("payload tampering was accepted")
	}
}

func TestRegistryUpcastsWithoutMutatingStoredEnvelope(t *testing.T) {
	stored := committedEvent(t, 1, "", `{"delta":2}`)
	originalPayload := append([]byte(nil), stored.Payload...)
	originalDigest := stored.EnvelopeDigest
	registry, err := NewRegistry(2)
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
		return json.Marshal(map[string]any{"amount": old.Delta})
	}); err != nil {
		t.Fatal(err)
	}
	decoded, err := registry.Upcast(stored)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.OriginalDigest != originalDigest || len(decoded.UpcastPath) != 1 || decoded.UpcastPath[0] != 2 {
		t.Fatalf("upcast evidence=%+v", decoded)
	}
	if decoded.Envelope.SchemaVersion != 2 || string(decoded.Envelope.Payload) != `{"amount":2}` {
		t.Fatalf("derived envelope=%+v", decoded.Envelope)
	}
	if string(stored.Payload) != string(originalPayload) || stored.EnvelopeDigest != originalDigest {
		t.Fatal("upcast mutated stored envelope")
	}
	if err := stored.VerifyStored(); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Envelope.VerifyStored(); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryFailsClosedForMissingAndFutureVersions(t *testing.T) {
	stored := committedEvent(t, 1, "", `{"delta":2}`)
	registry, err := NewRegistry(2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Upcast(stored); !errors.Is(err, ErrUpcasterNotFound) {
		t.Fatalf("missing upcaster error=%v", err)
	}
	future := stored
	future.SchemaVersion = 3
	future.EnvelopeDigest, err = future.digest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Upcast(future); !errors.Is(err, ErrFutureSchema) {
		t.Fatalf("future schema error=%v", err)
	}
	if err := registry.Register("counter.changed", 2, 1, func(json.RawMessage) (json.RawMessage, error) { return nil, nil }); !errors.Is(err, ErrUpcasterInvalid) {
		t.Fatalf("downgrade registration error=%v", err)
	}
}

func TestDecodePayloadUnknownFieldPolicy(t *testing.T) {
	type payload struct {
		Amount int `json:"amount"`
	}
	if _, err := DecodePayload[payload](json.RawMessage(`{"amount":2,"future":true}`), RejectUnknownFields); err == nil {
		t.Fatal("unknown field was accepted under reject policy")
	}
	decoded, err := DecodePayload[payload](json.RawMessage(`{"amount":2,"future":true}`), PreserveUnknownFields)
	if err != nil || decoded.Amount != 2 {
		t.Fatalf("preserve decode=%+v err=%v", decoded, err)
	}
	if _, err := coreencoding.Canonicalize(json.RawMessage(`{"amount":2}`)); err != nil {
		t.Fatal(err)
	}
}
