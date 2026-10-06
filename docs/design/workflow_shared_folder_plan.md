# Workflow Run and Builder with linked project folders

Status: implemented 2026-09-30, not deployed. This replaces the proposal to run
workflow chats in their shared data folder with one shared prompt. Workflows
instead use the same linked private-runtime design as Crew.

Related: [project instruction files](project_instruction_files.md),
[PLAT-296](../bugs/pulse_platform/sandbox/access/plat-296.md), and
[PLAT-371](../bugs/pulse_platform/sandbox/general/plat-371.md).

## Layout and paths

Each conversational CLI runtime stays outside workspace documents, keyed by
user, workflow, chat, provider and mode. It holds the generated instructions,
projected skills, CLI configuration and private home. Its `project/` directory
link points to the authoritative workflow folder.

Native tools use `project/<path>`; commands with workflow-relative paths use
`cd project && ...`. Workspace bridge tools keep their real-workflow-relative
paths without this prefix. The shared prompt records this distinction and the
server appends the actual target path. A directory link supports creates,
atomic saves, renames and deletes without copying or synchronizing data.

User-owned workflow instructions and native CLI configuration stay in the real
workflow; platform projections never use them as destinations. Preparing the
link refuses an existing file, directory or different link instead of deleting
it or falling back to a shared cwd. Removing a runtime leaves its target intact.

## Modes and permissions

Keep the existing separate Builder and Run prompts and skill bundles. Builder
adds its authoring references; Run cannot gain authoring permission from project
guidance or a linked path. Access and server-maintained provenance still select
the tool surface. This change adds no mode toggle or permission promotion.

The link itself grants no access. The real workflow is read-only for a read-only
turn and writable only with Builder's authorized grants. Landlock's final Run
policy drops initial and attached-folder workspace writes, leaving the private
runtime writable. Backend workflow execution may still perform its authorized
business actions and persist outputs; that is separate from the chat CLI's
ability to edit configuration. Existing Landlock blocked-path and host-grant
limitations remain as listed in `DECISIONS.md`.

`AGENTWORKS_ISOLATE_WORKFLOW_CLI=false` remains a transitional Builder rollback.
Run always isolates: the launcher automatically grants cwd writes, so using the
real workflow as Run's cwd would promote read-only access. API models retain
their working-directory behavior. Execution steps also keep private runtimes,
with `output/` linked to the exact invocation's iteration artifact directory;
their dedicated guard supplies native workspace permissions. Step tool modes
and bridge paths remain unchanged.

## Resume and verification

Adding the link preserves existing private directory identities and compatible
native sessions. Resuming another mode/chat directory or an old shared-folder
session stays refused, including Codex's project-directory override. The shared
path guidance changes both mode definition keys so stale retained processes
reload instructions through the existing relaunch path.
Restored workflow terminals defer to fresh query admission even during the
Builder rollback, so a saved pane cannot bypass current access/mode checks.

Offline tests cover all six providers, including Agy, stable existing runtimes,
mode boundaries, separate prompt/skills, link failures, bridge path guidance and
read-only Landlock policy construction. Linux launcher tests verify linked
read/edit/create/rename/delete and denial of ungranted nested link targets.
Authenticated live CLI qualification in both modes is required before deploy.
