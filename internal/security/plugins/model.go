package plugins

import (
	"errors"
	coreprovenance "github.com/adro-project/adro/core/provenance"
	extensionport "github.com/adro-project/adro/ports/extensions"
	"time"
)

const (
	registrySchemaVersion  = 2
	maxManifestMessageSize = 16 << 20
)

var (
	ErrNotFound      = errors.New("plugin not found")
	ErrConflict      = errors.New("plugin already installed")
	ErrUnverified    = errors.New("plugin manifest signature is not verified")
	ErrInvalid       = errors.New("invalid plugin manifest")
	ErrNotActive     = errors.New("plugin is not active")
	ErrKeyNotTrusted = errors.New("plugin signing key is not trusted")
	ErrKeyRevoked    = errors.New("plugin signing key is revoked")
	ErrNoRollback    = errors.New("plugin has no compatible rollback target")
	ErrIncompatible  = errors.New("plugin versions are incompatible")
)

type ExecutionMode string

const (
	ExecutionInProcess    ExecutionMode = "in_process"
	ExecutionOutOfProcess ExecutionMode = "out_of_process"
	ExecutionWASI         ExecutionMode = "wasi"
)

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

type DataEgressPermission struct {
	Destination    string                     `json:"destination"`
	Purpose        string                     `json:"purpose"`
	MaxSensitivity coreprovenance.Sensitivity `json:"max_sensitivity"`
}

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
	MinPlatform             string                 `json:"min_platform_version,omitempty"`
	MaxPlatform             string                 `json:"max_platform_version,omitempty"`
	Capabilities            []string               `json:"capabilities"`
	Permissions             []string               `json:"permissions,omitempty"`
	FilePermissions         []FilePermission       `json:"file_permissions,omitempty"`
	NetworkPermissions      []NetworkPermission    `json:"network_permissions,omitempty"`
	SecretPermissions       []SecretPermission     `json:"secret_permissions,omitempty"`
	DataEgressPermissions   []DataEgressPermission `json:"data_egress_permissions,omitempty"`
	CompatibleSchemaDigests []string               `json:"compatible_schema_digests,omitempty"`
}

type Installation struct {
	Manifest          Manifest  `json:"manifest"`
	TenantID          string    `json:"tenant_id"`
	WorkspaceID       string    `json:"workspace_id"`
	Digest            string    `json:"digest"`
	Signature         string    `json:"signature"`
	KeyID             string    `json:"key_id"`
	PublicKey         string    `json:"public_key,omitempty"`
	State             string    `json:"state"`
	HealthMessage     string    `json:"health_message,omitempty"`
	ConsecutiveErrors int       `json:"consecutive_errors"`
	InstalledAt       time.Time `json:"installed_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	ActivatedAt       time.Time `json:"activated_at,omitempty"`
	SupersededAt      time.Time `json:"superseded_at,omitempty"`
	LastHealthAt      time.Time `json:"last_health_at,omitempty"`
	Legacy            bool      `json:"legacy,omitempty"`
}

type InstallRequest struct {
	Manifest    Manifest `json:"manifest"`
	TenantID    string   `json:"tenant_id,omitempty"`
	WorkspaceID string   `json:"workspace_id,omitempty"`
	Digest      string   `json:"digest"`
	Signature   string   `json:"signature"`
	KeyID       string   `json:"key_id,omitempty"`
	// PublicKey is accepted only to derive KeyID for clients migrating from the
	// original API. The key must already exist in the registry trust store.
	PublicKey string `json:"public_key,omitempty"`
}

type KeyState string

const (
	KeyActive  KeyState = "active"
	KeyRetired KeyState = "retired"
	KeyRevoked KeyState = "revoked"
)

type TrustedKey struct {
	ID             string    `json:"id"`
	Publisher      string    `json:"publisher"`
	PublicKey      string    `json:"public_key"`
	State          KeyState  `json:"state"`
	AllowInProcess bool      `json:"allow_in_process"`
	AddedAt        time.Time `json:"added_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Replaces       string    `json:"replaces,omitempty"`
	RevokedAt      time.Time `json:"revoked_at,omitempty"`
	Reason         string    `json:"reason,omitempty"`
}

type TrustKeyRequest struct {
	ID             string `json:"id,omitempty"`
	Publisher      string `json:"publisher"`
	PublicKey      string `json:"public_key"`
	AllowInProcess bool   `json:"allow_in_process,omitempty"`
}

type KeyRotationRequest struct {
	CurrentKeyID   string `json:"current_key_id"`
	NewKeyID       string `json:"new_key_id,omitempty"`
	NewPublicKey   string `json:"new_public_key"`
	AllowInProcess bool   `json:"allow_in_process,omitempty"`
	Signature      string `json:"signature"`
}

var _ extensionport.Authorizer = (*Registry)(nil)

var _ extensionport.Quarantiner = (*Registry)(nil)
