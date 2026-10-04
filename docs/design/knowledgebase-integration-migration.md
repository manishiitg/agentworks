# Shared Knowledge Base: workflow/Crew integration and migration

Status: implemented in Knowledge Base MVP PR #268. Adoption is explicit per
project. Merge and deployment do not import files, add grants, or rewrite
existing projects.

## Project bindings

Workflow and Crew runtime `workflow.json` manifests accept:

```json
{
  "shared_knowledgebase": [
    {"alias": "payments", "folder_id": "folder_<immutable-id>", "access": "read"}
  ]
}
```

A binding restricts existing authority; it never grants access. Aliases are
unique across shared bindings, legacy `knowledgebase_sources`, and Crew workspace
attachments. Up to 20 bindings are supported. Access is `read` or `write`.
Bindings do not mount a host directory or export a `WORKFLOW_KB_<ALIAS>` path.

The access builder's existing `manage_knowledgebase_access` tool has three
additional actions:

- `inspect_project(workspace_path)` returns current bindings, output audience,
  and `manifest_version`.
- `bind_project(workspace_path, alias, folder_id, access,
  expected_manifest_version, request_id)` checks project ownership, folder
  authority, and audience access before saving. To deliberately replace a
  legacy knowledge source alias, set `replace_legacy_alias=true`. It cannot
  replace a Crew workspace attachment.
- `unbind_project(workspace_path, alias, expected_manifest_version, request_id)`
  removes a binding. Roll back migration before removing its last binding.

Configuration mutations are serialized, use manifest CAS, and record a private
intent before writing. A retry after an uncertain response returns the original
result when the resulting manifest matches. A later unrelated manifest change
causes a conflict. Ordinary workflow and Crew manifest rewrites preserve these
server-managed fields. Retained native sessions include knowledge configuration
in their policy key and relaunch when their scope changes.

## Runtime authorization

Workflow/Crew tools derive their project from trusted session configuration,
never a caller-supplied execution identity. Calls require:

1. Current product access, active execution identity, folder ACLs, and token caps.
2. A configured shared binding covering the requested folder or entry.
3. Actual Reader grants for every output audience member. Workflow audiences
   include owners, editors, and readers. Private Crews use their owner; when
   installation-wide project sharing is enabled, every enabled Work product
   user is included. Unclaimed or unresolved projects fail closed.
4. Editor authority and a write binding for writes. A read-only Crew/session
   or workflow step narrows the binding to read; a `none` step denies it.

Administrator status does not substitute for an audience member's explicit
folder grant. Audience, bindings, account status, and permissions are checked
again during calls. Service identities need their own grants and caps; they do
not replace output-reader checks. Connections outside a managed project still
use their normal authenticated identity, folder grants, and token caps.

Use `binding_alias` on the existing five MCP tools to select a bound folder.
Folder-scoped operations default to the sole binding; multiple bindings require
an alias or an explicit scope. Entry IDs and backup receipts are also checked
against the binding. A Crew save is immediately readable by an authorized
workflow or user before Git commit/push.

## Legacy coexistence and cutover

| Store | Behavior |
| --- | --- |
| Local workflow/Crew `knowledgebase/` | Continues until deliberate migration cutover |
| Legacy workflow `knowledgebase_sources` | Continues for unmigrated sources; migrated sources become unavailable until the consumer owner replaces the alias with a shared binding |
| Crew workspace attachments | Retain their separate read-only workspace contract |
| `learnings/` | Remains local and is excluded from import |
| Shared Knowledge Base | MCP reads/writes, live grants, explicit selected-version Git backup |

After cutover, `knowledgebase_mode` is `shared`. Workflow/Crew prompts direct
agents to the shared MCP tools. Session file/shell guards deny the local
knowledge archive, legacy source mounts are unavailable, and external file and
knowledge readers hide the archive. Local reorganize/consolidate agents refuse
maintenance and direct callers to MCP. There is no dual-write or legacy fallback.
The original files stay in place for rollback; shared content is never mounted.

