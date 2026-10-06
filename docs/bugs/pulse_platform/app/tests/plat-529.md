[← app / tests](index.md)

# PLAT-529 — `formsKitAdoption.test.ts` "builds folders and browser settings from the kit" fails on main

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | app |
| Area | tests |
| Summary | fixed on main: the raw `<button>` added by PLAT-516 now uses the shared Button. |

| Coordination | Value |
|---|---|
| State | fixed on main (2026-10-05); not deployed |
| Priority | P3 |
| Date | 2026-10-05 |
| Owner | frontend-chat |

## Evidence

`npx vitest run src/components/workflow/formsKitAdoption.test.ts` on a clean `origin/main` worktree: 1 failed, 8 passed; `AssertionError: expected 1 to be +0`. The rest of `src/components` and `src/products` (1691 tests) passes.

## Cause and fix

The test pins "settings screens use the shared `Button`, no raw `<button>`". Commit `b8de134c1` (PLAT-516, Code browser setup) added a raw `<button>` for the "Choose a browser" cards in `BrowserWorkspacePanel.tsx`. Fixed in the code, not the test: the cards use the shared `Button` (ghost variant) with `h-auto justify-start whitespace-normal font-normal` so the three-line card layout is unchanged. Type-check clean; `src/components/workflow` 604/604.

## Left

Owner: check the "Choose a browser" cards look the same in the Browser pane (layout, hover, disabled while busy).

## Register notes

[PLAT-529](plat-529.md), fixed on main: the raw `<button>` added by PLAT-516 now uses the shared Button.
