# PLAT-540 — Move workflow Browser into the visible toolbar

State: fixed on main, not deployed. Date: 2026-10-05. Priority: P2. Requested by owner.

## Behavior

Move Browser from collapsed Ops into the always-visible Views group, after
Activity and before Ops. The existing icon opens the same Browser view and shows
its selected state. The view continues to own connection status, choices and
settings. Panel help, walkthrough and the consolidated browser guide now identify
its new location. Relay keeps its existing Graph-only primary group.

## Verification

- Browser-rendered check of the actual WorkflowToolbar: Browser is visible before
  Ops while collapsed; clicking opens browser in the workflow store and marks the
  button selected. Expanding Ops reveals Plan without a duplicate Browser button.
  Light/dark 1100px and 420px layouts stay within the viewport; Relay retains Graph.
  Network responses were stubbed; no live workflow was run.
- Existing toolbar placement, responsive layout and panel guide checks pass:
  24 checks across three files. No new unit tests added for this reversible move.
- Production frontend build and diff checks pass.

## Remaining

Deploy/reload the frontend to show the new toolbar placement.
