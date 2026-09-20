// Package security centralizes sensitivity classification and boundary
// redaction. It is deliberately deterministic and side-effect free so the same
// value is treated identically at API, adapter, event, log, and trace borders.
package security

import (
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"unicode"

	"github.com/adro-project/adro/ports/secretstore"
)

const Redacted = "[redacted]"

type Sensitivity string

const (
	SensitivityPublic       Sensitivity = "public"
	SensitivityInternal     Sensitivity = "internal"
	SensitivityConfidential Sensitivity = "confidential"
	SensitivityRestricted   Sensitivity = "restricted"
	SensitivitySecret       Sensitivity = "secret"
)

var sensitivityOrder = map[Sensitivity]int{
	SensitivityPublic:       0,
	SensitivityInternal:     1,
	SensitivityConfidential: 2,
	SensitivityRestricted:   3,
	SensitivitySecret:       4,
}

func (s Sensitivity) Valid() bool {
	_, ok := sensitivityOrder[s]
	return ok
}

func (s Sensitivity) RequiresRedaction() bool {
	level, ok := sensitivityOrder[s]
	return ok && level >= sensitivityOrder[SensitivityConfidential]
}

type Surface string

const (
	SurfacePrompt         Surface = "prompt"
	SurfaceToolInput      Surface = "tool_input"
	SurfaceToolOutput     Surface = "tool_output"
	SurfaceTraceAttribute Surface = "trace_attribute"
	SurfaceEvent          Surface = "event"
	SurfaceLog            Surface = "log"
)

func (s Surface) Valid() bool {
	switch s {
	case SurfacePrompt, SurfaceToolInput, SurfaceToolOutput, SurfaceTraceAttribute, SurfaceEvent, SurfaceLog:
		return true
	default:
		return false
	}
}

// ClassifiedValue carries an explicit sensitivity decision across an adapter
// boundary. Redaction preserves opaque SecretRef values but never its plaintext
// payload when the classification is confidential or stronger.
type ClassifiedValue struct {
	Sensitivity Sensitivity
	Value       any
}

func Classify(sensitivity Sensitivity, value any) ClassifiedValue {
	return ClassifiedValue{Sensitivity: sensitivity, Value: value}
}

// Redact returns a JSON-compatible copy suitable for the selected boundary.
// Prompt and tool payload surfaces redact unclassified scalar content by
// default. Event and log surfaces preserve ordinary metadata while applying
// recursive key and explicit-classification rules.
func Redact(surface Surface, value any) any {
	if !surface.Valid() {
		return Redacted
	}
	defaultSensitivity := SensitivityPublic
	if surface == SurfacePrompt || surface == SurfaceToolInput || surface == SurfaceToolOutput {
		defaultSensitivity = SensitivityConfidential
	}
	return redactValue(surface, value, "", defaultSensitivity, 0)
}

// RedactAttribute applies trace/log key and value guards. The bool reports
// whether the attribute key is valid for export. IDs and cardinality policy are
// intentionally left to the telemetry package.
func RedactAttribute(key, value string) (string, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false
	}
	value = strings.TrimSpace(value)
	if ref := secretstore.SecretRef(value); ref.Valid() {
		return ref.String(), true
	}
	if sensitiveKey(key) || credentialShaped(value) {
		return Redacted, true
	}
	return value, true
}

func redactValue(surface Surface, value any, key string, sensitivity Sensitivity, depth int) any {
	if depth >= 64 {
		return Redacted
	}
	if classified, ok := value.(ClassifiedValue); ok {
		if !classified.Sensitivity.Valid() {
			return Redacted
		}
		return redactValue(surface, classified.Value, key, maxSensitivity(sensitivity, classified.Sensitivity), depth+1)
	}
	if value == nil {
		return nil
	}
	if ref, ok := opaqueSecretRef(value); ok {
		return ref
	}
	if sensitiveKey(key) || sensitivity.RequiresRedaction() {
		return redactSensitive(surface, value, depth+1)
	}

	switch typed := value.(type) {
	case map[string]any:
		return redactMap(surface, typed, sensitivity, depth+1)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = redactValue(surface, item, key, sensitivity, depth+1)
		}
		return result
	case json.RawMessage:
		var decoded any
		if json.Unmarshal(typed, &decoded) != nil {
			return Redacted
		}
		return redactValue(surface, decoded, key, sensitivity, depth+1)
	default:
		return redactReflected(surface, typed, key, sensitivity, depth+1)
	}
}

