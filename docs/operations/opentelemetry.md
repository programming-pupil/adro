# OpenTelemetry trace export

ADRO uses the official OpenTelemetry Go SDK and the standard OTLP/HTTP protobuf exporter. Runtime code creates W3C Trace Context carriers through `internal/telemetry`; the API process owns the SDK provider, injects it into orchestration executors, and flushes its batch processor during bounded process shutdown.

## Configuration

The preferred configuration is the standard OpenTelemetry environment contract supported by the OTLP/HTTP exporter:

```bash
export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=https://collector.example.test/v1/traces
# or use the base OTLP endpoint; the exporter appends /v1/traces
export OTEL_EXPORTER_OTLP_ENDPOINT=https://collector.example.test
```

Standard OTLP headers, TLS, timeout, compression, and protocol variables are interpreted by the upstream exporter. ADRO forces protobuf encoding and does not implement a private JSON wire format.

`ADRO_OTEL_EXPORTER_OTLP_ENDPOINT` remains a compatibility option for existing deployments. Its value must be a complete `http` or `https` traces URL without embedded credentials, a query, or a fragment. Set it to `disabled` to explicitly disable trace export even when standard endpoint variables are present. Credentials belong in the standard OTLP header or certificate settings and must not be embedded in URLs.

When no endpoint is configured, ADRO uses an SDK-backed local propagation provider. It creates child span contexts for event/provider correlation but starts no exporter or background processor.

## Lifecycle and failure behavior

The API creates one tracer provider at startup and shares it with request handling and orchestration. It does not create a provider per request or per provider call. Invalid compatibility configuration fails startup readiness closed; ADRO does not silently fall back to an exporter with ambiguous or unsafe settings.

On termination, `cmd/adro-api` first stops accepting HTTP work, then shuts down the execution provider, then calls `Server.Shutdown` with the process shutdown deadline. This flushes and stops the OpenTelemetry batch processor. A shutdown error is logged as a structured process error.

## Data handling

Span attributes are bounded to 32 entries and 256 bytes per value. Keys that conventionally contain high-cardinality identifiers (`*.id`, `*_id`, `session_id`, and `comment_id`) are rejected by the tracing adapter. Prompts, tool output, file contents, secrets, and full durable identifiers must not be added as attributes.

The current implementation covers standard trace export and W3C propagation. The complete task/session/turn/step/model/tool/delegation span hierarchy, metrics, log bridge, collector fault matrix, and Runtime Inspector trace waterfall remain separate tracked work and are not claimed stable by this document.
