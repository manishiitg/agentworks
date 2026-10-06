[← platform / chat-reliability](index.md)

# PLAT-360 — A step-completion turn colliding with a user turn closed the CLI and lost the message

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | platform |
| Area | chat-reliability |
| Summary | fixes a lost user message on a Cursor tmux workflow chat. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude |
| Ticket state | `fixed on main; deploy and live verification pending` |
| Last synchronized | `2026-09-25` |
| Priority | `P1 chat reliability` |
| Parent | [PLAT-352](plat-352.md) (chat turns execute exactly once) |

## Problem and server evidence

RTS workflow `automationtesting` (Cursor CLI, tmux transport), session
`41373bfd-6613-4a30-b34b-e43e27c99205`, 2026-09-25 (UTC):

- 03:43:05: the user sent a message. Delivery into the retained CLI was
  still running after 12 s, so the server answered 202 and finished the
  delivery in the background.
- 03:43:24: `[STREAM] … turn ended`. A background step finished and its
  completion ("synthetic") turn started:
  `[BG AGENT] Executing synthetic turn for session … via stored agent`. The
  next lines were `[ACTIVE_SESSION] Updated session … status to: error` and
  `[BG AGENT] Synthetic turn failed … after stream start: mcpagent: a turn is
  already in flight on this agent`, then `… failed asynchronously for agent
  workflow-full-…-step-0-…: mcpagent: a turn is already in flight on this
  agent — queued for retry`.
- 03:43:28: `[TMUX_REAPER] Closed stale coding-agent tmux session
  "mlp-cursor-cli-int-…" … owner="main:41373bfd-…"` with reason
  `owner session error`. The user's message was lost.
- 03:43:31: the user's second message relaunched the session. The step
  completion went first, and neither user message got a reply.

## Cause

A user message delivered as live input starts a turn on the retained
`mcpagent.Session` without holding the server's session input lane. The
completion turn therefore acquired the lane and called `Session.Run`, which
correctly refused with `mcpagent.ErrTurnAlreadyInFlight`: nothing ran.

`executeSyntheticTurnWithOutcome` (`agent_go/cmd/server/background_agents.go`)
treated every synthetic-turn failure the same way, both at stream setup and
on the asynchronous outcome: `updateSessionStatus(sessionID, "error")` and
`terminalStore.MarkTurnFailed(mainTerminalID)`. The tmux reaper
(`staleCodingAgentTmuxCleanupCandidates` in
`agent_go/cmd/server/coding_agent_tmux_reaper.go`) closes a terminal at once
when its owner session is `error`, so it killed the Cursor CLI that was still
running the user's turn. The retry for the completion was already queued; only
the status and terminal bookkeeping were wrong.

## Fix

Both synthetic-turn failure sites now go through one helper,
`settleFailedSyntheticTurn`. When the error is `ErrTurnAlreadyInFlight` it
logs the refusal and leaves the session status and main terminal as they are:
another turn owns them. Every other failure still marks the session `error`
and fails the terminal, as before. The refusal is still passed to the
completion callback, which queues the notification and arms the bounded
retry, so the completion runs after the user's turn ends.

The reaper is unchanged. Its rule (owner session `error` closes the terminal)
is correct once the status is accurate; the bug was the wrong status.

## Verification

- New `agent_go/cmd/server/synthetic_turn_in_flight_test.go`:
  - an in-flight refusal leaves the session `running`, does not fail the main
    terminal, keeps the session reachable for the retry, and the reaper
    closes nothing;
  - a genuine failure still sets `error` and the reaper still closes the
    terminal;
  - both failure sites in `executeSyntheticTurnWithOutcome` use the helper,
    and the asynchronous one still sets `terminalErr` (which drives the retry)
    first.
- The in-flight test fails without the fix (`status = "error", want running`).
- `go test ./cmd/server -run 'TestSyntheticTurn|TestCleanupStaleCodingAgentTmux|TestP0|TestDrainSyntheticTurn|Occup|TestWorkflowTurnQueues' -count=1` passed; `go vet ./cmd/server` clean.

Pending: deploy to RTS, then reproduce on a Cursor tmux workflow chat by
sending a message while a background step is about to finish. Expected: the
user's message gets its reply in the same terminal, no `status to: error` or
`TMUX_REAPER … owner session error` lines, and the step completion runs
afterwards from the retry.

## Register notes

[PLAT-360](plat-360.md) fixes a lost user
message on a Cursor tmux workflow chat. A background step's completion turn
started while the user's message was running in the retained CLI; `Session.Run`
refused it as "a turn is already in flight", but the server marked the session
`error`, and the tmux reaper then closed the CLI mid-turn. That refusal no
longer changes the session status or fails the terminal; the completion is
still queued for retry and runs after the user's turn. Fixed on main; deploy
and live verification pending.
