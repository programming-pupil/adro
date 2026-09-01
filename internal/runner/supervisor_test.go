package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSupervisorCapacityAndIsolation(t *testing.T) {
	s := NewSupervisor()
	r, _ := s.Register(Runner{Name: "r1", Provider: "mock", Version: "1", Concurrency: 1, SecurityDomain: "test"})
	_, _ = s.Heartbeat(r.ID, 0)
	if _, err := s.Choose("prod"); err == nil {
		t.Fatal("selected runner from wrong security domain")
	}
	if _, err := s.Choose("test"); err != nil {
		t.Fatal(err)
	}
	_, _ = s.Heartbeat(r.ID, 1)
	if _, err := s.Choose("test"); err == nil {
		t.Fatal("selected a full runner")
	}
}

func TestExecuteConfinesCommandAndCapturesOutput(t *testing.T) {
	root := t.TempDir()
	s := NewSupervisor()
	r, err := s.Register(Runner{Name: "exec", Provider: "local", Version: "1", Concurrency: 1, WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Heartbeat(r.ID, 0); err != nil {
		t.Fatal(err)
	}
	result, err := s.Execute(context.Background(), ExecuteRequest{RunnerID: r.ID, WorkDir: root, Command: []string{"/bin/sh", "-c", "printf ready"}, Env: map[string]string{"ADRO_TEST": "1"}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || result.Stdout != "ready" {
		t.Fatalf("result=%+v", result)
	}
	if _, err := s.Execute(context.Background(), ExecuteRequest{RunnerID: r.ID, WorkDir: filepath.Dir(root), Command: []string{"/bin/echo", "escape"}}); err == nil {
		t.Fatal("accepted work directory outside runner root")
	}
	if _, err := s.Execute(context.Background(), ExecuteRequest{RunnerID: r.ID, WorkDir: root, Command: []string{"/bin/echo"}, Env: map[string]string{"BAD=KEY": "x"}}); err == nil {
		t.Fatal("accepted invalid environment key")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentSupervisorRequiresHeartbeatAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runners.json")
	first, err := NewPersistentSupervisor(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := first.Register(Runner{Name: "restart", Provider: "local", Version: "1", WorkspaceRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Heartbeat(r.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := first.Flush(); err != nil {
		t.Fatal(err)
	}
	second, err := NewPersistentSupervisor(path)
	if err != nil {
		t.Fatal(err)
	}
	items := second.List()
	if len(items) != 1 || items[0].ActiveRuns != 0 || items[0].Status != Offline {
		t.Fatalf("restored runners=%+v", items)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]Runner
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted[r.ID].ActiveRuns != 1 {
		t.Fatalf("test setup did not persist active run: %+v", persisted[r.ID])
	}
}

func TestSupervisorRejectsUnpersistedRegistration(t *testing.T) {
	s := NewSupervisor()
	s.path = t.TempDir()
	if _, err := s.Register(Runner{Name: "durability", Provider: "local", Version: "1"}); err == nil {
		t.Fatal("expected persistence failure")
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("failed runner registration remained visible: %+v", got)
	}
}

func TestReapStaleRunnersRequiresFreshHeartbeat(t *testing.T) {
	s := NewSupervisor()
	r, err := s.Register(Runner{Name: "stale", Provider: "local", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	stale := s.runners[r.ID]
	stale.Status = Healthy
	stale.LastHeartbeat = time.Now().UTC().Add(-time.Minute)
	s.runners[r.ID] = stale
	s.mu.Unlock()
	reaped, err := s.ReapStale(time.Now().UTC(), 10*time.Second)
	if err != nil || len(reaped) != 1 || reaped[0].Status != Offline {
		t.Fatalf("reaped=%+v err=%v", reaped, err)
	}
	if _, err := s.Choose(""); err == nil {
		t.Fatal("stale runner remained schedulable")
	}
	if _, err := s.Heartbeat(r.ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Choose(""); err != nil {
		t.Fatalf("fresh heartbeat did not restore scheduling: %v", err)
	}
}
