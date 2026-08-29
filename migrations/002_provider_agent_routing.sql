-- Immutable provider bindings make routing decisions replayable. This migration
-- is additive and deliberately has no foreign keys: provider objects may be
-- retained after an external workspace is removed.
create table if not exists provider_bindings (
  id text primary key,
  workspace_id uuid not null,
  provider text not null,
  kind text not null,
  provider_object_id text,
  status text not null default 'configured',
  source text,
  config_revision text,
  created_at timestamptz not null default now()
);

alter table work_items
  alter column developer_agent_binding_id type text using developer_agent_binding_id::text;
alter table work_items
  alter column developer_agent_binding_id drop not null;
alter table work_items add column if not exists role text;
alter table work_items add column if not exists agent_route_source text;
alter table work_items add column if not exists routing_config_revision text;
alter table developer_profiles add column if not exists default_role text;
