[← chat / reliability](index.md)

# PLAT-539 — A long Codex chat showed its FIRST reply (3 October sandbox test) again after every turn

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | chat |
| Area | reliability |
| Summary | fixed on main, not deployed: the sidecar completion path now emits nothing when the turn is no longer tracked. |

| Coordination | Value |
|---|---|
| State | fixed on main (2026-10-05); not deployed; owner to check the chat after a restart |
| Date | 2026-10-05 |
| Owner | chat-reliability |

## Source

Owner, sales-outreach chat (Codex): after "The run finished: 0 messages..." the chat showed "Ran from the CLI runtime directory using only my own shell tools ..." and a sandbox test table. The Codex terminal showed only the real reply (10:06 PM).

## Cause (verified)

- The extra text was a second `unified_completion` (seq 16138, `source: coding_agent_sidecar`) written one second after the real one (seq 16135, `canonical_turn_completion`). Its text is the final message of the thread's FIRST turn (3 October 17:35). The same text had been
  recorded the same way at seq 11055, 12707 and 13030 on 4 October. `coding_agent_sidecar` is only the label of the server's own retained-turn completion path (`emitRetainedMainTurnStreamCompletion`, `server.go`).
- That path re-reads the turn's start time from `retainedMainTurns`. After the structured completion has settled the turn the entry is gone, so the start time is zero. With a zero start the "already emitted" guard is skipped, and `ReadRetainedTurnMessages` /
  `readCodexRolloutFinalAssistantText` (provider) applies no timestamp filter and keeps the completion of the first `task_started` turn in the file. The rollout itself was correct (175 turns, in order).
- The chat is one thread since 3 October, so the first turn is old. Any retained Codex chat can show its first reply the same way.

## Done

- `emitRetainedMainTurnStreamCompletion`: with no tracked turn it logs `[RETAINED_TURN] No active retained turn, not emitting a completion` and emits nothing. Test `TestRetainedTurnCompletionEmitsNothingWhenTheTurnIsNoLongerTracked` fails with one stale event on the old code.

## Left

- Provider hardening (not done): `ReadRetainedTurnMessages` should return nothing for a zero turn start, as the progress reader already does; it needs a provider commit and a pin bump.
- The stale completions already stored in the sales-outreach chat's history stay (seq 11055, 12707, 13030, 16138); they are old events, not removed.
- Owner check after restart: no extra sandbox reply after the next turn of a long Codex chat; look for the new log line.

## Register notes

[PLAT-539](plat-539.md), fixed on main, not deployed: the sidecar completion path now emits nothing when the turn is no longer tracked.
