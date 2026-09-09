package provider

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// This is intentionally opt-in: it spends a real Codex turn and requires the
// operator's authenticated local Codex installation. It is the acceptance
// proof for the app-server adapter; the ordinary unit suite remains hermetic.
func TestRealCodexAppServerStartAndResume(t *testing.T) {
	if strings.TrimSpace(os.Getenv("ADRO_REAL_CODEX_TEST")) != "1" {
		t.Skip("set ADRO_REAL_CODEX_TEST=1 to run the authenticated local Codex test")
	}
	executable := strings.TrimSpace(os.Getenv("ADRO_REAL_CODEX_BIN"))
	if executable == "" {
		var err error
		executable, err = exec.LookPath("codex")
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ADRO_EXECUTOR_TIMEOUT", "4m")
	p := NewLocalProvider(executable, nil, t.TempDir(), newTestBus())
	item, err := p.CreateWorkItem(context.Background(), WorkItemSpec{ID: "real-codex-app-server", Title: "real app-server"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := p.StartRun(context.Background(), StartRunCommand{WorkItemID: item.ID, Input: "Run exactly pwd in this checkout, then emit one ADRO_RESULT_JSON={\"outcome\":\"pass\",\"reason_code\":\"real_start\",\"summary\":\"started\",\"evidence_ids\":[\"real-start\"],\"fields\":{}} line."})
	if err != nil {
		t.Fatal(err)
	}
	firstSnapshot := waitRealSnapshot(t, p, first.ID)
	if firstSnapshot.Status != "completed" || firstSnapshot.SessionContinuity != "proven" || firstSnapshot.SessionID == "" {
		t.Fatalf("real start snapshot=%+v", firstSnapshot)
	}
	if !strings.Contains(firstSnapshot.Output, "ADRO_RESULT_JSON") || !strings.Contains(firstSnapshot.Output, "turn/completed") {
		t.Fatalf("real start evidence is incomplete: %s", firstSnapshot.Output)
	}
	t.Logf("real start evidence: %s", firstSnapshot.Output)
	second, err := p.ContinueWorkItem(context.Background(), ContinuationCommand{IssueID: item.ProviderIssueID, AgentID: "real-agent", Input: "Continue the same conversation, run exactly pwd again, then emit one ADRO_RESULT_JSON={\"outcome\":\"pass\",\"reason_code\":\"real_resume\",\"summary\":\"resumed\",\"evidence_ids\":[\"real-resume\"],\"fields\":{}} line.", ExpectedSessionID: firstSnapshot.SessionID, ExpectedWorkDir: firstSnapshot.WorkDir})
	if err != nil {
		t.Fatal(err)
	}
	secondSnapshot := waitRealSnapshot(t, p, second.ID)
	if secondSnapshot.Status != "completed" || secondSnapshot.SessionContinuity != "proven" || secondSnapshot.SessionID != firstSnapshot.SessionID {
		t.Fatalf("real resume snapshot=%+v first=%+v", secondSnapshot, firstSnapshot)
	}
}

func waitRealSnapshot(t *testing.T, p *LocalProvider, id string) RunSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		snapshot, err := p.GetRun(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Status != "running" {
			return snapshot
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("real Codex run did not finish within 5 minutes")
	return RunSnapshot{}
}
