# PLAT-347 — RTS deployment blocked on stale logical active-session state

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | platform |
| Area | performance |
| Summary | records the RTS release that could not activate because `/api/health` reported `active_sessions: 1` with zero in-flight work — retained chats keep that aggregate non-zero, so it was never a safe gate. |

**Category:** Performance  
**Severity:** P1  
**Status:** Fixed  
**Observed:** 2026-09-21 on RTS

## Problem

The production release was fully built and verified but activation waited on
`/api/health` reporting `active_sessions: 1`, even though
`in_flight_requests: 0` and no user work was observable. Retained chats and
stale logical ownership can keep that aggregate non-zero, so it is not a safe
deployment gate.

The old policy could wait for an hour and then abandon a valid release. It also
did not prevent new turns from starting during the wait, so it was neither a
true drain nor a deterministic zero-downtime handoff.

## Decision

RTS production activation is explicitly breaking. After the build, tests, and
release-asset checks pass, deployment swaps the release and restarts the three
user services immediately. Clients reconnect to the new runtime. The deploy no
longer uploads or consumes `DRAIN_TIMEOUT_SECONDS`.

Build isolation remains unchanged: a failed build never changes the active
release, and only RTS user services are restarted.

## Register notes

[PLAT-347](plat-347.md) records the RTS release that
could not activate because `/api/health` reported `active_sessions: 1` with
zero in-flight work — retained chats keep that aggregate non-zero, so it was
never a safe gate. Decision: RTS activation is explicitly breaking (swap and
restart user services immediately; clients reconnect), and the deploy no
longer uses `DRAIN_TIMEOUT_SECONDS`. Fixed.
