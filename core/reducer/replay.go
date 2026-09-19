package reducer

import (
	"fmt"

	coreencoding "github.com/adro-project/adro/core/encoding"
	"github.com/adro-project/adro/core/event"
)

// ReplayEvidence describes the exact immutable history used to derive a
// projection. UpcastPath stores the versions applied to each event; the
// original event digest remains the identity used for audit and divergence
// reports.
type ReplayEvidence struct {
	LastSequence         int64            `json:"last_sequence"`
	StateDigest          string           `json:"state_digest"`
	OriginalEventDigests []string         `json:"original_event_digests,omitempty"`
	UpcastPath           map[string][]int `json:"upcast_path,omitempty"`
}

// ReplayVerified validates the event chain, applies registered schema
// upcasters in memory, and then runs the pure reducer. It never writes a
// snapshot or mutates an envelope from the event store.
func ReplayVerified[S any, C any](reducer Reducer[S, C], initial S, events []event.Envelope, registry *event.Registry) (S, ReplayEvidence, error) {
	var evidence ReplayEvidence
	if reducer == nil {
		return initial, evidence, fmt.Errorf("reducer is required")
	}
	if err := event.ValidateChain(events); err != nil {
		return initial, evidence, err
	}
	state := initial
	evidence.UpcastPath = map[string][]int{}
	evidence.OriginalEventDigests = make([]string, 0, len(events))
	for _, stored := range events {
		decoded := event.ReplayEnvelope{Envelope: stored, OriginalDigest: stored.EnvelopeDigest}
		var err error
		if registry != nil {
			decoded, err = registry.Upcast(stored)
			if err != nil {
				return state, evidence, err
			}
		}
		next, err := reducer.Apply(state, decoded.Envelope)
		if err != nil {
			return state, evidence, fmt.Errorf("apply event %s at sequence %d: %w", stored.EventType, stored.Sequence, err)
		}
		state = next
		evidence.LastSequence = stored.Sequence
		evidence.OriginalEventDigests = append(evidence.OriginalEventDigests, decoded.OriginalDigest)
		if len(decoded.UpcastPath) > 0 {
			evidence.UpcastPath[stored.EventID] = append([]int(nil), decoded.UpcastPath...)
		}
	}
	var err error
	evidence.StateDigest, err = coreencoding.Digest(state)
	if err != nil {
		return state, evidence, fmt.Errorf("digest replayed state: %w", err)
	}
	return state, evidence, nil
}
