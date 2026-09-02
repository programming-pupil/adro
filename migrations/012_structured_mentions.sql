-- Structured comment mentions and per-target trigger outcomes.
CREATE TABLE IF NOT EXISTS comment_mentions (
  comment_id text NOT NULL, target_type text NOT NULL, target_id text NOT NULL,
  parser_version text NOT NULL, authority_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  dedupe_key text NOT NULL, outcome_json jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (comment_id, target_type, target_id, parser_version),
  UNIQUE (dedupe_key)
);
CREATE INDEX IF NOT EXISTS comment_mentions_target_idx ON comment_mentions(target_type, target_id, created_at DESC);
