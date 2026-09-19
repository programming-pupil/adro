package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/adro-project/adro/internal/orchestration"
)

func TestRedactOrchestrationEventsRemovesSensitivePayloads(t *testing.T) {
	const canary = "orchestration-canary-plaintext"
	payload, err := json.Marshal(map[string]any{
		"safe":   "visible",
		"prompt": canary,
		"nested": map[string]any{
			"authorization": "Bearer " + canary,
			"secret_ref":    "secret:vault/orchestration",
		},
		"classified": map[string]any{
			"sensitivity": "restricted",
			"value":       canary,
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	redacted := redactOrchestrationEvents([]orchestration.Event{{
		ID: "event", PlanID: "plan", WorkspaceID: "workspace", Sequence: 1,
		Type: "test", Payload: payload, PayloadHash: "payload-hash", EnvelopeHash: "envelope-hash",
	}})
	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	serialized := string(data)
	if strings.Contains(serialized, canary) {
		t.Fatalf("diagnostics leaked sensitive content: %s", serialized)
	}
	if !strings.Contains(serialized, "visible") || !strings.Contains(serialized, "secret:vault/orchestration") || !strings.Contains(serialized, "payload-hash") {
		t.Fatalf("diagnostics lost safe metadata or opaque refs: %s", serialized)
	}
}

func TestRedactOrchestrationEventsFailsClosedOnMalformedPayload(t *testing.T) {
	redacted := redactOrchestrationEvents([]orchestration.Event{{
		ID: "event", PlanID: "plan", WorkspaceID: "workspace", Sequence: 1,
		Type: "test", Payload: json.RawMessage(`{"secret":`), PayloadHash: "payload-hash", EnvelopeHash: "envelope-hash",
	}})
	payload, ok := redacted[0]["payload"].(map[string]any)
	if !ok || payload["redacted"] != true {
		t.Fatalf("malformed payload did not fail closed: %#v", redacted[0]["payload"])
	}
}
