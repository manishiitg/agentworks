[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-371 — CLI adapters overwrote and deleted a project's own instruction files; overlapping Crew/Code sessions collided

| Coordination | Value |
|---|---|
| State | merged to `main` in all three repos (2026-09-30), **not deployed** |
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

## Cursor (2026-09-30)

Cursor wrote several files into `.cursor/` (`mcp.json`, `cli.json`, `hooks.json`, the
hook script) with restore off by default, deleted a project's `cli.json` at startup,
and created a temporary `.git` marker that the structured path removed
unconditionally. Now every config write is a counted, always-restoring lease
(`projectfile.AcquireOwnedLease*`): a project's own file is restored byte-for-byte by
the last session and overlapping sessions keep each other's; a project's `cli.json` is
never deleted (a session with bridge tools replaces it with its allowlist and it is
restored afterwards); the `.git` marker is shared by sessions, removed by the last one,
and only if it is still the directory we created (a real repository is never touched).
Provider commit `667aeec`. Tests: `cursorcli_project_files_safety_test.go`.

## One managed copy of each CLI, and old Claude (2026-09-30)

With the session prompt carried only by the `AGENTS.md` block (project-instruction-only
mode), a Claude that ignores `AGENTS.md` runs every session with no system prompt and no
error. Claude Code 2.1.233 does not read `AGENTS.md`; 2.1.284 and 2.1.285 do (tested on
RTS with the service token: AGENTS.md-only and a CLAUDE.md control). RTS had both a
root-owned `/usr/bin/claude` 2.1.233 (and `pi` 0.84.2) from first provisioning and the
managed `~/.local/bin` copies kept current by the deploy and the cli-update timer. The
service PATH puts `~/.local/bin` first, so the managed copy won, but the old one was a
silent fallback (the deploy script already records it running for hours on 2026-09-25).

- RTS (2026-09-30): `sudo npm uninstall -g @anthropic-ai/claude-code
  @earendil-works/pi-coding-agent`; the service now resolves claude 2.1.285, pi 0.87.1 and
  cursor-agent only from `~/.local/bin`. The local Mac already has one copy of each CLI.
  Excellence was not reachable to check.
- Provisioning (`template.yaml`, `repair-bootstrap.sh`) no longer installs claude or pi
  system-wide.
- Deploy preflight (`build-and-activate.sh`): fails if `claude` on the service PATH is not
  the managed one, warns on any system copy, and fails if this claude does not read
  `AGENTS.md` (codeword probe; a capped account only warns).
- Adapter (provider `3036cff`): in instruction-only mode a claude older than 2.1.284, or
  whose version cannot be read, gets the prompt through `--system-prompt-file` and no
  `AGENTS.md` carrier (a duplicate at worst, never a gap).

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
- Guest call, multi-user, real Claude: reader1's Crew ("Caller") used `call_function` to
  ask owner1's Crew ("Support") `ask`. The Support turn ran as a guest with the reader
  role (its CLI pane got the `[AGENTWORKS SESSION]` block; its tool gate registered 30
  tools; the caller's pane got no block), answered from its own `AGENTS.md`, and the
  answer came back to the caller as an `[AUTO-NOTIFICATION]` result
  (`{"answer": "... OSPREY-3 ..."}`). The guest did not call `return_function_result`;
  its final answer is what is returned (the turn's own instruction says so), so the
  removed guest prompt was not needed for that path.
- Crew, multi-user (owner + reader in one Crew): owner answered from the Crew's own
  `AGENTS.md`; reader was refused a file write and offered a suggestion; the mode
  block reached only the reader's CLI, on both the first message and a live-input
  follow-up; no stored chat message or submission contains the block; the file was
  restored when both ended.
- RTS project folders (read-only scan 2026-09-29): five folders; the CLI folders
  held only our own projected skills; no user files were lost there. Excellence was
  not reachable for the same scan.

## Not done

- agy refuses a second session with a different tool mode in one folder.
- Skill cleanup is not session-counted (needed before workflows share a folder).
- Old unmarked skill folders of a provider not in use stay as clutter.
- Deploy: nothing is deployed. `mcpagent` still pins provider `68688ec` (the Cursor fix is in `667aeec`; this repo pins the newer one and builds against local copies). The `mcpagent` checkout has another session's uncommitted work, so its pin bump was left for that session to land.

## Pre-existing failures seen (not from this change)

`TestSalesCrewCatalogHasInstallableRoles`, `TestAssembledWorkflowPrompt` (24 KB
ceiling), Muse exec-lane MCP mount, a flaky Claude paste-chip pane test.
