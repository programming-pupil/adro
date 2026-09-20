package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// TimerProjectionReport records how many authoritative schedule requests were
// materialized. Re-running the projector is safe because TimerStore.Schedule
// uses the persisted schedule key and command digest as its idempotency fence.
type TimerProjectionReport struct {
	Scanned  int `json:"scanned"`
	Created  int `json:"created"`
	Replayed int `json:"replayed"`
}

// ProjectTimerSchedules materializes committed timer.schedule_requested events
// into a TimerStore. The event stream remains authoritative: a backend outage
// leaves the request in the stream and a later projector run can retry it.
func (j *Journal) ProjectTimerSchedules(ctx context.Context, store *TimerStore) (TimerProjectionReport, error) {
	if j == nil || store == nil {
		return TimerProjectionReport{}, errors.New("timer projector requires journal and timer store")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	events := j.List(Scope{})
	report := TimerProjectionReport{}
	for _, event := range events {
		if event.EventType != EventTimerScheduleRequested || event.AggregateType != "timer" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.Scanned++
		var envelope struct {
			Request TimerScheduleRequest `json:"request"`
		}
		if err := json.Unmarshal(event.Payload, &envelope); err != nil {
			return report, fmt.Errorf("decode timer schedule event %s: %w", event.EventID, err)
		}
		spec, err := envelope.Request.Spec()
		if err != nil || spec.Scope != event.Scope {
			if err == nil {
				err = errors.New("timer schedule scope does not match event scope")
			}
			return report, fmt.Errorf("invalid timer schedule event %s: %w", event.EventID, err)
		}
		_, created, err := store.Schedule(spec)
		if err != nil {
			return report, fmt.Errorf("materialize timer schedule %s: %w", spec.ScheduleKey, err)
		}
		if created {
			report.Created++
		} else {
			report.Replayed++
		}
	}
	return report, nil
}
