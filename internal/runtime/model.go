package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	coreencoding "github.com/adro-project/adro/core/encoding"
	"github.com/adro-project/adro/core/ids"
)

const (
	ModelContractVersion = 1

	ModelEventTextDelta      = "text_delta"
	ModelEventReasoningDelta = "reasoning_delta"
	ModelEventToolRequest    = "tool_request"
	ModelEventUsageDelta     = "usage_delta"
	ModelEventFinish         = "finish"
	ModelEventProviderError  = "provider_error"
	ModelEventStreamGap      = "stream_gap"

	ModelFinishCompleted = "completed"
	ModelFinishCancelled = "cancelled"
	ModelFinishRejected  = "rejected"
	ModelFinishSuspended = "suspended"

	StreamOverflowBlock        = "block"
	StreamOverflowDropOptional = "drop_optional"
	StreamOverflowDisconnect   = "disconnect"
)

var (
	ErrModelRequestInvalid      = errors.New("runtime model request is invalid")
	ErrModelIdempotencyConflict = errors.New("runtime model idempotency key conflict")
	ErrModelOutcomeUnknown      = errors.New("runtime model outcome is unknown")
	ErrModelTransition          = errors.New("runtime model state transition is invalid")
	ErrModelCapabilityDenied    = errors.New("runtime model capability is not negotiated")
	ErrStreamBackpressure       = errors.New("runtime model stream buffer is full")
	ErrStreamDisconnected       = errors.New("runtime model stream disconnected")
	ErrStreamGap                = errors.New("runtime model stream cursor is outside retention")
)

// ModelRequest is the immutable dispatch contract. The request payload is
// canonical JSON, while the digest binds context, policy, tools, adapter and
// config identities so replay does not silently use current global settings.
type ModelRequest struct {
	RequestID            string             `json:"request_id"`
	Scope                Scope              `json:"scope"`
	Model                string             `json:"model"`
	Adapter              string             `json:"adapter"`
	AdapterVersion       string             `json:"adapter_version"`
	Prompt               json.RawMessage    `json:"prompt"`
	ContextDigest        string             `json:"context_digest"`
	ToolCatalogDigest    string             `json:"tool_catalog_digest,omitempty"`
	PolicyBundleDigest   string             `json:"policy_bundle_digest"`
	ConfigSnapshotDigest string             `json:"config_snapshot_digest"`
	Continuation         *ContinuationToken `json:"continuation,omitempty"`
	Attempt              int                `json:"attempt"`
	IdempotencyKey       string             `json:"idempotency_key"`
	RequestDigest        string             `json:"request_digest"`
}

type ContinuationToken struct {
	Provider string `json:"provider"`
	Token    string `json:"token"`
	Digest   string `json:"digest"`
}

type ProviderCapabilities struct {
	ProviderVersion  string   `json:"provider_version"`
	ProtocolVersion  int      `json:"protocol_version"`
	Models           []string `json:"models,omitempty"`
	Streaming        bool     `json:"streaming"`
	Tools            bool     `json:"tools"`
	Images           bool     `json:"images"`
	StructuredOutput bool     `json:"structured_output"`
	Reasoning        bool     `json:"reasoning"`
	Continuation     bool     `json:"continuation"`
}

type Usage struct {
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CachedInputTokens int64   `json:"cached_input_tokens"`
	DurationMillis    int64   `json:"duration_millis"`
	FirstTokenMillis  int64   `json:"first_token_millis"`
	RetryCount        int     `json:"retry_count"`
	EstimatedCost     float64 `json:"estimated_cost"`
}

