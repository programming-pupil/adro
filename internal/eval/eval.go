// Package eval defines a deterministic, versioned runtime evaluation boundary.
// Evaluators consume immutable session bundles and produce structured evidence;
// they cannot mutate the session being evaluated.
package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const BundleSchemaVersion = 1
const EvaluatorSchemaVersion = 1

var (
	ErrInvalid   = errors.New("invalid evaluation input")
	ErrNotFound  = errors.New("evaluation run not found")
	ErrConflict  = errors.New("evaluation run version conflict")
	ErrTerminal  = errors.New("evaluation run is terminal")
	ErrBudget    = errors.New("evaluation budget exhausted")
	ErrEvaluator = errors.New("evaluator failed")
)

type RunState string

const (
	RunQueued    RunState = "queued"
	RunRunning   RunState = "running"
	RunPaused    RunState = "paused"
	RunCompleted RunState = "completed"
	RunFailed    RunState = "failed"
	RunCancelled RunState = "cancelled"
)

type BundleEvent struct {
	Sequence   int64          `json:"sequence"`
	Type       string         `json:"type"`
	Payload    map[string]any `json:"payload,omitempty"`
	EffectID   string         `json:"effect_id,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	Capability string         `json:"capability,omitempty"`
	Attempt    int            `json:"attempt,omitempty"`
	Authorized bool           `json:"authorized,omitempty"`
	Dispatched bool           `json:"dispatched,omitempty"`
	Receipted  bool           `json:"receipted,omitempty"`
}

type SessionBundle struct {
	SchemaVersion int               `json:"schema_version"`
	SessionID     string            `json:"session_id"`
	TenantID      string            `json:"tenant_id"`
	WorkspaceID   string            `json:"workspace_id"`
	SourceDigest  string            `json:"source_digest"`
	Redacted      bool              `json:"redacted"`
	Events        []BundleEvent     `json:"events"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Digest        string            `json:"digest"`
}

func (b SessionBundle) Validate() error {
	if b.SchemaVersion != BundleSchemaVersion || strings.TrimSpace(b.SessionID) == "" || strings.TrimSpace(b.TenantID) == "" || strings.TrimSpace(b.WorkspaceID) == "" || strings.TrimSpace(b.SourceDigest) == "" || strings.TrimSpace(b.Digest) == "" {
		return ErrInvalid
	}
	last := int64(0)
	for _, event := range b.Events {
		if event.Sequence < 1 || event.Sequence <= last || strings.TrimSpace(event.Type) == "" {
			return fmt.Errorf("%w: event sequence", ErrInvalid)
		}
		last = event.Sequence
	}
	if bundleDigest(b) != b.Digest {
		return fmt.Errorf("%w: bundle digest", ErrInvalid)
	}
	return nil
}

func NewSessionBundle(sessionID, tenantID, workspaceID, sourceDigest string, events []BundleEvent, redacted bool) (SessionBundle, error) {
	bundle := SessionBundle{SchemaVersion: BundleSchemaVersion, SessionID: strings.TrimSpace(sessionID), TenantID: strings.TrimSpace(tenantID), WorkspaceID: strings.TrimSpace(workspaceID), SourceDigest: strings.TrimSpace(sourceDigest), Redacted: redacted, Events: cloneEvents(events)}
	if bundle.SchemaVersion != BundleSchemaVersion || bundle.SessionID == "" || bundle.TenantID == "" || bundle.WorkspaceID == "" || bundle.SourceDigest == "" {
		return SessionBundle{}, ErrInvalid
	}
	sort.Slice(bundle.Events, func(i, j int) bool { return bundle.Events[i].Sequence < bundle.Events[j].Sequence })
	bundle.Digest = bundleDigest(bundle)
	return bundle, bundle.Validate()
}

type FindingSeverity string

const (
	SeverityInfo     FindingSeverity = "info"
	SeverityWarning  FindingSeverity = "warning"
	SeverityCritical FindingSeverity = "critical"
)

type Finding struct {
	RuleID    string            `json:"rule_id"`
	Severity  FindingSeverity   `json:"severity"`
	Message   string            `json:"message"`
	Sequences []int64           `json:"sequences,omitempty"`
	Evidence  map[string]string `json:"evidence,omitempty"`
	Invariant bool              `json:"invariant"`
}

