package harness

import (
	"context"
	"fmt"
	"time"
)

// OutboxPublisher is implemented by an event transport (the local events.Bus
// or a NATS JetStream adapter). The dispatcher claims durable records before
// publishing, so a crash results in an expiry/retry instead of a lost effect.
type OutboxPublisher interface {
	Publish(context.Context, OutboxEvent) error
}

type Dispatcher struct {
	Store     *Store
	Publisher OutboxPublisher
	Owner     string
	LeaseTTL  time.Duration
}

// DispatchOnce publishes up to limit pending records for one session. A
// successful publication is acknowledged only after the transport returns;
// failures are made visible and eligible for a later retry.
func (d Dispatcher) DispatchOnce(ctx context.Context, sessionID string, limit int) (int, error) {
	if d.Store == nil || d.Publisher == nil {
		return 0, fmt.Errorf("outbox dispatcher requires store and publisher")
	}
	if d.Owner == "" {
		return 0, fmt.Errorf("outbox dispatcher owner is required")
	}
	if d.LeaseTTL <= 0 {
		d.LeaseTTL = 30 * time.Second
	}
	claimed, err := d.Store.ClaimOutbox(sessionID, d.Owner, limit, d.LeaseTTL, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	count := 0
	for _, event := range claimed {
		if err := d.Publisher.Publish(ctx, event); err != nil {
			_ = d.Store.NackOutbox(sessionID, event.ID, d.Owner, time.Now().UTC().Add(time.Second))
			continue
		}
		if err := d.Store.AckOutbox(sessionID, event.ID, d.Owner, time.Now().UTC()); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
