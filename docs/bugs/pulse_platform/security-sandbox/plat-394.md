[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-394 — Seatbelt for every coding CLI on a Mac; `full_unconfined` removed

| Coordination | Value |
|---|---|
| State | on `main` (provider `f7e9150`, mcpagent `fa16dd7`, builder this commit); not deployed (Mac-only change: servers are unaffected); owner's final testing pending |
| Date | 2026-10-03 |
| Owner | security-sandbox |
| Related | PLAT-364 (confinement), PLAT-385 (blocked paths), PLAT-390 (two modes) |

## Decision (owner, 2026-10-03)

On a person's own Mac every coding CLI runs Full CLI inside Seatbelt. The
person's home stays open: their settings, logins, terminal config and other
projects. Only AgentWorks' protected files are refused, plus the ways out of
the sandbox. Nothing runs unconfined any more: a Mac without `sandbox-exec`
(or a multi-user Mac) runs bridge-only, like a server without its lock.

## The Mac profile

| | |
|---|---|
| Open | everything the person can use, including their whole home |
| Closed | AgentWorks' workspace data (`workspace-docs`: other workflows, users, config) except this chat's folder grants (`ProtectedRoots`) |
| Refused inside grants | the folder guard's blocked paths (`planning/`, the raw database, ...) |
| Blocked | `open`, `osascript`, `osacompile`, `automator`, `shortcuts`, Apple Events, LaunchServices |

## Per CLI

| CLI | Under Seatbelt |
|---|---|
| Claude | as before; its special config grants are gone (the home is open) |
| Codex | its own sandbox off (`danger-full-access`): macOS refuses a sandbox inside a sandbox |
| Cursor | its own sandbox off (`--sandbox disabled`), same reason |
| Muse | `--yolo` already turns its sandbox off (PLAT-390) |
| Agy | full mode needs a confined launch; `full_unconfined` removed |
| Pi | wrapped too; bridge-only as before |

## Done

- One profile through the shared `LandlockArgs`/`LandlockCmd` every adapter
  calls; strict Codex accepts a launch with no MCP config.
- `full_unconfined` removed in all three repos; a stored value reads as `full`.
- On a Mac the prompt says what the sandbox does (home open, AgentWorks data
  outside the chat, protected files and opening/scripting apps refused).
- The Code terminal is unchanged: it runs as the person on their own Mac.

## Verified (macOS, live)

- Real sandbox unit test: home files read/write; another workflow, a blocked
  path, `open` and `osascript` refused.
- `TestClaudeCodeTmuxRealSeatbeltFullCLIP0`: Claude writes its folder and
  reads a personal file; another workflow (read and write), a blocked
  `planning/plan.json` and `osascript` fail with "operation not permitted".
- Codex (native tools and subagent), Cursor, Muse (tmux and structured)
  full-mode live tests pass under Seatbelt; each checks the CLI started inside it.

## Left

- Agy not run live: not logged in on this Mac.
- The owner's final testing in the local app.
