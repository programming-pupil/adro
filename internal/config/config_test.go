package config

import (
	"strings"
	"testing"
)

func TestSingleNodeDefaultsValidate(t *testing.T) {
	if err := Validate(FromEnv()); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
}

func TestUnknownProviderFailsClosed(t *testing.T) {
	c := FromEnv()
	c.Provider = "typo"
	if err := Validate(c); err == nil || !strings.Contains(err.Error(), "ADRO_PROVIDER") {
		t.Fatalf("expected unknown provider to be rejected, got %v", err)
	}
}

func TestMockProviderIsRejectedForRuntime(t *testing.T) {
	c := FromEnv()
	c.Provider = "mock"
	if err := Validate(c); err == nil || !strings.Contains(err.Error(), "ADRO_PROVIDER") {
		t.Fatalf("mock runtime must be rejected, got %v", err)
	}
}

func TestAuthModeIsDocumentedAndUnknownValuesFailClosed(t *testing.T) {
	c := FromEnv()
	c.AuthMode = "requred"
	if err := Validate(c); err == nil || !strings.Contains(err.Error(), "ADRO_AUTH_MODE") {
		t.Fatalf("expected unknown auth mode to be rejected, got %v", err)
	}
	c.AuthMode = "REQUIRED"
	if err := Validate(c); err != nil {
		t.Fatalf("case-insensitive required auth mode should validate: %v", err)
	}
}

func TestFromEnvNormalizesProviderCase(t *testing.T) {
	t.Setenv("ADRO_PROVIDER", "MULTICA")
	if got := FromEnv().Provider; got != "multica" {
		t.Fatalf("provider was not normalized: %q", got)
	}
}

func TestProductionFailsClosedOnReferenceBackends(t *testing.T) {
	c := FromEnv()
	c.Profile = "production"
	if err := Validate(c); err == nil {
		t.Fatal("production reference profile must be blocked")
	}
}

func TestProductionRequiresEveryBoundary(t *testing.T) {
	c := Config{Profile: "production", PersistenceBackend: "postgres", EventBackend: "nats", WorkflowBackend: "temporal", ArtifactBackend: "s3", AuthBackend: "oidc", SecretStore: "external", RunnerMode: "rootless", SourceControl: "git", CIPipeline: "external", ReplicaCount: 2}
	if err := Validate(c); err == nil {
		t.Fatal("unshipped production adapters must remain blocked")
	}
}

func TestSingleNodeRejectsMultipleReplicas(t *testing.T) {
	c := FromEnv()
	c.ReplicaCount = 2
	if err := Validate(c); err == nil {
		t.Fatal("single-node cannot run multiple replicas")
	}
}

func TestSingleNodeRejectsUnshippedAdapters(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"postgres", func(c *Config) { c.PersistenceBackend = "postgres" }},
		{"nats", func(c *Config) { c.EventBackend = "nats" }},
		{"temporal", func(c *Config) { c.WorkflowBackend = "temporal" }},
		{"s3", func(c *Config) { c.ArtifactBackend = "s3" }},
		{"oidc", func(c *Config) { c.AuthBackend = "oidc" }},
		{"secret store", func(c *Config) { c.SecretStore = "external" }},
		{"sandbox", func(c *Config) { c.RunnerMode = "rootless" }},
		{"git", func(c *Config) { c.SourceControl = "git" }},
		{"ci", func(c *Config) { c.CIPipeline = "external" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := FromEnv()
			tt.mutate(&c)
			err := Validate(c)
			if err == nil || !strings.Contains(err.Error(), "not shipped") {
				t.Fatalf("expected an unshipped-adapter error, got %v", err)
			}
		})
	}
}
