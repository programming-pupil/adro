package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkbenchStaticHandlerRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	files := http.FileServer(http.Dir(root))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "..") {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		files.ServeHTTP(w, r)
	})

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
