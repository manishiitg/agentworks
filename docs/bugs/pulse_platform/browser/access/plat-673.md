[← browser / access](index.md)

# PLAT-673: Code and Crew browser stuck loading for read-only members

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | browser |
| Area | access |
| Summary | The Code/Crew browser stayed on its loading screen for members with the read-only workflow role: every browser status check returned 403 |

## What happened

## Fix

## Left

## Report

#agent_works, 2026-10-07 14:42 (Shashi, confirmed by Utkarsh, Excellence): switching on the browser in Code/Crew keeps showing the loading screen.

## Cause

`GET /api/browser/extension` (the pane's status check) returned 403 "Workspace write access required" for every call: 149 in ten minutes, about 11,400 on 6 Oct. `browserWorkspaceAccess` and live-browser control required the workflow-write role (`currentUserCanWriteWorkflows`) even for the caller's own Code/Crew project, and many members have the read-only workflow role.

## Fix

For a Code or Crew project, the browser belongs to the project owner (`canControlLiveBrowser`); the workflow-write role is required only for workflow browsers. Test: `TestCodeBrowserNeedsProjectOwnershipNotTheWorkflowRole`.
