#!/usr/bin/env sh
set -eu
api="${ADRO_API_URL:-http://127.0.0.1:8080}"
response=$(curl -fsS -X POST "$api/api/v1/requirements" \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: three-repo-demo' \
  -d '{"workspace_id":"demo","title":"Add invite contract","description":"Expose POST /invite and migrate the caller","acceptance_criteria":["provider returns code 0","caller compiles against InviteResponse"],"assignee_member_ids":["demo-developer"],"repository_ids":["feign-contract","provider-service","caller-service"]}')
id=$(printf '%s' "$response" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
test -n "$id"
curl -fsS -X POST "$api/api/v1/requirements/$id/start" >/dev/null
curl -fsS "$api/api/v1/requirements/$id/work-items"
