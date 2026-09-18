// Package reducer defines pure command-to-event state transitions.
package reducer

import (
	"context"

	"github.com/adro-project/adro/core"
	"github.com/adro-project/adro/core/event"
)

type Reducer[S any, C any] interface {
	Decide(context.Context, S, C, core.Dependencies) ([]event.Uncommitted, error)
	Apply(S, event.Envelope) (S, error)
}

func Replay[S any, C any](reducer Reducer[S, C], initial S, events []event.Envelope) (S, error) {
	state := initial
	for _, envelope := range events {
		next, err := reducer.Apply(state, envelope)
		if err != nil {
			return state, err
		}
		state = next
	}
	return state, nil
}
