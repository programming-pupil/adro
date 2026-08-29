// Package integrations contains provider-neutral extension interfaces.
package integrations

import "context"

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
