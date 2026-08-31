package plugins

import (
	"crypto/ed25519"
	crand "crypto/rand"
	"encoding/base64"
	"errors"
	"path/filepath"
	"testing"
)

func signedRequest(t *testing.T) InstallRequest {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{ID: "adro.nats", Name: "NATS transport", Version: "1.0.0", ProtocolVersion: "adro.plugin.v1", Capabilities: []string{"events.publish", "events.replay"}, Permissions: []string{"events:write"}}
	digest, err := manifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(privateKey, []byte(digest))
	return InstallRequest{Manifest: manifest, Digest: digest, Signature: base64.StdEncoding.EncodeToString(signature), PublicKey: base64.StdEncoding.EncodeToString(publicKey)}
}

func TestRegistryVerifiesPersistsAndQuarantinesPlugin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.json")
	registry, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := registry.Install(signedRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	id := installed.Manifest.ID + "@" + installed.Manifest.Version
	if _, err := registry.Activate(id); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := registry.RecordHealth(id, false, "transport unavailable"); err != nil {
			t.Fatal(err)
		}
	}
	item, err := registry.Get(id)
	if err != nil || item.State != "quarantined" || item.ConsecutiveErrors != 3 {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	restored, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := restored.Get(id); err != nil || got.State != "quarantined" {
		t.Fatalf("restored=%+v err=%v", got, err)
	}
}

func TestRegistryRejectsUnsignedOrTamperedManifest(t *testing.T) {
	registry, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	request := signedRequest(t)
	request.Manifest.Name = "tampered"
	if _, err := registry.Install(request); !errors.Is(err, ErrUnverified) {
		t.Fatalf("tampered error=%v", err)
	}
	request = signedRequest(t)
	request.Signature = ""
	if _, err := registry.Install(request); !errors.Is(err, ErrUnverified) {
		t.Fatalf("unsigned error=%v", err)
	}
}

func TestRegistryScopesInstallationsByWorkspace(t *testing.T) {
	registry, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	first := signedRequest(t)
	first.TenantID, first.WorkspaceID = "tenant-a", "workspace-a"
	if _, err := registry.Install(first); err != nil {
		t.Fatal(err)
	}
	second := signedRequest(t)
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
