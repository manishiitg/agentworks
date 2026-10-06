[← code / sandbox](index.md)

# PLAT-606: Code CLI shell made read-only by turn admission

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | code |
| Area | sandbox |
| Summary | Code chats' shell lost write access ('Permission denied' on every write) after a message to an already-running CLI |

## What happened

## Fix

## Left

## What happened

RTS, 2026-10-06, the owner's SDE Code project (`sde-private-3b40f2c4`, runs as slot01): "My command shell can't write to the project folder ... permission denied" (`sed: couldn't open temporary file ./sedXXXX: Permission denied`). File permissions were fine (group slot01, 2770/660). The shell guard had `WritePaths=[]` on every command since 2026-10-05 11:27 UTC.

Cause: `a82332284` (PLAT-442, 2026-10-05) made `resolveAgentProfileForQuery` set a read-only guard for every project profile while a turn is admitted, expecting the turn's real guard to be set later in the request. A message to a coding CLI that is already running is delivered to the live CLI before that point, so the read-only guard stayed and every later shell write was refused by the sandbox.

## Fix

`pinReadOnlyUnlessGuardCovers` keeps a guard that already grants writes inside the verified folder (adding the folder to reads if needed) and still pins a stale guard (a moved Crew's old folder) to read-only. Pinned by `TestAdmissionKeepsACurrentGuardAndPinsAStaleOne`.

## Left

Deploy RTS and Excellence (both likely affected for Code and Crew chats with a running CLI), then retry a write in the SDE Code chat. A chat whose CLI is still running keeps its read-only guard until its next full turn; starting a new turn (or New chat) resets it.
