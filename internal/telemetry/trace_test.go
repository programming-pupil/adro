package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adro-project/adro/internal/obs/trace"
	"github.com/adro-project/adro/internal/security/redact"

	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

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
	root, _ := trace.StartSpan(context.Background())
	ctx, finish := tracer.Start(root, "provider.run", map[string]string{
		"plan_id": "secret-plan", "component": "provider", "safe": "yes",
		"client.secret": "trace-canary", "authorization": "Bearer trace-canary",
		"secret_ref": "secret:vault/trace",
	})
	if trace.TraceID(ctx) == "" {
		t.Fatal("tracer did not install a span context")
	}
	if err := finish("ok", "done"); err != nil {
		t.Fatal(err)
	}
	if len(recorder.spans) != 1 || recorder.spans[0].ParentSpanID == "" || recorder.spans[0].Attributes["plan_id"] != "" || recorder.spans[0].Attributes["safe"] != "yes" {
		t.Fatalf("unexpected exported span: %+v", recorder.spans)
	}
	attributes := recorder.spans[0].Attributes
	if attributes["client.secret"] != redact.Redacted || attributes["authorization"] != redact.Redacted || attributes["secret_ref"] != "secret:vault/trace" {
		t.Fatalf("trace redaction was not enforced: %+v", attributes)
	}
}

func TestOTLPHTTPExporterPostsProtobufSpans(t *testing.T) {
	received := make(chan *collectortracev1.ExportTraceServiceRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" {
			t.Errorf("path=%q", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/x-protobuf" {
			t.Errorf("content type=%q", r.Header.Get("Content-Type"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read payload: %v", err)
		}
		var payload collectortracev1.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		received <- &payload
		response, _ := proto.Marshal(&collectortracev1.ExportTraceServiceResponse{})
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(response)
	}))
	defer server.Close()
	t.Setenv("ADRO_OTEL_EXPORTER_OTLP_ENDPOINT", server.URL+"/v1/traces")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "http/protobuf")
	tracer, err := NewTracerFromEnvironment(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, finish := tracer.Start(context.Background(), "test.span", map[string]string{"component": "test"})
	if trace.TraceID(ctx) == "" {
		t.Fatal("SDK tracer did not install trace context")
	}
	if err := finish("ok", ""); err != nil {
		t.Fatal(err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tracer.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-received:
		if len(request.ResourceSpans) != 1 || len(request.ResourceSpans[0].ScopeSpans) != 1 || len(request.ResourceSpans[0].ScopeSpans[0].Spans) != 1 {
			t.Fatalf("received=%+v", request)
		}
		if got := request.ResourceSpans[0].ScopeSpans[0].Spans[0].Name; got != "test.span" {
			t.Fatalf("span name=%q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OTLP exporter did not send a trace request")
	}
}

func TestOTLPCompatibilityEndpointValidationFailsClosed(t *testing.T) {
	t.Setenv("ADRO_OTEL_EXPORTER_OTLP_ENDPOINT", "collector.invalid/no-scheme?secret=value")
	if _, err := NewTracerFromEnvironment(context.Background()); err == nil {
		t.Fatal("invalid compatibility endpoint was accepted")
	}
}

func TestLocalTracerCreatesChildContextWithoutExporter(t *testing.T) {
	parent := trace.SpanContext{
		TraceID:    "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:     "00f067aa0ba902b7",
		TraceFlags: "01",
		TraceState: "vendor=value",
	}
	ctx, finish := LocalTracer().Start(trace.ContextWithSpan(context.Background(), parent), "child", nil)
	child, ok := trace.FromContext(ctx)
	if !ok || child.TraceID != parent.TraceID || child.SpanID == parent.SpanID || child.TraceState != parent.TraceState {
		t.Fatalf("noop tracer did not create a local child: parent=%+v child=%+v", parent, child)
	}
	if err := finish("ok", ""); err != nil {
		t.Fatal(err)
	}
}

func TestStandardOTLPTraceEndpointPostsProtobuf(t *testing.T) {
	receivedPath := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath <- r.URL.Path
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read payload: %v", err)
		}
		var payload collectortracev1.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		if len(payload.ResourceSpans) != 1 {
			t.Errorf("resource spans=%d", len(payload.ResourceSpans))
		}
		response, _ := proto.Marshal(&collectortracev1.ExportTraceServiceResponse{})
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(response)
	}))
	defer server.Close()

	t.Setenv("ADRO_OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", server.URL+"/custom/traces")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "http/protobuf")
	tracer, err := NewTracerFromEnvironment(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, finish := tracer.Start(context.Background(), "standard.endpoint", nil)
	if err := finish("ok", ""); err != nil {
		t.Fatal(err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tracer.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case path := <-receivedPath:
		if path != "/custom/traces" {
			t.Fatalf("path=%q", path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("standard OTLP traces endpoint did not receive a request")
	}
}

func TestOTLPCompatibilityEndpointRejectsEmbeddedCredentials(t *testing.T) {
	t.Setenv("ADRO_OTEL_EXPORTER_OTLP_ENDPOINT", "https://user:secret@collector.example.test/v1/traces")
	if _, err := NewTracerFromEnvironment(context.Background()); err == nil {
		t.Fatal("compatibility endpoint with embedded credentials was accepted")
	}
}
