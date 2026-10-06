[← app / chat](index.md)

# PLAT-463 — "New chat" button in the workflow chat

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | app |
| Area | chat |
| Summary | fixed on `main`, not deployed: the workflow chat composer shows the same "New chat" button as Code, for anyone who can write the workflow; the old conversation stays in Previous chats. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | frontend-chat |
| Related | PLAT-425 (provider change relaunches the chat) |

## Source

The owner could not restart a workflow's Builder CLI (a Muse chat with no MCP bridge)
because the workflow chat had no way to start a fresh conversation. Code and Video
Studio already show the composer's "New chat" button; the workflow chat did not.

## Done

- `WorkflowLayout.tsx`: the workflow chat passes `showNewChatAction` to `ChatArea`
  for anyone who can write the workflow (a read-only viewer of a shared workflow
  does not get it). The existing handler stops the running session, then gives the
  tab a new session; the old conversation stays in Previous chats because each
  Builder session keeps its own transcript file.

- `ChatInput.tsx`: the button is no longer disabled while a turn is in flight (it
  was greyed out on a stuck chat, the case it is needed for). New chat already stops
  the old session first; the tooltip says "Stop this chat and start a new one".
  This applies to every product that shows the button (Code, Video Studio, Dominion).

- Found with the owner on a stuck Muse Builder chat (jobsearch): New chat gave 409
  `workflow_busy` and the tab went back to the old chat after every click. Cause:
  `handleStopSession` marked the tracked run canceled only when the session still had
  a query-ID mapping; a Builder chat whose turn was lost has none, so the tracker kept
  listing it as running (the 409, and the workflow tab re-adopting it as the live
  chat). `session_lifecycle.go` now cancels the tracked run on every stop
  (`TestStopSessionWithoutQueryIDsClearsTrackedBuilderChat`, fails without the fix).
- `ChatArea.tsx`: New chat always sends the stop; it used to skip it when the
  active-session list did not contain the chat.
- An already-stuck run on a server started before this fix stays stuck until that
  server restarts (the stop of an old build never clears it).

- `multiuser_identity_test.go`: removed two unused fields (`pathRule`, `sameAsPath`)
  of the identity-row type that failed the commit hook's `unused` lint on main.

## Left

- Not enabled in SparkQuill chats (parent/child) on purpose. MCP gateway keeps its
  own `onNewChat` handler and was not changed.
- Not tried live in the browser; the owner tests locally.

## Register notes

[PLAT-463](plat-463.md), fixed on `main`, not deployed:
the workflow chat composer shows the same "New chat" button as Code, for anyone who
can write the workflow; the old conversation stays in Previous chats.
