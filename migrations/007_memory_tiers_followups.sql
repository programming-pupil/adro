-- Tiered deterministic memory and durable comment follow-up receipts.
-- Vector indexes are deliberately not required: scope, pinning, importance,
-- source citations and supersession provide predictable bounded compilation.

alter table harness_sessions add column if not exists project_id text;
alter table harness_sessions add column if not exists auto_compaction boolean not null default true;
alter table harness_sessions add column if not exists compaction_threshold numeric(4,3) not null default 0.800;
alter table harness_sessions add column if not exists compaction_retain_tail integer not null default 4;

alter table memory_items add column if not exists scope text not null default 'session';
alter table memory_items add column if not exists project_id text;
alter table memory_items add column if not exists importance numeric(4,3) not null default 0;
alter table memory_items add column if not exists pinned boolean not null default false;
alter table memory_items add column if not exists expires_at timestamptz;
create index if not exists memory_items_project_frontier on memory_items(workspace_id, project_id, scope, pinned, importance, created_at);

create table if not exists comment_follow_ups (
  id uuid primary key default gen_random_uuid(),
  comment_id uuid not null unique references comments(id) on delete restrict,
  tenant_id uuid not null,
  workspace_id uuid not null,
  target_type text not null check (target_type in ('requirement', 'bug')),
  target_id uuid not null,
  agent_binding_id text not null,
  harness_session_id uuid,
  context_version bigint not null default 1 check (context_version > 0),
  turn_id uuid,
  turn_hash text,
  outbox_id uuid,
  provider_run_id text,
  provider_session_id text,
  provider_work_dir text,
  mode text,
  status text not null,
  reason text,
  attempts integer not null default 0 check (attempts >= 0),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create index if not exists comment_follow_ups_target on comment_follow_ups(workspace_id, target_type, target_id, updated_at);

alter table comment_follow_ups enable row level security;
drop policy if exists workspace_isolation on comment_follow_ups;
create policy workspace_isolation on comment_follow_ups
  using (workspace_id::text = current_setting('app.workspace_id', true))
  with check (workspace_id::text = current_setting('app.workspace_id', true));
