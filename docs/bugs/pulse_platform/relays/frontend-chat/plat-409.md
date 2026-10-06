[← relays / frontend-chat](index.md)

# PLAT-409 — Ctrl+K omitted Relays

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | relays |
| Area | frontend-chat |
| Summary | fixed on main; deployment pending. |

| Coordination | Value |
|---|---|
| State | fixed on main; deployment pending |
| Date | 2026-10-04 |
| Owner | frontend-chat |

## Cause and implementation

The app shell's keyboard handler, custom-event handler and surface-change
close guard allowed AgentWorks, Crew and Code but excluded Relays. Add Relays
to all three. Reuse the existing workflow preset directory and navigation;
Relay entries carry a Relay label and badge and use workflowSurfaceForPreset
for active state and selection rather than forcing AgentWorks.

## Verification

Shared navigation contract, Relay switcher component regression, existing
switcher regressions and frontend TypeScript compilation passed.

## Remaining

Deploy the frontend through the normal release. The browser remained closed.

## Register notes

[PLAT-409](plat-409.md), fixed on main; deployment
pending. Include Relays in keyboard and custom-event entry points, label Relay
presets correctly, and open them through existing product-aware navigation.
