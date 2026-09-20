package eventstore_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	storeadapter "github.com/adro-project/adro/adapters/eventstore/sqlite"
	policyadapter "github.com/adro-project/adro/adapters/policy/eventstore"
	corepolicy "github.com/adro-project/adro/core/policy"
	"github.com/adro-project/adro/core/testkit"
)

func TestRecorderPersistsIdempotentDecisionEvidence(t *testing.T) {
	clock := testkit.NewManualClock(time.Date(2026, 9, 19, 2, 0, 0, 0, time.UTC))
	store, err := storeadapter.Open(filepath.Join(t.TempDir(), "events.db"), storeadapter.Options{
		Clock: clock, IDs: &testkit.SequenceIDs{}, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	recorder, err := policyadapter.New(store)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := corepolicy.FreezeBundle(corepolicy.Bundle{
		ID: "egress", Version: "v1", TenantID: "tenant", WorkspaceID: "workspace",
		Capabilities: []string{"events.publish"},
		Egress:       []corepolicy.EgressRule{{Destination: "https://api.example.test/events", Purpose: "delivery", MaxSensitivity: corepolicy.SensitivityInternal}},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := corepolicy.Input{
		TenantID: "tenant", WorkspaceID: "workspace", ActorID: "worker", Capability: "events.publish",
		Destination: "https://api.example.test/events", Purpose: "delivery", Sensitivity: corepolicy.SensitivityInternal,
	}
	decision, err := corepolicy.EvaluateFailClosed(context.Background(), corepolicy.BuiltinEvaluator{}, bundle, input, clock.Now(), time.Second, corepolicy.EngineVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordDecision(context.Background(), decision); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordDecision(context.Background(), decision); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	digest, err := corepolicy.DecisionDigest(decision)
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.Read(context.Background(), "policy:"+digest, 0, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	if events[0].EventType != policyadapter.EventTypeDecisionRecorded || events[0].TenantID != "tenant" || events[0].WorkspaceID != "workspace" || events[0].Classification != "internal" {
		t.Fatalf("event=%+v", events[0])
	}
	var restored corepolicy.DecisionRecord
	if err := json.Unmarshal(events[0].Payload, &restored); err != nil || restored != decision {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
	read, err := recorder.ReadDecision(context.Background(), digest)
	if err != nil || read != decision {
		t.Fatalf("read=%+v err=%v", read, err)
	}
}
