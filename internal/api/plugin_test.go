package api

import (
	"crypto/ed25519"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/adro-project/adro/internal/plugins"
)

func TestPluginTrustInstallRollbackRotationAndRevocationAPI(t *testing.T) {
	server := testServer(t)
	headers := map[string]string{"X-Tenant-ID": "tenant", "X-Workspace-ID": "workspace"}
	oldPublic, oldPrivate, err := ed25519.GenerateKey(crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	oldPublicText := base64.StdEncoding.EncodeToString(oldPublic)
	oldKeyID, err := plugins.SigningKeyID(oldPublicText)
	if err != nil {
		t.Fatal(err)
	}
	trustBody := mustMarshalPluginAPI(t, plugins.TrustKeyRequest{Publisher: "example.publisher", PublicKey: oldPublicText})
	trusted := request(t, server.Routes(), http.MethodPost, "/api/v1/plugins/keys", trustBody, headers)
	if trusted.Code != http.StatusCreated {
		t.Fatalf("trust status=%d body=%s", trusted.Code, trusted.Body.String())
	}

	v1 := pluginAPITestManifest("1.0.0", pluginAPITestDigest("schema-v1"))
	v1Install := request(t, server.Routes(), http.MethodPost, "/api/v1/plugins", signedPluginAPIRequest(t, oldPrivate, oldKeyID, v1), headers)
	if v1Install.Code != http.StatusCreated {
		t.Fatalf("v1 install status=%d body=%s", v1Install.Code, v1Install.Body.String())
	}
	if activated := request(t, server.Routes(), http.MethodPost, "/api/v1/plugins/adro.transport@1.0.0/activate", "", headers); activated.Code != http.StatusOK {
		t.Fatalf("v1 activate status=%d body=%s", activated.Code, activated.Body.String())
	}

	v2 := pluginAPITestManifest("2.0.0", pluginAPITestDigest("schema-v2"))
	v2.CompatibleSchemaDigests = []string{v1.SchemaDigest}
	v2Install := request(t, server.Routes(), http.MethodPost, "/api/v1/plugins", signedPluginAPIRequest(t, oldPrivate, oldKeyID, v2), headers)
	if v2Install.Code != http.StatusCreated {
		t.Fatalf("v2 install status=%d body=%s", v2Install.Code, v2Install.Body.String())
	}
	if activated := request(t, server.Routes(), http.MethodPost, "/api/v1/plugins/adro.transport@2.0.0/activate", "", headers); activated.Code != http.StatusOK {
		t.Fatalf("v2 activate status=%d body=%s", activated.Code, activated.Body.String())
	}
	rolledBack := request(t, server.Routes(), http.MethodPost, "/api/v1/plugins/adro.transport@2.0.0/rollback", "", headers)
	if rolledBack.Code != http.StatusOK || !containsJSONField(rolledBack.Body.Bytes(), "version", "1.0.0") {
		t.Fatalf("rollback status=%d body=%s", rolledBack.Code, rolledBack.Body.String())
	}

	newPublic, _, err := ed25519.GenerateKey(crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	newPublicText := base64.StdEncoding.EncodeToString(newPublic)
	newKeyID, err := plugins.SigningKeyID(newPublicText)
	if err != nil {
		t.Fatal(err)
	}
	rotationDigest, err := plugins.KeyRotationDigest("example.publisher", oldKeyID, newKeyID, newPublicText, false)
	if err != nil {
		t.Fatal(err)
	}
	rotation := plugins.KeyRotationRequest{
		NewKeyID: newKeyID, NewPublicKey: newPublicText,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(oldPrivate, []byte(rotationDigest))),
	}
	rotated := request(t, server.Routes(), http.MethodPost, "/api/v1/plugins/keys/"+oldKeyID+"/rotate", mustMarshalPluginAPI(t, rotation), headers)
	if rotated.Code != http.StatusOK || !containsJSONField(rotated.Body.Bytes(), "id", newKeyID) {
		t.Fatalf("rotate status=%d body=%s", rotated.Code, rotated.Body.String())
	}
	revoked := request(t, server.Routes(), http.MethodPost, "/api/v1/plugins/keys/"+oldKeyID+"/revoke", `{"reason":"publisher compromise"}`, headers)
	if revoked.Code != http.StatusOK || !containsJSONField(revoked.Body.Bytes(), "state", "revoked") {
		t.Fatalf("revoke status=%d body=%s", revoked.Code, revoked.Body.String())
	}
	installation := request(t, server.Routes(), http.MethodGet, "/api/v1/plugins/adro.transport@1.0.0", "", headers)
	if installation.Code != http.StatusOK || !containsJSONField(installation.Body.Bytes(), "state", "quarantined") {
		t.Fatalf("installation status=%d body=%s", installation.Code, installation.Body.String())
	}
}

func pluginAPITestManifest(version, schemaDigest string) plugins.Manifest {
	return plugins.Manifest{
		ID: "adro.transport", Name: "Transport adapter", Publisher: "example.publisher", Version: version,
		ProtocolVersion: "adro.extension.v1", AdapterVersion: version,
		SchemaDigest: schemaDigest, ArtifactDigest: pluginAPITestDigest("artifact-" + version),
		ExecutionMode: plugins.ExecutionOutOfProcess, MaxMessageBytes: 1 << 20,
		Capabilities: []string{"echo"}, Permissions: []string{"events:read"},
	}
}

func pluginAPITestDigest(value string) string {
	manifest := plugins.Manifest{
		ID: "digest", Name: "digest", Publisher: "digest", Version: value,
		ProtocolVersion: "digest", AdapterVersion: "digest", ExecutionMode: plugins.ExecutionOutOfProcess,
		SchemaDigest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		ArtifactDigest:  "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		MaxMessageBytes: 1024, Capabilities: []string{"digest"},
	}
	digest, _ := plugins.ManifestDigest(manifest)
	return digest
}

func signedPluginAPIRequest(t *testing.T, privateKey ed25519.PrivateKey, keyID string, manifest plugins.Manifest) string {
	t.Helper()
	digest, err := plugins.ManifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return mustMarshalPluginAPI(t, plugins.InstallRequest{
		Manifest: manifest, Digest: digest, KeyID: keyID,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(digest))),
	})
}

func mustMarshalPluginAPI(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func containsJSONField(data []byte, key, value string) bool {
	var decoded any
	if json.Unmarshal(data, &decoded) != nil {
		return false
	}
	return findJSONField(decoded, key, value)
}

func findJSONField(value any, key, expected string) bool {
	switch typed := value.(type) {
	case map[string]any:
		if actual, ok := typed[key].(string); ok && actual == expected {
			return true
		}
		for _, nested := range typed {
			if findJSONField(nested, key, expected) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if findJSONField(nested, key, expected) {
				return true
			}
		}
	}
	return false
}
