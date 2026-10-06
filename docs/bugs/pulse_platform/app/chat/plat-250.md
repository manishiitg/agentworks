[← app / chat](index.md)

# PLAT-250 — A chat tab left open across a server restart polls forever with no visible activity, until the page is manually refreshed

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | app |
| Area | chat |
| Summary | harness_issue, severity high — directly blocked the user's live |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-29` |

- **Priority:** harness_issue, severity high — directly blocked the user's live
  work in two different workflows (`jobsearch`, `website-aeo`) during this
  session.
- **Findings:** No workflow finding is linked. Discovered live, reported
  directly by the user ("check again now i changed something.. nothing is
  happening at all", reproduced again minutes later in a different
  workflow), reproduced against the actual running server's logs — not
  inferred from a screenshot alone.

## Root cause, confirmed against real server + frontend logs

The Go server's `--port 18743` process was restarted during this session
(`2026/08/29 21:37`). Its session event store is **in-memory only** — the
restart wipes it. A chat tab left open in the browser from before the
restart keeps its `lastEventIndex` (e.g. `38`) in the frontend's own
Zustand store, which a restart does *not* clear (only a page reload does).

`agent_go/cmd/server/polling.go`'s `GET /api/sessions/{id}/events?since=X`
handler explicitly signals "I don't know this session" with
`LastProcessedIndex: -1` when `!exists`:

```go
if !exists {
    response := GetEventsResponse{
        Events: []events.Event{}, ..., LastProcessedIndex: -1, ...
    }
    ...
}
```

But `ChatArea.tsx`'s `processEventsResponse` only ever *reads* this field
to advance the cursor forward (`if (... last_processed_index >= 0)`); it
had no handling at all for `-1`. So after a restart:

1. The tab keeps polling `since=38` forever.
2. The fresh server process's own (much shorter) post-restart event log for
   that session can never reach index 38, so it always returns `-1` /
   empty.
3. The frontend never advances its cursor and never resets it either — it
   silently polls the same dead cursor indefinitely, with **no error, no
   stuck-spinner, nothing** — the tab just looks idle forever.

Crucially, sending a new message on the stale tab (`POST
.../live-input`) *does* reach the backend and *does* complete a real turn
(confirmed live: `[STREAM] ... turn ended chunks=8 clean=1`,
`[RETAINED_TURN] ... state=completed`) — the backend is completely
healthy. The bug is purely that the tab's own polling loop can never see
any of it. A full page refresh works because the initial-load path
fetches recent events directly rather than resuming from a stale `since`
cursor, which is exactly why the user could confirm "it shows after i
refresh page" — the data was never missing, just permanently invisible to
the already-open tab.

## Fix

Added handling for `response.last_processed_index === -1` in
`processEventsResponse`: when the tab had previously tracked real progress
(`priorIndex > 0`), reset its stored cursor to `0` so the next poll tick
naturally requests `since=0` against the fresh server process and resyncs.
A genuinely brand-new, never-polled tab also gets `-1` on its very first
poll — deliberately left untouched (`priorIndex > 0` guard), since
resetting an already-`0` index is a no-op anyway and this keeps the fix
scoped to the actual regression shape (a session that used to have
progress, now doesn't).

## Verification

`npx tsc --noEmit` and `npm run build` both pass. No existing test suite
covers this component's polling loop; reproducing it live would require
restarting the dev server's backend process mid-session, which risks
disrupting the concurrent work already running against it, so this was
verified by direct code/log tracing (the exact `-1` signal and its absence
of handling), not a new automated test.

## Reverify

Confirm live: open a workflow chat tab, restart the backend server, then
send a message in the still-open tab without refreshing the page. It
should now resync and show the response, instead of appearing to do
nothing until a manual refresh.
