[← platform / frontend-chat](index.md)

# PLAT-026 — global activity hides the selected running workflow

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | platform |
| Area | frontend-chat |
| Summary | `frontend/src/components/GlobalActivityMonitor.tsx` |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `implemented` |
| Execution order | `E — third` |
| Last synchronized | `2026-08-04` |

> Claim this ticket as `in_progress` before implementation. Update this
> fragment during active work; synchronize the shared index at handoff.

- **Priority:** P1
- **Owner:** `frontend/src/components/GlobalActivityMonitor.tsx`
- **Evidence:** the deterministic filter removes
  `session.session_id === currentSessionId`, while the current-workflow selector
  does not render the monitor's running/clock/needs-input state.
- **Source:**
  [what_the_runtime_tells_an_agent_about_itself.md](../../../what_the_runtime_tells_an_agent_about_itself.md)
- **Problem:** the workflow currently being viewed can be the only running
  workflow whose status is absent from the global header.
- **Implementation boundary:** show the selected workflow's canonical activity
  exactly once—either retain its monitor item or project the identical status
  onto the current selector. Do not create a duplicate pill.
- **First pass (2026-08-04, incomplete) — caught by Codex review:** the
  selector was given a live status icon
  (`currentActiveSession`/`currentSessionId` in
  `globalActivityMonitorStatus.ts`), but the monitor's own exclusion still
  filtered only `session.session_id === currentSessionId`. A sibling session
  for the same workflow — a background Pulse schedule, a second observing tab
  — has a different `session_id`, so it still rendered its own pill while the
  selector also showed that workflow's status: the workflow appeared twice,
  which is exactly the acceptance boundary above. This was reported to the
  user as "shipped," which was wrong; recorded here so the mistake is visible,
  not smoothed over.
- **Implementation (2026-08-04, corrected):** extracted the full pill
  selection into an exported pure function,
  `visibleActivitySessions(activeSessions, currentSessionId,
  currentSessionWorkflowKey)`, in `globalActivityMonitorStatus.ts`. It now
  excludes a session by workflow identity (`sessionWorkflowKey`, also
  exported) when that identity matches the current session's, not only by
  literal session id. `GlobalActivityMonitor.tsx` computes
  `currentWorkflowKey` from `currentActiveSession(...)` and calls the shared
  function; the previous inline dedup-by-workflow logic moved into the same
  function so callers cannot drift into two definitions of "current" again.
- **Verification:** `visibleActivitySessions` has direct unit coverage for
  the reported scenario (sibling session, same workflow, different id) and
  three adjacent cases (no workflow key present, two non-workflow sessions
  sharing no key must not spuriously collide, and two same-workflow
  non-current siblings still dedup by rank). Verified the primary case fails
  against the pre-fix filter (`pulse-schedule-session` leaks through) and
  passes with the fix. `tsc -b` and `eslint` clean; full frontend suite
  399/399.
- **Regression tests:** `GlobalActivityMonitor.test.ts`, `describe('visibleActivitySessions', ...)`.
- **Acceptance:** with the current workflow plus two other active workflows,
  all three states remain visible, the selected workflow appears exactly once,
  and running/clock/needs-input transitions use the same source as `@active`.
  Met by direct unit test; a real multi-session runtime observation (current
  workflow + a genuinely concurrent sibling session for it) has not been
  exercised, so this stays `implemented` rather than `done` per the board's
  own rule that `done` requires runtime evidence.
