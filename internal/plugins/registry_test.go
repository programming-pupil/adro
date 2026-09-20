package plugins

import (
	"context"
	"crypto/ed25519"
	crand "crypto/rand"
	"encoding/base64"
	"errors"
	"path/filepath"
	"testing"

	"github.com/adro-project/adro/internal/security"
	extensionport "github.com/adro-project/adro/ports/extensions"
)

type testSigner struct {
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
	keyID      string
	publisher  string
}

func newTestSigner(t *testing.T, publisher string) testSigner {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return testSigner{publicKey: publicKey, privateKey: privateKey, keyID: keyID(publicKey), publisher: publisher}
}

func trustTestSigner(t *testing.T, registry *Registry, signer testSigner, allowInProcess bool) TrustedKey {
	t.Helper()
	key, err := registry.TrustKey(TrustKeyRequest{
		Publisher: signer.publisher, PublicKey: base64.StdEncoding.EncodeToString(signer.publicKey), AllowInProcess: allowInProcess,
	})
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func testManifest(version, schemaDigest string) Manifest {
	return Manifest{
		ID: "adro.transport", Name: "Transport adapter", Publisher: "example.publisher",
		Version: version, ProtocolVersion: "adro.extension.v1", AdapterVersion: version,
		SchemaDigest: schemaDigest, ArtifactDigest: digestForTest("artifact-" + version),
		ExecutionMode: ExecutionOutOfProcess, MaxMessageBytes: 1 << 20,
		Capabilities: []string{"events.publish", "events.replay"}, Permissions: []string{"events:write"},
		FilePermissions:       []FilePermission{{Path: "state/cache", Operations: []string{"write", "read"}}},
		NetworkPermissions:    []NetworkPermission{{Domain: "api.example.test", Ports: []int{443}, Protocol: "https", Purpose: "event delivery"}},
		SecretPermissions:     []SecretPermission{{Name: "transport-token", Destination: "extension:transport", Purpose: "authentication"}},
		DataEgressPermissions: []DataEgressPermission{{Destination: "https://api.example.test/events", Purpose: "event delivery", MaxSensitivity: security.SensitivityInternal}},
	}
}

func signedRequest(t *testing.T, signer testSigner, manifest Manifest) InstallRequest {
	t.Helper()
	digest, err := manifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(signer.privateKey, []byte(digest))
	return InstallRequest{
		Manifest: manifest, Digest: digest, Signature: base64.StdEncoding.EncodeToString(signature), KeyID: signer.keyID,
	}
}

func digestForTest(value string) string {
	manifest := Manifest{
		ID: "digest", Name: "digest", Publisher: "digest", Version: value,
		ProtocolVersion: "digest", AdapterVersion: "digest", ExecutionMode: ExecutionOutOfProcess,
		SchemaDigest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		ArtifactDigest:  "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		MaxMessageBytes: 1024, Capabilities: []string{"digest"},
	}
	digest, _ := manifestDigest(manifest)
	return digest
}

func TestRegistryVerifiesPersistsAndQuarantinesPlugin(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "plugins.json")
	registry, err := New(statePath)
	if err != nil {
		t.Fatal(err)
	}
	signer := newTestSigner(t, "example.publisher")
	trustTestSigner(t, registry, signer, false)
	installed, err := registry.Install(signedRequest(t, signer, testManifest("1.0.0", digestForTest("schema-v1"))))
	if err != nil {
		t.Fatal(err)
	}
	id := installed.Manifest.ID + "@" + installed.Manifest.Version
	if _, err := registry.Activate(id); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		if _, err := registry.RecordHealth(id, false, "transport unavailable"); err != nil {
			t.Fatal(err)
		}
	}
	item, err := registry.Get(id)
	if err != nil || item.State != "quarantined" || item.ConsecutiveErrors != 3 {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	restored, err := New(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := restored.Get(id); err != nil || got.State != "quarantined" || len(restored.ListKeys()) != 1 {
		t.Fatalf("restored=%+v keys=%+v err=%v", got, restored.ListKeys(), err)
	}
}

func TestRegistryReloadsDegradedActiveInstallation(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "plugins.json")
	registry, err := New(statePath)
	if err != nil {
		t.Fatal(err)
	}
	signer := newTestSigner(t, "example.publisher")
	trustTestSigner(t, registry, signer, false)
	request := signedRequest(t, signer, testManifest("1.0.0", digestForTest("schema-v1")))
	request.WorkspaceID, request.TenantID = "workspace", "tenant"
	installed, err := registry.Install(request)
	if err != nil {
		t.Fatal(err)
	}
	id := installed.Manifest.ID + "@" + installed.Manifest.Version
	if _, err := registry.ActivateForWorkspace("workspace", id); err != nil {
		t.Fatal(err)
	}
	degraded, err := registry.RecordHealthForWorkspace("workspace", id, false, "probe failed")
	if err != nil || degraded.State != "degraded" {
		t.Fatalf("degraded=%+v err=%v", degraded, err)
	}
	reloaded, err := New(statePath)
	if err != nil {
		t.Fatalf("reload degraded registry: %v", err)
	}
	got, err := reloaded.GetForWorkspace("workspace", id)
	if err != nil || got.State != "degraded" {
		t.Fatalf("reloaded=%+v err=%v", got, err)
	}
}

