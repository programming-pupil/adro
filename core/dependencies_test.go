package core

import (
	"context"
	"testing"
	"time"
)

type dependencyStub struct{}

func (dependencyStub) Now() time.Time                             { return time.Unix(1, 0).UTC() }
func (dependencyStub) NewID(string) string                        { return "id-1" }
func (dependencyStub) Uint64() uint64                             { return 1 }
func (dependencyStub) Sleep(context.Context, time.Duration) error { return nil }
func (dependencyStub) Next(int, string, uint64) time.Duration     { return time.Second }

func TestDependenciesValidate(t *testing.T) {
	stub := dependencyStub{}
	dependencies := Dependencies{Clock: stub, IDs: stub, Random: stub, Sleeper: stub, Backoff: stub}
	if err := dependencies.Validate(); err != nil {
		t.Fatal(err)
	}
	dependencies.Clock = nil
	if err := dependencies.Validate(); err == nil {
		t.Fatal("expected missing dependency to fail")
	}
}
