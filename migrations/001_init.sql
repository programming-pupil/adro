-- ADRO control-plane schema. The reference binary currently uses the same
-- contracts with an in-memory repository; this migration is the PostgreSQL
-- boundary for production deployments.
create extension if not exists pgcrypto;

create table if not exists requirements (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null,
  key text not null,
  title text not null,
  description text not null,
  acceptance_criteria jsonb not null default '[]',
  priority text not null default 'normal',
  status text not null default 'RECEIVED',
  created_by uuid not null,
  version bigint not null default 1,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (workspace_id, key)
);

create table if not exists requirement_assignees (
  requirement_id uuid not null references requirements(id) on delete cascade,
  member_id uuid not null,
  role text not null default 'developer',
  is_primary boolean not null default false,
  primary key (requirement_id, member_id, role)
);

create table if not exists repositories (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null,
  canonical_name text not null,
  clone_url text not null,
  provider text not null,
  default_branch text not null default 'main',
  language_set jsonb not null default '[]',
  metadata jsonb not null default '{}',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (workspace_id, canonical_name)
);

create table if not exists requirement_repositories (
  requirement_id uuid not null references requirements(id) on delete cascade,
  repository_id uuid not null references repositories(id),
  relation text not null default 'primary',
  source text not null default 'user',
  confidence numeric(5,4),
  primary key (requirement_id, repository_id)
);

create table if not exists work_items (
  id uuid primary key default gen_random_uuid(),
  requirement_id uuid references requirements(id),
  bug_id uuid,
  repository_id uuid not null references repositories(id),
  member_id uuid not null,
  developer_agent_binding_id uuid not null,
  provider_issue_id text,
  status text not null default 'todo',
  stage int not null default 1,
  baseline_commit text,
  head_commit text,
  branch_name text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  check (requirement_id is not null or bug_id is not null)
);

create table if not exists bugs (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null,
  requirement_id uuid references requirements(id),
  work_item_id uuid references work_items(id),
  repository_id uuid,
  fingerprint text not null,
  title text not null,
  steps_to_reproduce text,
  expected text,
  actual text,
  log_excerpt text,
  status text not null default 'OPEN',
  attempt_count int not null default 0,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (workspace_id, requirement_id, work_item_id, fingerprint)
);

create table if not exists provenance (
  id uuid primary key default gen_random_uuid(),
  work_item_id uuid not null references work_items(id),
  requirement_id uuid references requirements(id),
  bug_id uuid references bugs(id),
  agent_binding_id uuid not null,
  provider text not null,
  provider_agent_id text,
  provider_task_id text,
  provider_session_id text,
  repository_id uuid not null,
  baseline_commit text,
  head_commit text,
  context_version bigint not null default 1,
  created_at timestamptz not null default now()
);

create table if not exists evidence_bundles (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null,
  work_item_id uuid not null references work_items(id),
  kind text not null,
  status text not null,
  summary jsonb not null default '{}',
  artifact_uri text,
  content_sha256 text not null,
  producer_run_id uuid not null,
  created_at timestamptz not null default now()
);

create table if not exists event_outbox (
  event_id uuid primary key,
  event_type text not null,
  aggregate_type text not null,
  aggregate_id uuid not null,
  aggregate_version bigint not null,
  tenant_id uuid not null,
  workspace_id uuid not null,
  correlation_id uuid not null,
  causation_id uuid,
  provider text,
  provider_event_id text,
  occurred_at timestamptz not null,
  classification text not null,
  payload jsonb not null,
  published_at timestamptz
);
create unique index if not exists event_outbox_provider_dedupe on event_outbox(provider, provider_event_id) where provider_event_id is not null;
create index if not exists event_outbox_aggregate_cursor on event_outbox(aggregate_id, aggregate_version);

create table if not exists audit_events (
  id bigserial primary key,
  tenant_id uuid not null,
  workspace_id uuid not null,
  actor_type text not null,
  actor_id text not null,
  action text not null,
  correlation_id text not null,
  payload jsonb not null default '{}',
  previous_hash text,
  content_hash text not null,
  created_at timestamptz not null default now()
);

