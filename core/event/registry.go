package event

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	coreencoding "github.com/adro-project/adro/core/encoding"
)

var (
	// ErrFutureSchema means a reader was asked to consume an event newer than
	// the registry target. Silently ignoring newer fields would make replay
	// non-deterministic, so callers must upgrade the reader or quarantine the
	// stream.
	ErrFutureSchema = errors.New("event schema is newer than the reader")
	// ErrUpcasterNotFound means a historical event has no explicit migration
	// path to the reader's target schema.
	ErrUpcasterNotFound = errors.New("event schema upcaster is not registered")
	// ErrSchemaDowngrade is returned when a caller attempts to write a
	// migration in the reverse direction. Stored events are immutable and are
	// never rewritten to an older schema.
	ErrSchemaDowngrade = errors.New("event schema downgrade is not supported")
	ErrUpcasterInvalid = errors.New("event schema upcaster is invalid")
)

// Upcaster transforms one immutable payload version into the immediately next
// version. The stored envelope and digest are never changed; the transformed
// envelope returned for replay is an in-memory projection only.
type Upcaster func(json.RawMessage) (json.RawMessage, error)

type upcasterKey struct {
	eventType string
	from      int
}

// ReplayEnvelope retains the original event identity while exposing the
// upcasted payload to a reducer. OriginalDigest is the digest authenticated in
// the event store; Envelope is a derived, in-memory view and must never be
// persisted back to the store.
type ReplayEnvelope struct {
	Envelope       Envelope
	OriginalDigest string
	UpcastPath     []int
}

// Registry is an explicit, deterministic schema migration registry. Every
// migration is adjacent (v -> v+1), which prevents ambiguous paths and makes
// the exact upcaster path part of replay evidence.
type Registry struct {
	targetVersion int
	upcasters     map[upcasterKey]Upcaster
}

func NewRegistry(targetVersion int) (*Registry, error) {
	if targetVersion < 1 {
		return nil, fmt.Errorf("%w: target version must be positive", ErrUpcasterInvalid)
	}
	return &Registry{targetVersion: targetVersion, upcasters: map[upcasterKey]Upcaster{}}, nil
}

func (r *Registry) TargetVersion() int {
	if r == nil {
		return 0
	}
	return r.targetVersion
}

// Register adds one adjacent migration. Event types are exact strings; a
// wildcard migration is deliberately not supported because it can hide an
// accidental schema collision between aggregates.
func (r *Registry) Register(eventType string, fromVersion, toVersion int, upcaster Upcaster) error {
	if r == nil {
		return fmt.Errorf("%w: nil registry", ErrUpcasterInvalid)
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "" || fromVersion < 1 || toVersion != fromVersion+1 || upcaster == nil {
		return fmt.Errorf("%w: event type, adjacent versions and callback are required", ErrUpcasterInvalid)
	}
	key := upcasterKey{eventType: eventType, from: fromVersion}
	if _, exists := r.upcasters[key]; exists {
		return fmt.Errorf("%w: duplicate %s v%d migration", ErrUpcasterInvalid, eventType, fromVersion)
	}
	r.upcasters[key] = upcaster
	return nil
}

// Upcast verifies the stored bytes, then applies the unique adjacent path to
// the registry target. The input envelope is copied and remains untouched.
func (r *Registry) Upcast(envelope Envelope) (ReplayEnvelope, error) {
	if r == nil || r.targetVersion < 1 {
		return ReplayEnvelope{}, fmt.Errorf("%w: registry is required", ErrUpcasterInvalid)
	}
	if err := envelope.VerifyStored(); err != nil {
		return ReplayEnvelope{}, err
	}
	if envelope.SchemaVersion > r.targetVersion {
		return ReplayEnvelope{}, fmt.Errorf("%w: event=%s version=%d target=%d", ErrFutureSchema, envelope.EventType, envelope.SchemaVersion, r.targetVersion)
	}
	result := ReplayEnvelope{Envelope: cloneEnvelope(envelope), OriginalDigest: envelope.EnvelopeDigest}
	if envelope.SchemaVersion == r.targetVersion {
		return result, nil
	}

	payload := append(json.RawMessage(nil), envelope.Payload...)
	version := envelope.SchemaVersion
	for version < r.targetVersion {
		upcaster, ok := r.upcasters[upcasterKey{eventType: envelope.EventType, from: version}]
		if !ok {
			return ReplayEnvelope{}, fmt.Errorf("%w: event=%s from=%d to=%d", ErrUpcasterNotFound, envelope.EventType, version, r.targetVersion)
		}
		migrated, err := upcaster(append(json.RawMessage(nil), payload...))
		if err != nil {
			return ReplayEnvelope{}, fmt.Errorf("upcast %s v%d->v%d: %w", envelope.EventType, version, version+1, err)
		}
		canonical, err := coreencoding.Canonicalize(migrated)
		if err != nil {
			return ReplayEnvelope{}, fmt.Errorf("upcast %s v%d->v%d produced invalid JSON: %w", envelope.EventType, version, version+1, err)
		}
		payload = canonical
		version++
		result.UpcastPath = append(result.UpcastPath, version)
	}

	derived := result.Envelope
	derived.SchemaVersion = r.targetVersion
	derived.Payload = payload
	var err error
	derived.PayloadDigest, err = coreencoding.DigestRaw(payload)
	if err != nil {
		return ReplayEnvelope{}, err
	}
	derived.EnvelopeDigest, err = derived.digest()
	if err != nil {
		return ReplayEnvelope{}, err
	}
	result.Envelope = derived
	return result, nil
}

func cloneEnvelope(envelope Envelope) Envelope {
	envelope.Payload = append(json.RawMessage(nil), envelope.Payload...)
	return envelope
}

// UnknownFieldPolicy controls decoding of a versioned event payload into a
// typed projection. New readers should reject unknown fields by default;
// explicitly opting into preservation is safe only for forward-compatible
// metadata projections that do not make business decisions from the fields.
type UnknownFieldPolicy string

const (
	RejectUnknownFields   UnknownFieldPolicy = "reject"
	PreserveUnknownFields UnknownFieldPolicy = "preserve"
)

func DecodePayload[T any](raw json.RawMessage, policy UnknownFieldPolicy) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if policy == RejectUnknownFields {
		decoder.DisallowUnknownFields()
	} else if policy != PreserveUnknownFields {
		return value, fmt.Errorf("%w: unknown-field policy %q", ErrUpcasterInvalid, policy)
	}
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode event payload: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return value, errors.New("decode event payload: multiple JSON values")
		}
		return value, fmt.Errorf("decode event payload trailer: %w", err)
	}
	return value, nil
}
