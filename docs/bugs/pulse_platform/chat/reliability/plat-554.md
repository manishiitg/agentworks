[← platform / chat-reliability](index.md)

# PLAT-554 — Live context fill and plan-limit warning in the chat during a turn

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | chat |
| Area | reliability |
| Summary | fixed on main, not deployed: Codex and Claude stream throttled context/plan usage during a turn; the chat's working footer shows a context meter and a plan window at 90%+ with its reset time. |

| Coordination | Value |
|---|---|
| State | fixed on main (2026-10-06); not deployed |
| Date | 2026-10-06 |
| Owner | chat-reliability |

## Source

Owner request: show the coding CLI's context fill and plan windows while a turn runs; a window above 90% was only visible in the terminal-icon hover (`terminalUsage.ts`).

## Done

- Provider (6ea681c): status lines carry `context_used_tokens` and `context_window_tokens` next to `rate_limit_windows`, plus `LiveUsageThrottle` (newest snapshot, at most every 3 s, only when changed).
  - Codex tmux: the existing rollout status line gains the context keys. Codex structured: new rollout side channel turns `token_count` records into throttled status lines (stdout has no window and no usage until the end).
  - Claude tmux: from the statusline JSON (`context_window.current_usage` + `context_window_size`). Claude structured: assistant-message usage (input + cache read + cache write) and `rate_limit_event.unifiedWindows`; this transport reports no context window, so it shows tokens, not a percentage.
- agent_go: status lines from a structured transport (no tmux session) now update only the owning terminal instead of every terminal in the session. They stay out of chat history (status_line is not durable). The pre-existing re-lock of the store mutex on the no-terminal path is avoided.
- Frontend: the chat's working footer (`TranscriptActivityFooter`, still h-7) shows a small meter "ctx 42%" (or "ctx 235k") on the right, and the most-used plan window in amber when it is at or above 90% with its reset time ("7d 93% · resets 3:30 PM"). The footer re-renders only when those values change.
- Live check: `TestCodexCLIStructuredLiveUsage` (RUN_CODEX_CLI_STREAM_JSON_E2E=1) ran a real `codex exec --json` turn: a status_line chunk arrived with context 19681 / 258400 and the 7d window.

## Left

- Pi, Muse, Agy and Cursor publish no context window during a turn here; their footer shows nothing new.
- Not checked in a running AgentWorks server and browser; the footer reads the same terminal snapshot the composer hover already reads.

## Register notes

[PLAT-554](plat-554.md), P2, fixed on main, not deployed: Codex and Claude stream throttled context/plan usage during a turn; the chat's working footer shows a context meter and a plan window at 90%+ with its reset time.
