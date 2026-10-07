[← chat / rendering](index.md)

# PLAT-649: Builder chat flickers and jumps when a message is sent

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | chat |
| Area | rendering |
| Summary | Each send reset the event cursor and re-read the whole session; the replayed old completions and the pre-acknowledgement status ended the new turn for a moment, so the chat flickered and jumped. |

## What happened

In a workflow builder chat (owner, 2026-10-07, long Upwork and sales outreach
chats), sending a message made the chat flicker while the turn started and
while it streamed: messages redrew, the Stop button and activity line went
off and on, and the scroll position jumped. The owner's window showed, within
about 1 s of a send, the visible text dropping (7462 → 5702 → 3271 chars) and
scrollTop going 44991 → 44065 → 45609 with the same scroll container.

## Cause

Reproduced on an isolated server (ports 18843/18844/18845) with a 50 ms
sampler on the transcript scroller, a store watcher and request timings.

1. `ChatArea.submitQueryWithQuery` set the tab's event cursor to -1 on every
   send (old comment: "critical when continuing a restored session"). The
   catch-up poll that starts with the turn then read
   `events?since=0&durable_chat=1`, the whole session. `processEventsResponse`
   treated the replayed `unified_completion` of every earlier turn as the end
   of the new turn: `setTabStreaming(false)`, `setTabCompleted(true)` and
   `clearStreamingText`, and it replaced history rows in the store (8 of 123
   event objects in the test chat). The turn came back on with the next status
   about 0.7 s later. In a long chat that response is large and slow, so it can
   land while text is streaming and clear it.
2. The same first poll is sent before `/api/query` is acknowledged, so the
   session still reports the previous turn as `completed`; that status alone
   switched the new turn off as well.

Restore already fetches a fresh backend cursor before SSE opens, and a backend
restart is healed by the `last_processed_index === -1` branch, so the reset is
no longer needed.

## Fix

- `frontend/src/components/ChatArea.tsx`: keep the forward event cursor on
  send (no `setTabLastEventIndex(..., -1)`).
- While a send is not yet acknowledged, a non-running session status read in
  `processEventsResponse` is ignored for that session
  (`unacknowledgedSendSessionsRef`, cleared in a `finally` after the query
  response). A real completion event still settles the turn.

Sampler, same chat after a page reload (compact restore), one short send:

| | before | after |
|---|---|---|
| `events?since=0` requests per send | 1 (whole session, 208 ms) | 0 |
| turn switched off and on before the reply (isStreaming true→false→true) | yes, every send (~0.4–1.2 s) | no |
| Stop button off/on during the turn | yes | no |
| history event objects replaced at send | 8 of 123 | 0 |
| "Recovered activity" heal per send | 1 | 0 |
| scroller remounts / list removals | 0 / 0 | 0 / 0 |
| scrollTop | moved with the flip | only grows with new content |

## Left

- The owner's large scroll jump (about 900 px) and the 17 SSE connect/close
  cycles in 15 s during a workflow run were not reproduced on the small test
  chat; check the owner's chat again after this lands.
- Opening a chat still replaces the scroll container once (seen on the owner's
  window); not changed here.

## Opening a chat jumped from the top (2026-10-07)

The "left" item: opening a long chat painted it from the top and then jumped to the bottom (owner's window: scrollTop
0 -> 40,328 -> 45,948). Cause: the formatted transcript's Virtuoso reads `initialTopMostItemIndex` only when it
mounts, and a chat opened while its history was still loading mounted with no rows, so at index 0; the bottom
position computed once rows arrived was ignored. Fix: the Virtuoso is keyed on whether its starting position is known,
so it remounts once, while still empty, and opens at the bottom (or the saved reading position). A transcript that
already has rows on its first render is unaffected.

