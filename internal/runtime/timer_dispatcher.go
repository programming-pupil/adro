package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrTimerRetryable          = errors.New("timer command is retryable")
	ErrTimerHandlerUnavailable = errors.New("timer command handler is unavailable")
)

// TimerCommandHandler executes one claimed occurrence. Handlers must make the
// command's idempotency key/occurrence key part of their own durable command
// boundary before performing an external side effect.
type TimerCommandHandler func(context.Context, TimerClaim) error

type TimerDispatcherConfig struct {
	Store        *TimerStore
	Owner        string
	LeaseTTL     time.Duration
	PollInterval time.Duration
	BatchSize    int
	Handlers     map[string]TimerCommandHandler
}

type TimerDispatchReport struct {
	Claimed      int `json:"claimed"`
	Acknowledged int `json:"acknowledged"`
	Retried      int `json:"retried"`
	Failed       int `json:"failed"`
	Unavailable  int `json:"unavailable"`
}

// TimerDispatcher is a bounded, lease-fenced worker. It never invokes a
// handler without a durable claim and never acknowledges before the handler
// returns. A process restart therefore reclaims an expired occurrence.
type TimerDispatcher struct {
	store        *TimerStore
	owner        string
	leaseTTL     time.Duration
	pollInterval time.Duration
	batchSize    int
	handlers     map[string]TimerCommandHandler
}

func NewTimerDispatcher(config TimerDispatcherConfig) (*TimerDispatcher, error) {
	if config.Store == nil || strings.TrimSpace(config.Owner) == "" || config.LeaseTTL <= 0 || config.PollInterval <= 0 || config.BatchSize <= 0 {
		return nil, errors.New("timer dispatcher requires store, owner, positive lease/poll durations and batch size")
	}
	handlers := make(map[string]TimerCommandHandler, len(config.Handlers))
	for name, handler := range config.Handlers {
		name = strings.TrimSpace(name)
		if name == "" || handler == nil {
			return nil, errors.New("timer dispatcher handlers require non-empty names and callbacks")
		}
		handlers[name] = handler
	}
	return &TimerDispatcher{store: config.Store, owner: strings.TrimSpace(config.Owner), leaseTTL: config.LeaseTTL, pollInterval: config.PollInterval, batchSize: config.BatchSize, handlers: handlers}, nil
}

// DispatchDue claims and executes one bounded batch at the supplied instant.
// Callers may use a manual clock in tests or pass time.Now in a worker loop.
func (d *TimerDispatcher) DispatchDue(ctx context.Context, now time.Time) (TimerDispatchReport, error) {
	var report TimerDispatchReport
	if d == nil || d.store == nil {
		return report, errors.New("timer dispatcher is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	claims, err := d.store.ClaimDue(now, d.owner, d.leaseTTL, d.batchSize)
	if err != nil {
		return report, err
	}
	report.Claimed = len(claims)
	for _, claim := range claims {
		if err := ctx.Err(); err != nil {
			_ = d.store.ReleaseClaim(claim.Timer.ID, d.owner, claim.Timer.FencingToken, claim.OccurrenceKey, now)
			return report, err
		}
		handler, ok := d.handlers[claim.Timer.Command.Name]
		if !ok {
			if _, failErr := d.store.Fail(claim.Timer.ID, d.owner, claim.Timer.FencingToken, claim.OccurrenceKey, now, "handler_unavailable"); failErr != nil {
				return report, fmt.Errorf("fail unavailable timer %s: %w", claim.Timer.ID, failErr)
			}
			report.Unavailable++
			report.Failed++
			continue
		}
		handlerErr := handler(ctx, claim)
		if handlerErr == nil {
			if _, ackErr := d.store.Acknowledge(claim.Timer.ID, d.owner, claim.Timer.FencingToken, claim.OccurrenceKey, now); ackErr != nil {
				return report, fmt.Errorf("acknowledge timer %s: %w", claim.Timer.ID, ackErr)
			}
			report.Acknowledged++
			continue
		}
		if errors.Is(handlerErr, context.Canceled) || errors.Is(handlerErr, context.DeadlineExceeded) || errors.Is(handlerErr, ErrTimerRetryable) {
			if releaseErr := d.store.ReleaseClaim(claim.Timer.ID, d.owner, claim.Timer.FencingToken, claim.OccurrenceKey, now); releaseErr != nil {
				return report, fmt.Errorf("release retryable timer %s: %w", claim.Timer.ID, releaseErr)
			}
			report.Retried++
			continue
		}
		if _, failErr := d.store.Fail(claim.Timer.ID, d.owner, claim.Timer.FencingToken, claim.OccurrenceKey, now, "handler_failed"); failErr != nil {
			return report, fmt.Errorf("fail timer %s: %w", claim.Timer.ID, failErr)
		}
		report.Failed++
	}
	return report, nil
}

// Run owns exactly one ticker and exits when ctx is cancelled. It performs an
// immediate scan after startup so a restarted process does not wait for a
// full poll interval before reclaiming due work.
func (d *TimerDispatcher) Run(ctx context.Context) error {
	if d == nil {
		return errors.New("timer dispatcher is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := d.DispatchDue(ctx, time.Time{}); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			if _, err := d.DispatchDue(ctx, now); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return err
				}
				// A transient claim/store error should not kill the worker. The
				// next bounded scan retries it after the poll interval.
			}
		}
	}
}
