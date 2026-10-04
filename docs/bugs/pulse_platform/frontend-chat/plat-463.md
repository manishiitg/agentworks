[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-463 — "New chat" button in the workflow chat

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

## Left

- Not enabled in SparkQuill chats (parent/child) on purpose. MCP gateway keeps its
  own `onNewChat` handler and was not changed.
- Not tried live in the browser; the owner tests locally.
