// adro-web serves the ADRO browser workbench without a container runtime.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	return newWorkbenchHandlerWithDirectoryPicker(root, api, pickLocalDirectory)
}

type directoryPicker func(context.Context) (string, error)

func newWorkbenchHandlerWithDirectoryPicker(root, api string, pickDirectory directoryPicker) (http.Handler, error) {
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
		if r.URL.Path == "/_adro/directory-picker" {
			serveDirectoryPicker(w, r, pickDirectory)
			return
		}
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			proxy.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	}), nil
}

func serveDirectoryPicker(w http.ResponseWriter, r *http.Request, pickDirectory directoryPicker) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "POST is required"})
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "same-origin request required"})
		return
	}
	path, err := pickDirectory(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	path = filepath.Clean(strings.TrimSpace(path))
	info, err := os.Stat(path)
	if err != nil || !filepath.IsAbs(path) || !info.IsDir() {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "selected path is not an available absolute directory"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"path": path})
}

func pickLocalDirectory(ctx context.Context) (string, error) {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(ctx, "osascript", "-e", `POSIX path of (choose folder with prompt "Choose a project folder")`)
	case "windows":
		command = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", `Add-Type -AssemblyName System.Windows.Forms; $dialog = New-Object System.Windows.Forms.FolderBrowserDialog; if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $dialog.SelectedPath } else { exit 1 }`)
	default:
		if path, err := exec.LookPath("zenity"); err == nil {
			command = exec.CommandContext(ctx, path, "--file-selection", "--directory", "--title=Choose a project folder")
		} else if path, err := exec.LookPath("kdialog"); err == nil {
			command = exec.CommandContext(ctx, path, "--getexistingdirectory", filepath.Clean(os.Getenv("HOME")))
		} else {
			return "", errors.New("no supported native directory picker is installed")
		}
	}
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("directory selection cancelled or unavailable: %w", err)
	}
	path := strings.TrimSpace(string(output))
	if path == "" {
		return "", errors.New("directory selection returned no path")
	}
	return path, nil
}

var errInvalidAPIURL = errors.New("API URL must include scheme and host")