type Result struct {
	SchemaVersion    int            `json:"schema_version"`
	EvaluatorID      string         `json:"evaluator_id"`
	EvaluatorVersion string         `json:"evaluator_version"`
	BundleDigest     string         `json:"bundle_digest"`
	Score            float64        `json:"score"`
	Findings         []Finding      `json:"findings"`
	EvidenceDigest   string         `json:"evidence_digest"`
	MinimalBundle    *SessionBundle `json:"minimal_bundle,omitempty"`
	CostUnits        int64          `json:"cost_units"`
	Duration         time.Duration  `json:"duration"`
}

type Check func(context.Context, SessionBundle) (Finding, error)

type Evaluator interface {
	ID() string
	Version() string
	Evaluate(context.Context, SessionBundle) (Result, error)
}

type RuleEvaluator struct {
	EvaluatorID string
	VersionID   string
	Checks      map[string]Check
}

func (e RuleEvaluator) ID() string      { return e.EvaluatorID }
func (e RuleEvaluator) Version() string { return e.VersionID }

func (e RuleEvaluator) Evaluate(ctx context.Context, bundle SessionBundle) (Result, error) {
	if err := bundle.Validate(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(e.EvaluatorID) == "" || strings.TrimSpace(e.VersionID) == "" || len(e.Checks) == 0 {
		return Result{}, ErrInvalid
	}
	if ctx == nil {
		ctx = context.Background()
	}
	keys := make([]string, 0, len(e.Checks))
	for key := range e.Checks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := Result{SchemaVersion: EvaluatorSchemaVersion, EvaluatorID: e.EvaluatorID, EvaluatorVersion: e.VersionID, BundleDigest: bundle.Digest, Findings: []Finding{}}
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		check := e.Checks[key]
		if check == nil {
			return Result{}, fmt.Errorf("%w: nil check %s", ErrInvalid, key)
		}
		finding, err := check(ctx, cloneBundle(bundle))
		if err != nil {
			return Result{}, fmt.Errorf("%w: %s: %v", ErrEvaluator, key, err)
		}
		if finding.RuleID == "" {
			finding.RuleID = key
		}
		if finding.Severity == "" {
			finding.Severity = SeverityInfo
		}
		if finding.Severity != SeverityInfo && finding.Severity != SeverityWarning && finding.Severity != SeverityCritical {
			return Result{}, fmt.Errorf("%w: invalid severity", ErrInvalid)
		}
		result.Findings = append(result.Findings, finding)
	}
	violations := 0
	for _, finding := range result.Findings {
		if finding.Invariant {
			violations++
		}
	}
	result.Score = 1
	if len(result.Findings) > 0 {
		result.Score = float64(len(result.Findings)-violations) / float64(len(result.Findings))
	}
	result.CostUnits = int64(len(bundle.Events) * len(result.Findings))
	result.EvidenceDigest = resultDigest(result)
	if violations > 0 {
		minimal := minimizeBundle(bundle, result.Findings)
		result.MinimalBundle = &minimal
	}
	return result, nil
}

// BuiltinTrajectoryEvaluator catches the runtime safety failures that are
// deterministic from a redacted session bundle: duplicate effect dispatch,
// receipts without authorization, retries after an unknown write, and
// capability use without a grant marker.
func BuiltinTrajectoryEvaluator() RuleEvaluator {
	return RuleEvaluator{EvaluatorID: "adro.trajectory", VersionID: "v1", Checks: map[string]Check{
		"duplicate_effect_dispatch":      checkDuplicateEffectDispatch,
		"receipt_requires_authorization": checkReceiptAuthorization,
		"unknown_write_retry":            checkUnknownWriteRetry,
		"capability_boundary":            checkCapabilityBoundary,
	}}
}

func checkDuplicateEffectDispatch(_ context.Context, bundle SessionBundle) (Finding, error) {
	seen := map[string]int64{}
	var sequences []int64
	for _, event := range bundle.Events {
		if !event.Dispatched || strings.TrimSpace(event.EffectID) == "" {
			continue
		}
		if prior, ok := seen[event.EffectID]; ok {
			sequences = append(sequences, prior, event.Sequence)
		} else {
			seen[event.EffectID] = event.Sequence
		}
	}
	return Finding{RuleID: "duplicate_effect_dispatch", Severity: SeverityCritical, Message: "effect dispatched more than once without a reconciliation boundary", Sequences: uniqueInt64(sequences), Invariant: len(sequences) > 0}, nil
}

func checkReceiptAuthorization(_ context.Context, bundle SessionBundle) (Finding, error) {
	unauthorized := []int64{}
	for _, event := range bundle.Events {
		if event.Receipted && !event.Authorized {
			unauthorized = append(unauthorized, event.Sequence)
		}
	}
	return Finding{RuleID: "receipt_requires_authorization", Severity: SeverityCritical, Message: "effect receipt lacks a durable authorization fact", Sequences: unauthorized, Invariant: len(unauthorized) > 0}, nil
}

