package workspacebundle

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/artifact"
	"github.com/adro-project/adro/internal/domain"
	"github.com/adro-project/adro/internal/orchestration"
	"github.com/adro-project/adro/internal/store"
	"github.com/lib/pq"
)

// PostgresExportOptions identifies one compatible source workspace. DSN is
// used only to open a read-only, repeatable-read transaction and is never
// written to the bundle or returned in an error.
type PostgresExportOptions struct {
	DSN        string
	Workspace  string
	UploadRoot string
	Now        func() time.Time
}

type sourceRow map[string]any

type postgresDataset struct {
	Workspace              sourceRow
	Agents                 []sourceRow
	AgentRuntimes          []sourceRow
	AgentInvocationTargets []sourceRow
	Issues                 []sourceRow
	IssueLabels            []sourceRow
	IssueDependencies      []sourceRow
	PropertyDefinitions    []sourceRow
	Comments               []sourceRow
	Attachments            []sourceRow
	Projects               []sourceRow
	ProjectResources       []sourceRow
	Skills                 []sourceRow
	SkillFiles             []sourceRow
	AgentSkills            []sourceRow
	WorkspaceMCPServers    []sourceRow
	AgentMCPServers        []sourceRow
	Squads                 []sourceRow
	SquadMembers           []sourceRow
	Automations            []sourceRow
	Triggers               []sourceRow
	ChatSessions           []sourceRow
	ChatMessages           []sourceRow
}

type postgresCollection struct {
	name     string
	query    string
	optional bool
	assign   func(*postgresDataset, []sourceRow)
}

var postgresCollections = []postgresCollection{
	{
		name:   "agents",
		query:  `SELECT to_jsonb(row_value)::text FROM agent AS row_value WHERE row_value.workspace_id = $1::uuid ORDER BY row_value.created_at, row_value.id`,
		assign: func(data *postgresDataset, rows []sourceRow) { data.Agents = rows },
	},
	{
		name:     "agent runtimes",
		query:    `SELECT to_jsonb(row_value)::text FROM agent_runtime AS row_value WHERE row_value.workspace_id = $1::uuid ORDER BY row_value.created_at, row_value.id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.AgentRuntimes = rows },
	},
	{
		name:     "agent invocation targets",
		query:    `SELECT to_jsonb(row_value)::text FROM agent_invocation_target AS row_value JOIN agent AS owner ON owner.id = row_value.agent_id WHERE owner.workspace_id = $1::uuid ORDER BY row_value.agent_id, row_value.created_at, row_value.id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.AgentInvocationTargets = rows },
	},
	{
		name:   "issues",
		query:  `SELECT to_jsonb(row_value)::text FROM issue AS row_value WHERE row_value.workspace_id = $1::uuid ORDER BY row_value.created_at, row_value.id`,
		assign: func(data *postgresDataset, rows []sourceRow) { data.Issues = rows },
	},
	{
		name:     "issue labels",
		query:    `SELECT (to_jsonb(label) || jsonb_build_object('issue_id', relation.issue_id))::text FROM issue_to_label AS relation JOIN issue AS owner ON owner.id = relation.issue_id JOIN issue_label AS label ON label.id = relation.label_id WHERE owner.workspace_id = $1::uuid ORDER BY relation.issue_id, label.name, label.id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.IssueLabels = rows },
	},
	{
		name:     "issue dependencies",
		query:    `SELECT to_jsonb(row_value)::text FROM issue_dependency AS row_value JOIN issue AS owner ON owner.id = row_value.issue_id WHERE owner.workspace_id = $1::uuid ORDER BY row_value.issue_id, row_value.id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.IssueDependencies = rows },
	},
	{
		name:     "issue property definitions",
		query:    `SELECT to_jsonb(row_value)::text FROM issue_property AS row_value WHERE row_value.workspace_id = $1::uuid ORDER BY row_value.position, row_value.created_at, row_value.id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.PropertyDefinitions = rows },
	},
	{
		name:   "comments",
		query:  `SELECT to_jsonb(row_value)::text FROM comment AS row_value JOIN issue AS owner ON owner.id = row_value.issue_id WHERE owner.workspace_id = $1::uuid ORDER BY row_value.created_at, row_value.id`,
		assign: func(data *postgresDataset, rows []sourceRow) { data.Comments = rows },
	},
	{
		name:     "attachments",
		query:    `SELECT to_jsonb(row_value)::text FROM attachment AS row_value WHERE row_value.workspace_id = $1::uuid ORDER BY row_value.created_at, row_value.id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.Attachments = rows },
	},
	{
		name:     "projects",
		query:    `SELECT to_jsonb(row_value)::text FROM project AS row_value WHERE row_value.workspace_id = $1::uuid ORDER BY row_value.created_at, row_value.id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.Projects = rows },
	},
	{
		name:     "project resources",
		query:    `SELECT to_jsonb(row_value)::text FROM project_resource AS row_value WHERE row_value.workspace_id = $1::uuid ORDER BY row_value.position, row_value.created_at, row_value.id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.ProjectResources = rows },
	},
	{
		name:     "skills",
		query:    `SELECT to_jsonb(row_value)::text FROM skill AS row_value WHERE row_value.workspace_id = $1::uuid ORDER BY row_value.created_at, row_value.id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.Skills = rows },
	},
	{
		name:     "skill files",
		query:    `SELECT to_jsonb(row_value)::text FROM skill_file AS row_value JOIN skill AS owner ON owner.id = row_value.skill_id WHERE owner.workspace_id = $1::uuid ORDER BY row_value.created_at, row_value.id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.SkillFiles = rows },
	},
	{
		name:     "agent skill bindings",
		query:    `SELECT to_jsonb(row_value)::text FROM agent_skill AS row_value JOIN agent AS owner ON owner.id = row_value.agent_id WHERE owner.workspace_id = $1::uuid ORDER BY row_value.created_at, row_value.agent_id, row_value.skill_id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.AgentSkills = rows },
	},
	{
		name:     "workspace MCP servers",
		query:    `SELECT to_jsonb(row_value)::text FROM workspace_mcp_server AS row_value WHERE row_value.workspace_id = $1::uuid ORDER BY row_value.created_at, row_value.id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.WorkspaceMCPServers = rows },
	},
	{
		name:     "agent MCP server bindings",
		query:    `SELECT to_jsonb(row_value)::text FROM agent_mcp_server AS row_value JOIN agent AS owner ON owner.id = row_value.agent_id WHERE owner.workspace_id = $1::uuid ORDER BY row_value.created_at, row_value.agent_id, row_value.server_id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.AgentMCPServers = rows },
	},
	{
		name:     "squads",
		query:    `SELECT to_jsonb(row_value)::text FROM squad AS row_value WHERE row_value.workspace_id = $1::uuid ORDER BY row_value.created_at, row_value.id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.Squads = rows },
	},
	{
		name:     "squad members",
		query:    `SELECT to_jsonb(row_value)::text FROM squad_member AS row_value JOIN squad AS owner ON owner.id = row_value.squad_id WHERE owner.workspace_id = $1::uuid ORDER BY row_value.created_at, row_value.id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.SquadMembers = rows },
	},
	{
		name:     "automations",
		query:    `SELECT to_jsonb(row_value)::text FROM autopilot AS row_value WHERE row_value.workspace_id = $1::uuid ORDER BY row_value.created_at, row_value.id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.Automations = rows },
	},
	{
		name:     "automation triggers",
		query:    `SELECT to_jsonb(row_value)::text FROM autopilot_trigger AS row_value JOIN autopilot AS owner ON owner.id = row_value.autopilot_id WHERE owner.workspace_id = $1::uuid ORDER BY row_value.created_at, row_value.id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.Triggers = rows },
	},
	{
		name:     "chat sessions",
		query:    `SELECT to_jsonb(row_value)::text FROM chat_session AS row_value WHERE row_value.workspace_id = $1::uuid ORDER BY row_value.created_at, row_value.id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.ChatSessions = rows },
	},
	{
		name:     "chat messages",
		query:    `SELECT to_jsonb(row_value)::text FROM chat_message AS row_value JOIN chat_session AS owner ON owner.id = row_value.chat_session_id WHERE owner.workspace_id = $1::uuid ORDER BY row_value.created_at, row_value.id`,
		optional: true,
		assign:   func(data *postgresDataset, rows []sourceRow) { data.ChatMessages = rows },
	},
}

