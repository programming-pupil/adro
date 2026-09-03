#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

for command in node ssh-keygen; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'release signing test requires %s\n' "$command" >&2
    exit 2
  fi
done

TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/adro-release-signing.XXXXXX")"
trap 'rm -rf "$TEST_ROOT"' EXIT

key="$TEST_ROOT/release-key"
manifest="$TEST_ROOT/release-manifest.json"
signature="$TEST_ROOT/release-manifest.json.sig"
allowed_signers="$TEST_ROOT/release-allowed-signers"

ssh-keygen -q -t ed25519 -N '' -C adro-release-test -f "$key"
printf '%s\n' '{"schema_version":1,"product":"ADRO","commit":"test"}' > "$manifest"

node "$ROOT_DIR/scripts/release-assets.mjs" sign-manifest "$manifest" \
  --key "$key" --signature "$signature" --allowed-signers "$allowed_signers"
node "$ROOT_DIR/scripts/release-assets.mjs" verify-signature "$manifest" \
  --signature "$signature" --allowed-signers "$allowed_signers"

printf '%s\n' '{"schema_version":1,"product":"ADRO","commit":"tampered"}' > "$manifest"
if node "$ROOT_DIR/scripts/release-assets.mjs" verify-signature "$manifest" \
  --signature "$signature" --allowed-signers "$allowed_signers" >/dev/null 2>&1; then
  printf 'release signature verification accepted a tampered manifest\n' >&2
  exit 1
fi

printf 'release manifest signing and tamper rejection passed\n'