type ModelEvent struct {
	RequestID      string          `json:"request_id"`
	Sequence       int64           `json:"sequence"`
	EventID        string          `json:"event_id"`
	Type           string          `json:"type"`
	Text           string          `json:"text,omitempty"`
	ToolName       string          `json:"tool_name,omitempty"`
	ToolCallID     string          `json:"tool_call_id,omitempty"`
	Arguments      json.RawMessage `json:"arguments,omitempty"`
	Usage          *Usage          `json:"usage,omitempty"`
	FinishReason   string          `json:"finish_reason,omitempty"`
	ErrorCode      string          `json:"error_code,omitempty"`
	ErrorRetryable bool            `json:"error_retryable,omitempty"`
	Cursor         string          `json:"cursor"`
}

func (r ModelRequest) Validate() error {
	if !r.Scope.valid() || strings.TrimSpace(r.RequestID) == "" || strings.TrimSpace(r.Model) == "" || strings.TrimSpace(r.Adapter) == "" || strings.TrimSpace(r.AdapterVersion) == "" || strings.TrimSpace(r.ContextDigest) == "" || strings.TrimSpace(r.PolicyBundleDigest) == "" || strings.TrimSpace(r.ConfigSnapshotDigest) == "" || strings.TrimSpace(r.IdempotencyKey) == "" || r.Attempt < 1 {
		return ErrModelRequestInvalid
	}
	if err := ids.Validate("model_request", r.RequestID); err != nil {
		return fmt.Errorf("%w: request_id: %v", ErrModelRequestInvalid, err)
	}
	canonical, err := coreencoding.Canonicalize(r.Prompt)
	if err != nil {
		return fmt.Errorf("%w: prompt: %v", ErrModelRequestInvalid, err)
	}
	if !bytes.Equal(canonical, r.Prompt) {
		return fmt.Errorf("%w: prompt must use canonical JSON", ErrModelRequestInvalid)
	}
	if r.Continuation != nil {
		if strings.TrimSpace(r.Continuation.Provider) == "" || strings.TrimSpace(r.Continuation.Token) == "" || strings.TrimSpace(r.Continuation.Digest) == "" {
			return fmt.Errorf("%w: continuation is incomplete", ErrModelRequestInvalid)
		}
	}
	computed, err := r.ComputeDigest()
	if err != nil || computed != r.RequestDigest {
		return fmt.Errorf("%w: request_digest mismatch", ErrModelRequestInvalid)
	}
	return nil
}

func (r ModelRequest) ComputeDigest() (string, error) {
	return coreencoding.Digest(struct {
		RequestID            string             `json:"request_id"`
		Scope                Scope              `json:"scope"`
		Model                string             `json:"model"`
		Adapter              string             `json:"adapter"`
		AdapterVersion       string             `json:"adapter_version"`
		Prompt               json.RawMessage    `json:"prompt"`
		ContextDigest        string             `json:"context_digest"`
		ToolCatalogDigest    string             `json:"tool_catalog_digest,omitempty"`
		PolicyBundleDigest   string             `json:"policy_bundle_digest"`
		ConfigSnapshotDigest string             `json:"config_snapshot_digest"`
		Continuation         *ContinuationToken `json:"continuation,omitempty"`
		Attempt              int                `json:"attempt"`
		IdempotencyKey       string             `json:"idempotency_key"`
	}{r.RequestID, r.Scope, r.Model, r.Adapter, r.AdapterVersion, r.Prompt, r.ContextDigest, r.ToolCatalogDigest, r.PolicyBundleDigest, r.ConfigSnapshotDigest, r.Continuation, r.Attempt, r.IdempotencyKey})
}

func NewModelRequest(request ModelRequest) (ModelRequest, error) {
	canonical, err := coreencoding.Canonicalize(request.Prompt)
	if err != nil {
		return ModelRequest{}, fmt.Errorf("%w: prompt: %v", ErrModelRequestInvalid, err)
	}
	request.Prompt = canonical
	digest, err := request.ComputeDigest()
	if err != nil {
		return ModelRequest{}, err
	}
	request.RequestDigest = digest
	if err := request.Validate(); err != nil {
		return ModelRequest{}, err
	}
	return request, nil
}

