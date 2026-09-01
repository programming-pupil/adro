-- Durable workflow selection and ordinary chat projection.
-- The reference profile stores these records in its versioned JSON snapshot;
-- PostgreSQL adapters should apply this migration before enabling the APIs.
CREATE TABLE IF NOT EXISTS workflow_templates (
  id text PRIMARY KEY,
  workspace_id text NOT NULL,
  name text NOT NULL,
  description text NOT NULL DEFAULT '',
  mode text NOT NULL CHECK (mode IN ('automatic', 'design_approval')),
  steps jsonb NOT NULL,
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, name)
);
CREATE TABLE IF NOT EXISTS chat_sessions (
  id text PRIMARY KEY,
  workspace_id text NOT NULL,
  project_id text NOT NULL DEFAULT '',
  title text NOT NULL,
  harness_session_id text NOT NULL UNIQUE,
  status text NOT NULL DEFAULT 'active',
  created_by text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS chat_messages (
  id text PRIMARY KEY,
  chat_session_id text NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
  workspace_id text NOT NULL,
  role text NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
  content text NOT NULL,
  attachment_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
  turn_id text NOT NULL DEFAULT '',
  turn_hash text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS chat_sessions_workspace_project_idx ON chat_sessions(workspace_id, project_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS chat_messages_session_created_idx ON chat_messages(chat_session_id, created_at);
