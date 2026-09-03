package api

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"
)

// systemDiagnostics is intentionally secret-free. It gives an operator or a
// deployment probe enough information to distinguish a durable local profile
// from an unconfigured production boundary without exposing paths or tokens.
func (s *Server) systemDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	providerState := "unavailable"
	if s.Provider != nil {
		if health, err := s.Provider.Health(ctx); err == nil && health.Healthy {
			providerState = "healthy"
		} else if err == nil {
			providerState = "unhealthy"
		}
	}
	profile := strings.TrimSpace(os.Getenv("ADRO_PROFILE"))
	if profile == "" {
		profile = "single-node"
	}
	backends := map[string]string{
		"persistence":   envOrDefault("ADRO_PERSISTENCE_BACKEND", "file"),
		"events":        envOrDefault("ADRO_EVENT_BACKEND", "memory"),
		"orchestration": envOrDefault("ADRO_ORCHESTRATION_BACKEND", "file-single-node"),
		"workflow":      envOrDefault("ADRO_WORKFLOW_BACKEND", "in-process"),
		"artifacts":     envOrDefault("ADRO_ARTIFACT_BACKEND", "filesystem"),
		"memory":        envOrDefault("ADRO_MEMORY_BACKEND", "evidence-repository"),
		"auth":          envOrDefault("ADRO_AUTH_BACKEND", "local"),
		"secrets":       envOrDefault("ADRO_SECRET_STORE", "environment"),
		"runner":        envOrDefault("ADRO_RUNNER_MODE", "argv"),
		"source":        envOrDefault("ADRO_SOURCE_CONTROL", "none"),
		"ci":            envOrDefault("ADRO_CI_BACKEND", "none"),
	}
	state := map[string]bool{
		"control_plane_durable": os.Getenv("ADRO_STATE_FILE") != "",
		"events_durable":        os.Getenv("ADRO_EVENT_STATE_FILE") != "",
		"audit_durable":         os.Getenv("ADRO_AUDIT_STATE_FILE") != "",
		"provider_durable":      os.Getenv("ADRO_RUN_STATE_FILE") != "",
		"harness_durable":       s.Harness != nil && s.Harness.Durable(),
		"plugins_durable":       s.Plugins != nil && s.Plugins.Durable(),
		"memory_durable":        s.Memory != nil && strings.TrimSpace(os.Getenv("ADRO_MEMORY_STATE_FILE")) != "",
	}
	transcriptValid, compactionRecall := true, true
	if s.Harness != nil {
		for _, session := range s.Harness.ListSessions() {
			if probe, err := s.Harness.VerifyTranscript(session.ID); err != nil || !probe.Valid {
				transcriptValid = false
			}
			if probe, err := s.Harness.VerifyCompaction(session.ID); err != nil || !probe.RecallVerified {
				compactionRecall = false
			}
		}
	}
	state["harness_transcript_valid"] = transcriptValid
	state["harness_compaction_recall_verified"] = compactionRecall
	orchestrationProfile := "unavailable"
	if s.Orchestration != nil {
		if profiled, ok := s.Orchestration.(interface{ Profile() string }); ok {
			orchestrationProfile = profiled.Profile()
		} else {
			orchestrationProfile = "custom"
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"profile":               profile,
		"provider_state":        providerState,
		"backends":              backends,
		"durability":            state,
		"recovery_worker":       s.recoveryRunning(),
		"orchestration_profile": orchestrationProfile,
		"checked_at":            time.Now().UTC(),
		"production_ready":      false,
	})
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func (s *Server) recoveryRunning() bool {
	if s == nil {
		return false
	}
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	return s.recoveryStarted
}
