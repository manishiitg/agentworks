[← platform / frontend-chat](index.md)

# PLAT-408 — Ops starts collapsed in the workflow header

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | app |
| Area | ui |
| Summary | fixed on main; deployment pending. |

| Coordination | Value |
|---|---|
| State | fixed on main; deployment pending |
| Date | 2026-10-04 |
| Owner | frontend-chat |

## User decision and implementation

Keep the Ops label and start its shared workflow/Relay toolbar group collapsed.
Clicking Ops expands or collapses its existing icons inline. Views and icon-only
Setup remain expanded. The shared group supplies the toggle and aria-expanded.
This supersedes the always-open Ops choice in PLAT-398.

## Verification

Updated the existing toolbar regression to check the initial collapsed state and
toggle wiring. Frontend TypeScript compilation and toolbar regressions passed.

## Remaining

Deploy the frontend through the normal release. The running apps were untouched.

## Register notes

[PLAT-408](plat-408.md), fixed on main; deployment
pending. Keep the Ops label and expand its existing icons on click. Views and
icon-only Setup remain open.
