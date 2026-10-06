[← platform / frontend-chat](index.md)

# PLAT-460 — Fixed or auto-hidden global navigation

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | app |
| Area | ui |
| Summary | fixed on `main`, not deployed: the bottom pin toggles Fixed and Auto-hide; the saved choice applies to shared product navigation. |

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
- Auto-hide uses a six-pixel edge target and reveals the 48-pixel strip over the
  workspace on hover or keyboard focus. The edge is also a focusable/clickable
  reveal target. Mouse focus alone does not hold the strip open after leaving.
- [PLAT-461](plat-461.md) removes the reserved width and hidden shadow;
  [PLAT-462](plat-462.md) adds the shortcut hint and global quick navigation.
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

## Register notes

[PLAT-460](plat-460.md), fixed on `main`, not deployed:
the bottom pin toggles Fixed and Auto-hide; the saved choice applies to shared
product navigation. Hidden navigation reveals at the left edge and keeps its
live monitor mounted.
