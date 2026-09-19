package security

import (
	"errors"
	"reflect"
	"testing"
)

// Threat ID: TM-PI-001
func TestUntrustedTrustZonesCannotElevate(t *testing.T) {
	zones := map[TrustZone]TaintLabel{
		ZoneUserContent: TaintUserContent, ZoneRetrievedContent: TaintRetrievedContent,
		ZoneToolOutput: TaintToolOutput, ZoneRemoteContent: TaintRemoteContent,
		ZoneModelOutput: TaintModelOutput, ZoneUnknown: TaintUnknownSource,
	}
	for zone, taint := range zones {
		t.Run(string(zone), func(t *testing.T) {
			provenance, err := NewProvenance(zone, "source", "tenant:one", "model_context", SensitivityInternal)
			if err != nil {
				t.Fatal(err)
			}
			if provenance.TrustLevel != TrustUntrusted || !reflect.DeepEqual(provenance.TaintLabels, []TaintLabel{taint}) {
				t.Fatalf("zone %s provenance=%+v", zone, provenance)
			}
			elevated := provenance
			elevated.TrustLevel = TrustTrusted
			if err := EnforceTrustZone(zone, elevated); err == nil {
				t.Fatal("untrusted zone accepted elevated trust")
			}
		})
	}
}

// Threat IDs: TM-PI-001, TM-TENANT-001
func TestDeriveProvenancePreservesTaintSensitivityAndTenant(t *testing.T) {
	user, err := NewProvenance(ZoneUserContent, "user:message", "tenant:one", "model_context", SensitivityConfidential)
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewProvenance(ZoneToolOutput, "tool:result", "tenant:one", "model_context", SensitivityInternal)
	if err != nil {
		t.Fatal(err)
	}
	derived, err := DeriveProvenance(ZoneModelOutput, "model:summary", "tenant:one", "model_context", SensitivityPublic, tool, user)
	if err != nil {
		t.Fatal(err)
	}
	wantTaints := []TaintLabel{TaintModelOutput, TaintToolOutput, TaintUserContent}
	if derived.TrustLevel != TrustUntrusted || derived.Sensitivity != SensitivityConfidential || !reflect.DeepEqual(derived.TaintLabels, wantTaints) {
		t.Fatalf("derived provenance=%+v want taints=%v", derived, wantTaints)
	}
	foreign := tool
	foreign.TenantScope = "tenant:two"
	if _, err := DeriveProvenance(ZoneModelOutput, "model:summary", "tenant:one", "model_context", SensitivityInternal, user, foreign); err == nil {
		t.Fatal("cross-tenant derivation was accepted")
	}
}

func TestCanonicalizeProvenanceSortsDeduplicatesAndFailsClosed(t *testing.T) {
	provenance, err := CanonicalizeProvenance(Provenance{
		Source: "tool:result", TrustLevel: TrustReviewed, Sensitivity: SensitivityRestricted,
		TenantScope: "tenant:one", Purpose: "model_context",
		TaintLabels: []TaintLabel{TaintUserContent, TaintToolOutput, TaintUserContent},
	})
	if err != nil {
		t.Fatal(err)
	}
	if provenance.TrustLevel != TrustUntrusted || !reflect.DeepEqual(provenance.TaintLabels, []TaintLabel{TaintToolOutput, TaintUserContent}) {
		t.Fatalf("canonical provenance=%+v", provenance)
	}
	invalid := provenance
	invalid.Source = " source"
	if err := invalid.Validate(); err == nil {
		t.Fatal("noncanonical source was accepted")
	}
	invalid = provenance
	invalid.TaintLabels = []TaintLabel{"future"}
	if _, err := CanonicalizeProvenance(invalid); err == nil || errors.Is(err, nil) {
		t.Fatalf("invalid taint returned %v", err)
	}
}
