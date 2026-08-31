// Package config validates deployment boundaries before the API starts.
//
// The repository ships a useful single-node reference profile, but it does
// not ship PostgreSQL/RLS, NATS, Temporal, S3, OIDC, or a sandboxed runner.
// Production-shaped configuration must therefore fail closed instead of
// silently falling back to local implementations.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Provider           string
	AuthMode           string
	Profile            string
	PersistenceBackend string
	EventBackend       string
	WorkflowBackend    string
	ArtifactBackend    string
	AuthBackend        string
	SecretStore        string
	RunnerMode         string
	SourceControl      string
	CIPipeline         string
	ReplicaCount       int
}

func FromEnv() Config {
	return Config{
		Provider:           strings.ToLower(envOr("ADRO_PROVIDER", "local")),
		AuthMode:           strings.ToLower(envOr("ADRO_AUTH_MODE", "optional")),
		Profile:            envOr("ADRO_PROFILE", "single-node"),
		PersistenceBackend: envOr("ADRO_PERSISTENCE_BACKEND", "file"),
		EventBackend:       envOr("ADRO_EVENT_BACKEND", "memory"),
		WorkflowBackend:    envOr("ADRO_WORKFLOW_BACKEND", "in-process"),
		ArtifactBackend:    envOr("ADRO_ARTIFACT_BACKEND", "filesystem"),
		AuthBackend:        envOr("ADRO_AUTH_BACKEND", "local"),
		SecretStore:        envOr("ADRO_SECRET_STORE", "environment"),
		RunnerMode:         envOr("ADRO_RUNNER_MODE", "argv"),
		SourceControl:      envOr("ADRO_SOURCE_CONTROL", "none"),
		CIPipeline:         envOr("ADRO_CI_BACKEND", "none"),
		ReplicaCount:       envIntOr("ADRO_REPLICA_COUNT", 1),
	}
}

