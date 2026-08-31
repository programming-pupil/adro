-- ADRO-owned durable harness state. These tables are intentionally separate
-- from provider-native sessions so a provider restart can never erase the
-- transcript or make an already-dispatched side effect ambiguous.
create table if not exists harness_sessions (
  id uuid primary key,
  tenant_id uuid not null,
  workspace_id uuid not null,
  budget_tokens bigint not null default 0 check (budget_tokens >= 0),
  context_version bigint not null default 1 check (context_version > 0),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists session_turns (
  id uuid primary key,
  session_id uuid not null references harness_sessions(id) on delete restrict,
  tenant_id uuid not null,
  workspace_id uuid not null,
  sequence bigint not null check (sequence > 0),
  attempt_id text,
  role text not null check (role in ('system', 'user', 'assistant', 'tool')),
  content text not null,
  tool_name text,
  tool_call_id text,
  tool_status text,
  idempotency_key text,
  metadata jsonb not null default '{}',
  prev_hash text,
  content_hash text not null,
  created_at timestamptz not null default now(),
  unique (session_id, sequence),
  unique (session_id, content_hash),
  unique (session_id, idempotency_key)
);

create table if not exists session_checkpoints (
  id uuid primary key,
  session_id uuid not null references harness_sessions(id) on delete restrict,
  tenant_id uuid not null,
  workspace_id uuid not null,
  turn_sequence bigint not null check (turn_sequence >= 0),
  phase text not null,
  event_hash text,
  context_version bigint not null check (context_version > 0),
  outbox_ids jsonb not null default '[]',
  lease_ids jsonb not null default '[]',
  state text,
  checkpoint_hash text not null,
  created_at timestamptz not null default now()
);
create index if not exists session_checkpoints_cursor on session_checkpoints(session_id, turn_sequence, created_at);

create table if not exists context_archives (
  id uuid primary key,
  session_id uuid not null references harness_sessions(id) on delete restrict,
  tenant_id uuid not null,
  workspace_id uuid not null,
  start_sequence bigint not null check (start_sequence > 0),
  end_sequence bigint not null check (end_sequence >= start_sequence),
  source_hash text not null,
  replacement_hash text not null,
  summary text not null,
  retained_tail integer not null default 0 check (retained_tail >= 0),
  parent_archive_id uuid references context_archives(id),
  reason text,
  created_at timestamptz not null default now(),
  unique (session_id, start_sequence, end_sequence)
);
create index if not exists context_archives_cursor on context_archives(session_id, start_sequence, end_sequence);

create table if not exists memory_items (
  id uuid primary key,
  session_id uuid not null references harness_sessions(id) on delete restrict,
  tenant_id uuid not null,
  workspace_id uuid not null,
  kind text not null,
  content text not null,
  source_ids jsonb not null,
  confidence numeric(4,3) not null check (confidence >= 0 and confidence <= 1),
  supersedes jsonb not null default '[]',
  created_at timestamptz not null default now()
);

create table if not exists execution_leases (
  id uuid primary key,
  session_id uuid not null references harness_sessions(id) on delete restrict,
  tenant_id uuid not null,
  workspace_id uuid not null,
  lease_key text not null,
  owner text not null,
  state text not null check (state in ('held', 'released', 'expired')),
  version bigint not null default 1,
  expires_at timestamptz not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (session_id, lease_key, version)
);
create index if not exists execution_leases_active on execution_leases(session_id, lease_key, state, expires_at);

create table if not exists outbox_events (
  id uuid primary key,
  session_id uuid not null references harness_sessions(id) on delete restrict,
  tenant_id uuid not null,
  workspace_id uuid not null,
  idempotency_key text not null,
  payload jsonb not null,
  state text not null check (state in ('pending', 'processing', 'published')),
  attempts integer not null default 0 check (attempts >= 0),
  owner text,
  lease_until timestamptz,
  next_attempt_at timestamptz not null default now(),
  published_at timestamptz,
  created_at timestamptz not null default now(),
  unique (session_id, idempotency_key)
);
create index if not exists outbox_events_pending on outbox_events(state, next_attempt_at);

create table if not exists plugin_installations (
  id uuid primary key default gen_random_uuid(),
  plugin_id text not null,
  version text not null,
  protocol_version text not null,
  manifest jsonb not null,
  digest text not null,
  signature text not null,
  public_key text not null,
  state text not null check (state in ('verified', 'active', 'degraded', 'quarantined')),
  consecutive_errors integer not null default 0,
  health_message text,
  last_health_at timestamptz,
  tenant_id uuid not null,
  workspace_id uuid not null,
  installed_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (workspace_id, plugin_id, version, digest)
);
create index if not exists plugin_installations_active on plugin_installations(workspace_id, state);

do $$ declare t text; begin
  foreach t in array array['harness_sessions','session_turns','session_checkpoints','context_archives','memory_items','execution_leases','outbox_events','plugin_installations'] loop
    execute format('alter table %I enable row level security', t);
    execute format('drop policy if exists workspace_isolation on %I', t);
    execute format('create policy workspace_isolation on %I using (workspace_id::text = current_setting(''app.workspace_id'', true)) with check (workspace_id::text = current_setting(''app.workspace_id'', true))', t);
  end loop;
end $$;
