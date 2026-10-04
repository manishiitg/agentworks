[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-481 — The frontend test suite is red on main (release and DMG builds fail)

| Coordination | Value |
|---|---|
| State | fixed on main: `npm test` is green (482 files passed, 2802 tests passed, 1 skipped); `Desktop DMG` run still to be confirmed |
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

## Left

- Confirm `Desktop DMG` passes on `main`.
