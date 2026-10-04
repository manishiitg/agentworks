[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-465 — Product icon on the first chat tab

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | frontend-chat |

## Change (owner request)

Show the owning product's mark beside the workflow/project name on its first
persistent chat tab. Workflow/Goals, Relays, Crew, Code and Vault use the same
product marks as the shared product selector. Keep the existing busy, ready
and completion status dot. History/run tabs keep their existing presentation.

The shared ProductSurfaceIcon also gives switcher product destinations and
browse icons their recognizable marks.

## Validation

- Shared tab tests cover each of the five marks while preserving the status
  dot, name and existing hover behavior.
- Frontend type checking and the nine-suite switcher/tab/Vault test run passed.
- Browser preview verified the Goals mark beside sales-outreach and its busy
  status dot.

## Left

Deployment and verification on deployed product surfaces.
