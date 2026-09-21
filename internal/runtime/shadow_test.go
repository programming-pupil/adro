package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	storesqlite "github.com/adro-project/adro/adapters/eventstore/sqlite"
	coreevent "github.com/adro-project/adro/core/event"
	"github.com/adro-project/adro/core/testkit"
	"github.com/adro-project/adro/internal/durable"
	"github.com/adro-project/adro/ports/eventstore"
	_ "modernc.org/sqlite"
)

func TestEventStoreShadowMirrorsIdempotentlyWithoutOutbox(t *testing.T) {
	store, path := newShadowSQLiteStore(t)
	shadow, err := NewEventStoreShadow(store)
	if err != nil {
		t.Fatal(err)
	}
	journal := mustJournal(t, "")
	if err := journal.SetShadowWithError(shadow, time.Second); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Shutdown(context.Background()) })
	scope := testScope()
	if _, err := journal.Append(Input{
		EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID,
		Scope: scope, CorrelationID: "correlation-1", IdempotencyKey: "turn-1",
		Payload: map[string]any{"input": "hello"},
	}); err != nil {
		t.Fatal(err)
	}

	if err := journal.SetShadowWithError(shadow, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := journal.WaitShadow(context.Background()); err != nil {
		t.Fatal(err)
	}
	reports := journal.ShadowReports()
	if len(reports) != 1 || !reports[0].Matched() || reports[0].LegacyCount != 1 || reports[0].ShadowCount != 1 {
		t.Fatalf("shadow reports=%+v", reports)
	}
	head, _, err := store.Head(context.Background(), ShadowStreamID(scope))
	if err != nil || head != 1 {
		t.Fatalf("shadow head=%d err=%v", head, err)
	}
	if count := sqliteRowCount(t, path, "event_outbox"); count != 0 {
		t.Fatalf("shadow emitted %d outbox messages", count)
	}
}

func TestEventStoreShadowDetectsExistingPrefixDivergenceBeforeAppend(t *testing.T) {
	store, _ := newShadowSQLiteStore(t)
	shadow, err := NewEventStoreShadow(store)
	if err != nil {
		t.Fatal(err)
	}
	journal := mustJournal(t, "")
	t.Cleanup(func() { _ = journal.Shutdown(context.Background()) })
	scope := testScope()
	for index := 1; index <= 2; index++ {
		if _, err := journal.Append(Input{
			EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID,
			Scope: scope, CorrelationID: "correlation-1", IdempotencyKey: fmt.Sprintf("turn-%d", index),
			Payload: map[string]any{"index": index},
		}); err != nil {
			t.Fatal(err)
		}
	}
	legacy := journal.List(scope)
	diverged := legacy[0]
	diverged.Payload = json.RawMessage(`{"index":"changed"}`)
	mapped, err := mapShadowEvent(ShadowStreamID(scope), diverged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(context.Background(), eventstore.AppendRequest{
		StreamID: ShadowStreamID(scope), ExpectedSequence: 0, Events: []coreevent.Uncommitted{mapped},
	}); err != nil {
		t.Fatal(err)
	}

	report := shadow.Mirror(context.Background(), scope, legacy)
	if !report.Diverged || report.DivergenceAt != 1 || report.Error != ErrShadowDiverged.Error() {
		t.Fatalf("shadow report=%+v", report)
	}
	head, _, err := store.Head(context.Background(), ShadowStreamID(scope))
	if err != nil || head != 1 {
		t.Fatalf("diverged prefix was extended: head=%d err=%v", head, err)
	}
}

func TestJournalShadowFailureDoesNotFailLegacyCommit(t *testing.T) {
	shadow, err := NewEventStoreShadow(unavailableEventStore{})
	if err != nil {
		t.Fatal(err)
	}
	journal := mustJournal(t, "")
	if err := journal.SetShadowWithError(shadow, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Shutdown(context.Background()) })
	scope := testScope()
	event, err := journal.Append(Input{
		EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID,
		Scope: scope, IdempotencyKey: "turn-unavailable", Payload: map[string]any{"input": "committed"},
	})
	if err != nil || event.EventID == "" || len(journal.List(scope)) != 1 {
		t.Fatalf("legacy event=%+v err=%v events=%+v", event, err, journal.List(scope))
	}
	deadline, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := journal.WaitShadow(deadline); err == nil {
		t.Fatal("unavailable shadow unexpectedly converged")
	}
	reports := journal.ShadowReports()
	if len(reports) != 1 || reports[0].Error == "" || reports[0].Matched() {
		t.Fatalf("shadow reports=%+v", reports)
	}
}

