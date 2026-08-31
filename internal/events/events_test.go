package events

import (
	"context"
	"testing"
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
