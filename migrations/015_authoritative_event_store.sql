-- Authoritative runtime EventStore shared by the SQLite reference profile and
-- the PostgreSQL production adapter. Stream IDs are globally unique opaque IDs;
-- every row still carries an explicit tenant boundary for verification,
-- retention, export and future scoped read ports.

CREATE TABLE IF NOT EXISTS event_streams (
  stream_id text PRIMARY KEY,
  tenant_id text NOT NULL,
  workspace_id text NOT NULL,
  current_sequence bigint NOT NULL CHECK (current_sequence >= 0),
  head_digest text NOT NULL,
  updated_at_us bigint NOT NULL,
  UNIQUE (tenant_id, stream_id)
);

CREATE TABLE IF NOT EXISTS event_records (
  tenant_id text NOT NULL,
  stream_id text NOT NULL,
  sequence bigint NOT NULL CHECK (sequence > 0),
  event_id text NOT NULL UNIQUE,
  event_type text NOT NULL,
  idempotency_key text NOT NULL,
  append_digest text NOT NULL,
  payload_digest text NOT NULL,
  previous_digest text NOT NULL DEFAULT '',
  envelope_digest text NOT NULL,
  envelope_json bytea NOT NULL,
  committed_at_us bigint NOT NULL,
  PRIMARY KEY (tenant_id, stream_id, sequence),
  FOREIGN KEY (tenant_id, stream_id) REFERENCES event_streams(tenant_id, stream_id),
  UNIQUE (tenant_id, stream_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS event_records_stream_sequence_idx
  ON event_records(stream_id, sequence);

-- The legacy schema already owns event_outbox. Keep the authoritative runtime
-- delivery queue distinct until shadow comparison and read cutover complete.
CREATE TABLE IF NOT EXISTS runtime_event_outbox (
  id bigserial PRIMARY KEY,
  tenant_id text NOT NULL,
  stream_id text NOT NULL,
  sequence bigint NOT NULL,
  ordinal integer NOT NULL,
  topic text NOT NULL,
  message_key text NOT NULL,
  payload bytea NOT NULL,
  created_at_us bigint NOT NULL,
  FOREIGN KEY (tenant_id, stream_id) REFERENCES event_streams(tenant_id, stream_id),
  UNIQUE (tenant_id, topic, message_key),
  UNIQUE (tenant_id, stream_id, sequence, ordinal)
);

CREATE TABLE IF NOT EXISTS event_snapshots (
  tenant_id text NOT NULL,
  stream_id text NOT NULL,
  sequence bigint NOT NULL CHECK (sequence > 0),
  digest text NOT NULL,
  payload bytea NOT NULL,
  updated_at_us bigint NOT NULL,
  PRIMARY KEY (tenant_id, stream_id),
  FOREIGN KEY (tenant_id, stream_id) REFERENCES event_streams(tenant_id, stream_id)
);

CREATE TABLE IF NOT EXISTS event_leases (
  tenant_id text NOT NULL,
  stream_id text NOT NULL,
  owner text NOT NULL,
  fencing_token bigint NOT NULL CHECK (fencing_token > 0),
  expires_at_us bigint NOT NULL,
  updated_at_us bigint NOT NULL,
  PRIMARY KEY (tenant_id, stream_id)
);

-- Rollback is application-first: disable new EventStore writers and restore
-- the legacy read path before dropping these tables in dependency order.
-- Never drop them while a binary can still commit authoritative events.
