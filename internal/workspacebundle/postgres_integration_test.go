package workspacebundle

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adro-project/adro/internal/domain"
	_ "github.com/lib/pq"
)

func TestPostgresWorkspaceMigrationConformance(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("ADRO_POSTGRES_MIGRATION_TEST_DSN"))
	if dsn == "" {
		t.Skip("ADRO_POSTGRES_MIGRATION_TEST_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	database, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	schema := "adro_migration_" + domain.NewID()[:16]
	if _, err := database.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = database.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`) })
	if _, err := database.ExecContext(ctx, `SET search_path TO `+schema); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, postgresMigrationFixture); err != nil {
		t.Fatal(err)
	}

	sourceDSN, err := postgresDSNWithSchema(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	archive, manifest, err := ExportPostgresWorkspace(ctx, PostgresExportOptions{DSN: sourceDSN, Workspace: "portable-workspace"})
	if err != nil {
		t.Fatal(err)
	}
	counts := manifestCounts(manifest)
	if counts.Agents != 1 || counts.Squads != 1 || counts.Requirements != 2 || counts.Comments != 1 || counts.Repositories != 1 || counts.Projects != 1 || counts.Skills != 1 || counts.Automations != 1 || counts.ChatSessions != 1 || counts.ChatMessages != 1 {
		t.Fatalf("unexpected migration counts: %+v", counts)
	}
	agent := manifest.Definitions.Agents[0]
	if agent.ExecutorBinding.RuntimeID != "codex" || agent.ExecutorBinding.Model != "gpt-5" || agent.ExecutorBinding.ThinkingLevel != "high" {
		t.Fatalf("unexpected imported runtime: %+v", agent.ExecutorBinding)
	}
	if _, _, report, err := Preflight(archive, "target", "rename"); err != nil || !report.Valid {
		t.Fatalf("preflight report=%+v error=%v", report, err)
	}
}

func postgresDSNWithSchema(dsn, schema string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err == nil && parsed.Scheme != "" {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String(), nil
	}
	return strings.TrimSpace(dsn) + " search_path=" + schema, nil
}

const postgresMigrationFixture = `
CREATE TABLE workspace (id uuid PRIMARY KEY, name text NOT NULL, slug text NOT NULL, created_at timestamptz, updated_at timestamptz);
CREATE TABLE agent_runtime (id uuid PRIMARY KEY, workspace_id uuid NOT NULL, provider text NOT NULL, created_at timestamptz);
CREATE TABLE agent (
  id uuid PRIMARY KEY, workspace_id uuid NOT NULL, name text NOT NULL, description text, instructions text,
  runtime_id uuid, runtime_config jsonb, model text, thinking_level text, service_tier text, custom_args jsonb,
  custom_env jsonb, max_concurrent_tasks integer, owner_id uuid, archived_at timestamptz, kind text,
  created_at timestamptz, updated_at timestamptz
);
CREATE TABLE project (
  id uuid PRIMARY KEY, workspace_id uuid NOT NULL, title text NOT NULL, description text, status text,
  priority text, start_date date, due_date date, revision bigint, created_at timestamptz, updated_at timestamptz
);
CREATE TABLE project_resource (
  id uuid PRIMARY KEY, project_id uuid NOT NULL, workspace_id uuid NOT NULL, resource_type text NOT NULL,
  resource_ref jsonb NOT NULL, label text, position integer, created_at timestamptz
);
CREATE TABLE issue (
  id uuid PRIMARY KEY, workspace_id uuid NOT NULL, number integer, title text NOT NULL, description text,
  status text, priority text, assignee_type text, assignee_id uuid, creator_id uuid, project_id uuid,
  acceptance_criteria jsonb, metadata jsonb, properties jsonb, parent_issue_id uuid, stage integer,
  position double precision, start_date date, due_date date, revision bigint, created_at timestamptz, updated_at timestamptz
);
CREATE TABLE issue_label (
  id uuid PRIMARY KEY, workspace_id uuid NOT NULL, resource_type text NOT NULL, name text NOT NULL,
  color text NOT NULL, description text, created_at timestamptz, updated_at timestamptz
);
CREATE TABLE issue_to_label (issue_id uuid NOT NULL, label_id uuid NOT NULL);
CREATE TABLE issue_dependency (id uuid PRIMARY KEY, issue_id uuid NOT NULL, depends_on_issue_id uuid NOT NULL, type text NOT NULL);
CREATE TABLE issue_property (
  id uuid PRIMARY KEY, workspace_id uuid NOT NULL, name text NOT NULL, type text NOT NULL, description text,
  config jsonb, icon text, position double precision, created_at timestamptz, updated_at timestamptz
);
CREATE TABLE comment (
  id uuid PRIMARY KEY, issue_id uuid NOT NULL, author_type text, author_id uuid, content text,
  parent_id uuid, revision bigint, created_at timestamptz, updated_at timestamptz
);
CREATE TABLE attachment (id uuid PRIMARY KEY, workspace_id uuid NOT NULL, created_at timestamptz);
CREATE TABLE skill (
  id uuid PRIMARY KEY, workspace_id uuid NOT NULL, name text NOT NULL, description text, content text,
  config jsonb, created_at timestamptz, updated_at timestamptz
);
CREATE TABLE skill_file (id uuid PRIMARY KEY, skill_id uuid NOT NULL, path text, content text, created_at timestamptz, updated_at timestamptz);
CREATE TABLE agent_skill (agent_id uuid NOT NULL, skill_id uuid NOT NULL, enabled boolean, created_at timestamptz);
CREATE TABLE squad (id uuid PRIMARY KEY, workspace_id uuid NOT NULL, name text NOT NULL, description text, leader_id uuid, revision bigint, created_at timestamptz, updated_at timestamptz);
CREATE TABLE squad_member (id uuid PRIMARY KEY, squad_id uuid NOT NULL, member_type text, member_id uuid, role text, created_at timestamptz);
CREATE TABLE autopilot (
  id uuid PRIMARY KEY, workspace_id uuid NOT NULL, title text NOT NULL, description text, assignee_type text,
  assignee_id uuid, execution_mode text, issue_title_template text, project_id uuid, status text,
  revision bigint, created_at timestamptz, updated_at timestamptz
);
CREATE TABLE autopilot_trigger (
  id uuid PRIMARY KEY, autopilot_id uuid NOT NULL, kind text, enabled boolean, cron_expression text,
  timezone text, next_run_at timestamptz, label text, provider text, event_filters jsonb, created_at timestamptz
);
CREATE TABLE chat_session (
  id uuid PRIMARY KEY, workspace_id uuid NOT NULL, agent_id uuid, project_id uuid, creator_id uuid,
  title text, status text, created_at timestamptz, updated_at timestamptz
);
CREATE TABLE chat_message (id uuid PRIMARY KEY, chat_session_id uuid NOT NULL, role text, content text, created_at timestamptz);

INSERT INTO workspace VALUES ('00000000-0000-4000-8000-000000000001', 'Portable', 'portable-workspace', now(), now());
INSERT INTO agent_runtime VALUES ('00000000-0000-4000-8000-000000000002', '00000000-0000-4000-8000-000000000001', 'codex', now());
INSERT INTO agent VALUES (
  '00000000-0000-4000-8000-000000000003', '00000000-0000-4000-8000-000000000001', 'Builder', 'Developer', 'Build and verify',
  '00000000-0000-4000-8000-000000000002', '{}', 'gpt-5', 'high', null, '["--ephemeral"]', '{}', 2,
  '00000000-0000-4000-8000-000000000004', null, 'user', now(), now()
);
INSERT INTO project VALUES ('00000000-0000-4000-8000-000000000005', '00000000-0000-4000-8000-000000000001', 'Release', 'Release project', 'in_progress', 'high', current_date, current_date + 7, 1, now(), now());
INSERT INTO project_resource VALUES ('00000000-0000-4000-8000-000000000006', '00000000-0000-4000-8000-000000000005', '00000000-0000-4000-8000-000000000001', 'github_repo', '{"url":"https://github.com/example/service.git","ref":"main"}', 'service', 0, now());
INSERT INTO issue VALUES (
  '00000000-0000-4000-8000-000000000007', '00000000-0000-4000-8000-000000000001', 7, 'Portable issue', 'Keep history',
  'in_progress', 'high', 'agent', '00000000-0000-4000-8000-000000000003', '00000000-0000-4000-8000-000000000004',
  '00000000-0000-4000-8000-000000000005', '["imports"]', '{"source":"fixture"}', '{"00000000-0000-4000-8000-000000000019":"production"}', null, 1, 0, current_date,
  current_date + 7, 1, now(), now()
);
INSERT INTO issue VALUES (
  '00000000-0000-4000-8000-000000000017', '00000000-0000-4000-8000-000000000001', 6, 'Dependency', 'Required first',
  'done', 'medium', null, null, '00000000-0000-4000-8000-000000000004', null,
  '[]', '{}', '{}', null, null, 1, null, null, 1, now(), now()
);
INSERT INTO issue_label VALUES ('00000000-0000-4000-8000-000000000018', '00000000-0000-4000-8000-000000000001', 'issue', 'release', '#e11d48', 'Release work', now(), now());
INSERT INTO issue_to_label VALUES ('00000000-0000-4000-8000-000000000007', '00000000-0000-4000-8000-000000000018');
INSERT INTO issue_dependency VALUES ('00000000-0000-4000-8000-000000000020', '00000000-0000-4000-8000-000000000007', '00000000-0000-4000-8000-000000000017', 'blocked_by');
INSERT INTO issue_property VALUES ('00000000-0000-4000-8000-000000000019', '00000000-0000-4000-8000-000000000001', 'Environment', 'select', 'Deployment target', '{"options":[{"id":"production","name":"Production"}]}', 'server', 0, now(), now());
INSERT INTO comment VALUES ('00000000-0000-4000-8000-000000000008', '00000000-0000-4000-8000-000000000007', 'member', '00000000-0000-4000-8000-000000000004', 'Keep this discussion', null, 1, now(), now());
INSERT INTO skill VALUES ('00000000-0000-4000-8000-000000000009', '00000000-0000-4000-8000-000000000001', 'Review', 'Review changes', 'Run checks', '{}', now(), now());
INSERT INTO skill_file VALUES ('00000000-0000-4000-8000-000000000010', '00000000-0000-4000-8000-000000000009', 'SKILL.md', 'Review carefully', now(), now());
INSERT INTO agent_skill VALUES ('00000000-0000-4000-8000-000000000003', '00000000-0000-4000-8000-000000000009', true, now());
INSERT INTO squad VALUES ('00000000-0000-4000-8000-000000000011', '00000000-0000-4000-8000-000000000001', 'Delivery', 'Delivery team', '00000000-0000-4000-8000-000000000003', 1, now(), now());
INSERT INTO squad_member VALUES ('00000000-0000-4000-8000-000000000012', '00000000-0000-4000-8000-000000000011', 'agent', '00000000-0000-4000-8000-000000000003', 'leader', now());
INSERT INTO autopilot VALUES ('00000000-0000-4000-8000-000000000013', '00000000-0000-4000-8000-000000000001', 'Nightly', 'Nightly checks', 'agent', '00000000-0000-4000-8000-000000000003', 'create_issue', 'Nightly check', '00000000-0000-4000-8000-000000000005', 'active', 1, now(), now());
INSERT INTO autopilot_trigger VALUES ('00000000-0000-4000-8000-000000000014', '00000000-0000-4000-8000-000000000013', 'schedule', true, '0 1 * * *', 'UTC', now(), 'nightly', null, '{}', now());
INSERT INTO chat_session VALUES ('00000000-0000-4000-8000-000000000015', '00000000-0000-4000-8000-000000000001', '00000000-0000-4000-8000-000000000003', '00000000-0000-4000-8000-000000000005', '00000000-0000-4000-8000-000000000004', 'Release chat', 'active', now(), now());
INSERT INTO chat_message VALUES ('00000000-0000-4000-8000-000000000016', '00000000-0000-4000-8000-000000000015', 'user', 'Ship it', now());
`
