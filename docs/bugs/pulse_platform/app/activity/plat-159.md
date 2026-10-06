[← app / activity](index.md)

# PLAT-159 — a session's display label was set from its workspace folder name, not its configured workflow.json label, so the Global Activity Monitor could show a running session as a different workflow

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | app |
| Area | activity |
| Summary | P2 — no data loss and no wrong execution, but a live pill |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — fail-before/pass-after; live reverify pending |
| Last synchronized | `2026-08-20` |

- **Priority:** P2 — no data loss and no wrong execution, but a live pill
  telling the user "this other workflow is running" was in fact reporting on
  the workflow they were already looking at, under a different name. Directly
  undermines the Global Activity Monitor's one job: telling the user what
  else is running.
- **Owner:** `cmd/server/server.go` (`handleQuery`'s `workflow_phase`
  scheduler/cron identity-setting block), `cmd/server/polling.go`
  (`workflowDisplayLabel`)
- **Related:** [PLAT-131](../navigation/plat-131.md) — same UI surface (Global Activity
  Monitor), same general shape (a session's identity fields don't carry the
  name the frontend needs), different mechanism. PLAT-131 fixed enrichment
  *clobbering* already-correct fields; this ticket is about the *initial*
  value being wrong in the first place, for a different code path
  (scheduler/cron sessions using `req.SelectedFolder`, not the
  preset-DB-resolved path PLAT-131 covered).

## How it surfaced

Reported live: browsing the `twitter-automation` workflow (its own breadcrumb
correctly reads "twitter-automation"), the Global Activity Monitor pill in
the same header showed a separate, active-looking "social-media" item — as
if a different workflow were running in the background. It was not a
different workflow: `twitter-automation` is the configured display label for
the `Workflow/social-media` folder, and the pill was for the same session the
user was already looking at, running the currently-open Schedule tab.

## Root cause

`workflow.json` for this workflow sets:

```json
"label": "twitter-automation"
```

A workflow's stable folder identity (`social-media`) and its configured
display label (`twitter-automation`) are allowed to differ — that's the whole
point of `label` existing as a separate field, and `workflow_manifest.go`
already has a real resolver for it (`ReadWorkflowManifest` applies `m.Label`,
falling back to the folder name only when the manifest itself has none).

`handleQuery`'s `workflow_phase` scheduler/cron path
(`cmd/server/server.go`, resolving via `req.SelectedFolder` rather than the
preset DB) never consulted that resolver. It derived the session's identity
purely from the folder path:

```go
workflowName := workflowNameFromWorkspacePath(resolvedWPath) // "social-media"
sess.WorkflowName  = workflowName
sess.WorkflowLabel = workflowName   // should have been "twitter-automation"
sess.PresetName    = workflowName   // should have been "twitter-automation"
```

All three identity fields got the same folder-derived value. The frontend's
`GlobalActivityMonitor.tsx` resolves a pill's title through
`session.preset_name || session.workflow_name || session.workflow_label` —
every candidate in that chain had been overwritten with the folder name, so
there was nothing left carrying the real label no matter which field won.

## Fix

`cmd/server/polling.go` gets a new `workflowDisplayLabel(workspacePath,
manifest) string`: the manifest's `Label` when non-empty, the folder name
otherwise — the same fallback semantics `ReadWorkflowManifest` already
applies internally, extracted so this call site (and any other future one)
doesn't have to duplicate that decision inline.

`server.go`'s identity-setting block now loads the manifest *before* setting
session identity (previously the manifest load happened in a separate,
later block purely for LLM-config resolution) and uses
`workflowDisplayLabel` for `WorkflowLabel`/`PresetName`, while
`WorkflowName` keeps the raw folder-derived value — it is a stable
identifier, not a display string, and other code may reasonably depend on it
matching the folder.

## Test coverage

`workflow_display_label_test.go`: `workflowDisplayLabel` prefers the
manifest label when present, falls back to the folder name when the
manifest's label is empty, and falls back to the folder name when there is
no manifest at all. Fail-before/pass-after: temporarily reverted the
function to unconditionally return the folder name, confirmed the
label-preference test failed with the exact live symptom (folder name
instead of the configured label), restored it, confirmed all three pass.

No end-to-end test drives the full `handleQuery` scheduler/cron path — that
function has no existing harness for isolated testing (same gap PLAT-130
already noted for this file). The fix is scoped to the extracted, directly
tested function plus a minimal reordering at the one call site that had the
bug.

## What this does not fix

- Any other place that might independently derive a workflow's display name
  from its folder path instead of going through
  `workflowDisplayLabel`/`ReadWorkflowManifest`'s label resolution was not
  audited. This ticket fixes the one call site that reproduced live.
- Live reverify: confirm on the next real scheduler/cron-triggered
  `workflow_phase` session that the Global Activity Monitor pill shows the
  configured label, not the folder name.
