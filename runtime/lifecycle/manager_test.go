package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type fakeComponent struct {
	name       string
	deps       []string
	startErr   error
	stopErr    error
	mu         sync.Mutex
	root       context.Context
	operations *[]string
}

func (c *fakeComponent) Name() string           { return c.name }
func (c *fakeComponent) Dependencies() []string { return append([]string(nil), c.deps...) }
func (c *fakeComponent) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.root = ctx
	*c.operations = append(*c.operations, "start:"+c.name)
	return c.startErr
}
func (c *fakeComponent) Stop(context.Context) error {
	*c.operations = append(*c.operations, "stop:"+c.name)
	return c.stopErr
}
func (c *fakeComponent) Health(context.Context) Health {
	return Health{Status: StatusReady, ChangedAt: time.Unix(1, 0).UTC()}
}

func TestManagerStartsTopologicallyAndStopsInReverse(t *testing.T) {
	operations := []string{}
	manager, err := NewManager([]Component{
		&fakeComponent{name: "worker", deps: []string{"store"}, operations: &operations},
		&fakeComponent{name: "api", deps: []string{"store"}, operations: &operations},
		&fakeComponent{name: "store", operations: &operations},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"store", "api", "worker"}; !reflect.DeepEqual(manager.Order(), want) {
		t.Fatalf("order=%v want=%v", manager.Order(), want)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("repeated stop: %v", err)
	}
	want := []string{"start:store", "start:api", "start:worker", "stop:worker", "stop:api", "stop:store"}
	if !reflect.DeepEqual(operations, want) {
		t.Fatalf("operations=%v want=%v", operations, want)
	}
}

func TestManagerRollsBackOnlyStartedComponentsAndCancelsRoot(t *testing.T) {
	operations := []string{}
	store := &fakeComponent{name: "store", operations: &operations}
	worker := &fakeComponent{name: "worker", deps: []string{"store"}, startErr: errors.New("boom"), operations: &operations}
	manager, err := NewManager([]Component{worker, store}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err == nil {
		t.Fatal("expected startup failure")
	}
	want := []string{"start:store", "start:worker", "stop:store"}
	if !reflect.DeepEqual(operations, want) {
		t.Fatalf("operations=%v want=%v", operations, want)
	}
	select {
	case <-store.root.Done():
	default:
		t.Fatal("root context was not cancelled")
	}
}

func TestManagerRejectsInvalidGraphs(t *testing.T) {
	operations := []string{}
	tests := [][]Component{
		{&fakeComponent{name: "a", deps: []string{"missing"}, operations: &operations}},
		{&fakeComponent{name: "a", deps: []string{"b"}, operations: &operations}, &fakeComponent{name: "b", deps: []string{"a"}, operations: &operations}},
		{&fakeComponent{name: "a", operations: &operations}, &fakeComponent{name: "a", operations: &operations}},
	}
	for _, components := range tests {
		if _, err := NewManager(components, time.Second); err == nil {
			t.Fatalf("accepted invalid graph: %+v", components)
		}
	}
}

func TestManagerAggregatesStopErrors(t *testing.T) {
	operations := []string{}
	manager, err := NewManager([]Component{
		&fakeComponent{name: "a", stopErr: errors.New("a failed"), operations: &operations},
		&fakeComponent{name: "b", stopErr: errors.New("b failed"), operations: &operations},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(context.Background()); err == nil {
		t.Fatal("expected aggregated stop error")
	}
}