## Explicit migration through MCP

Migration uses new actions on `update_knowledgebase`; the public surface remains
five tool names. Every action requires the exact `workspace_path` and a stable
`request_id`. Use distinct IDs for each action and identical arguments for retries.

1. Establish grants through the access builder first. Every source owner must
   have an actual Owner grant on the destination, and every source output reader
   must have Reader. The importer never grants permissions. Legacy writers do
   not automatically receive Editor.
2. `migration_preview`: supply `folder_id`, unique `alias`, and final `access`.
   The destination must be empty, the caller must own the source project and
   have Editor access to import, and all audience grants must pass. The preview
   returns `migration_id`, source inventory/hash, skipped files, required
   owners/readers, and legacy workflow consumer aliases that need rebinding.
3. Review the preview. `migration_import`: supply `migration_id`; explicitly
   set `allow_skipped_files=true` only after reviewing omissions. Markdown is
   imported through the normal MCP mutation boundary, preserving hierarchy and
   normalized text. Entries use type `note` and filename-derived titles.
   Existing destination edits are never overwritten.
4. Pause all writers, enabled schedules/triggers, and project executions.
   Adapt authored scripts that use local knowledge paths to MCP. Cutover refuses
   tracked active executions, enabled schedules/triggers, and detected legacy
   path references in Python/shell/JavaScript/TypeScript under code/planning.
   The static script check is a guard, not a complete script converter; owners
   must verify their actual execution paths before approval.
5. `migration_cutover`: supply `migration_id`. It verifies the original manifest
   version, source inventory, imported entry versions/content, folder grants,
   and audience. It checkpoints intent and atomically saves the binding and
   shared mode. The opt-in `shared-kb-v1` entry lives in
   `knowledgebase_contract_history`; it does not force a global workflow
   contract upgrade on projects that remain on legacy storage.
6. Explicitly rebind approved consumer aliases with their own owner and audience
   checks. Old aliases fail closed after their source cuts over; they never
   fall back to a stale local snapshot. Import does not grant consumer access.
7. Verify an interactive and scheduled pilot, read-only scope, revocation, and
   immediate visibility. Git backup is a separate selected-version commit/push.

The importer reads only an owned project's canonical local `knowledgebase/`.
It refuses symlink traversal, inventories skipped symbolic links, unsupported
names/formats, JSON indexes, binary/invalid UTF-8 content, and files over 10 MiB.
Limits are 10,000 inventory paths and 100 MiB of scanned content; split larger
sources before migration. `_index.json` remains in the archive and is not
converted to metadata. No arbitrary host source path is accepted.

Scoped external tokens additionally need source-project authority: workflows
require `files:read`, a matching workflow cap, and `workflows:read` or
`runs:execute`; cutover/rollback also require builder access. Crews require
`crews:read` and a matching Crew cap, plus `crews:write` for cutover/rollback.
Knowledge Base scopes and caps still apply. Project binding mutations remain
exclusive to the app's access builder.

Private receipts record source hashes, destination entry IDs/versions,
configuration versions, and migration state. Interrupted imports resume using
stable internal request IDs. The content deduplication window is seven days;
an uncheckpointed mutation older than that fails safely on a name conflict
rather than overwriting it. Integration request IDs share the normal public
mutation namespace, so reuse with different arguments is rejected.

## Rollback and rollout

`migration_rollback(workspace_path, migration_id, request_id)` restores the
previous knowledge configuration under a manifest version check. Stop active
project executions first. Later manifest changes require owner inspection;
rollback does not overwrite them. Imported entries, later content edits, grants,
and original files are retained. Removing imported content or grants is a
separate deliberate operation.

Deployment enables the product and leaves existing projects unchanged. Start
with an owner-approved pilot and coordinate its consumers before cutover. This
PR includes fixture-based workflow/Crew, ACL/audience, live-read, retry,
interrupted-import, conflict, symlink, legacy-source, and rollback tests. It does
not execute paid model runs or migrate a production workspace during development.
