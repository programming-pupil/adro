package contract

import (
	"errors"
	"testing"
	"time"

	"github.com/adro-project/adro/ports/projection"
)

func validRecord() projection.Record {
	payload := []byte(`{"state":"running"}`)
	return projection.Record{
		TenantID:       "tenant-a",
		Projection:     "sessions",
		Key:            "session-1",
		Version:        1,
		SourceStream:   "stream-a",
		SourceSequence: 7,
		Digest:         Digest(payload),
		Payload:        payload,
		UpdatedAt:      time.Date(2026, 9, 20, 1, 2, 3, 0, time.FixedZone("CST", 8*60*60)),
	}
}

func TestValidateForWriteRejectsDigestMismatchAndPayloadLimit(t *testing.T) {
	item := validRecord()
	item.Digest = Digest([]byte("different"))
	if err := ValidateForWrite(item, "tenant-a", DefaultMaxPayload); !errors.Is(err, projection.ErrCorrupt) {
		t.Fatalf("digest mismatch error=%v", err)
	}

	item = validRecord()
	if err := ValidateForWrite(item, "tenant-a", int64(len(item.Payload)-1)); !errors.Is(err, projection.ErrLimit) {
		t.Fatalf("payload limit error=%v", err)
	}
}

func TestValidateForWriteRejectsCrossTenantRecord(t *testing.T) {
	if err := ValidateForWrite(validRecord(), "tenant-b", DefaultMaxPayload); !errors.Is(err, projection.ErrTenantMismatch) {
		t.Fatalf("cross-tenant error=%v", err)
	}
}

func TestEquivalentDefinesIdempotentReplayWithoutTimestamp(t *testing.T) {
	first := validRecord()
	replay := first
	replay.UpdatedAt = first.UpdatedAt.Add(10 * time.Minute)
	replay.Payload = append([]byte(nil), first.Payload...)
	if !Equivalent(first, replay) {
		t.Fatal("equivalent replay was not accepted")
	}

	replay.Payload[0] = 'X'
	if Equivalent(first, replay) {
		t.Fatal("payload mutation was accepted as equivalent")
	}
}

func TestNormalizeCanonicalizesAndCopies(t *testing.T) {
	payload := []byte("payload")
	item := projection.Record{
		TenantID:       " tenant-a ",
		Projection:     " sessions ",
		Key:            " key ",
		SourceStream:   " stream-a ",
		Digest:         " " + Digest(payload) + " ",
		Payload:        payload,
		Version:        1,
		SourceSequence: 1,
	}
	now := time.Date(2026, 9, 20, 1, 2, 3, 0, time.FixedZone("CST", 8*60*60))
	normalized := Normalize(item, now)
	if normalized.TenantID != "tenant-a" || normalized.Projection != "sessions" || normalized.Key != "key" || normalized.SourceStream != "stream-a" {
		t.Fatalf("normalized identity=%+v", normalized)
	}
	if normalized.Digest != Digest(payload) || !normalized.UpdatedAt.Equal(now.UTC()) {
		t.Fatalf("normalized metadata=%+v", normalized)
	}
	normalized.Payload[0] = 'X'
	if string(payload) != "payload" {
		t.Fatal("normalize did not copy payload")
	}
}

func TestValidateStoredRequiresTimestamp(t *testing.T) {
	item := validRecord()
	item.UpdatedAt = time.Time{}
	if err := ValidateStored(item, "tenant-a", DefaultMaxPayload); !errors.Is(err, projection.ErrCorrupt) {
		t.Fatalf("missing timestamp error=%v", err)
	}
}
