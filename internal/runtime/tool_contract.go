package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	coreencoding "github.com/adro-project/adro/core/encoding"
)

const (
	ToolSchemaVersion = 1
	ToolParallelSafe  = "parallel_safe"
	ToolExclusive     = "exclusive"

	ToolFieldPublic    = "public"
	ToolFieldInternal  = "internal"
	ToolFieldSensitive = "sensitive"
	ToolFieldSecret    = "secret"

	defaultToolInputLimit  = 1 << 20
	defaultToolOutputLimit = 4 << 20
)

var (
	ErrToolContractInvalid = errors.New("runtime tool contract is invalid")
	ErrToolInputInvalid    = errors.New("runtime tool input is invalid")
	ErrToolOutputInvalid   = errors.New("runtime tool output is invalid")
	ErrToolPayloadTooLarge = errors.New("runtime tool payload exceeds contract limit")
)

type frozenToolContract struct {
	contract ToolContract
	input    *toolSchema
	output   *toolSchema
}

// FreezeToolContract validates and canonicalizes a tool contract before any
// authorization or dispatch. The digest prevents plugin hot reload from
// changing schema or capability semantics during an active call.
func FreezeToolContract(contract ToolContract) (ToolContract, error) {
	contract.Name = strings.TrimSpace(contract.Name)
	contract.ConcurrencyMode = strings.TrimSpace(contract.ConcurrencyMode)
	capabilitySet := make(map[string]struct{}, len(contract.Capabilities))
	capabilities := make([]string, 0, len(contract.Capabilities))
	for _, capability := range contract.Capabilities {
		capability = strings.TrimSpace(capability)
		if capability == "" {
			return ToolContract{}, fmt.Errorf("%w: empty capability", ErrToolContractInvalid)
		}
		if _, exists := capabilitySet[capability]; exists {
			continue
		}
		capabilitySet[capability] = struct{}{}
		capabilities = append(capabilities, capability)
	}
	sort.Strings(capabilities)
	contract.Capabilities = capabilities
	secretScopeSet := make(map[string]struct{}, len(contract.SecretScopes))
	secretScopes := make([]string, 0, len(contract.SecretScopes))
	for _, scope := range contract.SecretScopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			return ToolContract{}, fmt.Errorf("%w: empty secret scope", ErrToolContractInvalid)
		}
		if _, exists := secretScopeSet[scope]; exists {
			continue
		}
		secretScopeSet[scope] = struct{}{}
		secretScopes = append(secretScopes, scope)
	}
	sort.Strings(secretScopes)
	contract.SecretScopes = secretScopes
	if contract.SchemaVersion == 0 {
		contract.SchemaVersion = ToolSchemaVersion
	}
	if contract.ConcurrencyMode == "" {
		contract.ConcurrencyMode = ToolExclusive
	}
	if contract.MaxInputBytes == 0 {
		contract.MaxInputBytes = defaultToolInputLimit
	}
	if contract.MaxOutputBytes == 0 {
		contract.MaxOutputBytes = defaultToolOutputLimit
	}
	if contract.SchemaVersion != ToolSchemaVersion || contract.Name == "" || len(contract.Capabilities) == 0 || !contract.SideEffectClass.valid() {
		return ToolContract{}, ErrToolContractInvalid
	}
	if contract.ConcurrencyMode != ToolParallelSafe && contract.ConcurrencyMode != ToolExclusive {
		return ToolContract{}, fmt.Errorf("%w: unsupported concurrency_mode", ErrToolContractInvalid)
	}
	if contract.Timeout < 0 || contract.MaxRetries < 0 || contract.MaxInputBytes < 1 || contract.MaxOutputBytes < 1 {
		return ToolContract{}, fmt.Errorf("%w: invalid limits", ErrToolContractInvalid)
	}
	if contract.SideEffectClass != EffectReadOnly && contract.MaxRetries > 0 {
		return ToolContract{}, fmt.Errorf("%w: automatic write retries are prohibited", ErrToolContractInvalid)
	}
	if err := validateReconcilePolicy(contract.SideEffectClass, contract.ReconcilePolicy); err != nil {
		return ToolContract{}, err
	}
	if contract.ReconcilePolicy == "" && contract.SideEffectClass == EffectReadOnly {
		contract.ReconcilePolicy = ReconcileNone
	}
	canonicalInput, err := canonicalToolSchema(contract.InputSchema)
	if err != nil {
		return ToolContract{}, fmt.Errorf("%w: input_schema: %v", ErrToolContractInvalid, err)
	}
	canonicalOutput, err := canonicalToolSchema(contract.OutputSchema)
	if err != nil {
		return ToolContract{}, fmt.Errorf("%w: output_schema: %v", ErrToolContractInvalid, err)
	}
	contract.InputSchema, contract.OutputSchema = canonicalInput, canonicalOutput
	classes := make(map[string]string, len(contract.FieldClasses))
	for path, classification := range contract.FieldClasses {
		path, classification = strings.TrimSpace(path), strings.TrimSpace(classification)
		if path == "" || !validToolClassification(classification) {
			return ToolContract{}, fmt.Errorf("%w: invalid field classification", ErrToolContractInvalid)
		}
		if _, exists := classes[path]; exists {
			return ToolContract{}, fmt.Errorf("%w: duplicate field classification", ErrToolContractInvalid)
		}
		classes[path] = classification
	}
	contract.FieldClasses = classes
	contract.ContractDigest = ""
	digest, err := coreencoding.Digest(contract)
	if err != nil {
		return ToolContract{}, fmt.Errorf("%w: digest: %v", ErrToolContractInvalid, err)
	}
	contract.ContractDigest = digest
	return contract, nil
}

