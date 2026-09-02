-- Graph execution profile. The reference SQLite store keeps equivalent JSON
-- projections; PostgreSQL adapters use these immutable facts and rebuildable
-- projections for crash recovery and replay.
CREATE TABLE IF NOT EXISTS agent_definitions (
  id text NOT NULL, workspace_id text NOT NULL, revision bigint NOT NULL,
  name text NOT NULL, role text NOT NULL DEFAULT '', spec_json jsonb NOT NULL,
  status text NOT NULL, created_by text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, id, revision)
);
CREATE TABLE IF NOT EXISTS squad_definitions (
  id text NOT NULL, workspace_id text NOT NULL, revision bigint NOT NULL,
  name text NOT NULL, spec_json jsonb NOT NULL, status text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, id, revision)
);
CREATE TABLE IF NOT EXISTS workflow_graphs (
  id text NOT NULL, workspace_id text NOT NULL, version bigint NOT NULL,
  graph_json jsonb NOT NULL, validation_digest text NOT NULL,
  PRIMARY KEY (workspace_id, id, version)
);
CREATE TABLE IF NOT EXISTS execution_plans (
  id text PRIMARY KEY, requirement_id text NOT NULL, workspace_id text NOT NULL,
  plan_hash text NOT NULL, snapshot_json jsonb NOT NULL, revision bigint NOT NULL,
  status text NOT NULL, idempotency_key text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, idempotency_key)
);
CREATE TABLE IF NOT EXISTS workflow_nodes (
  plan_id text NOT NULL, node_id text NOT NULL, kind text NOT NULL,
  status text NOT NULL, projection_json jsonb NOT NULL,
  PRIMARY KEY (plan_id, node_id)
);
CREATE TABLE IF NOT EXISTS node_attempts (
  id text PRIMARY KEY, plan_id text NOT NULL, node_id text NOT NULL,
  attempt_no integer NOT NULL, run_id text NOT NULL DEFAULT '', session_id text NOT NULL DEFAULT '',
  workdir text NOT NULL DEFAULT '', status text NOT NULL, input_manifest_json jsonb NOT NULL,
  result_json jsonb NOT NULL, lease_json jsonb NOT NULL,
  UNIQUE (plan_id, node_id, attempt_no)
);
CREATE TABLE IF NOT EXISTS workflow_edge_decisions (
  id text PRIMARY KEY, plan_id text NOT NULL, source_attempt_id text NOT NULL,
  edge_id text NOT NULL, target_node_id text NOT NULL, predicate_json jsonb NOT NULL,
  evidence_json jsonb NOT NULL, loop_count integer NOT NULL DEFAULT 0,
  idempotency_key text NOT NULL DEFAULT '', UNIQUE (plan_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS node_attempts_plan_node_idx ON node_attempts(plan_id, node_id, attempt_no);
