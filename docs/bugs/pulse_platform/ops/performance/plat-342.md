[← platform / performance](index.md)

# PLAT-342 — Ctrl+K quick switcher incurred avoidable first-open latency

| Field | Value |
|---|---|
| State | deployed |
| Priority | P1 |
| Product | ops |
| Area | performance |
| Summary | removes the quick switcher's first-use chunk fetch, renders its cached session list immediately, and stops forcing an active-session request or resetting the overlay when preset data changes. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `deployed to RTS; regression test and service health green` |
| Last synchronized | `2026-09-21` |
| Priority | `P1 performance / navigation` |

## Report

Opening the quick switcher with Ctrl+K was visibly slow, especially on its first
use. The switcher is core navigation and should appear from local state without
waiting for code or network work.

## Cause

The application split the small switcher into a lazy chunk, so first use could
pay an extra production asset round trip. Its open effect also forced the
active-session endpoint and depended on `workflowPresets`, causing the reset
effect to rerun when presets changed while the overlay was open.

## Fix

- Bundle the quick switcher with the application shell so Ctrl+K can render it
  immediately.
- Paint from the already-subscribed session cache and use the store's normal TTL
  refresh instead of forcing a request on every open.
- Reset query/selection only when the overlay opens or its explicit initial
  query changes.

## Verification

- A focused performance contract test prevents reintroducing lazy loading,
  force-refresh-on-open, or the unstable preset dependency.
- Deployed to RTS in release `6c47129-20260921082037`; public HTTP and service
  health checks passed. Interactive production timing verification remains.

## Register notes

[PLAT-342](plat-342.md) removes the quick switcher's
first-use chunk fetch, renders its cached session list immediately, and stops
forcing an active-session request or resetting the overlay when preset data
changes. The focused performance contract and frontend build pass; deployment
and service health are green in RTS release `6c47129-20260921082037`;
interactive timing verification remains.
