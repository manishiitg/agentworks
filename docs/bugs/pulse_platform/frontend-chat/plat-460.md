[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-460 — Fixed or auto-hidden global navigation

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | frontend-chat |

## Change (owner request)

Users can choose whether the shared left navigation strip stays fixed or hides
until they move to the left edge.

- A pin control at the bottom toggles Fixed (default) and Auto-hide.
- The shared `ProductTopBar` persists `product_navigation_mode` through the
  existing storage-safe preference hook; products restore the same choice.
- Auto-hide reserves a six-pixel edge and reveals the 48-pixel strip over the
  workspace on hover or keyboard focus. The edge is also a focusable/clickable
  reveal target. Mouse focus alone does not hold the strip open after leaving.
- Navigation and its live monitor stay mounted in both modes. Flyouts remain
  positioned against the viewport (no transformed ancestor). The existing
  terminal-focus rule hides the complete shell including the reveal edge.

## Validation

- `npx tsc -b` passed.
- Four targeted suites, 12 tests passed: shared navigation, activity-monitor
  dropdown, persistent preference hook, and new ProductTopBar tests for saved
  mode/defaults and live children remaining mounted through both toggles.
- An isolated browser preview of the real shell verified Fixed → Auto-hide,
  edge hover without taking focus, keyboard reveal, menu position, reduced
  layout width, and Auto-hide restored after a reload.

## Left

Deployment and verification on deployed product surfaces.
