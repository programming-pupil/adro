-- Immutable comment edit and attachment membership lineage.
--
-- Existing comment rows become revision 1. The application writes a snapshot
-- for every later optimistic edit and uses comment_revision in follow-up
-- identities so an edited mention cannot reuse an earlier provider run.

alter table if exists comments
  add column if not exists revision bigint not null default 1 check (revision > 0);
alter table if exists comments
  add column if not exists attachment_ids jsonb not null default '[]'::jsonb;

alter table if exists comment_follow_ups
  add column if not exists comment_revision bigint not null default 1 check (comment_revision > 0);
alter table if exists comment_follow_ups
  add column if not exists dispatch_target_type text not null default 'agent';
alter table if exists comment_follow_ups
  add column if not exists dispatch_target_id text not null default '';
alter table if exists comment_follow_ups
  add column if not exists dedupe_key text not null default '';
create unique index if not exists comment_follow_ups_revision_target
  on comment_follow_ups(comment_id, comment_revision, dispatch_target_type, dispatch_target_id);

create table if not exists comment_revisions (
  comment_id uuid not null references comments(id) on delete restrict,
  tenant_id uuid not null,
  workspace_id uuid not null,
  revision bigint not null check (revision > 0),
  content text not null check (length(trim(content)) > 0),
  mentions jsonb not null default '[]'::jsonb,
  attachment_ids jsonb not null default '[]'::jsonb,
  trigger_outcomes jsonb not null default '[]'::jsonb,
  editor_id text not null,
  editor_type text not null check (editor_type in ('member', 'agent', 'system')),
  created_at timestamptz not null default now(),
  primary key (comment_id, revision)
);
create index if not exists comment_revisions_scope
  on comment_revisions(tenant_id, workspace_id, comment_id, revision);

alter table comment_revisions enable row level security;
alter table comment_revisions force row level security;
drop policy if exists workspace_isolation on comment_revisions;
create policy workspace_isolation on comment_revisions
  using (
    tenant_id::text = current_setting('app.tenant_id', true)
    and workspace_id::text = current_setting('app.workspace_id', true)
  )
  with check (
    tenant_id::text = current_setting('app.tenant_id', true)
    and workspace_id::text = current_setting('app.workspace_id', true)
  );

-- Rollback is intentionally non-destructive: stop writers, drop only the
-- comment_revisions table/index and the new unique follow-up index, and retain
-- additive columns so an older binary can still read the rows.
