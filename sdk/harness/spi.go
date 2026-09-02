// Package harness defines the versioned extension contract for durable
// sessions. Implementations may use PostgreSQL, SQLite, or another durable
// store, but must preserve append-only transcript and idempotent side effects.
package harness

import (
	"context"
	"time"
)

const ProtocolVersion = "adro.harness.v1"

type Session struct {
	ID, TenantID, WorkspaceID, ProjectID string
	BudgetTokens                         int64
	ContextVersion                       int64
}

type MemoryItem struct {
	ID, SessionID, Scope, ProjectID, Kind, Content string
	Fingerprint                                    string
	SourceIDs, Supersedes                          []string
	Confidence, Importance                         float64
	Status                                         string
	QualityScore                                   float64
	EvidenceHash                                   string
	Pinned                                         bool
	ExpiresAt                                      *time.Time
	CreatedAt                                      time.Time
}

type Turn struct {
	ID, SessionID, AttemptID         string
	Sequence                         int64
	Role, Content                    string
	IdempotencyKey                   string
	PrevHash, Hash                   string
	CreatedAt                        time.Time
	ToolName, ToolCallID, ToolStatus string
}

type Checkpoint struct {
	ID, SessionID, Phase, EventHash, PrevHash, ToolCallID string
	TurnSequence, ContextVersion                          int64
	OutboxIDs, LeaseIDs                                   []string
	CreatedAt                                             time.Time
}

type ContextBlock struct {
	ID, Kind, Source, Content, Hash string
	Policy, Trust, SelectionReason  string
	TokenEstimate                   int64
	Mandatory                       bool
	Metadata                        map[string]string
}

type ContextManifest struct {
	SessionID, Digest, ParentDigest string
	Version, TokenBudget            int64
	TokenEstimate                   int64
	Blocks                          []ContextBlock
	CreatedAt                       time.Time
}

// ContextEnvelope is the immutable provider packet. SelectionDigest and
// ReplayKey make the exact block selection independently verifiable on retry.
type ContextEnvelope struct {
	Manifest        ContextManifest `json:"manifest"`
	SelectionDigest string          `json:"selection_digest"`
	ReplayKey       string          `json:"replay_key"`
}

type ArchiveWindow struct {
	ID, SessionID, SourceHash, ReplacementHash string
	StartSequence, EndSequence                 int64
	Summary                                    string
	ParentArchiveID                            string
}

type Lease struct {
	ID, SessionID, Key, Owner, State string
	Version                          int64
	ExpiresAt                        time.Time
}

type OutboxEvent struct {
	ID, SessionID, IdempotencyKey, State, Owner string
	Payload                                     []byte
	Attempts                                    int
	LeaseUntil, NextAttemptAt                   time.Time
	PublishedAt                                 *time.Time
}

// Store is the minimum durable session contract. AppendTurn and
// SaveCheckpoint must be atomic with respect to their own revision and reject
// stale or forged hashes. A provider is never allowed to replace this state.
type Store interface {
	CreateSession(context.Context, Session) (Session, error)
	GetSession(context.Context, string) (Session, error)
	AppendTurn(context.Context, string, Turn) (Turn, error)
	ListTurns(context.Context, string, int64, int) ([]Turn, int64, error)
	SaveCheckpoint(context.Context, string, Checkpoint) (Checkpoint, error)
	Recover(context.Context, string, time.Time) (Recovery, error)
	Compact(context.Context, string, CompactRequest) (ArchiveWindow, error)
}

type TranscriptIntegrity struct {
	SessionID      string
	TurnCount      int
	ArchiveCount   int
	Valid          bool
	RecallVerified bool
	CheckedAt      time.Time
	Error          string
}

type MemoryReduction struct {
	Added       []MemoryItem
	Superseded  []string
	Conflicts   []string
	SourceTurns []string
}

// IntegrityStore is optional for adapters that expose append-only transcript
// and compaction recall probes. It is separate from Store to preserve binary
// compatibility with existing providers.
type IntegrityStore interface {
	VerifyTranscript(context.Context, string) (TranscriptIntegrity, error)
	VerifyCompaction(context.Context, string) (TranscriptIntegrity, error)
	ReduceMemories(context.Context, string, []string, string) (MemoryReduction, error)
}

// ManifestCompiler is optional but recommended. The manifest is immutable for
// a dispatch attempt and its digest is the lineage key used by retries.
type ManifestCompiler interface {
	CompileManifest(context.Context, string, int64) (ContextManifest, error)
}

// EnvelopeCompiler is the strict variant used by providers that consume typed
// context packets. It is optional to preserve compatibility with older
// adapters while making the stronger contract discoverable.
type EnvelopeCompiler interface {
	CompileEnvelope(context.Context, string, int64) (ContextEnvelope, error)
}

// MemoryStore is optional for providers that want to persist structured
// working/session/project facts. Implementations must keep source citations
// and supersession deterministic; semantic or vector indexes are not required.
type MemoryStore interface {
	AddMemory(context.Context, MemoryItem) (MemoryItem, error)
	ListMemories(context.Context, string) ([]MemoryItem, error)
}

type Recovery struct {
	Session          Session
	LatestCheckpoint *Checkpoint
	PendingEffects   []OutboxEvent
	ExpiredLeases    []Lease
}

type CompactRequest struct {
	StartSequence, EndSequence int64
	Summary                    string
	RetainedTail               int
	Reason                     string
}

type LeaseStore interface {
	AcquireLease(context.Context, string, string, string, time.Duration) (Lease, error)
	ReleaseLease(context.Context, string, string, string) error
}

type OutboxStore interface {
	EnqueueOutbox(context.Context, string, string, []byte) (OutboxEvent, error)
	ClaimOutbox(context.Context, string, string, int, time.Duration) ([]OutboxEvent, error)
	AckOutbox(context.Context, string, string, string) error
	NackOutbox(context.Context, string, string, string, time.Time) error
}

// AtomicOutboxStore is the preferred contract for request paths that enqueue
// and immediately execute an effect. Implementations must make the
// idempotent lookup and lease claim one atomic operation; recovery workers may
// continue to use OutboxStore for records left pending by a crashed caller.
type AtomicOutboxStore interface {
	OutboxStore
	EnqueueAndClaimOutbox(context.Context, string, string, []byte, string, time.Duration) (OutboxEvent, bool, error)
}
