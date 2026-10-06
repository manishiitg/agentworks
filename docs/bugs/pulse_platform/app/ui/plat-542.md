# PLAT-542 — Show browser connection health in workspace toolbars

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | app |
| Area | ui |
| Summary | fixed on main, not deployed. |

State: fixed on main, not deployed. Date: 2026-10-05. Priority: P3. Requested by owner.

## Behavior

Workflow, Code and Crew Browser buttons show a green dot for a live connection,
independently of the pane being viewed. The tooltip and accessible description
say Connected or Not connected; selecting Browser retains its existing highlight.

Read-only discovery polls the current product/workspace every 2.5 seconds and on
window focus. A selected extension is authoritative, including a connection with
zero shared tabs: an offline extension cannot borrow a managed browser's green
status. Otherwise, active live sessions count; completed Playwright recordings do
not. Errors and workspace changes clear stale status. Discovery never launches a
browser, adopts a tab or changes debugger access. Relay remains unchanged.

## Verification

- Browser-rendered checks using the actual toolbar components passed for
  Workflow, Code and Crew: connect, disconnect, reconnect, zero shared tabs,
  selected-browser isolation, managed sessions, replay exclusion, request errors
  and a delayed response after switching workspaces. Clicking still opens Browser.
  Light/dark 1100px and 420px layouts fit the viewport. Relay stays Graph-only;
  discovery does not call the browser-start endpoint. Responses were stubbed;
  no user browser or live workflow was controlled.
- Existing toolbar placement, responsive layout and Code/Crew toolbar tests:
  10 checks across three files passed. No new unit tests for this reversible UI.
- Production frontend build and diff checks passed.

## Remaining

Deploy/reload the frontend to display the indicator.

## Register notes

[PLAT-542](plat-542.md), P3, fixed on main, not deployed. Green Browser status dot and accessible connection label in Workflow, Code and Crew; read-only scoped discovery distinguishes selected extensions, live browsers and completed replays.
