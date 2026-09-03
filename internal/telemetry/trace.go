// Package telemetry implements the transport-neutral part of OpenTelemetry
// context propagation used by ADRO. It intentionally owns no exporter: every
// process boundary carries the W3C Trace Context headers, while operators may
// attach any OpenTelemetry SDK/exporter without changing domain contracts.
package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const (
	TraceParentHeader = "traceparent"
	TraceStateHeader  = "tracestate"
)

type SpanContext struct {
	TraceID    string `json:"trace_id"`
	SpanID     string `json:"span_id"`
	TraceFlags string `json:"trace_flags"`
	TraceState string `json:"tracestate,omitempty"`
	Remote     bool   `json:"remote,omitempty"`
}

type contextKey struct{}

func (s SpanContext) Valid() bool {
	return validHexID(s.TraceID, 32) && validHexID(s.SpanID, 16) && validFlags(s.TraceFlags) && validTraceState(s.TraceState)
}

func (s SpanContext) TraceParent() string {
	if !s.Valid() {
		return ""
	}
	return "00-" + s.TraceID + "-" + s.SpanID + "-" + strings.ToLower(s.TraceFlags)
}

func ParseTraceParent(value, state string) (SpanContext, error) {
	value = strings.TrimSpace(value)
	parts := strings.Split(value, "-")
	if len(parts) != 4 || len(parts[0]) != 2 || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return SpanContext{}, errors.New("traceparent must contain version, trace-id, parent-id and flags")
	}
	version := strings.ToLower(parts[0])
	if version == "ff" || !validHex(version, 2) {
		return SpanContext{}, errors.New("traceparent version is invalid")
	}
	if version == "00" && len(value) != 55 {
		return SpanContext{}, errors.New("traceparent version 00 has an invalid length")
	}
	span := SpanContext{TraceID: strings.ToLower(parts[1]), SpanID: strings.ToLower(parts[2]), TraceFlags: strings.ToLower(parts[3]), TraceState: strings.TrimSpace(state), Remote: true}
	if !span.Valid() {
		return SpanContext{}, errors.New("traceparent contains an invalid or all-zero identifier")
	}
	return span, nil
}

func NewRoot() SpanContext {
	return SpanContext{TraceID: randomHex(16), SpanID: randomHex(8), TraceFlags: "01"}
}

func Child(parent SpanContext) SpanContext {
	if !parent.Valid() {
		return NewRoot()
	}
	return SpanContext{TraceID: parent.TraceID, SpanID: randomHex(8), TraceFlags: parent.TraceFlags, TraceState: parent.TraceState}
}

// StartSpan creates a child of the span stored in ctx, or a new root when no
// valid span exists. It is the common boundary primitive used by HTTP,
// orchestration workers, providers and subprocess launchers.
func StartSpan(ctx context.Context) (context.Context, SpanContext) {
	parent, ok := FromContext(ctx)
	span := NewRoot()
	if ok {
		span = Child(parent)
	}
	return ContextWithSpan(ctx, span), span
}

// StartRemoteSpan accepts an explicit W3C carrier when present. Invalid
// carriers fail closed to a new trace instead of retaining attacker-controlled
// identifiers. The error remains observable to tests/diagnostics without
// turning a malformed optional tracing header into an application failure.
func StartRemoteSpan(ctx context.Context, traceParent, traceState string) (context.Context, SpanContext, error) {
	traceParent = strings.TrimSpace(traceParent)
	if traceParent == "" {
		childCtx, span := StartSpan(ctx)
		return childCtx, span, nil
	}
	parent, err := ParseTraceParent(traceParent, traceState)
	if err != nil {
		span := NewRoot()
		return ContextWithSpan(ctx, span), span, err
	}
	span := Child(parent)
	return ContextWithSpan(ctx, span), span, nil
}

func ContextWithSpan(ctx context.Context, span SpanContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if !span.Valid() {
		return ctx
	}
	span.Remote = false
	return context.WithValue(ctx, contextKey{}, span)
}

func FromContext(ctx context.Context) (SpanContext, bool) {
	if ctx == nil {
		return SpanContext{}, false
	}
	span, ok := ctx.Value(contextKey{}).(SpanContext)
	return span, ok && span.Valid()
}

func InjectHeader(header http.Header, ctx context.Context) {
	span, ok := FromContext(ctx)
	if !ok || header == nil {
		return
	}
	header.Set(TraceParentHeader, span.TraceParent())
	if span.TraceState != "" {
		header.Set(TraceStateHeader, span.TraceState)
	} else {
		header.Del(TraceStateHeader)
	}
}

func TraceID(ctx context.Context) string {
	span, _ := FromContext(ctx)
	return span.TraceID
}

func Carrier(ctx context.Context) (string, string) {
	span, ok := FromContext(ctx)
	if !ok {
		return "", ""
	}
	return span.TraceParent(), span.TraceState
}

func Environment(ctx context.Context) []string {
	parent, state := Carrier(ctx)
	if parent == "" {
		return nil
	}
	values := []string{"TRACEPARENT=" + parent}
	if state != "" {
		values = append(values, "TRACESTATE="+state)
	}
	return values
}

func validHexID(value string, length int) bool {
	if !validHex(value, length) {
		return false
	}
	for _, ch := range value {
		if ch != '0' {
			return true
		}
	}
	return false
}

func validFlags(value string) bool { return validHex(value, 2) }

func validHex(value string, length int) bool {
	if len(value) != length || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validTraceState(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 512 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	members := strings.Split(value, ",")
	if len(members) > 32 {
		return false
	}
	for _, member := range members {
		member = strings.TrimSpace(member)
		parts := strings.SplitN(member, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || len(parts[0]) > 256 || len(parts[1]) > 256 {
			return false
		}
		if strings.ContainsAny(parts[0]+parts[1], " ,\t") {
			return false
		}
	}
	return true
}

func randomHex(bytes int) string {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		panic(fmt.Sprintf("telemetry random source unavailable: %v", err))
	}
	return hex.EncodeToString(buffer)
}
