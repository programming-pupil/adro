package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

type recordingExporter struct {
	spans []Span
}

func (e *recordingExporter) Export(_ context.Context, spans []Span) error {
	e.spans = append(e.spans, cloneSpans(spans)...)
	return nil
}

func (e *recordingExporter) Shutdown(context.Context) error { return nil }

func TestTracerExportsBoundedSpanWithParent(t *testing.T) {
	recorder := &recordingExporter{}
	tracer := Tracer{Exporter: recorder}
	root, _ := StartSpan(context.Background())
	ctx, finish := tracer.Start(root, "provider.run", map[string]string{"plan_id": "secret-plan", "component": "provider", "safe": "yes"})
	if TraceID(ctx) == "" {
		t.Fatal("tracer did not install a span context")
	}
	if err := finish("ok", "done"); err != nil {
		t.Fatal(err)
	}
	if len(recorder.spans) != 1 || recorder.spans[0].ParentSpanID == "" || recorder.spans[0].Attributes["plan_id"] != "" || recorder.spans[0].Attributes["safe"] != "yes" {
		t.Fatalf("unexpected exported span: %+v", recorder.spans)
	}
}

func TestOTLPHTTPExporterPostsSpans(t *testing.T) {
	var received []Span
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type=%q", r.Header.Get("Content-Type"))
		}
		var payload struct {
			Spans []Span `json:"spans"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		received = payload.Spans
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	if err := NewOTLPHTTPExporter(server.URL).Export(context.Background(), []Span{{Name: "test"}}); err != nil {
		t.Fatal(err)
	}
	if len(received) != 1 || received[0].Name != "test" {
		t.Fatalf("received=%+v", received)
	}
}
