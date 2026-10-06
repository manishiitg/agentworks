[← platform / frontend-chat](index.md)

# PLAT-131 — session enrichment erased the identity the frontend needs, so a global-activity pill could not switch workflows

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | app |
| Area | ui |
| Summary | P2 — no data loss and no wrong execution, but a live activity |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — fail-before/pass-after against the exact live payload; live reverify pending a server restart |
| Last synchronized | `2026-08-18` |

- **Priority:** P2 — no data loss and no wrong execution, but a live activity
  pill silently navigates to the wrong workflow, and the only visible symptom
  is "clicking does nothing useful."
- **Owner:** `cmd/server/polling.go` (`buildActiveSessionInfoSummary`)

## How it surfaced

Reported live: `rtslatency` shows in the top global activity monitor, but
clicking it does not switch to that workflow — it opens a Schedule tab under
whichever workflow is currently on screen (`build-in-public`), and the pill
survives a page refresh.

## Root cause

`buildActiveSessionInfoSummary` enriches each session with data from its
tracked execution. Both enrichment branches assigned three identity fields
unconditionally:

```go
active := trackedExecutionToActive(exec)
enriched.PresetQueryID = active.PresetQueryID   // unguarded
enriched.PresetName    = active.PresetName      // unguarded
enriched.WorkspacePath = active.WorkspacePath   // unguarded
if active.TriggeredBy != "" {                   // guarded
    enriched.TriggeredBy = active.TriggeredBy
}
```

A tracked execution is a *narrower* record than the session. A
workflow-builder background execution (`workshop_background`) legitimately
carries no preset and no workspace path — so for a session whose only running
execution was one of those, all three fields were overwritten with `""`.

`WorkflowName` survived only because its own assignment is guarded
(`if active.PresetName != ""`), and `TriggeredBy` survived for the same
reason. The three fields between them were not — an inconsistency inside a
single block, which is what makes this an oversight rather than a design.

The session had resolved its identity correctly at registration:
`server.go` sets `WorkspacePath` and derives `WorkflowName` from it in the
same guarded write, so a session cannot legitimately have one without the
other. Enrichment then destroyed half of it.

## Evidence

Captured live from `/api/sessions/active` while the pill was on screen (the
session had been running ~3h, driven by scheduled Pulse):

```json
{
  "session_id": "schedule-cron--42eca39a_1787009443095002000",
  "workflow_name":  "rtslatency",     // survived  (guarded assignment)
  "workflow_label": "rtslatency",     // survived  (guarded assignment)
  "agent_mode": "workflow_phase"
  // workspace_path:   ABSENT         // erased    (unguarded)
  // preset_query_id:  ABSENT         // erased    (unguarded)
  // preset_name:      ABSENT         // erased    (unguarded)
}
```

Every other scheduled session in the same response (`build-in-public`,
`upwork`, `tectonicusadaytrading`) carried a correct `workspace_path`; those
had no running `workshop_background` execution to trigger the clobber.

The name-without-path shape is itself the proof: nothing sets `WorkflowName`
for this path except a write that sets `WorkspacePath` alongside it.

## Why that broke the click

`findWorkflowPresetForSession` (`frontend/src/utils/workflowSessionRestore.ts`)
resolves a session's workflow by exactly the two fields that were erased:

```ts
const presetId = session.preset_query_id || runningWorkflow?.preset_query_id
if (presetId) { /* match by id */ }
const workspacePath = normalizeWorkspacePath(runningWorkflow?.workspace_path || session.workspace_path)
if (!workspacePath) return undefined      // ← both gone: bail
```

Returning `undefined` sends `openCanonicalActivitySession` down its fallback,
`openActiveSession`, which opens/activates a chat tab but never calls
`selectWorkflowPreset(preset)` — the one call that actually changes the
displayed workflow, and which only the `openWorkflowPresetPage` path makes.

Hence the exact reported behaviour: a Schedule tab opens under the
already-open workflow, the header never changes, and nothing is logged
because nothing errors.

## Fix

Guard the three assignments in both enrichment branches, matching the
`TriggeredBy` guard that was already there. A tracked execution that *does*
carry identity still wins — it is the more specific record; it simply can no
longer erase what the session already knew.

## Verification

Two tests in `cmd/server/polling_test.go`:

- `TestBuildActiveSessionInfoSummaryKeepsSessionIdentityWhenTrackedExecutionOmitsIt`
  reproduces the live shape (a running `workshop_background` execution with no
  identity over a session that has one) and asserts all four fields survive.
  Confirmed fail-before with the exact production symptom — `workspace_path = ""`
  — by reverting `polling.go` alone.
- `TestBuildActiveSessionInfoSummaryStillAdoptsTrackedExecutionIdentityWhenPresent`
  guards the opposite direction: a tracked execution carrying real identity
  must still override the session's. Passes both before and after, proving the
  guard did not turn into "never update".

Full run across `cmd/server`, `guidance`, `virtual-tools`, and
`step_based_workflow`: 6 pre-existing failures before and after (the PLAT-129
remainder), zero new.

## Not fixed here

- **The session is a zombie.** The same live payload shows five
  `synthetic-turn:steer-message-*` child executions stuck `running` since
  ~00:00 and ~00:29 UTC, `raw_session_status: "error"` alongside
  `phase: "running"`, and a session alive for over three hours. That is why
  the pill never disappears. It is the PLAT-116/117 orphan-settling family,
  not this bug — this ticket only restores the identity so the pill navigates
  correctly while it is shown.
- **The frontend has no fallback.** `findWorkflowPresetForSession` could match
  on `workflow_name`/`workflow_label`, which were correct throughout this
  incident, and would have degraded gracefully instead of silently opening the
  wrong workflow. Worth adding as defence in depth; not required now that the
  backend stops erasing the primary keys.