create table if not exists impact_reports (
  id uuid primary key default gen_random_uuid(),
  requirement_id uuid not null references requirements(id) on delete cascade,
  version bigint not null,
  input_snapshot jsonb not null default '{}',
  candidate_repositories jsonb not null default '[]',
  confirmed_repositories jsonb not null default '[]',
  unresolved_risks jsonb not null default '[]',
  status text not null default 'generated',
  created_at timestamptz not null default now(),
  unique (requirement_id, version)
);

create table if not exists team_workspaces (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null,
  name text not null,
  version bigint not null default 1,
  repository_ids jsonb not null default '[]',
  policy jsonb not null default '{}',
  status text not null default 'active',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (workspace_id, name)
);

create table if not exists developer_profiles (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null,
  member_id uuid not null,
  default_agent_binding_id text,
  git_identity jsonb not null default '{}',
  status text not null default 'active',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (workspace_id, member_id)
);

create table if not exists mcp_servers (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null,
  name text not null,
  endpoint text not null,
  protocol text not null,
  schema_digest text,
  scopes jsonb not null default '[]',
  secret_ref text,
  status text not null default 'configured',
  configuration jsonb not null default '{}',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (workspace_id, name)
);

create table if not exists skills (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null,
  name text not null,
  version text not null,
  kind text not null default 'workflow',
  contract jsonb not null default '{}',
  digest text,
  status text not null default 'installed',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (workspace_id, name, version)
);

create table if not exists automations (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null,
  name text not null,
  version bigint not null default 1,
  trigger jsonb not null default '{}',
  nodes jsonb not null default '[]',
  enabled boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (workspace_id, name, version)
);

create table if not exists approvals (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null,
  requirement_id uuid not null references requirements(id),
  kind text not null,
  decision text not null default 'pending',
  decided_by text,
  reason text,
  evidence_ids jsonb not null default '[]',
  created_at timestamptz not null default now(),
  decided_at timestamptz
);

create table if not exists diff_snapshots (
  id uuid primary key default gen_random_uuid(),
  work_item_id uuid not null references work_items(id) on delete cascade,
  repository_id uuid not null references repositories(id),
  base_commit text not null,
  head_commit text not null,
  stat jsonb not null default '{}',
  files jsonb not null default '[]',
  patch text,
  content_sha256 text not null,
  created_at timestamptz not null default now()
);

create table if not exists artifact_migrations (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null,
  artifact_id text not null,
  from_driver text not null,
  to_driver text not null,
  status text not null default 'running',
  copied_objects bigint not null default 0,
  verified_objects bigint not null default 0,
  error text,
  rollback_until timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  check (from_driver <> to_driver)
);

create table if not exists usage_records (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null,
  run_id text not null,
  member_id text,
  model text,
  input_tokens bigint not null default 0,
  output_tokens bigint not null default 0,
  cache_read_tokens bigint not null default 0,
  cache_write_tokens bigint not null default 0,
  duration_ms bigint not null default 0,
  estimated_cost numeric(18,6) not null default 0,
  created_at timestamptz not null default now()
);

-- RLS is enabled for production PostgreSQL deployments. The application must
-- set app.tenant_id and app.workspace_id at transaction start from verified
-- OIDC claims; request JSON and headers are never trusted as authorization.
do $$ declare t text; begin
  foreach t in array array['requirements','repositories','work_items','bugs','evidence_bundles','event_outbox','audit_events','team_workspaces','developer_profiles','mcp_servers','skills','automations','approvals','diff_snapshots','artifact_migrations','usage_records'] loop
    execute format('alter table %I enable row level security', t);
  end loop;
end $$;

do $$ declare t text; begin
  foreach t in array array['requirements','repositories','bugs','evidence_bundles','event_outbox','audit_events','team_workspaces','developer_profiles','mcp_servers','skills','automations','approvals','artifact_migrations','usage_records'] loop
    execute format('drop policy if exists workspace_isolation on %I', t);
    execute format('create policy workspace_isolation on %I using (workspace_id::text = current_setting(''app.workspace_id'', true)) with check (workspace_id::text = current_setting(''app.workspace_id'', true))', t);
  end loop;
end $$;
