package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Span is the small ADRO-owned representation exported at process
// boundaries. It intentionally maps cleanly to OTLP while keeping the core
// runtime independent from a vendor SDK.
type Span struct {
	Name          string            `json:"name"`
	TraceID       string            `json:"trace_id"`
	SpanID        string            `json:"span_id"`
	ParentSpanID  string            `json:"parent_span_id,omitempty"`
	StartTime     time.Time         `json:"start_time"`
	EndTime       time.Time         `json:"end_time"`
	Attributes    map[string]string `json:"attributes,omitempty"`
	Status        string            `json:"status,omitempty"`
	StatusMessage string            `json:"status_message,omitempty"`
}

// SpanExporter is deliberately batch-oriented so HTTP/provider/tool spans
// can share one bounded export path. Implementations must not mutate spans.
type SpanExporter interface {
	Export(context.Context, []Span) error
	Shutdown(context.Context) error
}

type NopExporter struct{}

func (NopExporter) Export(context.Context, []Span) error { return nil }
func (NopExporter) Shutdown(context.Context) error       { return nil }

// OTLPHTTPExporter sends a compact JSON envelope to an operator-configured
// OTLP gateway. The endpoint is intentionally explicit; with no endpoint the
// tracer is a zero-cost no-op and never attempts a network connection.
type OTLPHTTPExporter struct {
	Endpoint string
	Client   *http.Client
}

func NewOTLPHTTPExporter(endpoint string) *OTLPHTTPExporter {
	return &OTLPHTTPExporter{Endpoint: strings.TrimRight(strings.TrimSpace(endpoint), "/"), Client: &http.Client{Timeout: 5 * time.Second}}
}

func (e *OTLPHTTPExporter) Export(ctx context.Context, spans []Span) error {
	if e == nil || e.Endpoint == "" || len(spans) == 0 {
		return nil
	}
	body, err := json.Marshal(struct {
		Resource map[string]string `json:"resource"`
		Spans    []Span            `json:"spans"`
	}{Resource: map[string]string{"service.name": "adro"}, Spans: cloneSpans(spans)})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	client := e.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("otlp exporter returned non-success status")
	}
	return nil
}

func (e *OTLPHTTPExporter) Shutdown(context.Context) error { return nil }

func ExporterFromEnvironment() SpanExporter {
	endpoint := strings.TrimSpace(os.Getenv("ADRO_OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint == "" || strings.EqualFold(endpoint, "disabled") {
		return NopExporter{}
	}
	return NewOTLPHTTPExporter(endpoint)
}

type tracerKey struct{}
type exporterKey struct{}

// Tracer records one span and exports it on End. Attributes are bounded and
// values that look like high-cardinality identifiers are intentionally omitted
// unless explicitly whitelisted by the caller.
type Tracer struct {
	Exporter SpanExporter
	Now      func() time.Time
}

type activeSpan struct {
	tracer Tracer
	ctx    context.Context
	span   Span
	parent SpanContext
	ended  bool
	mu     sync.Mutex
}

func (t Tracer) Start(ctx context.Context, name string, attributes map[string]string) (context.Context, func(string, string) error) {
	if ctx == nil {
		ctx = context.Background()
	}
	childCtx, spanContext := StartSpan(ctx)
	parent, _ := FromContext(ctx)
	now := time.Now().UTC()
	if t.Now != nil {
		now = t.Now().UTC()
	}
	span := &activeSpan{tracer: t, ctx: childCtx, parent: parent, span: Span{Name: strings.TrimSpace(name), TraceID: spanContext.TraceID, SpanID: spanContext.SpanID, ParentSpanID: parent.SpanID, StartTime: now, Attributes: boundedAttributes(attributes)}}
	return context.WithValue(childCtx, tracerKey{}, span), func(status, message string) error {
		return span.End(status, message)
	}
}

func (s *activeSpan) End(status, message string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return nil
	}
	s.ended = true
	now := time.Now().UTC()
	if s.tracer.Now != nil {
		now = s.tracer.Now().UTC()
	}
	s.span.EndTime = now
	s.span.Status = strings.TrimSpace(status)
	s.span.StatusMessage = strings.TrimSpace(message)
	exporter := s.tracer.Exporter
	s.mu.Unlock()
	if exporter == nil {
		return nil
	}
	return exporter.Export(s.ctx, []Span{s.span})
}

func boundedAttributes(attributes map[string]string) map[string]string {
	if len(attributes) == 0 {
		return nil
	}
	result := make(map[string]string, len(attributes))
	for key, value := range attributes {
		key = strings.TrimSpace(key)
		if key == "" || len(result) >= 32 || strings.HasSuffix(key, ".id") || strings.HasSuffix(key, "_id") || key == "session_id" || key == "comment_id" {
			continue
		}
		if len(value) > 256 {
			value = value[:256]
		}
		result[key] = value
	}
	return result
}

func cloneSpans(spans []Span) []Span {
	copySpans := make([]Span, len(spans))
	copy(copySpans, spans)
	for i := range copySpans {
		if spans[i].Attributes != nil {
			copySpans[i].Attributes = make(map[string]string, len(spans[i].Attributes))
			for key, value := range spans[i].Attributes {
				copySpans[i].Attributes[key] = value
			}
		}
	}
	return copySpans
}
