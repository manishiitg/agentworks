[← app / ui-control](index.md)

# PLAT-664: Agent cannot drive the panel when the workflow is open in more than one tab

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | app |
| Area | ui-control |
| Summary | With the same workflow open in several browser tabs, the agent's UI control refuses every action with ambiguous_client |

## What happened

## Fix

## Left

## Report

#agent_works, 2026-10-06 16:48 (Confida): the agent could no longer open views (dashboard, playbooks); it answered with `ambiguous_client` and asked the user to close extra windows. The user had three Confida tabs open.

## Fix

Target the tab the user last sent a message from (or the focused one) instead of refusing when several are attached; refuse only when none can be chosen.

## Done

Each tab's binding records when it last became visible. A UI action goes to the visible tab that became visible last, else to the tab last seen visible; `ambiguous_client` remains only when no tab can be told apart (none ever visible, or an exact tie). Test: `TestUIControlDisconnectedAndAmbiguousClients`.