func TestActivationPersistenceFailureRollsBackSupersededInstallation(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "plugins.json")
	registry, err := New(statePath)
	if err != nil {
		t.Fatal(err)
	}
	signer := newTestSigner(t, "example.publisher")
	trustTestSigner(t, registry, signer, false)
	var installed []Installation
	for _, version := range []string{"1.0.0", "1.1.0"} {
		request := signedRequest(t, signer, testManifest(version, digestForTest("schema-v1")))
		request.WorkspaceID, request.TenantID = "workspace", "tenant"
		item, installErr := registry.Install(request)
		if installErr != nil {
			t.Fatal(installErr)
		}
		installed = append(installed, item)
	}
	firstID := installed[0].Manifest.ID + "@" + installed[0].Manifest.Version
	if _, err := registry.ActivateForWorkspace("workspace", firstID); err != nil {
		t.Fatal(err)
	}
	registry.path = t.TempDir() // rename over a directory fails after in-memory mutation.
	secondID := installed[1].Manifest.ID + "@" + installed[1].Manifest.Version
	if _, err := registry.ActivateForWorkspace("workspace", secondID); err == nil {
		t.Fatal("activation unexpectedly persisted")
	}
	first, err := registry.GetForWorkspace("workspace", firstID)
	if err != nil || first.State != "active" {
		t.Fatalf("first installation was not restored: %+v err=%v", first, err)
	}
	second, err := registry.GetForWorkspace("workspace", secondID)
	if err != nil || second.State != "verified" {
		t.Fatalf("second installation was not restored: %+v err=%v", second, err)
	}
	active, err := registry.ActiveForWorkspace("workspace", installed[0].Manifest.ID)
	if err != nil || active.Manifest.Version != "1.0.0" {
		t.Fatalf("active=%+v err=%v", active, err)
	}
}

func TestRegistryRejectsUntrustedTamperedAndOverprivilegedInstall(t *testing.T) {
	registry, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	signer := newTestSigner(t, "example.publisher")
	request := signedRequest(t, signer, testManifest("1.0.0", digestForTest("schema-v1")))
	if _, err := registry.Install(request); !errors.Is(err, ErrKeyNotTrusted) {
		t.Fatalf("untrusted key error=%v", err)
	}
	trustTestSigner(t, registry, signer, false)

	tampered := request
	tampered.Manifest.Name = "tampered"
	if _, err := registry.Install(tampered); !errors.Is(err, ErrUnverified) {
		t.Fatalf("tampered error=%v", err)
	}
	unsigned := request
	unsigned.Signature = ""
	if _, err := registry.Install(unsigned); !errors.Is(err, ErrUnverified) {
		t.Fatalf("unsigned error=%v", err)
	}
	inProcess := testManifest("1.1.0", digestForTest("schema-v1"))
	inProcess.ExecutionMode = ExecutionInProcess
	if _, err := registry.Install(signedRequest(t, signer, inProcess)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unapproved in-process install error=%v", err)
	}
}