// ExportPostgresWorkspace converts durable user-authored records from a
// compatible PostgreSQL workspace into the same portable archive used by the
// regular export, preflight, and import paths.
func ExportPostgresWorkspace(ctx context.Context, options PostgresExportOptions) ([]byte, Manifest, error) {
	if strings.TrimSpace(options.DSN) == "" {
		return nil, Manifest{}, errors.New("source DSN is required")
	}
	if strings.TrimSpace(options.Workspace) == "" {
		return nil, Manifest{}, errors.New("source workspace ID or slug is required")
	}
	database, err := sql.Open("postgres", options.DSN)
	if err != nil {
		return nil, Manifest{}, errors.New("open compatible PostgreSQL source")
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	if err := database.PingContext(ctx); err != nil {
		return nil, Manifest{}, fmt.Errorf("connect compatible PostgreSQL source: %w", redactConnectionError(err))
	}

	dataset, err := loadPostgresDataset(ctx, database, options.Workspace)
	if err != nil {
		return nil, Manifest{}, err
	}
	now := time.Now().UTC()
	if options.Now != nil {
		now = options.Now().UTC()
	}
	return buildPostgresArchive(ctx, dataset, options.UploadRoot, now)
}

func loadPostgresDataset(ctx context.Context, database *sql.DB, workspace string) (postgresDataset, error) {
	transaction, err := database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return postgresDataset{}, fmt.Errorf("start source snapshot: %w", err)
	}
	defer transaction.Rollback()

	var encoded []byte
	err = transaction.QueryRowContext(ctx,
		`SELECT to_jsonb(row_value)::text FROM workspace AS row_value WHERE row_value.id::text = $1 OR row_value.slug = $1 ORDER BY (row_value.id::text = $1) DESC LIMIT 1`,
		workspace,
	).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return postgresDataset{}, fmt.Errorf("source workspace %q was not found", workspace)
	}
	if err != nil {
		return postgresDataset{}, fmt.Errorf("read source workspace: %w", err)
	}
	workspaceRow, err := decodeSourceRow(encoded)
	if err != nil {
		return postgresDataset{}, fmt.Errorf("decode source workspace: %w", err)
	}
	workspaceID := rowString(workspaceRow, "id")
	if workspaceID == "" {
		return postgresDataset{}, errors.New("source workspace has no ID")
	}

	dataset := postgresDataset{Workspace: workspaceRow}
	for index, collection := range postgresCollections {
		savepoint := ""
		if collection.optional {
			savepoint = "source_optional_" + strconv.Itoa(index)
			if _, err := transaction.ExecContext(ctx, "SAVEPOINT "+savepoint); err != nil {
				return postgresDataset{}, fmt.Errorf("prepare optional source read: %w", err)
			}
		}
		rows, queryErr := querySourceRows(ctx, transaction, collection.query, workspaceID)
		if queryErr != nil {
			if collection.optional && missingSchemaObject(queryErr) {
				if _, err := transaction.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+savepoint); err != nil {
					return postgresDataset{}, fmt.Errorf("recover optional source read: %w", err)
				}
				if _, err := transaction.ExecContext(ctx, "RELEASE SAVEPOINT "+savepoint); err != nil {
					return postgresDataset{}, fmt.Errorf("finish optional source read: %w", err)
				}
				continue
			}
			return postgresDataset{}, fmt.Errorf("read source %s: %w", collection.name, queryErr)
		}
		if savepoint != "" {
			if _, err := transaction.ExecContext(ctx, "RELEASE SAVEPOINT "+savepoint); err != nil {
				return postgresDataset{}, fmt.Errorf("finish optional source read: %w", err)
			}
		}
		collection.assign(&dataset, rows)
	}
	if err := transaction.Commit(); err != nil {
		return postgresDataset{}, fmt.Errorf("finish source snapshot: %w", err)
	}
	return dataset, nil
}

func querySourceRows(ctx context.Context, transaction *sql.Tx, query, workspaceID string) ([]sourceRow, error) {
	rows, err := transaction.QueryContext(ctx, query, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]sourceRow, 0)
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}
		row, err := decodeSourceRow(encoded)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func decodeSourceRow(encoded []byte) (sourceRow, error) {
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	var row sourceRow
	if err := decoder.Decode(&row); err != nil {
		return nil, err
	}
	return row, nil
}

func missingSchemaObject(err error) bool {
	var databaseError *pq.Error
	if !errors.As(err, &databaseError) {
		return false
	}
	code := string(databaseError.Code)
	return code == "42P01" || code == "42703"
}

func redactConnectionError(err error) error {
	var databaseError *pq.Error
	if errors.As(err, &databaseError) {
		return fmt.Errorf("%s (%s)", databaseError.Message, databaseError.Code)
	}
	return errors.New("connection failed")
}

