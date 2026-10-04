[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-481 — The frontend test suite is red on main (release and DMG builds fail)

| Coordination | Value |
|---|---|
| State | open: the circular import is fixed on `main`; the stale contract tests are being updated |
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
  WorkWorkspacePane (2), ConnectorsBrowser.browserHeader, WorkflowCapabilitiesPanel.mcpLayout.

## Done

- `llm-config-api.ts` sets the base URL per request (also follows a workspace switch);
  the two import failures and the services tests pass.

## Left

- Update the 11 stale tests to the current, intended structure (or report a real regression),
  then confirm `npm test` is green and `Desktop DMG` passes.
