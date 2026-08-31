-- Durable ADRO-owned 1-7 pipeline state. External task/session identifiers are
-- execution provenance only; the global session_id remains an ADRO identity.
create table if not exists pipeline_runs (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null,
  requirement_id uuid not null references requirements(id) on delete cascade,
  session_id uuid not null unique,
  parent_session_id text,
  pipeline_stage smallint not null default 1 check (pipeline_stage between 1 and 7),
  status text not null default 'running' check (status in ('running', 'waiting_provider', 'suspended', 'completed', 'failed')),
  designer_agent_id text not null,
  developer_agent_id text not null,
  tester_agent_id text not null,
  arbitrator_agent_id text not null,
  max_retries integer not null default 3 check (max_retries between 1 and 100),
  retry_count integer not null default 0 check (retry_count >= 0),
  unit_retry_count integer not null default 0 check (unit_retry_count >= 0),
  coverage_threshold numeric(5,2) not null default 80 check (coverage_threshold > 0 and coverage_threshold <= 100),
  active_provider_issue_id text,
  active_provider_task_id text,
  active_agent_id text,
  context jsonb not null default '{}',
  final_report text,
  suspend_reason text,
  version bigint not null default 1,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create unique index if not exists pipeline_runs_one_active_requirement
  on pipeline_runs(requirement_id)
  where status in ('running', 'waiting_provider', 'suspended');
create index if not exists pipeline_runs_workspace_stage
  on pipeline_runs(workspace_id, pipeline_stage, status);

create table if not exists pipeline_transitions (
  pipeline_id uuid not null references pipeline_runs(id) on delete cascade,
  sequence integer not null check (sequence > 0),
  from_stage smallint not null check (from_stage between 1 and 7),
  to_stage smallint not null check (to_stage between 1 and 7),
  outcome text not null check (outcome in ('pass', 'fail')),
  agent_id text not null,
  provider_issue_id text,
  provider_task_id text,
  provider_session_id text,
  summary text,
  created_at timestamptz not null default now(),
  primary key (pipeline_id, sequence)
);

-- Work Items can be queried directly by session/stage during recovery without
-- joining provider-native tables. parent_session_id records the originally
-- pinned development session used by every incremental repair.
alter table work_items add column if not exists session_id uuid;
alter table work_items add column if not exists pipeline_stage smallint check (pipeline_stage between 1 and 7);
alter table work_items add column if not exists parent_session_id text;
alter table work_items add column if not exists pipeline_id uuid references pipeline_runs(id) on delete set null;
create index if not exists work_items_pipeline_session on work_items(session_id, pipeline_stage);