func buildPostgresArchive(ctx context.Context, data postgresDataset, uploadRoot string, now time.Time) ([]byte, Manifest, error) {
	sourceWorkspace := rowString(data.Workspace, "id")
	if sourceWorkspace == "" {
		return nil, Manifest{}, errors.New("source workspace has no ID")
	}

	control := store.WorkspaceSnapshot{
		Format:           store.WorkspaceSnapshotFormat,
		SourceWorkspace:  sourceWorkspace,
		CommentRevisions: map[string][]domain.CommentRevision{},
	}
	definitions := orchestration.DefinitionBundle{
		Format:            orchestration.DefinitionBundleFormat,
		SourceWorkspaceID: sourceWorkspace,
	}

	runtimes := indexRows(data.AgentRuntimes, "id")
	invocationTargets := groupRows(data.AgentInvocationTargets, "agent_id")
	agentIDs := make(map[string]bool)
	for _, row := range data.Agents {
		agent := mapPostgresAgent(row, runtimes, invocationTargets[rowString(row, "id")], sourceWorkspace)
		if agent.ID == "" || agent.Name == "" || rowString(row, "kind") == "system" {
			continue
		}
		definitions.Agents = append(definitions.Agents, agent)
		agentIDs[agent.ID] = true
	}
	if len(definitions.Agents) == 0 {
		return nil, Manifest{}, errors.New("source workspace has no portable Agent definition")
	}

	projectRepositories := make(map[string][]string)
	for _, row := range data.ProjectResources {
		if rowString(row, "resource_type") != "github_repo" {
			continue
		}
		reference := rowMap(row, "resource_ref")
		cloneURL := mapString(reference, "url")
		if cloneURL == "" {
			continue
		}
		repositoryID := rowString(row, "id")
		projectID := rowString(row, "project_id")
		if repositoryID == "" || projectID == "" {
			continue
		}
		control.Repositories = append(control.Repositories, domain.Repository{
			ID:            repositoryID,
			WorkspaceID:   sourceWorkspace,
			CanonicalName: repositoryName(rowString(row, "label"), cloneURL),
			CloneURL:      cloneURL,
			Provider:      repositoryProvider(cloneURL),
			DefaultBranch: defaultString(mapString(reference, "ref"), "main"),
			Metadata:      sanitizeSourceMap(reference),
			IndexStatus:   "pending",
			CreatedAt:     rowTime(row, "created_at"),
			UpdatedAt:     rowTime(row, "created_at"),
		})
		projectRepositories[projectID] = append(projectRepositories[projectID], repositoryID)
	}

	projects := make(map[string]bool)
	for _, row := range data.Projects {
		projectID := rowString(row, "id")
		if projectID == "" {
			continue
		}
		projects[projectID] = true
		control.TeamWorkspaces = append(control.TeamWorkspaces, domain.TeamWorkspace{
			ID:            projectID,
			WorkspaceID:   sourceWorkspace,
			Name:          defaultString(rowString(row, "title"), "Imported project"),
			Version:       maxInt64(rowInt64(row, "revision"), 1),
			RepositoryIDs: append([]string(nil), projectRepositories[projectID]...),
			Policy: sanitizeSourceMap(map[string]any{
				"description": rowString(row, "description"),
				"priority":    rowString(row, "priority"),
				"start_date":  row["start_date"],
				"due_date":    row["due_date"],
			}),
			Status:    mapProjectStatus(rowString(row, "status")),
			CreatedAt: rowTime(row, "created_at"),
			UpdatedAt: rowTime(row, "updated_at"),
		})
	}

	issueLabels := groupRows(data.IssueLabels, "issue_id")
	issueDependencies := groupRows(data.IssueDependencies, "issue_id")
	propertyDefinitions := indexRows(data.PropertyDefinitions, "id")
	for _, row := range data.Issues {
		issueID := rowString(row, "id")
		requirement := mapPostgresIssue(row, sourceWorkspace, projectRepositories, issueLabels[issueID], issueDependencies[issueID], propertyDefinitions)
		if requirement.ID != "" && requirement.Title != "" {
			control.Requirements = append(control.Requirements, requirement)
		}
	}
	requirementIDs := make(map[string]bool, len(control.Requirements))
	for _, requirement := range control.Requirements {
		requirementIDs[requirement.ID] = true
	}

	commentsByID := indexRows(data.Comments, "id")
	for _, row := range data.Comments {
		comment := mapPostgresComment(row, sourceWorkspace, commentsByID)
		if comment.ID == "" || !requirementIDs[comment.TargetID] {
			continue
		}
		control.Comments = append(control.Comments, comment)
		control.CommentRevisions[comment.ID] = []domain.CommentRevision{{
			CommentID:      comment.ID,
			Revision:       comment.Revision,
			Content:        comment.Content,
			EditorID:       comment.AuthorID,
			EditorType:     comment.AuthorType,
			OriginatorID:   comment.OriginatorID,
			OriginatorType: comment.OriginatorType,
			CreatedAt:      comment.UpdatedAt,
		}}
	}
	commentIDs := make(map[string]bool, len(control.Comments))
	for _, comment := range control.Comments {
		commentIDs[comment.ID] = true
	}

	skillFiles := groupRows(data.SkillFiles, "skill_id")
	for _, row := range data.Skills {
		skill := mapPostgresSkill(row, skillFiles[rowString(row, "id")], sourceWorkspace)
		if skill.ID != "" && skill.Name != "" {
			control.Skills = append(control.Skills, skill)
		}
	}
	skillIDs := make(map[string]bool, len(control.Skills))
	for _, skill := range control.Skills {
		skillIDs[skill.ID] = true
	}
	agentSkillIDs := make(map[string][]string)
	for _, row := range data.AgentSkills {
		agentID, skillID := rowString(row, "agent_id"), rowString(row, "skill_id")
		if !agentIDs[agentID] || !skillIDs[skillID] {
			continue
		}
		control.Bindings = append(control.Bindings, domain.CapabilityBinding{
			ID:           "agent-skill-" + agentID + "-" + skillID,
			WorkspaceID:  sourceWorkspace,
			AgentID:      agentID,
			CapabilityID: skillID,
			Kind:         "skill",
			Enabled:      rowBoolDefault(row, "enabled", true),
			CreatedAt:    rowTime(row, "created_at"),
		})
		if rowBoolDefault(row, "enabled", true) {
			agentSkillIDs[agentID] = append(agentSkillIDs[agentID], skillID)
		}
	}

	mcpIDs := make(map[string]bool)
	for _, row := range data.WorkspaceMCPServers {
		server := mapPostgresMCPServer(row, sourceWorkspace)
		if server.ID == "" || server.Name == "" {
			continue
		}
		control.MCPServers = append(control.MCPServers, server)
		mcpIDs[server.ID] = true
	}
	agentMCPIDs := make(map[string][]string)
	for _, row := range data.AgentMCPServers {
		agentID, serverID := rowString(row, "agent_id"), rowString(row, "server_id")
		if !agentIDs[agentID] || !mcpIDs[serverID] {
			continue
		}
		enabled := rowBoolDefault(row, "enabled", true)
		control.Bindings = append(control.Bindings, domain.CapabilityBinding{
			ID:           "agent-mcp-" + agentID + "-" + serverID,
			WorkspaceID:  sourceWorkspace,
			AgentID:      agentID,
			CapabilityID: serverID,
			Kind:         "mcp",
			Enabled:      enabled,
			CreatedAt:    rowTime(row, "created_at"),
		})
		if enabled {
			agentMCPIDs[agentID] = append(agentMCPIDs[agentID], serverID)
		}
	}
	for index := range definitions.Agents {
		agentID := definitions.Agents[index].ID
		definitions.Agents[index].SkillIDs = append([]string(nil), agentSkillIDs[agentID]...)
		definitions.Agents[index].MCPServerIDs = append([]string(nil), agentMCPIDs[agentID]...)
		sort.Strings(definitions.Agents[index].SkillIDs)
		sort.Strings(definitions.Agents[index].MCPServerIDs)
	}

	squadMembers := groupRows(data.SquadMembers, "squad_id")
	for _, row := range data.Squads {
		squad, ok := mapPostgresSquad(row, squadMembers[rowString(row, "id")], sourceWorkspace, agentIDs)
		if ok {
			definitions.Squads = append(definitions.Squads, squad)
		}
	}

	triggers := groupRows(data.Triggers, "autopilot_id")
	for _, row := range data.Automations {
		automation := mapPostgresAutomation(row, triggers[rowString(row, "id")], sourceWorkspace)
		if automation.ID != "" && automation.Name != "" {
			control.Automations = append(control.Automations, automation)
		}
	}

	chatSessionIDs := make(map[string]bool)
	for _, row := range data.ChatSessions {
		agentID := rowString(row, "agent_id")
		if agentID != "" && !agentIDs[agentID] {
			continue
		}
		projectID := rowString(row, "project_id")
		if !projects[projectID] {
			projectID = ""
		}
		sessionID := rowString(row, "id")
		if sessionID == "" {
			continue
		}
		chatSessionIDs[sessionID] = true
		control.ChatSessions = append(control.ChatSessions, domain.ChatSession{
			ID:               sessionID,
			WorkspaceID:      sourceWorkspace,
			AgentID:          agentID,
			ProjectID:        projectID,
			Title:            defaultString(rowString(row, "title"), "Imported conversation"),
			HarnessSessionID: "portable-chat-" + sessionID,
			Status:           defaultString(rowString(row, "status"), "active"),
			CreatedBy:        rowString(row, "creator_id"),
			CreatedAt:        rowTime(row, "created_at"),
			UpdatedAt:        rowTime(row, "updated_at"),
		})
	}
	chatMessageIDs := make(map[string]bool)
	for _, row := range data.ChatMessages {
		sessionID := rowString(row, "chat_session_id")
		if !chatSessionIDs[sessionID] {
			continue
		}
		messageID := rowString(row, "id")
		if messageID == "" {
			continue
		}
		chatMessageIDs[messageID] = true
		control.ChatMessages = append(control.ChatMessages, domain.ChatMessage{
			ID:            messageID,
			ChatSessionID: sessionID,
			WorkspaceID:   sourceWorkspace,
			Role:          mapChatRole(rowString(row, "role")),
			Content:       rowString(row, "content"),
			CreatedAt:     rowTime(row, "created_at"),
		})
	}

	artifacts, payloads, err := mapPostgresAttachments(ctx, data.Attachments, uploadRoot, sourceWorkspace, requirementIDs, commentIDs, chatMessageIDs)
	if err != nil {
		return nil, Manifest{}, err
	}
	control.Attachments = artifacts.attachments
	attachEntityReferences(&control, artifacts.byOwner)

	sortPostgresSnapshot(&control)
	manifest := Manifest{
		Format:            Format,
		Version:           1,
		CreatedAt:         now,
		SourceWorkspaceID: sourceWorkspace,
		Control:           control,
		Definitions:       definitions,
		Artifacts:         artifacts.entries,
		Excluded: []string{
			"authentication credentials and custom environment variables",
			"executor sessions, task tokens, and local work directories",
			"leases, queued work, execution history, and automation runs",
			"provider and runner observations",
			"webhook secrets",
			"local-directory project resources",
		},
	}
	return finalizeArchive(manifest, payloads)
}

