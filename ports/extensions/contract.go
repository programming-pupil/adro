// Package extensions defines the immutable execution grant consumed by the
// runtime extension supervisor. Control-plane registries validate signed
// installation records and return this narrow grant; the runtime never reads
// registry storage or trusts caller-supplied plugin policy directly.
package extensions

import (
	"context"
	"time"
)

type ExecutionMode string

const (
	ExecutionInProcess    ExecutionMode = "in_process"
	ExecutionOutOfProcess ExecutionMode = "out_of_process"
	ExecutionWASI         ExecutionMode = "wasi"
)

func (m ExecutionMode) Valid() bool {
	switch m {
	case ExecutionInProcess, ExecutionOutOfProcess, ExecutionWASI:
		return true
	default:
		return false
	}
}

type FilePermission struct {
	Path       string   `json:"path"`
	Operations []string `json:"operations"`
}

type NetworkPermission struct {
	Domain   string `json:"domain,omitempty"`
	IP       string `json:"ip,omitempty"`
	CIDR     string `json:"cidr,omitempty"`
	Ports    []int  `json:"ports"`
	Protocol string `json:"protocol"`
	Purpose  string `json:"purpose"`
}

type SecretPermission struct {
	Name        string `json:"name"`
	Destination string `json:"destination"`
	Purpose     string `json:"purpose"`
}

// DataEgressPermission is a signed obligation carried into the runtime grant.
// MaxSensitivity stays a string at this port boundary so the public contract
// does not depend on an internal security package. The control plane validates
// the value before issuing the grant.
type DataEgressPermission struct {
	Destination    string `json:"destination"`
	Purpose        string `json:"purpose"`
	MaxSensitivity string `json:"max_sensitivity"`
}

// Manifest is the execution-relevant subset of a signed extension manifest.
// It must be produced from the registry's stored, signature-verified record.
type Manifest struct {
	ID                      string                 `json:"id"`
	Name                    string                 `json:"name"`
	Publisher               string                 `json:"publisher"`
	Version                 string                 `json:"version"`
	ProtocolVersion         string                 `json:"protocol_version"`
	AdapterVersion          string                 `json:"adapter_version"`
	SchemaDigest            string                 `json:"schema_digest"`
	ArtifactDigest          string                 `json:"artifact_digest"`
	ExecutionMode           ExecutionMode          `json:"execution_mode"`
	MaxMessageBytes         int64                  `json:"max_message_bytes"`
	Capabilities            []string               `json:"capabilities"`
	Permissions             []string               `json:"permissions,omitempty"`
	FilePermissions         []FilePermission       `json:"file_permissions,omitempty"`
	NetworkPermissions      []NetworkPermission    `json:"network_permissions,omitempty"`
	SecretPermissions       []SecretPermission     `json:"secret_permissions,omitempty"`
	DataEgressPermissions   []DataEgressPermission `json:"data_egress_permissions,omitempty"`
	CompatibleSchemaDigests []string               `json:"compatible_schema_digests,omitempty"`
}

// Installation identifies the signed registry record and carries the
// registry-issued execution manifest. State must be active at authorization.
type Installation struct {
	Manifest    Manifest  `json:"manifest"`
	TenantID    string    `json:"tenant_id"`
	WorkspaceID string    `json:"workspace_id"`
	Digest      string    `json:"digest"`
	Signature   string    `json:"signature"`
	KeyID       string    `json:"key_id"`
	State       string    `json:"state"`
	ActivatedAt time.Time `json:"activated_at,omitempty"`
}

// Authorizer resolves an untrusted installation identity to the canonical
// execution grant stored by the signed registry. Implementations must ignore
// caller-supplied capabilities and return the stored manifest.
type Authorizer interface {
	AuthorizeExecution(context.Context, Installation) (Installation, error)
}

type AuthorizeFunc func(context.Context, Installation) (Installation, error)

func (f AuthorizeFunc) AuthorizeExecution(ctx context.Context, item Installation) (Installation, error) {
	return f(ctx, item)
}

// Quarantiner removes an unhealthy installation from active scheduling.
type Quarantiner interface {
	QuarantineExecution(context.Context, Installation, string) error
}

type QuarantineFunc func(context.Context, Installation, string) error

func (f QuarantineFunc) QuarantineExecution(ctx context.Context, item Installation, reason string) error {
	return f(ctx, item, reason)
}
