[← integrations / skills](index.md)

# PLAT-581: Skills belong to each workspace, with step usage and scoped uninstall

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | integrations |
| Area | skills |
| Summary | Remove the shared skills library; discover and manage skills in their owning workspace, including workflow step attachments. |

## What happened

The Skills panel read an account-wide store and filtered it to main-chat
selections. Workspace/private CLI installs were invisible, and workflow step
attachments were not included. Installation and lookup could cross workspace
boundaries through the shared library. The owner asked to remove that concept,
keep each skill in its owning workspace, and support scoped uninstall.

## Fix

- One shared panel/API inventories the active workspace's `skills/`, `.skills/`
  and provider skill folders. Rows show main-chat and per-step attachments;
  attachment selection remains independent for each agent.
- API, main chat and step runtime lookup have no account-wide fallback.
  Agent installs/imports are workspace scoped in every product. Remove the
  library picker/groups, unscoped update routes and startup library installer.
  Authoring prompts use the current workspace, with no ambient library
  read/write grant.
- A one-time migration copies saved main-chat and step references from old
  stores into that workspace, preserving supporting/binary files. Legacy
  files remain only as migration input for workspaces not yet opened; they are
  not listed or used directly. Uninstall cannot resurrect them.
- Uninstall removes owned copies/provider projections and clears that
  workspace's manifest and step configuration references. It reports affected
  agents before confirmation, protects platform-managed skills, and preserves
  other workspaces. Missing attachments can be removed too.
- Service-token-only inventory/import/read/delete routes refuse links and
  escapes. Imports stage their files, binary assets survive HTTP, and runtime
  supporting-file reads are constrained to the resolved skill folder.

## Verification

- Live isolated workspace server, called by the actual runtime loader and
  `read_skill` resolver: workspace/native attachments, binary migration,
  supporting-file reads, uninstall/reference cleanup and workspace isolation.
- Real HTTP/filesystem lifecycle and link/managed-skill regression checks.
- Focused runtime, scoped API/Code tool, Crew creation and authoring checks.
- All backend packages compile; frontend build-mode type checks and 14 panel/shared
  contract checks pass. Modified frontend lint has no errors.

## Review follow-up (2026-10-06)

- A legacy skill holding a symlink made the migration return an error, which failed every skill call for that
  workspace (list, files, the runtime's loading) on every call, because the marker was never written; the runtime
  turned the error into "no files" without logging, so steps silently ran without their skills. Now that skill is
  skipped and logged, the rest migrate and the marker is written. Pinned by
  `TestWorkspaceSkillMigrationSkipsALinkedLegacySkill`. The runtime now logs when a skill's files cannot load.
- Not changed: the marker `.workspace-skills-v1` sits in each workspace root (shows in file trees), and a reader
  viewing a workflow's skills can trigger the copy-in (a server-side write, idempotent).
- Shared skills across workspaces now come from Brain: [PLAT-576](../../brain/skills/plat-576.md).

## Left

Deployment has not been run. Backend and workspace services must be restarted
or redeployed together for the scoped service endpoints.
