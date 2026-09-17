package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	storesqlite "github.com/adro-project/adro/adapters/eventstore/sqlite"
	coreevent "github.com/adro-project/adro/core/event"
	"github.com/adro-project/adro/core/testkit"
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
	journal.SetShadow(shadow, time.Second)
	scope := testScope()
	if _, err := journal.Append(Input{
		EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID,
		Scope: scope, CorrelationID: "correlation-1", IdempotencyKey: "turn-1",
		Payload: map[string]any{"input": "hello"},
	}); err != nil {
		t.Fatal(err)
	}

	reports := journal.SetShadow(shadow, time.Second)
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
	journal.SetShadow(shadow, 20*time.Millisecond)
	scope := testScope()
	event, err := journal.Append(Input{
		EventType: EventTurnStarted, AggregateType: "run", AggregateID: scope.RunID,
		Scope: scope, IdempotencyKey: "turn-unavailable", Payload: map[string]any{"input": "committed"},
	})
	if err != nil || event.EventID == "" || len(journal.List(scope)) != 1 {
		t.Fatalf("legacy event=%+v err=%v events=%+v", event, err, journal.List(scope))
	}
	reports := journal.ShadowReports()
	if len(reports) != 1 || reports[0].Error == "" || reports[0].Matched() {
		t.Fatalf("shadow reports=%+v", reports)
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
	store, _ := newShadowSQLiteStore(t)
	shadow, err := NewEventStoreShadow(store)
	if err != nil {
		t.Fatal(err)
	}
	reports := restarted.SetShadow(shadow, time.Second)
	if len(reports) != 1 || !reports[0].Matched() {
		t.Fatalf("restart reports=%+v", reports)
	}
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
