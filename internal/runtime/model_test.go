package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func newModelRequest(t *testing.T) ModelRequest {
	t.Helper()
	request, err := NewModelRequest(ModelRequest{
		RequestID: "model-request-1", Scope: testScope(), Model: "model-1", Adapter: "adapter-1", AdapterVersion: "1.2.3",
		Prompt: json.RawMessage(`{"messages":[{"content":"hello","role":"user"}]}`), ContextDigest: "ctx-1", ToolCatalogDigest: "tools-1",
		PolicyBundleDigest: "policy-1", ConfigSnapshotDigest: "config-1", Attempt: 1, IdempotencyKey: "model-key-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func modelEvent(requestID string, sequence int64, eventType string) ModelEvent {
	event := ModelEvent{RequestID: requestID, Sequence: sequence, EventID: "model-event-" + string(rune('0'+sequence)), Type: eventType}
	if eventType == ModelEventTextDelta {
		event.Text = "hello"
	}
	if eventType == ModelEventToolRequest {
		event.ToolName, event.ToolCallID, event.Arguments = "search", "tool-call-1", json.RawMessage(`{"q":"durable"}`)
	}
	if eventType == ModelEventUsageDelta {
		event.Usage = &Usage{InputTokens: 1, OutputTokens: 2}
	}
	if eventType == ModelEventFinish {
		event.FinishReason = ModelFinishCompleted
	}
	event.Cursor = StreamCursor(requestID, sequence, event.EventID)
	return event
}

func TestModelRequestCanonicalDigestAndCapabilityNegotiationFailClosed(t *testing.T) {
	request := newModelRequest(t)
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	changed := request
	changed.Prompt = json.RawMessage(`{"messages":[{"role":"user","content":"changed"}]}`)
	if err := changed.Validate(); !errors.Is(err, ErrModelRequestInvalid) {
		t.Fatalf("changed request was accepted: %v", err)
	}
	caps := ProviderCapabilities{ProviderVersion: "provider-1", ProtocolVersion: ModelContractVersion, Models: []string{"model-1"}, Streaming: true, Tools: true}
	if err := caps.Validate(); err != nil || !caps.Supports("model-1", "streaming") || caps.Supports("unknown", "streaming") {
		t.Fatalf("capabilities=%+v err=%v", caps, err)
	}
	caps.ProtocolVersion++
	if caps.Validate() == nil || caps.Supports("model-1", "streaming") {
		t.Fatalf("incompatible capability was not rejected: %+v", caps)
	}
}

func TestModelEventValidationSeparatesPartialFramesAndTerminalFrames(t *testing.T) {
	requestID := "model-request-1"
	text := modelEvent(requestID, 1, ModelEventTextDelta)
	if err := text.Validate(0, requestID); err != nil {
		t.Fatal(err)
	}
	tool := modelEvent(requestID, 2, ModelEventToolRequest)
	if err := tool.Validate(1, requestID); err != nil {
		t.Fatal(err)
	}
	finish := modelEvent(requestID, 3, ModelEventFinish)
	if err := finish.Validate(2, requestID); err != nil {
		t.Fatal(err)
	}
	bad := tool
	bad.Sequence = 4
	bad.Cursor = StreamCursor(requestID, bad.Sequence, bad.EventID)
	if err := bad.Validate(3, requestID); err != nil {
		t.Fatal(err)
	}
	bad.Arguments = json.RawMessage(`{"q":`)
	if err := bad.Validate(3, requestID); err == nil {
		t.Fatal("truncated tool arguments accepted")
	}
	bad = text
	bad.Text = string([]byte{0xff})
	if err := bad.Validate(0, requestID); err == nil {
		t.Fatal("invalid UTF-8 text accepted")
	}
}

func TestBoundedModelStreamEnforcesCursorRetentionAndOptionalDrop(t *testing.T) {
	stream, err := NewBoundedModelStream("model-request-1", 2, 2, StreamOverflowDropOptional)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Append(modelEvent("model-request-1", 1, ModelEventTextDelta)); err != nil {
		t.Fatal(err)
	}
	if err := stream.Append(modelEvent("model-request-1", 2, ModelEventTextDelta)); err != nil {
		t.Fatal(err)
	}
	items, _, gap, err := stream.Read(StreamCursor("model-request-1", 1, "model-event-1"), 10)
	if err != nil || gap != nil || len(items) != 1 || items[0].Sequence != 2 {
		t.Fatalf("stream read items=%+v gap=%+v err=%v", items, gap, err)
	}
	if err := stream.Append(modelEvent("model-request-1", 3, ModelEventTextDelta)); err != nil {
		t.Fatal(err)
	}
	if stream.DroppedOptional() != 1 {
		t.Fatalf("dropped optional=%d", stream.DroppedOptional())
	}
	terminal := modelEvent("model-request-1", 4, ModelEventFinish)
	if err := stream.Append(terminal); !errors.Is(err, ErrStreamBackpressure) {
		t.Fatalf("terminal event bypassed full buffer: %v", err)
	}
	_, _, gap, err = stream.Read(StreamCursor("model-request-1", 2, "model-event-2"), 10)
	if !errors.Is(err, ErrStreamGap) || gap == nil || gap.Reason != "optional_delta_dropped" {
		t.Fatalf("dropped delta gap=%+v err=%v", gap, err)
	}
	_, _, gap, err = stream.Read(StreamCursor("model-request-1", 0, "missing"), 10)
	if !errors.Is(err, ErrStreamGap) || gap == nil || gap.Reason != "cursor_outside_retention" {
		t.Fatalf("retention gap=%+v err=%v", gap, err)
	}
}

func TestBoundedModelStreamDisconnectsOnExplicitOverflowPolicy(t *testing.T) {
	stream, err := NewBoundedModelStream("request-disconnect", 1, 1, StreamOverflowDisconnect)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Append(modelEvent("request-disconnect", 1, ModelEventFinish)); err != nil {
		t.Fatal(err)
	}
	if err := stream.Append(modelEvent("request-disconnect", 2, ModelEventFinish)); !errors.Is(err, ErrStreamDisconnected) {
		t.Fatalf("overflow did not disconnect stream: %v", err)
	}
	if _, _, _, err := stream.Read("", 1); !errors.Is(err, ErrStreamDisconnected) {
		t.Fatalf("disconnected stream was readable: %v", err)
	}
}

func TestModelGatewayContractIsProviderNeutral(t *testing.T) {
	var gateway ModelGateway = fakeModelGateway{}
	if _, err := gateway.Capabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := newModelRequest(t)
	stream, err := gateway.Dispatch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	select {
	case event := <-stream.Events():
		if event.RequestID != request.RequestID {
			t.Fatalf("event=%+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("gateway stream did not produce an event")
	}
}

type fakeModelGateway struct{}

func (fakeModelGateway) Capabilities(context.Context) (ProviderCapabilities, error) {
	return ProviderCapabilities{ProviderVersion: "fake", ProtocolVersion: ModelContractVersion, Models: []string{"model-1"}, Streaming: true}, nil
}

func (fakeModelGateway) Dispatch(_ context.Context, request ModelRequest) (ModelEventStream, error) {
	channel := make(chan ModelEvent, 1)
	channel <- modelEvent(request.RequestID, 1, ModelEventFinish)
	close(channel)
	return fakeModelStream{events: channel}, nil
}

type fakeModelStream struct{ events <-chan ModelEvent }

func (s fakeModelStream) Events() <-chan ModelEvent { return s.events }
func (fakeModelStream) Close() error                { return nil }
