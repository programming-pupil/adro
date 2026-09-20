// Package contract contains the shared, adapter-independent projection rules.
// It deliberately owns no storage state so SQLite, PostgreSQL and reference
// stores cannot drift on tenant, digest or CAS semantics.
package contract

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/adro-project/adro/ports/projection"
	"github.com/adro-project/adro/ports/scope"
)

const DefaultMaxPayload int64 = 64 << 20

func RequireTenant(ctx context.Context, tenantID string) error {
	scoped, err := scope.Tenant(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(tenantID) != scoped {
		return projection.ErrTenantMismatch
	}
	return nil
}

func ValidateIdentity(tenantID, projectionName, key string) error {
	if canonical(tenantID) == "" || canonical(projectionName) == "" || canonical(key) == "" {
		return errors.New("projection tenant, name and key are required")
	}
	if strings.ContainsAny(tenantID+projectionName+key, "\x00\r\n") {
		return errors.New("projection tenant, name and key contain a control character")
	}
	return nil
}

func ValidateForWrite(item projection.Record, tenantID string, maxPayload int64) error {
	if err := ValidateIdentity(item.TenantID, item.Projection, item.Key); err != nil {
		return err
	}
	if strings.TrimSpace(tenantID) == "" || item.TenantID != strings.TrimSpace(tenantID) {
		return projection.ErrTenantMismatch
	}
	if item.Version < 1 || item.SourceSequence < 1 || canonical(item.SourceStream) == "" {
		return errors.New("projection version and source identity are required")
	}
	if maxPayload <= 0 {
		maxPayload = DefaultMaxPayload
	}
	if int64(len(item.Payload)) > maxPayload {
		return projection.ErrLimit
	}
	if !ValidDigest(item.Digest) || !strings.EqualFold(item.Digest, Digest(item.Payload)) {
		return projection.ErrCorrupt
	}
	return nil
}

func ValidateStored(item projection.Record, tenantID string, maxPayload int64) error {
	if err := ValidateForWrite(item, tenantID, maxPayload); err != nil {
		return err
	}
	if item.UpdatedAt.IsZero() {
		return projection.ErrCorrupt
	}
	return nil
}

func ValidateOffset(item projection.Offset, tenantID string, stored bool) error {
	if err := ValidateIdentity(item.TenantID, item.Projection, item.PartitionID); err != nil {
		return err
	}
	if strings.TrimSpace(tenantID) == "" || item.TenantID != strings.TrimSpace(tenantID) {
		return projection.ErrTenantMismatch
	}
	if item.LastSequence < 0 || !ValidDigest(item.ProjectionDigest) {
		return projection.ErrCorrupt
	}
	if stored && item.UpdatedAt.IsZero() {
		return projection.ErrCorrupt
	}
	return nil
}

func NormalizeOffset(item projection.Offset, now time.Time) projection.Offset {
	item.TenantID = strings.TrimSpace(item.TenantID)
	item.Projection = strings.TrimSpace(item.Projection)
	item.PartitionID = strings.TrimSpace(item.PartitionID)
	item.ProjectionDigest = strings.ToLower(strings.TrimSpace(item.ProjectionDigest))
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now.UTC()
	} else {
		item.UpdatedAt = item.UpdatedAt.UTC()
	}
	return item
}

func Digest(payload []byte) string {
	return projection.Digest(payload)
}

func ValidDigest(value string) bool {
	if len(strings.TrimSpace(value)) != 64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimSpace(value))
	return err == nil
}

func Equivalent(a, b projection.Record) bool {
	return a.TenantID == b.TenantID &&
		a.Projection == b.Projection &&
		a.Key == b.Key &&
		a.Version == b.Version &&
		a.SourceStream == b.SourceStream &&
		a.SourceSequence == b.SourceSequence &&
		strings.EqualFold(a.Digest, b.Digest) &&
		string(a.Payload) == string(b.Payload)
}

func Clone(item projection.Record) projection.Record {
	item.Payload = append([]byte(nil), item.Payload...)
	return item
}

func Normalize(item projection.Record, now time.Time) projection.Record {
	item.TenantID = strings.TrimSpace(item.TenantID)
	item.Projection = strings.TrimSpace(item.Projection)
	item.Key = strings.TrimSpace(item.Key)
	item.SourceStream = strings.TrimSpace(item.SourceStream)
	item.Digest = strings.ToLower(strings.TrimSpace(item.Digest))
	item.Payload = append([]byte(nil), item.Payload...)
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now.UTC()
	} else {
		item.UpdatedAt = item.UpdatedAt.UTC()
	}
	return item
}

func canonical(value string) string {
	return strings.TrimSpace(value)
}

// ValidateBatch applies backend-independent checks before a transactional
// projection page is executed. Mutations must remain in source order.
func ValidateBatch(batch projection.Batch, tenantID string, maxPayload int64) error {
	if err := ValidateIdentity(batch.TenantID, batch.Projection, batch.PartitionID); err != nil {
		return err
	}
	if strings.TrimSpace(tenantID) == "" || batch.TenantID != strings.TrimSpace(tenantID) {
		return projection.ErrTenantMismatch
	}
	if batch.LastSequence < 1 {
		return errors.New("projection batch last sequence must be positive")
	}
	if maxPayload <= 0 {
		maxPayload = DefaultMaxPayload
	}
	seen := make(map[string]int64, len(batch.Mutations))
	var previousSequence int64
	for _, mutation := range batch.Mutations {
		if canonical(mutation.Key) == "" || strings.TrimSpace(mutation.Key) != mutation.Key || strings.ContainsAny(mutation.Key, "\x00\r\n") {
			return errors.New("projection mutation key is invalid")
		}
		if mutation.SourceSequence < 1 || mutation.SourceSequence > batch.LastSequence || mutation.SourceSequence < previousSequence {
			return errors.New("projection mutation source sequence is out of order")
		}
		if mutation.Delete && len(mutation.Payload) != 0 {
			return errors.New("projection delete mutation contains a payload")
		}
		if int64(len(mutation.Payload)) > maxPayload {
			return projection.ErrLimit
		}
		if previous, ok := seen[mutation.Key]; ok && previous == mutation.SourceSequence {
			return errors.New("projection batch contains duplicate mutation")
		}
		seen[mutation.Key] = mutation.SourceSequence
		previousSequence = mutation.SourceSequence
	}
	return nil
}

// ProjectionDigest delegates the canonical encoding to the projection port so
// all backends and workers share one offset digest contract.
func ProjectionDigest(tenantID, projectionName, sourceStream string, records []projection.Record) (string, error) {
	return projection.ProjectionDigest(tenantID, projectionName, sourceStream, records)
}