func TestManifestDeclaresAndValidatesEverySensitiveCapabilityClass(t *testing.T) {
	manifest := testManifest("1.0.0", digestForTest("schema-v1"))
	first, err := manifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	reordered := cloneManifest(manifest)
	reordered.Capabilities[0], reordered.Capabilities[1] = reordered.Capabilities[1], reordered.Capabilities[0]
	reordered.FilePermissions[0].Operations[0], reordered.FilePermissions[0].Operations[1] = reordered.FilePermissions[0].Operations[1], reordered.FilePermissions[0].Operations[0]
	second, err := manifestDigest(reordered)
	if err != nil || first != second {
		t.Fatalf("canonical digest drifted: first=%s second=%s err=%v", first, second, err)
	}

	for name, mutate := range map[string]func(*Manifest){
		"file escape":            func(m *Manifest) { m.FilePermissions[0].Path = "../host" },
		"network":                func(m *Manifest) { m.NetworkPermissions[0].Ports = []int{0} },
		"secret":                 func(m *Manifest) { m.SecretPermissions[0].Purpose = "" },
		"egress":                 func(m *Manifest) { m.DataEgressPermissions[0].Destination = "http://api.example.test" },
		"network without egress": func(m *Manifest) { m.DataEgressPermissions = nil },
		"egress mismatch":        func(m *Manifest) { m.DataEgressPermissions[0].Destination = "https://other.example.test/events" },
		"message":                func(m *Manifest) { m.MaxMessageBytes = maxManifestMessageSize + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneManifest(manifest)
			mutate(&candidate)
			if _, err := manifestDigest(candidate); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid manifest returned %v", err)
			}
		})
	}
}

func TestRegistryScopesInstallationsByWorkspace(t *testing.T) {
	registry, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	signer := newTestSigner(t, "example.publisher")
	trustTestSigner(t, registry, signer, false)
	first := signedRequest(t, signer, testManifest("1.0.0", digestForTest("schema-v1")))
	first.TenantID, first.WorkspaceID = "tenant-a", "workspace-a"
	if _, err := registry.Install(first); err != nil {
		t.Fatal(err)
	}
	second := signedRequest(t, signer, testManifest("1.0.0", digestForTest("schema-v1")))
	second.TenantID, second.WorkspaceID = "tenant-b", "workspace-b"
	if _, err := registry.Install(second); err != nil {
		t.Fatal(err)
	}
	id := first.Manifest.ID + "@" + first.Manifest.Version
	if got := len(registry.ListWorkspace("workspace-a")); got != 1 {
		t.Fatalf("workspace-a list=%d", got)
	}
	if got := len(registry.ListWorkspace("workspace-b")); got != 1 {
		t.Fatalf("workspace-b list=%d", got)
	}
	if _, err := registry.GetForWorkspace("workspace-a", id); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.GetForWorkspace("workspace-c", id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign workspace lookup=%v", err)
	}
}

// Threat IDs: TM-SUPPLY-002, TM-EXT-001
func TestAuthorizeExecutionReturnsStoredPolicyAndRejectsForgedIdentity(t *testing.T) {
	registry, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	signer := newTestSigner(t, "example.publisher")
	trustTestSigner(t, registry, signer, false)
	request := signedRequest(t, signer, testManifest("1.0.0", digestForTest("schema-v1")))
	request.TenantID, request.WorkspaceID = "tenant", "workspace"
	installed, err := registry.Install(request)
	if err != nil {
		t.Fatal(err)
	}
	active, err := registry.ActivateForWorkspace("workspace", installed.Manifest.ID+"@"+installed.Manifest.Version)
	if err != nil {
		t.Fatal(err)
	}

	identity := toExecutionInstallation(active)
	forged := identity
	forged.Manifest.Capabilities = []string{"host.admin"}
	forged.Manifest.FilePermissions = []extensionport.FilePermission{{Path: "/", Operations: []string{"read", "write"}}}
	forged.Manifest.DataEgressPermissions = []extensionport.DataEgressPermission{{
		Destination: "https://attacker.invalid", Purpose: "exfiltration", MaxSensitivity: string(security.SensitivitySecret),
	}}
	grant, err := registry.AuthorizeExecution(context.Background(), forged)
	if err != nil {
		t.Fatal(err)
	}
	if len(grant.Manifest.Capabilities) != len(active.Manifest.Capabilities) || grant.Manifest.Capabilities[0] != active.Manifest.Capabilities[0] {
		t.Fatalf("authorization returned caller policy: %+v", grant.Manifest)
	}
	if len(grant.Manifest.FilePermissions) != 1 || grant.Manifest.FilePermissions[0].Path != "state/cache" {
		t.Fatalf("authorization returned forged file permission: %+v", grant.Manifest.FilePermissions)
	}
	if len(grant.Manifest.DataEgressPermissions) != 1 || grant.Manifest.DataEgressPermissions[0].Destination != "https://api.example.test/events" || grant.Manifest.DataEgressPermissions[0].MaxSensitivity != string(security.SensitivityInternal) {
		t.Fatalf("authorization returned forged data-egress permission: %+v", grant.Manifest.DataEgressPermissions)
	}

	for name, mutate := range map[string]func(*extensionport.Installation){
		"digest":    func(item *extensionport.Installation) { item.Digest = digestForTest("forged") },
		"signature": func(item *extensionport.Installation) { item.Signature = "forged" },
		"key":       func(item *extensionport.Installation) { item.KeyID = "forged" },
		"artifact":  func(item *extensionport.Installation) { item.Manifest.ArtifactDigest = digestForTest("other-artifact") },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := identity
			mutate(&candidate)
			if _, err := registry.AuthorizeExecution(context.Background(), candidate); !errors.Is(err, ErrUnverified) {
				t.Fatalf("forged identity returned %v", err)
			}
		})
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.AuthorizeExecution(cancelled, identity); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled authorization returned %v", err)
	}
}

