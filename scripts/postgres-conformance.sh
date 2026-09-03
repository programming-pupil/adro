#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${ADRO_GO_BIN:-$ROOT_DIR/scripts/e2e-go.sh}"
REPORT_DIR="${ADRO_POSTGRES_EVIDENCE_DIR:-$ROOT_DIR/var/test-report/postgres}"

run_conformance() {
  (cd "$ROOT_DIR" && ADRO_POSTGRES_TEST_DSN="$1" "$GO_BIN" test ./internal/orchestration -run TestPostgresDriverConformance -count=1 -v)
}

millis() {
  ruby -e 'puts (Process.clock_gettime(Process::CLOCK_MONOTONIC) * 1000).to_i'
}

snapshot_fingerprint() {
  psql "$1" -X -v ON_ERROR_STOP=1 -Atqc "SELECT revision::text || ':' || md5(encode(state_json, 'hex')) FROM adro_orchestration_state WHERE id = 1"
}

run_rehearsal() {
  local source_dsn="$1"
  local backup_dsn="$2"
  local admin_dsn="$3"
  local restore_dsn="$4"
  local restore_db="$5"
  local dump_file="$6"
  if [[ ! "$restore_db" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
    printf 'invalid PostgreSQL restore database name: %s\n' "$restore_db" >&2
    return 2
  fi

  local source_fingerprint
  source_fingerprint="$(snapshot_fingerprint "$source_dsn")"
  if [[ -z "$source_fingerprint" ]]; then
    printf 'PostgreSQL source snapshot fingerprint is empty\n' >&2
    return 1
  fi
  # FORCE ROW LEVEL SECURITY correctly prevents the application role from
  # reading every workspace. Operators must supply a distinct backup identity
  # with audited BYPASSRLS/read privileges; silently weakening RLS is forbidden.
  pg_dump --format=custom --no-owner --no-privileges --file "$dump_file" "$backup_dsn"

  local started finished restored_fingerprint
  started="$(millis)"
  psql "$admin_dsn" -X -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS \"$restore_db\" WITH (FORCE)" >/dev/null
  psql "$admin_dsn" -X -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$restore_db\"" >/dev/null
  pg_restore --exit-on-error --no-owner --no-privileges --dbname "$restore_dsn" "$dump_file"
  restored_fingerprint="$(snapshot_fingerprint "$restore_dsn")"
  finished="$(millis)"

  if [[ "$source_fingerprint" != "$restored_fingerprint" ]]; then
    printf 'PostgreSQL restore fingerprint mismatch: source=%s restored=%s\n' "$source_fingerprint" "$restored_fingerprint" >&2
    return 1
  fi
  local rto_ms=$((finished - started))
  mkdir -p "$REPORT_DIR"
  REPORT_DIR="$REPORT_DIR" SOURCE_FINGERPRINT="$source_fingerprint" RESTORED_FINGERPRINT="$restored_fingerprint" RTO_MS="$rto_ms" DUMP_BYTES="$(wc -c < "$dump_file" | tr -d ' ')" ruby -rjson -e '
    report = {
      "schema_version" => 1,
      "profile" => "postgresql-operational",
      "backup_format" => "pg_dump-custom",
      "restore_tool" => "pg_restore",
      "source_fingerprint" => ENV.fetch("SOURCE_FINGERPRINT"),
      "restored_fingerprint" => ENV.fetch("RESTORED_FINGERPRINT"),
      "rto_ms" => ENV.fetch("RTO_MS").to_i,
      "rpo_records" => 0,
      "dump_bytes" => ENV.fetch("DUMP_BYTES").to_i,
      "result" => "passed"
    }
    File.write(File.join(ENV.fetch("REPORT_DIR"), "postgres-backup-restore.json"), JSON.pretty_generate(report) + "\n")
    File.write(File.join(ENV.fetch("REPORT_DIR"), "postgres-backup-restore.md"), "# PostgreSQL backup and restore rehearsal\n\n- Result: passed\n- Backup: pg_dump custom format\n- Restore: pg_restore into a newly created database\n- Snapshot fingerprint: `#{report["source_fingerprint"]}`\n- RTO: #{report["rto_ms"]} ms\n- RPO: #{report["rpo_records"]} records\n- Dump size: #{report["dump_bytes"]} bytes\n")
    puts JSON.generate(report)
  '
}

if [[ -n "${ADRO_POSTGRES_TEST_DSN:-}" ]]; then
  for command in psql pg_dump pg_restore ruby; do
    if ! command -v "$command" >/dev/null 2>&1; then
      printf 'blocked_external_prerequisite: %s is required for PostgreSQL conformance\n' "$command" >&2
      exit 2
    fi
  done
  if [[ -z "${ADRO_POSTGRES_BACKUP_DSN:-}" || -z "${ADRO_POSTGRES_ADMIN_DSN:-}" || -z "${ADRO_POSTGRES_RESTORE_DSN:-}" || -z "${ADRO_POSTGRES_RESTORE_DB:-}" ]]; then
    printf 'blocked_external_prerequisite: ADRO_POSTGRES_BACKUP_DSN, ADRO_POSTGRES_ADMIN_DSN, ADRO_POSTGRES_RESTORE_DSN and ADRO_POSTGRES_RESTORE_DB are required for operational restore evidence\n' >&2
    exit 2
  fi
  run_conformance "$ADRO_POSTGRES_TEST_DSN"
  evidence_root="$(mktemp -d "${TMPDIR:-/tmp}/adro-pg-evidence.XXXXXX")"
  trap 'rm -rf "$evidence_root"' EXIT
  run_rehearsal "$ADRO_POSTGRES_TEST_DSN" "$ADRO_POSTGRES_BACKUP_DSN" "$ADRO_POSTGRES_ADMIN_DSN" "$ADRO_POSTGRES_RESTORE_DSN" "$ADRO_POSTGRES_RESTORE_DB" "$evidence_root/adro.dump"
  exit 0
fi

for command in initdb pg_ctl createuser createdb psql pg_dump pg_restore ruby; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'blocked_external_prerequisite: %s is required for local PostgreSQL conformance\n' "$command" >&2
    exit 2
  fi
done

PG_TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/adro-pg-conformance.XXXXXX")"
PG_DATA="$PG_TEST_ROOT/data"
PG_SOCKET="$PG_TEST_ROOT/socket"
PG_PORT="${ADRO_POSTGRES_TEST_PORT:-$(ruby -rsocket -e 'server = TCPServer.new("127.0.0.1", 0); puts server.addr[1]; server.close')}"
mkdir -p "$PG_SOCKET"

cleanup() {
  if [[ -f "$PG_DATA/postmaster.pid" ]]; then
    pg_ctl -D "$PG_DATA" -m fast -w stop >/dev/null
  fi
  rm -rf "$PG_TEST_ROOT"
}
trap cleanup EXIT

initdb -D "$PG_DATA" -A trust --no-locale -E UTF8 >/dev/null
pg_ctl -D "$PG_DATA" -o "-k $PG_SOCKET -p $PG_PORT" -w start >/dev/null
createuser -h "$PG_SOCKET" -p "$PG_PORT" --createdb adro_app
createuser -h "$PG_SOCKET" -p "$PG_PORT" --superuser adro_backup
createdb -h "$PG_SOCKET" -p "$PG_PORT" -O adro_app adro_test

source_dsn="host=$PG_SOCKET port=$PG_PORT dbname=adro_test user=adro_app sslmode=disable"
admin_dsn="host=$PG_SOCKET port=$PG_PORT dbname=postgres user=adro_app sslmode=disable"
restore_dsn="host=$PG_SOCKET port=$PG_PORT dbname=adro_restore user=adro_app sslmode=disable"
backup_dsn="host=$PG_SOCKET port=$PG_PORT dbname=adro_test user=adro_backup sslmode=disable"
run_conformance "$source_dsn"
run_rehearsal "$source_dsn" "$backup_dsn" "$admin_dsn" "$restore_dsn" adro_restore "$PG_TEST_ROOT/adro.dump"
