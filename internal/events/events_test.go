package events

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/adro-project/adro/internal/durable"
)

func TestBusDeduplicatesAndCursors(t *testing.T) {
	b := NewBus()
	e := New("test.v1", "r", "r1", "t", "w", 1, nil)
	if err := b.Publish(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	got, next := b.List("r1", "", 10)
	if len(got) != 1 || next != "" {
		t.Fatalf("got=%d next=%q", len(got), next)
	}
	if got[0].EventID != e.EventID {
		t.Fatal("event changed")
	}
	if got[0].Sequence != 1 || got[0].PayloadHash == "" {
		t.Fatalf("missing event integrity metadata: %+v", got[0])
	}
}

func TestPersistentBusSequencesRemainUniqueAcrossPeerWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	first, err := NewPersistentBus(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPersistentBus(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Publish(context.Background(), New("peer.one.v1", "run", "r1", "t", "w", 1, map[string]any{"source": "first"})); err != nil {
		t.Fatal(err)
	}
	if err := second.Publish(context.Background(), New("peer.two.v1", "run", "r2", "t", "w", 1, map[string]any{"source": "second"})); err != nil {
		t.Fatal(err)
	}
	items, _ := second.List("", "", 10)
	if len(items) != 2 || items[0].Sequence != 1 || items[1].Sequence != 2 || items[0].PayloadHash == "" || items[1].PayloadHash == "" {
		t.Fatalf("peer sequence/hash metadata is not canonical: %+v", items)
	}
}

func TestPersistentBusRejectsPayloadTamper(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	b, err := NewPersistentBus(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(context.Background(), New("tamper.v1", "run", "r1", "t", "w", 1, map[string]any{"state": "original"})); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state persistedEvents
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	state.Events[0].Payload["state"] = "tampered"
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPersistentBus(path); err == nil {
		t.Fatal("tampered event payload was accepted")
	}
}

func TestPersistentBusRejectsEnvelopeMetadataTamper(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	b, err := NewPersistentBus(path)
	if err != nil {
		t.Fatal(err)
	}
	event := New("tamper.metadata.v1", "run", "r1", "tenant-a", "workspace-a", 1, map[string]any{"state": "original"})
	if err := b.Publish(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state persistedEvents
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	state.Events[0].TenantID = "tenant-attacker"
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPersistentBus(path); err == nil {
		t.Fatal("tampered envelope metadata was accepted")
	}
}

func TestPersistentBusRejectsSequenceGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	b, err := NewPersistentBus(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := b.Publish(context.Background(), New("sequence.v1", "run", "r1", "tenant", "workspace", int64(i+1), map[string]any{"i": i})); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state persistedEvents
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	state.Events[1].Sequence = 4
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPersistentBus(path); err == nil {
		t.Fatal("sequence gap was normalized instead of rejected")
	}
}

func TestReplayRejectsUnknownCursor(t *testing.T) {
	b := NewBus()
	if err := b.Publish(context.Background(), New("cursor.v1", "run", "r1", "tenant", "workspace", 1, nil)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Replay("consumer", "r1", "missing-cursor", 10); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("expected ErrInvalidCursor, got %v", err)
	}
}

func TestReplayScopedDoesNotReuseCursorAcrossStreams(t *testing.T) {
	b := NewBus()
	first := New("scope.v1", "run", "r1", "tenant-a", "workspace-a", 1, nil)
	second := New("scope.v1", "run", "r2", "tenant-a", "workspace-a", 1, nil)
	if err := b.Publish(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if err := b.AckScoped("consumer", "tenant-a", "workspace-a", "r1", first.EventID); err != nil {
		t.Fatal(err)
	}
	items, _, err := b.ReplayScoped("consumer", "tenant-a", "workspace-a", "r2", "", 10)
	if err != nil || len(items) != 1 || items[0].EventID != second.EventID {
		t.Fatalf("scoped replay=%+v err=%v", items, err)
	}
}

func TestReplayScopedFiltersWorkspaceBeforeApplyingPagination(t *testing.T) {
	b := NewBus()
	a := New("scope.v1", "run", "a", "tenant", "workspace-a", 1, map[string]any{"id": "a"})
	bEvent := New("scope.v1", "run", "b", "tenant", "workspace-b", 1, map[string]any{"id": "b"})
	c := New("scope.v1", "run", "c", "tenant", "workspace-a", 1, map[string]any{"id": "c"})
	for _, event := range []Envelope{a, bEvent, c} {
		if err := b.Publish(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	items, next, err := b.ReplayScoped("consumer", "tenant", "workspace-a", "", "", 1)
	if err != nil || len(items) != 1 || items[0].AggregateID != "a" || next == "" {
		t.Fatalf("first scoped page=%+v next=%q err=%v", items, next, err)
	}
	items, next, err = b.ReplayScoped("consumer", "tenant", "workspace-a", "", next, 1)
	if err != nil || len(items) != 1 || items[0].AggregateID != "c" || next == "" {
		t.Fatalf("second scoped page=%+v next=%q err=%v", items, next, err)
	}
	items, next, err = b.ReplayScoped("consumer", "tenant", "workspace-a", "", next, 1)
	if err != nil || len(items) != 0 || next != "" {
		t.Fatalf("scoped stream should be exhausted: items=%+v next=%q err=%v", items, next, err)
	}
	if _, _, err := b.ReplayScoped("consumer", "tenant", "workspace-a", "", bEvent.EventID, 10); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("foreign workspace cursor was accepted: %v", err)
	}
}

func TestAckScopedAllowsWorkspaceStreamWithoutAggregate(t *testing.T) {
	b := NewBus()
	event := New("scope.v1", "run", "run-1", "", "workspace", 1, nil)
	if err := b.Publish(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := b.AckScoped("consumer", "", "workspace", "", event.EventID); err != nil {
		t.Fatal(err)
	}
	items, _, err := b.ReplayScoped("consumer", "", "workspace", "", "", 10)
	if err != nil || len(items) != 0 {
		t.Fatalf("ack was not scoped to workspace stream: items=%+v err=%v", items, err)
	}
}

func TestSlowSubscriberReceivesGapBeforeLiveStreamResumes(t *testing.T) {
	b := NewBus()
	updates, cancel := b.Subscribe(1)
	defer cancel()
	for i := 0; i < 3; i++ {
		if err := b.Publish(context.Background(), New("overflow.v1", "run", "r1", "t", "w", int64(i+1), map[string]any{"i": i})); err != nil {
			t.Fatal(err)
		}
	}
	if first := <-updates; first.Sequence != 1 {
		t.Fatalf("first event=%+v", first)
	}
	if err := b.Publish(context.Background(), New("overflow.v1", "run", "r1", "t", "w", 4, map[string]any{"i": 3})); err != nil {
		t.Fatal(err)
	}
	gap := <-updates
	if gap.EventType != "stream.gap.v1" || gap.Payload["dropped_count"] != float64(2) && gap.Payload["dropped_count"] != int64(2) || gap.Payload["from_sequence"] == nil || gap.Payload["to_sequence"] == nil {
		t.Fatalf("overflow was not surfaced as a replayable gap: %+v", gap)
	}
	items, _ := b.List("r1", "", 10)
	if len(items) != 4 {
		t.Fatalf("durable history lost events after overflow: %+v", items)
	}
}

func TestPersistentBusMergesPeerWritersAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	first, err := NewPersistentBus(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPersistentBus(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New("a.v1", "run", "r1", "t", "w", 1, nil)
	b := New("b.v1", "run", "r2", "t", "w", 1, nil)
	if err := first.Publish(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := second.Publish(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if err := first.Reload(); err != nil {
		t.Fatal(err)
	}
	items, _ := first.List("", "", 10)
	if len(items) != 2 {
		t.Fatalf("peer event lost after merge: %+v", items)
	}
	if items[1].PreviousHash != items[0].EnvelopeHash {
		t.Fatalf("peer merge broke event chain: %+v", items)
	}
	if _, err := NewPersistentBus(path); err != nil {
		t.Fatalf("peer-merged event chain cannot be reopened: %v", err)
	}
}

func TestPersistentBusPeerMergePreservesCommittedHashes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	first, err := NewPersistentBus(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPersistentBus(path)
	if err != nil {
		t.Fatal(err)
	}

	firstEvent := New("immutable.first.v1", "run", "r1", "tenant", "workspace", 1, map[string]any{"source": "first"})
	if err := first.Publish(context.Background(), firstEvent); err != nil {
		t.Fatal(err)
	}
	if err := second.Reload(); err != nil {
		t.Fatal(err)
	}
	committed, _ := first.List("r1", "", 10)
	if len(committed) != 1 {
		t.Fatalf("initial committed history=%+v", committed)
	}
	committedSequence := committed[0].Sequence
	committedPreviousHash := committed[0].PreviousHash
	committedEnvelopeHash := committed[0].EnvelopeHash

	peerEvent := New("immutable.peer.v1", "run", "r2", "tenant", "workspace", 1, map[string]any{"source": "peer"})
	middleEvent := New("immutable.middle.v1", "run", "r3", "tenant", "workspace", 1, map[string]any{"source": "middle"})
	if err := first.Publish(context.Background(), middleEvent); err != nil {
		t.Fatal(err)
	}
	if err := second.Publish(context.Background(), peerEvent); err != nil {
		t.Fatal(err)
	}
	if err := first.Reload(); err != nil {
		t.Fatal(err)
	}
	items, _ := first.List("", "", 10)
	if len(items) != 3 {
		t.Fatalf("peer history=%+v", items)
	}
	if items[0].Sequence != committedSequence || items[0].PreviousHash != committedPreviousHash || items[0].EnvelopeHash != committedEnvelopeHash {
		t.Fatalf("committed event was resealed during peer merge: before=(%d,%q,%q) after=(%d,%q,%q)", committedSequence, committedPreviousHash, committedEnvelopeHash, items[0].Sequence, items[0].PreviousHash, items[0].EnvelopeHash)
	}
	if items[1].EventID != middleEvent.EventID || items[2].EventID != peerEvent.EventID {
		t.Fatalf("peer merge changed committed order: %+v", items)
	}
	if items[2].PreviousHash != items[1].EnvelopeHash {
		t.Fatalf("new tail did not link to the committed prefix: %+v", items)
	}
	if _, err := NewPersistentBus(path); err != nil {
		t.Fatalf("merged immutable history cannot be reopened: %v", err)
	}
}

func TestPersistentBusFaultBeforeRenameDoesNotExposeEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	b, err := NewPersistentBus(path)
	if err != nil {
		t.Fatal(err)
	}
	restore := durable.SetFaultInjector(func(point string) error {
		if point == "events.snapshot.before_rename" {
			return errors.New("injected crash")
		}
		return nil
	})
	defer restore()
	if err := b.Publish(context.Background(), New("fault.v1", "run", "r1", "t", "w", 1, nil)); err == nil {
		t.Fatal("expected injected persistence error")
	}
	items, _ := b.List("r1", "", 10)
	if len(items) != 0 {
		t.Fatalf("failed event remained in memory: %+v", items)
	}
}

func TestPersistentBusAckAndReplaySurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	b, err := NewPersistentBus(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := b.Publish(context.Background(), New("replay.v1", "run", "r1", "t", "w", int64(i+1), map[string]any{"i": i})); err != nil {
			t.Fatal(err)
		}
	}
	items, _, err := b.Replay("consumer-1", "r1", "", 2)
	if err != nil || len(items) != 2 {
		t.Fatalf("initial replay=%+v err=%v", items, err)
	}
	if err := b.Ack("consumer-1", items[1].EventID); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPersistentBus(path)
	if err != nil {
		t.Fatal(err)
	}
	items, _, err = restarted.Replay("consumer-1", "r1", "", 10)
	if err != nil || len(items) != 1 || items[0].Payload["i"] != float64(2) {
		t.Fatalf("ack was not durable: %+v err=%v", items, err)
	}
}

func TestBusDeduplicatesProviderRetries(t *testing.T) {
	b := NewBus()
	a := New("execution.message.v1", "run", "r1", "t", "w", 1, nil)
	a.Provider, a.ProviderEventID = "local", "task:1"
	_ = b.Publish(context.Background(), a)
	_ = b.Publish(context.Background(), Envelope{EventID: "different", EventType: a.EventType, AggregateType: a.AggregateType, AggregateID: a.AggregateID, Provider: a.Provider, ProviderEventID: a.ProviderEventID})
	got, _ := b.List("r1", "", 10)
	if len(got) != 1 {
		t.Fatalf("provider retry was published twice: %d", len(got))
	}
}

func TestBusPublishRollsBackWhenStateCannotBeReplaced(t *testing.T) {
	b := NewBus()
	b.statePath = t.TempDir()
	e := New("durability.v1", "run", "r1", "tenant", "workspace", 1, nil)
	if err := b.Publish(context.Background(), e); err == nil {
		t.Fatal("expected persistence failure")
	}
	items, _ := b.List("r1", "", 10)
	if len(items) != 0 {
		t.Fatalf("failed event remained visible: %+v", items)
	}
}

func TestBusDoesNotExposeMutablePayloadThroughHistoryOrSubscribers(t *testing.T) {
	b := NewBus()
	updates, cancel := b.Subscribe(1)
	defer cancel()
	e := New("payload.v1", "run", "r1", "tenant", "workspace", 1, map[string]any{
		"nested": map[string]any{"state": "original"},
	})
	if err := b.Publish(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	e.Payload["nested"].(map[string]any)["state"] = "caller-mutated"
	delivered := <-updates
	delivered.Payload["nested"].(map[string]any)["state"] = "subscriber-mutated"
	history, _ := b.List("r1", "", 10)
	if got := history[0].Payload["nested"].(map[string]any)["state"]; got != "original" {
		t.Fatalf("history payload was mutable through caller/subscriber: %v", got)
	}
}
