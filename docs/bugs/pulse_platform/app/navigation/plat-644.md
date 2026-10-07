[← app / navigation](index.md)

# PLAT-644: Keyboard: panel search, toolbar minimize, chat tab keys

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | app |
| Area | navigation |
| Summary | Panel search on Cmd/Ctrl+J, one panel registry, a minimizable toolbar, Cmd/Ctrl+1-5 chat tabs and shorter Ctrl+K rows |

## Owner requests (2026-10-07)

- Switching Code chat tabs with Alt+1-5 meant changing modifier after Cmd/Ctrl+K.
- A keyboard way to search and open a right-panel view, for every product.
- One place to register product panels, so adding, editing or removing one later is easy.
- An icon to minimize the toolbar, showing the shortcut.
- Ctrl+K rows showed too much (for example "Automation · Workflow/rtslatency · active: busy · workflow-builder ·
  wfask-e4").

## What changed

- **Panel registry.** `frontend/src/products/productPanels.ts` lists every product's panels: Crew/Code
  (`WORK_PANELS`), Brain (`BRAIN_PANELS`) and Vault (`VAULT_PANELS`), moved there from their components, plus
  Workflows and Relays (`WORKFLOW_PANELS`, re-exporting their existing registry in `workspaceViews.ts`). Each toolbar
  renders from it.
- **Cmd/Ctrl+J panel search.** Each toolbar registers what it currently shows and how to open a panel
  (`usePanelSwitcherStore`, keyed by product). Cmd/Ctrl+J opens a search over the current product's panels; Enter
  opens one, and also shows the toolbar again if it was hidden.
- **Toolbar minimize.** The shared `WorkspaceToolbarFrame` ends with a hide icon; hidden, every product shows one
  restore icon. Both tooltips name Cmd/Ctrl+J. The choice is per viewer (local storage).
- **Chat tab keys.** Cmd/Ctrl+1-5 switch Code chat tabs, the same modifier as Cmd/Ctrl+K. In a browser tab Chrome
  keeps Cmd/Ctrl+1-8 for its own tabs, so Alt+1-5 remains as the fallback, and hints show the keys that work where
  the app runs (desktop app or browser). Alt+Shift+T still opens a chat tab (Cmd/Ctrl+Shift+T reopens a browser tab).
- **Shorter Ctrl+K rows.** A row shows its kind and state (for example "Automation · busy", plus a bot origin such as
  Slack). The folder path, current step and session ID are no longer shown but still match a search.
- Hints: the start card and the shortcuts panel list Cmd/Ctrl+J; the shortcuts panel shows the chat-switch keys
  for the desktop app and the browser.

## Verification

Frontend type check and lint clean; 71 test files (switcher, Crew/Code, chat, workflow toolbar) pass. Two Crew/Code
toolbar tests that matched the old arrays' source text now check the registry data. Not yet seen in the running app:
check Cmd/Ctrl+J, the hide icon and the Ctrl+K rows after the next local restart.

## Sections inside panels (2026-10-07)

Owner: ⌘J could not find "slack", "models" or "gmail", because it listed only top-level panels. The registry now
also holds each panel's tabs (`PanelSection`, with search keywords): Integrations (Tools & secrets, Brain, Folders,
Slack, WhatsApp, Google apps incl. Gmail, Use in AI apps, and the MCP / Secrets / Skills / Vault sub-tabs), Identity
(General, Models, Upgrades), Automation (Schedules, Webhooks, Functions, Bots, Chats), Knowledge, Access, Pulse, and
Vault People (Users, Groups). Relays and Code list only the tabs their panels show. ⌘J lists panels when empty and
searches panels and tabs as you type; choosing a tab opens its panel on that tab. It also offers the app-wide
Providers & models page from every product.

Panels that ignored a requested tab now take one through `useWorkspaceViewTarget`, which applies each target once
(a remounted panel does not re-apply an old one): workflow Integrations, Identity, Access and Pulse; Crew/Code
Integrations and Identity. Vault People takes a request prop. Also removed icon imports left unused when the panel
arrays moved.

Verification: type check clean; the product, workflow, switcher and chat tests pass apart from failures already on
main before this work (Vault GatewaySurface 14, WorkspacePanelGuideButton 2, workspaceToolbarPlacement 2; the relay
switcher test passes alone and is flaky in the full run).

With nothing typed, ⌘J now lists every panel with its tabs indented under it (Automation → Schedules, Webhooks,
Functions…), so the list also shows what can be typed (owner, 2026-10-07).
