// Package projection contains the event-driven read-model worker. It treats
// EventStore as authoritative, applies deterministic mutations, and advances a
// tenant-scoped offset only after the corresponding records are durable.
package projection

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/adro-project/adro/core"
	coreencoding "github.com/adro-project/adro/core/encoding"
	coreevent "github.com/adro-project/adro/core/event"
	"github.com/adro-project/adro/core/ids"
	eventstoreport "github.com/adro-project/adro/ports/eventstore"
	projectionport "github.com/adro-project/adro/ports/projection"
	"github.com/adro-project/adro/ports/scope"
)

var (
	ErrInvalidConfig      = errors.New("projection worker configuration is invalid")
	ErrSequenceGap        = errors.New("projection worker event sequence gap")
	ErrScopeMismatch      = errors.New("projection worker event scope mismatch")
	ErrDiverged           = errors.New("projection worker projection diverged")
	ErrDuplicateMutation  = errors.New("projection worker emitted duplicate mutation")
	ErrSubscriptionClosed = errors.New("projection worker subscription closed")
)

// Mutation is one deterministic change derived from an authoritative event.
// A worker owns one projection name, so a mutation only needs a record key.
type Mutation struct {
	Key     string
	Payload []byte
	Delete  bool
}

// Projector must be deterministic for a given event. It should perform no
// writes; the worker applies the returned mutations with CAS and tenant scope.
type Projector func(context.Context, coreevent.Envelope) ([]Mutation, error)

type Config struct {
	Events         eventstoreport.Store
	Store          projectionport.Store
	Offsets        projectionport.OffsetStore
	Clock          core.Clock
	TenantID       string
	StreamID       string
	ProjectionName string
	PageSize       int
	BufferSize     int
	CASRetries     int
}

type Worker struct {
	events         eventstoreport.Store
	store          projectionport.Store
	offsets        projectionport.OffsetStore
	clock          core.Clock
	tenantID       string
	streamID       string
	projectionName string
	pageSize       int
	bufferSize     int
	casRetries     int
}

type Report struct {
	Events           int   `json:"events"`
	Mutations        int   `json:"mutations"`
	SkippedMutations int   `json:"skipped_mutations"`
	UpdatedRecords   int   `json:"updated_records"`
	DeletedRecords   int   `json:"deleted_records"`
	LastSequence     int64 `json:"last_sequence"`
}

func NewWorker(config Config) (*Worker, error) {
	if config.Events == nil || config.Store == nil || config.Offsets == nil {
		return nil, fmt.Errorf("%w: events, store and offsets are required", ErrInvalidConfig)
	}
	if err := ids.Validate("tenant", strings.TrimSpace(config.TenantID)); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := ids.Validate("stream", strings.TrimSpace(config.StreamID)); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if strings.TrimSpace(config.ProjectionName) == "" || strings.ContainsAny(config.ProjectionName, "\x00\r\n") {
		return nil, fmt.Errorf("%w: projection name is required and cannot contain control characters", ErrInvalidConfig)
	}
	if config.PageSize == 0 {
		config.PageSize = 128
	}
	if config.BufferSize == 0 {
		config.BufferSize = 64
	}
	if config.CASRetries == 0 {
		config.CASRetries = 3
	}
	if config.PageSize < 1 || config.PageSize > 1000 || config.BufferSize < 1 || config.BufferSize > 1000 || config.CASRetries < 0 {
		return nil, fmt.Errorf("%w: page/buffer must be 1..1000 and retries cannot be negative", ErrInvalidConfig)
	}
	if config.Clock == nil {
		config.Clock = core.SystemClock{}
	}
	return &Worker{
		events: config.Events, store: config.Store, offsets: config.Offsets, clock: config.Clock,
		tenantID: strings.TrimSpace(config.TenantID), streamID: strings.TrimSpace(config.StreamID),
		projectionName: strings.TrimSpace(config.ProjectionName), pageSize: config.PageSize,
		bufferSize: config.BufferSize, casRetries: config.CASRetries,
	}, nil
}

