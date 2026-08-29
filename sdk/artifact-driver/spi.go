// Package artifactdriver defines the stable contract for independently
// packaged ArtifactStore drivers.
package artifactdriver

import (
	"context"
	"io"
)

type Key struct {
	TenantID, ArtifactID string
	Version              int64
}
type Capabilities struct{ Range, Multipart, ObjectLock, Encryption bool }
type ObjectMeta struct {
	Key                      Key
	MediaType, ContentSHA256 string
	SizeBytes                int64
}
type Driver interface {
	Capabilities(context.Context) (Capabilities, error)
	Put(context.Context, Key, io.Reader) (ObjectMeta, error)
	Open(context.Context, Key, int64, int64) (io.ReadCloser, ObjectMeta, error)
	Stat(context.Context, Key) (ObjectMeta, error)
	Health(context.Context) error
}
