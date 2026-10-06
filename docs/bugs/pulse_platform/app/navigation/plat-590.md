[← app / navigation](index.md)

# PLAT-590: Refresh lands on Human actions instead of the saved view

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | app |
| Area | navigation |
| Summary | A workflow with pending decisions opened Human actions on every refresh, over the saved view |

## What happened

Owner, 2026-10-06: refreshing the page always opened the Human actions panel instead of where they were.
`useDefaultHumanActionsView` opens Human actions once when a workflow opens with pending decisions, unless the view
changed while the count loaded. A page refresh counts as opening, and [PLAT-551](plat-551.md) restores the saved view
before the count arrives, so the hook saw "unchanged" and replaced the restored view. Upwork always has pending
decisions, so it happened on every refresh.

## Fix

The default applies only when the workflow has no saved view (`hasSavedWorkflowWorkspaceView`). The toolbar badge
still shows pending decisions. A test pins the refresh case next to the two existing ones; `tsc -b` is clean.

## Left

- Confirm live: choose a view in Upwork, refresh, stay on it.
