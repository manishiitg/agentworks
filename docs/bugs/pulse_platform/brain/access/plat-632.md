[← brain / access](index.md)

# PLAT-632: Admin's projects limited to explicitly granted Brain folders

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | brain |
| Area | access |
| Summary | An admin's Crew saw only folders the admin had explicit grants on: the output-audience check ignored the admin's implicit Owner role |

## What happened

## Fix

## Left

## What happened

RTS, 2026-10-06: the gptlive1 Crew (owner: admin) reported Editor on `RTS/Latency` only and role None on `RTS`; nothing else in Brain was visible. Not a binding (none exist, PLAT-628). Cause: a project's Brain view is limited to folders every output-audience member can read, and that check (`boundRole`) used each member's explicit grants only. The admin's Owner role everywhere is implicit, and their only explicit grant was Owner on `RTS/Latency` (from the rtslatency import), so the admin's own Crew was narrowed to that folder.

## Fix

`BindingPolicy.Admins`: audience members who are enabled administrators (filled by the server from the user directory) never narrow a project; other members still do. Regression test `TestAdminInAudienceDoesNotNarrowProjectBrain`. Two server tests that asserted a project could not leave its bound folder were removed (bindings were removed in PLAT-628). Not deployed.
