package core

import (
	"strings"
	"testing"
	"time"

	"github.com/adro-project/adro/core/ids"
)

func TestSystemDependenciesSatisfyRuntimeContracts(t *testing.T) {
	now := (SystemClock{}).Now()
	if now.Location() != time.UTC || time.Since(now) > time.Second {
		t.Fatalf("system clock returned %v", now)
	}
	generator := &CryptoIDs{}
	first, second := generator.NewID("event"), generator.NewID("event")
	if first == second || !strings.HasPrefix(first, "event-") {
		t.Fatalf("production IDs first=%q second=%q", first, second)
	}
	if err := ids.Validate("event", first); err != nil {
		t.Fatal(err)
	}
}