// Rebuild replays the authoritative stream from sequence zero. It updates the
// durable offset only after the complete replay succeeds; a crash before that
// point is safe because record writes are idempotent by source sequence.
func (w *Worker) Rebuild(ctx context.Context, projector Projector) (Report, error) {
	if err := w.validateCall(ctx, projector); err != nil {
		return Report{}, err
	}
	base, err := w.offsets.GetOffset(ctx, w.tenantID, w.projectionName, w.streamID)
	hasBase := true
	if errors.Is(err, projectionport.ErrOffsetNotFound) {
		hasBase = false
	} else if err != nil {
		return Report{}, err
	} else {
		head, _, headErr := w.events.Head(ctx, w.streamID)
		if headErr != nil {
			return Report{}, fmt.Errorf("read projection stream head: %w", headErr)
		}
		if base.LastSequence > head {
			return Report{}, fmt.Errorf("%w: offset sequence %d is ahead of stream sequence %d", ErrDiverged, base.LastSequence, head)
		}
	}
	report, cursor, err := w.replay(ctx, 0, projector, false, false, Report{})
	if err != nil {
		return report, err
	}
	digest, err := w.projectionDigest(ctx)
	if err != nil {
		return report, err
	}
	if hasBase && base.LastSequence > cursor {
		return report, fmt.Errorf("%w: offset sequence %d is ahead of stream sequence %d", ErrDiverged, base.LastSequence, cursor)
	}
	expected := int64(0)
	if hasBase {
		expected = base.LastSequence
	}
	if err := w.putOffset(ctx, cursor, digest, expected); err != nil {
		return report, err
	}
	report.LastSequence = cursor
	return report, nil
}

// Run replays from the durable offset, then consumes the EventStore
// subscription. The subscription is opened before replay so events committed
// during recovery remain buffered and cannot be lost at the hand-off.
func (w *Worker) Run(ctx context.Context, projector Projector) error {
	if err := w.validateCall(ctx, projector); err != nil {
		return err
	}
	offset, hasOffset, err := w.readOffset(ctx)
	if err != nil {
		return err
	}
	start := int64(0)
	if hasOffset {
		start = offset.LastSequence
	}
	subscription, err := w.events.Subscribe(ctx, eventstoreport.Subscription{
		TenantID: w.tenantID, StreamID: w.streamID, After: start, BufferSize: w.bufferSize,
	})
	if err != nil {
		return fmt.Errorf("subscribe projection stream: %w", err)
	}
	defer subscription.Close()

	_, cursor, err := w.replay(ctx, start, projector, true, hasOffset, Report{LastSequence: start})
	if err != nil {
		return err
	}
	hasOffset = true
	eventsCh := subscription.Events()
	errorsCh := subscription.Errors()
	for {
		if eventsCh == nil && errorsCh == nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrSubscriptionClosed
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err, ok := <-errorsCh:
			if !ok {
				errorsCh = nil
				continue
			}
			if err != nil {
				return fmt.Errorf("projection subscription: %w", err)
			}
		case envelope, ok := <-eventsCh:
			if !ok {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				eventsCh = nil
				continue
			}
			if err := validateEnvelopeScope(envelope, w.tenantID, w.streamID); err != nil {
				return err
			}
			if envelope.Sequence <= cursor {
				continue
			}
			if envelope.Sequence > cursor+1 {
				_, cursor, err = w.replay(ctx, cursor, projector, true, hasOffset, Report{LastSequence: cursor})
				if err != nil {
					return err
				}
				if envelope.Sequence <= cursor {
					continue
				}
				if envelope.Sequence != cursor+1 {
					return fmt.Errorf("%w: expected %d got %d", ErrSequenceGap, cursor+1, envelope.Sequence)
				}
			}
			report := Report{LastSequence: cursor}
			if err := w.applyEnvelope(ctx, envelope, projector, &report); err != nil {
				return err
			}
			digest, err := w.projectionDigest(ctx)
			if err != nil {
				return err
			}
			if err := w.putOffset(ctx, envelope.Sequence, digest, cursor); err != nil {
				return err
			}
			cursor = envelope.Sequence
		}
	}
}

