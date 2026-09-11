package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkbenchStaticHandlerRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := newWorkbenchHandler(root, "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}

	rootResponse := httptest.NewRecorder()
	handler.ServeHTTP(rootResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	if rootResponse.Code != http.StatusOK || rootResponse.Body.String() != "ok" {
		t.Fatalf("root response: code=%d body=%q", rootResponse.Code, rootResponse.Body.String())
	}
	traversalResponse := httptest.NewRecorder()
	handler.ServeHTTP(traversalResponse, httptest.NewRequest(http.MethodGet, "/../secret", nil))
	if traversalResponse.Code != http.StatusBadRequest {
		t.Fatalf("traversal response code=%d", traversalResponse.Code)
	}
}

func TestWorkbenchHandlerProxiesAPI(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/login" || r.Header.Get("X-Test") != "yes" {
			t.Fatalf("unexpected request: %s %s headers=%v", r.Method, r.URL.Path, r.Header)
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "ok"})
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("proxied"))
	}))
	defer upstream.Close()
	handler, err := newWorkbenchHandler(t.TempDir(), upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login", nil)
	req.Header.Set("X-Test", "yes")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusCreated || response.Body.String() != "proxied" {
		t.Fatalf("proxy response: code=%d body=%q", response.Code, response.Body.String())
	}
	if response.Header().Get("Set-Cookie") == "" {
		t.Fatal("proxy did not preserve Set-Cookie")
	}
}

func TestWorkbenchHandlerRejectsInvalidAPIURL(t *testing.T) {
	if _, err := newWorkbenchHandler(t.TempDir(), "not-a-url"); err == nil {
		t.Fatal("expected invalid API URL error")
	}
}