func Validate(c Config) error {
	c.Provider = strings.ToLower(strings.TrimSpace(c.Provider))
	if c.Provider == "" {
		c.Provider = "local"
	}
	if err := oneOf("ADRO_PROVIDER", c.Provider, "local"); err != nil {
		return err
	}
	c.AuthMode = strings.ToLower(strings.TrimSpace(c.AuthMode))
	if c.AuthMode == "" {
		c.AuthMode = "optional"
	}
	if err := oneOf("ADRO_AUTH_MODE", c.AuthMode, "optional", "required"); err != nil {
		return err
	}
	c.Profile = strings.ToLower(strings.TrimSpace(c.Profile))
	if c.Profile == "" {
		return fmt.Errorf("ADRO_PROFILE is required")
	}
	if c.Profile != "single-node" && c.Profile != "production" && c.Profile != "ha" {
		return fmt.Errorf("unsupported ADRO_PROFILE %q", c.Profile)
	}
	if c.ReplicaCount < 1 {
		return fmt.Errorf("ADRO_REPLICA_COUNT must be at least 1")
	}
	if c.Profile == "single-node" && c.ReplicaCount != 1 {
		return fmt.Errorf("single-node profile requires ADRO_REPLICA_COUNT=1")
	}

	if err := oneOf("ADRO_PERSISTENCE_BACKEND", c.PersistenceBackend, "file", "postgres"); err != nil {
		return err
	}
	if err := oneOf("ADRO_EVENT_BACKEND", c.EventBackend, "memory", "nats"); err != nil {
		return err
	}
	if err := oneOf("ADRO_WORKFLOW_BACKEND", c.WorkflowBackend, "in-process", "temporal"); err != nil {
		return err
	}
	if err := oneOf("ADRO_ARTIFACT_BACKEND", c.ArtifactBackend, "filesystem", "s3"); err != nil {
		return err
	}
	if err := oneOf("ADRO_AUTH_BACKEND", c.AuthBackend, "local", "oidc", "mtls"); err != nil {
		return err
	}
	if err := oneOf("ADRO_SECRET_STORE", c.SecretStore, "environment", "external"); err != nil {
		return err
	}
	if err := oneOf("ADRO_RUNNER_MODE", c.RunnerMode, "argv", "rootless", "container", "vm"); err != nil {
		return err
	}
	if err := oneOf("ADRO_SOURCE_CONTROL", c.SourceControl, "none", "git"); err != nil {
		return err
	}
	if err := oneOf("ADRO_CI_BACKEND", c.CIPipeline, "none", "external"); err != nil {
		return err
	}

	production := c.Profile == "production" || c.Profile == "ha" || c.ReplicaCount > 1
	if !production {
		local := []struct {
			name, got, want string
		}{
			{"ADRO_PERSISTENCE_BACKEND", c.PersistenceBackend, "file"},
			{"ADRO_EVENT_BACKEND", c.EventBackend, "memory"},
			{"ADRO_WORKFLOW_BACKEND", c.WorkflowBackend, "in-process"},
			{"ADRO_ARTIFACT_BACKEND", c.ArtifactBackend, "filesystem"},
			{"ADRO_AUTH_BACKEND", c.AuthBackend, "local"},
			{"ADRO_SECRET_STORE", c.SecretStore, "environment"},
			{"ADRO_RUNNER_MODE", c.RunnerMode, "argv"},
			{"ADRO_SOURCE_CONTROL", c.SourceControl, "none"},
			{"ADRO_CI_BACKEND", c.CIPipeline, "none"},
		}
		for _, item := range local {
			if !strings.EqualFold(strings.TrimSpace(item.got), item.want) {
				return fmt.Errorf("blocked: %s=%q selects an adapter that is not shipped; the single-node implementation requires %s", item.name, item.got, item.want)
			}
		}
		return nil
	}
	// These are intentionally explicit requirements. The corresponding SPIs
	// exist, but adapters are not included in this repository.
	required := []struct {
		name, got, want string
	}{
		{"ADRO_PERSISTENCE_BACKEND", c.PersistenceBackend, "postgres"},
		{"ADRO_EVENT_BACKEND", c.EventBackend, "nats"},
		{"ADRO_WORKFLOW_BACKEND", c.WorkflowBackend, "temporal"},
		{"ADRO_ARTIFACT_BACKEND", c.ArtifactBackend, "s3"},
		{"ADRO_AUTH_BACKEND", c.AuthBackend, "oidc or mtls"},
		{"ADRO_SECRET_STORE", c.SecretStore, "external"},
		{"ADRO_RUNNER_MODE", c.RunnerMode, "rootless, container, or vm"},
		{"ADRO_SOURCE_CONTROL", c.SourceControl, "git"},
		{"ADRO_CI_BACKEND", c.CIPipeline, "external"},
	}
	for _, item := range required {
		if !matchesRequired(item.name, item.got) {
			return fmt.Errorf("blocked: %s=%q is not a production boundary; configure %s (the adapter is not shipped in this release)", item.name, item.got, item.want)
		}
	}
	return fmt.Errorf("blocked: production/HA adapters are declared but not shipped; use the single-node profile or install and conformance-test every external adapter")
}

func matchesRequired(name, value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	switch name {
	case "ADRO_AUTH_BACKEND":
		return value == "oidc" || value == "mtls"
	case "ADRO_RUNNER_MODE":
		return value == "rootless" || value == "container" || value == "vm"
	default:
		return value == map[string]string{
			"ADRO_PERSISTENCE_BACKEND": "postgres",
			"ADRO_EVENT_BACKEND":       "nats",
			"ADRO_WORKFLOW_BACKEND":    "temporal",
			"ADRO_ARTIFACT_BACKEND":    "s3",
			"ADRO_SECRET_STORE":        "external",
			"ADRO_SOURCE_CONTROL":      "git",
			"ADRO_CI_BACKEND":          "external",
		}[name]
	}
}

func oneOf(name, value string, allowed ...string) error {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range allowed {
		if value == candidate {
			return nil
		}
	}
	return fmt.Errorf("%s=%q is invalid (allowed: %s)", name, value, strings.Join(allowed, ", "))
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envIntOr(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return -1
	}
	return parsed
}
