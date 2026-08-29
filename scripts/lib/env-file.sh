#!/usr/bin/env bash

# Atomically update a key while preserving the credential-file boundary. The
# temporary inode is made private before rename so umask and an existing 0644
# target can never weaken the final mode.
set_env_key() {
  local file="$1" key="$2" value="$3" temp_file
  [ -f "$file" ] || { printf 'set_env_key: %s does not exist\n' "$file" >&2; return 1; }
  temp_file="$(mktemp "${file}.tmp.XXXXXX")" || return 1
  if ! awk -v key="$key" -v value="$value" '
    BEGIN { found = 0 }
    index($0, key "=") == 1 { print key "=" value; found = 1; next }
    { print }
    END { if (!found) print key "=" value }
  ' "$file" > "$temp_file"; then
    rm -f "$temp_file"
    return 1
  fi
  chmod 0600 "$temp_file" || { rm -f "$temp_file"; return 1; }
  mv -f "$temp_file" "$file"
}
