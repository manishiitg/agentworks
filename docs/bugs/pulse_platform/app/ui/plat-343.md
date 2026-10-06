[← platform / frontend-chat](index.md)

# PLAT-343 — Workflow switch briefly rendered the previous workflow transcript

| Field | Value |
|---|---|
| State | deployed |
| Priority | P0 |
| Product | app |
| Area | ui |
| Summary | prevents the previous workflow's transcript from remaining visible after a different workflow is selected. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `deployed to RTS; focused regressions and service health green` |
| Last synchronized | `2026-09-21` |
| Priority | `P0 chat correctness / navigation` |

## Report

After selecting another workflow, the header changed immediately but the chat
pane continued showing the old workflow's messages while the new workflow's
session and tab were being resolved. On a slow server this made unrelated
messages appear to belong to the newly selected workflow.

## Cause

Workflow selection is synchronous, but `openWorkflowPresetPage` awaits active
sessions, running-workflow lookup, and tab restoration before activating the
destination tab. The chat renderer treated the still-active source tab as valid
during this interval and had no ownership check between the selected workflow
and the active workflow tab.

## Fix

The workflow surface now requires the active tab's `presetQueryId` to match the
currently selected workflow. A mismatch renders the existing loading surface
immediately, even if the old tab has content. Once destination activation is
atomic and the IDs match, the destination transcript renders normally.

This does not clear, merge, or mutate either workflow's events; it only prevents
the source workflow from being displayed under the destination workflow's UI.

## Verification

- Resolver regression coverage verifies that source content cannot override a
  workflow-ownership mismatch.
- Existing workflow surface resolver coverage remains green.
- Deployed to RTS in release `6c47129-20260921082037`; public HTTP and service
  health checks passed. Interactive workflow-switch verification remains.

## Register notes

[PLAT-343](plat-343.md) prevents the previous
workflow's transcript from remaining visible after a different workflow is
selected. The workflow chat surface now verifies that the active tab belongs to
the selected workflow and displays its loading state during asynchronous tab
resolution. Focused resolver tests and the frontend build pass; deployment and
service health are green in RTS release `6c47129-20260921082037`; interactive
acceptance remains.
