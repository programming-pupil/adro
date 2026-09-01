-- Provider-neutral discussion threads for requirements and bugs. Comments are
-- immutable facts; replies use parent_id/root_id so a client can rebuild a
-- thread without relying on provider-native issue semantics.
create table if not exists comments (
  id uuid primary key,
  tenant_id uuid not null,
  workspace_id uuid not null,
  target_type text not null check (target_type in ('requirement', 'bug')),
  target_id uuid not null,
  parent_id uuid references comments(id) on delete restrict,
  root_id uuid not null references comments(id) on delete restrict,
  author_id text not null,
  author_type text not null check (author_type in ('member', 'agent', 'system')),
  content text not null check (length(trim(content)) > 0),
  mentions jsonb not null default '[]',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  check (parent_id is null or parent_id <> id)
);
create index if not exists comments_target_cursor on comments(workspace_id, target_type, target_id, created_at, id);
create index if not exists comments_thread_cursor on comments(workspace_id, root_id, created_at, id);

alter table comments enable row level security;
drop policy if exists workspace_isolation on comments;
create policy workspace_isolation on comments
  using (workspace_id::text = current_setting('app.workspace_id', true))
  with check (workspace_id::text = current_setting('app.workspace_id', true));
