package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/adro-project/adro/core/identity"
	"github.com/adro-project/adro/core/testkit"
)

func TestProjectTimerSchedulesFromAuthoritativeHumanEvent(t *testing.T) {
	root := t.TempDir()
	fixture := newLifecycleFixture(t, filepath.Join(root, "runtime.json"))
	startSessionTurn(t, fixture, "turn-1")
	now := time.Now().UTC().Truncate(time.Microsecond)
	actor := humanActor(fixture.scope, "reviewer-1", now)
	request := interactionRequest(now, "turn-1", "human-project", identity.ActorRef{Type: actor.Type, ID: actor.ID})
	if _, err := fixture.journal.RequestHumanInteraction(fixture.scope, request, lifecycleOwner, fixture.fence); err != nil {
		t.Fatal(err)
	}
	clock := testkit.NewManualClock(now)
	timers, err := NewTimerStore(filepath.Join(root, "timers.json"), TimerStoreOptions{Clock: clock, IDs: &testkit.SequenceIDs{}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := fixture.journal.ProjectTimerSchedules(context.Background(), timers)
	if err != nil || first.Scanned != 1 || first.Created != 1 || first.Replayed != 0 {
		t.Fatalf("first projection=%+v err=%v", first, err)
	}
	second, err := fixture.journal.ProjectTimerSchedules(context.Background(), timers)
	if err != nil || second.Scanned != 1 || second.Created != 0 || second.Replayed != 1 {
		t.Fatalf("second projection=%+v err=%v", second, err)
	}
	items, err := timers.List(fixture.scope, false)
	if err != nil || len(items) != 1 || items[0].Command.Name != HumanDeadlineCommandName {
		t.Fatalf("timers=%+v err=%v", items, err)
	}
}