func (c ProviderCapabilities) Validate() error {
	if strings.TrimSpace(c.ProviderVersion) == "" || c.ProtocolVersion != ModelContractVersion {
		return ErrModelCapabilityDenied
	}
	copyModels := append([]string(nil), c.Models...)
	sort.Strings(copyModels)
	for i := 1; i < len(copyModels); i++ {
		if copyModels[i] == copyModels[i-1] || strings.TrimSpace(copyModels[i]) == "" {
			return ErrModelCapabilityDenied
		}
	}
	return nil
}

func (c ProviderCapabilities) Supports(model string, feature string) bool {
	if c.Validate() != nil {
		return false
	}
	if model != "" {
		found := false
		for _, candidate := range c.Models {
			if candidate == model {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	switch feature {
	case "streaming":
		return c.Streaming
	case "tools":
		return c.Tools
	case "images":
		return c.Images
	case "structured_output":
		return c.StructuredOutput
	case "reasoning":
		return c.Reasoning
	case "continuation":
		return c.Continuation
	default:
		return false
	}
}

func (e ModelEvent) Validate(previousSequence int64, requestID string) error {
	if e.RequestID != requestID || e.Sequence != previousSequence+1 || strings.TrimSpace(e.EventID) == "" || strings.TrimSpace(e.Type) == "" || !utf8.ValidString(e.Text) {
		return fmt.Errorf("%w: event sequence or identity", ErrModelRequestInvalid)
	}
	if strings.TrimSpace(e.Cursor) == "" || e.Cursor != StreamCursor(e.RequestID, e.Sequence, e.EventID) {
		return fmt.Errorf("%w: invalid stream cursor", ErrModelRequestInvalid)
	}
	switch e.Type {
	case ModelEventTextDelta, ModelEventReasoningDelta:
		if e.Text == "" {
			return fmt.Errorf("%w: empty text delta", ErrModelRequestInvalid)
		}
	case ModelEventToolRequest:
		if e.ToolName == "" || e.ToolCallID == "" || len(e.Arguments) == 0 {
			return fmt.Errorf("%w: incomplete tool request", ErrModelRequestInvalid)
		}
		canonical, err := coreencoding.Canonicalize(e.Arguments)
		if err != nil || !bytes.Equal(canonical, e.Arguments) {
			return fmt.Errorf("%w: tool arguments must be complete canonical JSON", ErrModelRequestInvalid)
		}
	case ModelEventUsageDelta:
		if e.Usage == nil || e.Usage.InputTokens < 0 || e.Usage.OutputTokens < 0 || e.Usage.CachedInputTokens < 0 || e.Usage.RetryCount < 0 {
			return fmt.Errorf("%w: invalid usage delta", ErrModelRequestInvalid)
		}
	case ModelEventFinish:
		if e.FinishReason == "" {
			return fmt.Errorf("%w: finish reason is required", ErrModelRequestInvalid)
		}
	case ModelEventProviderError:
		if e.ErrorCode == "" {
			return fmt.Errorf("%w: provider error code is required", ErrModelRequestInvalid)
		}
	case ModelEventStreamGap:
		return fmt.Errorf("%w: gap is transport metadata, not a model event", ErrModelRequestInvalid)
	default:
		return fmt.Errorf("%w: unsupported event type %q", ErrModelRequestInvalid, e.Type)
	}
	return nil
}

func StreamCursor(requestID string, sequence int64, eventID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%d:%s", requestID, sequence, eventID)))
}

// ModelGateway is the adapter boundary. Dispatch must return a stream whose
// events are validated by Runtime; the adapter cannot close a call directly.
type ModelGateway interface {
	Capabilities(context.Context) (ProviderCapabilities, error)
	Dispatch(context.Context, ModelRequest) (ModelEventStream, error)
}

type ModelEventStream interface {
	Events() <-chan ModelEvent
	Close() error
}

type StreamGap struct {
	RequestID string `json:"request_id"`
	From      int64  `json:"from"`
	To        int64  `json:"to"`
	Reason    string `json:"reason"`
}

// BoundedModelStream is a deterministic, bounded stream projection. It is
// intentionally synchronous: adapters push validated events, consumers read
// by cursor, and an old cursor produces an explicit gap instead of silently
// skipping retained events.
type BoundedModelStream struct {
	mu           sync.Mutex
	requestID    string
	capacity     int
	overflow     string
	retention    int
	events       []ModelEvent
	lastSequence int64
	droppedFrom  int64
	droppedTo    int64
	dropped      int64
	disconnected bool
}

func NewBoundedModelStream(requestID string, capacity, retention int, overflow string) (*BoundedModelStream, error) {
	if strings.TrimSpace(requestID) == "" || capacity < 1 || retention < 1 {
		return nil, errors.New("request_id, positive capacity and positive retention are required")
	}
	if overflow != StreamOverflowBlock && overflow != StreamOverflowDropOptional && overflow != StreamOverflowDisconnect {
		return nil, errors.New("unsupported stream overflow policy")
	}
	return &BoundedModelStream{requestID: requestID, capacity: capacity, retention: retention, overflow: overflow}, nil
}

func (s *BoundedModelStream) Append(event ModelEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disconnected {
		return ErrStreamDisconnected
	}
	if err := event.Validate(s.lastSequence, s.requestID); err != nil {
		return err
	}
	if len(s.events) >= s.capacity {
		switch s.overflow {
		case StreamOverflowBlock:
			return ErrStreamBackpressure
		case StreamOverflowDisconnect:
			s.disconnected = true
			return ErrStreamDisconnected
		case StreamOverflowDropOptional:
			if event.Type != ModelEventTextDelta && event.Type != ModelEventReasoningDelta {
				return ErrStreamBackpressure
			}
			if s.droppedFrom == 0 {
				s.droppedFrom = event.Sequence
			}
			s.droppedTo = event.Sequence
			s.dropped++
			s.lastSequence = event.Sequence
			return nil
		}
	}
	s.events = append(s.events, event)
	s.lastSequence = event.Sequence
	if len(s.events) > s.retention {
		s.events = append([]ModelEvent(nil), s.events[len(s.events)-s.retention:]...)
	}
	return nil
}

func (s *BoundedModelStream) Read(afterCursor string, limit int) ([]ModelEvent, string, *StreamGap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		return nil, "", nil, errors.New("limit must be positive")
	}
	if s.disconnected {
		return nil, "", nil, ErrStreamDisconnected
	}
	start := 0
	if afterCursor != "" {
		if len(s.events) == 0 {
			return nil, "", &StreamGap{RequestID: s.requestID, From: 1, To: 0, Reason: "cursor_outside_retention"}, ErrStreamGap
		}
		found := false
		for index, event := range s.events {
			if event.Cursor == afterCursor {
				start = index + 1
				found = true
				break
			}
		}
		if !found {
			return nil, "", &StreamGap{RequestID: s.requestID, From: s.events[0].Sequence, To: s.events[len(s.events)-1].Sequence, Reason: "cursor_outside_retention"}, ErrStreamGap
		}
		for _, event := range s.events {
			if event.Cursor == afterCursor && s.droppedFrom > event.Sequence {
				return nil, "", &StreamGap{RequestID: s.requestID, From: s.droppedFrom, To: s.droppedTo, Reason: "optional_delta_dropped"}, ErrStreamGap
			}
		}
	}
	end := start + limit
	if end > len(s.events) {
		end = len(s.events)
	}
	items := append([]ModelEvent(nil), s.events[start:end]...)
	next := ""
	if end < len(s.events) {
		next = s.events[end-1].Cursor
	}
	return items, next, nil, nil
}

func (s *BoundedModelStream) DroppedOptional() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}
