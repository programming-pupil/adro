package main

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adro-project/adro/ports/eventstore"
)

func TestOpenRuntimeEventShadowIsExplicitAndSupportsSQLite(t *testing.T) {
	store, err := openRuntimeEventShadow("", "")
	if err != nil || store != nil {
		t.Fatalf("disabled store=%v err=%v", store, err)
	}
	for _, input := range [][2]string{{"sqlite", ""}, {"", "shadow.db"}, {"unknown", "shadow.db"}} {
		if store, err := openRuntimeEventShadow(input[0], input[1]); err == nil || store != nil {
			t.Fatalf("driver=%q dsn=%q store=%v err=%v", input[0], input[1], store, err)
		}
	}
	store, err = openRuntimeEventShadow("sqlite", filepath.Join(t.TempDir(), "shadow.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Head(t.Context(), "closed-shadow"); !errors.Is(err, eventstore.ErrClosed) {
		t.Fatalf("closed store error=%v", err)
	}
}

func TestParseRuntimeEventShadowTimeout(t *testing.T) {
	if timeout, err := parseRuntimeEventShadowTimeout(""); err != nil || timeout != 2*time.Second {
		t.Fatalf("default timeout=%s err=%v", timeout, err)
	}
	if timeout, err := parseRuntimeEventShadowTimeout("750ms"); err != nil || timeout != 750*time.Millisecond {
		t.Fatalf("configured timeout=%s err=%v", timeout, err)
	}
	for _, raw := range []string{"0s", "-1s", "invalid"} {
		if _, err := parseRuntimeEventShadowTimeout(raw); err == nil {
			t.Fatalf("timeout %q unexpectedly accepted", raw)
		}
	}
}

func TestRequestLoggingRecordsRejectedResponses(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	handler := withRequestLogging(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Request-ID", "generated\r\nforged")
		http.Error(w, "invalid", http.StatusUnprocessableEntity)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/invalid", nil))

	output := logs.String()
	for _, expected := range []string{`"level":"WARN"`, `"msg":"http request rejected"`, `"status":422`, `"request_id":"generatedforged"`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("request log missing %s: %s", expected, output)
		}
	}
}

func TestRequestLoggingRecoversPanicsAndReturnsProblem(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	handler := withRequestLogging(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("broken handler")
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/panic", nil))

	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"error_code":"internal_server_error"`) {
		t.Fatalf("panic response status=%d body=%s", response.Code, response.Body.String())
	}
	output := logs.String()
	for _, expected := range []string{`"msg":"http panic recovered"`, `"panic":"broken handler"`, `"stack":`, `"msg":"http request failed"`, `"status":500`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("panic log missing %s: %s", expected, output)
		}
	}
}

func TestRequestLogWriterPreservesStreamingInterfaces(t *testing.T) {
	response := httptest.NewRecorder()
	writer := &requestLogWriter{ResponseWriter: response}
	flusher, ok := any(writer).(http.Flusher)
	if !ok {
		t.Fatal("request writer does not implement http.Flusher")
	}
	flusher.Flush()
	if !writer.started || writer.status != http.StatusOK {
		t.Fatalf("flush state=%+v", writer)
	}
	written, err := io.Copy(writer, strings.NewReader("stream"))
	if err != nil || written != 6 || writer.bytes != 6 {
		t.Fatalf("stream written=%d bytes=%d err=%v", written, writer.bytes, err)
	}
}
