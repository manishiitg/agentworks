[← platform / coding-agent-bridge](index.md)

# PLAT-504 — "Selected model is at capacity" (Codex) was only visible in the terminal, not in the chat

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | fixed on main: a failed Codex turn is reported from structured records (rollout `codex_error_info`, `turn.failed`). |

| Coordination | Value |
|---|---|
| State | fixed on main (provider `63976ce`, pinned in the builder); needs a restart |
| Date | 2026-10-05 |
| Owner | coding-agent-bridge |

## Source

Owner, Upwork chat on Codex: the terminal showed "■ Selected model is at capacity. Please try a different model." The chat area showed nothing. A stored-events check of the chat
found no event containing that text.

## What Codex records (structured, real examples from 2026-10-05 10:30 and 10:31)

Rollout file: `event_msg` `task_complete` with `last_agent_message: null` and `error: {"message": "Selected model is at capacity. Please try a different model.", "codex_error_info": "server_overloaded"}`.
`codex exec --json` stdout: `{"type":"turn.failed","error":{"message":...}}` (preceded by `{"type":"error","message":...}`); the message can be a JSON-serialized API error.

## Cause

- Interactive (tmux) path: the rollout's turn error was read only when the reply text was empty, so any pane text (the error banner itself) hid it.
- Structured path (workflow steps): neither the stdout `turn.failed` nor the rollout error was read; the step failed as `codex run failed: exit status 1: Reading additional input from stdin...`.

## Done

- `CodexTurnError` (message + `codex_error_info`); `Capacity()` is true for `server_overloaded`; its text is "codex-cli model at capacity: ... (try again shortly or pick a different model)".
- Interactive: the rollout's error decides, whatever the pane says (no terminal text matching). Structured: `turn.failed` on stdout (else the `error` event), plus the rollout's code
  found by the run's own thread id. Structured records only.
- Checked live: a real Codex failure on the structured transport (unsupported model) now returns a `CodexTurnError` with the provider's reason. One unit test on the real capacity record.

## Left

- Not seen live: an actual "at capacity" turn on either path (cannot be provoked); the interactive path was checked by code and the record shape only.
- No retry or capacity-wait handling (PLAT-101 style) for `server_overloaded`: it is reported as a failure with a clear reason. Decide separately.
- How the chat renders the error is the existing LLM-error card; not changed.

## Register notes

[PLAT-504](plat-504.md), fixed on main: a failed Codex turn is reported from structured records (rollout `codex_error_info`, `turn.failed`). Left: retry policy.
