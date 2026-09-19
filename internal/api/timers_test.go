package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adro-project/adro/core/testkit"
	"github.com/adro-project/adro/internal/runtime"
)

func TestTimerRoutesExposeScopedExplainAndCancellation(t *testing.T) {
	s := testServer(t)
	clock := testkit.NewManualClock(time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC))
	store, err := runtime.NewTimerStore(filepath.Join(t.TempDir(), "timers.json"), runtime.TimerStoreOptions{Clock: clock, IDs: &testkit.SequenceIDs{}})
	if err != nil {
		t.Fatal(err)
	}
	s.Timers = store
	visible, _, err := store.Schedule(runtime.TimerSpec{
		ID: "timer-visible", ScheduleKey: "approval-visible",
		Scope: runtime.Scope{TenantID: "tenant-a", WorkspaceID: "workspace-a", SessionID: "session-a", RunID: "run-a"},
		DueAt: clock.Now().Add(time.Hour), Command: runtime.TimerCommand{Name: "approval.deadline", IdempotencyKey: "approval-visible"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Schedule(runtime.TimerSpec{
		ID: "timer-hidden", ScheduleKey: "approval-hidden",
		Scope: runtime.Scope{TenantID: "tenant-b", WorkspaceID: "workspace-b", SessionID: "session-b", RunID: "run-b"},
		DueAt: clock.Now().Add(time.Hour), Command: runtime.TimerCommand{Name: "approval.deadline", IdempotencyKey: "approval-hidden"},
	}); err != nil {
		t.Fatal(err)
	}

	headers := map[string]string{"X-Tenant-ID": "tenant-a", "X-Workspace-ID": "workspace-a"}
	list := request(t, s.Routes(), http.MethodGet, "/api/v1/timers?include_terminal=true", "", headers)
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), "timer-hidden") || !strings.Contains(list.Body.String(), visible.ID) {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	foreign := request(t, s.Routes(), http.MethodGet, "/api/v1/timers/timer-hidden", "", headers)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign timer status=%d body=%s", foreign.Code, foreign.Body.String())
	}
	explanation := request(t, s.Routes(), http.MethodGet, "/api/v1/timers/"+visible.ID+"/explain", "", headers)
	if explanation.Code != http.StatusOK || !strings.Contains(explanation.Body.String(), "waiting_for_due_time") {
		t.Fatalf("explanation status=%d body=%s", explanation.Code, explanation.Body.String())
	}
	cancel := request(t, s.Routes(), http.MethodPost, "/api/v1/timers/"+visible.ID+"/cancel", `{"reason":"operator_review"}`, headers)
	if cancel.Code != http.StatusOK || !strings.Contains(cancel.Body.String(), `"state":"cancelled"`) {
		t.Fatalf("cancel status=%d body=%s", cancel.Code, cancel.Body.String())
	}
	cancelAgain := request(t, s.Routes(), http.MethodPost, "/api/v1/timers/"+visible.ID+"/cancel", `{"reason":"operator_review"}`, headers)
	if cancelAgain.Code != http.StatusOK {
		t.Fatalf("idempotent terminal cancellation status=%d body=%s", cancelAgain.Code, cancelAgain.Body.String())
	}
	var cancelled runtime.Timer
	if err := json.Unmarshal(cancel.Body.Bytes(), &cancelled); err != nil || cancelled.TerminalReason != "operator_review" {
		t.Fatalf("cancelled=%+v err=%v", cancelled, err)
	}
}

func TestTimerRoutesRejectMutationWithoutManagePermission(t *testing.T) {
	t.Setenv("ADRO_AUTH_MODE", "required")
	s := testServer(t)
	clock := testkit.NewManualClock(time.Now().UTC())
	store, err := runtime.NewTimerStore(filepath.Join(t.TempDir(), "timers.json"), runtime.TimerStoreOptions{Clock: clock, IDs: &testkit.SequenceIDs{}})
	if err != nil {
		t.Fatal(err)
	}
	s.Timers = store
	timer, _, err := store.Schedule(runtime.TimerSpec{ID: "timer-protected", ScheduleKey: "protected", Scope: runtime.Scope{TenantID: "tenant-a", WorkspaceID: "workspace-a", SessionID: "session-a", RunID: "run-a"}, DueAt: clock.Now().Add(time.Hour), Command: runtime.TimerCommand{Name: "resume", IdempotencyKey: "protected"}})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, s.Routes(), http.MethodPost, "/api/v1/timers/"+timer.ID+"/cancel", `{}`, map[string]string{"X-Tenant-ID": "tenant-a", "X-Workspace-ID": "workspace-a"})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated cancellation status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := store.Get(timer.ID); err != nil && !errors.Is(err, runtime.ErrTimerNotFound) {
		t.Fatal(err)
	}
}
