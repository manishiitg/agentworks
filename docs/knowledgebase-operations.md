# Knowledge Base MVP operations

Knowledge Base is a built-in product (`knowledgebase`). The app reads content and
shows access, activity and connection settings. Its chat manages folder access.
Content saves and explicit Git backups happen through MCP, including in Crews,
Code and workflows. An authorized reader sees a successful save immediately.

## Installation

Enable the product through the existing `AGENT_PRODUCTS` and frontend
`AGENTWORKS_ENABLED_PRODUCT_SURFACES` configuration. Grant the product to accounts
through the existing account directory. Product access does not grant folder
access. Administrators establish root or narrower folder grants in the access chat.

Server configuration:

| Variable | Meaning |
| --- | --- |
| `AGENTWORKS_KNOWLEDGEBASE_ORG` | Trusted installation organization ID; defaults to `installation`. |
| `AGENTWORKS_KNOWLEDGEBASE_ROOT` | Optional absolute persistent data directory outside every workspace file root. |
| `AGENTWORKS_KNOWLEDGEBASE_BACKUP_REMOTE` | Optional private Git remote; unset means live content works with Git backup unconfigured. |
| `AGENTWORKS_KNOWLEDGEBASE_BACKUP_BRANCH` | Publication branch; defaults to `main`. |

Without an explicit data root, data lives below the platform's persistent
`AGENTWORKS_STATE_ROOT`, partitioned by the organization ID. Run one host, with
processes sharing the same local volume. SQLite WAL and advisory locks require
local filesystem semantics.

Provision a dedicated private backup repository and server Git credentials through
the deployment's secret management. Restrict direct repository access to backup
administrators: Git cannot enforce the application's folder ACLs. Avoid credentials
embedded in remote URLs. The product never returns credentials to connected agents.

## Connections

Create and revoke separate connections in Connect. `knowledgebase:read` admits the
reader tools; `knowledgebase:write` additionally admits save and backup tools.
Write connections carry both scopes.
Optional folder caps further restrict the identity's current grants. Omitting caps
uses its current grants; an empty cap list grants nothing. Only administrators can
mint a Knowledge Base token for a managed service account. Disabling a service
account invalidates its connections.

## Workflow and Crew rollout

The shared product coexists with workflow-local `knowledgebase/`, legacy
`knowledgebase_sources`, and Crew workspace attachments. Merging or deploying
this MVP does not migrate them, rewrite manifests, or grant folder access.
Work/Code profiles and workflow tools can use the shared MCP surface under their
execution identity; an existing attachment is not a shared-folder grant.
See [the integration and migration plan](design/knowledgebase-integration-migration.md)
for the merge rollout, identity/audience requirements, and explicit cutover.

## MCP operations

The MCP endpoint is `/api/external/v1/mcp`. Authenticate with a Bearer token, use
`get_api_spec` to discover tools and schemas, then `call_tool` with the operation
name and its arguments. The existing local bridge uses the same catalog.

The MVP has five tools, all requiring `action`:

| Tool | Actions |
| --- | --- |
| `browse_knowledgebase` | `folders`, `entries` |
| `read_knowledgebase` | `read`, `search` |
| `update_knowledgebase` | `create`, `update`, `delete`, `create_folder` |
| `backup_knowledgebase` | `status`, `commit`, `push` |
| `manage_knowledgebase_access` | `inspect`; other access actions belong to the app's builder |

Read-only connections discover four tools with read-only action schemas. Activity
history stays in the app. Use different request IDs for different actions.

Typical write flow: list folders, read an entry's version, call
`update_knowledgebase(action=update)` with `expected_version`, a patch or replacement, and a stable
`request_id`. Keep the same ID and arguments when retrying uncertain delivery.
To back up, call `backup_knowledgebase(action=commit)` with selected current versions/deletion
tokens, then `backup_knowledgebase(action=push)` with the returned opaque receipt. Never stage the
live directory or push a private receipt ref yourself.

Content and diffs must be UTF-8 text. Binary control characters and NUL bytes are
rejected; CRLF and CR line endings are normalized to LF. Tags must be unique and
non-empty; an empty tag list clears tags.

Without a configured remote or any initialized backup history/receipts, a
deleted filename can be reused immediately with a new entry ID. Its retained
deletion record reports `backup_status: not_required`. Removing a remote after
backup was initialized does not waive confirmation for reserved paths.

Backup status includes a durable, sanitized `last_backup_error` for backend
failures. Successful publication, confirmed unknown-outcome recovery, or
administrator reconciliation clears it. Argument and authorization errors do not
overwrite organization-wide backup status. Backup calls serialize network work
with a 30-second timeout; a concurrent backup or security mutation can return
retryable `BACKUP_BUSY`. Live reads and ordinary content saves remain available.

## Recovery

Git contains plain Markdown from explicitly pushed snapshots. It is insufficient
to recover pending edits, permissions or metadata. Back up the entire Knowledge
Base data root, including live files, registries, private identity/activity/request/
journal/receipt records, staging Git objects and a consistent SQLite snapshot.
Use the SQLite backup API or stop all writers before copying the grant database;
copying only its main file while WAL is active is not a consistent backup.

Restore into the same trusted organization boundary. Preserve registry entry IDs,
sequences, tombstones, receipt ownership and ACL generation. Startup recovery
finishes journaled saves before serving content. An uncertain push is reconciled
on the next explicit publication call. Do not force-push, automatically rebase,
or import remote changes into live content. Unexpected branch changes require
administrator reconciliation. If only Markdown survives, restore into an
administrator-only area and reassign access before exposing it.

To accept an existing plain-Markdown branch or an observed external branch change,
an administrator uses `POST /api/knowledgebase/maintenance/reconcile-backup` with
their normal signed-in session. Review the repository's branch first. This
maintenance endpoint observes and accepts the base; it never imports live content,
commits or pushes, and MCP connection tokens cannot call it. A pending unknown
receipt must be reconciled by retrying its explicit push before this operation.
The repository's configured organization, remote and branch are pinned; changing
them requires provisioning a separate data root rather than silently redirecting
existing receipts.

The dedicated access builder offers the shared Codex and Pi provider adapters in
MCP-only structured mode. Other engines can be added after their adapter preserves
the same restricted tool surface. Provider accounts and models use the platform's
existing configuration.


Workflow/Crew adoption is now available through owner-managed shared folder
bindings and explicit `update_knowledgebase` migration actions. Follow the
[integration and migration runbook](design/knowledgebase-integration-migration.md).
Deployment itself performs no migration or grant changes. Pause project writers,
schedules and triggers before cutover; import and rollback preserve source files,
and rollback also preserves imported entries and later content edits.
