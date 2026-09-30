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
	health     Health
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
	if c.health.Status != "" || !c.health.ChangedAt.IsZero() || c.health.Reason != "" || c.health.Ready || c.health.Live {
		return c.health
	}
	return Health{Status: StatusReady, ChangedAt: time.Unix(1, 0).UTC()}
}

type blockingComponent struct {
	fakeComponent
}

func (c *blockingComponent) Start(ctx context.Context) error {
	c.mu.Lock()
	c.root = ctx
	*c.operations = append(*c.operations, "start:"+c.name)
	c.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

type blockingStopComponent struct {
	fakeComponent
}

func (c *blockingStopComponent) Stop(ctx context.Context) error {
	*c.operations = append(*c.operations, "stop:"+c.name)
	<-ctx.Done()
	return ctx.Err()
}

type retryStopComponent struct {
	fakeComponent
	attempts int
}

func (c *retryStopComponent) Stop(context.Context) error {
	c.attempts++
	*c.operations = append(*c.operations, "stop:"+c.name)
	if c.attempts == 1 {
		return errors.New("transient stop failure")
	}
	return nil
}

type ownedWorkerComponent struct {
	fakeComponent
	done chan struct{}
}

func (c *ownedWorkerComponent) Start(ctx context.Context) error {
	if err := c.fakeComponent.Start(ctx); err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		close(c.done)
	}()
	return nil
}

func (c *ownedWorkerComponent) Stop(ctx context.Context) error {
	*c.operations = append(*c.operations, "stop:"+c.name)
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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

func TestManagerSnapshotPreservesNewStatus(t *testing.T) {
	operations := []string{}
	manager, err := NewManager([]Component{
		&fakeComponent{name: "new", operations: &operations, health: Health{Status: StatusNew}},
		&fakeComponent{name: "ready", operations: &operations, health: Health{Status: StatusReady}},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := manager.Snapshot(context.Background())
	if snapshot.Status != StatusNew || snapshot.Ready || snapshot.Live {
		t.Fatalf("new component was aggregated incorrectly: %+v", snapshot)
	}
}

func TestManagerSnapshotSeparatesReadinessAndLiveness(t *testing.T) {
	operations := []string{}
	manager, err := NewManager([]Component{
		&fakeComponent{name: "store", operations: &operations, health: Health{
			Status: StatusDegraded, Reason: "database reconnecting", ChangedAt: time.Unix(3, 0).UTC(), Live: true,
		}},
		&fakeComponent{name: "api", operations: &operations, health: Health{
			Status: StatusReady, ChangedAt: time.Unix(4, 0).UTC(), Ready: true, Live: true,
		}},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := manager.Snapshot(context.Background())
	if snapshot.Status != StatusDegraded || snapshot.Ready || !snapshot.Live {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if snapshot.Reason != "store: database reconnecting" || !snapshot.ChangedAt.Equal(time.Unix(4, 0).UTC()) {
		t.Fatalf("snapshot reason/time=%+v", snapshot)
	}
	if got := manager.Readiness(context.Background()); got.Ready {
		t.Fatalf("degraded component unexpectedly ready: %+v", got)
	}
	if got := manager.Liveness(context.Background()); !got.Live {
		t.Fatalf("live degraded component reported dead: %+v", got)
	}
}

func TestManagerSnapshotFailsClosedOnInvalidOrInconsistentHealth(t *testing.T) {
	operations := []string{}
	manager, err := NewManager([]Component{
		&fakeComponent{name: "invalid", operations: &operations, health: Health{Status: Status("mystery"), Ready: true, Live: true}},
		&fakeComponent{name: "failed", operations: &operations, health: Health{Status: StatusFailed, Ready: true, Live: true}},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := manager.Snapshot(context.Background())
	if snapshot.Status != StatusFailed || snapshot.Ready || snapshot.Live {
		t.Fatalf("invalid health was not fail-closed: %+v", snapshot)
	}
	if snapshot.Reason != "invalid: invalid lifecycle status" {
		t.Fatalf("invalid health reason=%q", snapshot.Reason)
	}
}

func TestManagerStartupTimeoutIsBounded(t *testing.T) {
	operations := []string{}
	manager, err := NewManagerWithTimeouts([]Component{
		&blockingComponent{fakeComponent: fakeComponent{name: "blocked", operations: &operations}},
	}, 5*time.Millisecond, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = manager.Start(context.Background())
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("startup timeout error=%v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("startup timeout was not bounded: %s", time.Since(started))
	}
}

func TestManagerShutdownTimeoutIsBounded(t *testing.T) {
	operations := []string{}
	manager, err := NewManagerWithTimeouts([]Component{
		&blockingStopComponent{fakeComponent: fakeComponent{name: "blocked", operations: &operations}},
	}, time.Second, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = manager.Stop(context.Background())
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown timeout error=%v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("shutdown timeout was not bounded: %s", time.Since(started))
	}
}

func TestManagerRetriesComponentsThatFailedToStop(t *testing.T) {
	operations := []string{}
	component := &retryStopComponent{fakeComponent: fakeComponent{name: "worker", operations: &operations}}
	manager, err := NewManager([]Component{component}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(context.Background()); err == nil {
		t.Fatal("first stop unexpectedly succeeded")
	}
	if err := manager.Start(context.Background()); err == nil {
		t.Fatal("manager restarted while cleanup was pending")
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("retry stop: %v", err)
	}
	if component.attempts != 2 {
		t.Fatalf("stop attempts=%d", component.attempts)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("restart after cleanup: %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("final stop: %v", err)
	}
}

func TestManagerCancellationReleasesOwnedWorker(t *testing.T) {
	operations := []string{}
	worker := &ownedWorkerComponent{
		fakeComponent: fakeComponent{name: "worker", operations: &operations},
		done:          make(chan struct{}),
	}
	manager, err := NewManagerWithTimeouts([]Component{worker}, time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-worker.done:
	default:
		t.Fatal("owned worker survived manager shutdown")
	}
}
