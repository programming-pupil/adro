-- Production hardening for the graph orchestration contract.
--
-- Migration 011 was originally shipped as a graph-only schema.  This
-- additive migration brings the durable event/outbox and structured mention
-- records under the same tenant/workspace boundary.  It is intentionally
-- expand-only: existing rows receive an empty tenant marker and deployments
-- must backfill it before enabling FORCE ROW LEVEL SECURITY.

alter table if exists agent_definitions
  add column if not exists tenant_id text not null default '';
alter table if exists agent_definitions
  add column if not exists workspace_id text not null default '';
alter table if exists squad_definitions
  add column if not exists tenant_id text not null default '';
alter table if exists squad_definitions
  add column if not exists workspace_id text not null default '';
alter table if exists workflow_graphs
  add column if not exists tenant_id text not null default '';
alter table if exists workflow_graphs
  add column if not exists workspace_id text not null default '';
alter table if exists execution_plans
  add column if not exists tenant_id text not null default '';
alter table if exists execution_plans
  add column if not exists workspace_id text not null default '';
alter table if exists workflow_nodes
  add column if not exists tenant_id text not null default '';
alter table if exists workflow_nodes
  add column if not exists workspace_id text not null default '';
alter table if exists node_attempts
  add column if not exists tenant_id text not null default '';
alter table if exists node_attempts
  add column if not exists workspace_id text not null default '';
alter table if exists workflow_edge_decisions
  add column if not exists tenant_id text not null default '';
alter table if exists workflow_edge_decisions
  add column if not exists workspace_id text not null default '';

create table if not exists execution_events (
  event_id text primary key,
  tenant_id text not null,
  workspace_id text not null,
  plan_id text not null,
  run_id text not null default '',
  node_id text not null default '',
  attempt_id text not null default '',
  sequence bigint not null check (sequence > 0),
  event_type text not null,
  payload_json jsonb not null,
  payload_hash text not null,
  previous_hash text,
  envelope_hash text not null,
  idempotency_key text not null default '',
  writer_id text not null default '',
  fencing_token bigint not null default 0,
  committed_at timestamptz not null default now(),
  unique (plan_id, sequence),
  unique (plan_id, idempotency_key)
);
create index if not exists execution_events_scope_cursor
  on execution_events(tenant_id, workspace_id, plan_id, sequence);

create table if not exists orchestration_outbox_events (
  id text primary key,
  tenant_id text not null,
  workspace_id text not null,
  plan_id text not null,
  idempotency_key text not null,
  kind text not null,
  payload_json jsonb not null,
  status text not null check (status in ('pending', 'leased', 'acked', 'failed')),
  owner text,
  lease_expires_at timestamptz,
  attempts integer not null default 0 check (attempts >= 0),
  max_attempts integer not null default 8 check (max_attempts > 0),
  last_error text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (plan_id, idempotency_key)
);
alter table if exists orchestration_outbox_events
  add column if not exists max_attempts integer not null default 8;
create index if not exists orchestration_outbox_claim_idx
  on orchestration_outbox_events(plan_id, status, lease_expires_at, created_at);

alter table if exists comment_mentions
  add column if not exists tenant_id text not null default '';
alter table if exists comment_mentions
  add column if not exists workspace_id text not null default '';
alter table if exists comment_mentions
  add column if not exists source_hash text not null default '';
alter table if exists comment_mentions
  add column if not exists updated_at timestamptz not null default now();
create index if not exists comment_mentions_scope_idx
  on comment_mentions(tenant_id, workspace_id, comment_id, created_at desc);

-- RLS is enabled (rather than silently relying on every handler to filter) for
-- every orchestration fact.  The application sets app.tenant_id and
-- app.workspace_id at transaction start.  Empty legacy markers remain
-- inaccessible until an operator performs the documented backfill.
do $$
declare
  table_name text;
begin
  foreach table_name in array array[
    'agent_definitions', 'squad_definitions', 'workflow_graphs',
    'execution_plans', 'workflow_nodes', 'node_attempts',
    'workflow_edge_decisions', 'execution_events',
    'orchestration_outbox_events', 'comment_mentions'
  ] loop
    execute format('alter table %I enable row level security', table_name);
    execute format('alter table %I force row level security', table_name);
    execute format('drop policy if exists adro_scope_isolation on %I', table_name);
    execute format(
      'create policy adro_scope_isolation on %I using (tenant_id = current_setting(''app.tenant_id'', true) and workspace_id = current_setting(''app.workspace_id'', true)) with check (tenant_id = current_setting(''app.tenant_id'', true) and workspace_id = current_setting(''app.workspace_id'', true))',
      table_name
    );
  end loop;
end $$;

-- Backfill/rollback guidance (operator-run, deliberately not destructive):
--   begin;
--   update <table> set tenant_id = '<tenant>' where tenant_id = '';
--   commit;
-- To roll back this expand-only migration, first disable the policies in a
-- controlled maintenance window, then drop only the newly-added tables and
-- indexes.  Existing columns are retained for forward compatibility.
