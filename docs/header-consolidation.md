# Header Consolidation

How every right-side workspace view was brought under one shared header
standard. Companion to `ui-design-guidelines.md`.

## The standard

One component renders every header: `WorkspaceViewHeader`
(`frontend/src/components/workflow/WorkspaceViewHeader.tsx`).

- Leading icon in an `h-9 w-9` tile (`h-4` glyph), sized to span the
  title + subtitle block. Custom elements (the hub's entity avatar)
  render as-is at the same footprint.
- Title `text-sm font-semibold`, subtitle `text-xs text-muted-foreground`,
  rendered by the header itself — views never override the sizes.
- Optional inline `context` (counts, timestamps, badges) next to the
  title, and a full-width `below` row (stats strips, pills).
- Actions on the right, rendered with the shared pair below.
- Tabs, when a view has them, belong to the header via its `tabs` prop
  and render pinned to its bottom edge. Pickers and dropdowns live in
  the content, never in the header row.
- Per-tab extras via `tabActions` render left of the base pair; to
  change the pair itself per tab (Ask AI message, refresh handler),
  compute `actions` from the active tab instead.
- `bare` variant for views whose shell owns the row (Costs, Execution
  Logs via `InspectorShell`) — visually identical, no duplicate border.
  `sticky` variant for scroll views (triggers).

Plan and Report are the only views without a header: they are canvas
overlays, not titled views.

## The action pair

Every header ends in the same two icons: Ask AI left, refresh right,
via `WorkspaceViewActions`. The order is a product rule, not a habit —
Ask AI is always the left icon and refresh the right one so the pair is
predictable wherever it appears.

- `AskAIButton` sends one pre-written message to chat. Icon-only in
  headers: it expands on hover/focus to reveal the label, and sends on
  a two-click arm/confirm (a single click next to Refresh used to burn
  a chat turn on misclicks). After a successful send it briefly shows
  "Sent!".
- `WorkspaceViewIconButton` is the shared refresh button (spinning
  state while refreshing). Every view that loads data has one.

## One function to chat

All right-pane sends converge on `sendWorkspacePaneMessageToChat`
(`frontend/src/utils/workspacePaneChat.ts`): the only public entry
point. It takes a message plus one destination key (automation path,
tab id, or Crew project), resolves the right conversation, opens the
left chat, queues behind a running turn through the same durable chat
queue, and restores a stuck session. Callers are one-liners:

| Sender | File |
|---|---|
| Ask AI buttons (everywhere) | `AskAIButton.tsx` |
| Crew views | `WorkWorkspacePane.tsx` (adds the project id, same function) |
| Reports dashboard widgets | `reportWidgets/useReportChat.ts` |
| Human decisions (Report) | `utils/reportHumanInputChat.ts` |
| Capabilities panel | `WorkflowCapabilitiesPanel.tsx` |

The only sender outside it is the main chat composer in `ChatArea.tsx`,
which owns the queue itself. Product surfaces with their own chat lane
(Crew) route through `AskAIButton`'s `onAsk` prop and still land in the
same function. New "send to chat" buttons need nothing else.

## Ask AI copy

Ask AI messages are user-visible, so they are plain business words: no
tool names, skill paths, or config keys. Builder messages use the
`workspaceAskAI` marker blocks (`utils/askAIMessage.ts`) — a visible
summary plus hidden builder instructions — with one message per view in
`WORKSPACE_ASK_AI_MESSAGE` (`workspaceAskAI.ts`). The map is exhaustive
over registered views, so a new view cannot silently ship without one.
Views with distinct tabs (Integrations, Knowledge) follow the active
tab instead of the view. Crew messages stay plain strings sent to the
project chat.

## What was migrated

- All Builder inspectors and Setup sections, the automation hub, and
  every Crew pane render the shared header with the standard pair.
- The execution-logs run dropdown moved out of the header into a
  content row; the live-browser session picker sits below the header.
- Files tree and file content share the same header; Costs and Logs
  use the `bare` variant inside their shell.
- Knowledge became one umbrella view with three tabs (Learnings,
  Knowledge Base, Database), each with its own Ask AI message.
- Pulse gained the shared refresh button; triggers use the sticky
  variant.
- Toolbars use open/close groups (`WorkspaceToolbarGroup`), never
  dropdowns: primary icons stay visible, Ops and Setup collapse into
  single-open groups that follow the active view.

## Verification

- `npx vitest run` (frontend; only pre-existing peer-owned provider /
  session-restore failures remain)
- `npx tsc -b`, `npx eslint` on touched files
- Header adoption is pinned by the per-view header tests; Ask AI copy
  by the `workspaceAskAI` marker-block tests.
