package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adro-project/adro/internal/domain"
)

const (
	workspaceUUID = "00000000-0000-0000-0000-000000000001"
	agentA        = "00000000-0000-0000-0000-00000000000a"
	agentB        = "00000000-0000-0000-0000-00000000000b"
	agentC        = "00000000-0000-0000-0000-00000000000c"
	agentD        = "00000000-0000-0000-0000-00000000000d"
)

func TestAgentRouteConfigPrecedenceAndStableBinding(t *testing.T) {
	config, err := ParseAgentRouteConfig(`{"workspaces":{"` + workspaceUUID + `":{"default_agent_id":"` + agentC + `","members":{" alice ":"` + agentA + `"},"roles":{" Developer ":"` + agentB + `"}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	resolver := NewAgentRouteResolver(config, agentD)
	member := resolver.Resolve(workspaceUUID, "alice", "developer", nil)
	if member.ProviderAssigneeID != agentA || member.Source != "member" || member.Binding.ID == "" {
		t.Fatalf("member route=%+v", member)
	}
	role := resolver.Resolve(workspaceUUID, "nobody", "DEVELOPER", nil)
	if role.ProviderAssigneeID != agentB || role.Source != "role" {
		t.Fatalf("role route=%+v", role)
	}
	workspace := resolver.Resolve(workspaceUUID, "nobody", "qa", nil)
	if workspace.ProviderAssigneeID != agentC || workspace.Source != "workspace_default" {
		t.Fatalf("workspace route=%+v", workspace)
	}
	legacy := resolver.Resolve("another-workspace", "nobody", "qa", nil)
	if legacy.ProviderAssigneeID != agentD || legacy.Source != "legacy_default" {
		t.Fatalf("legacy route=%+v", legacy)
	}
	profile := domain.ProviderBinding{ID: "pb-existing", ProviderObjectID: agentD, ConfigRevision: "old", Source: "profile", Provider: "multica", WorkspaceID: workspaceUUID, Kind: "agent"}
	profileDecision := resolver.Resolve(workspaceUUID, "alice", "developer", &profile)
	if profileDecision.ProviderAssigneeID != agentD || profileDecision.Source != "profile" || profileDecision.ConfigRevision != "old" {
		t.Fatalf("profile route=%+v", profileDecision)
	}
	first := resolver.Resolve(workspaceUUID, "alice", "developer", nil).Binding.ID
	reordered, err := ParseAgentRouteConfig(`{"workspaces":{"` + workspaceUUID + `":{"roles":{" Developer ":"` + agentB + `"},"members":{" alice ":"` + agentA + `"},"default_agent_id":"` + agentC + `"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := NewAgentRouteResolver(reordered, agentD).Resolve(workspaceUUID, "alice", "developer", nil).Binding.ID; got != first {
		t.Fatalf("binding changed after JSON key reorder: %s != %s", got, first)
	}
	upperConfig, err := ParseAgentRouteConfig(`{"workspaces":{"` + strings.ToUpper(workspaceUUID) + `":{"members":{"alice":"` + agentA + `"}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := NewAgentRouteResolver(upperConfig, "").Resolve(workspaceUUID, " alice ", "", nil); got.ProviderAssigneeID != agentA {
		t.Fatalf("normalized workspace/member route=%+v", got)
	}
	upperAgentConfig, err := ParseAgentRouteConfig(`{"workspaces":{"` + workspaceUUID + `":{"members":{"alice":"` + strings.ToUpper(agentA) + `"}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	upperDecision := NewAgentRouteResolver(upperAgentConfig, "").Resolve(workspaceUUID, "alice", "", nil)
	if upperDecision.ProviderAssigneeID != agentA || upperDecision.Binding.ID != first {
		t.Fatalf("native UUID casing changed route=%+v first_binding=%s", upperDecision, first)
	}
	if diagnostics := NewAgentRouteResolver(upperConfig, "").Diagnostics(strings.ToUpper(workspaceUUID)); diagnostics.MemberRouteCount != 1 {
		t.Fatalf("normalized workspace diagnostics=%+v", diagnostics)
	}
	crossWorkspace := profile
	crossWorkspace.WorkspaceID = "00000000-0000-0000-0000-000000000002"
	if got := resolver.Resolve(workspaceUUID, "alice", "developer", &crossWorkspace); got.Source == "profile" {
		t.Fatalf("cross-workspace profile binding was accepted: %+v", got)
	}
}

func TestProviderBindingCanonicalizesUUIDCasing(t *testing.T) {
	lower := NewProviderBinding("multica", workspaceUUID, "agent", agentA, "configured", "member", "cfg")
	upper := NewProviderBinding("multica", strings.ToUpper(workspaceUUID), "agent", strings.ToUpper(agentA), "configured", "member", "cfg")
	if upper.ID != lower.ID {
		t.Fatalf("UUID casing changed binding ID: lower=%s upper=%s", lower.ID, upper.ID)
	}
	if upper.WorkspaceID != workspaceUUID || upper.ProviderObjectID != agentA {
		t.Fatalf("binding fields were not canonicalized: %+v", upper)
	}
}

func TestAgentRouteConfigRejectsUnsafeShapes(t *testing.T) {
	cases := []string{
		`{"unknown":{}}`,
		`{"workspaces":{},"workspaces":{}}`,
		`{"workspaces":{"not-a-uuid":{"default_agent_id":"` + agentA + `"}}}`,
		`{"workspaces":{"` + workspaceUUID + `":{"default_agent_id":"not-an-agent"}}}`,
		`{"workspaces":{"` + workspaceUUID + `":{"roles":{" Developer ":"` + agentA + `","developer":"` + agentB + `"}}}}`,
		`{"workspaces":{}} {"trailing":true}`,
	}
	for _, raw := range cases {
		if _, err := ParseAgentRouteConfig(raw); err == nil {
			t.Errorf("expected invalid config: %s", raw)
		}
	}
	if _, err := ParseAgentRouteConfig(strings.Repeat("x", maxAgentRouteConfigBytes+1)); err == nil {
		t.Fatal("oversized config accepted")
	}
}

func TestAgentRouteResolverFromEnvRejectsMalformedLegacyID(t *testing.T) {
	t.Setenv("ADRO_MULTICA_AGENT_MAP", "")
	t.Setenv("ADRO_MULTICA_AGENT_ID", "definitely-not-a-uuid")
	if _, err := NewAgentRouteResolverFromEnv(); err == nil {
		t.Fatal("malformed legacy agent id was accepted")
	}
	t.Setenv("ADRO_MULTICA_AGENT_ID", strings.ToUpper(agentA))
	resolver, err := NewAgentRouteResolverFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if got := resolver.Resolve("workspace", "member", "", nil).ProviderAssigneeID; got != agentA {
		t.Fatalf("legacy agent id was not canonicalized: %q", got)
	}
}

func TestMulticaProviderErrorsAreTypedAndRedacted(t *testing.T) {
	secret := "upstream response must not escape"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(secret))
	}))
	defer server.Close()
	_, err := NewMulticaProvider(server.URL, "token").Capabilities(context.Background())
	if ErrorCodeOf(err) != ErrorUnauthorized || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "token") {
		t.Fatalf("error=%v code=%s", err, ErrorCodeOf(err))
	}
	malformed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not-json")) }))
	defer malformed.Close()
	_, err = NewMulticaProvider(malformed.URL, "").Capabilities(context.Background())
	if ErrorCodeOf(err) != ErrorInvalidResponse {
		t.Fatalf("malformed response code=%s err=%v", ErrorCodeOf(err), err)
	}
}

func TestRouteDiagnosticsNeverContainsNativeIDs(t *testing.T) {
	config, err := ParseAgentRouteConfig(`{"workspaces":{"` + workspaceUUID + `":{"members":{"alice":"` + agentA + `"}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	result := NewAgentRouteResolver(config, "").Diagnostics(workspaceUUID)
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), agentA) {
		t.Fatalf("diagnostics leaked native id: %s", data)
	}
}
