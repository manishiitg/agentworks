[← chat / rendering](index.md)

# PLAT-580: Stop/Send button flickers during a run

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | chat |
| Area | rendering |
| Summary | The composer's Stop and Send buttons swapped several times a second during a run |

## What happened

Owner, 2026-10-06: the Stop button in the composer flickers a lot during a run. The button shows while a turn is in
flight (`isStreaming` or running background agents). `isStreaming` follows the server's `busy`, `can_steer` and
`terminal_busy` flags, which dip for a moment during a run, so with no background agents the composer swapped Stop
and Send. An earlier fix had moved the control off `isStreaming` alone, but the combined signal still dipped.

## Fix

`useHeldTurnInFlight` keeps the in-flight state for 2 seconds after it drops, releases it at once when the turn
completes (`isCompleted`), and never carries it to another tab. `ChatInput` uses it only for the button's visibility;
whether the composer accepts input still follows `isStreaming`. One test pins it; `tsc -b` and the 29 ChatInput
tests pass. Not yet seen in a browser.

## Left

- Confirm live on a long run (Codex and Claude chats).
- The underlying flags still dip; a durable "turn running" field from the runtime phase would remove the need for the hold.
