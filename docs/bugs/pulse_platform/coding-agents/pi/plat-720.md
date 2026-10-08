[← coding-agents / pi](index.md)

# PLAT-720: Model key checks fail after 30 s on slow services

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | coding-agents |
| Area | pi |
| Summary | On a slow model service (NVIDIA NIM free models) the key setup's Test key / Try it reported 'check failed' after 30 s while the server was still waiting (up to 50 s) |

## What happened

## Fix

## Left

## Seen

Excellence, 2026-10-08 ~10:50 CEST: a member's NVIDIA NIM key; Try it on `z-ai/glm-5.3-flash` showed "check failed". The server log has one `POST /api/byok/try-model` that took 30.3 s; NVIDIA's free models answer slowly.

## Cause

The browser's provider API client gives every request 30 s; the server waits up to 50 s for the service (`byok.go`).

## Fix

`byokTestKey` and `byokTryModel` wait 60 s.

## Also reported (not changed here)

"Cannot change the model": the chat's Models panel offers only the key's starred models; more are added through Browse <service> models, star, Save picks. The Crew still had the key's first pick saved. Waiting for the member to confirm whether that was the flow; if a pick should also switch the chat, change the browser so selecting a model saves it as a pick and selects it.
