package plugins

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	corepolicy "github.com/adro-project/adro/core/policy"
	extensionport "github.com/adro-project/adro/ports/extensions"
	"net"
	"net/url"
	"path"
	"sort"
	"strings"
)

func validateManifest(manifest Manifest) error {
	for name, value := range map[string]string{
		"id": manifest.ID, "name": manifest.Name, "publisher": manifest.Publisher,
		"version": manifest.Version, "protocol_version": manifest.ProtocolVersion,
		"adapter_version": manifest.AdapterVersion,
	} {
		if !canonicalIdentifier(value, 256) {
			return fmt.Errorf("%w: canonical %s is required", ErrInvalid, name)
		}
	}
	if !manifest.ExecutionMode.Valid() {
		return fmt.Errorf("%w: invalid execution mode", ErrInvalid)
	}
	if !validDigest(manifest.SchemaDigest) || !validDigest(manifest.ArtifactDigest) {
		return fmt.Errorf("%w: schema and artifact digests must be sha256 digests", ErrInvalid)
	}
	if manifest.MaxMessageBytes < 1024 || manifest.MaxMessageBytes > maxManifestMessageSize {
		return fmt.Errorf("%w: max_message_bytes is outside 1024..%d", ErrInvalid, maxManifestMessageSize)
	}
	if err := validateStringSet("capabilities", manifest.Capabilities, true); err != nil {
		return err
	}
	if err := validateStringSet("permissions", manifest.Permissions, false); err != nil {
		return err
	}
	for index, permission := range manifest.FilePermissions {
		if err := validateFilePermission(permission); err != nil {
			return fmt.Errorf("%w: file permission %d: %v", ErrInvalid, index, err)
		}
	}
	for index, permission := range manifest.NetworkPermissions {
		if err := validateNetworkPermission(permission); err != nil {
			return fmt.Errorf("%w: network permission %d: %v", ErrInvalid, index, err)
		}
	}
	for index, permission := range manifest.SecretPermissions {
		if !canonicalIdentifier(permission.Name, 256) || !canonicalIdentifier(permission.Destination, 256) || !canonicalIdentifier(permission.Purpose, 256) {
			return fmt.Errorf("%w: secret permission %d is incomplete", ErrInvalid, index)
		}
	}
	for index, permission := range manifest.DataEgressPermissions {
		if err := validateEgressPermission(permission); err != nil {
			return fmt.Errorf("%w: data egress permission %d: %v", ErrInvalid, index, err)
		}
	}
	if err := validateNetworkEgressBindings(manifest); err != nil {
		return err
	}
	if err := validateStringSet("compatible_schema_digests", manifest.CompatibleSchemaDigests, false); err != nil {
		return err
	}
	for _, digest := range manifest.CompatibleSchemaDigests {
		if !validDigest(digest) {
			return fmt.Errorf("%w: invalid compatible schema digest", ErrInvalid)
		}
	}
	return nil
}