// Threat IDs: TM-SUPPLY-001, TM-EXT-002
func TestRegistryKeyRotationRevocationAndCompatibleRollback(t *testing.T) {
	registry, err := New(filepath.Join(t.TempDir(), "plugins.json"))
	if err != nil {
		t.Fatal(err)
	}
	oldSigner := newTestSigner(t, "example.publisher")
	oldKey := trustTestSigner(t, registry, oldSigner, false)
	schemaV1 := digestForTest("schema-v1")
	v1 := signedRequest(t, oldSigner, testManifest("1.0.0", schemaV1))
	v1.WorkspaceID, v1.TenantID = "workspace", "tenant"
	first, err := registry.Install(v1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ActivateForWorkspace("workspace", first.Manifest.ID+"@"+first.Manifest.Version); err != nil {
		t.Fatal(err)
	}

	newSigner := newTestSigner(t, "example.publisher")
	newPublicKey := base64.StdEncoding.EncodeToString(newSigner.publicKey)
	rotation := KeyRotationRequest{CurrentKeyID: oldKey.ID, NewPublicKey: newPublicKey, NewKeyID: newSigner.keyID}
	rotation.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(oldSigner.privateKey, []byte(rotationDigest(oldKey, newSigner.keyID, newPublicKey, false))))
	if _, err := registry.RotateKey(rotation); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Install(signedRequest(t, oldSigner, testManifest("1.0.1", schemaV1))); !errors.Is(err, ErrKeyRevoked) {
		t.Fatalf("retired key installed a new version: %v", err)
	}

	v2Manifest := testManifest("2.0.0", digestForTest("schema-v2"))
	v2Manifest.CompatibleSchemaDigests = []string{schemaV1}
	v2 := signedRequest(t, newSigner, v2Manifest)
	v2.WorkspaceID, v2.TenantID = "workspace", "tenant"
	second, err := registry.Install(v2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ActivateForWorkspace("workspace", second.Manifest.ID+"@"+second.Manifest.Version); err != nil {
		t.Fatal(err)
	}
	rolledBack, err := registry.RollbackForWorkspace("workspace", first.Manifest.ID)
	if err != nil || rolledBack.Manifest.Version != "1.0.0" || rolledBack.State != "active" {
		t.Fatalf("rollback=%+v err=%v", rolledBack, err)
	}
	if _, err := registry.RevokeKey(oldKey.ID, "publisher compromise"); err != nil {
		t.Fatal(err)
	}
	quarantined, err := registry.GetForWorkspace("workspace", first.Manifest.ID+"@"+first.Manifest.Version)
	if err != nil || quarantined.State != "quarantined" {
		t.Fatalf("revoked installation=%+v err=%v", quarantined, err)
	}
}

func TestRollbackRejectsIncompatibleSchema(t *testing.T) {
	registry, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	signer := newTestSigner(t, "example.publisher")
	trustTestSigner(t, registry, signer, false)
	for _, manifest := range []Manifest{
		testManifest("1.0.0", digestForTest("schema-v1")),
		testManifest("2.0.0", digestForTest("schema-v2")),
	} {
		request := signedRequest(t, signer, manifest)
		request.WorkspaceID, request.TenantID = "workspace", "tenant"
		installed, installErr := registry.Install(request)
		if installErr != nil {
			t.Fatal(installErr)
		}
		if _, activateErr := registry.ActivateForWorkspace("workspace", installed.Manifest.ID+"@"+installed.Manifest.Version); activateErr != nil {
			t.Fatal(activateErr)
		}
	}
	if _, err := registry.RollbackForWorkspace("workspace", "adro.transport"); !errors.Is(err, ErrNoRollback) {
		t.Fatalf("incompatible rollback returned %v", err)
	}
}
