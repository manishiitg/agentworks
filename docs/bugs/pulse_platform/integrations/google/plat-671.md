[← integrations / google](index.md)

# PLAT-671: Google apps: say why Connect is disabled for members

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | integrations |
| Area | google |
| Summary | Members saw a disabled Connect Google account button in Goals, Relay and Crew with no clear reason |

## What happened

## Fix

## Left

## Report

#agent_works, 2026-10-07 14:23 (Saurabh, Confida): "connect google account this button only active in code. this is disable in goal, relay, crew".

Outside Code, Google accounts are shared and admin-managed (`canManageSharedGmail`); in Code they are the person's own. That rule is unchanged; members only saw a small grey line and a disabled button.

## Fix

When the panel is locked, the collapsed header gives the reason, the panel shows a clear notice ("…shared, so only an administrator can connect one. Ask an admin, or connect your own Google account in Code."), and the button reads "Only an admin can connect" (or "Only the owner can connect" in another person's Code).
