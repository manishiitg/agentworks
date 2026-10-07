[← code / chat](index.md)

# PLAT-659: Message stuck Queued after a deploy changes the Code definition

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | code |
| Area | chat |
| Summary | A definition or provider change queued the next message behind a tmux live-input turn that never ends, so it stayed "Queued" forever; an idle CLI's turn is now ended and the message runs. |

## What happened

RTS, 2026-10-07. Code project sde-private, side chat task2 (session
`product-3dff1bdd-f711-4747-abae-e2c5f9faf7ac`). The coding CLI ran in tmux
live-input mode: one streaming turn had been open since about 05:40 UTC and
held the session input lane, while the person's messages went into the running
CLI as live input. A deploy at about 06:39 changed the Code product definition.
At 07:31:44 the person sent a message; the server logged
"[CHAT_HISTORY] Product definition changed …; relaunching the coding CLI",
queued the message ("occupied by input_lane") and re-checked every 2 s. The
live-input turn only ends when the CLI ends, so the message stayed "Queued" and
the chat showed "Working…" forever. A provider change takes the same path.

## Cause

`handleQuery` (agent_go/cmd/server/server.go) applies a runtime change
(definition, provider, workflow runtime) only between turns: while the session
is occupied it queues the message, and the queue watcher waited only for the
occupant to go away. That rule exists because relaunching mid-turn killed a
running Muse turn (Excellence, 2026-10-03). It treated an open live-input turn
whose CLI had finished answering as "mid-turn", and nothing ever ended it.

## Fix

- A message queued for a runtime change marks its session
  (`queueOccupiedConversationTurnForRuntimeChange`).
- The queue watcher (`watchOccupiedConversationTurnQueue`) calls
  `endIdleTurnForRuntimeChange` on each 2 s tick. When the main CLI is idle at
  its prompt for 3 ticks in a row, it ends the turn the way a definition change
  always does (`interruptWorkflowPolicySession`: cancel the turn, close the CLI)
  and releases the busy markers as Stop does (`releaseStoppedSessionTurnMarkers`).
  The watcher then kicks the queue and the message relaunches the CLI with the
  new runtime.
- Idle signal: the provider adapter's idle-composer check
  `llmproviders.CodingAgentPaneReady` on the main pane, the same check the
  retained-turn observer settles turns on. No live pane, a failed capture, or a
  provider without a pane check (Muse) counts as busy, so the message keeps
  waiting as before.
- A CLI that is mid-response keeps its turn; the message waits (2026-10-03 rule).

## Verification

`TestRuntimeChangeEndsOnlyAnIdleLiveTurn` (agent_go/cmd/server): with the input
lane held and a runtime change queued, an idle CLI's turn is cancelled on the
third tick and a busy CLI's turn is never cancelled. Run on GitHub through
`scripts/verify-remote.sh`.

## Left

- Not verified live: the real tmux pane check against a long live-input turn on
  RTS after the next deploy, for Claude, Codex and Cursor.
- Muse has no pane-ready check, so a Muse chat still waits for its turn to end.
