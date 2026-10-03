[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-390 — Hybrid native-tools mode removed: mcp_only or full, one prompt per CLI per mode

| Coordination | Value |
|---|---|
| State | on `main` (provider `427b14e`, mcpagent `51acb82`, builder this commit); not deployed; owner's final testing pending |
| Date | 2026-10-03 |
| Owner | coding-agent-bridge |
| Related | PLAT-364 (confinement), PLAT-385 (blocked paths in the sandbox) |

## Decision (owner, 2026-10-03)

"Native agent tools" has two settings: off = `mcp_only`, on = `full` — the
CLI's own tools inside a sandbox (Landlock on Linux, Seatbelt on a Mac; on a
Mac other CLIs run `full_unconfined` until each gets Seatbelt). If the CLI
cannot be confined it runs `mcp_only`. The reads-only `hybrid` middle state is
gone; a stored `hybrid` reads as `full`.

## Per CLI (full)

| CLI | Full mode |
|---|---|
| Claude | Bash/Read/Write/Edit/Grep/Glob/skills/todos/subagents/web; no permission prompts |
| Codex | shell + subagents in its `workspace-write` sandbox |
| Cursor | new: its own Shell/Read/Edit/Write/Delete; shell approved by the hook (never `--force`); subagents, cloud/background agents, computer use denied |
| Muse | new: no tool allowlist, so no hook and no `--disable-shell/--disable-write` |
| Agy | full gate as before; hybrid read gate removed |
| Pi | bridge-only in every mode (no confined full mode yet) |

## Prompts

mcpagent's routing preamble now says, per CLI and mode, what is really on, the
sandbox boundary, that protected files are refused, and when to still use the
bridge (platform actions, integrations, the workflow DB, notifications).
`TestCodingAgentModesContract` checks prompt vs launch options for every CLI in
both modes. The builder's "CLI Tool Environment" section now follows the mode
the chat really starts with, for workflow chats too (before, workflow chats on
native tools were told their native tools were disabled).

## Done / left

- Done: unit and contract tests; provider/mcpagent/builder suites match their
  main baselines (pre-existing failures only).
- Left: live P0 per CLI on the isolated test server (Codex, Cursor, Muse full
  modes are new); the owner's final testing; Seatbelt for the other CLIs and
  removing `full_unconfined` (step 3).
