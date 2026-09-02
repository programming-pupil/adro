package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adro-project/adro/internal/durable"
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

func TestCompileManifestIsTypedBoundedAndStable(t *testing.T) {
	store := newTestSession(t, filepath.Join(t.TempDir(), "harness.json"))
	if _, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "first requirement"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendTurn("session-1", Turn{Role: RoleAssistant, Content: "implementation plan"}); err != nil {
		t.Fatal(err)
	}
	manifest, err := store.CompileManifest("session-1", 12)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Digest == "" || len(manifest.Blocks) == 0 || manifest.TokenEstimate > manifest.TokenBudget {
		t.Fatalf("invalid manifest=%+v", manifest)
	}
	for _, block := range manifest.Blocks {
		if block.Hash == "" || block.Source == "" || block.Policy == "" || block.Trust == "" || block.TokenEstimate <= 0 {
			t.Fatalf("missing block lineage=%+v", block)
		}
	}
	second, err := store.CompileManifest("session-1", 12)
	if err != nil || second.Digest != manifest.Digest {
		t.Fatalf("manifest is not stable: first=%s second=%s err=%v", manifest.Digest, second.Digest, err)
	}
}

func TestCompileEnvelopeCarriesReplaySelection(t *testing.T) {
	store := newTestSession(t, filepath.Join(t.TempDir(), "harness.json"))
	if _, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "preserve exact context"}); err != nil {
		t.Fatal(err)
	}
	envelope, err := store.CompileEnvelope("session-1", 64)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.SelectionDigest == "" || envelope.ReplayKey == "" || envelope.Manifest.Digest == "" {
		t.Fatalf("incomplete context envelope: %+v", envelope)
	}
	if envelope.Manifest.TokenEstimate > envelope.Manifest.TokenBudget {
		t.Fatalf("envelope exceeded hard budget: %+v", envelope.Manifest)
	}
	second, err := store.CompileEnvelope("session-1", 64)
	if err != nil || second.ReplayKey != envelope.ReplayKey || second.SelectionDigest != envelope.SelectionDigest {
		t.Fatalf("context replay key is unstable first=%+v second=%+v err=%v", envelope, second, err)
	}
}

func TestPersistentStoreRejectsStaleWriterAndFaultsBeforeRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	first := newTestSession(t, path)
	second, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.AppendTurn("session-1", Turn{Role: RoleUser, Content: "winner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := second.AppendTurn("session-1", Turn{Role: RoleUser, Content: "stale"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale writer err=%v", err)
	}
	restore := durable.SetFaultInjector(func(point string) error {
		if point == "harness.snapshot.before_rename" {
			return fmt.Errorf("injected crash")
		}
		return nil
	})
	defer restore()
	if _, err := first.AppendTurn("session-1", Turn{Role: RoleUser, Content: "crash window"}); err == nil {
		t.Fatal("expected injected persistence failure")
	}
	recovered, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	turns, _, err := recovered.ListTurns("session-1", 0, 10)
	if err != nil || len(turns) != 2 || turns[0].Content != "winner" || turns[1].Content != "crash window" {
		t.Fatalf("fault altered durable state turns=%+v err=%v", turns, err)
	}
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

func TestAppendOnlyTranscriptRebuildsMissingSnapshotTurns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	store := newTestSession(t, path)
	turn, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "long lived transcript"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	session := state.Sessions["session-1"]
	session.Turns = nil
	state.Sessions["session-1"] = session
	data, err = json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	turns, _, err := recovered.ListTurns("session-1", 0, 10)
	if err != nil || len(turns) != 1 || turns[0].Hash != turn.Hash {
		t.Fatalf("replayed turns=%+v err=%v", turns, err)
	}
	integrity, err := recovered.VerifyTranscript("session-1")
	if err != nil || !integrity.Valid {
		t.Fatalf("integrity=%+v err=%v", integrity, err)
	}
}

func TestAppendOnlyTranscriptTamperFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	store := newTestSession(t, path)
	if _, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendTurn("session-1", Turn{Role: RoleAssistant, Content: "second"}); err != nil {
		t.Fatal(err)
	}
	transcript := transcriptPath(path)
	data, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "second", "tampered", 1))
	if err := os.WriteFile(transcript, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("tampered append-only transcript err=%v", err)
	}
}

