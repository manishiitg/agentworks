[← Platform issue index](../../pulse_platform_issue_register.md)

# PLAT-409 — Ctrl+K omitted Relays

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
