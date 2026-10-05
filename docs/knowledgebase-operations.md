# Brain MVP operations

Brain is the product name. The stable product ID remains `knowledgebase`; API routes, MCP tool names, scopes, storage paths, and existing bindings retain their names for compatibility.

Brain is a built-in product (`knowledgebase`). The app reads content and
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
Supply the repository HTTPS URL and username; an optional PAT for a private repository is entered in the secure confirmation field. Branch defaults to main. The existing
builder proposes `manage_knowledgebase_access` / `configure_backup` for app
confirmation. An unrestricted external admin connection can apply it directly.
Setup persists private configuration across restarts; it does not verify remote
reachability or publish content. An HTTPS PAT is encrypted in KB-owned private configuration; existing SSH URLs use host SSH credentials.
The setup action cannot redirect an existing destination. Environment variables
remain authoritative when configured.

## Connections

Manage connections through the platform's global MCP connection settings.
Brain uses the same `/api/external/v1/mcp` endpoint; there is no separate
KB Connect tab or server. `knowledgebase:read` admits the
reader tools; `knowledgebase:write` additionally admits save and backup tools.
Write connections carry both scopes. Unrestricted external writers may use access list/grant/revoke directly within current Owner grants; administrative service-account actions and backup setup require a current administrator. Folder-capped tokens and managed workflow/Crew execution remain content-only. App chat continues to require confirmation of its frozen access/configuration proposals.
Optional folder caps further restrict the identity's current grants. Omitting caps
uses its current grants; an empty cap list grants nothing. Only administrators can
mint a Brain token for a managed service account. Disabling a service
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
| `manage_knowledgebase_access` | `inspect`; unrestricted writable external connections also expose `list`, `grant`, `revoke`, `create_service_account`, `disable_service_account`, `configure_backup` |

Read-only connections discover four tools with read-only action schemas. Use different request IDs for different actions.

### Set up a GitHub backup through MCP

An administrator using an unrestricted writable connection can call
`manage_knowledgebase_access` with:

```json
{
  "action": "configure_backup",
  "remote_url": "https://github.com/your-org/knowledge-backup.git",
  "username": "your-github-username",
  "pat": "<optional PAT for a private repository>",
  "branch": "main",
  "request_id": "backup-setup-1"
}
```

Supply an existing repository URL and username. The PAT is optional: public repositories can be read without one, while private reads and authenticated writes need a suitable credential. A public repository may still need a PAT to push. Setup stores configuration without creating the repository, checking remote access, committing or pushing. Use `backup_knowledgebase` for status, commit and push. External MCP setup applies directly; app chat uses its existing confirmation card with a secure PAT field, never asks for the PAT in messages.

KB owns the secret: the PAT is AES-256-GCM encrypted in private control storage, bound to the organization, remote and username. Git receives it only in a URL-scoped Authorization header via its child environment, with redirects and credential caching disabled. No PAT or header is stored in Git, tool responses or request journals. This has no Vault dependency. Reconfigure the same destination with a new request ID to rotate a PAT; omit `pat` to retain it, or send `pat: ""` to remove it. The repository destination remains pinned. Existing SSH backups continue using host SSH credentials and cannot accept a PAT.

The encryption key derives from the platform's `AUTH_SECRET`. Preserve that secret through restarts/restores; changing it requires re-entering the PAT. Back up KB private configuration securely, separate from its Markdown Git backup. Deployment environment destinations remain operator-managed.

### Give another user read or write access through MCP

Use `manage_knowledgebase_access(action=list)` on the target folder to discover
existing platform identities and grants, then `action=inspect` to obtain the
current `acl_version`. A folder Owner or administrator can grant access:

```json
{
  "action": "grant",
  "folder_path": "Engineering/Payments/CheckoutService",
  "identity_id": "<identity_id returned by list>",
  "role": "Editor",
  "expected_acl_version": "<acl_version returned by inspect>",
  "request_id": "checkout-access-1"
}
```

`Reader` can read, `Editor` can read and update, and `Owner` can also manage
access. Grants inherit to descendants. Repeat `grant` with a new role to change
access; use `revoke` with the identity, folder, current ACL version and a new
request ID to remove its direct grant. An inherited parent grant still applies.
Authorized external MCP changes take effect immediately. These actions target
existing platform users or managed service accounts; they do not invite users.
An Editor cannot grant access merely because they can update content.

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

