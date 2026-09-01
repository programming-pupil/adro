// Package harness implements the durable, provider-neutral session harness.
//
// A harness session is deliberately independent from a provider-native run:
// turns, checkpoints, archives, leases, and outbox records are ADRO-owned
// facts. The local profile persists an atomic JSON snapshot; production
// adapters can implement the same contracts against PostgreSQL and a durable
// queue without changing pipeline semantics. The local profile uses an atomic
// JSON snapshot plus a short-lived fsynced journal for crash-window recovery.
package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/internal/domain"
)

var (
	ErrNotFound   = errors.New("harness session not found")
	ErrConflict   = errors.New("harness state conflict")
	ErrCorrupt    = errors.New("harness state is corrupt")
	ErrLeaseBusy  = errors.New("lease is held by another owner")
	ErrLeaseLost  = errors.New("lease is no longer owned")
	ErrWindowUsed = errors.New("compaction window overlaps an existing archive")
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Turn struct {
	ID             string            `json:"id"`
	SessionID      string            `json:"session_id"`
	Sequence       int64             `json:"sequence"`
	AttemptID      string            `json:"attempt_id,omitempty"`
	Role           Role              `json:"role"`
	Content        string            `json:"content"`
	ToolName       string            `json:"tool_name,omitempty"`
	ToolCallID     string            `json:"tool_call_id,omitempty"`
	ToolStatus     string            `json:"tool_status,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	PrevHash       string            `json:"prev_hash,omitempty"`
	Hash           string            `json:"hash"`
	CreatedAt      time.Time         `json:"created_at"`
}

type CheckpointPhase string

const (
	CheckpointTurnStarted     CheckpointPhase = "turn_started"
	CheckpointToolBefore      CheckpointPhase = "tool_before"
	CheckpointToolAfter       CheckpointPhase = "tool_after"
	CheckpointCompactionBegin CheckpointPhase = "compaction_before"
	CheckpointCompactionDone  CheckpointPhase = "compaction_after"
	CheckpointEffectBefore    CheckpointPhase = "effect_before"
	CheckpointEffectAfter     CheckpointPhase = "effect_after"
)

func (p CheckpointPhase) valid() bool {
	switch p {
	case CheckpointTurnStarted, CheckpointToolBefore, CheckpointToolAfter,
		CheckpointCompactionBegin, CheckpointCompactionDone, CheckpointEffectBefore,
		CheckpointEffectAfter:
		return true
	default:
		return false
	}
}

type Checkpoint struct {
	ID             string          `json:"id"`
	SessionID      string          `json:"session_id"`
	TurnSequence   int64           `json:"turn_sequence"`
	Phase          CheckpointPhase `json:"phase"`
	EventHash      string          `json:"event_hash"`
	PrevHash       string          `json:"prev_hash,omitempty"`
	ToolCallID     string          `json:"tool_call_id,omitempty"`
	ContextVersion int64           `json:"context_version"`
	OutboxIDs      []string        `json:"outbox_ids,omitempty"`
	LeaseIDs       []string        `json:"lease_ids,omitempty"`
	State          string          `json:"state,omitempty"`
	Hash           string          `json:"hash"`
	CreatedAt      time.Time       `json:"created_at"`
}

type ArchiveWindow struct {
	ID              string    `json:"id"`
	SessionID       string    `json:"session_id"`
	StartSequence   int64     `json:"start_sequence"`
	EndSequence     int64     `json:"end_sequence"`
	SourceHash      string    `json:"source_hash"`
	ReplacementHash string    `json:"replacement_hash"`
	Summary         string    `json:"summary"`
	RetainedTail    int       `json:"retained_tail"`
	ParentArchiveID string    `json:"parent_archive_id,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type MemoryItem struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	// Scope controls retention and visibility without requiring semantic search.
	// working is attempt-local, session is conversation-local, and project is
	// shared by sessions that opt into the same project ID.
	Scope     string `json:"scope"`
	ProjectID string `json:"project_id,omitempty"`
	Kind      string `json:"kind"`
	// Fingerprint is a deterministic claim key used by the reducer to detect
	// duplicate and conflicting facts without a vector index.
	Fingerprint string     `json:"fingerprint,omitempty"`
	Content     string     `json:"content"`
	SourceIDs   []string   `json:"source_ids"`
	Confidence  float64    `json:"confidence"`
	Importance  float64    `json:"importance,omitempty"`
	Pinned      bool       `json:"pinned,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Supersedes  []string   `json:"supersedes,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type Session struct {
	ID                   string    `json:"id"`
	TenantID             string    `json:"tenant_id"`
	WorkspaceID          string    `json:"workspace_id"`
	ProjectID            string    `json:"project_id,omitempty"`
	BudgetTokens         int64     `json:"budget_tokens"`
	AutoCompaction       bool      `json:"auto_compaction"`
	CompactionThreshold  float64   `json:"compaction_threshold"`
	CompactionRetainTail int       `json:"compaction_retain_tail"`
	AutoCompactionSet    bool      `json:"-"`
	ContextVersion       int64     `json:"context_version"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type Lease struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Key       string    `json:"key"`
	Owner     string    `json:"owner"`
	State     string    `json:"state"`
	Version   int64     `json:"version"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type OutboxEvent struct {
	ID             string          `json:"id"`
	SessionID      string          `json:"session_id"`
	IdempotencyKey string          `json:"idempotency_key"`
	Payload        json.RawMessage `json:"payload"`
	State          string          `json:"state"`
	Attempts       int             `json:"attempts"`
	Owner          string          `json:"owner,omitempty"`
	LeaseUntil     time.Time       `json:"lease_until,omitempty"`
	NextAttemptAt  time.Time       `json:"next_attempt_at"`
	PublishedAt    *time.Time      `json:"published_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

type Recovery struct {
	Session          Session
	LatestCheckpoint *Checkpoint
	PendingEffects   []OutboxEvent
	ExpiredLeases    []Lease
}

type ContextStatus struct {
	SessionID            string  `json:"session_id"`
	ContextVersion       int64   `json:"context_version"`
	TurnCount            int     `json:"turn_count"`
	ArchivedTurns        int     `json:"archived_turns"`
	TokenEstimate        int64   `json:"token_estimate"`
	BudgetTokens         int64   `json:"budget_tokens"`
	ArchiveCount         int     `json:"archive_count"`
	MemoryCount          int     `json:"memory_count"`
	CheckpointCount      int     `json:"checkpoint_count"`
	AutoCompaction       bool    `json:"auto_compaction"`
	CompactionThreshold  float64 `json:"compaction_threshold"`
	CompactionRetainTail int     `json:"compaction_retain_tail"`
	LastTurnHash         string  `json:"last_turn_hash,omitempty"`
	TranscriptDurable    bool    `json:"transcript_durable"`
}

// TranscriptIntegrity is a compact audit result for the append-only log and
// archive replacement proofs. It is safe to expose through diagnostics.
type TranscriptIntegrity struct {
	SessionID      string    `json:"session_id"`
	TurnCount      int       `json:"turn_count"`
	ArchiveCount   int       `json:"archive_count"`
	Valid          bool      `json:"valid"`
	RecallVerified bool      `json:"recall_verified"`
	CheckedAt      time.Time `json:"checked_at"`
	Error          string    `json:"error,omitempty"`
}

type MemoryReduction struct {
	Added       []MemoryItem `json:"added"`
	Superseded  []string     `json:"superseded,omitempty"`
	Conflicts   []string     `json:"conflicts,omitempty"`
	SourceTurns []string     `json:"source_turns"`
}

type sessionState struct {
	Session     Session         `json:"session"`
	Turns       []Turn          `json:"turns"`
	Checkpoints []Checkpoint    `json:"checkpoints"`
	Archives    []ArchiveWindow `json:"archives"`
	Memories    []MemoryItem    `json:"memories"`
	Leases      []Lease         `json:"leases"`
	Outbox      []OutboxEvent   `json:"outbox"`
}

type persistedState struct {
	Version         int                     `json:"version"`
	Sessions        map[string]sessionState `json:"sessions"`
	ProjectMemories map[string][]MemoryItem `json:"project_memories,omitempty"`
}

type Store struct {
	mu              sync.RWMutex
	path            string
	transcriptPath  string
	sessions        map[string]sessionState
	projectMemories map[string][]MemoryItem
}

const harnessStateVersion = 3

// Durable reports whether this store is backed by an operator-owned snapshot
// file. It intentionally does not expose the path in diagnostics.
func (s *Store) Durable() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.path != ""
}

func New(path string) (*Store, error) {
	s := &Store{path: strings.TrimSpace(path), sessions: map[string]sessionState{}, projectMemories: map[string][]MemoryItem{}}
	if s.path != "" {
		s.transcriptPath = transcriptPath(s.path)
	}
	if s.path == "" {
		return s, nil
	}
	var state persistedState
	var snapshotErr error
	data, err := os.ReadFile(s.path)
	snapshotExists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read harness state: %w", err)
	}
	if snapshotExists {
		if err := json.Unmarshal(data, &state); err != nil {
			snapshotErr = fmt.Errorf("decode harness state: %w", err)
		}
	}
	// The journal is written after the temporary snapshot is synced and before
	// rename. If a process dies in that window, the last complete journal record
	// is the durable intent and is safe to recover. A stale journal after a
	// successful rename contains the same state and is harmless.
	if journal, journalErr := readLastJournal(journalPath(s.path)); journalErr != nil {
		return nil, fmt.Errorf("read harness journal: %w", journalErr)
	} else if journal != nil {
		state = *journal
		snapshotErr = nil
	} else if snapshotErr != nil {
		return nil, snapshotErr
	} else if !snapshotExists {
		if s.transcriptPath != "" {
			if _, transcriptErr := os.Stat(s.transcriptPath); transcriptErr == nil {
				return nil, fmt.Errorf("%w: transcript exists without a session snapshot", ErrCorrupt)
			}
		}
		return s, nil
	}
	if state.Version > harnessStateVersion {
		return nil, fmt.Errorf("unsupported harness state version %d", state.Version)
	}
	if state.Sessions != nil {
		s.sessions = state.Sessions
	}
	if state.ProjectMemories != nil {
		s.projectMemories = state.ProjectMemories
	}
	if state.Version < harnessStateVersion {
		if err := migrateCheckpointChains(s.sessions); err != nil {
			return nil, err
		}
		state.Version = harnessStateVersion
	}
	// Older snapshots predate memory scopes. Treat those records as session
	// memory so upgrades preserve their visibility and ordering.
	for sessionID, sessionState := range s.sessions {
		for i := range sessionState.Memories {
			if strings.TrimSpace(sessionState.Memories[i].Scope) == "" {
				sessionState.Memories[i].Scope = "session"
			}
		}
		s.sessions[sessionID] = sessionState
	}
	for projectID, memories := range s.projectMemories {
		for i := range memories {
			memories[i].Scope = "project"
			if memories[i].ProjectID == "" {
				memories[i].ProjectID = projectID
			}
		}
		s.projectMemories[projectID] = memories
	}
	for id := range s.sessions {
		if err := validateSessionState(s.sessions[id]); err != nil {
			return nil, fmt.Errorf("validate session %s: %w", id, err)
		}
	}
	for projectID, memories := range s.projectMemories {
		if strings.TrimSpace(projectID) == "" {
			return nil, fmt.Errorf("%w: project memory has empty project id", ErrCorrupt)
		}
		seen := map[string]struct{}{}
		for _, memory := range memories {
			if memory.ID == "" || memory.Scope != "project" || memory.ProjectID != projectID || strings.TrimSpace(memory.Content) == "" || memory.Confidence < 0 || memory.Confidence > 1 || memory.Importance < 0 || memory.Importance > 1 || memory.Fingerprint != "" && memory.Fingerprint != memoryFingerprint(memory.Kind, memory.Content) {
				return nil, fmt.Errorf("%w: project memory %s", ErrCorrupt, memory.ID)
			}
			if _, duplicate := seen[memory.ID]; duplicate {
				return nil, fmt.Errorf("%w: duplicate project memory %s", ErrCorrupt, memory.ID)
			}
			seen[memory.ID] = struct{}{}
		}
	}
	if s.transcriptPath != "" {
		if err := s.reconcileTranscript(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) Flush() error {
	// Flush performs a rename-based snapshot write. Serialize it with
	// mutations so concurrent requests cannot race two file swaps.
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistLocked()
}

func (s *Store) persistLocked() error {
	if s.path == "" {
		return nil
	}
	state := persistedState{Version: harnessStateVersion, Sessions: s.sessions, ProjectMemories: s.projectMemories}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	journalData, err := json.Marshal(state)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".adro-harness-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Keep one complete, fsynced record while the snapshot is being swapped.
	// This closes the crash window between writing the temporary file and the
	// rename without turning the journal into a second source of truth.
	journal := journalPath(s.path)
	if err := appendJournal(journal, journalData); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(journal)
		return err
	}
	// Sync the directory entry as well as the file contents so a power loss
	// after rename cannot silently resurrect the previous snapshot.
	dirFile, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer dirFile.Close()
	if err := dirFile.Sync(); err != nil {
		return err
	}
	// The snapshot is now authoritative. Removal is best effort: a stale record
	// is byte-for-byte equivalent and will be replayed safely on the next boot.
	_ = os.Remove(journal)
	return nil
}

func journalPath(snapshotPath string) string {
	return snapshotPath + ".journal"
}

func appendJournal(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open harness journal: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("write harness journal: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync harness journal: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close harness journal: %w", err)
	}
	return nil
}

func readLastJournal(path string) (*persistedState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var latest *persistedState
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var candidate persistedState
		if err := json.Unmarshal([]byte(line), &candidate); err != nil {
			// A torn final line is ignored. Earlier complete records remain
			// usable, which is the important property during crash recovery.
			continue
		}
		latest = &candidate
	}
	return latest, nil
}

type transcriptRecord struct {
	Version   int    `json:"version"`
	SessionID string `json:"session_id"`
	Turn      Turn   `json:"turn"`
}

func transcriptPath(snapshotPath string) string { return snapshotPath + ".transcript.jsonl" }

func appendTranscript(path string, turn Turn) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	record, err := json.Marshal(transcriptRecord{Version: 1, SessionID: turn.SessionID, Turn: turn})
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open transcript: %w", err)
	}
	if _, err := file.Write(append(record, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("write transcript: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync transcript: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close transcript: %w", err)
	}
	return nil
}

func readTranscript(path string) (map[string][]Turn, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string][]Turn{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	records := map[string][]Turn{}
	lines := strings.Split(string(data), "\n")
	for index, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record transcriptRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			// A torn final append is recoverable. Corruption in an earlier line
			// is not, because skipping it would silently rewrite history.
			if index == len(lines)-1 {
				continue
			}
			return nil, false, fmt.Errorf("%w: transcript line %d: %v", ErrCorrupt, index+1, err)
		}
		if record.Version != 1 || strings.TrimSpace(record.SessionID) == "" || record.Turn.SessionID != record.SessionID || record.Turn.Hash == "" {
			return nil, false, fmt.Errorf("%w: invalid transcript record at line %d", ErrCorrupt, index+1)
		}
		turn := record.Turn
		if hashTurn(turn) != turn.Hash {
			return nil, false, fmt.Errorf("%w: transcript turn %s hash mismatch", ErrCorrupt, turn.ID)
		}
		items := records[record.SessionID]
		if turn.Sequence != int64(len(items)+1) {
			return nil, false, fmt.Errorf("%w: transcript sequence for session %s is %d", ErrCorrupt, record.SessionID, turn.Sequence)
		}
		if len(items) > 0 && turn.PrevHash != items[len(items)-1].Hash {
			return nil, false, fmt.Errorf("%w: transcript prev_hash for session %s is invalid", ErrCorrupt, record.SessionID)
		}
		records[record.SessionID] = append(items, cloneTurn(turn))
	}
	return records, true, nil
}

func (s *Store) reconcileTranscript() error {
	records, exists, err := readTranscript(s.transcriptPath)
	if err != nil {
		return fmt.Errorf("read transcript: %w", err)
	}
	changed := false
	if !exists {
		for _, state := range s.sessions {
			for _, turn := range state.Turns {
				if err := appendTranscript(s.transcriptPath, turn); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for sessionID, logged := range records {
		state, ok := s.sessions[sessionID]
		if !ok {
			return fmt.Errorf("%w: transcript references unknown session %s", ErrCorrupt, sessionID)
		}
		if len(logged) > len(state.Turns) {
			state.Turns = append(state.Turns, logged[len(state.Turns):]...)
			state.Session.UpdatedAt = logged[len(logged)-1].CreatedAt
			s.sessions[sessionID] = state
			changed = true
		}
		if len(state.Turns) > len(logged) {
			for _, turn := range state.Turns[len(logged):] {
				if err := appendTranscript(s.transcriptPath, turn); err != nil {
					return err
				}
			}
		}
		for index, turn := range state.Turns {
			if index >= len(logged) || turnDigest(turn) != turnDigest(logged[index]) || turn.Hash != logged[index].Hash || turn.PrevHash != logged[index].PrevHash {
				return fmt.Errorf("%w: transcript diverges at session %s sequence %d", ErrCorrupt, sessionID, index+1)
			}
		}
	}
	for sessionID, state := range s.sessions {
		if _, ok := records[sessionID]; ok {
			continue
		}
		for _, turn := range state.Turns {
			if err := appendTranscript(s.transcriptPath, turn); err != nil {
				return err
			}
		}
	}
	if changed {
		for sessionID, state := range s.sessions {
			if err := validateSessionState(state); err != nil {
				return fmt.Errorf("validate reconciled session %s: %w", sessionID, err)
			}
		}
		if err := s.persistLocked(); err != nil {
			return fmt.Errorf("persist reconciled transcript state: %w", err)
		}
	}
	return nil
}

func (s *Store) CreateSession(session Session) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(session.ID) == "" {
		session.ID = domain.NewID()
	}
	if strings.TrimSpace(session.TenantID) == "" || strings.TrimSpace(session.WorkspaceID) == "" {
		return Session{}, errors.New("tenant_id and workspace_id are required")
	}
	if session.BudgetTokens < 0 {
		return Session{}, errors.New("budget_tokens cannot be negative")
	}
	if session.CompactionThreshold <= 0 {
		session.CompactionThreshold = 0.80
	}
	if session.CompactionThreshold > 1 {
		return Session{}, errors.New("compaction_threshold must be in (0,1]")
	}
	if session.CompactionRetainTail < 0 {
		return Session{}, errors.New("compaction_retain_tail cannot be negative")
	}
	if session.BudgetTokens > 0 {
		// A bounded session needs an automatic guard by default. The explicit
		// compact endpoint remains available when a caller has a better summary.
		if !session.AutoCompactionSet {
			session.AutoCompaction = true
		}
		if session.CompactionRetainTail == 0 {
			session.CompactionRetainTail = 4
		}
	}
	if _, exists := s.sessions[session.ID]; exists {
		return Session{}, ErrConflict
	}
	now := time.Now().UTC()
	session.ContextVersion = 1
	session.CreatedAt, session.UpdatedAt = now, now
	s.sessions[session.ID] = sessionState{Session: session}
	if err := s.persistLocked(); err != nil {
		delete(s.sessions, session.ID)
		return Session{}, fmt.Errorf("persist session: %w", err)
	}
	return session, nil
}

func (s *Store) EnsureSession(session Session) (Session, error) {
	if strings.TrimSpace(session.ID) == "" {
		return s.CreateSession(session)
	}
	s.mu.RLock()
	state, ok := s.sessions[session.ID]
	s.mu.RUnlock()
	if ok {
		if session.TenantID != "" && state.Session.TenantID != session.TenantID || session.WorkspaceID != "" && state.Session.WorkspaceID != session.WorkspaceID {
			return Session{}, ErrConflict
		}
		return state.Session, nil
	}
	created, err := s.CreateSession(session)
	if !errors.Is(err, ErrConflict) {
		return created, err
	}
	// Concurrent bootstrap requests should converge on the same session. Read
	// the winner and still enforce tenant/workspace ownership.
	s.mu.RLock()
	state, ok = s.sessions[session.ID]
	s.mu.RUnlock()
	if !ok {
		return Session{}, err
	}
	if session.TenantID != "" && state.Session.TenantID != session.TenantID || session.WorkspaceID != "" && state.Session.WorkspaceID != session.WorkspaceID {
		return Session{}, ErrConflict
	}
	return state.Session, nil
}

func (s *Store) GetSession(id string) (Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.sessions[id]
	if !ok {
		return Session{}, ErrNotFound
	}
	return state.Session, nil
}

// ListSessions returns a stable snapshot of all durable sessions. Recovery
// workers use this cursor-free view because the local profile owns the whole
// file; a SQL adapter can implement the same contract with a tenant-scoped
// cursor without changing worker semantics.
func (s *Store) ListSessions() []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]Session, 0, len(s.sessions))
	for _, state := range s.sessions {
		items = append(items, state.Session)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items
}

func (s *Store) AppendTurn(sessionID string, turn Turn) (Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return Turn{}, ErrNotFound
	}
	if turn.Role == "" || !validRole(turn.Role) || strings.TrimSpace(turn.Content) == "" {
		return Turn{}, errors.New("role and content are required")
	}
	if turn.SessionID == "" {
		turn.SessionID = sessionID
	}
	if turn.SessionID != sessionID {
		return Turn{}, ErrConflict
	}
	if turn.IdempotencyKey != "" {
		for _, existing := range state.Turns {
			if existing.IdempotencyKey == turn.IdempotencyKey {
				if turnDigest(existing) != turnDigest(turn) {
					return Turn{}, ErrConflict
				}
				return cloneTurn(existing), nil
			}
		}
	}
	turn.ID = strings.TrimSpace(turn.ID)
	if turn.ID == "" {
		turn.ID = domain.NewID()
	}
	if turn.Sequence != 0 && turn.Sequence != int64(len(state.Turns)+1) {
		return Turn{}, ErrConflict
	}
	turn.Sequence = int64(len(state.Turns) + 1)
	turn.PrevHash = ""
	if len(state.Turns) > 0 {
		turn.PrevHash = state.Turns[len(state.Turns)-1].Hash
	}
	turn.CreatedAt = time.Now().UTC()
	turn.Hash = hashTurn(turn)
	state.Turns = append(state.Turns, cloneTurn(turn))
	state.Session.UpdatedAt = turn.CreatedAt
	_, autoCompacted := autoCompactLocked(&state)
	s.sessions[sessionID] = state
	if err := s.persistLocked(); err != nil {
		state.Turns = state.Turns[:len(state.Turns)-1]
		if autoCompacted {
			state.Archives = state.Archives[:len(state.Archives)-1]
			state.Session.ContextVersion--
		}
		s.sessions[sessionID] = state
		return Turn{}, fmt.Errorf("persist turn: %w", err)
	}
	if err := appendTranscript(s.transcriptPath, turn); err != nil {
		// The snapshot is already durable. Return an error so callers fail closed;
		// the next boot reconciles the snapshot turn into the append-only log.
		return Turn{}, fmt.Errorf("persist transcript: %w", err)
	}
	return cloneTurn(turn), nil
}

// RecordToolCall persists one provider tool transaction and its paired
// checkpoints. The idempotency keys make replay after a lost provider
// response converge on the existing turns/checkpoints.
func (s *Store) RecordToolCall(sessionID, callID, name, input, output string, contextVersion int64) ([]Checkpoint, error) {
	callID, name = strings.TrimSpace(callID), strings.TrimSpace(name)
	if callID == "" {
		return nil, errors.New("tool call id is required")
	}
	if contextVersion <= 0 {
		if session, err := s.GetSession(sessionID); err != nil {
			return nil, err
		} else {
			contextVersion = session.ContextVersion
		}
	}
	beforeContent := strings.TrimSpace(input)
	if beforeContent == "" {
		beforeContent = "tool call"
	}
	afterContent := strings.TrimSpace(output)
	if afterContent == "" {
		afterContent = "tool completed"
	}
	before, err := s.AppendTurn(sessionID, Turn{Role: RoleTool, Content: beforeContent, ToolName: name, ToolCallID: callID, ToolStatus: "before", IdempotencyKey: "tool:" + callID + ":before"})
	if err != nil {
		return nil, err
	}
	beforeCheckpoint, err := s.SaveCheckpoint(sessionID, Checkpoint{TurnSequence: before.Sequence, Phase: CheckpointToolBefore, EventHash: before.Hash, ToolCallID: callID, ContextVersion: contextVersion, State: "tool started"})
	if err != nil {
		return nil, err
	}
	after, err := s.AppendTurn(sessionID, Turn{Role: RoleTool, Content: afterContent, ToolName: name, ToolCallID: callID, ToolStatus: "after", IdempotencyKey: "tool:" + callID + ":after"})
	if err != nil {
		return nil, err
	}
	afterCheckpoint, err := s.SaveCheckpoint(sessionID, Checkpoint{TurnSequence: after.Sequence, Phase: CheckpointToolAfter, EventHash: after.Hash, ToolCallID: callID, ContextVersion: contextVersion, State: "tool completed"})
	if err != nil {
		return nil, err
	}
	return []Checkpoint{beforeCheckpoint, afterCheckpoint}, nil
}

func (s *Store) ListTurns(sessionID string, after int64, limit int) ([]Turn, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return nil, 0, ErrNotFound
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	items := make([]Turn, 0, limit)
	for _, turn := range state.Turns {
		if turn.Sequence <= after {
			continue
		}
		items = append(items, cloneTurn(turn))
		if len(items) == limit {
			break
		}
	}
	var next int64
	if len(items) == limit {
		next = items[len(items)-1].Sequence
	}
	return items, next, nil
}

func (s *Store) SaveCheckpoint(sessionID string, checkpoint Checkpoint) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return Checkpoint{}, ErrNotFound
	}
	if !checkpoint.Phase.valid() || checkpoint.TurnSequence < 0 {
		return Checkpoint{}, errors.New("invalid checkpoint phase or turn sequence")
	}
	if checkpoint.TurnSequence > int64(len(state.Turns)) {
		return Checkpoint{}, ErrConflict
	}
	if checkpoint.EventHash != "" && !hasTurnHash(state.Turns, checkpoint.EventHash) {
		return Checkpoint{}, fmt.Errorf("%w: checkpoint event hash is not in the transcript", ErrCorrupt)
	}
	if len(state.Checkpoints) > 0 && checkpoint.TurnSequence < state.Checkpoints[len(state.Checkpoints)-1].TurnSequence {
		return Checkpoint{}, ErrConflict
	}
	if len(state.Checkpoints) > 0 && checkpoint.ContextVersion < state.Checkpoints[len(state.Checkpoints)-1].ContextVersion {
		return Checkpoint{}, ErrConflict
	}
	previousHash := ""
	if len(state.Checkpoints) > 0 {
		previousHash = state.Checkpoints[len(state.Checkpoints)-1].Hash
	}
	if checkpoint.PrevHash != "" && checkpoint.PrevHash != previousHash {
		return Checkpoint{}, fmt.Errorf("%w: checkpoint previous hash does not match the chain", ErrCorrupt)
	}
	// A provider dispatch may have committed its provenance before the process
	// lost the response while writing the after-effect checkpoint. Replaying
	// the durable intent must converge on the existing checkpoint instead of
	// growing an unbounded duplicate trail.
	for _, existing := range state.Checkpoints {
		if existing.TurnSequence == checkpoint.TurnSequence &&
			existing.Phase == checkpoint.Phase &&
			existing.EventHash == checkpoint.EventHash &&
			existing.ToolCallID == checkpoint.ToolCallID &&
			existing.ContextVersion == checkpoint.ContextVersion &&
			existing.State == checkpoint.State &&
			sameStrings(existing.OutboxIDs, checkpoint.OutboxIDs) &&
			sameStrings(existing.LeaseIDs, checkpoint.LeaseIDs) {
			return cloneCheckpoint(existing), nil
		}
	}
	if checkpoint.ToolCallID != "" {
		pairedBefore, pairedAfter := false, false
		for _, existing := range state.Checkpoints {
			if existing.ToolCallID != checkpoint.ToolCallID {
				continue
			}
			pairedBefore = pairedBefore || existing.Phase == CheckpointToolBefore
			pairedAfter = pairedAfter || existing.Phase == CheckpointToolAfter
		}
		switch checkpoint.Phase {
		case CheckpointToolBefore:
			if pairedBefore {
				return Checkpoint{}, fmt.Errorf("%w: tool checkpoint is already open", ErrConflict)
			}
		case CheckpointToolAfter:
			if !pairedBefore || pairedAfter {
				return Checkpoint{}, fmt.Errorf("%w: tool checkpoint has no unique before phase", ErrConflict)
			}
		}
	}
	checkpoint.ID = strings.TrimSpace(checkpoint.ID)
	if checkpoint.ID == "" {
		checkpoint.ID = domain.NewID()
	}
	checkpoint.SessionID = sessionID
	checkpoint.PrevHash = previousHash
	checkpoint.OutboxIDs = append([]string(nil), checkpoint.OutboxIDs...)
	checkpoint.LeaseIDs = append([]string(nil), checkpoint.LeaseIDs...)
	checkpoint.CreatedAt = time.Now().UTC()
	checkpoint.Hash = hashCheckpoint(checkpoint)
	state.Checkpoints = append(state.Checkpoints, checkpoint)
	state.Session.UpdatedAt = checkpoint.CreatedAt
	s.sessions[sessionID] = state
	if err := s.persistLocked(); err != nil {
		state.Checkpoints = state.Checkpoints[:len(state.Checkpoints)-1]
		s.sessions[sessionID] = state
		return Checkpoint{}, fmt.Errorf("persist checkpoint: %w", err)
	}
	return cloneCheckpoint(checkpoint), nil
}

func (s *Store) ListCheckpoints(sessionID string) ([]Checkpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return nil, ErrNotFound
	}
	items := make([]Checkpoint, len(state.Checkpoints))
	for i := range state.Checkpoints {
		items[i] = cloneCheckpoint(state.Checkpoints[i])
	}
	return items, nil
}

type CompactRequest struct {
	StartSequence int64  `json:"start_sequence"`
	EndSequence   int64  `json:"end_sequence"`
	Summary       string `json:"summary"`
	RetainedTail  int    `json:"retained_tail"`
	Reason        string `json:"reason,omitempty"`
}

func (s *Store) Compact(sessionID string, request CompactRequest) (ArchiveWindow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return ArchiveWindow{}, ErrNotFound
	}
	archive, err := compactLocked(&state, request)
	if err != nil {
		return ArchiveWindow{}, err
	}
	s.sessions[sessionID] = state
	if err := s.persistLocked(); err != nil {
		state.Archives = state.Archives[:len(state.Archives)-1]
		state.Session.ContextVersion--
		s.sessions[sessionID] = state
		return ArchiveWindow{}, fmt.Errorf("persist compaction: %w", err)
	}
	return archive, nil
}

func compactLocked(state *sessionState, request CompactRequest) (ArchiveWindow, error) {
	if state == nil {
		return ArchiveWindow{}, ErrNotFound
	}
	if request.StartSequence < 1 || request.EndSequence < request.StartSequence || request.EndSequence > int64(len(state.Turns)) || strings.TrimSpace(request.Summary) == "" {
		return ArchiveWindow{}, errors.New("invalid compaction window or summary")
	}
	if request.RetainedTail < 0 {
		return ArchiveWindow{}, errors.New("retained_tail cannot be negative")
	}
	for _, existing := range state.Archives {
		if request.StartSequence <= existing.EndSequence && request.EndSequence >= existing.StartSequence {
			return ArchiveWindow{}, ErrWindowUsed
		}
	}
	var source strings.Builder
	for _, turn := range state.Turns {
		if turn.Sequence >= request.StartSequence && turn.Sequence <= request.EndSequence {
			data, _ := json.Marshal(turn)
			source.Write(data)
			source.WriteByte('\n')
		}
	}
	sourceHash := digest([]byte(source.String()))
	replacementHash := digest([]byte(strings.TrimSpace(request.Summary)))
	archive := ArchiveWindow{ID: domain.NewID(), SessionID: state.Session.ID, StartSequence: request.StartSequence, EndSequence: request.EndSequence, SourceHash: sourceHash, ReplacementHash: replacementHash, Summary: strings.TrimSpace(request.Summary), RetainedTail: request.RetainedTail, Reason: strings.TrimSpace(request.Reason), CreatedAt: time.Now().UTC()}
	if len(state.Archives) > 0 {
		archive.ParentArchiveID = state.Archives[len(state.Archives)-1].ID
	}
	state.Archives = append(state.Archives, archive)
	state.Session.ContextVersion++
	state.Session.UpdatedAt = archive.CreatedAt
	return archive, nil
}

// autoCompactLocked archives the oldest unarchived turns once a bounded
// session reaches its configured threshold. The generated summary is
// deterministic and provenance-preserving; callers can still replace it with
// a higher-quality model summary through Compact because the full transcript
// remains intact for audit and replay.
func autoCompactLocked(state *sessionState) (ArchiveWindow, bool) {
	if state == nil || !state.Session.AutoCompaction || state.Session.BudgetTokens <= 0 || len(state.Turns) == 0 {
		return ArchiveWindow{}, false
	}
	threshold := state.Session.CompactionThreshold
	if threshold <= 0 || threshold > 1 {
		threshold = 0.80
	}
	budget := state.Session.BudgetTokens
	var total int64
	for _, turn := range state.Turns {
		if !turnArchived(state.Archives, turn.Sequence) {
			total += estimateTokens(turn.Content)
		}
	}
	if float64(total) <= float64(budget)*threshold {
		return ArchiveWindow{}, false
	}
	retain := state.Session.CompactionRetainTail
	if retain < 1 {
		retain = 1
	}
	active := make([]Turn, 0, len(state.Turns))
	started := false
	for _, turn := range state.Turns {
		if turnArchived(state.Archives, turn.Sequence) {
			if started {
				break
			}
			continue
		}
		started = true
		active = append(active, turn)
	}
	if len(active) <= retain {
		return ArchiveWindow{}, false
	}
	end := active[len(active)-retain-1].Sequence
	start := active[0].Sequence
	selected := active[:len(active)-retain]
	var selectedTokens int64
	for _, turn := range selected {
		selectedTokens += estimateTokens(turn.Content)
	}
	// A tiny window is cheaper and more faithful when left as-is. The bounded
	// compiler will truncate it if the caller chose an unusually small budget.
	if selectedTokens < 16 {
		return ArchiveWindow{}, false
	}
	summary := automaticSummary(selected, budget)
	archive, err := compactLocked(state, CompactRequest{StartSequence: start, EndSequence: end, Summary: summary, RetainedTail: retain, Reason: "automatic budget guard"})
	if err != nil {
		return ArchiveWindow{}, false
	}
	return archive, true
}

func turnArchived(archives []ArchiveWindow, sequence int64) bool {
	for _, archive := range archives {
		if sequence >= archive.StartSequence && sequence <= archive.EndSequence {
			return true
		}
	}
	return false
}

func automaticSummary(turns []Turn, budget int64) string {
	sourceRunes := 0
	for _, turn := range turns {
		sourceRunes += len([]rune(turn.Content)) + 24
	}
	maxRunes := sourceRunes / 3
	budgetRunes := int(budget * 2)
	if budgetRunes > 0 && maxRunes > budgetRunes {
		maxRunes = budgetRunes
	}
	if maxRunes < 96 {
		maxRunes = 96
	}
	if maxRunes > 12000 {
		maxRunes = 12000
	}
	var builder strings.Builder
	builder.WriteString("Auto-archived transcript; full turns remain available for replay.\n")
	for _, turn := range turns {
		line := fmt.Sprintf("[%d %s] %s\n", turn.Sequence, turn.Role, strings.TrimSpace(turn.Content))
		remaining := maxRunes - builder.Len()
		if remaining <= 0 {
			break
		}
		if len([]rune(line)) > remaining {
			runes := []rune(line)
			if remaining > 4 {
				line = string(runes[:remaining-4]) + "...\n"
			}
		}
		builder.WriteString(line)
	}
	return strings.TrimSpace(builder.String())
}

func (s *Store) AddMemory(item MemoryItem) (MemoryItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[item.SessionID]
	if !ok {
		return MemoryItem{}, ErrNotFound
	}
	item.Scope = strings.ToLower(strings.TrimSpace(item.Scope))
	if item.Scope == "" {
		item.Scope = "session"
	}
	if item.Scope != "working" && item.Scope != "session" && item.Scope != "project" {
		return MemoryItem{}, errors.New("memory scope must be working, session, or project")
	}
	if strings.TrimSpace(item.Kind) == "" || strings.TrimSpace(item.Content) == "" || len(item.SourceIDs) == 0 || item.Confidence < 0 || item.Confidence > 1 || item.Importance < 0 || item.Importance > 1 {
		return MemoryItem{}, errors.New("memory kind, content, source_ids and confidence are required")
	}
	if item.Fingerprint == "" {
		item.Fingerprint = memoryFingerprint(item.Kind, item.Content)
	} else if item.Fingerprint != memoryFingerprint(item.Kind, item.Content) {
		return MemoryItem{}, errors.New("memory fingerprint does not match kind and content")
	}
	if item.Scope == "project" && strings.TrimSpace(state.Session.ProjectID) == "" {
		return MemoryItem{}, errors.New("project memory requires a session project_id")
	}
	if item.Scope == "project" {
		item.ProjectID = strings.TrimSpace(item.ProjectID)
		if item.ProjectID == "" {
			item.ProjectID = state.Session.ProjectID
		}
		if item.ProjectID != state.Session.ProjectID {
			return MemoryItem{}, ErrConflict
		}
	} else {
		item.ProjectID = ""
	}
	for _, source := range item.SourceIDs {
		if !hasTurnID(state.Turns, source) {
			return MemoryItem{}, fmt.Errorf("%w: memory source turn %s is missing", ErrCorrupt, source)
		}
	}
	item.ID = strings.TrimSpace(item.ID)
	if item.ID == "" {
		item.ID = domain.NewID()
	}
	if hasMemoryID(state.Memories, item.ID) || hasMemoryID(s.projectMemories[item.ProjectID], item.ID) {
		return MemoryItem{}, ErrConflict
	}
	availableMemories := state.Memories
	if item.Scope == "project" {
		availableMemories = s.projectMemories[item.ProjectID]
	}
	// A reducer replay may submit the same claim after its response was lost.
	// Return the durable active item so retries converge without creating a new
	// memory record or changing its source provenance.
	for _, existing := range activeMemoryFrontier(availableMemories, time.Now().UTC()) {
		fingerprint := existing.Fingerprint
		if fingerprint == "" {
			fingerprint = memoryFingerprint(existing.Kind, existing.Content)
		}
		if existing.Scope == item.Scope && existing.Kind == item.Kind && fingerprint == item.Fingerprint {
			return cloneMemory(existing), nil
		}
	}
	for _, superseded := range item.Supersedes {
		if strings.TrimSpace(superseded) == item.ID {
			return MemoryItem{}, fmt.Errorf("%w: memory cannot supersede itself", ErrConflict)
		}
		if !hasMemoryID(availableMemories, superseded) {
			return MemoryItem{}, fmt.Errorf("%w: superseded memory %s is missing", ErrCorrupt, superseded)
		}
	}
	item.SourceIDs = append([]string(nil), item.SourceIDs...)
	item.Supersedes = append([]string(nil), item.Supersedes...)
	item.CreatedAt = time.Now().UTC()
	if item.Scope == "project" {
		s.projectMemories[item.ProjectID] = append(s.projectMemories[item.ProjectID], item)
	} else {
		state.Memories = append(state.Memories, item)
	}
	state.Session.UpdatedAt = item.CreatedAt
	s.sessions[item.SessionID] = state
	if err := s.persistLocked(); err != nil {
		if item.Scope == "project" {
			items := s.projectMemories[item.ProjectID]
			s.projectMemories[item.ProjectID] = items[:len(items)-1]
		} else {
			state.Memories = state.Memories[:len(state.Memories)-1]
		}
		s.sessions[item.SessionID] = state
		return MemoryItem{}, fmt.Errorf("persist memory: %w", err)
	}
	return cloneMemory(item), nil
}

// ReduceMemories extracts deterministic claims from a transcript turn. It is
// deliberately lexical and explainable: operators can inspect the source
// turn IDs, fingerprint, and supersession chain without a vector database.
// Lines use an optional "fact:", "constraint:", "decision:", "invariant:",
// or "preference:" prefix; unprefixed non-empty lines become facts.
func (s *Store) ReduceMemories(sessionID string, sourceIDs []string, content string) (MemoryReduction, error) {
	sourceIDs = uniqueNonEmpty(sourceIDs)
	if len(sourceIDs) == 0 || strings.TrimSpace(content) == "" {
		return MemoryReduction{}, errors.New("source_ids and content are required")
	}
	s.mu.RLock()
	state, ok := s.sessions[sessionID]
	if !ok {
		s.mu.RUnlock()
		return MemoryReduction{}, ErrNotFound
	}
	for _, sourceID := range sourceIDs {
		if !hasTurnID(state.Turns, sourceID) {
			s.mu.RUnlock()
			return MemoryReduction{}, fmt.Errorf("%w: memory source turn %s is missing", ErrCorrupt, sourceID)
		}
	}
	existing := append([]MemoryItem(nil), state.Memories...)
	if state.Session.ProjectID != "" {
		existing = append(existing, s.projectMemories[state.Session.ProjectID]...)
	}
	s.mu.RUnlock()
	claims := parseClaims(content)
	if len(claims) == 0 {
		return MemoryReduction{}, errors.New("no memory claims found")
	}
	reduction := MemoryReduction{SourceTurns: append([]string(nil), sourceIDs...)}
	for _, claim := range claims {
		item := MemoryItem{SessionID: sessionID, Scope: "session", Kind: claim.Kind, Content: claim.Content, SourceIDs: sourceIDs, Confidence: claim.Confidence, Importance: claim.Importance, Fingerprint: memoryFingerprint(claim.Kind, claim.Content)}
		for _, old := range existing {
			if old.Scope != item.Scope || old.Kind != item.Kind || memorySubject(old.Content) != memorySubject(item.Content) || !memoryActive(old, time.Now().UTC()) {
				continue
			}
			if old.Fingerprint == item.Fingerprint || strings.EqualFold(strings.TrimSpace(old.Content), strings.TrimSpace(item.Content)) {
				item.ID = old.ID
				break
			}
			item.Supersedes = append(item.Supersedes, old.ID)
			reduction.Superseded = append(reduction.Superseded, old.ID)
			reduction.Conflicts = append(reduction.Conflicts, old.ID)
		}
		if item.ID != "" {
			continue
		}
		saved, err := s.AddMemory(item)
		if err != nil {
			return reduction, err
		}
		reduction.Added = append(reduction.Added, saved)
		existing = append(existing, saved)
	}
	return reduction, nil
}

type parsedClaim struct {
	Kind       string
	Content    string
	Confidence float64
	Importance float64
}

func parseClaims(content string) []parsedClaim {
	claims := make([]parsedClaim, 0)
	for _, raw := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kind := "fact"
		for _, candidate := range []string{"fact", "constraint", "decision", "invariant", "preference"} {
			prefix := candidate + ":"
			if strings.HasPrefix(strings.ToLower(line), prefix) {
				kind, line = candidate, strings.TrimSpace(line[len(prefix):])
				break
			}
		}
		if line == "" {
			continue
		}
		claims = append(claims, parsedClaim{Kind: kind, Content: line, Confidence: 0.8, Importance: 0.6})
	}
	return claims
}

func memoryFingerprint(kind, content string) string {
	return digest([]byte(strings.ToLower(strings.TrimSpace(kind)) + "\x00" + normalizeMemoryText(content)))
}

func normalizeMemoryText(content string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(content))), " ")
}

func memorySubject(content string) string {
	normalized := normalizeMemoryText(content)
	for _, marker := range []string{"=", " must ", " should ", " can ", " is ", " are ", " -> "} {
		if index := strings.Index(normalized, marker); index > 0 {
			return strings.TrimSpace(normalized[:index])
		}
	}
	words := strings.Fields(normalized)
	if len(words) > 3 {
		words = words[:3]
	}
	return strings.Join(words, " ")
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (s *Store) ListMemories(sessionID string) ([]MemoryItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return nil, ErrNotFound
	}
	items := make([]MemoryItem, 0, len(state.Memories)+len(s.projectMemories[state.Session.ProjectID]))
	for i := range state.Memories {
		if memoryActive(state.Memories[i], time.Now().UTC()) {
			items = append(items, cloneMemory(state.Memories[i]))
		}
	}
	if state.Session.ProjectID != "" {
		for _, memory := range s.projectMemories[state.Session.ProjectID] {
			if memoryActive(memory, time.Now().UTC()) {
				items = append(items, cloneMemory(memory))
			}
		}
	}
	return activeMemoryFrontier(items, time.Now().UTC()), nil
}

func (s *Store) ListArchives(sessionID string) ([]ArchiveWindow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]ArchiveWindow(nil), state.Archives...), nil
}

func (s *Store) ContextStatus(sessionID string) (ContextStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return ContextStatus{}, ErrNotFound
	}
	archived := 0
	for _, archive := range state.Archives {
		archived += int(archive.EndSequence - archive.StartSequence + 1)
	}
	var tokens int64
	for _, turn := range state.Turns {
		if turn.Sequence > 0 && !turnArchived(state.Archives, turn.Sequence) {
			tokens += int64(len([]rune(turn.Content))+3) / 4
		}
	}
	memoryItems := append([]MemoryItem(nil), state.Memories...)
	memoryItems = append(memoryItems, s.projectMemories[state.Session.ProjectID]...)
	memoryCount := len(activeMemoryFrontier(memoryItems, time.Now().UTC()))
	status := ContextStatus{SessionID: sessionID, ContextVersion: state.Session.ContextVersion, TurnCount: len(state.Turns), ArchivedTurns: archived, TokenEstimate: tokens, BudgetTokens: state.Session.BudgetTokens, ArchiveCount: len(state.Archives), MemoryCount: memoryCount, CheckpointCount: len(state.Checkpoints), AutoCompaction: state.Session.AutoCompaction, CompactionThreshold: state.Session.CompactionThreshold, CompactionRetainTail: state.Session.CompactionRetainTail, TranscriptDurable: s.transcriptPath != ""}
	if len(state.Turns) > 0 {
		status.LastTurnHash = state.Turns[len(state.Turns)-1].Hash
	}
	return status, nil
}

// VerifyTranscript checks the durable append-only log against the in-memory
// snapshot, including turn hash and previous-hash continuity. It is safe to
// run during a health probe and never mutates state.
func (s *Store) VerifyTranscript(sessionID string) (TranscriptIntegrity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.sessions[sessionID]
	result := TranscriptIntegrity{SessionID: sessionID, CheckedAt: time.Now().UTC()}
	if !ok {
		return result, ErrNotFound
	}
	result.TurnCount, result.ArchiveCount = len(state.Turns), len(state.Archives)
	if err := validateSessionState(state); err != nil {
		result.Error = err.Error()
		return result, err
	}
	if s.transcriptPath == "" {
		result.Valid = true
		return result, nil
	}
	records, exists, err := readTranscript(s.transcriptPath)
	if err != nil {
		result.Error = err.Error()
		return result, err
	}
	if !exists {
		result.Error = "append-only transcript is missing"
		return result, fmt.Errorf("%w: %s", ErrCorrupt, result.Error)
	}
	logged := records[sessionID]
	if len(logged) != len(state.Turns) {
		result.Error = fmt.Sprintf("transcript turn count %d does not match snapshot %d", len(logged), len(state.Turns))
		return result, fmt.Errorf("%w: %s", ErrCorrupt, result.Error)
	}
	for i, turn := range state.Turns {
		if turn.Hash != logged[i].Hash || turn.PrevHash != logged[i].PrevHash || turnDigest(turn) != turnDigest(logged[i]) {
			result.Error = fmt.Sprintf("transcript diverges at sequence %d", i+1)
			return result, fmt.Errorf("%w: %s", ErrCorrupt, result.Error)
		}
	}
	result.Valid = true
	return result, nil
}

// VerifyCompaction extends the transcript probe with an archive recall probe:
// every replacement summary must be present in a generously bounded compiled
// context. A summary that cannot be recalled is a failed quality gate even if
// its cryptographic digest is intact.
func (s *Store) VerifyCompaction(sessionID string) (TranscriptIntegrity, error) {
	result, err := s.VerifyTranscript(sessionID)
	if err != nil {
		return result, err
	}
	compiled, compileErr := s.Compile(sessionID, 1<<60)
	if compileErr != nil {
		result.Error = compileErr.Error()
		return result, compileErr
	}
	s.mu.RLock()
	state := s.sessions[sessionID]
	for _, archive := range state.Archives {
		if !strings.Contains(compiled, archive.Summary) {
			result.Error = fmt.Sprintf("archive %s summary was not recalled", archive.ID)
			s.mu.RUnlock()
			return result, fmt.Errorf("%w: %s", ErrCorrupt, result.Error)
		}
	}
	s.mu.RUnlock()
	result.RecallVerified = true
	return result, nil
}

func (s *Store) Recover(sessionID string, now time.Time) (Recovery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return Recovery{}, ErrNotFound
	}
	if err := validateSessionState(state); err != nil {
		return Recovery{}, err
	}
	previous := state
	state.Leases = append([]Lease(nil), state.Leases...)
	state.Outbox = append([]OutboxEvent(nil), state.Outbox...)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var latest *Checkpoint
	if len(state.Checkpoints) > 0 {
		item := cloneCheckpoint(state.Checkpoints[len(state.Checkpoints)-1])
		latest = &item
	}
	expired := make([]Lease, 0)
	dirty := false
	for i := range state.Leases {
		lease := &state.Leases[i]
		if lease.State == "held" && !lease.ExpiresAt.After(now) {
			lease.State = "expired"
			lease.UpdatedAt = now
			expired = append(expired, cloneLease(*lease))
			dirty = true
		}
	}
	for i := range state.Outbox {
		event := &state.Outbox[i]
		if event.State == "processing" && !event.LeaseUntil.After(now) {
			event.State, event.Owner = "pending", ""
			event.LeaseUntil = time.Time{}
			event.NextAttemptAt = now
			dirty = true
		}
	}
	if dirty {
		state.Session.UpdatedAt = now
		s.sessions[sessionID] = state
		if err := s.persistLocked(); err != nil {
			s.sessions[sessionID] = previous
			return Recovery{}, fmt.Errorf("persist lease recovery: %w", err)
		}
	}
	pending := make([]OutboxEvent, 0)
	for _, event := range state.Outbox {
		if event.State == "pending" || event.State == "processing" {
			pending = append(pending, cloneOutbox(event))
		}
	}
	return Recovery{Session: state.Session, LatestCheckpoint: latest, PendingEffects: pending, ExpiredLeases: expired}, nil
}

func (s *Store) AcquireLease(sessionID, key, owner string, ttl time.Duration, now time.Time) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return Lease{}, ErrNotFound
	}
	previousState := state
	state.Leases = append([]Lease(nil), state.Leases...)
	if strings.TrimSpace(key) == "" || strings.TrimSpace(owner) == "" || ttl <= 0 {
		return Lease{}, errors.New("lease key, owner and positive ttl are required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for i := range state.Leases {
		lease := &state.Leases[i]
		if lease.Key != key || lease.State == "released" {
			continue
		}
		if lease.ExpiresAt.After(now) && lease.Owner != owner {
			return Lease{}, ErrLeaseBusy
		}
		lease.Owner, lease.State, lease.Version = owner, "held", lease.Version+1
		lease.ExpiresAt, lease.UpdatedAt = now.Add(ttl), now
		s.sessions[sessionID] = state
		if err := s.persistLocked(); err != nil {
			s.sessions[sessionID] = previousState
			return Lease{}, fmt.Errorf("persist lease renewal: %w", err)
		}
		return cloneLease(*lease), nil
	}
	lease := Lease{ID: domain.NewID(), SessionID: sessionID, Key: key, Owner: owner, State: "held", Version: 1, ExpiresAt: now.Add(ttl), CreatedAt: now, UpdatedAt: now}
	state.Leases = append(state.Leases, lease)
	state.Session.UpdatedAt = now
	s.sessions[sessionID] = state
	if err := s.persistLocked(); err != nil {
		state.Leases = state.Leases[:len(state.Leases)-1]
		s.sessions[sessionID] = state
		return Lease{}, fmt.Errorf("persist lease: %w", err)
	}
	return cloneLease(lease), nil
}

func (s *Store) ReleaseLease(sessionID, leaseID, owner string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	previousState := state
	state.Leases = append([]Lease(nil), state.Leases...)
	for i := range state.Leases {
		lease := &state.Leases[i]
		if lease.ID != leaseID {
			continue
		}
		if lease.Owner != owner || lease.State != "held" {
			return ErrLeaseLost
		}
		if now.IsZero() {
			now = time.Now().UTC()
		}
		lease.State, lease.ExpiresAt, lease.UpdatedAt, lease.Version = "released", time.Time{}, now, lease.Version+1
		state.Session.UpdatedAt = now
		s.sessions[sessionID] = state
		if err := s.persistLocked(); err != nil {
			s.sessions[sessionID] = previousState
			return fmt.Errorf("persist lease release: %w", err)
		}
		return nil
	}
	return ErrNotFound
}

// HeartbeatLease renews one specific lease and fails closed when ownership was
// lost. AcquireLease remains useful for initial acquisition; long-running
// workers should use this method so a stale owner cannot renew by key alone.
func (s *Store) HeartbeatLease(sessionID, leaseID, owner string, ttl time.Duration, now time.Time) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return Lease{}, ErrNotFound
	}
	if strings.TrimSpace(leaseID) == "" || strings.TrimSpace(owner) == "" || ttl <= 0 {
		return Lease{}, errors.New("lease id, owner and positive ttl are required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for i := range state.Leases {
		lease := &state.Leases[i]
		if lease.ID != leaseID {
			continue
		}
		if lease.Owner != owner || lease.State != "held" || !lease.ExpiresAt.After(now) {
			return Lease{}, ErrLeaseLost
		}
		previous := state
		state.Leases = append([]Lease(nil), state.Leases...)
		lease = &state.Leases[i]
		lease.Version++
		lease.ExpiresAt, lease.UpdatedAt = now.Add(ttl), now
		state.Session.UpdatedAt = now
		s.sessions[sessionID] = state
		if err := s.persistLocked(); err != nil {
			s.sessions[sessionID] = previous
			return Lease{}, fmt.Errorf("persist lease heartbeat: %w", err)
		}
		return cloneLease(*lease), nil
	}
	return Lease{}, ErrNotFound
}

func (s *Store) EnqueueOutbox(sessionID, key string, payload any) (OutboxEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return OutboxEvent{}, ErrNotFound
	}
	if strings.TrimSpace(key) == "" {
		return OutboxEvent{}, errors.New("outbox idempotency key is required")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return OutboxEvent{}, fmt.Errorf("encode outbox payload: %w", err)
	}
	for _, existing := range state.Outbox {
		if existing.IdempotencyKey == key {
			if string(existing.Payload) != string(data) {
				return OutboxEvent{}, ErrConflict
			}
			return cloneOutbox(existing), nil
		}
	}
	now := time.Now().UTC()
	event := OutboxEvent{ID: domain.NewID(), SessionID: sessionID, IdempotencyKey: key, Payload: append([]byte(nil), data...), State: "pending", NextAttemptAt: now, CreatedAt: now}
	state.Outbox = append(state.Outbox, event)
	state.Session.UpdatedAt = now
	s.sessions[sessionID] = state
	if err := s.persistLocked(); err != nil {
		state.Outbox = state.Outbox[:len(state.Outbox)-1]
		s.sessions[sessionID] = state
		return OutboxEvent{}, fmt.Errorf("persist outbox event: %w", err)
	}
	return cloneOutbox(event), nil
}

// EnqueueAndClaimOutbox atomically records an idempotent side effect and
// claims it for the caller. Keeping the insert/find and claim under one lock
// closes the handoff window in which a recovery worker could publish a newly
// enqueued effect before the originating request claimed it. The boolean is
// true only when this call owns the claim; published records are returned for
// idempotent replay and active records return ErrLeaseBusy.
func (s *Store) EnqueueAndClaimOutbox(sessionID, key string, payload any, owner string, ttl time.Duration, now time.Time) (OutboxEvent, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return OutboxEvent{}, false, ErrNotFound
	}
	if strings.TrimSpace(key) == "" {
		return OutboxEvent{}, false, errors.New("outbox idempotency key is required")
	}
	if strings.TrimSpace(owner) == "" || ttl <= 0 {
		return OutboxEvent{}, false, errors.New("outbox owner and positive ttl are required")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return OutboxEvent{}, false, fmt.Errorf("encode outbox payload: %w", err)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	previousState := state
	state.Outbox = append([]OutboxEvent(nil), state.Outbox...)
	for i := range state.Outbox {
		event := &state.Outbox[i]
		if event.IdempotencyKey != key {
			continue
		}
		if string(event.Payload) != string(data) {
			return OutboxEvent{}, false, ErrConflict
		}
		if event.State == "published" {
			return cloneOutbox(*event), false, nil
		}
		if event.State == "processing" && event.LeaseUntil.After(now) {
			return cloneOutbox(*event), false, ErrLeaseBusy
		}
		if event.State != "pending" && event.State != "processing" {
			return cloneOutbox(*event), false, ErrConflict
		}
		if event.State == "pending" && event.NextAttemptAt.After(now) {
			return cloneOutbox(*event), false, ErrLeaseBusy
		}
		event.State, event.Owner, event.LeaseUntil = "processing", owner, now.Add(ttl)
		event.Attempts++
		state.Session.UpdatedAt = now
		s.sessions[sessionID] = state
		if err := s.persistLocked(); err != nil {
			s.sessions[sessionID] = previousState
			return OutboxEvent{}, false, fmt.Errorf("persist outbox claim: %w", err)
		}
		return cloneOutbox(*event), true, nil
	}

	event := OutboxEvent{ID: domain.NewID(), SessionID: sessionID, IdempotencyKey: key, Payload: append([]byte(nil), data...), State: "processing", Attempts: 1, Owner: owner, LeaseUntil: now.Add(ttl), NextAttemptAt: now, CreatedAt: now}
	state.Outbox = append(state.Outbox, event)
	state.Session.UpdatedAt = now
	s.sessions[sessionID] = state
	if err := s.persistLocked(); err != nil {
		state.Outbox = state.Outbox[:len(state.Outbox)-1]
		s.sessions[sessionID] = state
		return OutboxEvent{}, false, fmt.Errorf("persist outbox claim: %w", err)
	}
	return cloneOutbox(event), true, nil
}

func (s *Store) ClaimOutbox(sessionID, owner string, limit int, ttl time.Duration, now time.Time) ([]OutboxEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return nil, ErrNotFound
	}
	previousState := state
	state.Outbox = append([]OutboxEvent(nil), state.Outbox...)
	if strings.TrimSpace(owner) == "" || ttl <= 0 {
		return nil, errors.New("outbox owner and positive ttl are required")
	}
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	items := make([]OutboxEvent, 0, limit)
	for i := range state.Outbox {
		event := &state.Outbox[i]
		if len(items) == limit || event.State != "pending" || event.NextAttemptAt.After(now) {
			continue
		}
		event.State, event.Owner, event.LeaseUntil = "processing", owner, now.Add(ttl)
		event.Attempts++
		items = append(items, cloneOutbox(*event))
	}
	if len(items) == 0 {
		return items, nil
	}
	state.Session.UpdatedAt = now
	s.sessions[sessionID] = state
	if err := s.persistLocked(); err != nil {
		s.sessions[sessionID] = previousState
		return nil, fmt.Errorf("persist outbox claim: %w", err)
	}
	return items, nil
}

func (s *Store) AckOutbox(sessionID, eventID, owner string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	previousState := state
	state.Outbox = append([]OutboxEvent(nil), state.Outbox...)
	for i := range state.Outbox {
		event := &state.Outbox[i]
		if event.ID != eventID {
			continue
		}
		if event.State == "published" {
			return nil
		}
		if event.State != "processing" || event.Owner != owner {
			return ErrLeaseLost
		}
		if now.IsZero() {
			now = time.Now().UTC()
		}
		event.State, event.Owner, event.LeaseUntil, event.PublishedAt = "published", "", time.Time{}, &now
		state.Session.UpdatedAt = now
		s.sessions[sessionID] = state
		if err := s.persistLocked(); err != nil {
			s.sessions[sessionID] = previousState
			return fmt.Errorf("persist outbox acknowledgement: %w", err)
		}
		return nil
	}
	return ErrNotFound
}

// CompleteOutbox marks an intent published when the caller performed the side
// effect synchronously without first claiming the record. It is the local
// fast path for API requests; recovery workers always use claim + AckOutbox.
func (s *Store) CompleteOutbox(sessionID, eventID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	previousState := state
	state.Outbox = append([]OutboxEvent(nil), state.Outbox...)
	for i := range state.Outbox {
		event := &state.Outbox[i]
		if event.ID != eventID {
			continue
		}
		if event.State == "published" {
			return nil
		}
		if event.State != "pending" {
			return ErrLeaseLost
		}
		if now.IsZero() {
			now = time.Now().UTC()
		}
		event.State, event.Owner, event.LeaseUntil, event.PublishedAt = "published", "", time.Time{}, &now
		state.Session.UpdatedAt = now
		s.sessions[sessionID] = state
		if err := s.persistLocked(); err != nil {
			s.sessions[sessionID] = previousState
			return fmt.Errorf("persist outbox completion: %w", err)
		}
		return nil
	}
	return ErrNotFound
}

func (s *Store) NackOutbox(sessionID, eventID, owner string, retryAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return ErrNotFound
	}
	previousState := state
	state.Outbox = append([]OutboxEvent(nil), state.Outbox...)
	for i := range state.Outbox {
		event := &state.Outbox[i]
		if event.ID != eventID {
			continue
		}
		if event.State != "processing" || event.Owner != owner {
			return ErrLeaseLost
		}
		if retryAt.IsZero() {
			retryAt = time.Now().UTC().Add(time.Second)
		}
		event.State, event.Owner, event.LeaseUntil, event.NextAttemptAt = "pending", "", time.Time{}, retryAt.UTC()
		s.sessions[sessionID] = state
		if err := s.persistLocked(); err != nil {
			s.sessions[sessionID] = previousState
			return fmt.Errorf("persist outbox retry: %w", err)
		}
		return nil
	}
	return ErrNotFound
}

func validateSessionState(state sessionState) error {
	if state.Session.ID == "" || state.Session.TenantID == "" || state.Session.WorkspaceID == "" {
		return ErrCorrupt
	}
	var previous string
	for i, turn := range state.Turns {
		if turn.SessionID != state.Session.ID || turn.Sequence != int64(i+1) || turn.Hash == "" || turn.PrevHash != previous || hashTurn(turn) != turn.Hash {
			return fmt.Errorf("%w: transcript chain at sequence %d", ErrCorrupt, i+1)
		}
		previous = turn.Hash
	}
	var previousCheckpointHash string
	toolCheckpointPhases := map[string]CheckpointPhase{}
	for i, checkpoint := range state.Checkpoints {
		if checkpoint.SessionID != state.Session.ID || checkpoint.Hash == "" || hashCheckpoint(checkpoint) != checkpoint.Hash || checkpoint.TurnSequence > int64(len(state.Turns)) || (i > 0 && checkpoint.TurnSequence < state.Checkpoints[i-1].TurnSequence) || checkpoint.PrevHash != previousCheckpointHash {
			return fmt.Errorf("%w: checkpoint %s", ErrCorrupt, checkpoint.ID)
		}
		if checkpoint.EventHash != "" && !hasTurnHash(state.Turns, checkpoint.EventHash) {
			return fmt.Errorf("%w: checkpoint event hash %s", ErrCorrupt, checkpoint.EventHash)
		}
		if checkpoint.ToolCallID != "" {
			switch checkpoint.Phase {
			case CheckpointToolBefore:
				if _, exists := toolCheckpointPhases[checkpoint.ToolCallID]; exists {
					return fmt.Errorf("%w: duplicate tool-before checkpoint %s", ErrCorrupt, checkpoint.ID)
				}
				toolCheckpointPhases[checkpoint.ToolCallID] = checkpoint.Phase
			case CheckpointToolAfter:
				if toolCheckpointPhases[checkpoint.ToolCallID] != CheckpointToolBefore {
					return fmt.Errorf("%w: tool-after checkpoint %s has no before phase", ErrCorrupt, checkpoint.ID)
				}
				toolCheckpointPhases[checkpoint.ToolCallID] = checkpoint.Phase
			}
		}
		previousCheckpointHash = checkpoint.Hash
	}
	for i, archive := range state.Archives {
		if archive.SessionID != state.Session.ID || archive.StartSequence < 1 || archive.EndSequence < archive.StartSequence || archive.EndSequence > int64(len(state.Turns)) {
			return fmt.Errorf("%w: archive %s", ErrCorrupt, archive.ID)
		}
		var source strings.Builder
		for _, turn := range state.Turns {
			if turn.Sequence >= archive.StartSequence && turn.Sequence <= archive.EndSequence {
				data, _ := json.Marshal(turn)
				source.Write(data)
				source.WriteByte('\n')
			}
		}
		if archive.SourceHash != digest([]byte(source.String())) || archive.ReplacementHash != digest([]byte(strings.TrimSpace(archive.Summary))) {
			return fmt.Errorf("%w: archive digest %s", ErrCorrupt, archive.ID)
		}
		if i > 0 && archive.ParentArchiveID != state.Archives[i-1].ID {
			return fmt.Errorf("%w: archive parent %s", ErrCorrupt, archive.ID)
		}
		for j := 0; j < i; j++ {
			other := state.Archives[j]
			if archive.StartSequence <= other.EndSequence && archive.EndSequence >= other.StartSequence {
				return fmt.Errorf("%w: overlapping archive %s", ErrCorrupt, archive.ID)
			}
		}
	}
	for _, memory := range state.Memories {
		if memory.ID == "" || memory.SessionID != state.Session.ID || memory.Scope == "project" || strings.TrimSpace(memory.Content) == "" || memory.Confidence < 0 || memory.Confidence > 1 || memory.Importance < 0 || memory.Importance > 1 || memory.Fingerprint != "" && memory.Fingerprint != memoryFingerprint(memory.Kind, memory.Content) {
			return fmt.Errorf("%w: memory %s", ErrCorrupt, memory.ID)
		}
		for _, sourceID := range memory.SourceIDs {
			if !hasTurnID(state.Turns, sourceID) {
				return fmt.Errorf("%w: memory source %s", ErrCorrupt, sourceID)
			}
		}
	}
	return nil
}

func migrateCheckpointChains(sessions map[string]sessionState) error {
	for sessionID, state := range sessions {
		previous := ""
		for i := range state.Checkpoints {
			checkpoint := &state.Checkpoints[i]
			if checkpoint.Hash == "" || hashCheckpoint(*checkpoint) != checkpoint.Hash {
				return fmt.Errorf("%w: checkpoint %s", ErrCorrupt, checkpoint.ID)
			}
			if checkpoint.PrevHash == "" && i > 0 {
				checkpoint.PrevHash = previous
				checkpoint.Hash = hashCheckpoint(*checkpoint)
			}
			previous = checkpoint.Hash
		}
		sessions[sessionID] = state
	}
	return nil
}

func validRole(role Role) bool {
	return role == RoleSystem || role == RoleUser || role == RoleAssistant || role == RoleTool
}

func hashTurn(turn Turn) string {
	turn.Hash = ""
	data, _ := json.Marshal(turn)
	return digest(data)
}

func turnDigest(turn Turn) string {
	turn.ID, turn.Sequence, turn.PrevHash, turn.Hash, turn.CreatedAt = "", 0, "", "", time.Time{}
	return hashTurn(turn)
}

func hashCheckpoint(checkpoint Checkpoint) string {
	checkpoint.Hash = ""
	data, _ := json.Marshal(checkpoint)
	return digest(data)
}

func digest(data []byte) string {
	hash := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(hash[:])
}

func hasTurnHash(turns []Turn, value string) bool {
	for _, turn := range turns {
		if turn.Hash == value {
			return true
		}
	}
	return false
}

func hasTurnID(turns []Turn, value string) bool {
	for _, turn := range turns {
		if turn.ID == value {
			return true
		}
	}
	return false
}

func hasMemoryID(items []MemoryItem, value string) bool {
	for _, item := range items {
		if item.ID == value {
			return true
		}
	}
	return false
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func cloneTurn(turn Turn) Turn {
	turn.Metadata = cloneStringMap(turn.Metadata)
	return turn
}

func cloneCheckpoint(checkpoint Checkpoint) Checkpoint {
	checkpoint.OutboxIDs = append([]string(nil), checkpoint.OutboxIDs...)
	checkpoint.LeaseIDs = append([]string(nil), checkpoint.LeaseIDs...)
	return checkpoint
}

func cloneMemory(item MemoryItem) MemoryItem {
	item.SourceIDs = append([]string(nil), item.SourceIDs...)
	item.Supersedes = append([]string(nil), item.Supersedes...)
	return item
}

func memoryActive(item MemoryItem, now time.Time) bool {
	return item.ExpiresAt == nil || item.ExpiresAt.After(now)
}

func memoryScopeRank(scope string) int {
	switch scope {
	case "working":
		return 0
	case "session":
		return 1
	case "project":
		return 2
	default:
		return 3
	}
}

// sortMemories gives deterministic, non-semantic prioritization to the
// compiler. Pinned constraints and high-importance working/session facts win;
// project facts remain available as a lower-priority long-term tier.
func sortMemories(items []MemoryItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Pinned != items[j].Pinned {
			return items[i].Pinned
		}
		if items[i].Importance != items[j].Importance {
			return items[i].Importance > items[j].Importance
		}
		if memoryScopeRank(items[i].Scope) != memoryScopeRank(items[j].Scope) {
			return memoryScopeRank(items[i].Scope) < memoryScopeRank(items[j].Scope)
		}
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
}

func activeMemoryFrontier(items []MemoryItem, now time.Time) []MemoryItem {
	superseded := make(map[string]struct{}, len(items))
	for _, item := range items {
		for _, id := range item.Supersedes {
			superseded[id] = struct{}{}
		}
	}
	active := make([]MemoryItem, 0, len(items))
	for _, item := range items {
		if _, hidden := superseded[item.ID]; hidden || !memoryActive(item, now) {
			continue
		}
		active = append(active, cloneMemory(item))
	}
	sortMemories(active)
	return active
}

func cloneLease(lease Lease) Lease { return lease }

func cloneOutbox(event OutboxEvent) OutboxEvent {
	event.Payload = append([]byte(nil), event.Payload...)
	return event
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

// Compile returns a bounded context view while retaining the complete
// transcript and archive records for audit/replay. Archived windows are
// replaced by their verified summaries; the newest retained tail remains raw.
func (s *Store) Compile(sessionID string, maxTokens int64) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return "", ErrNotFound
	}
	if maxTokens <= 0 {
		maxTokens = state.Session.BudgetTokens
	}
	if maxTokens <= 0 {
		maxTokens = 1
	}
	memoryItems := make([]MemoryItem, 0, len(state.Memories)+len(s.projectMemories[state.Session.ProjectID]))
	now := time.Now().UTC()
	for _, memory := range state.Memories {
		if memoryActive(memory, now) {
			memoryItems = append(memoryItems, cloneMemory(memory))
		}
	}
	if state.Session.ProjectID != "" {
		for _, memory := range s.projectMemories[state.Session.ProjectID] {
			if memoryActive(memory, now) {
				memoryItems = append(memoryItems, cloneMemory(memory))
			}
		}
	}
	sortMemories(memoryItems)
	prefixLines := make([]string, 0, len(state.Archives)+len(memoryItems))
	appendLine := func(line string) (string, bool) {
		line = strings.TrimSuffix(line, "\n") + "\n"
		remaining := maxTokens - estimateTokens(strings.Join(prefixLines, ""))
		if remaining <= 0 {
			return "", false
		}
		if estimateTokens(line) > remaining {
			// Keep a bounded prefix with an explicit marker when a single summary
			// or memory item is larger than the remaining context budget.
			maxRunes := int(remaining*4) - 4
			if maxRunes <= 0 {
				return "", false
			}
			runes := []rune(strings.TrimSuffix(line, "\n"))
			if len(runes) > maxRunes {
				runes = runes[:maxRunes]
			}
			line = string(runes) + "...\n"
			if estimateTokens(line) > remaining {
				return "", false
			}
		}
		prefixLines = append(prefixLines, line)
		return line, true
	}
	for _, archive := range state.Archives {
		if _, ok := appendLine(fmt.Sprintf("[archive %s] %s", archive.ID, archive.Summary)); !ok {
			break
		}
	}
	// Memory is append-only, but a newer fact can supersede an older one. Keep
	// the full ledger for audit while compiling only the active frontier; this
	// prevents stale decisions from consuming the model budget after repair.
	superseded := make(map[string]struct{})
	for _, memory := range memoryItems {
		for _, id := range memory.Supersedes {
			superseded[id] = struct{}{}
		}
	}
	for _, memory := range memoryItems {
		if _, hidden := superseded[memory.ID]; hidden {
			continue
		}
		if _, ok := appendLine(fmt.Sprintf("[memory %s %s] %s", memory.Kind, memory.ID, memory.Content)); !ok {
			break
		}
	}
	archived := func(sequence int64) bool {
		for _, archive := range state.Archives {
			if sequence >= archive.StartSequence && sequence <= archive.EndSequence {
				return true
			}
		}
		return false
	}
	// Preserve the newest raw turns when archive summaries consume the budget.
	// Select backwards, then write the selected tail chronologically.
	selected := make([]string, 0, len(state.Turns))
	for i := len(state.Turns) - 1; i >= 0; i-- {
		turn := state.Turns[i]
		if archived(turn.Sequence) {
			continue
		}
		line := fmt.Sprintf("%s: %s", turn.Role, turn.Content)
		actual, ok := appendLine(line)
		if !ok {
			break
		}
		selected = append(selected, actual)
	}
	if len(selected) > 0 {
		// Remove the backwards-written turn tail and put it back in transcript
		// order. The prefix is stable because archive/memory lines are unique in
		// position even when their text happens to repeat.
		prefixLines = prefixLines[:len(prefixLines)-len(selected)]
		for i := len(selected) - 1; i >= 0; i-- {
			prefixLines = append(prefixLines, selected[i])
		}
	}
	return strings.TrimSpace(strings.Join(prefixLines, "")), nil
}

func estimateTokens(value string) int64 {
	runes := len([]rune(value))
	if runes == 0 {
		return 0
	}
	return int64((runes + 3) / 4)
}

// SortArchives is useful to external adapters that merge records from a
// database cursor. It is intentionally deterministic and does not mutate the
// store's internal state.
func SortArchives(items []ArchiveWindow) []ArchiveWindow {
	output := append([]ArchiveWindow(nil), items...)
	sort.SliceStable(output, func(i, j int) bool { return output[i].StartSequence < output[j].StartSequence })
	return output
}
