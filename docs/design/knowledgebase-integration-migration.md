# Shared Knowledge Base: workflow/Crew integration and migration

Status: MVP coexistence and merge rollout decision, plus a proposed explicit
migration contract. The automatic importer, new manifest bindings, and cutover
adapter described below are not implemented in this PR. Deployment must not
claim that existing workflows or Crews have migrated.

## What ships at merge

The shared Knowledge Base is available through five MCP tools. Work/Code
profiles and the workflow runtime register these tools. Calls use the trusted
execution identity and enforce current product access, folder grants, and
connection caps. The access builder manages grants; content connections inspect
access but cannot change it. Git publication remains explicit.

Existing storage continues to operate:

| Store | Existing contract | MVP behavior |
| --- | --- | --- |
| Workflow `knowledgebase/context/` and `knowledgebase/notes/` | Local context, contributions, topic files, and `_index.json` | Remains local; existing writers keep their current contract |
| Workflow `knowledgebase_sources` | Workflow IDs/aliases resolved by `pkg/workflowkb`; manifest audience checks and `WORKFLOW_KB_<ALIAS>` | Keeps existing resolution and access rules |
| Crew workspace attachments | Authorized Crew project bindings and read-only workspace paths | Keeps existing resolution; does not become a KB-folder grant |
| Workflow `learnings/` | Local procedural learning | Remains local; excluded from initial migration |
| Shared Knowledge Base product | Folder ACLs, versioned Markdown entries, MCP-only writes | Explicitly selected by agents/users; never mounted as a host path |

Product availability alone is not automatic adoption. An interactive call runs
as its authenticated user. A scheduled workflow/Crew run uses its server-owned
execution identity; it must not inherit the deployer's or administrator's access.
The current tools authorize that identity, not a manifest's complete audience.
Before introducing automatic shared-KB bindings for runs whose outputs are
visible to other users, enforce the audience contract below. Do not describe
current per-principal MCP access as equivalent to legacy attachment sharing.

## Integration contract for explicit adoption

Persist a binding to the immutable KB folder ID, an alias, and intended read or
write use in the workflow/Crew configuration. A display path can accompany the
ID; it is not authority. This is a proposed configuration extension, not a
currently accepted `knowledgebase_sources` JSON form.

- A binding selects one backing store. Legacy aliases keep the existing path
  resolver. Shared bindings resolve to an MCP folder scope, not a filesystem
  directory or `WORKFLOW_KB_*` mount. Reject ambiguous duplicate aliases.
- Agent context states which store each alias uses. Shared reads, searches,
  contributions, and updates call the five MCP tools. Existing legacy
  contributions remain local until their writer is explicitly cut over.
- Check the workflow/Crew owner and every user who can view its output against
  the bound folder's live Reader grants before admitting a shared read. An
  unresolved identity, public audience, or insufficient grant fails closed.
  A caller's own permission does not authorize sharing the result with others.
- A dedicated service identity for unattended execution gets an explicit grant
  and token cap for the bound folder. Its grant does not replace the audience
  check. Recheck bindings, user grants, account status, and caps during calls.
- Shared writes require Editor for the execution identity, explicit owner
  consent to change that folder, and the same audience constraints. Legacy
  `AllowedKBWriters` never automatically becomes Editor: Editor includes create,
  delete, and backup authority in addition to patching content.
- Revoking grants or changing workflow/Crew audience invalidates access without
  needing to edit stored bindings. No administrator fallback, directory mount,
  silent legacy fallback after shared cutover, or automatic dual-write.

## Explicit migration procedure

Use a per-workflow/Crew migration with preview and deliberate cutover as the
default. Merge/deployment does not run it automatically. A future deployment
job can invoke the same migration contract for an approved inventory.

1. Inventory sources, canonical workspace IDs, attachment aliases, owners,
   readers, consumers, scheduled identities, and current writers. Snapshot
   manifests and source content. Refuse unresolved or ambiguous identities.
2. Preview a mapping into `workflows/<stable-id>/context` and `/notes`, or
   `crews/<stable-project-id>/knowledge`. These are organization-relative KB
   paths. Confirm every segment meets KB naming rules and resolve collisions
   explicitly; never silently rename or flatten files.
3. Import Markdown only, using MCP `update_knowledgebase` actions and stable
   request IDs. Preserve hierarchy and normalized text. Preview unsupported
   JSON indexes, binary files, symlinks, invalid filenames, and oversize files.
   `_index.json` is not a KB entry: retain it in the legacy snapshot and map its
   topic/title information to entry metadata where explicitly approved.
4. Establish reviewed grants through the access builder. Source owners become
   folder Owners, source readers and approved consumer audiences become Readers.
   Migrating existing shared access must not silently broaden a source's
   audience. Explicitly grant the migration/execution identity the required
   access; legacy write permission defaults to Reader pending a new Editor grant.
5. Record an external migration receipt containing the source hashes, stable
   identity/alias mapping, destination entry IDs/versions, newly added grants,
   original configuration version, and migration state. Retry resumes that
   receipt; never overwrite destination edits or repeat grants blindly.
6. Pause legacy writers for final verification. Compare imported content hashes
   after the normal line-ending normalization, and verify both allowed and
   denied reads with the intended execution identity. Keep original files in
   place. Do not claim a fallback is read-only unless runtime writes are disabled.
7. Cut over the owning workflow/Crew first, then consumers after both endpoints
   pass verification. Atomically update the configuration under its version
   check and existing contract-ladder mechanism. Shared bindings do not satisfy
   old shell-path readers: migrate those readers/writers in the same cutover.
8. Verify an interactive run and an unattended run, immediate visibility after
   save, revoked access, and no writes to the legacy source. Git backup, if
   requested, is a separate selected-version commit/push operation.

Rollback restores the previous configuration only under a version check and
pauses shared writers first. Keep imported content by default. Delete imported
entries or revoke migration-created grants only after verifying no later edits,
new consumers, or independent grants rely on them. Recursive folder deletion is
not supported by MVP; never promise rollback by deleting an entire folder.

## Merge and rollout gates

Before merge: deleted-path/security regressions pass; legacy attachment and
workflow/Crew tests pass; prompts identify both stores; deployment variables and
backup recovery are documented. No existing manifest is rewritten by the PR.

After deployment: check the product with explicitly granted test identities,
including a narrowed read token and revoked write token. Keep existing workflows
and Crews on legacy storage until the binding adapter/importer and audience
checks above ship. Pick an owner-approved pilot with few consumers, verify the
preview, then migrate consumers incrementally. Production IDs and workspaces
must come from the installation inventory, not assumptions in a review comment.

Acceptance for the migration follow-up includes a workflow and a Crew reading
the same shared folder, a migrated notes consumer, scheduled execution with a
scoped identity, audience-change denial, revoke-before-push denial, interrupted
import retry, conflict on concurrent edits, and rollback preserving later edits.
