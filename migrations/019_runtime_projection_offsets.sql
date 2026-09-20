-- Durable checkpoints for rebuildable projection workers. The digest is a
-- deterministic digest of the projection rows at last_sequence; event history
-- remains authoritative and can rebuild the read model after a reset.

CREATE TABLE IF NOT EXISTS runtime_projection_offsets (
  tenant_id text NOT NULL,
  projection_name text NOT NULL,
  partition_id text NOT NULL,
  last_sequence bigint NOT NULL CHECK (last_sequence >= 0),
  projection_digest text NOT NULL,
  updated_at_us bigint NOT NULL,
  PRIMARY KEY (tenant_id, projection_name, partition_id)
);
