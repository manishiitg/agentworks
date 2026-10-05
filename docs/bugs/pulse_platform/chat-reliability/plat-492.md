[← Platform issue index](../../pulse_platform_issue_register.md)

# PLAT-492 — After a provider switch the next messages never ran (queued messages were never re-kicked)

| Coordination | Value |
|---|---|
| State | fixed on `main`; deploy pending. Regression of PLAT-425 (a provider change queues the message instead of steering the old CLI) |
| Severity | P0 (the Builder chat looked dead: messages stayed "queued" for hours, the new provider did nothing) |
| Date | 2026-10-05 |
| Owner | chat-reliability |
| Related | PLAT-425, PLAT-437 |

## Problem

Owner, Upwork Builder chat (`455e81f4`), provider switched Muse to Codex between turns: "the new messages just don't run at all and the new provider also doesn't do anything".
The durable turn queue (`chat_history/conversation-turn-dispatch.json`) still held the four messages of 00:10-00:14 IST nine hours later, all with `started_at` null.
The log signature `[CHAT_HISTORY] Provider changed ... the retained CLI is not codex-cli` appears once per send, then `queued_for_turn`.

## Cause

PLAT-425 made a message sent after a provider change wait in the durable queue (a changed runtime never applies mid-turn) where before it was steered live into the running CLI.
The queue only starts a waiting message when `kickConversationTurnQueue` runs at a moment the session is idle. Every occupant (input lane, turn cancel handle, stored agent `TurnInProgress`,
retained main-turn record) is released without a kick of its own or a moment after the kick that saw it. Reproduced live (below): the kick at turn end saw `input_lane`, the kick after the lane release
saw `stored_agent_turn`, and nothing kicked again, so both queued messages waited forever. In the owner's chat the retained Muse record was not released until 01:26 (tmux reaper), and the queue was not kicked then either.
Queueing was already there for any message sent during a turn; the provider fix made it the normal path for the first messages after a switch, so a latent missed-kick race became a dead chat.

## Fix

`kickConversationTurnQueue` no longer gives up when the session is occupied: it starts one watcher per session that re-checks every 2 s and kicks once the occupant is gone (it stops when nothing is queued).
The log now names the occupant (`session X occupied by <input_lane|stored_agent_turn|...>; queued messages wait and the queue is re-checked every 2s`).
Live check: `mcp-agent test provider-switch-e2e` (long Muse turn, a steer into it, Builder LLM switched to Codex, two more messages): without the fix C and D stay queued (failed twice, 5 min timeout);
with it both run (`PASS`, answered when the running turn ended).

## Left

- Deploy; then repeat the owner's sequence on the local app. Stale entries from before the fix drop on the next server restart (older than 30 min) or run, newest chat state permitting.
- Why the retained Muse main-turn record of `455e81f4` stayed until 01:26 after its turns completed (the 00:13 and 00:24 completions did not settle it) is not explained: the log of that period was rotated away. With the watcher it only delays queued messages, it no longer strands them.
- A first Codex turn after a switch replays the whole history; not measured here with a 120-message chat (the test chat is small), so any silent start is unconfirmed.