func manifestDigest(manifest Manifest) (string, error) {
	if err := validateManifest(manifest); err != nil {
		return "", err
	}
	canonical := cloneManifest(manifest)
	sort.Strings(canonical.Capabilities)
	sort.Strings(canonical.Permissions)
	sort.Strings(canonical.CompatibleSchemaDigests)
	for index := range canonical.FilePermissions {
		sort.Strings(canonical.FilePermissions[index].Operations)
	}
	sort.Slice(canonical.FilePermissions, func(i, j int) bool { return canonical.FilePermissions[i].Path < canonical.FilePermissions[j].Path })
	sort.Slice(canonical.NetworkPermissions, func(i, j int) bool {
		return permissionKey(canonical.NetworkPermissions[i]) < permissionKey(canonical.NetworkPermissions[j])
	})
	sort.Slice(canonical.SecretPermissions, func(i, j int) bool {
		left, right := canonical.SecretPermissions[i], canonical.SecretPermissions[j]
		return left.Name+"\x00"+left.Destination+"\x00"+left.Purpose < right.Name+"\x00"+right.Destination+"\x00"+right.Purpose
	})
	sort.Slice(canonical.DataEgressPermissions, func(i, j int) bool {
		left, right := canonical.DataEgressPermissions[i], canonical.DataEgressPermissions[j]
		return left.Destination+"\x00"+left.Purpose < right.Destination+"\x00"+right.Purpose
	})
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ManifestDigest returns the canonical digest that publishers sign. Sorting
// and normalization are owned by the registry so clients do not need to
// reproduce implementation-specific JSON ordering.
func ManifestDigest(manifest Manifest) (string, error) {
	return manifestDigest(manifest)
}

func validateFilePermission(permission FilePermission) error {
	value := strings.TrimSpace(permission.Path)
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return errors.New("path must be a portable workspace-relative path")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != value {
		return errors.New("path must be canonical and cannot escape the workspace")
	}
	if len(permission.Operations) == 0 {
		return errors.New("at least one file operation is required")
	}
	seen := map[string]struct{}{}
	for _, operation := range permission.Operations {
		operation = strings.TrimSpace(operation)
		switch operation {
		case "read", "write", "create", "delete", "execute":
		default:
			return fmt.Errorf("unsupported operation %q", operation)
		}
		if _, duplicate := seen[operation]; duplicate {
			return fmt.Errorf("duplicate operation %q", operation)
		}
		seen[operation] = struct{}{}
	}
	return nil
}

func validateNetworkPermission(permission NetworkPermission) error {
	selectors := 0
	if permission.Domain = strings.TrimSpace(strings.ToLower(permission.Domain)); permission.Domain != "" {
		selectors++
		if !validDomain(permission.Domain) {
			return errors.New("invalid domain")
		}
	}
	if permission.IP = strings.TrimSpace(permission.IP); permission.IP != "" {
		selectors++
		if net.ParseIP(permission.IP) == nil {
			return errors.New("invalid IP")
		}
	}
	if permission.CIDR = strings.TrimSpace(permission.CIDR); permission.CIDR != "" {
		selectors++
		if _, _, err := net.ParseCIDR(permission.CIDR); err != nil {
			return errors.New("invalid CIDR")
		}
	}
	if selectors != 1 || len(permission.Ports) == 0 || !canonicalIdentifier(permission.Purpose, 256) {
		return errors.New("exactly one selector, ports, and purpose are required")
	}
	switch strings.ToLower(strings.TrimSpace(permission.Protocol)) {
	case "tcp", "udp", "http", "https":
	default:
		return errors.New("invalid protocol")
	}
	seen := map[int]struct{}{}
	for _, port := range permission.Ports {
		if port < 1 || port > 65535 {
			return fmt.Errorf("port %d is outside 1..65535", port)
		}
		if _, duplicate := seen[port]; duplicate {
			return fmt.Errorf("duplicate port %d", port)
		}
		seen[port] = struct{}{}
	}
	return nil
}

func validateEgressPermission(permission DataEgressPermission) error {
	if !canonicalIdentifier(permission.Purpose, 256) || !permission.MaxSensitivity.Valid() {
		return errors.New("purpose and max sensitivity are required")
	}
	parsed, err := url.Parse(strings.TrimSpace(permission.Destination))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("destination must be an absolute credential-free https URL without query or fragment")
	}
	return nil
}

func validateNetworkEgressBindings(manifest Manifest) error {
	networkRules := make([]corepolicy.NetworkRule, len(manifest.NetworkPermissions))
	for index, permission := range manifest.NetworkPermissions {
		networkRules[index] = corepolicy.NetworkRule{
			Domain: permission.Domain, IP: permission.IP, CIDR: permission.CIDR,
			Ports: append([]int(nil), permission.Ports...), Protocol: permission.Protocol, Purpose: permission.Purpose,
		}
	}
	egressRules := make([]corepolicy.EgressRule, len(manifest.DataEgressPermissions))
	for index, permission := range manifest.DataEgressPermissions {
		egressRules[index] = corepolicy.EgressRule{
			Destination: permission.Destination, Purpose: permission.Purpose,
			MaxSensitivity: corepolicy.Sensitivity(permission.MaxSensitivity),
		}
	}
	if err := corepolicy.ValidateNetworkEgress(networkRules, egressRules); err != nil {
		return fmt.Errorf("%w: network/data-egress binding: %v", ErrInvalid, err)
	}
	return nil
}

func validateStringSet(name string, values []string, required bool) error {
	if required && len(values) == 0 {
		return fmt.Errorf("%w: at least one %s value is required", ErrInvalid, name)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !canonicalIdentifier(value, 256) {
			return fmt.Errorf("%w: %s contains an invalid value", ErrInvalid, name)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%w: %s contains duplicate %q", ErrInvalid, name, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func manifestsCompatible(current, candidate Manifest) bool {
	if current.ID != candidate.ID || current.ProtocolVersion != candidate.ProtocolVersion {
		return false
	}
	if current.SchemaDigest == candidate.SchemaDigest {
		return true
	}
	for _, digest := range current.CompatibleSchemaDigests {
		if digest == candidate.SchemaDigest {
			return true
		}
	}
	return false
}

func decodePublicKey(value string) (ed25519.PublicKey, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, ErrUnverified
	}
	return ed25519.PublicKey(key), nil
}

func decodeSignatureBytes(value string) ([]byte, error) {
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, ErrUnverified
	}
	return signature, nil
}

func keyID(publicKey ed25519.PublicKey) string { return keyIDForBytes(publicKey) }

