package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/adro-project/adro/internal/api"
	"github.com/adro-project/adro/internal/artifact"
	"github.com/adro-project/adro/internal/audit"
	"github.com/adro-project/adro/internal/config"
	"github.com/adro-project/adro/internal/events"
	"github.com/adro-project/adro/internal/provider"
	"github.com/adro-project/adro/internal/runner"
	"github.com/adro-project/adro/internal/store"
)

func main() {
	cfg := config.FromEnv()
	if err := config.Validate(cfg); err != nil {
		slog.Error("deployment configuration rejected", "error", err)
		os.Exit(1)
	}
	addr := flag.String("addr", ":8080", "HTTP listen address")
	artifactRoot := flag.String("artifact-root", "", "filesystem artifact root (defaults to ADRO_ARTIFACT_ROOT or ./var/artifacts)")
	flag.Parse()
	root := *artifactRoot
	if root == "" {
		root = os.Getenv("ADRO_ARTIFACT_ROOT")
	}
	if root == "" {
		root = filepath.Join("var", "artifacts")
	}
	fs, err := artifact.NewFileStore(root)
	if err != nil {
		slog.Error("artifact store", "error", err)
		os.Exit(1)
	}
	bus := events.NewBus()
	if eventPath := os.Getenv("ADRO_EVENT_STATE_FILE"); eventPath != "" {
		bus, err = events.NewPersistentBus(eventPath)
		if err != nil {
			slog.Error("load event state", "error", err)
			os.Exit(1)
		}
	}
	controlStore := store.NewMemory()
	if statePath := os.Getenv("ADRO_STATE_FILE"); statePath != "" {
		controlStore, err = store.NewPersistentMemory(statePath)
		if err != nil {
			slog.Error("load control-plane state", "error", err, "path", statePath)
			os.Exit(1)
		}
	}
	router := provider.NewAgentRouteResolver(provider.AgentRouteConfig{}, "")
	var routeErr error
	router, routeErr = provider.NewAgentRouteResolverFromEnv()
	if routeErr != nil {
		slog.Error("agent route configuration", "error", routeErr)
		os.Exit(1)
	}
	workRoot := os.Getenv("ADRO_WORK_ROOT")
	if workRoot == "" {
		workRoot = filepath.Join(root, "workspaces")
	}
	runStatePath := os.Getenv("ADRO_RUN_STATE_FILE")
	localExecutor, executorErr := provider.DiscoverLocalProvider(workRoot, bus)
	if executorErr == nil && runStatePath != "" {
		localExecutor, executorErr = provider.NewPersistentLocalProvider(localExecutor.Executable, localExecutor.Args, workRoot, runStatePath, bus)
	}
	if executorErr != nil {
		slog.Error("local executor discovery failed", "error", executorErr, "hint", "install claude or codex, or set ADRO_EXECUTOR")
		os.Exit(1)
	}
	var p provider.ExecutionProvider = localExecutor
	srv := api.NewWithRouting(controlStore, p, fs, bus, slog.Default(), router)
	if runnerPath := os.Getenv("ADRO_RUNNER_STATE_FILE"); runnerPath != "" {
		supervisor, supervisorErr := runner.NewPersistentSupervisor(runnerPath)
		if supervisorErr != nil {
			slog.Error("load runner state", "error", supervisorErr)
			os.Exit(1)
		}
		srv.Runners = supervisor
	}
	if auditPath := os.Getenv("ADRO_AUDIT_STATE_FILE"); auditPath != "" {
		ledger, ledgerErr := audit.NewPersistentLedger(auditPath)
		if ledgerErr != nil {
			slog.Error("load audit state", "error", ledgerErr)
			os.Exit(1)
		}
		srv.Audit = ledger
	}
	httpServer := &http.Server{Addr: *addr, Handler: withRequestLogging(srv.Routes()), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		slog.Info("adro api listening", "addr", *addr, "artifact_root", root)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "error", err)
	}
}

func withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("http request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds(), "request_id", w.Header().Get("X-Request-ID"))
	})
}
