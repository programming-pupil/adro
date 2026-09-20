package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	projectionconformance "github.com/adro-project/adro/conformance/projection"
	"github.com/adro-project/adro/core/testkit"
	"github.com/adro-project/adro/ports/projection"
	"github.com/adro-project/adro/ports/scope"
)

func TestPostgresProjectionConformance(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("ADRO_POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("set ADRO_POSTGRES_TEST_DSN to run PostgreSQL projection conformance")
	}
	projectionconformance.Run(t, func(t *testing.T) projectionconformance.Backend {
		store, err := Open(dsn, Options{Clock: testkit.NewManualClock(time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC))})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`TRUNCATE runtime_projections`); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	})
}

func TestPostgresProjectionRequiresTenantScope(t *testing.T) {
	if strings.TrimSpace(os.Getenv("ADRO_POSTGRES_TEST_DSN")) == "" {
		t.Skip("set ADRO_POSTGRES_TEST_DSN to run PostgreSQL projection tests")
	}
	store, err := Open(os.Getenv("ADRO_POSTGRES_TEST_DSN"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Get(context.Background(), "tenant-a", "sessions", "key"); !errors.Is(err, scope.ErrMissingTenant) {
		t.Fatalf("unscoped get error=%v", err)
	}
	if err := store.Put(context.Background(), projection.Record{TenantID: "tenant-a", Projection: "sessions", Key: "key", Version: 1, SourceStream: "stream", SourceSequence: 1, Payload: []byte("payload")}, 0); !errors.Is(err, scope.ErrMissingTenant) {
		t.Fatalf("unscoped put error=%v", err)
	}
}
