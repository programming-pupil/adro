// Package policy defines durable boundaries for policy decision evidence.
package policy

import (
	"context"
	"errors"

	corepolicy "github.com/adro-project/adro/core/policy"
)

var ErrDecisionNotFound = errors.New("policy decision was not found")

type DecisionRecorder interface {
	RecordDecision(context.Context, corepolicy.DecisionRecord) error
}

type DecisionStore interface {
	DecisionRecorder
	ReadDecision(context.Context, string) (corepolicy.DecisionRecord, error)
}

type RecordFunc func(context.Context, corepolicy.DecisionRecord) error

func (f RecordFunc) RecordDecision(ctx context.Context, record corepolicy.DecisionRecord) error {
	return f(ctx, record)
}
