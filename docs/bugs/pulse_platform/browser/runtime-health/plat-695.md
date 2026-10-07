[← browser / runtime-health](index.md)

# PLAT-695: Runtime Health: show the Chrome extension's shared tabs

| Field | Value |
|---|---|
| State | closed |
| Priority | P3 |
| Product | browser |
| Area | runtime-health |
| Summary | Runtime Health shows 0 browser sessions while the Chrome extension has shared tabs; it lists only server-run browsers |

## What happened

## Fix

## Left

## Report

#agent_works, 2026-10-07 17:35 (Utkarsh, Excellence, Code project scraper): two tabs (Gmail, YouTube) are open through the Browser Bridge extension and the Browser panel says "Connected · 2 shared tabs", but Runtime Health → Browsers says "0 procs · 0 sessions / No browser sessions running".

## Cause

Runtime Health counts browsers the server runs (headless/CDP/agent-browser processes) and workflow shell processes. Extension tabs live in the person's own Chrome, so they are not counted. Correct but misleading.

## Fix (to do)

Add an "Your Chrome (extension)" row to Runtime Health → Browsers: connection state and shared tab count for the current project, from the same status the Browser panel uses; label the existing rows "On the server".

## Closed

2026-10-07: superseded. The Runtime Health panel was removed (owner decision, [PLAT-696](../../app/ui/plat-696.md)), so there is no panel to add the extension row to.