func (w *Worker) validateCall(ctx context.Context, projector Projector) error {
	if w == nil || w.events == nil || w.store == nil || w.offsets == nil {
		return ErrInvalidConfig
	}
	if projector == nil {
		return fmt.Errorf("%w: projector is required", ErrInvalidConfig)
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidConfig)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tenantID, err := scope.Tenant(ctx)
	if err != nil {
		return err
	}
	if tenantID != w.tenantID {
		return projectionport.ErrTenantMismatch
	}
	return nil
}

func (w *Worker) readOffset(ctx context.Context) (projectionport.Offset, bool, error) {
	offset, err := w.offsets.GetOffset(ctx, w.tenantID, w.projectionName, w.streamID)
	if errors.Is(err, projectionport.ErrOffsetNotFound) {
		digest, digestErr := w.projectionDigest(ctx)
		if digestErr != nil {
			return projectionport.Offset{}, false, digestErr
		}
		return projectionport.Offset{TenantID: w.tenantID, Projection: w.projectionName, PartitionID: w.streamID, ProjectionDigest: digest}, false, nil
	}
	if err != nil {
		return projectionport.Offset{}, false, err
	}
	head, _, err := w.events.Head(ctx, w.streamID)
	if err != nil {
		return projectionport.Offset{}, false, fmt.Errorf("read projection stream head: %w", err)
	}
	if offset.LastSequence > head {
		return projectionport.Offset{}, false, fmt.Errorf("%w: offset sequence %d exceeds stream head %d", ErrDiverged, offset.LastSequence, head)
	}
	digest, err := w.projectionDigest(ctx)
	if err != nil {
		return projectionport.Offset{}, false, err
	}
	if !strings.EqualFold(digest, offset.ProjectionDigest) {
		return projectionport.Offset{}, false, fmt.Errorf("%w: offset digest does not match stored records", ErrDiverged)
	}
	return offset, true, nil
}

func (w *Worker) replay(ctx context.Context, after int64, projector Projector, persist, hasOffset bool, report Report) (Report, int64, error) {
	if after < 0 {
		return report, after, fmt.Errorf("%w: negative starting sequence", ErrInvalidConfig)
	}
	cursor := after
	previousDigest, err := w.previousDigest(ctx, after)
	if err != nil {
		return report, cursor, err
	}
	for {
		batch, err := w.events.Read(ctx, w.streamID, cursor, w.pageSize)
		if err != nil {
			return report, cursor, fmt.Errorf("read projection event batch after %d: %w", cursor, err)
		}
		if len(batch) == 0 {
			break
		}
		if err := coreevent.ValidateChainFrom(batch, cursor, previousDigest); err != nil {
			return report, cursor, fmt.Errorf("%w: %v", ErrDiverged, err)
		}
		for _, envelope := range batch {
			if envelope.Sequence != cursor+1 {
				return report, cursor, fmt.Errorf("%w: expected %d got %d", ErrSequenceGap, cursor+1, envelope.Sequence)
			}
			if err := w.applyEnvelope(ctx, envelope, projector, &report); err != nil {
				return report, cursor, err
			}
			cursor = envelope.Sequence
			previousDigest = envelope.EnvelopeDigest
		}
		if persist {
			digest, err := w.projectionDigest(ctx)
			if err != nil {
				return report, cursor, err
			}
			expected := cursor - int64(len(batch))
			if !hasOffset && expected < 0 {
				expected = 0
			}
			if err := w.putOffset(ctx, cursor, digest, expected); err != nil {
				return report, cursor, err
			}
			hasOffset = true
		}
		if len(batch) < w.pageSize {
			break
		}
	}
	report.LastSequence = cursor
	return report, cursor, nil
}