func keyIDForBytes(publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	return "ed25519:" + hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func canonicalIdentifier(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}

func validDomain(value string) bool {
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if value == "" || len(value) > 253 || strings.ContainsAny(value, " /:@\\\t\r\n") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func permissionKey(permission NetworkPermission) string {
	ports := make([]int, len(permission.Ports))
	copy(ports, permission.Ports)
	sort.Ints(ports)
	parts := make([]string, len(ports))
	for index, port := range ports {
		parts[index] = fmt.Sprintf("%05d", port)
	}
	return permission.Domain + permission.IP + permission.CIDR + "\x00" + permission.Protocol + "\x00" + strings.Join(parts, ",") + "\x00" + permission.Purpose
}

func toExecutionInstallation(item Installation) extensionport.Installation {
	manifest := item.Manifest
	grant := extensionport.Installation{
		TenantID: item.TenantID, WorkspaceID: item.WorkspaceID, Digest: item.Digest,
		Signature: item.Signature, KeyID: item.KeyID, State: item.State, ActivatedAt: item.ActivatedAt,
		Manifest: extensionport.Manifest{
			ID: manifest.ID, Name: manifest.Name, Publisher: manifest.Publisher, Version: manifest.Version,
			ProtocolVersion: manifest.ProtocolVersion, AdapterVersion: manifest.AdapterVersion,
			SchemaDigest: manifest.SchemaDigest, ArtifactDigest: manifest.ArtifactDigest,
			ExecutionMode: extensionport.ExecutionMode(manifest.ExecutionMode), MaxMessageBytes: manifest.MaxMessageBytes,
			Capabilities: append([]string(nil), manifest.Capabilities...), Permissions: append([]string(nil), manifest.Permissions...),
			CompatibleSchemaDigests: append([]string(nil), manifest.CompatibleSchemaDigests...),
		},
	}
	grant.Manifest.FilePermissions = make([]extensionport.FilePermission, len(manifest.FilePermissions))
	for index, permission := range manifest.FilePermissions {
		grant.Manifest.FilePermissions[index] = extensionport.FilePermission{Path: permission.Path, Operations: append([]string(nil), permission.Operations...)}
	}
	grant.Manifest.NetworkPermissions = make([]extensionport.NetworkPermission, len(manifest.NetworkPermissions))
	for index, permission := range manifest.NetworkPermissions {
		grant.Manifest.NetworkPermissions[index] = extensionport.NetworkPermission{
			Domain: permission.Domain, IP: permission.IP, CIDR: permission.CIDR,
			Ports: append([]int(nil), permission.Ports...), Protocol: permission.Protocol, Purpose: permission.Purpose,
		}
	}
	grant.Manifest.SecretPermissions = make([]extensionport.SecretPermission, len(manifest.SecretPermissions))
	for index, permission := range manifest.SecretPermissions {
		grant.Manifest.SecretPermissions[index] = extensionport.SecretPermission{
			Name: permission.Name, Destination: permission.Destination, Purpose: permission.Purpose,
		}
	}
	grant.Manifest.DataEgressPermissions = make([]extensionport.DataEgressPermission, len(manifest.DataEgressPermissions))
	for index, permission := range manifest.DataEgressPermissions {
		grant.Manifest.DataEgressPermissions[index] = extensionport.DataEgressPermission{
			Destination: permission.Destination, Purpose: permission.Purpose, MaxSensitivity: string(permission.MaxSensitivity),
		}
	}
	return grant
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func cloneManifest(manifest Manifest) Manifest {
	manifest.Capabilities = append([]string(nil), manifest.Capabilities...)
	manifest.Permissions = append([]string(nil), manifest.Permissions...)
	manifest.CompatibleSchemaDigests = append([]string(nil), manifest.CompatibleSchemaDigests...)
	manifest.FilePermissions = append([]FilePermission(nil), manifest.FilePermissions...)
	for index := range manifest.FilePermissions {
		manifest.FilePermissions[index].Operations = append([]string(nil), manifest.FilePermissions[index].Operations...)
	}
	manifest.NetworkPermissions = append([]NetworkPermission(nil), manifest.NetworkPermissions...)
	for index := range manifest.NetworkPermissions {
		manifest.NetworkPermissions[index].Ports = append([]int(nil), manifest.NetworkPermissions[index].Ports...)
	}
	manifest.SecretPermissions = append([]SecretPermission(nil), manifest.SecretPermissions...)
	manifest.DataEgressPermissions = append([]DataEgressPermission(nil), manifest.DataEgressPermissions...)
	return manifest
}

func cloneInstallation(item Installation) Installation {
	item.Manifest = cloneManifest(item.Manifest)
	return item
}

func installationKey(workspaceID, pluginID, version string) string {
	return workspaceID + "\x00" + pluginID + "@" + version
}

func activeSlot(workspaceID, pluginID string) string {
	return workspaceID + "\x00" + pluginID
}

func cloneStringMap(values map[string]string) map[string]string {
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func cloneStringSliceMap(values map[string][]string) map[string][]string {
	copy := make(map[string][]string, len(values))
	for key, value := range values {
		copy[key] = append([]string(nil), value...)
	}
	return copy
}
