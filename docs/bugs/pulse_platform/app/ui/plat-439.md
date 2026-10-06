[← platform / frontend-chat](index.md)

# PLAT-439 — Remove the product workspace inspector popup

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

## Request

The user reported an "Inspect Code workspaces" popup showing "Network Error"
on Excellence, describing it as appearing in Crew, then requested its removal.
It was an admin/reviewer read-only Code inspector opened from the shared
product selector; the current source admitted that entry only for Code.
The screenshot does not establish the cause of the network error.

## Done

- Remove the inspection menu entry, role selectors, popup state and mount from
  the shared Code/Crew surface.
- Delete the unused AdminCodeInspector component, including its automatic
  workspace-list request.
- Keep the authenticated, audited inspection API and Providers conversations
  overview, which has its own use of the Code review API. This change removes
  the reported popup, not the separate review service or permission rules.

## Verification

All 8 existing WorkSurface tests passed with Node's experimental web storage
disabled (`NODE_OPTIONS=--no-experimental-webstorage`), so jsdom supplies storage.
Frontend type check passed. Changed-file lint passed except the existing
`react-refresh/only-export-components` finding for `selectWorkChatTabIds`;
linting the unchanged HEAD source reproduces the same finding, and excluding
that one rule passes. No live Excellence deployment or acceptance check is
included in this change.

## Left

Deploy to Excellence and confirm the product selector no longer offers the
inspection entry. The removed popup's network failure was not diagnosed;
removal is the user's chosen resolution.

## Register notes

[PLAT-439](plat-439.md), fixed on main; deployment
pending. Remove the admin/reviewer inspection entry and popup from Code/Crew's
shared product surface after the user reported a Network Error on Excellence.
