[← coding-agents / sessions](index.md)

# PLAT-029 — stale live metadata reconnects forever to a missing tmux

| Field | Value |
|---|---|
| State | closed |
| Priority | P0 |
| Product | coding-agents |
| Area | sessions |
| Summary | terminal live-attach lifecycle |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implemented` |
| Last synchronized | `2026-08-04` |

- **Priority:** P0
- **Owner:** terminal live-attach lifecycle
- **Evidence:** the Social Media main terminal was recorded as
  `state=completed, process_state=live, snapshot_kind=live`, but `tmux
  has-session` proved its named session no longer existed. The frontend kept
  opening the live-stream websocket and appeared stuck instead of rendering
  the retained transcript as a scrollable archive.
- **Root cause:** the stream resolver trusted an in-memory terminal snapshot
  without verifying its tmux target. Missing-target verification existed only
  for restart recovery, after the in-memory snapshot was absent. The periodic
  watchdog was not a sufficient request-time correctness boundary.
- **Implementation:** before upgrading a stored terminal to a websocket, the
  backend now runs a bounded `tmux has-session` probe. A missing target is
  immediately reconciled through `MarkProcessClosed`, which retains content,
  clears the tmux identity, and publishes `process_state=closed` plus
  `snapshot_kind=archived`. An indeterminate probe returns 503 without opening
  a hanging websocket. Cross-site origin rejection now happens before lookup
  or reconciliation, so a forbidden request cannot inspect or mutate terminal
  lifecycle state.
- **Primary files:** `agent_go/cmd/server/terminal_live_attach.go` and
  `agent_go/cmd/server/terminal_live_attach_test.go`.
- **Verification:** the backend regression starts from a stored live snapshot,
  makes the tmux probe return `can't find session`, expects HTTP 410, and proves
  the retained transcript survives as a closed archive with no tmux target.
  The focused resolve/stream suite passes.
- **Acceptance:** a dead tmux can never be upgraded to a websocket. Selecting
  its terminal settles to retained static content rather than reconnecting
  indefinitely. Runtime confirmation requires a rebuilt server; development
  did not restart the user's running backend.
