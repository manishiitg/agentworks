[← platform / frontend-chat](index.md)

# PLAT-278 — The agent could describe the workspace pane but never point at it

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | platform |
| Area | frontend-chat |
| Summary | The user asked whether workspace-view tools know about “Needs your decision” |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; live verification pending` |
| Last synchronized | `2026-09-05` |

## 2026-09-05 — Decision cards discussed and answered in chat

The user asked whether workspace-view tools know about “Needs your decision”
and why saving an answer through chat requires a manual refresh or another
instruction to mark it.

Inspection confirmed that `pulse` was already a supported view and “Ask in
chat” already supplied exact decision/option IDs and named
`answer_human_input_request`. The missing connections were discoverability and
timing: the view description did not mention decision cards, cards refreshed
at turn completion rather than at the saved tool receipt, and `PulseWorkspace`
did not listen for the existing refresh event for its related projections.

**Codex correction implemented and tested; deployment and live verification pending:**

- Describe decision cards, answers, and application history under `pulse` in
  the opening tool and workspace guidance. Chat reads the current typed record
  and saves a clear final answer in that turn without a second “mark it” request.
- A successful decision mutation receipt for the visible workflow triggers
  the existing lightweight refresh event while chat continues. Discussion,
  starts, failed saves, and another workflow's receipt do not trigger it.
  Turn completion remains a fallback.
- Pulse listens for that event and reloads its related projections without
  resetting the selected filter. Decision cards retain the existing silent
  background refresh behavior. No report iframe reload or polling was added.
- Answered remains distinct from applied/consumed.

Validation: receipt filtering tests, a rendered decision-panel test that moves
an answer out of pending before chat completion, Pulse filter-preservation
tests, existing chat-message/report-stability tests, TypeScript compilation,
Go workspace-view/human-input tests, and guidance rendering pass. A live
browser/server conversation after rebuild remains to be verified.

- **Priority:** builder UX, severity medium — the workflow page has 21
  workspace views and the agent could not open any of them. It told the
  user where to click instead ("open the Report tab"), which is the one
  thing the prompt's own voice section tells it not to do.
- **Origin:** raised by the user while looking at the workflow toolbar:
  "i want the agent to be aware of this toolbar.. and able to like show
  any toolbar content", then extended across the session — deeper
  targeting, a refresh that is distinct from opening, an activity row for
  runs, and the chat chrome around it.

## Problem

The toolbar above the workflow chat switches the right-hand pane between
21 views (Views / Pulse / Setup clusters). Nothing connected the agent to
it: no tool, no event, no reference doc. Three consequences:

1. The agent narrated navigation instead of performing it.
2. Having changed what a view shows — rewriting `db/reports/index.html`,
   writing DB rows, adding a schedule — it had no way to make the open
   view reflect that, so the user saw a stale pane.
3. It could not point *inside* a view: "the fractions step failed" with no
   way to put that step on screen.

Separately, the chat surface around this had its own gaps: a step run
disappeared into a collapsed "N tool calls" chip, the composer switched
off mid-turn even though steering was supported, and all three toolbar
clusters could be expanded at once.

## Resolution

**Tools** (`cmd/server/workflow_view_tool.go`, registered for every
workflow phase in `installWorkflowPhaseTools` — opening a view mutates
nothing, so read-only sessions get them too):

- `open_workspace_view(view, target?)` — puts a view on screen.
- `refresh_workspace_view(view, target?)` — reloads what a view shows;
  opens it first if it is not up. Distinct from open on purpose: opening
  the view already on screen is a no-op, reloading it is not.
- Both emit a `workflow.view` presentation carrying `action` and optional
  `target`. The Go view list mirrors `workspaceViews.ts`; a vitest reads
  the Go file and fails if the two ever drift.

**`target`, per view** — report: a top-level tab, delivered into the report
iframe as `report.focus` plus a `report:focus` event (`reporting-policy.md`
documents the listener; a report that ignores it stays put). flow: a step
id, focused on the canvas via the existing `focusStep`. files: a path,
opened in the pane rather than just showing the tree. database,
execution-logs, schedules: documented meanings. A view with nothing to
focus ignores the target, so passing one is always safe.

**Frontend** — `useWorkflowViewPresentations` (mounted in `WorkflowLayout`)
turns the events into the same `openWorkspaceView` the toolbar buttons
call, so a closed pane opens. `workspaceViewRefreshToken` remounts an open
inspector; the report re-reads its HTML through its existing refresh event.

**Reference doc** — `builder-reference/references/workspace-views.md`: what
each of the 21 views shows, when to open it, and what `target` means. The
workshop prompt's "Talking to the user" bullet points at it.

**Chat surface, same session:**

- A run of a step, the workflow, or an evaluation renders its own compact
  activity row (spinner → tick/cross, step and group named) instead of
  folding into a tool chip.
- A tool-call *end* whose start was not retained (restored trace, bridge
  call across a turn boundary; with or without a `tool_call_id`) folds into
  the tool batch instead of rendering a full "Command Completed" shell card.
- Send, the command menu and attach stay enabled during a run. Sending
  mid-turn was already implemented — `routeSubmit` queues and the
  live-delivery effect steers it in — but the button was not rendered, so
  the capability was invisible. Server/skill/browser pickers stay disabled:
  changing what the agent has mid-run is incoherent.
- The emerald rail down every agent turn and the emerald AGENT label are
  gone platform-wide; the tick/cross marks keep colour, where it means
  something.
- The toolbar's three clusters open one at a time.

## Verification

- `cmd/server` — `TestOpenWorkspaceViewToolOpensAKnownViewAndRefusesOthers`
  covers both tools, the unknown-view error listing real views, target
  trimming, a blank target never reaching the payload, and open/refresh
  producing distinct presentation ids.
- `frontend` — the Go/TS view-list mirror test; transcript tests for the
  run activity row and for orphan tool ends (with and without an id).
- Not yet verified live: the `report:focus` handoff needs a report whose
  HTML implements the listener, which no existing report does yet.

## Remaining

- `database`, `execution-logs` and `schedules` accept a `target` in the
  contract but their components do not act on it yet; they open the view
  and ignore the target. Wiring each is a small controlled-selection prop.
- No existing report implements `report:focus`; until one does, a report
  tab target is silently ignored.
