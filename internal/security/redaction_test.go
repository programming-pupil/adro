package security

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/adro-project/adro/ports/secretstore"
)

func TestSensitivityAndSurfaceVocabulary(t *testing.T) {
	for _, value := range []Sensitivity{SensitivityPublic, SensitivityInternal, SensitivityConfidential, SensitivityRestricted, SensitivitySecret} {
		if !value.Valid() {
			t.Fatalf("valid sensitivity %q rejected", value)
		}
	}
	if Sensitivity("unknown").Valid() || SensitivityInternal.RequiresRedaction() || !SensitivityConfidential.RequiresRedaction() {
		t.Fatal("sensitivity ordering is invalid")
	}
	for _, surface := range []Surface{SurfacePrompt, SurfaceToolInput, SurfaceToolOutput, SurfaceTraceAttribute, SurfaceEvent, SurfaceLog} {
		if !surface.Valid() {
			t.Fatalf("valid surface %q rejected", surface)
		}
	}
	if Surface("unknown").Valid() || Redact(Surface("unknown"), "value") != Redacted {
		t.Fatal("unknown surface did not fail closed")
	}
}

func TestPromptAndToolPayloadsRedactByDefault(t *testing.T) {
	for _, surface := range []Surface{SurfacePrompt, SurfaceToolInput, SurfaceToolOutput} {
		if got := Redact(surface, "canary-plaintext"); got != Redacted {
			t.Fatalf("%s scalar = %#v, want redacted", surface, got)
		}
		if got := Redact(surface, secretstore.SecretRef("secret:vault/item")); got != "secret:vault/item" {
			t.Fatalf("%s secret ref = %#v", surface, got)
		}
	}
}

func TestRecursiveKeyAndClassificationRedaction(t *testing.T) {
	const canary = "canary-plaintext-value"
	value := map[string]any{
		"safe":          "visible",
		"authorization": "Bearer " + canary,
		"nested": map[string]any{
			"apiKey":     canary,
			"secret_ref": "secret:vault/item",
		},
		"classified": map[string]any{
			"sensitivity": "restricted",
			"source":      "user",
			"value":       canary,
			"reference":   "secret:vault/classified",
		},
	}
	redacted := Redact(SurfaceEvent, value)
	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal redacted value: %v", err)
	}
	serialized := string(data)
	if strings.Contains(serialized, canary) {
		t.Fatalf("redacted event contains canary: %s", serialized)
	}
	if !strings.Contains(serialized, "visible") || !strings.Contains(serialized, "secret:vault/item") || !strings.Contains(serialized, "secret:vault/classified") {
		t.Fatalf("redaction removed safe metadata or opaque refs: %s", serialized)
	}
}

func TestExplicitClassifiedValueRedactsCanary(t *testing.T) {
	const canary = "explicit-canary"
	for _, sensitivity := range []Sensitivity{SensitivityConfidential, SensitivityRestricted, SensitivitySecret} {
		redacted := Redact(SurfaceLog, Classify(sensitivity, map[string]any{
			"payload": canary,
			"ref":     "secret:vault/item",
		}))
		data, err := json.Marshal(redacted)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(data), canary) || !strings.Contains(string(data), "secret:vault/item") {
			t.Fatalf("classification %q was not enforced: %s", sensitivity, data)
		}
	}
	if got := Redact(SurfaceEvent, Classify(Sensitivity("invalid"), "value")); got != Redacted {
		t.Fatalf("invalid classification returned %#v", got)
	}
}

func TestTraceAttributeRedaction(t *testing.T) {
	for name, input := range map[string]struct {
		key, value, want string
		ok               bool
	}{
		"safe":           {"component", "runtime", "runtime", true},
		"secret key":     {"client.secret", "canary", Redacted, true},
		"bearer value":   {"header", "Bearer canary", Redacted, true},
		"credential URL": {"endpoint", "https://user:pass@example.test", Redacted, true},
		"opaque ref":     {"secret_ref", "secret:vault/item", "secret:vault/item", true},
		"empty key":      {" ", "value", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := RedactAttribute(input.key, input.value)
			if got != input.want || ok != input.ok {
				t.Fatalf("RedactAttribute(%q, %q) = (%q, %v), want (%q, %v)", input.key, input.value, got, ok, input.want, input.ok)
			}
		})
	}
}

func TestRedactionHandlesRawJSONAndDepthBound(t *testing.T) {
	raw := json.RawMessage(`{"password":"canary","safe":"value"}`)
	data, err := json.Marshal(Redact(SurfaceEvent, raw))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "canary") || !strings.Contains(string(data), "value") {
		t.Fatalf("raw JSON redaction failed: %s", data)
	}

	var value any = "leaf"
	for index := 0; index < 70; index++ {
		value = []any{value}
	}
	data, err = json.Marshal(Redact(SurfaceEvent, value))
	if err != nil {
		t.Fatalf("marshal deep value: %v", err)
	}
	if !strings.Contains(string(data), Redacted) {
		t.Fatalf("depth bound did not fail closed: %s", data)
	}
}
