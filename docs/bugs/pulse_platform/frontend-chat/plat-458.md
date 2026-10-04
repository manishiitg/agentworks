[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-458 — Show the workflow or project name on the persistent chat tab

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | frontend-chat |

## Change (owner request)

The persistent chat tab used the generic label "Chat", leaving its workflow,
Crew or Code identity invisible when the workspace pane was closed.

- Workflow and Relay builder tabs use the name from their own workflow preset.
  An unnamed or still-loading preset falls back to "Workflow" or "Relay".
- The shared project surface (Crew, Code and Vault) uses the selected project's
  identity name, then its title, then the product noun.
- These names update from current project/preset state, including renames.
  Long names retain the shared pill's truncation and show the full name on hover.
- Only the visible persistent tab label changes; conversation identity and
  read-only history/run tab labels are preserved.

## Validation

- `npx tsc -b` passed.
- Existing shared-pill, workflow strip selection, runtime projection and Work
  tab tests passed: four suites, 36 tests.
- `workChatTabSelection.test.ts` fails during module initialization, before any
  tests execute. Reproduced with the unmodified main version of WorkSurface;
  tracked separately in [PLAT-459](plat-459.md).

## Left

Deployment and live verification.
