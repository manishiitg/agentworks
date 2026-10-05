# Knowledge Base MVP operations

Knowledge Base is a built-in product (`knowledgebase`). The app reads content and
shows content and access settings. Its chat manages folder access, initial backup configuration and shared Files Git requests. The reader and Git controls reuse the platform Files view.
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
| `AGENTWORKS_KNOWLEDGEBASE_BACKUP_REMOTE` | Optional private Git remote; overrides any app-saved destination; when unset, use app-saved setup or leave backup unconfigured. |
| `AGENTWORKS_KNOWLEDGEBASE_BACKUP_BRANCH` | Publication branch; defaults to `main`. |

Without an explicit data root, data lives below the platform's persistent
`AGENTWORKS_STATE_ROOT`, partitioned by the organization ID. Run one host, with
processes sharing the same local volume. SQLite WAL and advisory locks require
local filesystem semantics.

Provision a dedicated private backup repository and server Git credentials through
the deployment's secret management. Restrict direct repository access to backup
administrators: Git cannot enforce the application's folder ACLs. Avoid credentials
embedded in remote URLs. The product never returns credentials to connected agents.

Administrators can also use the `Configure backup` action above the chat.
Supply the exact dedicated private repository SSH URL and branch. The existing
builder proposes `manage_knowledgebase_access` / `configure_backup` for app
confirmation. An unrestricted external admin connection can apply it directly.
Setup persists private configuration across restarts; it does not verify remote
reachability or publish content. Server SSH access must already be provisioned.
The setup action cannot redirect an existing destination. Environment variables
remain authoritative when configured.

## Connections

Manage connections through the platform's global MCP connection settings.
Knowledge Base uses the same `/api/external/v1/mcp` endpoint; there is no separate
KB Connect tab or server. `knowledgebase:read` admits the
reader tools; `knowledgebase:write` additionally admits save and backup tools.
Write connections carry both scopes. Unrestricted external writers may use access list/grant/revoke directly within current Owner grants; administrative service-account actions and backup setup require a current administrator. Folder-capped tokens and managed workflow/Crew execution remain content-only. App chat continues to require confirmation of its frozen access/configuration proposals.
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

On a local single-user instance, open the global **Connect an AI agent (MCP)**
page and create the `agentworks-local` access token. No name input is needed. It includes all access available to the local
account, without a scope picker; live folder grants still apply. Copy the token
into your HTTP MCP client's `Authorization: Bearer <ACCESS_TOKEN>` header.
Local tokens have no automatic expiry; remove one from the same page when finished. Multi-user and
hosted server setup uses the existing OAuth connection approval flow.

The MVP has five tools, all requiring `action`:

| Tool | Actions |
| --- | --- |
| `browse_knowledgebase` | `folders`, `entries` |
| `read_knowledgebase` | `read`, `search` |
| `update_knowledgebase` | `create`, `update`, `delete`, `create_folder` |
| `backup_knowledgebase` | `status`, `commit`, `push`, `git` |
| `manage_knowledgebase_access` | `inspect`; other access actions belong to the app's builder |

Read-only connections discover four tools with read-only action schemas. Use different request IDs for different actions.

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
Base data root, including live files, registries, private identity/request/
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
The repository's organization and remote are pinned. Files selects the active branch; receipts are branch-bound and stale receipts cannot be pushed to a different branch.

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

## Review and migration prerequisites

Access changes requested in chat are pending until approved in the app. Review
the exact folder, identity and role; cancel incorrect proposals. Stale versions
require a fresh inspection and proposal. Proposals expire after 15 minutes.

Migration needs an explicit external owner connection with Knowledge Base write
scope and source-project builder authority for every action. It is unavailable
in workflow/Crew agent execution, Run mode, schedules and steps. Import first,
then rebind every consumer through its owner and confirmed access proposal,
then cut over. Pause writers, schedules, executions and affected configuration
changes; cutover re-scans and refuses remaining legacy consumers. No installation
is migrated by merging this PR. OAuth Knowledge Base scopes are opt-in.

Activity tracking is deferred for MVP: no Activity view, API endpoint, or event
recording. Recovery journals and retry/backup receipts remain in private state.

## Files Git controls

Configure the repository using the existing backup setup action. Root Readers see the shared Files Source Control, history, diff and blame; unrestricted root Editors/Owners can stage, commit, push, pull, change branches, stash or discard. Folder-scoped readers keep the ordinary Files view and selected receipt backup tools. The external MCP `git` action is available to unrestricted writable connections; the viewer exposes read-only Git to root Readers. Git requests use `/api/knowledgebase/git` and `backup_knowledgebase(action=git)`, not workspace paths or a separate MCP server.

Example external MCP call:

```json
{"action":"git","op":"pull","request_id":"pull-main-001"}
```

Pull fast-forwards and updates live knowledge. Commit or stash unpublished edits before pulling or switching an existing branch. A branch change preserves metadata/IDs for surviving files and existing folder grants; imports new files as notes. Stash/restore/discard also change live content and are immediately visible to authorized readers. No UI content editor is added.

The remote repository must contain only regular Markdown using KB-valid paths. Links, submodules, binary/control files, case collisions or trees above 5,000 files / 50 MiB are rejected atomically. Each Markdown file is limited to 10 MiB; private Git generations are limited to 512 MiB. Unresolved conflicts leave the previous live state intact. Pull supports fast-forward only; reconcile diverged histories outside the MVP flow.

Push uses a remote lease and persists a delivery intent before transport. If its outcome is unknown, retry the original Git push request ID as the same user; other repository actions and receipt publication remain blocked until it is reconciled. Complete Files commits/pushes before using the scoped selected-version receipt flow. Include the private Git generation pointer/directories and pending delivery intent in a full disaster-recovery backup.
