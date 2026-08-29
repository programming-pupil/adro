// Package provider publishes the stable extension manifest shared by external
// execution adapters. Runtime execution uses the richer internal SPI.
package provider

type Manifest struct {
	Name               string   `json:"name"`
	ProtocolVersion    string   `json:"protocol_version"`
	MinPlatformVersion string   `json:"min_platform_version"`
	MaxPlatformVersion string   `json:"max_platform_version"`
	Capabilities       []string `json:"capabilities"`
	Permissions        []string `json:"permissions"`
}
type Health struct {
	Healthy bool   `json:"healthy"`
	Message string `json:"message,omitempty"`
}
type Adapter interface {
	Manifest() Manifest
	Health() Health
}
