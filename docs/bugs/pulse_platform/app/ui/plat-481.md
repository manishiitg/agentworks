[← platform / frontend-chat](index.md)

# PLAT-481 — The frontend test suite is red on main (release and DMG builds fail)

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | app |
| Area | ui |
| Summary | fixed on main: the circular import and the stale contract tests are fixed; the suite is green. |

| Coordination | Value |
|---|---|
| State | fixed on main: `npm test` is green (482 files passed, 2802 tests passed, 1 skipped); `frontend-ci.yml` now runs it on pushes; the DMG runs on release tags only |
| Date | 2026-10-04 |
| Owner | frontend-chat |

## Source

`Desktop DMG` fails on every recent `main` run at "Test Frontend Performance Contracts", which
runs the whole frontend suite (`npm test`, vitest). On a clean `main` it has 11 failing tests in
9 files plus 2 files that fail to import. Every PR shows red because of it.

## Causes

- **Circular import (2 files fail to load):** `services/llm-config-api.ts` read
  `getApiBaseUrl()` at module load, while `services/api.ts` imports stores that import it, so a test
  that imported `useLLMStore` first hit "Cannot access '__vite_ssr_import_6__' before
  initialization" (`workChatTabSelection.test.ts`, `PlatformChat.parentSubmit.test.tsx`).
- **Stale source-contract tests:** the Vault commits of 3–4 Oct (`ba931c50b`, `aa7f10146`,
  `9832ca775`) restructured Setup, Integrations, the gateway console and the Gmail / secrets /
  access panels without updating the tests that read those component sources:
  WorkflowResponsiveLayout, formsKitAdoption (4), WorkspacePanelGuideButton, gatewayConsoleShared,
  WorkWorkspacePane (2), ConnectorsBrowser.browserHeader, Wo## Done

- `llm-config-api.ts` sets the base URL per request (also follows a workspace switch);
  the two import failures and the services tests pass.
- All 11 stale tests were test-side (no code regression found); each keeps its intent on the new structure:
  - WorkflowResponsiveLayout: the persistent Chat tab title is now the workflow name, not "Chat".
  - formsKitAdoption Gmail: kit imports checked are ToggleRow/FormSection/Input/Textarea (no checkbox is used); still zero raw elements.
  - formsKitAdoption secrets: checks the Button/Checkbox/Input kit imports instead of badge/SettingsCard; still zero raw elements.
  - formsKitAdoption LLM actions: dropped the `Input` import assertion only (the provider search field was removed in 6c7a648b6); still no raw input.
  - formsKitAdoption access bodies: the Users role dropdowns now use the kit `Select`, so the assertion is zero raw elements and no `<select`.
  - WorkspacePanelGuideButton: Gmail how-to answer text is now "Connect Google account".
  - gatewayConsoleShared: groups are a list first; the test opens the group, then asserts the MCP permissions list (a connector is mocked) and the opened group survive the outage and recover.
  - WorkWorkspacePane: secret selection moved from Identity to Integrations > Plugins (Secrets/Vault); Identity tabs are General/Connected work/Models; Integrations opens on a section picker with breadcrumbs. Removed the `persistExplicitGlobalSelection`/`allowGlobalPromotion` assertions on Identity (feature moved to the Vault panel).
  - ConnectorsBrowser banner: the "Why can't I add one?" explainer was removed on purpose (everyone connects privately); the test now asserts it is gone and the plain private-connect/Vault wording.
  - WorkflowCapabilitiesPanel.mcpLayout: the Gmail section now says "Your Google accounts for this project" and renders `GoogleAccountList`.

## Done (2026-10-04, later)

- `gatewayConsoleShared` outage recovery was the last red test (a Vault change on 4 Oct made an empty
  permissions list say "No MCPs assigned. Add MCPs (1)", so `Docs` is no longer on screen until Add MCPs is
  opened). The test now asserts that text; the "(1)" proves the connector loader recovered. The whole suite
  is green.
- The macOS DMG build no longer runs on every push and pull request. `Desktop DMG` runs on a `v*` release tag
  or by hand (workflow_dispatch). The frontend typecheck and `npm test` moved to a cheap Ubuntu workflow,
  `frontend-ci.yml`, that runs on pushes and PRs that touch `frontend/`. The Go checks stay in
  `coding-cli-p0.yml`.

- Two more fixes after `frontend-ci.yml` ran for the first time: `MCPDetailsModal.test` still expected a "Use … from
  Vault" checkbox (Vault MCPs are available automatically now) and `WorkSurface.test` let a late provider-manifest
  write fail as an unhandled rejection after teardown (the store is mocked as already loaded, like
  `RunsOnPicker.test`). The ordinary-chat modal labels personal connections "Your connection".

## Left

- Confirm `frontend-ci.yml` is green on `main`, and that the next `v*` tag builds the DMG.

## Register notes

[PLAT-481](plat-481.md), fixed on main: the circular import and the stale
contract tests are fixed; the suite is green. The DMG build now runs on release tags only; a new
`frontend-ci.yml` runs the frontend checks on pushes. Left: confirm both are green.
