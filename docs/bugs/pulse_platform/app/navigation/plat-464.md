[← app / navigation](index.md)

# PLAT-464 — Clickable quick-switcher navigation

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | app |
| Area | navigation |
| Summary | fixed on `main`, not deployed: footer icons replace visible typed scope hints; browse lists, products and menu destinations also appear below work in the scrollable list. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | frontend-chat |

## Change (owner request)

Replace the switcher's visible `@` options with clickable icons, and make
browse lists and menu destinations available by scrolling and clicking.

- The default list keeps running work first, then idle/recent projects, then
  browse shortcuts, products, and allowed menus at the bottom.
- Footer icons browse Running work, All workflows, All Relays, All Crews,
  All Code projects, All chats, and All products. A named filter chip replaces
  typing a scope; its clear button restores the full list. Legacy scopes still
  work for existing internal callers, without footer syntax hints.
- Both footer icons and scrollable entries open Activity, Schedules and
  triggers, Providers, Users and access, and MCP Connect. Vault additionally
  offers its audit and MCP endpoint icons when the current administrator can
  access it. Product and role gates are rechecked before activation.
- Workflow and Relay browse lists are separate. Changing the browse filter
  resets the list to the top and focuses search without closing the switcher.
- The footer wraps at narrow widths. Its buttons have accessible names,
  tooltips and keyboard focus indicators. This is shared React web UI, used
  by the browser and desktop builds; it has no macOS-specific dependency.

## Validation

- Nine targeted suites, 43 tests passed, covering browse filters, footer
  routes, scrollable menu routes, access gates, running-work order, existing
  directory/routing/scroll behavior, Vault routing, and product tab marks.
- Frontend type checking passed.
- Isolated web browser preview verified the icon footer, separate workflow
  and Relay lists, scroll-to-menu navigation, and narrow-width layout.

## Left

Deployment and verification on deployed web and desktop builds.

## Register notes

[PLAT-464](plat-464.md), fixed on `main`, not deployed:
footer icons replace visible typed scope hints; browse lists, products and
menu destinations also appear below work in the scrollable list.
