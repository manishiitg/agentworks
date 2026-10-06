[← platform / frontend-chat](index.md)

# PLAT-143 — workflow restoration uses one global boolean, so an unrelated session can cover the selected workflow with a permanent restore screen

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | platform |
| Area | frontend-chat |
| Summary | P1 — a healthy workflow becomes unusable until refresh even |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — restore ownership is keyed and reference-counted by session; integration coverage remains desirable |
| Last synchronized | `2026-08-19` |

- **Priority:** P1 — a healthy workflow becomes unusable until refresh even
  though its workspace APIs are responding normally.
- **Owner:** `frontend/src/stores/useChatStore.ts`,
  `frontend/src/components/workflow/WorkflowLayout.tsx`, and ChatArea surface
  selection.
- **Observed on:** LinkedIn, showing `Restoring previous session…` indefinitely.
- **Related:** [PLAT-109](plat-109.md) (workflow-switch hydration/selection),
  [PLAT-116](../coding-agent-bridge/plat-116.md)) (the latest LinkedIn run also ended through the missed
  completion path).

## Evidence

- LinkedIn's workspace and schedule-history requests returned HTTP 200 quickly;
  the backend was not blocked reading the workflow.
- The frontend store exposes one process-wide
  `isRestoringWorkflowSessions: boolean`.
- ChatArea renders its full restore surface from that singleton.
- Several effects can set the singleton to true while rehydrating different
  tabs/sessions. The 10-second timeout clears the bit, but a subsequent effect
  can immediately arm it again.
- At the same time, unrelated session-event endpoints were being polled
  continuously, demonstrating that restore work from other product/workflow
  sessions remained active in the same frontend process.

## Root cause

Restoration is session-owned work represented as global application state. The
UI therefore cannot answer which workflow or tab is restoring, deduplicate two
requests for the same session, or distinguish a failed optional history refresh
from a workflow that has no usable content.

The timeout masks one stuck promise but cannot repair incorrect ownership. It
also allows repeated effects to make the spinner effectively permanent.

## Fix reasoning

Model restoration by identity:

```text
restoreState[session_id] = idle | restoring | ready | failed
```

- Render the blocking restore surface only for the active tab's session.
- Coalesce concurrent restoration for the same session into one promise.
- Permit one bounded automatic attempt; a failure becomes `failed`, not another
  automatic loop.
- Keep already available formatted history visible when an optional refresh
  fails, with a small manual Retry action.
- Cancel or detach restoration work when tab ownership changes.
- Remove the process-wide boolean once all consumers use the keyed state.

## Acceptance

- Restoring Video Studio or another workflow cannot show a spinner on LinkedIn.
- Switching workflows during restoration displays the destination's own state.
- One session produces at most one in-flight hydration request.
- A timeout settles to visible history or a recoverable error within the bound;
  it cannot re-arm without an explicit new request.
- Refresh and rapid workflow switching are covered in a frontend integration
  test with two sessions restoring concurrently.
