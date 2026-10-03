[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-390 — Hybrid native-tools mode removed: mcp_only or full, one prompt per CLI per mode

| Coordination | Value |
|---|---|
| State | on `main` (provider `427b14e`→`7c1966d`, mcpagent `51acb82`→`d73145b`, builder `f0a735dca`); partly deployed (Excellence); owner's final testing pending |
| Date | 2026-10-03 |
| Owner | coding-agent-bridge |
| Related | PLAT-364 (confinement), PLAT-385 (blocked paths in the sandbox) |

## Decision (owner, 2026-10-03)

"Native agent tools" has two settings: off = `mcp_only`, on = `full` — the
CLI's own tools inside a sandbox (Landlock on Linux, Seatbelt on a Mac for
every CLI since PLAT-394; `full_unconfined` is gone). If the CLI
cannot be confined it runs `mcp_only`. The reads-only `hybrid` middle state is
gone; a stored `hybrid` reads as `full`.

## Per CLI (full)

| CLI | Full mode |
|---|---|
| Claude | Bash/Read/Write/Edit/Grep/Glob/skills/todos/subagents/web; no permission prompts |
| Codex | shell + subagents in its `workspace-write` sandbox |
| Cursor | new: its own Shell/Read/Edit/Write, pre-approved in `.cursor/cli.json` (`Shell(*)`, `Read(**)`, `Write(**)`) plus a shell-allow hook script (never `--force`); its Delete tool (always asks), subagents, cloud/background agents and computer use denied; deletes go through the shell |
| Muse | new: no tool allowlist and `--yolo` (no Muse approvals or sandbox; its Bubblewrap probe cannot run inside the lock and kept the TUI from settling, Excellence 2026-10-03); interactive launches now also pass `--reasoning-effort` |
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
- Live (macOS, isolated test server): P0 gate passes for Codex and Claude;
  Muse and Cursor gates pass except one flaky Muse formatting test (passes
  alone) and one Cursor test that also fails on the provider before this
  change. Full-mode live tests pass: `TestCodexCLIRealNativeToolsP0`,
  `TestCodexCLIRealNativeToolsSubagentP0`, `TestCursorCLIRealFullNativeP0`,
  `TestMuseCLIRealFullNative` (tmux and structured).
- Regressions found and fixed: Muse full mode never settled on Linux (fixed
  with `--yolo`, `fcc28ff`); Cursor full mode stopped on its own permission
  prompts (fixed `7c1966d`).
- Left: Linux proof of the Muse and Cursor fixes on a server; the owner's final
  testing; Seatbelt for the other CLIs and removing `full_unconfined`: done in PLAT-394.