func TestJournalShadowShutdownCancelsPendingWork(t *testing.T) {
	shadow, err := NewEventStoreShadow(unavailableEventStore{})
	if err != nil {
		t.Fatal(err)
	}
	journal := mustJournal(t, "")
	t.Cleanup(func() {
		_ = journal.Shutdown(context.Background())
	})
	if err := journal.SetShadowWithError(shadow, time.Second); err != nil {
		t.Fatal(err)
	}
	scope := testScope()
	if _, err := journal.Append(Input{
		EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID,
		Scope: scope, IdempotencyKey: "turn-shutdown", Payload: map[string]any{"input": "stop"},
	}); err != nil {
		t.Fatal(err)
	}

	deadline, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := journal.WaitShadow(deadline); err == nil {
		t.Fatal("unavailable shadow unexpectedly converged")
	}

	started := time.Now()
	if err := journal.Shutdown(context.Background()); err == nil {
		t.Fatal("shutdown unexpectedly discarded pending shadow work")
	} else if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("shutdown blocked for %s: %v", elapsed, err)
	}
	if reports := journal.ShadowReports(); len(reports) != 1 || !reports[0].Pending {
		t.Fatalf("pending shadow reports=%+v", reports)
	}
}

func TestJournalShadowSetupFailsWhenQueueCannotPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	journal := mustJournal(t, path)
	if _, err := journal.Append(Input{
		EventType: EventTurnStarted, AggregateType: "run", AggregateID: testScope().RunID,
		Scope: testScope(), IdempotencyKey: "shadow-setup", Payload: map[string]any{"ok": true},
	}); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("shadow queue write failed")
	restore := durable.SetFaultInjector(func(point string) error {
		if point == "runtime.journal.write" {
			return injected
		}
		return nil
	})
	err := journal.SetShadowWithError(successfulShadow{}, time.Second)
	restore()
	if !errors.Is(err, injected) || journal.shadow != nil || journal.shadowDone != nil {
		t.Fatalf("failed shadow setup: err=%v shadow=%v worker=%v", err, journal.shadow, journal.shadowDone)
	}
	restarted := mustJournal(t, path)
	if len(restarted.shadowPending) != 0 {
		t.Fatalf("failed setup persisted shadow work: %+v", restarted.shadowPending)
	}
}

