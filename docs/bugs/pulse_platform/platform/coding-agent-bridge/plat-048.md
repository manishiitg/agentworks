[← platform / coding-agent-bridge](index.md)

# PLAT-048 — scheduled and completed coding-agent tmux processes leak

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | coding-agent process lifecycle, terminal leases, and shutdown |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-07` |

- **Priority:** P0
- **Owner:** coding-agent process lifecycle, terminal leases, and shutdown
- **Source:** local process audit after the desktop became slow

## Problem

Persistent tmux was inferred from `execution_kind=main_agent`, which also
classified scheduled agents as continuable. Completed schedules and stale
sessions therefore survived, while treating every `workflow_phase` as bounded
would break real Automation Builder and Ask-in-chat continuations.

## Fix

Persistence now uses scheduled origin, child ownership, typed stage, automatic
notification status, and the explicit `user_interactive_continuation` marker.
Only real interactive main chats retain tmux, with a maximum one-hour idle
window. Stop closes a still-live process without rewriting a completed logical
outcome, the live lease reaper handles bounded processes without a restart, and
shutdown matches current Claude, Codex, Cursor, Agy, and Pi session prefixes.
Native resume is allowed only with an actual provider session ID.

## Verification

Focused terminal-lease, coding-agent mode, tmux reaper, Stop-route, resume,
schedule-conversion, frontend build, and shell-syntax tests pass. Runtime
acceptance still requires one completed schedule to exit and one interactive
chat to remain continuable on the deployed binary.
