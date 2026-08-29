package artifact

import (
	"context"
	"errors"
	"fmt"
)

// Migrator copies immutable objects between ArtifactStore drivers and verifies
// the destination digest before the control-plane migration is advanced. A
// caller can invoke Copy in resumable batches and persist the counters in
// domain.ArtifactMigration.
type Migrator struct {
	Source      Store
	Destination Store
}

func (m Migrator) Copy(ctx context.Context, key Key) (ObjectMeta, error) {
	if m.Source == nil || m.Destination == nil {
		return ObjectMeta{}, errors.New("source and destination stores are required")
	}
	sourceMeta, err := m.Source.Stat(ctx, key)
	if err != nil {
		return ObjectMeta{}, err
	}
	reader, _, err := m.Source.Open(ctx, key, ByteRange{Start: 0, End: -1})
	if err != nil {
		return ObjectMeta{}, err
	}
	defer reader.Close()
	destinationMeta, err := m.Destination.Put(ctx, key, reader, PutOptions{MediaType: sourceMeta.MediaType, Immutable: sourceMeta.Immutable})
	if err != nil {
		return ObjectMeta{}, err
	}
	if destinationMeta.ContentSHA256 != sourceMeta.ContentSHA256 || destinationMeta.SizeBytes != sourceMeta.SizeBytes {
		return ObjectMeta{}, fmt.Errorf("artifact verification failed: source %s/%d, destination %s/%d", sourceMeta.ContentSHA256, sourceMeta.SizeBytes, destinationMeta.ContentSHA256, destinationMeta.SizeBytes)
	}
	return destinationMeta, nil
}