func redactMap(surface Surface, value map[string]any, inherited Sensitivity, depth int) map[string]any {
	local := inherited
	if raw, ok := value["sensitivity"].(string); ok {
		candidate := Sensitivity(strings.ToLower(strings.TrimSpace(raw)))
		if candidate.Valid() {
			local = maxSensitivity(local, candidate)
		}
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		if classificationMetadataKey(key) {
			result[key] = item
			continue
		}
		result[key] = redactValue(surface, item, key, local, depth+1)
	}
	return result
}

func redactSensitive(surface Surface, value any, depth int) any {
	if ref, ok := opaqueSecretRef(value); ok {
		return ref
	}
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if classificationMetadataKey(key) {
				result[key] = item
			} else if ref, ok := opaqueSecretRef(item); ok {
				result[key] = ref
			} else {
				result[key] = Redacted
			}
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			if ref, ok := opaqueSecretRef(item); ok {
				result[index] = ref
			} else {
				result[index] = Redacted
			}
		}
		return result
	case json.RawMessage:
		return Redacted
	default:
		if reflected := reflect.ValueOf(value); reflected.IsValid() &&
			(reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array || reflected.Kind() == reflect.Map || reflected.Kind() == reflect.Struct) {
			return redactReflected(surface, value, "", SensitivityConfidential, depth+1)
		}
		return Redacted
	}
}

func redactReflected(surface Surface, value any, key string, sensitivity Sensitivity, depth int) any {
	data, err := json.Marshal(value)
	if err != nil {
		return Redacted
	}
	var decoded any
	if json.Unmarshal(data, &decoded) != nil {
		return Redacted
	}
	switch decoded.(type) {
	case map[string]any, []any:
		return redactValue(surface, decoded, key, sensitivity, depth+1)
	default:
		return decoded
	}
}

func opaqueSecretRef(value any) (string, bool) {
	switch typed := value.(type) {
	case secretstore.SecretRef:
		if typed.Valid() {
			return typed.String(), true
		}
	case string:
		candidate := secretstore.SecretRef(typed)
		if candidate.Valid() {
			return candidate.String(), true
		}
	}
	return "", false
}

func sensitiveKey(key string) bool {
	normalized := normalizeKey(key)
	if normalized == "" {
		return false
	}
	switch normalized {
	case "prompt", "input", "output", "content", "arguments", "body",
		"password", "passwd", "passphrase", "secret", "credential", "credentials",
		"authorization", "proxy_authorization", "cookie", "set_cookie", "private_key",
		"api_key", "apikey", "access_key", "client_secret", "token", "token_value",
		"access_token", "refresh_token", "id_token", "session_token":
		return true
	}
	return strings.Contains(normalized, "password") || strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "credential") || strings.HasSuffix(normalized, "_token") ||
		strings.HasSuffix(normalized, "_api_key") || strings.HasSuffix(normalized, "_private_key")
}

func normalizeKey(key string) string {
	var builder strings.Builder
	lastSeparator := false
	for _, ch := range strings.TrimSpace(key) {
		if unicode.IsLetter(ch) || unicode.IsDigit(ch) {
			builder.WriteRune(unicode.ToLower(ch))
			lastSeparator = false
			continue
		}
		if builder.Len() > 0 && !lastSeparator {
			builder.WriteByte('_')
			lastSeparator = true
		}
	}
	return strings.Trim(builder.String(), "_")
}

func classificationMetadataKey(key string) bool {
	switch normalizeKey(key) {
	case "sensitivity", "classification", "source", "trust", "trust_level", "type", "kind", "schema_version":
		return true
	default:
		return false
	}
}

func credentialShaped(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(lower, "bearer ") || strings.HasPrefix(lower, "basic ") || strings.HasPrefix(lower, "token ") {
		return true
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.User != nil
}

func maxSensitivity(left, right Sensitivity) Sensitivity {
	if sensitivityOrder[right] > sensitivityOrder[left] {
		return right
	}
	return left
}
