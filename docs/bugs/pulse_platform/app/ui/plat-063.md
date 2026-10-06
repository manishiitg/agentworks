[← platform / frontend-chat](index.md)

# PLAT-063 — the report pane flashed mid-run because a view flip or outer render reloaded it

| Field | Value |
|---|---|
| State | open |
| Priority | P3 |
| Product | app |
| Area | ui |
| Summary | P3 (cosmetic; no data loss) |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code`, Codex follow-up |
| Ticket state | `done` — user-confirmed on the live UI |
| Last synchronized | `2026-08-12` |

- **Priority:** P3 (cosmetic; no data loss)
- **Owner:** workflow canvas mounting / workspace-view sync
- **Reported as:** "something refreshes the reporting page… when a step runs"

## Problem

`WorkflowCanvasWithProvider` (`WorkflowCanvas.tsx:3050`) returns **three
different component types** depending on view state:

```js
if (workflowWorkspaceView === 'files')       return <WorkflowFilesCanvasInner/>
if (viewMode === 'report'|'log'|'soul')      return <WorkflowReportCanvasInner/>
return <ReactFlowProvider><WorkflowCanvasInner/></ReactFlowProvider>
```

React destroys and rebuilds a subtree when the component *type* at a position
changes. So changing `workflowWorkspaceView` does not re-render the pane — it
**unmounts and remounts** it.

`WorkflowLayout.tsx:1231` flipped that value in both directions:

```js
if (!workspaceMinimized && workflowWorkspaceView !== 'files')
    setWorkflowWorkspaceView('files')          // report unmounts
if (workspaceMinimized && workflowWorkspaceView === 'files')
    setWorkflowWorkspaceView(canvasViewMode)   // report remounts
```

Two remounts back to back — a visible flash.

## Why it read as a data refresh but was not

The rebuilt viewer immediately recreates its report document, so the failure
looks like a data refresh even when no user-invoked refresh occurred. That is
why instrumenting all four report events (`workflow-report-data-stale`,
`workflow-report-refresh-requested`, the preference and export events) produced
**zero logs** while the flashing continued. The negative probe result was the
decisive clue: whatever it was, it was not the explicit refresh path.

## Two wrong theories worth recording

1. **`orchestrator_end` fires per step.** It does not — there is exactly one
   caller (`workflow_orchestrator.go:678`, `execution_mode:"workflow_execution"`).
   Acting on this would have been actively harmful: the proposed fix was to
   narrow `EVENT_TYPES.COMPLETION` to `workflow_end`, and…
2. **…`workflow_end` and `batch_execution_end` are never emitted by any Go
   code.** Both appear in the frontend `COMPLETION` list; only `orchestrator_end`
   actually fires. Narrowing to `workflow_end` would have meant *nothing ever
   completes*. **This is a real latent defect and is NOT fixed by this ticket** —
   completion detection rests entirely on one event with two dead entries beside
   it that look like redundancy and provide none.

## Implementation (2026-08-09)

`WorkflowLayout.tsx:1231` — the "un-minimizing the workspace switches to Files"
rule is now skipped while a preview view (`report`/`log`/`soul`) is open, so the
preview is never torn down to show Files and immediately restored.

**Tradeoff, accepted:** un-minimizing the workspace while on a preview view no
longer auto-switches to Files; the user clicks Files. One-line revert if that
reads wrong in use.

Frontend-only — no Go rebuild, no server restart.

## Verification

`npx tsc --noEmit` clean, and **the user confirmed on the live UI that the
flashing stopped**. That is why this ships `done` rather than the usual
`runtime_reverify`.

## Follow-up worth doing separately

- The type-swap in `WorkflowCanvasWithProvider` remains a standing hazard: *any*
  future view-state flip will remount rather than re-render. Rendering one
  component type that switches its inner content would remove the whole class.
- The dead `workflow_end` / `batch_execution_end` entries in `EVENT_TYPES.COMPLETION`
  (see above) deserve their own ticket.

## Second root cause and fix (2026-08-11)

The view-flip fix stopped the **component-tree** remount. It did not protect the
HTML report iframe from an ordinary render of its still-mounted parent. The
iframe accepts the report document through React's `srcDoc` prop; Chromium can
treat a repeated assignment as a document navigation. Live terminal, Pulse,
and human-input status updates therefore made the report visibly reload even
when the selected workspace view never changed.

`HtmlWidgetFrame.tsx` now memoizes the iframe component. It receives a numeric
`refreshToken` only from `ReportViewer`'s explicit Refresh action. A new page
document, a changed data API, or that token change emits `report:data`; unrelated
parent renders do none of those things and leave the frame/document untouched.

**Verification:** in the live Electron app, the report iframe remained present
across a seven-second background polling interval while terminal activity
continued. No report file changed on disk and no explicit report-refresh event
was dispatched. `ReportViewerStability.test.ts` pins the explicit-refresh and
memoization contract. This is frontend-only; no server restart is required.

## Third root cause: polling and the scroll-repair workaround (2026-08-12)

The remaining visible jitter was present even before a report document loaded,
so it could not originate in the iframe. The decision panel above the document
polls pending human inputs every five seconds. Every background check toggled
its public `loading` state and replaced the input array even when the payload was
unchanged, repainting the report shell.

An earlier defensive scroll workaround then amplified that repaint: it observed
an outer scroll reset, assigned `scrollTop` in `requestAnimationFrame`, and the
assignment generated another scroll event after its guard cleared. Live browser
logs captured dozens of `unexpected scroll reset restored` events in under one
second. This was the apparent continuous refresh.

Background decision checks are now silent, preserve the last good result across
transient failures, and retain the previous array identity when data is
unchanged. The corrective scroll loop was removed; the stable mounted pane now
uses native scrolling. After hot reload, several polling cycles produced no new
scroll-repair events, report remounts, or iframe loads.

Verification: `ReportViewerStability.test.ts` (5/5) and `npx tsc -b` pass.
