package telemetry

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/adro-project/adro/internal/security"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/adro-project/adro"

var localPropagationProvider oteltrace.TracerProvider = sdktrace.NewTracerProvider(
	sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.NeverSample())),
)

// Span is retained as a small in-memory test record. Production export uses
// the official OpenTelemetry SDK and OTLP exporter configured by
// NewTracerFromEnvironment.
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

// SpanExporter is a deterministic test seam. Runtime processes must configure
// an SDK TracerProvider instead of implementing a private wire exporter.
type SpanExporter interface {
	Export(context.Context, []Span) error
	Shutdown(context.Context) error
}

type NopExporter struct{}

func (NopExporter) Export(context.Context, []Span) error { return nil }
func (NopExporter) Shutdown(context.Context) error       { return nil }

// Tracer creates spans through an injected OpenTelemetry TracerProvider. The
// Exporter field remains available for deterministic unit tests that should not
// start SDK processors or network clients.
type Tracer struct {
	Provider oteltrace.TracerProvider
	Exporter SpanExporter
	Now      func() time.Time
	shutdown func(context.Context) error
}

// NewTracerFromEnvironment builds an official OpenTelemetry SDK provider. With
// no configured OTLP endpoint it still creates valid local span contexts but
// owns no exporter or background processor. OTLP export uses the standard
// OTEL_EXPORTER_OTLP_* environment contract; ADRO_OTEL_EXPORTER_OTLP_ENDPOINT
// is retained as an explicit full traces URL compatibility option.
func NewTracerFromEnvironment(ctx context.Context) (Tracer, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	enabled, endpoint, err := otlpHTTPConfiguration()
	if err != nil {
		return Tracer{}, err
	}
	if !enabled {
		return LocalTracer(), nil
	}
	exporterOptions := []otlptracehttp.Option{otlptracehttp.WithEncoding(otlptracehttp.EncodingProtobuf)}
	if endpoint != "" {
		exporterOptions = append(exporterOptions, otlptracehttp.WithEndpointURL(endpoint))
	}
	exporter, err := otlptracehttp.New(ctx, exporterOptions...)
	if err != nil {
		return Tracer{}, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", "adro"))),
		sdktrace.WithBatcher(exporter),
	)
	return Tracer{Provider: provider, shutdown: provider.Shutdown}, nil
}

func otlpHTTPConfiguration() (enabled bool, endpoint string, err error) {
	compatibilityEndpoint := strings.TrimSpace(os.Getenv("ADRO_OTEL_EXPORTER_OTLP_ENDPOINT"))
	if strings.EqualFold(compatibilityEndpoint, "disabled") {
		return false, "", nil
	}
	if compatibilityEndpoint != "" {
		parsed, parseErr := url.Parse(compatibilityEndpoint)
		if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return false, "", errors.New("ADRO_OTEL_EXPORTER_OTLP_ENDPOINT must be an absolute http(s) traces URL without embedded credentials, query, or fragment")
		}
		return true, compatibilityEndpoint, nil
	}
	for _, name := range []string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true, "", nil
		}
	}
	return false, "", nil
}

// LocalTracer returns a shared SDK provider with no exporter or processor. It
// preserves W3C parent/child context for components constructed outside the
// process bootstrap without re-reading environment variables or allocating one
// provider per span.
func LocalTracer() Tracer {
	return Tracer{Provider: localPropagationProvider}
}

func (t Tracer) Enabled() bool { return t.Provider != nil || t.Exporter != nil }

func (t Tracer) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if t.shutdown != nil {
		return t.shutdown(ctx)
	}
	if t.Exporter != nil {
		return t.Exporter.Shutdown(ctx)
	}
	return nil
}

type activeSpan struct {
	tracer Tracer
	ctx    context.Context
	span   Span
	ended  bool
	mu     sync.Mutex
}

func (t Tracer) Start(ctx context.Context, name string, attributes map[string]string) (context.Context, func(string, string) error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if t.Provider != nil {
		values := boundedAttributes(attributes)
		otelAttributes := make([]attribute.KeyValue, 0, len(values))
		for key, value := range values {
			otelAttributes = append(otelAttributes, attribute.String(key, value))
		}
		startOptions := []oteltrace.SpanStartOption{oteltrace.WithAttributes(otelAttributes...)}
		if t.Now != nil {
			startOptions = append(startOptions, oteltrace.WithTimestamp(t.Now().UTC()))
		}
		childCtx, span := t.Provider.Tracer(instrumentationName).Start(ctx, strings.TrimSpace(name), startOptions...)
		childCtx = contextWithLocalSpanContext(childCtx, span.SpanContext())
		var once sync.Once
		return childCtx, func(status, message string) error {
			once.Do(func() {
				message = strings.TrimSpace(message)
				switch strings.ToLower(strings.TrimSpace(status)) {
				case "ok":
					span.SetStatus(codes.Ok, message)
				case "", "unset":
				default:
					span.SetStatus(codes.Error, message)
				}
				if t.Now != nil {
					span.End(oteltrace.WithTimestamp(t.Now().UTC()))
				} else {
					span.End()
				}
			})
			return nil
		}
	}

	childCtx, spanContext := StartSpan(ctx)
	parent, _ := FromContext(ctx)
	now := time.Now().UTC()
	if t.Now != nil {
		now = t.Now().UTC()
	}
	span := &activeSpan{tracer: t, ctx: childCtx, span: Span{Name: strings.TrimSpace(name), TraceID: spanContext.TraceID, SpanID: spanContext.SpanID, ParentSpanID: parent.SpanID, StartTime: now, Attributes: boundedAttributes(attributes)}}
	return childCtx, func(status, message string) error {
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
		value, ok := security.RedactAttribute(key, value)
		if !ok {
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
