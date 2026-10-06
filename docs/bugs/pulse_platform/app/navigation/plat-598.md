[← app / navigation](index.md)

# PLAT-598: Integrations always opens its overview instead of restoring the last section

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | app |
| Area | navigation |
| Summary | Opening Integrations should show its section overview every time, instead of restoring the last visited section. |

## What happened

Owner asked to always start at the pictured Integrations overview (Tools & secrets,
Brain, Folders & workflows, Slack, WhatsApp, Google apps). Previously the top-level
section used persisted tab state and the menu started closed, so returning or
reloading opened the last section directly.

## Fix

Code/Crew and workflow/Relay Integrations start with their overview open and use
local state for the selected section. Opening a section and returning through
its breadcrumb still work. Remount the integration panel when the workspace
changes; workflow transitions into Integrations also remount it. Identity and
other non-Integrations view transitions keep their existing state behavior.
Nested Tools & secrets tab preferences and integration configuration are unchanged.
Existing saved top-level section keys are ignored, so no storage migration is needed.

Verification: existing four layout/connection suites (13 tests) and TypeScript
compilation pass. A real local browser rendered the Code integration panel with
its old saved section seeded as Slack: all six overview choices appeared. Slack
opened from the menu, and reloading returned to the overview. The preview used
the real component with a populated chat fixture, normal theme/tooltip providers,
and no authenticated backend; configuring connections was not exercised.

## Left

No implementation or deployment work remains. The local browser check used a fixture; authenticated connection setup was outside this change.

## Deployment evidence — 2026-10-06

Excellence release `agents-4e94bdea-20261006122619` records builder revision
`4e94bdea98`, containing this change and PLAT-589/591/594/595. Frontend build,
catalog and bundle checks passed. Public health is 200; slot self-test passed
with 156 passed, zero failed, 16 skipped.
