[← platform / security-sandbox](index.md)

# PLAT-261 — Workflows cannot receive explicit access to user-selected folders outside `workspace-docs`

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | sandbox |
| Area | security |
| Summary | product capability, severity medium. |

| Coordination | Value |
|---|---|
| Assigned agent | Unassigned |
| Ticket state | `fixed` |
| Last synchronized | `2026-08-29` |

- **Priority:** product capability, severity medium.
- **Findings:** User-requested feature; no Pulse finding is linked.
- **Related:** [PLAT-244](plat-244.md) (narrow active workspace-tool
  surface); [PLAT-073](../../goals/pulse-governance/plat-073-remaining-board.md)) (FolderGuard propagation
  and bridge containment).

## Problem

A workflow can currently work only within its platform-managed
`workspace-docs` scopes, apart from narrowly managed exceptions such as
CDP host Downloads access. A user cannot explicitly attach an
existing source repository, shared-assets directory, or other host folder and
grant that workflow read-only or read-write access.

The runtime already supports absolute FolderGuard allow-list entries for shell
execution, but there is no workflow-level grant model, approval UI, Builder
tool contract, or complete runtime propagation. `additional_read_paths` is not
this feature: it is step-local, read-only, and deliberately restricted to paths
inside the current workflow.

## Proposed contract

Persist approved, host-local grants in `workflow.json`:

```json
{
  "folder_access": [
    {
      "id": "folder_abc123",
      "alias": "rts-source",
      "path": "/Users/mipl/ai-work/rts-website",
      "access": "read_write"
    }
  ]
}
```

Each grant has a stable ID, unique workflow-local alias, canonical absolute
path, and `read_only` or `read_write` access. The setting is host-local runtime
configuration; it must not be inferred to exist on another host merely because
portable workflow artifacts were restored there.

The Workflow Builder receives tools to inspect grants and request a grant. The
trusted UI lists grants and lets the user change access or remove one. An agent
must not self-authorize an arbitrary path. If the user supplies an exact
absolute path in chat, the pending request may preserve it so the user can
approve or deny that exact proposal. Otherwise the user selects a folder with
the native picker. In both cases, creating or widening a grant requires a
trusted user action and the backend canonicalizes and validates the path before
recording it.

At session creation, the platform snapshots approved grants into the existing
session FolderGuard:

- read-only folders extend `ReadPaths` and are excluded from `WritePaths`;
- read-write folders extend both;
- child group, orchestrator, sub-agent, and message-sequence sessions inherit
  the snapshot through the existing session-policy propagation;
- shell sessions receive stable alias environment variables such as
  `WORKFLOW_FOLDER_RTS_SOURCE`, and agent instructions list their access modes;
- revoking or narrowing a grant affects new turns/sessions and cannot leave a
  stale child session with broader authority.

Only the currently active agent-facing filesystem tools are in scope:

1. `execute_shell_command` receives the approved absolute paths through its
   internally attached FolderGuard. Reading, listing, creating, moving, and
   deleting remain ordinary guarded shell operations.
2. `diff_patch_workspace_file` gains a trusted external-path resolution route
   that can patch only a file beneath an approved read-write grant.

Do **not** restore the removed agent-facing `read_workspace_file`,
`list_workspace_files`, `update_workspace_file`, `move_workspace_file`, or
`delete_workspace_file` tools. Their remaining typed Go client methods are
platform-internal and are not part of this feature.

## Security and path-resolution requirements

- Agent calls identify folders by approved alias or stable grant ID; they do
  not submit a new host path as permission evidence.
- Canonicalize both the approved root and requested target, reject NUL bytes,
  `..` traversal, unknown aliases, and symlink escapes.
- A read-only grant must reject writes in both bridge validation and the OS
  sandbox. A read-write grant must not authorize a parent or sibling path.
- External diff-patch operations must use a dedicated authenticated workspace
  boundary. The existing `/api/documents/...` handlers remain confined to
  `workspace-docs`; do not weaken their global containment check.
- Never expose a grant from one workflow, user, or session to another.
- Record add, permission-change, and removal actions in the workflow changelog
  with actor and reason.

## Acceptance criteria

1. The UI can add a folder through a native picker, choose read-only or
   read-write, show its alias/path/mode, change mode, and revoke it.
2. Builder tools can inspect grants and request changes, but cannot complete a
   new or widened grant without trusted user approval.
3. `workflow.json` round-trips the grant model without affecting older files
   that omit `folder_access`.
4. A fresh Builder session and every execution/session variant receive the
   same authorized external roots, including child-session inheritance.
5. Shell can read an approved read-only folder, cannot write it, and can read
   and write an approved read-write folder.
6. `diff_patch_workspace_file` can patch a file under an approved read-write
   alias and rejects read-only, unknown, traversal, symlink-escape, sibling,
   and revoked targets.
7. Existing `workspace-docs` behavior and containment tests continue to pass.
8. No removed basic workspace file tool is registered or reintroduced.
9. Local/native, Docker, and dedicated-host behavior either works through an
   explicit host mapping or fails with a clear unsupported/unavailable grant
   state—never by silently broadening access.

