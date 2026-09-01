package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestSession(t *testing.T, path string) *Store {
	t.Helper()
	store, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSession(Session{ID: "session-1", TenantID: "tenant-1", WorkspaceID: "workspace-1", BudgetTokens: 10_000}); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestTranscriptCheckpointAndRecoverySurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	first := newTestSession(t, path)
	turn, err := first.AppendTurn("session-1", Turn{Role: RoleUser, Content: "implement the repair", IdempotencyKey: "turn:1"})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := first.AppendTurn("session-1", Turn{Role: RoleUser, Content: "implement the repair", IdempotencyKey: "turn:1"})
	if err != nil || duplicate.ID != turn.ID {
		t.Fatalf("idempotent append=%+v err=%v", duplicate, err)
	}
	if _, err := first.SaveCheckpoint("session-1", Checkpoint{TurnSequence: 1, Phase: CheckpointTurnStarted, EventHash: turn.Hash, ContextVersion: 1, State: "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.EnqueueOutbox("session-1", "effect:1", map[string]any{"type": "provider.dispatch"}); err != nil {
		t.Fatal(err)
	}
	claimed, err := first.ClaimOutbox("session-1", "worker-1", 10, time.Minute, time.Now())
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	second, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := second.Recover("session-1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if recovery.LatestCheckpoint == nil || recovery.LatestCheckpoint.EventHash != turn.Hash || len(recovery.PendingEffects) != 1 {
		t.Fatalf("recovery=%+v", recovery)
	}
	if _, _, err := second.ListTurns("session-1", 0, 10); err != nil {
		t.Fatal(err)
	}
}

func TestTranscriptTamperFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	first := newTestSession(t, path)
	if _, err := first.AppendTurn("session-1", Turn{Role: RoleAssistant, Content: "done"}); err != nil {
		t.Fatal(err)
	}
	data, err := osReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "done", "tampered", 1))
	if err := osWriteFile(path, data); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("tampered transcript err=%v", err)
	}
}