func TestJournalRestartBackfillsEventStoreShadow(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "runtime.json")
	journal := mustJournal(t, journalPath)
	scope := testScope()
	if _, err := journal.Append(Input{
		EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID,
		Scope: scope, IdempotencyKey: "turn-restart", Payload: map[string]any{"input": "restore"},
	}); err != nil {
		t.Fatal(err)
	}
	restarted := mustJournal(t, journalPath)
	t.Cleanup(func() { _ = restarted.Shutdown(context.Background()) })
	store, _ := newShadowSQLiteStore(t)
	shadow, err := NewEventStoreShadow(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.SetShadowWithError(shadow, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := restarted.WaitShadow(context.Background()); err != nil {
		t.Fatal(err)
	}
	reports := restarted.ShadowReports()
	if len(reports) != 1 || !reports[0].Matched() {
		t.Fatalf("restart reports=%+v", reports)
	}
}

func TestJournalPersistenceFaultsPreserveAuthoritativeMemoryAndRecovery(t *testing.T) {
	points := []struct {
		name            string
		point           string
		diskHasCommit   bool
		finalEventCount int
	}{
		{name: "write", point: "runtime.journal.write", finalEventCount: 1},
		{name: "rename", point: "runtime.journal.rename", finalEventCount: 1},
		{name: "directory_sync", point: "runtime.journal.directory_sync", diskHasCommit: true, finalEventCount: 2},
	}
	for _, test := range points {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runtime.json")
			journal := mustJournal(t, path)
			scope := testScope()
			injected := errors.New("injected journal durability failure")
			restore := durable.SetFaultInjector(func(point string) error {
				if point == test.point {
					return injected
				}
				return nil
			})
			_, err := journal.Append(Input{
				EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID,
				Scope: scope, IdempotencyKey: "faulted-append", Payload: map[string]any{"point": test.point},
			})
			restore()
			if !errors.Is(err, injected) {
				t.Fatalf("append error=%v, want injected failure", err)
			}
			if got := len(journal.List(scope)); got != 0 || journal.revision != 0 {
				t.Fatalf("authoritative memory advanced after failed append: revision=%d events=%d", journal.revision, got)
			}

			restarted := mustJournal(t, path)
			if got := len(restarted.List(scope)); got != map[bool]int{true: 1, false: 0}[test.diskHasCommit] {
				t.Fatalf("restart events=%d, diskHasCommit=%t", got, test.diskHasCommit)
			}
			if err := restarted.Verify(); err != nil {
				t.Fatalf("restart verification failed: %v", err)
			}
			if _, err := restarted.Append(Input{
				EventType: EventTurnFinished, AggregateType: "run", AggregateID: scope.RunID,
				Scope: scope, IdempotencyKey: "recovery-append", Payload: map[string]any{"recovered": true},
			}); err != nil {
				t.Fatalf("recovery append failed: %v", err)
			}
			final, err := NewJournal(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(final.List(scope)); got != test.finalEventCount {
				t.Fatalf("final events=%d, want %d", got, test.finalEventCount)
			}
			if err := final.Verify(); err != nil {
				t.Fatalf("final verification failed: %v", err)
			}
		})
	}
}

