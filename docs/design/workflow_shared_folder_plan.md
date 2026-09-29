# Workflows on the shared folder (plan, not built)

Status: planned 2026-09-30. Crew and Code are built first and tested (see
`project_instruction_files_safe`: one shared prompt per folder, the reader role
sent as an `[AGENTWORKS SESSION]` block in front of each message, and a project's
own AGENTS.md / .claude / .cursor / .pi never overwritten or deleted). Workflows
follow only after that has run for a while.

Consolidated behavior: [project instruction files](project_instruction_files.md); ticket [PLAT-371](../bugs/pulse_platform/security-sandbox/plat-371.md).

## Why

Workflow Builder and Run chats run in a private per-session runtime folder
(`cliruntime.Prepare`, PLAT-296) because Builder and Run need different prompts and
skills. With native tools becoming the main way agents work, a working directory
outside the workflow folder costs too much: relative paths and shell commands go to
the wrong place, and files written with a relative path are silently misplaced.

## Decisions

1. One shared prompt per folder: `workflow-shared.md` goes in the instruction file,
   identical for Builder and Run. The mode text (Builder ~1.4 KB, Run ~3.6 KB) goes in
   the session block in front of each message, normal turn and live input. Run's block
   is cut to about 1 KB; the rest moves into a skill loaded on demand.
2. Native CLI session key = (session id, mode). A Builder/Run switch inside one chat
   tab never shares a CLI conversation across modes; the visible chat history stays
   per session id. Today a mode change already refuses to resume the earlier native
   session (`modeChangedThisTurn`); the private folder's identity
   (user, workflow, session, provider, mode) also separated them. Without the private
   folder the session handle must carry the mode.
3. Generated files must stay out of workflow data. `AGENTS.md` blocks, `.claude/skills`,
   `.cursor`, `.pi`, `.codex`, `.agents` in a workflow folder are excluded from: backup
   hashing (`shouldSkipBackupHashFile`), git versioning (`workspace_git.go`), workspace
   listings and search (`workflowfiles`, `handlers/documents.go`), publish and
   remote-workflow sync, and the hidden-folder lists (`code_admin.go`,
   `crew_directory.go`). Search for every enumerator before switching the cwd.
4. Skills are counted per session like the instruction file, so one mode ending never
   deletes a skill the other mode's running session uses (Run's skills are a subset of
   Builder's: system-tools, builder-reference, workflow-ui-control; Builder adds
   workflow-commands and ui-ux-pro-max).
5. Switch the working directory: remove the `cliruntime.Prepare` call for workflow chats
   and schedules and the "private runtime directory" prompt line. Keep
   `AGENTWORKS_ISOLATE_WORKFLOW_CLI` as a one-release rollback.
6. Migration: existing Builder chats resume from the private folder, so the resume check
   refuses them once and they start a fresh CLI session with history replayed.

## Tests

Listing and backup exclusions; two overlapping sessions in one workflow folder; a real
Claude turn as Builder and as Run on a test workflow; a project's own files surviving.

## Not deploying

Nothing here is deployed. Ask per host before any deploy.
