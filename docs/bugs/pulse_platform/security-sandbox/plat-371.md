[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-371 — CLI adapters overwrote and deleted a project's own instruction files; overlapping Crew/Code sessions collided

| Coordination | Value |
|---|---|
| State | built and tested locally on branches, **not merged, not deployed** (2026-09-30) |
| Date | 2026-09-29 |
| Owner | security-sandbox |
| Related | [PLAT-296](plat-296.md) (private folders for workflow Builder/Run), [PLAT-364](plat-364.md) (CLI confinement), [design: project instruction files](../../../design/project_instruction_files.md), [plan: workflows on the shared folder](../../../design/workflow_shared_folder_plan.md) |

## Problem

Crew and Code chats, schedules, triggers and bots all run with the project folder
as the CLI's working directory, and several sessions can be live in it at once.
Two layers wrote into that folder and cleaned up carelessly:

1. **The adapters** wrote the session prompt to `CLAUDE.md` (Claude), `AGENTS.md`
   (Codex, Muse), `.pi/APPEND_SYSTEM.md` (Pi) and `.cursor/rules/mlp-system.mdc`
   (Cursor). They overwrote a file the project already had, and deleted it when the
   first session ended. Restore was off by default and, with overlapping sessions,
   would have restored another session's file. Reproduced with the real Claude
   adapter functions: the user's `CLAUDE.md` was gone after one session.
2. **`mcpagent`'s startup cleanup** (`cleanupInactiveCodingAgentProjectArtifacts`)
   ran `RemoveAll` on `.claude`, `.cursor`, `.pi`, `.codex` and `.agents` of the
   project for every provider that was not the active one. Reproduced: starting a
   Codex turn deleted a test project's `.claude/settings.json`,
   `.claude/commands/deploy.md`, `.cursor/rules/team.mdc` and `.pi/settings.json`.
   Any Code project with committed CLI configuration was exposed, silently, on any
   turn.

Also: the prompt differs between an owner and a reader (or guest) of a Crew, so one
shared instruction file could not hold both.

## Decisions (owner, 2026-09-29/30)

- Never delete anything we did not create; delete only what we created.
- `AGENTS.md` is the single project-instructions file for Claude and Codex
  (Claude Code reads it natively; verified 2.1.284). We stop writing `CLAUDE.md`.
- A Crew has two roles, owner (Builder) and reader (Run). A guest call is a reader.
  The owner does not need a way to chat in Run mode.
- One shared prompt per folder. The reader role is a short block in front of each
  message; tools and folder guards enforce it; refusals repeat it.
- Crew and Code first, tested; workflows follow separately (plan doc).

## Built

- `multi-llm-provider-go` `pkg/projectfile` (branch `project-files-safe`): a marked
  block added to the file, a per-path session count, idempotent lease tokens, stale
  block cleanup, an owned-file lease for uniquely named files. Claude (now
  `AGENTS.md`), Codex, Muse, Pi and the Cursor rules file use it. Projected skills
  carry `.agentworks-managed`; an existing folder of the same name is taken over
  and marked so skill updates keep reaching old projects; other skills untouched.
- `mcpagent` (branch `project-files-safe`): startup and on-close cleanup remove only
  marked skills, our block, and generated config recognised by content. No folder
  is removed.
- This repo (branch `crew-code-private-cli-folder`): `crew_session_mode.go`
  (`[AGENTWORKS SESSION]` block on the normal turn and on live input, stripped from
  history, submissions and native transcripts), the reader/guest prompt removed from
  the system prompt, a short shared-prompt paragraph, and write refusals in a
  read-only session that name `submit_crew_suggestion` / `submit_workflow_suggestion`.
- Bridge refusals (2026-09-30): a call to a mutating tool a reader was never given
  (`crewReaderDeniedTools`) now returns the read-only message instead of "not found"
  (`refuseReaderDeniedTool`, both `/tools/custom` routes), and a shell command that
  fails in a read-only session with "Operation not permitted" / "Permission denied" /
  "Read-only file system" gets the same message appended to stderr
  (`withReadOnlyShellHint`). Hidden tools stay hidden from the catalog.

## Evidence

Unit tests in all three repos. Real CLIs: Claude 2.1.284 and Codex 0.159 both read
the user's own `AGENTS.md` together with our block. Server-level runs on isolated
local instances (real Claude and Codex through `/api/agent-profiles/*/query`):

- Code, single-user: user's `AGENTS.md`, `.claude/settings.json`,
  `.claude/commands/deploy.md`, `.cursor/rules/team.mdc`, `.pi/settings.json` intact
  during and after; two Claude chats plus a Codex chat overlapping; one shared block;
  after the last session ended `AGENTS.md` was byte-for-byte the original.
- Bridge refusals, live, using the reader session's own credentials: `create_project_schedule`
  and `set_workflow_secret` returned the read-only message with `submit_crew_suggestion`;
  `list_project_schedules` still worked; `echo hello > notes.md` was blocked by the
  sandbox and now says why; `ls` was unaffected; `notes.md` was not created. (The model
  itself refuses before trying, so the messages are what a CLI sees if it does try.)
- Crew, multi-user (owner + reader in one Crew): owner answered from the Crew's own
  `AGENTS.md`; reader was refused a file write and offered a suggestion; the mode
  block reached only the reader's CLI, on both the first message and a live-input
  follow-up; no stored chat message or submission contains the block; the file was
  restored when both ended.
- RTS project folders (read-only scan 2026-09-29): five folders; the CLI folders
  held only our own projected skills; no user files were lost there. Excellence was
  not reachable for the same scan.

## Not done

- Cursor still removes a project's `.cursor/cli.json` at startup and rewrites
  `hooks.json` without restoring.
- agy refuses a second session with a different tool mode in one folder.
- A real guest call (function call from another user) with the reader wording;
  the old guest prompt told the agent to call `return_function_result`.
- Skill cleanup is not session-counted (needed before workflows share a folder).
- Old unmarked skill folders of a provider not in use stay as clutter.
- Merge order: provider, then bump the `mcpagent` pin, then this repo.

## Pre-existing failures seen (not from this change)

`TestSalesCrewCatalogHasInstallableRoles`, `TestAssembledWorkflowPrompt` (24 KB
ceiling), Muse exec-lane MCP mount, a flaky Claude paste-chip pane test.
