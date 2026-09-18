// Package ids defines stable identifiers used by the durable runtime core.
package ids

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

const MaxLength = 128

var ErrInvalid = errors.New("invalid runtime identifier")

type TenantID string
type WorkspaceID string
type AgentID string
type SessionID string
type TurnID string
type StepID string
type EffectID string
type ToolCallID string
type ApprovalID string
type CheckpointID string
type EventID string

// Validate rejects empty, ambiguous, and display-oriented identifiers. IDs are
// opaque and must not embed tenant secrets or mutable names.
func Validate(namespace, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed != value || len(value) > MaxLength {
		return fmt.Errorf("%w: %s must contain 1..%d bytes", ErrInvalid, namespace, MaxLength)
	}
	for i, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' || r == ':' {
			continue
		}
		return fmt.Errorf("%w: %s contains unsupported rune at byte %d", ErrInvalid, namespace, i)
	}
	return nil
}
