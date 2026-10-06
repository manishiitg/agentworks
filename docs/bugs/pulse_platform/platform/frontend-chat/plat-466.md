[← platform / frontend-chat](index.md)

# PLAT-466 — Lighter chat restore (older turns as messages only) and opening at the latest message

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | platform |
| Area | frontend-chat |
| Summary | see ticket for state: interactive chat restore returns the latest turn whole and older turns as messages only (`view=messages`), paged over that view; a transcript that is still mounting never saves a reading position,… |

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | frontend-chat |

## Problem

An interactive chat restore replaced the tab with the latest page of durable
events (the client asked for 100 rows). One turn with ~50 tool calls fills that
page, pushes real messages out, and puts the "Load earlier messages" row right
above the latest turn. Fast Cmd+K switching could also leave a chat showing the
top of its loaded window.

## Owner decisions (2026-10-04)

1. A restore returns the latest turn complete (a running turn too) and every
   older turn as messages only: no tool call, result, status or thinking rows
   and no "N tool calls" count. No expand, no inspect. Nothing is deleted; the
   rows stay in the journal and are simply not returned.
2. A chat that opens or is switched to shows its latest content and follows it
   while a turn runs or the reader has not scrolled.
3. Tool calls are a debugging aid, not a supported feature, and may be removed
   from the UI: one frontend switch (`utils/toolCallVisibility.ts`) decides
   whether they render; the server compact view does not depend on it.

## Design

Opt-in: `GET /api/sessions/{id}/events?durable_chat=1&view=messages[&limit=N][&before_sequence=S]`.
Without `view=messages` the response is exactly as before (read-only and
scheduled-run tabs, Pulse, external APIs, terminal views). With it, `limit`
counts messages (default 40) instead of rows.

Rules (`agent_go/internal/events/event_journal_compact.go`), over the journal's
sequence order:

- A turn starts at a `user_message` row. A user_message directly after a tool
  call row is a mid-turn steering message and does not start the latest turn.
- First page: the whole latest turn (every row, capped at 1000 rows from the
  tail so a runaway turn cannot make the restore unbounded), plus older rows of
  the kept set until 40 messages are collected, then moved back to the start of
  that turn. A page therefore never begins between a user message and its reply.
  40 messages is about 20 exchanges: enough to read back, light enough to open
  instantly; the whole latest turn is what the reader is looking at.
- Kept set for older turns: user messages, assistant messages (transcript chunk,
  `unified_completion`, `agent_end`, `conversation_end`,
  `background_agent_completed`), `agent_error` / `conversation_error`, and every
  question, approval and feedback row with its resolution
  (`coding_agent_question`, `plan_approval`, `request_human_feedback`,
  `blocking_human_feedback`, `human_feedback_resolved`), so an unanswered one
  stays answerable. Dropped: `tool_call_*`, live-input receipts, background
  agent started/terminated, status.
- `before_sequence` pages apply the same cut over the compact view only and
  return ~50 messages (the frontend passes 50). `has_more` is true only when a
  kept row exists below the page's first row; `oldest_sequence` is the cursor.
- `latest_sequence`/the live tail are unchanged, so SSE and polling continue
  from the journal tip exactly as before.

Frontend: `hydrateTabEvents` restores compactly only for an interactive chat tab
(not view-only, execution or bot tabs), with `compact: false` for terminal
views (`TerminalCenter`, `restoredTerminal`, `useFormattedTranscriptHydration`).
The tab's pagination records `compact: true` so "Load earlier messages" pages
the same filtered view.

Position fix (`components/useTranscriptScroll.ts`): a reading position or
`following=false` is saved only if the reader used a scroll gesture or the
transcript has been mounted for 700 ms (`TRANSCRIPT_SETTLE_MS`); the saved
state carries `deliberate`, and a state that is not deliberate is reset to
"follow the latest message" when read. A new chat or one with a running turn
therefore opens at the bottom; a deliberate scroll-up is kept; sending a message
clears it. "Load earlier messages" is shown only once the transcript has settled.

## Done

- Backend: compact journal read, opt-in on the events endpoint, Go tests
  (`event_journal_compact_test.go`, `polling_test.go`).
- Frontend: restore opt-in (`hydrateTabEvents`, `getRecentChatEvents`), compact
  "Load earlier messages" paging (pagination carries `compact`), the single
  tool-call visibility switch, the position fix and its tests
  (`sessionRestore.compact.test.ts`, `useTranscriptScroll.dom.test.tsx`).

## Left

- Not verified in a browser (a chat with a very long running turn, fast Cmd+K
  A->B->A->B).
- Not deployed.

## Register notes

[PLAT-466](plat-466.md), see ticket for state:
interactive chat restore returns the latest turn whole and older turns as
messages only (`view=messages`), paged over that view; a transcript that is
still mounting never saves a reading position, so a chat opens at the bottom.
