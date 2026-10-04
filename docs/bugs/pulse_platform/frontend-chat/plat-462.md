[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-462 — Quick switching across running work, products and menus

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | frontend-chat |

## Change (owner request)

Ctrl+K / Cmd+K primarily switches between running workflows, Crews, Code and
Relays, while also providing navigation when the shared sidebar is hidden.

- Open the same switcher from every product, including Video, Dominion,
  SparkQuill and Vault.
- Keep running or waiting work before idle/recent work in the default list,
  with another running item initially selected when possible. Local streaming
  turns are included before the server session appears. Product/menu entries
  appear on search or under `@products` / `@menus`, keeping the default focused
  on work. Existing scopes and project/session restore paths remain available.
- A scheduled Crew with a view-only tab remains listed as an active session
  when no project row represents it; suppress only duplicates actually shown.
- Search product names or Activity, Schedules and triggers, Providers, Users
  and access, and Connect an AI agent (MCP). Apply the sidebar's product/role
  gates, and recheck navigation access on activation. Open shared pages on an
  allowed surface that renders them and clear previous overlays.
- Crew/Code and Vault builder restoration preserves a requested global page
  instead of dismissing it when asynchronous chat preparation completes.
- On changing to Auto-hide, show an eight-second dismissible shortcut hint
  with an Open quick navigation action. A reload of a saved preference does
  not repeat the hint.

## Validation

- Frontend type checking passed.
- Eleven targeted suites passed (36 tests): switcher directories, Relay
  routing, active scope, scroll request and cached opening, product/menu
  navigation and access gates, sidebar preference/hint, shared navigation,
  preference hook, and Vault restoration including global-page protection.
- Isolated browser preview of the real shell/switcher verified running work
  first, product search and Users navigation from another product, shortcut
  opening, and sidebar hint/reveal behavior.
- The wider run again encountered the pre-existing Work chat-selection
  initialization failure tracked in [PLAT-459](plat-459.md); 37 tests passed
  in its other eleven suites before the final running-priority refinements.

## Left

Deployment and verification on deployed product surfaces. PLAT-459 remains
open independently.