func (w *Worker) previousDigest(ctx context.Context, after int64) (string, error) {
	if after == 0 {
		return "", nil
	}
	batch, err := w.events.Read(ctx, w.streamID, after-1, 1)
	if err != nil {
		return "", fmt.Errorf("read projection predecessor at %d: %w", after, err)
	}
	if len(batch) != 1 || batch[0].Sequence != after {
		return "", fmt.Errorf("%w: predecessor sequence %d is unavailable", ErrDiverged, after)
	}
	if err := validateEnvelopeScope(batch[0], w.tenantID, w.streamID); err != nil {
		return "", err
	}
	return batch[0].EnvelopeDigest, nil
}

func (w *Worker) applyEnvelope(ctx context.Context, envelope coreevent.Envelope, projector Projector, report *Report) error {
	if err := validateEnvelopeScope(envelope, w.tenantID, w.streamID); err != nil {
		return err
	}
	mutations, err := projector(ctx, envelope)
	if err != nil {
		return fmt.Errorf("project event %d: %w", envelope.Sequence, err)
	}
	if err := validateMutations(mutations); err != nil {
		return err
	}
	report.Events++
	for _, mutation := range mutations {
		report.Mutations++
		changed, deleted, skipped, err := w.applyMutation(ctx, envelope, mutation)
		if err != nil {
			return fmt.Errorf("apply projection mutation at sequence %d: %w", envelope.Sequence, err)
		}
		if changed {
			report.UpdatedRecords++
		}
		if deleted {
			report.DeletedRecords++
		}
		if skipped {
			report.SkippedMutations++
		}
	}
	return nil
}

func validateEnvelopeScope(envelope coreevent.Envelope, tenantID, streamID string) error {
	if envelope.TenantID != tenantID || envelope.StreamID != streamID {
		return fmt.Errorf("%w: event %s is outside worker scope", ErrScopeMismatch, envelope.EventID)
	}
	if err := envelope.VerifyStored(); err != nil {
		return fmt.Errorf("%w: event %s failed integrity verification: %v", ErrDiverged, envelope.EventID, err)
	}
	return nil
}

func validateMutations(mutations []Mutation) error {
	seen := make(map[string]struct{}, len(mutations))
	for _, mutation := range mutations {
		if strings.TrimSpace(mutation.Key) == "" || strings.TrimSpace(mutation.Key) != mutation.Key || strings.ContainsAny(mutation.Key, "\x00\r\n") {
			return fmt.Errorf("%w: mutation key is invalid", ErrInvalidConfig)
		}
		if mutation.Delete && len(mutation.Payload) != 0 {
			return fmt.Errorf("%w: delete mutation %q contains a payload", ErrInvalidConfig, mutation.Key)
		}
		if _, exists := seen[mutation.Key]; exists {
			return fmt.Errorf("%w: key %q", ErrDuplicateMutation, mutation.Key)
		}
		seen[mutation.Key] = struct{}{}
	}
	return nil
}

