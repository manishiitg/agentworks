[← platform / coding-agent-bridge](index.md)

# PLAT-351 — Retained-turn observer settles on durable runner outcome when the pane never reports idle

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | fixes the false "running" global monitor: the retained-turn observer settled only on `CodingAgentPaneReady` pixels, which has no Muse case and returns false forever, so a quiet-but-finished Muse turn stayed busy indefin… |

| Field | Value |
|---|---|
| Status | `implemented and pushed; live acceptance pending (localhost restart)` |
| Priority | P1 retained-turn correctness |
| Owner | server agent runtime |
| Reported | 2026-09-22 |
| Related | PLAT-179 (shared `finalResponse` tool-call guard), PLAT-346 (retained lifecycle timers) |

## Problem

`observeRetainedMainTurnStream` settled a retained main-agent turn only on
`llmproviders.CodingAgentPaneReady` pixels, with a stability gate. That
dispatch has no Muse case and returns false forever for `muse-cli`, and the
observer loop had no deadline and never consulted the provider's durable
turn sidecar (it only did so in `handleRetainedMainTurnStreamClosed`, after
the tmux control stream died).

A quiet-but-finished Muse retained turn therefore stayed in
`retainedMainTurns` indefinitely: the session stayed busy, the global
activity monitor showed a false "running" (1hr+ in the reported News
Monitor case), and no log line distinguished it from a genuinely active
turn. The provider's own completion contract already existed and was
correct — `museRetainedTurnReady` (5s log quiet + TUI at prompt + pane
stable, with pending-question auto-answer serviced first) feeds
`musecli.ReadRetainedTurnMessages` → `retainedturn.FinalResponse` — but
nothing on the live path asked it.

## Fix

- After `retainedMainTurnDurableQuietWindow` (15s) of tmux-output quiet
  with a never-idle pane, the observer consults
  `retainedturn.FinalResponse` (at most every
  `retainedMainTurnDurableRecheckWindow`, 5s) and settles `completed` on
  runner outcome instead of pixels. The completion event carries the
  sidecar's final text via the existing emit path.
- Providers without a retained-turn reader return "" and keep exact
  pane-only behavior. Silent long tool runs cannot settle early: while a
  tool is in flight the newest assistant message carries the ToolCall
  (PLAT-179 guard) or the TUI is not at its prompt, so the sidecar stays
  empty.
- The live-loop settle is additionally allowlisted to readers that assert
  completion (Muse, Codex, Claude, Cursor). Pi is excluded: its reader
  returns all in-turn messages with no completion gate, so a question
  awaiting a slow user would settle early. Follow-up: gate Pi's reader on
  completion (or a provider-contract capability flag) and re-admit it.
- One diagnostic `WARNING` per observer at 30m when neither pane-idle nor
  a durable final response has fired. It never settles the turn:
  marathon turns are untouched by design (no hard observer deadline).
- Consolidated the two inline final-response seam blocks (stream-closed
  reconcile, completion emit) onto one `retainedTurnFinalResponse`
  helper. Windows are vars, not consts, so tests can shrink them.

Commit: `7e887ec3d`.

## Verification

- New `TestRetainedMainTurnSettlesOnDurableFinalResponseWhenPaneNeverIdles`:
  Muse pane stays busy forever; an empty sidecar keeps the turn open
  (consult observed), then a durable final settles it to completed with
  `unified_completion`, live tmux kept. Passes.
- Full `Retained|LiveAttach|LiveInput|MCPAgentSession|TerminalStream|TmuxStream`
  set passes; `go vet` and `gofmt` clean.
- Full `cmd/server` suite shows only the 3 pre-existing failures,
  byte-identical on the pristine tree (a 4th flaked once under load and
  passed on rerun with the change).
- Live acceptance pending: restart localhost, confirm the next quiet
  Muse retained turn settles within ~15s of its final response and the
  global monitor clears, and that a still-unsettled turn emits the 30m
  warning with provider/elapsed/quiet.

## Register notes

[PLAT-351](plat-351.md) fixes the false
"running" global monitor: the retained-turn observer settled only on
`CodingAgentPaneReady` pixels, which has no Muse case and returns false
forever, so a quiet-but-finished Muse turn stayed busy indefinitely. After
15s of stream quiet the observer now consults the provider durable sidecar
(`retainedturn.FinalResponse`, at most every 5s) and settles on runner
outcome; providers without a retained reader keep pane-only behavior, and a
one-time 30m warning (never a forced settle) covers turns where neither
signal fires. Implemented and pushed; live acceptance pending.
