package events

import (
	"context"
	"errors"
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
