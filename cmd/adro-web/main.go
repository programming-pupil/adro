// adro-web serves the ADRO browser workbench without a container runtime.
package main

import (
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	addr := flag.String("addr", ":8081", "HTTP listen address")
	root := flag.String("root", filepath.Join("apps", "web"), "workbench document root")
	api := flag.String("api", "http://127.0.0.1:8080", "ADRO API upstream URL")
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

	handler, err := newWorkbenchHandler(absoluteRoot, *api)
	if err != nil {
		slog.Error("configure API proxy", "api", *api, "error", err)
		os.Exit(1)
	}

	slog.Info("adro web listening", "addr", *addr, "root", absoluteRoot, "api", *api)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		slog.Error("web server stopped", "error", err)
		os.Exit(1)
	}
}

func newWorkbenchHandler(root, api string) (http.Handler, error) {
	upstream, err := url.Parse(api)
	if err != nil || upstream.Scheme == "" || upstream.Host == "" {
		return nil, &url.Error{Op: "parse", URL: api, Err: errInvalidAPIURL}
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	files := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "..") {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			proxy.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	}), nil
}

var errInvalidAPIURL = errors.New("API URL must include scheme and host")
