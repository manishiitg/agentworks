# PLAT-505: any signed-in account could list every workflow's schedules and pause all schedules

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | schedules |
| Area | general |
| Summary | fixed on main, not deployed or checked live as a non-admin. |

**State:** fixed on main (not deployed, not checked live as a non-admin). P1.

**Found:** 2026-10-05, owner asked whether the global Schedules page is access controlled. Read from the code, not exploited:
- `GET /api/scheduler/jobs` returned every workflow's schedules (name, folder path, enabled, next/last run, durations) to any signed-in account, read-only ones included. The workflow part used a cache shared across users and never called the visibility check; Crew/Relay schedules were already per user. Per-job reads and actions did check visibility or ownership.
- `PUT /api/scheduler/config`, the global pause that stops every schedule on the platform, was wrapped only in `requireWorkflowWriteAccess`, so any editor-level account could pause or resume all schedules, not only administrators.

**Decision (owner, 2026-10-05):** the global pause is administrators only. The schedules list is filtered to what the account may open (administrators see everything); the page stays available because each Crew's own Automations panel uses the same call.

**Fix:** the pause route uses `requireAdmin`; the list handler runs `filterWorkflowManifestsForUser` (the same filter the workflows list uses); the pause buttons show only to an administrator or in a single-user (local) run (`useCanPauseSchedules`). A local run has no accounts and keeps everything.

**Left:** deploy; then check live as a non-admin (list shows only their workflows, pause returns 403) and as an administrator (sees all, pause works).

## Register notes

[PLAT-505](plat-505.md), P1, fixed on main, not deployed or checked live as a non-admin. The schedules list is now filtered to what the account may open; the global pause is administrators only.
