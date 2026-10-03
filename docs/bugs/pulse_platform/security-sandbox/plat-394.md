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
| Closed | AgentWorks' workspace data (`workspace-docs`: other workflows, users, config) and the app's own folder (`~/Library/Application Support/AgentWorks`: every chat's CLI runtime, `state/auth`, `personal-mcp`, chat event databases, `config.json` with the server token), except this chat's folder grants and its own runtime (`ProtectedRoots`) |
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

## Found in owner testing (2026-10-03)

- The app folder was open: a chat's working folder is its runtime under
  `state/cli-runtimes/v1/`, and `..` (other chats' runtimes, logins, the token
  in `config.json`) was readable and writable. Closed with `cliSeatbeltProtectedRoots`;
  checked with the real profile on the owner's folders (own runtime works;
  another runtime, `config.json`, `state/auth` and writes beside the runtimes refused).
- Codex showed "Trust this folder?": the folder was pre-trusted in the sandbox's
  private `.codex`, not the person's `~/.codex` (provider `b7839e8`).

## Sandbox contract (the owner's self-test, automated)

Two layers, both in P0 (`scripts/run-coding-cli-p0.sh`, once per CLI):

- mcpagent `TestCLISandboxContract` (`RUN_CLI_SANDBOX_CONTRACT=1`): every CLI
  through the real Full CLI launch options in the Builder layout; verdicts from
  the disk and marker tokens, not the model's report.
- `agent_go test cli-sandbox-contract --provider X` (this repo): a real Builder
  chat on a real workflow with another workflow attached, through a running
  server, so the server's own grants are tested (PLAT-395's blanket read and the
  open app folder only showed up here). Run it on the host of the server's
  workspace-docs; Pi is the bridge-only shape. The shell actions run from one
  harness-written script because Codex declined to attempt items one by one.

It found, in one run each: Claude refusing edits through `project/`, Cursor's
trust screen and approval stops (`--trust`, `--add-dir`, `--force`; `--force`
keeps hooks), Agy's stale `statusLine`, and Codex loading the person's own MCP
servers. All six CLIs pass both layers on macOS (provider `fc8c84e`, mcpagent `83276c0`).

## Contract updates (2026-10-04)

- A third command, `agent_go test cli-step-contract`, covers workflow steps (an agent
  step and a scripted step); the measured permission map is PLAT-419.
- The chat contract also runs the harness script directly under the Seatbelt profile
  the server wrote for the chat (a Mac), so the server-computed grants are tested
  without depending on a model's willingness; the model-driven part covers trust
  screens, approvals and the CLI's own edit tool, retried up to three times (Muse
  reads the script and cites the project's rules, then declines).
- Server tests need their own `AGENTWORKS_STATE_ROOT` (`--state-root`) and must be
  stopped by port: a test server that shared the real state folder exposed the
  owner's personal MCP connections to chats (PLAT-418).

## Left

- Agy not run live: not logged in on this Mac.
- The owner's final testing in the local app.
