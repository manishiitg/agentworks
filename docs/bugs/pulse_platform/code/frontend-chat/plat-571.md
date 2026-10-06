[← code / frontend-chat](index.md)

# PLAT-571: Code side-chat tabs

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | code |
| Area | frontend-chat |
| Summary | Up to 4 extra full chats per Code project (5 in all), renamable; the primary chat keeps Slack, WhatsApp, MCP, schedules and triggers |

## What is wanted

Owner, 2026-10-06 (handed over by the Brain/Vault session; parked for later, nothing started): in Code only, a "new
tab" option to start extra chats in the same project, and renaming tabs so each says what it is for.

This reverses part of the decision "One chat per Crew and Code" (kept 2026-09-30), which exists mainly because a
WhatsApp or Slack message must land in one known conversation. The owner's resolution: one primary chat owns the
channels; side tabs are extra. A new `DECISIONS.md` entry is needed when it is built.

## Proposed design

- The primary tab stays `code:project:<id>`; WhatsApp and Slack always go there.
- A side tab is its own server conversation in the project's folder. Reuse `isolateProjectAutomationBinding`
  (`product_conversation_registry.go`) with a "chat" kind next to schedule and trigger, key `<projectId>:chat:<id>`.
- When a side tab closes or finishes it posts a short summary (what it did, files changed) into the primary chat.
- Two chats editing one folder clobber each other. Step 1: one writer at a time (others read, plan, answer). Later: a
  git worktree per side tab, after checking Code projects are git repos.
- Cap side tabs per project at 2 or 3; each is a CLI process.

## Checked by reading code (not in a browser)

1. Streaming on hidden tabs works: ChatArea keeps an SSE connection for every open tab with an active session, in the
   account-scoped `useChatStore`; unmounting a view does not disconnect them. A side chat must be a normal server
   session so it appears in `activeRuntimeSessionIds`.
2. Drafts survive tab switches: ChatInput keeps the draft per tab in `tabConfig.inputText`.

## Still to build

- `WorkChatTabs` (`products/work/WorkSurface.tsx`) shows only the canonical tab plus view-only history tabs; it needs
  a third kind, a live side tab.
- Terminal control, steer, stop and "is running" assume one session per project; they must act on the active tab's.
- `globalProductNavigation.ts` maps `code:project:` session ids to a project; side chats must map back too.
- Rename: `ChatTabPill` has a rename control but nothing passes `onRenameTab`; wire it to
  `agentApi.renameChatHistorySession`.
- Code's "New chat" replaces the primary conversation (the server rotates the session); keep it distinct from "new tab".
- Tab logic is fragile (a 6h tab sweep shipped and was reverted): verify live in a browser, switching mid-turn, typing
  in one tab while another streams, closing a side tab.

## Built (2026-10-06)

Owner: take it up; every tab is full Builder mode, like running 3-4 agents on one repository locally; keep a primary
chat for MCP, Slack, in-chat schedules and triggers; be careful with ChatArea, tabs, chat input, tab switching and
Ctrl+K.

- The server already supported it: `resolveProductProjectBindingWithStore` gives a suffixed key
  (`<projectId>:<suffix>`) its own native CLI session and durable conversation in the same project folder, with
  native agent tools on, and no `product.json` session. No server change.
- `WorkChatTabs` (`WorkSurface.tsx`) shows live side chats next to the primary and a "+" (Code, own projects only)
  that opens one: key `workSideChatKey(projectId, id)` = `<projectId>:chat:<id>`, resolved on the server first so it
  has a real session, copying the primary's model, MCPs and skills. At most 3 side chats (4 chats) per project.
- Side chats are renamable (`renameChatHistorySession`, the existing tab-pill control). A side chat that is still
  working cannot be closed (its started work reports back to it); closing an idle one stops its session.
- "New chat" (which replaces the primary conversation) is offered only in the primary tab.
- Manifest changes (identity, model, MCPs, skills) update every live chat of the project, not only the primary.
- Ctrl+K names a side chat in its subtitle (`Code · Chat 2`) so two chats of one project differ.
- The pending coding-agent question card follows the tab's own streaming flag, not the global one.

Unchanged, checked by reading the code: Slack and WhatsApp use the bare project id; MCP ask-a-Crew uses it; in-chat
schedules (including one-time) default to the primary; webhooks, Gmail and trigger links use isolated chats; workspace
pane messages need an exact primary match; completion notices go to the session that started the work. Per tab
already: draft, queued messages, Stop/Send, steer, terminal view, scroll; hidden tabs keep streaming.

Tests: a side chat stays active but is never the primary (`workChatTabSelection.test.ts`); `tsc -b` clean; Work,
tab-helper and quick-switcher tests pass.

## Left

- Owner browser check: open two side chats, type in one while another streams, switch tabs mid-run, refresh, Ctrl+K
  away and back, try closing a running side chat, send a Slack or MCP message while a side chat is active.
- Two chats editing the same files can overwrite each other (owner chose full mode). Next: a git worktree per side
  chat, after checking whether Code projects are git repositories.
- The cap is enforced in the UI only; the server accepts any suffixed key.

## RTS test notes

- 2026-10-06: deployed to RTS (`016e738`) for the owner's test. Owner: make the "+" more prominent; it is now a primary-coloured "New tab" pill (label avoids "New chat", which replaces the primary conversation).

## Follow-ups (2026-10-06)

- Owner: at most 5 tabs by default. `WORK_SIDE_CHAT_LIMIT` is 4 (the primary plus four side chats).
- Owner: from a Crew, picking a Code side chat (Ctrl+K) opened the primary instead. Opening Code prepares the
  primary chat, and `createChatTab` reusing its tab makes it active. `workTabToKeepActive` keeps a chat of the same
  project the user had picked; switching inside Code was unaffected because the preparation does not re-run there.
  Pinned in `workChatTabSelection.test.ts`. To check on RTS with the rest.
- Owner, 2026-10-06 (RTS): "tabs seem to work well for now" in their testing.
- Owner: chat-tab keys like the terminal's. Alt+1–5 / Option(⌥)+1–5 pick a chat (1 = primary), Alt+Shift+T /
  ⌥⇧T opens one (`workChatTabShortcut`, physical keys, so ⌥ works on a Mac). Active only while Code is on screen
  and never for a key Code's terminal already took. Shown for both systems in the start-card hint, the New tab
  tooltip and the shortcuts panel's new Code section. Pinned in `workChatTabSelection.test.ts`.
- Owner: show on the tab whether a chat is working or idle, mainly for tabs not in focus. The shared `ChatTabPill`
  (Code, Crew, workflows, Vault) shows a spinner while working, an amber pulsing dot when the session waits for the
  user (`runtimeNeedsUserInput` from the active-sessions list, the same signal as the activity monitor and the chat
  footer), green when finished since last seen, grey when idle. Hidden tabs keep updating: a running tab stays
  subscribed. Test in `AgentWorksChatTabItem.test.tsx`. Unrelated, already failing on main: four workflow tests
  (`WorkflowResponsiveLayout`, `WorkspacePanelGuideButton` x2, `workspaceToolbarPlacement`) expect older source text.