func canonicalToolSchema(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	canonical, err := coreencoding.Canonicalize([]byte(raw))
	if err != nil {
		return "", err
	}
	if _, err := parseToolSchema(string(canonical)); err != nil {
		return "", err
	}
	return string(canonical), nil
}

func validateToolInput(contract ToolContract, input any) ([]byte, error) {
	return validateToolPayload(contract, input, true)
}

func validateToolOutput(contract ToolContract, output any) ([]byte, error) {
	return validateToolPayload(contract, output, false)
}

func validateToolPayload(contract ToolContract, value any, input bool) ([]byte, error) {
	frozen, err := FreezeToolContract(contract)
	if err != nil {
		return nil, err
	}
	data, err := coreencoding.Marshal(value)
	if err != nil {
		if input {
			return nil, fmt.Errorf("%w: %v", ErrToolInputInvalid, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrToolOutputInvalid, err)
	}
	limit, schemaRaw, invalid := frozen.MaxOutputBytes, frozen.OutputSchema, ErrToolOutputInvalid
	if input {
		limit, schemaRaw, invalid = frozen.MaxInputBytes, frozen.InputSchema, ErrToolInputInvalid
	}
	if len(data) > limit {
		return data, fmt.Errorf("%w: got %d bytes, limit %d", ErrToolPayloadTooLarge, len(data), limit)
	}
	schema, err := parseToolSchema(schemaRaw)
	if err != nil {
		return data, fmt.Errorf("%w: schema: %v", invalid, err)
	}
	if schema != nil {
		var decoded any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			return data, fmt.Errorf("%w: %v", invalid, err)
		}
		if err := schema.validate("$", decoded); err != nil {
			return data, fmt.Errorf("%w: %v", invalid, err)
		}
	}
	return data, nil
}

func validToolClassification(value string) bool {
	switch value {
	case ToolFieldPublic, ToolFieldInternal, ToolFieldSensitive, ToolFieldSecret:
		return true
	default:
		return false
	}
}

// toolSchema is the intentionally bounded JSON Schema subset supported by the
// runtime core. Unsupported keywords fail closed instead of being ignored.
type toolSchema struct {
	Type                 string
	Required             map[string]bool
	Properties           map[string]*toolSchema
	Items                *toolSchema
	AdditionalProperties bool
	MaxLength            *int
	MaxItems             *int
	Minimum              *float64
	Maximum              *float64
	Enum                 []any
}

func parseToolSchema(raw string) (*toolSchema, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	canonical, err := coreencoding.Canonicalize([]byte(raw))
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return decodeToolSchema(value)
}

func decodeToolSchema(value any) (*toolSchema, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("schema must be an object")
	}
	allowed := map[string]bool{"type": true, "required": true, "properties": true, "items": true, "additionalProperties": true, "maxLength": true, "maxItems": true, "minimum": true, "maximum": true, "enum": true}
	for keyword := range object {
		if !allowed[keyword] {
			return nil, fmt.Errorf("unsupported keyword %q", keyword)
		}
	}
	typeName, _ := object["type"].(string)
	if !validToolSchemaType(typeName) {
		return nil, errors.New("schema type is required")
	}
	schema := &toolSchema{Type: typeName, Required: map[string]bool{}, Properties: map[string]*toolSchema{}, AdditionalProperties: true}
	if raw, exists := object["additionalProperties"]; exists {
		value, ok := raw.(bool)
		if !ok {
			return nil, errors.New("additionalProperties must be boolean")
		}
		schema.AdditionalProperties = value
	}
	if raw, exists := object["required"]; exists {
		items, ok := raw.([]any)
		if !ok {
			return nil, errors.New("required must be an array")
		}
		for _, item := range items {
			name, ok := item.(string)
			if !ok || name == "" || schema.Required[name] {
				return nil, errors.New("required entries must be unique non-empty strings")
			}
			schema.Required[name] = true
		}
	}
	if raw, exists := object["properties"]; exists {
		properties, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("properties must be an object")
		}
		for name, child := range properties {
			parsed, err := decodeToolSchema(child)
			if err != nil {
				return nil, fmt.Errorf("property %s: %w", name, err)
			}
			schema.Properties[name] = parsed
		}
	}
	if raw, exists := object["items"]; exists {
		items, err := decodeToolSchema(raw)
		if err != nil {
			return nil, fmt.Errorf("items: %w", err)
		}
		schema.Items = items
	}
	if value, ok := schemaInteger(object["maxLength"]); ok {
		schema.MaxLength = &value
	} else if _, exists := object["maxLength"]; exists {
		return nil, errors.New("maxLength must be a non-negative integer")
	}
	if value, ok := schemaInteger(object["maxItems"]); ok {
		schema.MaxItems = &value
	} else if _, exists := object["maxItems"]; exists {
		return nil, errors.New("maxItems must be a non-negative integer")
	}
	if value, ok := schemaNumber(object["minimum"]); ok {
		schema.Minimum = &value
	} else if _, exists := object["minimum"]; exists {
		return nil, errors.New("minimum must be numeric")
	}
	if value, ok := schemaNumber(object["maximum"]); ok {
		schema.Maximum = &value
	} else if _, exists := object["maximum"]; exists {
		return nil, errors.New("maximum must be numeric")
	}
	if raw, exists := object["enum"]; exists {
		values, ok := raw.([]any)
		if !ok || len(values) == 0 {
			return nil, errors.New("enum must be a non-empty array")
		}
		schema.Enum = values
	}
	if err := schema.validateKeywords(object); err != nil {
		return nil, err
	}
	if schema.Minimum != nil && schema.Maximum != nil && *schema.Minimum > *schema.Maximum {
		return nil, errors.New("minimum cannot exceed maximum")
	}
	return schema, nil
}

