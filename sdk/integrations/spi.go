// Package integrations contains provider-neutral extension interfaces.
package integrations

import "context"

type GenerationRequest struct {
	SessionID       string
	ParentSessionID string
	Requirement     string
	DesignDoc       string
	BaselineCommit  string
	CurrentCommit   string
	FailureLogs     []string
	RepairAttempt   int
}

type GenerationResult struct {
	Commit            string
	PatchArtifactURI  string
	ProviderTaskID    string
	ProviderSessionID string
}

// CodeGenerator performs initial development and incremental repair. A repair
// implementation must reject a result whose ProviderSessionID differs from
// ParentSessionID.
type CodeGenerator interface {
	Generate(context.Context, GenerationRequest) (GenerationResult, error)
}

type TestRequest struct {
	SessionID        string
	Commit           string
	Kind             string
	CoverageTarget   float64
	PreviouslyPassed []string
}

type TestResult struct {
	Passed      bool
	Coverage    float64
	PassedTests []string
	FailedTests []string
	ErrorLogURI string
	ReportURI   string
}

// TestRunner owns both unit and integration evidence. Pipeline policy, rather
// than the adapter, decides whether a result advances, retries, or returns to
// development.
type TestRunner interface {
	Run(context.Context, TestRequest) (TestResult, error)
}

type SourceControl interface {
	Clone(context.Context, string, string, string) error
	Diff(context.Context, string, string) (string, error)
	Commit(context.Context, string, string, string) (string, error)
}
type CIPipeline interface {
	Start(context.Context, string, string) (string, error)
	Status(context.Context, string) (string, error)
}
type Deployer interface {
	Deploy(context.Context, string, string, string) (string, error)
	Rollback(context.Context, string) error
}
type EvidenceCollector interface {
	Collect(context.Context, map[string]any) (map[string]any, error)
}
type Notifier interface {
	Notify(context.Context, string, string) (string, error)
}
type SecretStore interface {
	Resolve(context.Context, string) (string, error)
}
type IdentityProvider interface {
	Subject(context.Context, string) (string, error)
}