## Verification plan

- Unit tests for grant JSON compatibility, alias uniqueness, canonicalization,
  traversal/symlink rejection, and read/write mode enforcement.
- Registry tests proving the active workspace tool list remains shell,
  diff-patch, text generation, and web search only.
- Session tests covering parent-to-child FolderGuard inheritance and
  revoke/narrow behavior.
- Workspace API tests proving external diff-patch requires authenticated,
  approved capability while `/api/documents/...` still rejects external paths.
- Real macOS sandbox and Linux Landlock/mount-namespace checks for read-only and
  read-write external folders.
- One end-to-end workflow run that reads an attached repository and patches a
  disposable file after explicit approval.

## Decision history

- **2026-08-29:** Chose `workflow.json` as the workflow-local source of grant
  metadata and the existing session FolderGuard as the runtime enforcement
  channel. Chose trusted UI approval over agent-authored absolute-path grants.
- **2026-08-29:** Narrowed agent filesystem integration to
  `execute_shell_command` and `diff_patch_workspace_file`; explicitly rejected
  restoring the five basic file tools removed on 2026-08-05.
- **2026-08-29:** Implemented `folder_access` persistence and validation, the
  desktop native picker and Attached folders UI, owner-approved read-only vs
  read-write grants, Builder inspection/request guidance, FolderGuard and
  alias-environment propagation across Builder/execution/child sessions, live
  per-workflow revocation/narrowing, and a server-token-protected external
  diff-patch boundary with traversal and symlink containment. No basic file
  tool was restored.
- **2026-08-29 follow-up audit:** Added the explicit `BlockedWritePaths`
  projection required for read-only host grants, including live
  read-only/read-write transitions. Added attached-folder aliases to both the
  scripted authoring contract and direct scripted runtime environment. The
  macOS sandbox now grants each approved directory node as well as its
  descendants, so listing a selected root and creating a direct child in a
  read-write root both work.
- **2026-08-29 discoverability follow-up:** The Workflow Builder identity now
  explains the owner-approved attachment flow even when the workflow has zero
  grants. Previously the section was conditional on a grant already existing,
  hiding `request_workflow_folder_access` at exactly the point it was needed.
- **2026-08-29 pending-request follow-up:** The request tool now persists a
  proposal containing alias, access mode, reason, time, and—only when explicitly
  supplied by the user—the exact proposed absolute path. A request with a path
  presents Approve / Deny; a path-free request presents Choose folder / Deny.
  Neither form grants access: the trusted UI action does, after backend
  canonicalization and validation. The agent cannot invent a path or
  self-approve.
- **2026-08-29 live-session follow-up:** Approval now reconciles legacy active
  Builder sessions even when they predate the explicit workflow-ownership tag.
  `get_workflow_config` also refreshes the calling session from the current
  manifest, so an approved grant cannot appear in configuration while the same
  Builder shell retains stale roots.
- **2026-08-29 restored-session follow-up:** A real restored Workflow Builder
  reproduced an additional reset path: generic workflow-phase setup rebuilt the
  session FolderGuard after startup without replaying `folder_access`. The
  approved grant remained visible in `workflow.json`, but shell resolution saw
  only the workflow and Downloads roots. Both workflow-phase initialization
  points now reapply durable grants immediately after rebuilding the base
  guard, so restoration no longer depends on a later configuration-tool call.

## Verification receipt

- Frontend TypeScript and production Vite build pass, including the bundle
  budget check.
- Manifest tests cover valid grants, root rejection, environment-key alias
  collisions, host canonicalization, and stable creation timestamps.
- Runtime tests cover read-only/read-write projection, missing-host omission,
  stable alias environment variables, workflow-scoped live revocation, and
  live read-only-to-read-write widening without a stale write deny.
- A zero-grant Builder prompt test requires the request tool, approval location,
  and truthful "not attached yet" state to remain discoverable.
- Pending-request tests cover persistence, duplicate suppression/update,
  manifest validation, and exact-path proposals that remain inert until user
  approval.
- Live-session tests cover legacy session adoption plus immediate add, narrow,
  and revoke reconciliation for read/write roots and alias environment values.
- A restored-Builder regression test resets a session to its base workflow
  guard, reapplies the manifest, and requires both the read-write host root and
  `WORKFLOW_FOLDER_<ALIAS>` environment value to be restored.
- Workspace tests cover server environment filtering, approved external diff
  application, sibling denial, blocked-write denial, linked-alias traversal
  rejection, and symlink-escape denial.
- A real macOS sandbox test covers directory listing and direct child creation
  for an explicitly authorized root; the existing external read-only sandbox
  test proves reads succeed and writes remain denied.
- Linux coverage now includes a Landlock policy test proving that an external
  read-only root receives no write grant while an external read-write root
  receives both grants, plus a Landlock runtime test that reads the former,
  rejects a write to it, and writes successfully to the latter. The Linux
  security test binary cross-compiles for both amd64 and arm64; the runtime
  enforcement test executes on a Linux host with Landlock and its launcher.
