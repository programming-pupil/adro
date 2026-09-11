# Workspace migration

ADRO migrates durable, user-authored workspace state through a verified ZIP
archive. Import uses the same preflight and atomic commit path whether the ZIP
was exported from ADRO or converted from a compatible PostgreSQL schema.

## What moves

- Agent runtime, model, thinking level, service tier, portable arguments, and
  concurrency configuration
- Squads and their Agent membership, issues and comments, projects and Git
  repositories, Skills and Agent bindings, automations, chats, and attachments
- Stable parent, assignee, project, repository, comment-thread, chat, and
  attachment references; `rename` rewrites all of them deterministically

Authentication credentials, custom environment variables, webhook secrets,
provider sessions, live tasks, queues, leases, run history, and machine-local
paths are deliberately excluded. Reauthenticate installed coding clients after
migration. Local-directory project resources must be registered again on the
target machine.

## Portable ZIP

Stop the local service before CLI import so it cannot overwrite the same state
files concurrently.

```bash
go run ./cmd/adroctl workspace export \
  --home .adro --artifact-root .adro/artifacts \
  --workspace local --file workspace.zip

go run ./cmd/adroctl workspace preflight \
  --workspace target --file workspace.zip --conflict rename

go run ./cmd/adroctl workspace import \
  --home .adro --artifact-root .adro/artifacts \
  --workspace target --file workspace.zip --conflict rename --dry-run

go run ./cmd/adroctl workspace import \
  --home .adro --artifact-root .adro/artifacts \
  --workspace target --file workspace.zip --conflict rename
```

The setup and System administration screens expose the same ZIP preflight and
import contract through the HTTP API.

## PostgreSQL conversion

Use a source account that can only read the selected database and attachment
directory. Prefer `ADRO_MIGRATION_SOURCE_DSN` over a command-line DSN so the
connection string is not stored in shell history.

```bash
export ADRO_MIGRATION_SOURCE_DSN="$SOURCE_DSN"
export ADRO_MIGRATION_SOURCE_WORKSPACE="$SOURCE_WORKSPACE"
export ADRO_MIGRATION_SOURCE_UPLOAD_ROOT="$SOURCE_UPLOAD_ROOT"

go run ./cmd/adroctl workspace export-postgres \
  --file workspace.zip

go run ./cmd/adroctl workspace preflight-postgres \
  --workspace local --conflict rename

go run ./cmd/adroctl workspace import-postgres \
  --home .adro --artifact-root .adro/artifacts \
  --workspace local --conflict rename --dry-run

go run ./cmd/adroctl workspace import-postgres \
  --home .adro --artifact-root .adro/artifacts \
  --workspace local --conflict rename
```

Conversion reads one read-only, repeatable-read PostgreSQL snapshot. When an
attachment is present, its URL must resolve beneath `SOURCE_UPLOAD_ROOT`; path
traversal, symlink escape, size drift, or digest drift fails the operation.

## Conflict modes

| Mode | Behavior |
| --- | --- |
| `rename` | Deterministically remap imported IDs and all internal references; recommended when combining workspaces |
| `skip` | Keep existing target records and import only IDs that do not exist |
| `fail` | Stop at the first target ID conflict without changing target state |

An archive digest scopes replay protection. Re-importing the same digest into
the same target is a no-op receipt rather than a duplicate write.

## Failure and rollback

Preflight verifies the manifest digest, entry limits, attachment SHA-256 and
size, definition contracts, and all portable object references. Import then
backs up both control and orchestration stores, writes immutable artifacts, and
commits each store. Any error deletes artifacts created by that attempt and
restores both backups. Existing target artifacts are never deleted.

Keep the source database and exported ZIP unchanged until these checks pass:

1. The import receipt reports `dry_run: false` and the expected entity counts.
2. Imported Agents and Squads are visible and their assigned issues retain the
   same execution target.
3. Imported chats, comments, projects, Skills, automations, and attachments open.
4. A newly created test issue runs with an installed, authenticated coding client.
