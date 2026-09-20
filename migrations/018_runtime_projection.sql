-- Rebuildable runtime projections. These rows are never authoritative; every
-- record carries its source stream and sequence so workers can replay from
-- sequence zero and reject stale or cross-tenant writes.

CREATE TABLE IF NOT EXISTS runtime_projections (
  tenant_id text NOT NULL,
  projection_name text NOT NULL,
  record_key text NOT NULL,
  version bigint NOT NULL CHECK (version > 0),
  source_stream text NOT NULL,
  source_sequence bigint NOT NULL CHECK (source_sequence > 0),
  digest text NOT NULL,
  payload bytea NOT NULL,
  updated_at_us bigint NOT NULL,
  PRIMARY KEY (tenant_id, projection_name, record_key)
);

CREATE INDEX IF NOT EXISTS runtime_projections_source_idx
  ON runtime_projections(tenant_id, source_stream, source_sequence);