func checkUnknownWriteRetry(_ context.Context, bundle SessionBundle) (Finding, error) {
	unknown := map[string]bool{}
	violations := []int64{}
	for _, event := range bundle.Events {
		if event.EffectID == "" {
			continue
		}
		if strings.EqualFold(event.Type, "effect.outcome_unknown") {
			unknown[event.EffectID] = true
		}
		if event.Attempt > 1 && event.Dispatched && unknown[event.EffectID] {
			violations = append(violations, event.Sequence)
		}
	}
	return Finding{RuleID: "unknown_write_retry", Severity: SeverityCritical, Message: "a dispatched effect was retried after its outcome became unknown", Sequences: violations, Invariant: len(violations) > 0}, nil
}

func checkCapabilityBoundary(_ context.Context, bundle SessionBundle) (Finding, error) {
	violations := []int64{}
	for _, event := range bundle.Events {
		if strings.TrimSpace(event.Capability) != "" && !event.Authorized {
			violations = append(violations, event.Sequence)
		}
	}
	return Finding{RuleID: "capability_boundary", Severity: SeverityCritical, Message: "a capability-bearing event was not authorized", Sequences: violations, Invariant: len(violations) > 0}, nil
}

type Run struct {
	ID               string        `json:"id"`
	Revision         int64         `json:"revision"`
	State            RunState      `json:"state"`
	EvaluatorID      string        `json:"evaluator_id"`
	EvaluatorVersion string        `json:"evaluator_version"`
	Bundle           SessionBundle `json:"bundle"`
	Result           *Result       `json:"result,omitempty"`
	BudgetUnits      int64         `json:"budget_units"`
	ConsumedUnits    int64         `json:"consumed_units"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	CancelReason     string        `json:"cancel_reason,omitempty"`
}

type StoreOptions struct {
	Now func() time.Time
	ID  func() string
}

type Store struct {
	mu   sync.RWMutex
	now  func() time.Time
	id   func() string
	runs map[string]Run
}

func NewStore(options StoreOptions) *Store {
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	id := options.ID
	if id == nil {
		var sequence int64
		id = func() string { sequence++; return fmt.Sprintf("eval-%d", sequence) }
	}
	return &Store{now: now, id: id, runs: map[string]Run{}}
}

func (s *Store) Start(bundle SessionBundle, evaluator Evaluator, budget int64) (Run, error) {
	if s == nil || evaluator == nil || budget < 1 {
		return Run{}, ErrInvalid
	}
	if err := bundle.Validate(); err != nil {
		return Run{}, err
	}
	if strings.TrimSpace(evaluator.ID()) == "" || strings.TrimSpace(evaluator.Version()) == "" {
		return Run{}, ErrInvalid
	}
	now := s.now().UTC()
	run := Run{ID: s.id(), Revision: 1, State: RunQueued, EvaluatorID: evaluator.ID(), EvaluatorVersion: evaluator.Version(), Bundle: cloneBundle(bundle), BudgetUnits: budget, CreatedAt: now, UpdatedAt: now}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.runs[run.ID]; exists {
		return Run{}, ErrConflict
	}
	s.runs[run.ID] = cloneRun(run)
	return cloneRun(run), nil
}

func (s *Store) Get(id string) (Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.runs[strings.TrimSpace(id)]
	if !ok {
		return Run{}, ErrNotFound
	}
	return cloneRun(run), nil
}

func (s *Store) Execute(ctx context.Context, id string, expectedRevision int64, evaluator Evaluator) (Run, error) {
	if s == nil || evaluator == nil {
		return Run{}, ErrInvalid
	}
	s.mu.Lock()
	run, ok := s.runs[strings.TrimSpace(id)]
	if !ok {
		s.mu.Unlock()
		return Run{}, ErrNotFound
	}
	if run.Revision != expectedRevision {
		s.mu.Unlock()
		return Run{}, ErrConflict
	}
	if run.State == RunCompleted || run.State == RunFailed || run.State == RunCancelled {
		s.mu.Unlock()
		return Run{}, ErrTerminal
	}
	if run.EvaluatorID != evaluator.ID() || run.EvaluatorVersion != evaluator.Version() {
		s.mu.Unlock()
		return Run{}, ErrConflict
	}
	if run.ConsumedUnits >= run.BudgetUnits {
		run.State, run.Revision, run.UpdatedAt = RunFailed, run.Revision+1, s.now().UTC()
		run.CancelReason = ErrBudget.Error()
		s.runs[run.ID] = run
		s.mu.Unlock()
		return cloneRun(run), ErrBudget
	}
	run.State, run.Revision, run.UpdatedAt = RunRunning, run.Revision+1, s.now().UTC()
	s.runs[run.ID] = run
	s.mu.Unlock()

	result, evalErr := evaluator.Evaluate(ctx, cloneBundle(run.Bundle))
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.runs[run.ID]
	if !ok {
		return Run{}, ErrNotFound
	}
	if evalErr != nil {
		if errors.Is(evalErr, context.Canceled) || errors.Is(evalErr, context.DeadlineExceeded) {
			current.State, current.CancelReason = RunPaused, evalErr.Error()
		} else {
			current.State, current.CancelReason = RunFailed, evalErr.Error()
		}
		current.Revision, current.UpdatedAt = current.Revision+1, s.now().UTC()
		s.runs[run.ID] = current
		return cloneRun(current), evalErr
	}
	if result.CostUnits > current.BudgetUnits-current.ConsumedUnits {
		current.State, current.CancelReason = RunFailed, ErrBudget.Error()
		current.Revision, current.UpdatedAt = current.Revision+1, s.now().UTC()
		s.runs[run.ID] = current
		return cloneRun(current), ErrBudget
	}
	current.ConsumedUnits += result.CostUnits
	current.Result = &result
	current.State, current.Revision, current.UpdatedAt = RunCompleted, current.Revision+1, s.now().UTC()
	s.runs[run.ID] = current
	return cloneRun(current), nil
}

func (s *Store) Cancel(id string, expectedRevision int64, reason string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[strings.TrimSpace(id)]
	if !ok {
		return Run{}, ErrNotFound
	}
	if run.Revision != expectedRevision {
		return Run{}, ErrConflict
	}
	if run.State == RunCompleted || run.State == RunFailed || run.State == RunCancelled {
		return Run{}, ErrTerminal
	}
	run.State, run.CancelReason = RunCancelled, strings.TrimSpace(reason)
	run.Revision, run.UpdatedAt = run.Revision+1, s.now().UTC()
	s.runs[run.ID] = run
	return cloneRun(run), nil
}

func bundleDigest(bundle SessionBundle) string {
	copy := bundle
	copy.Digest = ""
	data, _ := json.Marshal(copy)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func resultDigest(result Result) string {
	copy := result
	copy.EvidenceDigest = ""
	copy.MinimalBundle = nil
	data, _ := json.Marshal(copy)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func minimizeBundle(bundle SessionBundle, findings []Finding) SessionBundle {
	wanted := map[int64]struct{}{}
	for _, finding := range findings {
		if !finding.Invariant {
			continue
		}
		for _, sequence := range finding.Sequences {
			wanted[sequence] = struct{}{}
		}
	}
	events := make([]BundleEvent, 0, len(wanted))
	for _, event := range bundle.Events {
		if _, ok := wanted[event.Sequence]; ok {
			events = append(events, event)
		}
	}
	minimal, err := NewSessionBundle(bundle.SessionID, bundle.TenantID, bundle.WorkspaceID, bundle.SourceDigest, events, true)
	if err != nil {
		return bundle
	}
	return minimal
}

func cloneEvents(events []BundleEvent) []BundleEvent {
	result := make([]BundleEvent, len(events))
	for i, event := range events {
		result[i] = event
		if event.Payload != nil {
			result[i].Payload = map[string]any{}
			for key, value := range event.Payload {
				result[i].Payload[key] = value
			}
		}
	}
	return result
}

func cloneBundle(bundle SessionBundle) SessionBundle {
	bundle.Events = cloneEvents(bundle.Events)
	if bundle.Metadata != nil {
		bundle.Metadata = map[string]string{}
		for key, value := range bundle.Metadata {
			bundle.Metadata[key] = value
		}
	}
	return bundle
}

func cloneRun(run Run) Run {
	run.Bundle = cloneBundle(run.Bundle)
	if run.Result != nil {
		result := *run.Result
		result.Findings = append([]Finding(nil), run.Result.Findings...)
		if run.Result.MinimalBundle != nil {
			minimal := cloneBundle(*run.Result.MinimalBundle)
			result.MinimalBundle = &minimal
		}
		run.Result = &result
	}
	return run
}

func uniqueInt64(values []int64) []int64 {
	set := map[int64]struct{}{}
	for _, value := range values {
		set[value] = struct{}{}
	}
	result := make([]int64, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
