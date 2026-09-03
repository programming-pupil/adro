package telemetry

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestW3CTraceContextPropagation(t *testing.T) {
	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	ctx, span, err := StartRemoteSpan(context.Background(), parent, "vendor=value")
	if err != nil {
		t.Fatal(err)
	}
	if span.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || span.SpanID == "00f067aa0ba902b7" || span.TraceState != "vendor=value" {
		t.Fatalf("unexpected child span: %#v", span)
	}
	request, _ := http.NewRequest(http.MethodGet, "http://example.test", nil)
	InjectHeader(request.Header, ctx)
	if got := request.Header.Get(TraceParentHeader); !strings.HasPrefix(got, "00-4bf92f3577b34da6a3ce929d0e0e4736-") || got == parent {
		t.Fatalf("unexpected propagated traceparent %q", got)
	}
	if got := request.Header.Get(TraceStateHeader); got != "vendor=value" {
		t.Fatalf("unexpected tracestate %q", got)
	}
	if values := Environment(ctx); len(values) != 2 || !strings.HasPrefix(values[0], "TRACEPARENT=00-4bf92f3577b34da6a3ce929d0e0e4736-") || values[1] != "TRACESTATE=vendor=value" {
		t.Fatalf("unexpected environment carrier %#v", values)
	}
}

func TestInvalidCarrierStartsUntrustedNewTrace(t *testing.T) {
	ctx, span, err := StartRemoteSpan(context.Background(), "00-00000000000000000000000000000000-0000000000000000-01", "")
	if err == nil || !span.Valid() || span.TraceID == strings.Repeat("0", 32) {
		t.Fatalf("invalid carrier was trusted: span=%#v err=%v", span, err)
	}
	stored, ok := FromContext(ctx)
	if !ok || stored.TraceID != span.TraceID {
		t.Fatalf("new trace not stored: %#v", stored)
	}
}

func TestTraceStateValidationRejectsInjection(t *testing.T) {
	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	if _, err := ParseTraceParent(parent, "vendor=value\r\nx-secret=leak"); err == nil {
		t.Fatal("expected tracestate injection to be rejected")
	}
}