func validToolSchemaType(value string) bool {
	switch value {
	case "object", "array", "string", "integer", "number", "boolean", "null":
		return true
	default:
		return false
	}
}

func (s *toolSchema) validateKeywords(object map[string]any) error {
	allowedByType := map[string]map[string]bool{
		"object":  {"required": true, "properties": true, "additionalProperties": true},
		"array":   {"items": true, "maxItems": true},
		"string":  {"maxLength": true},
		"integer": {"minimum": true, "maximum": true},
		"number":  {"minimum": true, "maximum": true},
		"boolean": {},
		"null":    {},
	}
	for keyword := range object {
		if keyword == "type" || keyword == "enum" || allowedByType[s.Type][keyword] {
			continue
		}
		return fmt.Errorf("keyword %q is not valid for type %q", keyword, s.Type)
	}
	return nil
}

func schemaInteger(value any) (int, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := number.Int64()
	return int(parsed), err == nil && parsed >= 0 && int64(int(parsed)) == parsed
}

func schemaNumber(value any) (float64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := number.Float64()
	return parsed, err == nil
}

func (s *toolSchema) validate(path string, value any) error {
	switch s.Type {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be object", path)
		}
		for required := range s.Required {
			if _, exists := object[required]; !exists {
				return fmt.Errorf("%s.%s is required", path, required)
			}
		}
		for name, child := range object {
			property, exists := s.Properties[name]
			if !exists {
				if !s.AdditionalProperties {
					return fmt.Errorf("%s.%s is not allowed", path, name)
				}
				continue
			}
			if err := property.validate(path+"."+name, child); err != nil {
				return err
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be array", path)
		}
		if s.MaxItems != nil && len(items) > *s.MaxItems {
			return fmt.Errorf("%s exceeds maxItems", path)
		}
		if s.Items != nil {
			for index, item := range items {
				if err := s.Items.validate(fmt.Sprintf("%s[%d]", path, index), item); err != nil {
					return err
				}
			}
		}
	case "string":
		item, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s must be string", path)
		}
		if s.MaxLength != nil && len([]rune(item)) > *s.MaxLength {
			return fmt.Errorf("%s exceeds maxLength", path)
		}
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("%s must be integer", path)
		}
		integer, err := number.Int64()
		if err != nil {
			return fmt.Errorf("%s must be integer", path)
		}
		if s.Minimum != nil && float64(integer) < *s.Minimum || s.Maximum != nil && float64(integer) > *s.Maximum {
			return fmt.Errorf("%s is outside numeric bounds", path)
		}
	case "number":
		number, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("%s must be number", path)
		}
		parsed, err := number.Float64()
		if err != nil || s.Minimum != nil && parsed < *s.Minimum || s.Maximum != nil && parsed > *s.Maximum {
			return fmt.Errorf("%s is outside numeric bounds", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be boolean", path)
		}
	case "null":
		if value != nil {
			return fmt.Errorf("%s must be null", path)
		}
	default:
		return fmt.Errorf("%s has unsupported type %q", path, s.Type)
	}
	if len(s.Enum) > 0 {
		candidate, _ := coreencoding.Marshal(value)
		matched := false
		for _, allowed := range s.Enum {
			encoded, _ := coreencoding.Marshal(allowed)
			if bytes.Equal(candidate, encoded) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s is outside enum", path)
		}
	}
	return nil
}
