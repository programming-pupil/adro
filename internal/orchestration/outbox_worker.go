package orchestration

import (
	"context"
	"errors"
	"strings"
	"time"
)

// OutboxStore is the minimum durable contract required by a publisher. Both
// the file repository and SQL adapters implement it; queue-backed deployments
// can provide the same lease/fencing semantics without importing the worker.
type OutboxStore interface {
	ListOutbox(planID, status string) []OutboxRecord
	ClaimOutbox(planID, owner string, ttl time.Duration, now time.Time) (OutboxRecord, error)
	AckOutbox(id, owner string, now time.Time, deliveryErr error) error
}

// OutboxHandler performs the external side effect represented by one intent.
// It must be idempotent on record.IdempotencyKey; the dispatcher never invokes
// a handler without first acquiring the durable delivery lease.
type OutboxHandler func(context.Context, OutboxRecord) error

type OutboxDispatcher struct {
	Store    OutboxStore
	Owner    string
	LeaseTTL time.Duration
	MaxBatch int
	Now      func() time.Time
}

type OutboxDispatchReport struct {
	Claimed       int            `json:"claimed"`
	Acked         int            `json:"acked"`
	Retried       int            `json:"retried"`
	Failed        int            `json:"failed"`
	LastError     string         `json:"last_error,omitempty"`
	FailedRecords []OutboxRecord `json:"failed_records,omitempty"`
}

func (d OutboxDispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

// Drain claims and delivers a bounded batch in the foreground. Expired leases
// are eligible for takeover through ClaimOutbox, while active leases remain
// invisible to competing workers. A delivery error is durably acknowledged as
// pending (or failed once MaxAttempts is reached), then the next intent is
// processed so one poison message cannot starve an entire plan.
func (d OutboxDispatcher) Drain(ctx context.Context, planID string, handler OutboxHandler) (OutboxDispatchReport, error) {
	if d.Store == nil {
		return OutboxDispatchReport{}, errors.New("outbox store is required")
	}
	if strings.TrimSpace(d.Owner) == "" {
		return OutboxDispatchReport{}, errors.New("outbox owner is required")
	}
	if d.LeaseTTL <= 0 {
		d.LeaseTTL = 15 * time.Minute
	}
	if handler == nil {
		return OutboxDispatchReport{}, errors.New("outbox handler is required")
	}
	limit := d.MaxBatch
	if limit <= 0 {
		limit = 100
	}
	var report OutboxDispatchReport
	for report.Claimed < limit {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		now := d.now()
		record, err := d.Store.ClaimOutbox(planID, d.Owner, d.LeaseTTL, now)
		if errors.Is(err, ErrNotFound) {
			return report, nil
		}
		if err != nil {
			return report, err
		}
		report.Claimed++
		deliveryErr := handler(ctx, record)
		if ackErr := d.Store.AckOutbox(record.ID, d.Owner, d.now(), deliveryErr); ackErr != nil {
			return report, ackErr
		}
		if deliveryErr == nil {
			report.Acked++
			continue
		}
		report.LastError = deliveryErr.Error()
		if record.MaxAttempts > 0 && record.Attempts >= record.MaxAttempts {
			report.Failed++
			record.Status = "failed"
			record.LastError = deliveryErr.Error()
			report.FailedRecords = append(report.FailedRecords, record)
		} else {
			report.Retried++
		}
	}
	return report, nil
}
