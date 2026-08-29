// adro-web serves the ADRO browser workbench without a container runtime.
package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	addr := flag.String("addr", ":8081", "HTTP listen address")
	root := flag.String("root", filepath.Join("apps", "web"), "workbench document root")
	flag.Parse()

	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		slog.Error("resolve workbench root", "error", err)
		os.Exit(1)
	}
	if info, err := os.Stat(absoluteRoot); err != nil || !info.IsDir() {
		if err == nil {
			err = os.ErrInvalid
		}
		slog.Error("workbench root is unavailable", "root", absoluteRoot, "error", err)
		os.Exit(1)
	}

	files := http.FileServer(http.Dir(absoluteRoot))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "..") {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		files.ServeHTTP(w, r)
	})

	slog.Info("adro web listening", "addr", *addr, "root", absoluteRoot)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		slog.Error("web server stopped", "error", err)
		os.Exit(1)
	}
}
