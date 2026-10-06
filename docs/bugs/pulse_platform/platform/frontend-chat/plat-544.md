[← platform / frontend-chat](index.md)

# PLAT-544 — Plan reload leaves variables, connections and open details stale

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | platform |
| Area | frontend-chat |
| Summary | fixed on main, not deployed. |

| Coordination | Value |
|---|---|
| State | fixed on main; not deployed |
| Date | 2026-10-05 |
| Owner | frontend-chat |

## Report and cause

The owner reported that the Plan reload control did not appear to refresh the
graph. The current button does fetch `plan.json` and `step_config.json` afresh,
but source inspection found four gaps:

- The variables manifest shown on the canvas was not reloaded by that button.
- Graph reconciliation compared edge IDs only, missing updated route labels,
  handles, selected-route styling and other content with unchanged IDs. Its node
  comparison also omitted non-step card content and node type.
- An open step-details panel retained the originally clicked node object.
- Step update/add/delete methods called the cached loader after persisting the
  change, so they could immediately show the pre-write plan again.

The attached screenshot alone does not identify which gap the owner encountered.

## Done

- Manual refresh reads variables alongside plan/config and updates the displayed
  manifest and execution-options store. Variable-read errors prevent a success
  toast; responses from a workspace left during the read are ignored.
- Compare complete generated node data/type/position and edge content. Existing
  saved-position and viewport restoration remains in effect.
- Store the selected step by workspace and node ID and resolve its current data
  from the graph, including closing details when the node disappears.
- Use the fresh-read path after step writes, retaining the displayed graph while
  the read completes.

## Verification

- Seven focused frontend checks passed, including a save/add/delete cache
  regression and changes to cards and routes retaining their IDs and positions.
- TypeScript project checks and targeted lint passed.
- Browser check used the production `WorkflowCanvasInner`, production plan hook
  and API transport, React Flow and an isolated HTTP fixture in the owned
  worktree. After changing the fixture on disk, clicking the actual Refresh plan
  button updated the title, variables card (`ACCOUNT OLD` → `ACCOUNT NEW`) and
  already-open description (`Old instruction` → `Fresh instruction from the
  server`). This did not edit any live workflow or use the deployed backend.

## Left

Release the frontend and verify the owner's workflow on its deployed instance.

## Register notes

[PLAT-544](plat-544.md), P2, fixed on main, not deployed. Reload all displayed plan inputs, reconcile card/connection content, refresh open details and bypass cached data after step writes. Verified with the actual canvas button against an isolated local HTTP fixture.