func TestCompactionRequiresExactNonOverlappingWindowAndKeepsArchive(t *testing.T) {
	store := newTestSession(t, "")
	for _, content := range []string{"a", "b", "c", "d"} {
		if _, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	archive, err := store.Compact("session-1", CompactRequest{StartSequence: 1, EndSequence: 2, Summary: "a and b are complete", RetainedTail: 1, Reason: "budget"})
	if err != nil {
		t.Fatal(err)
	}
	if archive.SourceHash == "" || archive.ReplacementHash == "" || archive.ParentArchiveID != "" {
		t.Fatalf("archive=%+v", archive)
	}
	if _, err := store.Compact("session-1", CompactRequest{StartSequence: 2, EndSequence: 3, Summary: "overlap"}); !errors.Is(err, ErrWindowUsed) {
		t.Fatalf("overlap err=%v", err)
	}
	status, err := store.ContextStatus("session-1")
	if err != nil || status.ArchivedTurns != 2 || status.ContextVersion != 2 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	compiled, err := store.Compile("session-1", 100)
	if err != nil || !strings.Contains(compiled, "a and b are complete") || strings.Contains(compiled, "user: a") {
		t.Fatalf("compiled=%q err=%v", compiled, err)
	}
}

func TestAutomaticCompactionUsesBudgetGuardAndKeepsTail(t *testing.T) {
	store, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSession(Session{ID: "auto-session", TenantID: "tenant", WorkspaceID: "workspace", BudgetTokens: 10, CompactionRetainTail: 1}); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{strings.Repeat("first long turn ", 8), strings.Repeat("second long turn ", 8), strings.Repeat("third long turn ", 8)} {
		if _, err := store.AppendTurn("auto-session", Turn{Role: RoleUser, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	archives, err := store.ListArchives("auto-session")
	if err != nil || len(archives) == 0 {
		t.Fatalf("automatic archives=%+v err=%v", archives, err)
	}
	if archives[0].Reason != "automatic budget guard" || archives[0].StartSequence != 1 {
		t.Fatalf("automatic archive=%+v", archives[0])
	}
	compiled, err := store.Compile("auto-session", 100)
	if err != nil || !strings.Contains(compiled, "Auto-archived transcript") || !strings.Contains(compiled, "third long turn") {
		t.Fatalf("compiled=%q err=%v", compiled, err)
	}
}

func TestAutomaticCompactionCanBeDisabled(t *testing.T) {
	store, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSession(Session{ID: "manual-session", TenantID: "tenant", WorkspaceID: "workspace", BudgetTokens: 10, AutoCompactionSet: true, AutoCompaction: false}); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{strings.Repeat("first long turn ", 8), strings.Repeat("second long turn ", 8)} {
		if _, err := store.AppendTurn("manual-session", Turn{Role: RoleUser, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	archives, err := store.ListArchives("manual-session")
	if err != nil || len(archives) != 0 {
		t.Fatalf("manual archives=%+v err=%v", archives, err)
	}
}

func TestCompileIncludesMemoryAndHonorsBudget(t *testing.T) {
	store := newTestSession(t, "")
	turn, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "the original requirement and constraints"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddMemory(MemoryItem{SessionID: "session-1", Kind: "decision", Content: "retain the original checkout", SourceIDs: []string{turn.ID}, Confidence: 0.9}); err != nil {
		t.Fatal(err)
	}
	compiled, err := store.Compile("session-1", 5)
	if err != nil {
		t.Fatal(err)
	}
	if estimateTokens(compiled) > 5 {
		t.Fatalf("compiled context exceeded budget: tokens=%d context=%q", estimateTokens(compiled), compiled)
	}
	if !strings.Contains(compiled, "memory") {
		t.Fatalf("compiled context omitted durable memory: %q", compiled)
	}
}

func TestCompileUsesActiveMemoryFrontier(t *testing.T) {
	store := newTestSession(t, "")
	turn, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "decide the checkout policy"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.AddMemory(MemoryItem{SessionID: "session-1", Kind: "decision", Content: "use the temporary checkout", SourceIDs: []string{turn.ID}, Confidence: 0.8})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddMemory(MemoryItem{SessionID: "session-1", Kind: "decision", Content: "keep the original checkout", SourceIDs: []string{turn.ID}, Supersedes: []string{old.ID}, Confidence: 0.95}); err != nil {
		t.Fatal(err)
	}
	compiled, err := store.Compile("session-1", 200)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(compiled, "temporary checkout") || !strings.Contains(compiled, "original checkout") {
		t.Fatalf("compiled stale memory frontier: %q", compiled)
	}
}

func TestProjectMemoryCrossesSessionsWithoutSemanticSearch(t *testing.T) {
	store, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSession(Session{ID: "project-session-1", TenantID: "tenant-1", WorkspaceID: "workspace-1", ProjectID: "project-1"}); err != nil {
		t.Fatal(err)
	}
	turn, err := store.AppendTurn("project-session-1", Turn{Role: RoleUser, Content: "the service must preserve idempotency"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddMemory(MemoryItem{SessionID: "project-session-1", Scope: "project", Kind: "constraint", Content: "all mutations are idempotent", SourceIDs: []string{turn.ID}, Confidence: 1, Pinned: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSession(Session{ID: "project-session-2", TenantID: "tenant-1", WorkspaceID: "workspace-1", ProjectID: "project-1"}); err != nil {
		t.Fatal(err)
	}
	memories, err := store.ListMemories("project-session-2")
	if err != nil || len(memories) != 1 || memories[0].Scope != "project" {
		t.Fatalf("cross-session memories=%+v err=%v", memories, err)
	}
	compiled, err := store.Compile("project-session-2", 100)
	if err != nil || !strings.Contains(compiled, "all mutations are idempotent") {
		t.Fatalf("compiled project memory=%q err=%v", compiled, err)
	}
}

func TestExpiredWorkingMemoryIsExcluded(t *testing.T) {
	store, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSession(Session{ID: "working-session", TenantID: "tenant-1", WorkspaceID: "workspace-1"}); err != nil {
		t.Fatal(err)
	}
	turn, err := store.AppendTurn("working-session", Turn{Role: RoleUser, Content: "temporary context"})
	if err != nil {
		t.Fatal(err)
	}
	expired := time.Now().UTC().Add(-time.Minute)
	if _, err := store.AddMemory(MemoryItem{SessionID: "working-session", Scope: "working", Kind: "scratch", Content: "expired scratch", SourceIDs: []string{turn.ID}, Confidence: 1, ExpiresAt: &expired}); err != nil {
		t.Fatal(err)
	}
	memories, err := store.ListMemories("working-session")
	if err != nil || len(memories) != 0 {
		t.Fatalf("expired memory remained visible: %+v err=%v", memories, err)
	}
}

func TestMemoryCannotSupersedeItself(t *testing.T) {
	store := newTestSession(t, "")
	turn, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "source"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AddMemory(MemoryItem{SessionID: "session-1", ID: "memory-1", Kind: "decision", Content: "invalid", SourceIDs: []string{turn.ID}, Supersedes: []string{"memory-1"}, Confidence: 1})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("self-supersede err=%v", err)
	}
}

func TestCheckpointContextVersionCannotRewind(t *testing.T) {
	store := newTestSession(t, "")
	turn, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "checkpoint"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveCheckpoint("session-1", Checkpoint{TurnSequence: 1, EventHash: turn.Hash, Phase: CheckpointTurnStarted, ContextVersion: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveCheckpoint("session-1", Checkpoint{TurnSequence: 1, EventHash: turn.Hash, Phase: CheckpointToolAfter, ContextVersion: 2}); !errors.Is(err, ErrConflict) {
		t.Fatalf("context checkpoint rewind accepted: %v", err)
	}
}

func TestCheckpointReplayReturnsExistingRecord(t *testing.T) {
	store := newTestSession(t, "")
	turn, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	input := Checkpoint{TurnSequence: 1, EventHash: turn.Hash, Phase: CheckpointEffectAfter, ContextVersion: 1, OutboxIDs: []string{"outbox-1"}, State: "provider run recorded"}
	first, err := store.SaveCheckpoint("session-1", input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.SaveCheckpoint("session-1", input)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || second.ID != first.ID {
		t.Fatalf("checkpoint replay created a duplicate: first=%+v second=%+v", first, second)
	}
	checkpoints, err := store.ListCheckpoints("session-1")
	if err != nil || len(checkpoints) != 1 {
		t.Fatalf("checkpoint count=%d err=%v", len(checkpoints), err)
	}
}

func TestLeaseAndOutboxRecoveryAreIdempotent(t *testing.T) {
	store := newTestSession(t, "")
	now := time.Now().UTC()
	lease, err := store.AcquireLease("session-1", "repo/main", "worker-1", time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireLease("session-1", "repo/main", "worker-2", time.Minute, now); !errors.Is(err, ErrLeaseBusy) {
		t.Fatalf("busy err=%v", err)
	}
	if err := store.ReleaseLease("session-1", lease.ID, "worker-1", now); err != nil {
		t.Fatal(err)
	}
	event, err := store.EnqueueOutbox("session-1", "notify:1", map[string]string{"state": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimOutbox("session-1", "worker-1", 1, time.Minute, time.Time{})
	if err != nil || len(claimed) != 1 || claimed[0].ID != event.ID {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	if err := store.AckOutbox("session-1", event.ID, "worker-2", now); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("foreign ack err=%v", err)
	}
	if err := store.AckOutbox("session-1", event.ID, "worker-1", now); err != nil {
		t.Fatal(err)
	}
	if err := store.AckOutbox("session-1", event.ID, "worker-1", now); err != nil {
		t.Fatal(err)
	}
}

type recordingPublisher struct {
	count int
	fail  bool
}

func (p *recordingPublisher) Publish(_ context.Context, _ OutboxEvent) error {
	p.count++
	if p.fail {
		return errors.New("transport unavailable")
	}
	return nil
}

func TestOutboxDispatcherNacksTransportFailureAndAcksSuccess(t *testing.T) {
	store := newTestSession(t, "")
	if _, err := store.EnqueueOutbox("session-1", "effect:dispatch", map[string]string{"type": "pipeline"}); err != nil {
		t.Fatal(err)
	}
	publisher := &recordingPublisher{fail: true}
	dispatcher := Dispatcher{Store: store, Publisher: publisher, Owner: "worker-1", LeaseTTL: time.Minute}
	if count, err := dispatcher.DispatchOnce(context.Background(), "session-1", 1); err != nil || count != 0 {
		t.Fatalf("failed dispatch count=%d err=%v", count, err)
	}
	time.Sleep(1100 * time.Millisecond)
	publisher.fail = false
	if count, err := dispatcher.DispatchOnce(context.Background(), "session-1", 1); err != nil || count != 1 {
		t.Fatalf("successful dispatch count=%d err=%v", count, err)
	}
}

func TestEnqueueAndClaimOutboxClosesRecoveryRaceAndPreservesIdempotency(t *testing.T) {
	store := newTestSession(t, "")
	now := time.Now().UTC()
	event, claimed, err := store.EnqueueAndClaimOutbox("session-1", "effect:atomic", map[string]string{"type": "provider"}, "api", time.Minute, now)
	if err != nil || !claimed || event.State != "processing" || event.Owner != "api" || event.Attempts != 1 {
		t.Fatalf("atomic claim=%+v claimed=%v err=%v", event, claimed, err)
	}
	if _, claimed, err := store.EnqueueAndClaimOutbox("session-1", "effect:atomic", map[string]string{"type": "provider"}, "worker", time.Minute, now); !errors.Is(err, ErrLeaseBusy) || claimed {
		t.Fatalf("active claim was stolen: claimed=%v err=%v", claimed, err)
	}
	if err := store.AckOutbox("session-1", event.ID, "api", now); err != nil {
		t.Fatal(err)
	}
	replayed, claimed, err := store.EnqueueAndClaimOutbox("session-1", "effect:atomic", map[string]string{"type": "provider"}, "api", time.Minute, now)
	if err != nil || claimed || replayed.State != "published" || replayed.ID != event.ID {
		t.Fatalf("published replay=%+v claimed=%v err=%v", replayed, claimed, err)
	}
}

// Small wrappers keep the test independent from platform-specific file mode
// defaults while making the tamper operation explicit.
var osReadFile = func(path string) ([]byte, error) { return os.ReadFile(path) }
var osWriteFile = func(path string, data []byte) error { return os.WriteFile(path, data, 0o600) }
