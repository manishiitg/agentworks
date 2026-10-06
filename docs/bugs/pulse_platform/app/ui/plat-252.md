[← platform / frontend-chat](index.md)

# PLAT-252 — A scheduled/bot-run tab whose session data never arrives is stuck on "Restoring previous session..." forever, with no escape

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | app |
| Area | ui |
| Summary | harness_issue, severity medium — no data loss, but a |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-31` |

- **Priority:** harness_issue, severity medium — no data loss, but a
  permanently unusable tab with no visible error and no way to recover
  short of closing it.
- **Findings:** No workflow finding is linked. Reported live by the user
  with a screenshot: a tab titled `full-run [default / iteration-0]`
  (opened from the Schedule panel to view a running scheduled job) stuck
  on the "Restoring previous session..." spinner indefinitely.

## Root cause

This is a different code path from the tab-restore timeout gap fixed
earlier in `WorkflowLayout.tsx` (that one is confirmed still correct — all
3 of its `restoreWorkflowStateFromEvents` call sites remain properly
wrapped in `withWorkflowRestoreTimeout`). This report traces instead to
`resolveChatSurface.ts`'s `isReadOnlyRunView` input:

```ts
if (!hasLiveOrRestoredContent && (isRestoring || resumeSettling || isReadOnlyRunView)) {
  return 'restoring'
}
```

A scheduled/bot-run tab (`isReadOnlyRunView`) is *deliberately* forced into
`'restoring'` while it has no content, by design — such a tab must never
fall through to the previous-chats landing panel (the earlier
"schedule-bounce" fix). But unlike a normal resumed tab, which has a
`resumeGaveUp` timer that eventually lets a dead resume fall to
`'landing'`, `isReadOnlyRunView` had **no equivalent give-up mechanism at
all**. If the session's events never arrive — its in-memory event-store
entry is gone (e.g. after a server restart) or the initial fetch in
`WorkflowScheduleRunsPanel.tsx`'s `openScheduledRunInChat` failed and was
silently swallowed by an empty `catch` — the tab has no content, isn't
streaming, and `isReadOnlyRunView` stays true forever: permanently stuck,
by construction, with no timeout anywhere in the chain.

## Fix

Added a `readOnlyRunViewGaveUp` timer in `ChatArea.tsx`, mirroring the
existing `resumeGaveUp` pattern exactly: it flips true after
`RESUME_SETTLE_MS` if `isReadOnlyRunView` is true and the tab still has no
content and isn't streaming. This does **not** change
`resolveChatSurface`'s returned surface or its landing-avoidance guarantee
(no changes to `resolveChatSurface.ts` at all) — it only swaps which JSX
renders inside the existing `'restoring'` branch: past the timeout, an
explicit "Couldn't load this run's session data — it may no longer be
available." message with a "Try again" button replaces the spinner. "Try
again" calls a new `retryReadOnlyRunView`, which re-fetches the session's
events via `agentApi.getRecentSessionEvents` (the same call
`openScheduledRunInChat` makes on first open) and applies them the same
way; if the session really is gone, this legitimately comes back empty and
the give-up timer simply re-arms and fires again.

## Verification

`npx tsc --noEmit` and `npx eslint src/components/ChatArea.tsx` both
clean (one pre-existing, unrelated `react-hooks/exhaustive-deps` warning
on a different hook in the same file). No new automated test added —
reproducing the stuck state live requires a scheduled-run tab whose
backend session is genuinely gone (e.g. after a server restart), which
risks disrupting other concurrent sessions on the shared dev server; the
fix was verified by direct tracing of `resolveChatSurface`'s precedence
chain and confirming the new timer/branch follow the exact same pattern
already proven correct for `resumeGaveUp`.

## Follow-up hardening — 2026-08-31

Sales Outreach exposed an earlier, distinct path into the same symptom. The
initial workflow reconnect deliberately filtered out every external read-only
session, including a live scheduled run, then created a blank Workflow
Builder tab. A later polling reconciler could eventually discover the
schedule, but its first catch-up read only the volatile EventStore. If that
buffer had expired, the polling loop marked the empty Schedule tab streaming
again on each pass; that also prevented the existing give-up timer from
arming reliably.

`WorkflowLayout.tsx` now treats schedules (but not bot sessions) as
first-class parallel tabs during the initial reconnect. Schedule catch-up
uses the workflow's durable conversation history when live events are gone.
The server also preserves `triggered_by=cron` and the configured schedule
name in the running-workflow record from the start, so the frontend cannot
mistake it for the user's interactive Builder chat.

Focused scheduler, workflow-tab, and TypeScript checks pass. Live
reverification remains: opening Sales Outreach while a schedule is running
must show its named Schedule tab immediately alongside Chat, with its saved
transcript rather than a permanent restore spinner.

## Reverify

Confirm live: open a scheduled/bot-run tab whose backend session has
expired (e.g. right after a dev-server restart). It should show the spinner
for ~10s, then switch to the "Couldn't load..." message with a working
"Try again" button, instead of spinning forever.