Migration needs an explicit external owner connection with Brain write
scope and source-project builder authority for every action. It is unavailable
in workflow/Crew agent execution, Run mode, schedules and steps. Import first,
then rebind every consumer through its owner and confirmed access proposal,
then cut over. Pause writers, schedules, executions and affected configuration
changes; cutover re-scans and refuses remaining legacy consumers. No installation
is migrated by merging this PR. OAuth Brain scopes are opt-in.

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

## Attach to a workflow through MCP, Builder or UI

In Workflow → Attached folders, the Brain folders panel follows the Vault project selection pattern: it lists caller-authorized folders, permits owners to select them, and offers Read only / Read-write. Selection never grants permission. The full workflow audience must already have Reader access; the execution identity needs Editor access for a write binding. The existing Ask AI button sends setup to the workflow Builder.

The root Builder can discover folders with `browse_knowledgebase action=folders`, then use `manage_knowledgebase_access action=inspect_project` for its exact workspace and `bind_project` / `unbind_project` with the returned manifest version. These actions also work directly through the global MCP. External tokens need KB read/write plus existing authoring scopes for the exact workflow/Crew. KB-only writers and folder-capped tokens cannot change project bindings. No sixth MCP tool or dedicated connection is added.

For a read smoke test, create a Markdown note containing a fresh marker, bind its folder under an alias such as `kbtest`, and ask the Builder to retrieve it through `read_knowledgebase`. Configure an agent step with `knowledgebase_access=read` (ordinary workflows have KB enabled by default), and ask it to retrieve the same marker using `binding_alias=kbtest`. Check its tool trace. For writes, use a write binding and `knowledgebase_access=read-write` plus a concrete `knowledgebase_contribution`. Content is shared immediately without a Git push. Read-only and disabled steps cannot write; steps cannot change bindings or grants. Folder grants and the output audience are rechecked on tool calls.

MCP sequence (use real values returned by discovery):

```json
{"action":"inspect_project","workspace_path":"Workflow/payments"}
{"action":"bind_project","workspace_path":"Workflow/payments","folder_id":"folder_ID","alias":"kbtest","access":"read","expected_manifest_version":"VERSION_FROM_INSPECT","request_id":"bind-kbtest-1"}
```

Manifest version conflicts require inspecting again before a new request. Exact retries are idempotent but still check project ownership and token revocation. Shared content calls resolve against the invoking step's session, so a parent's write binding does not override a step's read-only policy.

## Create a workflow from local or hosted MCP

Global MCP admits `create_workflow` through AgentWorks `product.yaml`. It uses the existing app workflow creator and schema: `folder_name` (kebab-case), `workflow_json` (schema version, unique ID, label), and `plan_json` (non-empty valid graph). Discover it using `get_api_spec`, invoke it with `call_tool`, and use the returned `workflow_id` for Builder and KB bindings.

The connection needs unrestricted `builder:chat` (and its companion read/run scopes), account creation rights, AgentWorks product access, and an enabled external Builder. The local Owner’s `agentworks-local` token includes these when enabled; hosted connections use their existing OAuth Builder consent. Ownership is stamped from the current authenticated user, and revoked grants are rechecked before creation. Scoped Builder/read/run connections cannot create.

After creation, inspect the project with `manage_knowledgebase_access(action=inspect_project)` and bind an authorized folder with `action=bind_project`, current `expected_manifest_version`, and a stable `request_id`. These actions check live project Owner and folder/audience permissions. Creation cannot inject folder grants or KB/project bindings. Use Builder to author/test scripted-step code before running. Existing folders and workflow IDs are never overwritten.

Fresh MCP/chat-created workflows receive the same current contract and `code_layout_version=1` defaults as UI creation, even if the client supplied legacy markers. This only applies to new workflows; existing workflows still go through the checked migration flow. Authored scripted code belongs in `code/<step-id>/main.py`.

External Builder invokes its admitted managed tools directly for API models and projects them as direct MCP bridge tools for coding CLIs. It does not depend on an HTTP shell tool to read KB bindings or edit plans. KB tools still require explicit connection scope and enforce the binding and audience on every call; a Reader binding has no update tool.
