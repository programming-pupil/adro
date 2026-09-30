// Package errs classifies errors at package boundaries. Callers make retry and
// protocol decisions using KindOf, never by matching human-readable messages.
package errs

import "errors"

type Kind uint8

const (
	KindInternal Kind = iota
	KindInvalid
	KindNotFound
	KindConflict
	KindFenced
	KindUnavailable
	// KindAmbiguous means a commit may have succeeded. A retry must reuse the
	// original idempotency key; it must not repeat the operation with a new key.
	KindAmbiguous
	KindDenied
	KindBudget
	KindUnsupported
)

// Error carries a stable operation and machine-readable code. Detail must not
// contain secrets. Neither Detail nor the wrapped error is included in Error's
// diagnostic string: underlying errors can contain credentials or payloads.
type Error struct {
	Kind   Kind
	Op     string
	Code   string
	Detail map[string]string
	Err    error
}

func (e *Error) Error() string {
	if e == nil {
		return "internal: nil_error"
	}
	return e.Op + ": " + e.Code
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// KindOf returns the outermost classified error's kind. An absent, unclassified,
// or invalid classification fails closed as KindInternal.
func KindOf(err error) Kind {
	var classified *Error
	if errors.As(err, &classified) && classified != nil && classified.Kind <= KindUnsupported {
		return classified.Kind
	}
	return KindInternal
}

// Retryable reports whether recovery permits a retry. For KindAmbiguous the
// original idempotency key is mandatory; this function does not perform retries.
func Retryable(err error) bool {
	k := KindOf(err)
	return k == KindUnavailable || k == KindAmbiguous
}