func (w *Worker) applyMutation(ctx context.Context, envelope coreevent.Envelope, mutation Mutation) (changed, deleted, skipped bool, err error) {
	for attempt := 0; attempt <= w.casRetries; attempt++ {
		current, getErr := w.store.Get(ctx, w.tenantID, w.projectionName, mutation.Key)
		found := getErr == nil
		if getErr != nil && !errors.Is(getErr, projectionport.ErrNotFound) {
			return false, false, false, getErr
		}
		if found {
			if current.SourceStream != envelope.StreamID {
				return false, false, false, fmt.Errorf("%w: record %q belongs to stream %q", ErrDiverged, mutation.Key, current.SourceStream)
			}
			if current.SourceSequence > envelope.Sequence {
				return false, false, true, nil
			}
			if current.SourceSequence == envelope.Sequence {
				if mutation.Delete {
					if deleteErr := w.store.Delete(ctx, w.tenantID, w.projectionName, mutation.Key, current.Version); deleteErr == nil {
						return false, true, false, nil
					} else if errors.Is(deleteErr, projectionport.ErrConflict) {
						continue
					} else if errors.Is(deleteErr, projectionport.ErrNotFound) {
						return false, false, true, nil
					} else {
						return false, false, false, deleteErr
					}
				}
				if string(current.Payload) != string(mutation.Payload) {
					return false, false, false, fmt.Errorf("%w: record %q has a different payload at sequence %d", ErrDiverged, mutation.Key, envelope.Sequence)
				}
				return false, false, true, nil
			}
		}
		if mutation.Delete {
			if !found {
				return false, false, true, nil
			}
			if deleteErr := w.store.Delete(ctx, w.tenantID, w.projectionName, mutation.Key, current.Version); deleteErr == nil {
				return false, true, false, nil
			} else if errors.Is(deleteErr, projectionport.ErrConflict) {
				continue
			} else if errors.Is(deleteErr, projectionport.ErrNotFound) {
				return false, false, true, nil
			} else {
				return false, false, false, deleteErr
			}
		}
		version, expected := int64(1), int64(0)
		if found {
			version, expected = current.Version+1, current.Version
		}
		item := projectionport.Record{
			TenantID: w.tenantID, Projection: w.projectionName, Key: mutation.Key,
			Version: version, SourceStream: envelope.StreamID, SourceSequence: envelope.Sequence,
			Digest: projectionport.Digest(mutation.Payload), Payload: append([]byte(nil), mutation.Payload...), UpdatedAt: w.clock.Now().UTC(),
		}
		if putErr := w.store.Put(ctx, item, expected); putErr == nil {
			return true, false, false, nil
		} else if errors.Is(putErr, projectionport.ErrConflict) {
			continue
		} else {
			return false, false, false, putErr
		}
	}
	return false, false, false, projectionport.ErrConflict
}

func (w *Worker) projectionDigest(ctx context.Context) (string, error) {
	records, err := w.store.List(ctx, w.tenantID, w.projectionName)
	if err != nil {
		return "", fmt.Errorf("list projection records for digest: %w", err)
	}
	filtered := records[:0]
	for _, record := range records {
		if record.SourceStream == w.streamID {
			filtered = append(filtered, record)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Key < filtered[j].Key })
	canonical := make([]digestRecord, 0, len(filtered))
	for _, record := range filtered {
		canonical = append(canonical, digestRecord{
			Key: record.Key, Version: record.Version, SourceStream: record.SourceStream,
			SourceSequence: record.SourceSequence, Digest: record.Digest, Payload: record.Payload,
		})
	}
	digest, err := coreencoding.Digest(struct {
		TenantID   string         `json:"tenant_id"`
		Projection string         `json:"projection"`
		Records    []digestRecord `json:"records"`
	}{TenantID: w.tenantID, Projection: w.projectionName, Records: canonical})
	if err != nil {
		return "", fmt.Errorf("digest projection records: %w", err)
	}
	return digest, nil
}

type digestRecord struct {
	Key            string `json:"key"`
	Version        int64  `json:"version"`
	SourceStream   string `json:"source_stream"`
	SourceSequence int64  `json:"source_sequence"`
	Digest         string `json:"digest"`
	Payload        []byte `json:"payload"`
}

func (w *Worker) putOffset(ctx context.Context, sequence int64, digest string, expected int64) error {
	if err := w.offsets.PutOffset(ctx, projectionport.Offset{
		TenantID: w.tenantID, Projection: w.projectionName, PartitionID: w.streamID,
		LastSequence: sequence, ProjectionDigest: digest, UpdatedAt: w.clock.Now().UTC(),
	}, expected); err != nil {
		return fmt.Errorf("persist projection offset at sequence %d: %w", sequence, err)
	}
	return nil
}