func TestJournalRecoversWhenSnapshotWasTorn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	first := newTestSession(t, path)
	turn, err := first.AppendTurn("session-1", Turn{Role: RoleUser, Content: "journal recovery"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	journal, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journalPath(path), append(journal, '\n', '{'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"torn":`), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	turns, _, err := recovered.ListTurns("session-1", 0, 10)
	if err != nil || len(turns) != 1 || turns[0].Hash != turn.Hash {
		t.Fatalf("journal recovery turns=%+v err=%v", turns, err)
	}
}

func TestJournalMiddleCorruptionFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	store := newTestSession(t, path)
	if _, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "journal source"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	journal := append([]byte(`{"broken":`+"\n"), snapshot...)
	journal = append(journal, '\n')
	if err := os.WriteFile(journalPath(path), journal, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("middle journal corruption err=%v", err)
	}
}

func TestTranscriptTornTailWithTrailingWhitespaceIsRecoverable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	store := newTestSession(t, path)
	turn, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "transcript tail"})
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := os.ReadFile(transcriptPath(path))
	if err != nil {
		t.Fatal(err)
	}
	transcript = append(transcript, []byte(`{"torn":`+"\n   \t")...)
	if err := os.WriteFile(transcriptPath(path), transcript, 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	turns, _, err := recovered.ListTurns("session-1", 0, 10)
	if err != nil || len(turns) != 1 || turns[0].Hash != turn.Hash {
		t.Fatalf("recovered transcript turns=%+v err=%v", turns, err)
	}
}

func TestTranscriptMiddleCorruptionFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	store := newTestSession(t, path)
	if _, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendTurn("session-1", Turn{Role: RoleAssistant, Content: "second"}); err != nil {
		t.Fatal(err)
	}
	transcript, err := os.ReadFile(transcriptPath(path))
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(string(transcript))
	corrupt := []byte(lines[0] + "\n{" + "broken" + "\n" + lines[1] + "\n")
	if err := os.WriteFile(transcriptPath(path), corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("middle transcript corruption err=%v", err)
	}
}

func TestCheckpointHashChainFailsClosedOnSplice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	store := newTestSession(t, path)
	turn, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "checkpoint chain"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.SaveCheckpoint("session-1", Checkpoint{TurnSequence: 1, Phase: CheckpointTurnStarted, EventHash: turn.Hash, ContextVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.SaveCheckpoint("session-1", Checkpoint{TurnSequence: 1, Phase: CheckpointEffectAfter, EventHash: turn.Hash, ContextVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if second.PrevHash != first.Hash {
		t.Fatalf("checkpoint chain not linked: first=%+v second=%+v", first, second)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	sessionState := state.Sessions["session-1"]
	checkpoints := sessionState.Checkpoints
	checkpoints[1].PrevHash = "sha256:spliced"
	checkpoints[1].Hash = hashCheckpoint(checkpoints[1])
	sessionState.Checkpoints = checkpoints
	state.Sessions["session-1"] = sessionState
	tampered, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("spliced checkpoint err=%v", err)
	}
}

func TestToolCheckpointsRequireBeforeAndAfterPair(t *testing.T) {
	store := newTestSession(t, "")
	turn, err := store.AppendTurn("session-1", Turn{Role: RoleTool, Content: "tool result"})
	if err != nil {
		t.Fatal(err)
	}
	base := Checkpoint{TurnSequence: 1, EventHash: turn.Hash, ContextVersion: 1, ToolCallID: "tool-1"}
	if _, err := store.SaveCheckpoint("session-1", Checkpoint{TurnSequence: 1, Phase: CheckpointToolAfter, EventHash: turn.Hash, ContextVersion: 1, ToolCallID: base.ToolCallID}); !errors.Is(err, ErrConflict) {
		t.Fatalf("tool-after without before err=%v", err)
	}
	base.Phase = CheckpointToolBefore
	before, err := store.SaveCheckpoint("session-1", base)
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.SaveCheckpoint("session-1", Checkpoint{TurnSequence: 1, Phase: CheckpointToolAfter, EventHash: turn.Hash, ContextVersion: 1, ToolCallID: base.ToolCallID})
	if err != nil || after.PrevHash != before.Hash {
		t.Fatalf("tool checkpoint pair before=%+v after=%+v err=%v", before, after, err)
	}
	if _, err := store.SaveCheckpoint("session-1", Checkpoint{TurnSequence: 1, Phase: CheckpointToolBefore, EventHash: turn.Hash, ContextVersion: 1, ToolCallID: base.ToolCallID, State: "retry"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused tool-call id err=%v", err)
	}
}

func TestContextStatusExcludesArchivedTurnTokens(t *testing.T) {
	store := newTestSession(t, "")
	if _, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: strings.Repeat("archived ", 20)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Compact("session-1", CompactRequest{StartSequence: 1, EndSequence: 1, Summary: "archived summary"}); err != nil {
		t.Fatal(err)
	}
	status, err := store.ContextStatus("session-1")
	if err != nil || status.TokenEstimate != 0 {
		t.Fatalf("archived tokens remained in status=%+v err=%v", status, err)
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

func TestCompactionFailsClosedWhenSummaryDoesNotReduce(t *testing.T) {
	store := newTestSession(t, "")
	content := strings.Repeat("durable context ", 30)
	if _, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: content}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Compact("session-1", CompactRequest{StartSequence: 1, EndSequence: 1, Summary: content}); err == nil {
		t.Fatal("non-reducing compaction unexpectedly succeeded")
	}
	archives, err := store.ListArchives("session-1")
	if err != nil || len(archives) != 0 {
		t.Fatalf("failed compaction left archive state=%+v err=%v", archives, err)
	}
	checkpoints, err := store.ListCheckpoints("session-1")
	if err != nil || len(checkpoints) != 0 {
		t.Fatalf("failed compaction left checkpoint state=%+v err=%v", checkpoints, err)
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
	checkpoints, err := store.ListCheckpoints("auto-session")
	if err != nil || len(checkpoints) < 2 || checkpoints[0].Phase != CheckpointCompactionBegin || checkpoints[1].Phase != CheckpointCompactionDone {
		t.Fatalf("automatic compaction checkpoints=%+v err=%v", checkpoints, err)
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

func TestMemoryFingerprintReplayIsIdempotent(t *testing.T) {
	store := newTestSession(t, "")
	turn, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "source"})
	if err != nil {
		t.Fatal(err)
	}
	item := MemoryItem{SessionID: "session-1", Kind: "fact", Content: "the API is durable", SourceIDs: []string{turn.ID}, Confidence: 1}
	first, err := store.AddMemory(item)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AddMemory(item)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.Fingerprint == "" || second.Fingerprint != first.Fingerprint {
		t.Fatalf("memory replay was not idempotent: first=%+v second=%+v", first, second)
	}
	memories, err := store.ListMemories("session-1")
	if err != nil || len(memories) != 1 {
		t.Fatalf("memory replay created duplicate records: memories=%+v err=%v", memories, err)
	}
}

func TestMemoryReducerExtractsAndSupersedesClaims(t *testing.T) {
	store := newTestSession(t, "")
	first, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "constraint: all writes are idempotent"})
	if err != nil {
		t.Fatal(err)
	}
	reduction, err := store.ReduceMemories("session-1", []string{first.ID}, first.Content)
	if err != nil || len(reduction.Added) != 1 || reduction.Added[0].Kind != "constraint" {
		t.Fatalf("first reduction=%+v err=%v", reduction, err)
	}
	second, err := store.AppendTurn("session-1", Turn{Role: RoleAssistant, Content: "constraint: all writes are serialized"})
	if err != nil {
		t.Fatal(err)
	}
	reduction, err = store.ReduceMemories("session-1", []string{second.ID}, second.Content)
	if err != nil || len(reduction.Added) != 1 || len(reduction.Superseded) != 1 || len(reduction.Conflicts) != 1 {
		t.Fatalf("conflict reduction=%+v err=%v", reduction, err)
	}
	memories, err := store.ListMemories("session-1")
	if err != nil || len(memories) != 1 || memories[0].Content != "all writes are serialized" {
		t.Fatalf("active memory frontier=%+v err=%v", memories, err)
	}
}

func TestMemoryLifecycleTransitionsFailClosed(t *testing.T) {
	store := newTestSession(t, filepath.Join(t.TempDir(), "harness.json"))
	turn, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "candidate evidence"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.AddMemory(MemoryItem{SessionID: "session-1", Kind: "fact", Content: "candidate fact", SourceIDs: []string{turn.ID}, Confidence: 0.7, Status: "candidate"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionMemory("session-1", item.ID, "confirmed"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionMemory("session-1", item.ID, "candidate"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected monotonic lifecycle rejection, got %v", err)
	}
	if _, err := store.TransitionMemory("session-1", item.ID, "forgotten"); err != nil {
		t.Fatal(err)
	}
	memories, err := store.ListMemories("session-1")
	if err != nil || len(memories) != 0 {
		t.Fatalf("forgotten memory remained active: %+v err=%v", memories, err)
	}
}

func TestCompactionRecallProbe(t *testing.T) {
	store := newTestSession(t, "")
	for _, content := range []string{"one long turn", "two long turn", "tail"} {
		if _, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Compact("session-1", CompactRequest{StartSequence: 1, EndSequence: 2, Summary: "one and two are archived", RetainedTail: 1}); err != nil {
		t.Fatal(err)
	}
	probe, err := store.VerifyCompaction("session-1")
	if err != nil || !probe.Valid || !probe.RecallVerified {
		t.Fatalf("compaction probe=%+v err=%v", probe, err)
	}
}

func TestRecordToolCallPersistsPairedCheckpoints(t *testing.T) {
	store := newTestSession(t, "")
	checkpoints, err := store.RecordToolCall("session-1", "tool-42", "shell", "go test ./...", "ok", 1)
	if err != nil || len(checkpoints) != 2 || checkpoints[0].Phase != CheckpointToolBefore || checkpoints[1].Phase != CheckpointToolAfter {
		t.Fatalf("checkpoints=%+v err=%v", checkpoints, err)
	}
	turns, _, err := store.ListTurns("session-1", 0, 10)
	if err != nil || len(turns) != 2 || turns[0].ToolCallID != "tool-42" || turns[1].ToolStatus != "after" {
		t.Fatalf("tool turns=%+v err=%v", turns, err)
	}
}

func TestRecordToolCallUsesCurrentContextVersionWhenOmitted(t *testing.T) {
	store := newTestSession(t, "")
	checkpoints, err := store.RecordToolCall("session-1", "tool-context", "shell", "input", "output", 0)
	if err != nil || len(checkpoints) != 2 || checkpoints[0].ContextVersion != 1 || checkpoints[1].ContextVersion != 1 {
		t.Fatalf("context-normalized checkpoints=%+v err=%v", checkpoints, err)
	}
}

func TestCompactionPersistsPairedCheckpointsAndNoPartialBegin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	store := newTestSession(t, path)
	if _, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "archive me"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Compact("session-1", CompactRequest{StartSequence: 1, EndSequence: 1, Summary: "archived"}); err != nil {
		t.Fatal(err)
	}
	checkpoints, err := store.ListCheckpoints("session-1")
	if err != nil || len(checkpoints) != 2 || checkpoints[0].Phase != CheckpointCompactionBegin || checkpoints[1].Phase != CheckpointCompactionDone || checkpoints[1].PrevHash != checkpoints[0].Hash {
		t.Fatalf("compaction checkpoints=%+v err=%v", checkpoints, err)
	}
	restarted, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := restarted.Recover("session-1", time.Now().UTC())
	if err != nil || recovery.LatestCheckpoint == nil || recovery.LatestCheckpoint.Phase != CheckpointCompactionDone {
		t.Fatalf("compaction recovery=%+v err=%v", recovery, err)
	}
	if _, err := restarted.Compact("session-1", CompactRequest{StartSequence: 0, EndSequence: 1, Summary: "invalid"}); err == nil {
		t.Fatal("invalid compaction unexpectedly succeeded")
	}
	checkpoints, err = restarted.ListCheckpoints("session-1")
	if err != nil || len(checkpoints) != 2 {
		t.Fatalf("failed compaction left checkpoint state=%+v err=%v", checkpoints, err)
	}
}

func TestPersistedCheckpointWithInvalidPhaseFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.json")
	store := newTestSession(t, path)
	turn, err := store.AppendTurn("session-1", Turn{Role: RoleUser, Content: "checkpoint"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveCheckpoint("session-1", Checkpoint{TurnSequence: 1, EventHash: turn.Hash, Phase: CheckpointTurnStarted, ContextVersion: 1}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	sessionState := state.Sessions["session-1"]
	checkpoint := &sessionState.Checkpoints[0]
	checkpoint.Phase = CheckpointPhase("unknown")
	checkpoint.Hash = hashCheckpoint(*checkpoint)
	state.Sessions["session-1"] = sessionState
	tampered, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("invalid checkpoint phase err=%v", err)
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
