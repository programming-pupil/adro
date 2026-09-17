package event

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCommitIsCanonicalAndVerifiable(t *testing.T) {
	occurred := time.Date(2026, 9, 17, 12, 30, 0, 123456789, time.FixedZone("offset", 8*60*60))
	first, err := Commit(Uncommitted{
		StreamID: "session-1", EventType: "session.started", TenantID: "tenant-1", WorkspaceID: "workspace-1",
		Actor: Actor{Type: "user", ID: "user-1"}, CorrelationID: "session-1", OccurredAt: occurred,
		Classification: "internal", Payload: json.RawMessage(`{"b":1.0,"a":2}`),
	}, CommitMetadata{Sequence: 1, EventID: "event-1", CommittedAt: occurred.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Commit(Uncommitted{
		StreamID: "session-1", EventType: "session.started", TenantID: "tenant-1", WorkspaceID: "workspace-1",
		Actor: Actor{Type: "user", ID: "user-1"}, CorrelationID: "session-1", OccurredAt: occurred.UTC(),
		Classification: "internal", Payload: json.RawMessage(`{"a":2.0,"b":1}`),
	}, CommitMetadata{Sequence: 1, EventID: "event-1", CommittedAt: occurred.Add(time.Second).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if first.EnvelopeDigest != second.EnvelopeDigest || first.PayloadDigest != second.PayloadDigest {
		t.Fatalf("canonical digests differ: first=%+v second=%+v", first, second)
	}
	if err := first.Verify(); err != nil {
		t.Fatal(err)
	}
	first.Payload = json.RawMessage(`{"a":3}`)
	if err := first.Verify(); err == nil {
		t.Fatal("tampered payload passed verification")
	}
}