func TestJournalShadowShutdownSucceedsAfterDrain(t *testing.T) {
	journal := mustJournal(t, "")
	shadow := successfulShadow{}
	if err := journal.SetShadowWithError(shadow, time.Second); err != nil {
		t.Fatal(err)
	}
	scope := testScope()
	if _, err := journal.Append(Input{
		EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID,
		Scope: scope, IdempotencyKey: "shutdown-drain", Payload: map[string]any{"ok": true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := journal.WaitShadow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := journal.Shutdown(context.Background()); err != nil {
		t.Fatalf("successful shadow shutdown failed: %v", err)
	}
	if reports := journal.ShadowReports(); len(reports) != 1 || !reports[0].Matched() {
		t.Fatalf("drained shadow reports=%+v", reports)
	}
}

func TestJournalShadowQueuesEachScopeAtItsOwnTarget(t *testing.T) {
	journal := mustJournal(t, "")
	first := testScope()
	second := first
	second.RunID = "other-run"
	for index, scope := range []Scope{first, second, first} {
		if _, err := journal.Append(Input{
			EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID,
			Scope: scope, IdempotencyKey: fmt.Sprintf("scope-%d", index), Payload: map[string]any{"index": index},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.SetShadowWithError(successfulShadow{}, time.Second); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := journal.WaitShadow(ctx); err != nil {
		t.Fatal(err)
	}
	if err := journal.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	reports := journal.ShadowReports()
	if len(reports) != 2 {
		t.Fatalf("shadow reports=%+v", reports)
	}
	for _, report := range reports {
		want := int64(1)
		if report.Scope == first {
			want = 2
		}
		if report.TargetCount != want || !report.Matched() {
			t.Fatalf("scope=%+v target=%d report=%+v", report.Scope, want, report)
		}
	}
}

func TestJournalShadowShutdownCancelsInFlightMirror(t *testing.T) {
	journal := mustJournal(t, "")
	shadow := newCancellableShadow()
	if err := journal.SetShadowWithError(shadow, time.Second); err != nil {
		t.Fatal(err)
	}
	scope := testScope()
	if _, err := journal.Append(Input{
		EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID,
		Scope: scope, IdempotencyKey: "shutdown-cancel", Payload: map[string]any{"ok": true},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-shadow.started:
	case <-time.After(time.Second):
		t.Fatal("shadow mirror did not start")
	}
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- journal.Shutdown(context.Background()) }()
	select {
	case <-shadow.cancelled:
	case <-time.After(time.Second):
		t.Fatal("in-flight shadow mirror did not observe cancellation")
	}
	select {
	case err := <-shutdownDone:
		if err == nil {
			t.Fatal("shutdown reported success with cancelled pending work")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not wait for the cancelled mirror")
	}
	if reports := journal.ShadowReports(); len(reports) != 1 || !reports[0].Pending {
		t.Fatalf("cancelled shadow reports=%+v", reports)
	}
}

func TestJournalShadowPendingQueueResumesAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	failed := mustJournal(t, path)
	if err := failed.SetShadowWithError(failingShadow{}, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	scope := testScope()
	if _, err := failed.Append(Input{
		EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID,
		Scope: scope, IdempotencyKey: "restart-pending", Payload: map[string]any{"ok": true},
	}); err != nil {
		t.Fatal(err)
	}
	waitContext, cancel := context.WithTimeout(context.Background(), time.Second)
	waitErr := failed.WaitShadow(waitContext)
	cancel()
	if waitErr == nil {
		t.Fatal("failing shadow unexpectedly converged")
	}
	if err := failed.Shutdown(context.Background()); err == nil {
		t.Fatal("shutdown discarded failed shadow work")
	}

	restarted := mustJournal(t, path)
	reports := restarted.ShadowReports()
	if len(reports) != 1 || !reports[0].Pending || reports[0].TargetCount != 1 {
		t.Fatalf("persisted pending reports=%+v", reports)
	}
	if err := restarted.SetShadowWithError(successfulShadow{}, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := restarted.WaitShadow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reports := restarted.ShadowReports(); len(reports) != 1 || !reports[0].Matched() {
		t.Fatalf("resumed shadow reports=%+v", reports)
	}
	if err := restarted.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestJournalConcurrentInstancesRebaseAppendHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	left := mustJournal(t, path)
	right := mustJournal(t, path)
	scope := testScope()
	start := make(chan struct{})
	errors := make(chan error, 2)
	go func() {
		<-start
		_, err := left.Append(Input{
			EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID,
			Scope: scope, IdempotencyKey: "concurrent-left", Payload: map[string]any{"writer": "left"},
		})
		errors <- err
	}()
	go func() {
		<-start
		_, err := right.Append(Input{
			EventType: EventTurnFinished, AggregateType: "run", AggregateID: scope.RunID,
			Scope: scope, IdempotencyKey: "concurrent-right", Payload: map[string]any{"writer": "right"},
		})
		errors <- err
	}()
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-errors; err != nil {
			t.Fatalf("concurrent append failed: %v", err)
		}
	}

	restarted := mustJournal(t, path)
	events := restarted.List(scope)
	if len(events) != 2 {
		t.Fatalf("rebased events=%d, want 2: %+v", len(events), events)
	}
	if events[0].Sequence != 1 || events[1].Sequence != 2 || events[1].PreviousHash != events[0].EnvelopeHash {
		t.Fatalf("rebased event chain=%+v", events)
	}
	if err := restarted.Verify(); err != nil {
		t.Fatalf("rebased journal verification failed: %v", err)
	}
}

func TestJournalStaleInstanceDoesNotResurrectDrainedShadowWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	first := mustJournal(t, path)
	if _, err := first.Append(Input{
		EventType: EventTurnStarted, AggregateType: "run", AggregateID: testScope().RunID,
		Scope: testScope(), IdempotencyKey: "shadow-rebase", Payload: map[string]any{"ok": true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := first.SetShadowWithError(failingShadow{}, time.Second); err != nil {
		t.Fatal(err)
	}
	stale := mustJournal(t, path)
	if err := first.Shutdown(context.Background()); err == nil {
		t.Fatal("pending work was discarded")
	}
	active := mustJournal(t, path)
	if err := active.SetShadowWithError(successfulShadow{}, time.Second); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := active.WaitShadow(ctx); err != nil {
		t.Fatal(err)
	}
	if err := active.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	committed, err := stale.persistCandidateLocked(stale.events, stale.leases, stale.effects, stale.shadowPending, stale.shadowReports)
	if err != nil {
		t.Fatal(err)
	}
	if len(committed.ShadowPending) != 0 || !committed.ShadowReports[shadowScopeKey(testScope())].Matched() {
		t.Fatalf("stale writer resurrected drained queue: %+v", committed)
	}
}

func TestJournalStaleInstanceCannotOverwriteLeaseFence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	left := mustJournal(t, path)
	right := mustJournal(t, path)
	scope := testScope()
	lease, err := left.AcquireLease(scope, "left", time.Minute, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	staleLeases := cloneLeases(right.leases)
	staleLeases[lease.Key] = Lease{Key: lease.Key, Owner: "right", FencingToken: 1}
	if _, err := right.persistCandidateLocked(right.events, staleLeases, right.effects, right.shadowPending, right.shadowReports); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale lease overwrite error=%v", err)
	}
	reread := mustJournal(t, path)
	if got := reread.leases[lease.Key]; got != lease {
		t.Fatalf("live lease overwritten: %+v", got)
	}
}

func TestJournalStaleAppendRejectsPeerIdempotencyAndLeaseChanges(t *testing.T) {
	t.Run("idempotency", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "runtime.json")
		stale := mustJournal(t, path)
		peer := mustJournal(t, path)
		input := Input{EventType: EventTurnStarted, AggregateType: "run", AggregateID: testScope().RunID,
			Scope: testScope(), IdempotencyKey: "shared-key", Payload: map[string]any{"source": "stale"}}
		candidate, err := stale.prepareLocked(stale.events, input)
		if err != nil {
			t.Fatal(err)
		}
		input.Payload = map[string]any{"source": "peer"}
		if _, err := peer.Append(input); err != nil {
			t.Fatal(err)
		}
		if _, err := stale.persistCandidateLocked([]Event{candidate}, stale.leases, stale.effects, stale.shadowPending, stale.shadowReports); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("stale idempotency key error=%v", err)
		}
		if got := len(mustJournal(t, path).List(testScope())); got != 1 {
			t.Fatalf("peer history changed: %d", got)
		}
	})
	t.Run("fencing", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "runtime.json")
		stale := mustJournal(t, path)
		scope := testScope()
		lease, err := stale.AcquireLease(scope, "owner", time.Minute, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := stale.prepareLocked(stale.events, Input{
			EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID,
			Scope: scope, WriterID: "owner", FencingToken: lease.FencingToken,
			Payload: map[string]any{"ok": true},
		})
		if err != nil {
			t.Fatal(err)
		}
		peer := mustJournal(t, path)
		if _, err := peer.AcquireLease(scope, "owner", time.Minute, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if _, err := stale.persistCandidateLocked([]Event{candidate}, stale.leases, stale.effects, stale.shadowPending, stale.shadowReports); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("stale fence error=%v", err)
		}
	})
}

func TestShadowMergePrefersNewerDurableObservation(t *testing.T) {
	scope := testScope()
	older := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	newer := older.Add(time.Second)
	baseReports := map[string]ShadowReport{
		shadowScopeKey(scope): {
			Scope: scope, CheckedAt: newer, Attempts: 1, Pending: false,
		},
	}
	overlayReports := map[string]ShadowReport{
		shadowScopeKey(scope): {
			Scope: scope, CheckedAt: older, Attempts: 3, Pending: true, Error: "stale failure",
		},
	}
	mergedReports := mergeShadowReports(baseReports, overlayReports)
	if got := mergedReports[shadowScopeKey(scope)]; got.CheckedAt != newer || got.Pending {
		t.Fatalf("stale shadow report replaced newer report: %+v", got)
	}

	baseWork := map[string]shadowWork{
		shadowScopeKey(scope): {
			Scope: scope, TargetCount: 1, Attempts: 1, UpdatedAt: newer,
		},
	}
	overlayWork := map[string]shadowWork{
		shadowScopeKey(scope): {
			Scope: scope, TargetCount: 1, Attempts: 3, UpdatedAt: older,
		},
	}
	mergedWork := mergeShadowWork(baseWork, overlayWork)
	if got := mergedWork[shadowScopeKey(scope)]; got.UpdatedAt != newer || got.Attempts != 1 {
		t.Fatalf("stale shadow work replaced newer work: %+v", got)
	}
}

type successfulShadow struct{}

func (successfulShadow) Mirror(ctx context.Context, scope Scope, events []Event) ShadowReport {
	select {
	case <-ctx.Done():
		return ShadowReport{Scope: scope, StreamID: ShadowStreamID(scope), Error: ctx.Err().Error()}
	default:
	}
	report := ShadowReport{
		Scope: scope, StreamID: ShadowStreamID(scope), LegacyCount: int64(len(events)), ShadowCount: int64(len(events)),
		CheckedAt: time.Now().UTC(),
	}
	report.LegacyDigest = projectionDigest(events, &report)
	report.ShadowDigest = report.LegacyDigest
	return report
}

type failingShadow struct{}

func (failingShadow) Mirror(context.Context, Scope, []Event) ShadowReport {
	return ShadowReport{Error: "shadow unavailable"}
}

type cancellableShadow struct {
	started    chan struct{}
	cancelled  chan struct{}
	startOnce  sync.Once
	cancelOnce sync.Once
}

func newCancellableShadow() *cancellableShadow {
	return &cancellableShadow{started: make(chan struct{}), cancelled: make(chan struct{})}
}

func (s *cancellableShadow) Mirror(ctx context.Context, scope Scope, events []Event) ShadowReport {
	s.startOnce.Do(func() { close(s.started) })
	<-ctx.Done()
	s.cancelOnce.Do(func() { close(s.cancelled) })
	return ShadowReport{Scope: scope, StreamID: ShadowStreamID(scope), LegacyCount: int64(len(events)), ShadowCount: int64(len(events))}
}

func newShadowSQLiteStore(t *testing.T) (*storesqlite.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shadow.db")
	store, err := storesqlite.Open(path, storesqlite.Options{
		Clock: testkit.NewManualClock(time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)),
		IDs:   &testkit.SequenceIDs{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func sqliteRowCount(t *testing.T, path, table string) int {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

type unavailableEventStore struct{}

func (unavailableEventStore) Append(context.Context, eventstore.AppendRequest) (eventstore.AppendResult, error) {
	return eventstore.AppendResult{}, errors.New("shadow unavailable")
}

func (unavailableEventStore) Read(context.Context, string, int64, int) ([]coreevent.Envelope, error) {
	return nil, errors.New("shadow unavailable")
}

func (unavailableEventStore) Head(context.Context, string) (int64, string, error) {
	return 0, "", errors.New("shadow unavailable")
}

func (unavailableEventStore) Subscribe(context.Context, eventstore.Subscription) (eventstore.EventSubscription, error) {
	return nil, errors.New("shadow unavailable")
}