func mapPostgresAgent(row sourceRow, runtimes map[string]sourceRow, invocationTargets []sourceRow, workspaceID string) orchestration.AgentDefinition {
	runtime := runtimes[rowString(row, "runtime_id")]
	runtimeID := normalizeRuntimeID(rowString(runtime, "provider"))
	if runtimeID == "" {
		runtimeID = normalizeRuntimeID(mapString(rowMap(row, "runtime_config"), "provider"))
	}
	status := orchestration.AgentActive
	if rowString(row, "archived_at") != "" {
		status = orchestration.AgentArchived
	}
	runtimeConfig := mapPostgresRuntimeConfig(rowMap(row, "runtime_config"), runtimeID)
	return orchestration.AgentDefinition{
		ID:                    rowString(row, "id"),
		WorkspaceID:           workspaceID,
		Revision:              maxInt64(rowInt64(row, "revision"), 1),
		Name:                  rowString(row, "name"),
		OwnerID:               rowString(row, "owner_id"),
		Description:           rowString(row, "description"),
		AvatarURL:             rowString(row, "avatar_url"),
		Instructions:          rowString(row, "instructions"),
		ConversationStarters:  mapPostgresConversationStarters(row),
		AccessPolicy:          mapPostgresAgentAccess(row, invocationTargets, workspaceID),
		DisabledRuntimeSkills: mapPostgresDisabledRuntimeSkills(row, runtimeID),
		ExecutorBinding: orchestration.ExecutorBinding{
			ProviderID:    "local",
			RuntimeID:     runtimeID,
			Model:         rowString(row, "model"),
			ThinkingLevel: normalizeThinkingLevel(rowString(row, "thinking_level")),
			ServiceTier:   normalizeServiceTier(rowString(row, "service_tier")),
			CustomArgs:    portableRuntimeArgs(runtimeID, rowStringSlice(row, "custom_args")),
			RuntimeConfig: runtimeConfig,
			ConfigVersion: "postgres-import-v1",
		},
		ConcurrencyBudget: orchestration.Budget{Concurrent: int(maxInt64(rowInt64(row, "max_concurrent_tasks"), 1))},
		InputSchema:       orchestration.SchemaRef{ID: "portable.agent.input", Version: 1},
		OutputSchema:      orchestration.SchemaRef{ID: "portable.agent.output", Version: 1},
		Status:            status,
		CreatedBy:         rowString(row, "owner_id"),
		CreatedAt:         rowTime(row, "created_at"),
		UpdatedAt:         rowTime(row, "updated_at"),
	}
}

func mapPostgresRuntimeConfig(raw map[string]any, runtimeID string) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	result := map[string]string{}
	if runtimeID == "openclaw" {
		mode := strings.ToLower(mapString(raw, "mode"))
		if mode == "local" || mode == "gateway" {
			result["mode"] = mode
		}
		gateway := rowMap(sourceRow(raw), "gateway")
		if host := mapString(gateway, "host"); host != "" && !isAbsolutePortablePath(host) && !strings.ContainsAny(host, "\r\n\x00") {
			result["gateway.host"] = host
		}
		if port := int(rowInt64(sourceRow(gateway), "port")); port > 0 && port <= 65535 {
			result["gateway.port"] = strconv.Itoa(port)
		}
		if tls, exists := gateway["tls"]; exists {
			if value, ok := tls.(bool); ok {
				result["gateway.tls"] = strconv.FormatBool(value)
			}
		}
		if len(result) == 0 {
			return nil
		}
		return result
	}

	var walk func(prefix string, value map[string]any)
	walk = func(prefix string, value map[string]any) {
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if len(result) >= 32 || key == "provider" || key == "runtime_name" || containsSensitiveName(strings.ToLower(key)) {
				continue
			}
			fullKey := key
			if prefix != "" {
				fullKey = prefix + "." + key
			}
			if nested, ok := value[key].(map[string]any); ok {
				walk(fullKey, nested)
				continue
			}
			if !portableRuntimeConfigKey(fullKey) {
				continue
			}
			text := valueString(value[key])
			if text == "" || len(text) > 1024 || strings.ContainsAny(text, "\r\n\x00") || isAbsolutePortablePath(text) {
				continue
			}
			result[fullKey] = text
		}
	}
	walk("", raw)
	if len(result) == 0 {
		return nil
	}
	return result
}

