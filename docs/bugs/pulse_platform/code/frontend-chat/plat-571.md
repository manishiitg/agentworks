[← code / frontend-chat](index.md)

# PLAT-571: Code side-chat tabs (parked)

| Field | Value |
|---|---|
| State | open |
| Priority | P3 |
| Product | code |
| Area | frontend-chat |
| Summary | Parked: extra chat tabs in a Code project and renaming tabs; one primary chat keeps owning WhatsApp and Slack |

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
