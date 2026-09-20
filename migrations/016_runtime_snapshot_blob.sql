-- Durable snapshot and blob boundaries for the Runtime reference adapters.
-- Event history remains authoritative; snapshots are rebuildable caches and
-- blob rows contain references/metadata rather than inline event payloads.

CREATE TABLE IF NOT EXISTS runtime_snapshots (
  tenant_id text NOT NULL,
  stream_id text NOT NULL,
  sequence bigint NOT NULL CHECK (sequence > 0),
  schema_version integer NOT NULL CHECK (schema_version > 0),
  encoding_version integer NOT NULL CHECK (encoding_version > 0),
  hash_algorithm text NOT NULL,
  hash_version integer NOT NULL CHECK (hash_version > 0),
  digest text NOT NULL,
  payload bytea NOT NULL,
  created_at_us bigint NOT NULL,
  PRIMARY KEY (tenant_id, stream_id)
);

CREATE TABLE IF NOT EXISTS runtime_blobs (
  tenant_id text NOT NULL,
  digest text NOT NULL,
  size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
  media_type text NOT NULL DEFAULT '',
  encryption_key text NOT NULL DEFAULT '',
  classification text NOT NULL DEFAULT '',
  retain_until_us bigint,
  created_at_us bigint NOT NULL,
  tombstoned boolean NOT NULL DEFAULT false,
  legal_hold boolean NOT NULL DEFAULT false,
  tombstone text NOT NULL DEFAULT '',
  stored_digest text NOT NULL DEFAULT '',
  payload bytea NOT NULL,
  PRIMARY KEY (tenant_id, digest)
);
CREATE INDEX IF NOT EXISTS runtime_blobs_retention_idx
  ON runtime_blobs (tenant_id, retain_until_us, tombstoned);