func portableRuntimeConfigKey(value string) bool {
	if value == "" || !((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z') || value[0] == '_') {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' || character == '.' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func mapPostgresDisabledRuntimeSkills(row sourceRow, runtimeID string) []orchestration.DisabledRuntimeSkill {
	values, _ := row["disabled_runtime_skills"].([]any)
	result := make([]orchestration.DisabledRuntimeSkill, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		entry, ok := value.(map[string]any)
		if !ok {
			continue
		}
		providerID := normalizeRuntimeID(mapString(entry, "provider"))
		if providerID == "" {
			providerID = runtimeID
		}
		if runtimeID == "" || providerID != runtimeID {
			continue
		}
		root := mapString(entry, "root")
		key := strings.ReplaceAll(mapString(entry, "key"), `\\`, "/")
		cleaned := path.Clean(key)
		plugin := mapString(entry, "plugin")
		if (root != "provider" && root != "universal" && root != "plugin") || key == "" || strings.HasPrefix(key, "/") || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned != key || (root == "plugin") != (plugin != "") {
			continue
		}
		identity := strings.Join([]string{root, key, plugin}, "\x00")
		if seen[identity] {
			continue
		}
		seen[identity] = true
		result = append(result, orchestration.DisabledRuntimeSkill{RuntimeID: runtimeID, Provider: runtimeID, Root: root, Key: key, Name: mapString(entry, "name"), Plugin: plugin})
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.Join([]string{result[i].Root, result[i].Key, result[i].Plugin}, "\x00") < strings.Join([]string{result[j].Root, result[j].Key, result[j].Plugin}, "\x00")
	})
	return result
}

func mapPostgresConversationStarters(row sourceRow) []orchestration.ConversationStarter {
	values, _ := row["conversation_starters"].([]any)
	result := make([]orchestration.ConversationStarter, 0, len(values))
	for _, value := range values {
		entry, ok := value.(map[string]any)
		if !ok {
			continue
		}
		starter := orchestration.ConversationStarter{Label: mapString(entry, "label"), Prompt: mapString(entry, "prompt")}
		if starter.Label != "" && starter.Prompt != "" && len(result) < 3 {
			result = append(result, starter)
		}
	}
	return result
}

func mapPostgresAgentAccess(row sourceRow, targets []sourceRow, workspaceID string) orchestration.AgentAccessPolicy {
	mode := rowString(row, "permission_mode")
	if mode == "" {
		if rowString(row, "visibility") == "workspace" {
			return orchestration.AgentAccessPolicy{Mode: "workspace"}
		}
		return orchestration.AgentAccessPolicy{Mode: "private"}
	}
	if mode != "public_to" {
		return orchestration.AgentAccessPolicy{Mode: "private"}
	}
	members := make([]string, 0)
	for _, target := range targets {
		switch rowString(target, "target_type") {
		case "workspace":
			if rowString(target, "target_id") == workspaceID {
				return orchestration.AgentAccessPolicy{Mode: "workspace"}
			}
		case "member":
			if targetID := rowString(target, "target_id"); targetID != "" {
				members = append(members, targetID)
			}
		}
	}
	if len(members) == 0 {
		return orchestration.AgentAccessPolicy{Mode: "private"}
	}
	sort.Strings(members)
	return orchestration.AgentAccessPolicy{Mode: "members", MemberIDs: members}
}

func mapPostgresMCPServer(row sourceRow, workspaceID string) domain.MCPServer {
	configuration := rowMap(row, "config")
	transport := strings.ToLower(mapString(configuration, "type"))
	switch transport {
	case "local", "stdio":
		transport = "stdio"
	case "remote", "streamable-http", "http", "https":
		transport = "http"
	default:
		if mapString(configuration, "command") != "" {
			transport = "stdio"
		} else if mapString(configuration, "url") != "" {
			transport = "http"
		} else {
			transport = "unknown"
		}
	}
	return domain.MCPServer{
		ID:            rowString(row, "id"),
		WorkspaceID:   workspaceID,
		Name:          rowString(row, "name"),
		Endpoint:      "redacted://configure-after-import",
		Protocol:      transport,
		Status:        "disabled",
		Configuration: map[string]any{"migration_state": "credential_configuration_required"},
		CreatedAt:     rowTime(row, "created_at"),
		UpdatedAt:     rowTime(row, "updated_at"),
	}
}

func mapPostgresIssue(row sourceRow, workspaceID string, projectRepositories map[string][]string, labels, dependencies []sourceRow, propertyDefinitions map[string]sourceRow) domain.Requirement {
	assigneeType := rowString(row, "assignee_type")
	assigneeID := rowString(row, "assignee_id")
	memberIDs := []string(nil)
	if assigneeType == "member" && assigneeID != "" {
		memberIDs = []string{assigneeID}
	}
	projectID := rowString(row, "project_id")
	metadata := sanitizeSourceMap(rowMap(row, "metadata"))
	if metadata == nil {
		metadata = map[string]any{}
	}
	labelValues := make([]any, 0, len(labels))
	for _, label := range labels {
		if resourceType := rowString(label, "resource_type"); resourceType != "" && resourceType != "issue" {
			continue
		}
		labelValues = append(labelValues, sanitizeSourceMap(map[string]any{
			"id": rowString(label, "id"), "name": rowString(label, "name"), "color": rowString(label, "color"), "description": rowString(label, "description"),
		}))
	}
	if len(labelValues) > 0 {
		metadata["labels"] = labelValues
	}
	dependencyValues := make([]any, 0, len(dependencies))
	for _, dependency := range dependencies {
		dependencyValues = append(dependencyValues, map[string]any{
			"id": rowString(dependency, "id"), "depends_on_requirement_id": rowString(dependency, "depends_on_issue_id"), "type": rowString(dependency, "type"),
		})
	}
	if len(dependencyValues) > 0 {
		metadata["dependencies"] = dependencyValues
	}
	properties := sanitizeSourceMap(rowMap(row, "properties"))
	definitionValues := make([]any, 0, len(properties))
	for propertyID := range properties {
		definition := propertyDefinitions[propertyID]
		if definition == nil {
			continue
		}
		definitionValues = append(definitionValues, sanitizeSourceMap(map[string]any{
			"id": propertyID, "name": rowString(definition, "name"), "type": rowString(definition, "type"),
			"description": rowString(definition, "description"), "config": rowMap(definition, "config"), "icon": rowString(definition, "icon"),
		}))
	}
	sort.Slice(definitionValues, func(i, j int) bool {
		left, _ := definitionValues[i].(map[string]any)
		right, _ := definitionValues[j].(map[string]any)
		return valueString(left["id"]) < valueString(right["id"])
	})
	if len(definitionValues) > 0 {
		metadata["property_definitions"] = definitionValues
	}
	if len(metadata) == 0 {
		metadata = nil
	}
	return domain.Requirement{
		ID:                  rowString(row, "id"),
		WorkspaceID:         workspaceID,
		Key:                 issueKey(row),
		Title:               rowString(row, "title"),
		Description:         rowString(row, "description"),
		AcceptanceCriteria:  rowStringSlice(row, "acceptance_criteria"),
		Priority:            defaultString(rowString(row, "priority"), "none"),
		Status:              mapRequirementStatus(rowString(row, "status")),
		CreatedBy:           rowString(row, "creator_id"),
		AssigneeMemberIDs:   memberIDs,
		AssigneeTargetType:  assigneeType,
		AssigneeTargetID:    assigneeID,
		RepositoryIDs:       append([]string(nil), projectRepositories[projectID]...),
		TeamWorkspaceID:     projectID,
		ParentRequirementID: rowString(row, "parent_issue_id"),
		Stage:               int(rowInt64(row, "stage")),
		Position:            rowFloat64(row, "position"),
		StartDate:           rowTimePointer(row, "start_date"),
		DueDate:             rowTimePointer(row, "due_date"),
		Metadata:            metadata,
		Properties:          properties,
		Version:             maxInt64(rowInt64(row, "revision"), 1),
		CreatedAt:           rowTime(row, "created_at"),
		UpdatedAt:           rowTime(row, "updated_at"),
	}
}

func mapPostgresComment(row sourceRow, workspaceID string, rows map[string]sourceRow) domain.Comment {
	authorType := defaultString(rowString(row, "author_type"), "member")
	authorID := rowString(row, "author_id")
	comment := domain.Comment{
		ID:          rowString(row, "id"),
		WorkspaceID: workspaceID,
		TargetType:  "requirement",
		TargetID:    rowString(row, "issue_id"),
		ParentID:    rowString(row, "parent_id"),
		AuthorID:    authorID,
		AuthorType:  authorType,
		Content:     rowString(row, "content"),
		Revision:    maxInt64(rowInt64(row, "revision"), 1),
		CreatedAt:   rowTime(row, "created_at"),
		UpdatedAt:   rowTime(row, "updated_at"),
	}
	comment.RootID = sourceCommentRoot(comment.ID, rows)
	if authorType == "member" {
		comment.OriginatorID = authorID
		comment.OriginatorType = "member"
		comment.OriginatorSource = "workspace-import"
	}
	return comment
}

func mapPostgresSkill(row sourceRow, files []sourceRow, workspaceID string) domain.Skill {
	fileData := make([]any, 0, len(files))
	for _, file := range files {
		filePath := rowString(file, "path")
		if filePath == "" || path.IsAbs(filePath) || path.Clean(filePath) != filePath || strings.HasPrefix(filePath, "../") {
			continue
		}
		fileData = append(fileData, map[string]any{"path": filePath, "content": rowString(file, "content")})
	}
	contract := sanitizeSourceMap(map[string]any{
		"description": rowString(row, "description"),
		"content":     rowString(row, "content"),
		"config":      rowMap(row, "config"),
		"files":       fileData,
	})
	encoded, _ := json.Marshal(contract)
	digest := sha256.Sum256(encoded)
	return domain.Skill{
		ID:          rowString(row, "id"),
		WorkspaceID: workspaceID,
		Name:        rowString(row, "name"),
		Version:     "1",
		Kind:        "instruction",
		Contract:    contract,
		Digest:      hex.EncodeToString(digest[:]),
		Status:      "active",
		CreatedAt:   rowTime(row, "created_at"),
		UpdatedAt:   rowTime(row, "updated_at"),
	}
}

func mapPostgresSquad(row sourceRow, rows []sourceRow, workspaceID string, agents map[string]bool) (orchestration.SquadDefinition, bool) {
	squadID := rowString(row, "id")
	leaderID := rowString(row, "leader_id")
	members := make([]orchestration.SquadMember, 0, len(rows))
	nodes := make([]orchestration.WorkflowNode, 0, len(rows))
	entryIDs := make([]string, 0, len(rows))
	seenLeader := false
	for _, memberRow := range rows {
		if rowString(memberRow, "member_type") != "agent" {
			continue
		}
		agentID := rowString(memberRow, "member_id")
		if !agents[agentID] {
			continue
		}
		memberID := rowString(memberRow, "id")
		if memberID == "" {
			memberID = "squad-member-" + squadID + "-" + agentID
		}
		leader := agentID == leaderID
		seenLeader = seenLeader || leader
		role := rowString(memberRow, "role")
		if role == "" {
			role = "member"
			if leader {
				role = "leader"
			}
		}
		members = append(members, orchestration.SquadMember{
			ID:          memberID,
			AgentID:     agentID,
			Role:        role,
			Leader:      leader,
			MaxAttempts: 1,
		})
		nodeID := "node-" + memberID
		nodes = append(nodes, orchestration.WorkflowNode{
			ID:       nodeID,
			Kind:     orchestration.NodeAgent,
			AgentRef: &orchestration.VersionedRef{ID: agentID, Revision: 1},
		})
		entryIDs = append(entryIDs, nodeID)
	}
	if squadID == "" || !seenLeader || len(members) == 0 {
		return orchestration.SquadDefinition{}, false
	}
	return orchestration.SquadDefinition{
		ID:               squadID,
		WorkspaceID:      workspaceID,
		Name:             rowString(row, "name"),
		Description:      rowString(row, "description"),
		Revision:         maxInt64(rowInt64(row, "revision"), 1),
		PublishedVersion: 1,
		Members:          members,
		Graph: orchestration.WorkflowGraph{
			ID:           "graph-" + squadID,
			Version:      1,
			EntryNodeIDs: append([]string(nil), entryIDs...),
			ExitNodeIDs:  append([]string(nil), entryIDs...),
			Nodes:        nodes,
		},
		Policy: orchestration.SquadPolicy{MaxNestingDepth: 1},
		Status: orchestration.SquadPublished,
	}, true
}

func mapPostgresAutomation(row sourceRow, rows []sourceRow, workspaceID string) domain.Automation {
	sources := make([]any, 0, len(rows))
	for _, trigger := range rows {
		sources = append(sources, sanitizeSourceMap(map[string]any{
			"id":              rowString(trigger, "id"),
			"kind":            rowString(trigger, "kind"),
			"enabled":         rowBoolDefault(trigger, "enabled", true),
			"cron_expression": rowString(trigger, "cron_expression"),
			"timezone":        rowString(trigger, "timezone"),
			"next_run_at":     trigger["next_run_at"],
			"label":           rowString(trigger, "label"),
			"provider":        rowString(trigger, "provider"),
			"event_filters":   trigger["event_filters"],
		}))
	}
	return domain.Automation{
		ID:          rowString(row, "id"),
		WorkspaceID: workspaceID,
		Name:        rowString(row, "title"),
		Version:     maxInt64(rowInt64(row, "revision"), 1),
		Trigger:     map[string]any{"sources": sources},
		Nodes: []map[string]any{sanitizeSourceMap(map[string]any{
			"assignee_type":        defaultString(rowString(row, "assignee_type"), "agent"),
			"assignee_id":          rowString(row, "assignee_id"),
			"execution_mode":       rowString(row, "execution_mode"),
			"issue_title_template": rowString(row, "issue_title_template"),
			"project_id":           rowString(row, "project_id"),
			"description":          rowString(row, "description"),
		})},
		Enabled:   rowString(row, "status") == "active",
		CreatedAt: rowTime(row, "created_at"),
		UpdatedAt: rowTime(row, "updated_at"),
	}
}

type postgresArtifacts struct {
	attachments []domain.EntityAttachment
	entries     []ArtifactEntry
	byOwner     map[string][]string
}

func mapPostgresAttachments(ctx context.Context, rows []sourceRow, uploadRoot, workspaceID string, requirements, comments, chatMessages map[string]bool) (postgresArtifacts, map[string][]byte, error) {
	result := postgresArtifacts{byOwner: map[string][]string{}}
	payloads := map[string][]byte{}
	var total int64
	for _, row := range rows {
		ownerType, ownerID := attachmentOwner(row, requirements, comments, chatMessages)
		if ownerID == "" {
			continue
		}
		attachmentID := rowString(row, "id")
		if attachmentID == "" {
			continue
		}
		payload, err := readSourceUpload(ctx, uploadRoot, rowString(row, "url"))
		if err != nil {
			return postgresArtifacts{}, nil, fmt.Errorf("attachment %q: %w", attachmentID, err)
		}
		if expected, exists := row["size_bytes"]; exists && expected != nil && rowInt64(row, "size_bytes") != int64(len(payload)) {
			return postgresArtifacts{}, nil, fmt.Errorf("attachment %q size mismatch: database=%d file=%d", attachmentID, rowInt64(row, "size_bytes"), len(payload))
		}
		total += int64(len(payload))
		if total > maxArchiveBytes {
			return postgresArtifacts{}, nil, errors.New("source attachments exceed the portable archive limit")
		}
		key := artifact.Key{TenantID: workspaceID, ArtifactID: attachmentID, Version: 1}
		entryPath := artifactPath(key)
		digest := sha256.Sum256(payload)
		mediaType := defaultString(rowString(row, "content_type"), "application/octet-stream")
		result.attachments = append(result.attachments, domain.EntityAttachment{
			ID:          attachmentID,
			WorkspaceID: workspaceID,
			OwnerType:   ownerType,
			OwnerID:     ownerID,
			Filename:    defaultString(rowString(row, "filename"), "attachment"),
			MediaType:   mediaType,
			SizeBytes:   int64(len(payload)),
			ArtifactURI: key.URI(),
			CreatedBy:   rowString(row, "uploader_id"),
			CreatedAt:   rowTime(row, "created_at"),
		})
		result.entries = append(result.entries, ArtifactEntry{
			Key:           key,
			Path:          entryPath,
			MediaType:     mediaType,
			SizeBytes:     int64(len(payload)),
			ContentSHA256: hex.EncodeToString(digest[:]),
			Immutable:     true,
		})
		result.byOwner[ownerType+":"+ownerID] = append(result.byOwner[ownerType+":"+ownerID], attachmentID)
		payloads[entryPath] = payload
	}
	return result, payloads, nil
}

func attachmentOwner(row sourceRow, requirements, comments, chatMessages map[string]bool) (string, string) {
	if id := rowString(row, "comment_id"); comments[id] {
		return "comment", id
	}
	if id := rowString(row, "issue_id"); requirements[id] {
		return "requirement", id
	}
	if id := rowString(row, "chat_message_id"); chatMessages[id] {
		return "chat_message", id
	}
	return "", ""
}

func attachEntityReferences(snapshot *store.WorkspaceSnapshot, byOwner map[string][]string) {
	for index := range snapshot.Comments {
		comment := &snapshot.Comments[index]
		comment.AttachmentIDs = append([]string(nil), byOwner["comment:"+comment.ID]...)
		for revisionIndex := range snapshot.CommentRevisions[comment.ID] {
			snapshot.CommentRevisions[comment.ID][revisionIndex].AttachmentIDs = append([]string(nil), comment.AttachmentIDs...)
		}
	}
	for index := range snapshot.ChatMessages {
		message := &snapshot.ChatMessages[index]
		message.AttachmentIDs = append([]string(nil), byOwner["chat_message:"+message.ID]...)
	}
}

func readSourceUpload(ctx context.Context, root, rawURL string) ([]byte, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("source upload root is required when attachments exist")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, errors.New("attachment URL is invalid")
	}
	decodedPath, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil {
		return nil, errors.New("attachment URL path is invalid")
	}
	const marker = "/uploads/"
	index := strings.Index(decodedPath, marker)
	if index < 0 {
		return nil, errors.New("attachment URL is not backed by the configured upload root")
	}
	key := strings.TrimPrefix(decodedPath[index+len(marker):], "/")
	if key == "" || strings.Contains(key, `\`) || path.IsAbs(key) || path.Clean(key) != key || strings.HasPrefix(key, "../") || strings.HasSuffix(key, ".meta.json") || strings.HasSuffix(key, ".tmp") {
		return nil, errors.New("attachment storage key is unsafe")
	}

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve source upload root: %w", err)
	}
	filePath, err := filepath.EvalSymlinks(filepath.Join(realRoot, filepath.FromSlash(key)))
	if err != nil {
		return nil, fmt.Errorf("resolve source attachment: %w", err)
	}
	if !pathWithin(realRoot, filePath) {
		return nil, errors.New("attachment storage key escapes the upload root")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open source attachment: %w", err)
	}
	defer file.Close()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	payload, err := io.ReadAll(io.LimitReader(file, maxEntryBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read source attachment: %w", err)
	}
	if len(payload) > maxEntryBytes {
		return nil, errors.New("source attachment exceeds the portable object limit")
	}
	return payload, nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func sourceCommentRoot(id string, rows map[string]sourceRow) string {
	current := id
	seen := map[string]bool{}
	for current != "" && !seen[current] {
		seen[current] = true
		parent := rowString(rows[current], "parent_id")
		if parent == "" || rows[parent] == nil {
			return current
		}
		current = parent
	}
	return id
}

func indexRows(rows []sourceRow, key string) map[string]sourceRow {
	result := make(map[string]sourceRow, len(rows))
	for _, row := range rows {
		if id := rowString(row, key); id != "" {
			result[id] = row
		}
	}
	return result
}

func groupRows(rows []sourceRow, key string) map[string][]sourceRow {
	result := make(map[string][]sourceRow)
	for _, row := range rows {
		if id := rowString(row, key); id != "" {
			result[id] = append(result[id], row)
		}
	}
	return result
}

func rowString(row sourceRow, key string) string {
	if row == nil {
		return ""
	}
	return valueString(row[key])
}

func mapString(value map[string]any, key string) string {
	return valueString(value[key])
}

func valueString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return ""
	}
}

func rowMap(row sourceRow, key string) map[string]any {
	if value, ok := row[key].(map[string]any); ok {
		return value
	}
	return nil
}

func rowStringSlice(row sourceRow, key string) []string {
	values, ok := row[key].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text := valueString(value); text != "" {
			result = append(result, text)
		}
	}
	return result
}

func rowInt64(row sourceRow, key string) int64 {
	switch value := row[key].(type) {
	case json.Number:
		integer, _ := value.Int64()
		return integer
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	case string:
		integer, _ := strconv.ParseInt(value, 10, 64)
		return integer
	default:
		return 0
	}
}

func rowFloat64(row sourceRow, key string) float64 {
	switch value := row[key].(type) {
	case json.Number:
		number, _ := value.Float64()
		return number
	case float64:
		return value
	case string:
		number, _ := strconv.ParseFloat(value, 64)
		return number
	default:
		return 0
	}
}

func rowBoolDefault(row sourceRow, key string, fallback bool) bool {
	value, exists := row[key]
	if !exists || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(typed)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func rowTime(row sourceRow, key string) time.Time {
	if value, ok := row[key].(time.Time); ok {
		return value.UTC()
	}
	text := rowString(row, key)
	if text == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func rowTimePointer(row sourceRow, key string) *time.Time {
	value := rowTime(row, key)
	if value.IsZero() {
		return nil
	}
	return &value
}

func maxInt64(value, minimum int64) int64 {
	if value < minimum {
		return minimum
	}
	return value
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func normalizeRuntimeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	aliases := map[string]string{
		"claude-code":    "claude",
		"cursor-agent":   "cursor",
		"github-copilot": "copilot",
		"open-code":      "opencode",
		"qwen-code":      "qwen",
		"kiro-cli":       "kiro",
	}
	if alias := aliases[value]; alias != "" {
		return alias
	}
	return value
}

func normalizeThinkingLevel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	allowed := map[string]bool{"none": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true, "off": true, "on": true}
	if allowed[value] {
		return value
	}
	return ""
}

func normalizeServiceTier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "priority" || value == "flex" {
		return value
	}
	return ""
}

func sanitizePortableArgs(args []string) []string {
	result := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		argument := strings.TrimSpace(args[index])
		name, _, hasValue := strings.Cut(argument, "=")
		lowerName := strings.ToLower(name)
		blocked := containsSensitiveName(lowerName) || lowerName == "--cwd" || lowerName == "--workdir" || lowerName == "--workspace"
		if blocked {
			if !hasValue && index+1 < len(args) && !strings.HasPrefix(strings.TrimSpace(args[index+1]), "-") {
				index++
			}
			continue
		}
		if argument == "" || isAbsolutePortablePath(argument) {
			continue
		}
		if hasValue {
			_, value, _ := strings.Cut(argument, "=")
			if isAbsolutePortablePath(value) {
				continue
			}
		}
		result = append(result, argument)
	}
	return result
}

func containsSensitiveName(value string) bool {
	for _, fragment := range []string{"password", "passwd", "token", "secret", "credential", "cookie", "authorization", "private-key", "private_key", "api-key", "api_key", "apikey"} {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func sanitizeSourceMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		if containsSensitiveName(strings.ToLower(key)) {
			continue
		}
		switch typed := item.(type) {
		case map[string]any:
			result[key] = sanitizeSourceMap(typed)
		case []any:
			cleaned := make([]any, 0, len(typed))
			for _, child := range typed {
				switch nested := child.(type) {
				case map[string]any:
					cleaned = append(cleaned, sanitizeSourceMap(nested))
				case string:
					if !isAbsolutePortablePath(nested) {
						cleaned = append(cleaned, nested)
					}
				default:
					cleaned = append(cleaned, child)
				}
			}
			result[key] = cleaned
		case string:
			if !isAbsolutePortablePath(typed) {
				result[key] = typed
			}
		default:
			if item != nil {
				result[key] = item
			}
		}
	}
	return result
}

func isAbsolutePortablePath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\\`) {
		return true
	}
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

func mapRequirementStatus(status string) domain.RequirementStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "in_progress":
		return domain.RequirementDeveloping
	case "in_review":
		return domain.RequirementReadyForHumanQA
	case "done":
		return domain.RequirementAccepted
	case "blocked":
		return domain.RequirementHumanTriageRequired
	case "cancelled":
		return domain.RequirementCancelled
	default:
		return domain.RequirementReceived
	}
}

func mapProjectStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed":
		return "completed"
	case "cancelled":
		return "archived"
	case "paused":
		return "paused"
	default:
		return "active"
	}
}

func mapChatRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "user" || role == "assistant" || role == "system" {
		return role
	}
	return "system"
}

func issueKey(row sourceRow) string {
	if number := rowInt64(row, "number"); number > 0 {
		return "ISSUE-" + strconv.FormatInt(number, 10)
	}
	return "ISSUE-" + rowString(row, "id")
}

func repositoryName(label, cloneURL string) string {
	if strings.TrimSpace(label) != "" {
		return strings.TrimSpace(label)
	}
	trimmed := strings.TrimSuffix(strings.TrimSpace(cloneURL), ".git")
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Path != "" {
		trimmed = parsed.Path
	}
	trimmed = strings.Trim(trimmed, "/")
	if index := strings.LastIndex(trimmed, "/"); index >= 0 {
		trimmed = trimmed[index+1:]
	}
	return defaultString(trimmed, "repository")
}

func repositoryProvider(cloneURL string) string {
	if parsed, err := url.Parse(cloneURL); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return "git"
}

func sortPostgresSnapshot(snapshot *store.WorkspaceSnapshot) {
	sort.Slice(snapshot.Requirements, func(i, j int) bool { return snapshot.Requirements[i].ID < snapshot.Requirements[j].ID })
	sort.Slice(snapshot.Comments, func(i, j int) bool { return snapshot.Comments[i].ID < snapshot.Comments[j].ID })
	sort.Slice(snapshot.Attachments, func(i, j int) bool { return snapshot.Attachments[i].ID < snapshot.Attachments[j].ID })
	sort.Slice(snapshot.Repositories, func(i, j int) bool { return snapshot.Repositories[i].ID < snapshot.Repositories[j].ID })
	sort.Slice(snapshot.TeamWorkspaces, func(i, j int) bool { return snapshot.TeamWorkspaces[i].ID < snapshot.TeamWorkspaces[j].ID })
	sort.Slice(snapshot.MCPServers, func(i, j int) bool { return snapshot.MCPServers[i].ID < snapshot.MCPServers[j].ID })
	sort.Slice(snapshot.Skills, func(i, j int) bool { return snapshot.Skills[i].ID < snapshot.Skills[j].ID })
	sort.Slice(snapshot.Bindings, func(i, j int) bool { return snapshot.Bindings[i].ID < snapshot.Bindings[j].ID })
	sort.Slice(snapshot.Automations, func(i, j int) bool { return snapshot.Automations[i].ID < snapshot.Automations[j].ID })
	sort.Slice(snapshot.ChatSessions, func(i, j int) bool { return snapshot.ChatSessions[i].ID < snapshot.ChatSessions[j].ID })
	sort.Slice(snapshot.ChatMessages, func(i, j int) bool { return snapshot.ChatMessages[i].ID < snapshot.ChatMessages[j].ID })
}
