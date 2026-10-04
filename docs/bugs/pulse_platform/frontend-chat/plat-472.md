[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-472 — After a backend restart a workflow chat's panel stays disconnected (browser_disconnected) until its next turn

| Coordination | Value |
|---|---|
| State | fixed on `main`; needs a backend restart to take effect |
| Date | 2026-10-04 |
| Owner | frontend-chat |
| Related | PLAT-434 (a page on a tab the agent cannot open still registers) |

## Source

The owner's website-aeo Builder chat answered "browser_disconnected" when it tried to
inspect or refresh the visible Pulse panel, right after a backend restart. The server
log shows the page's connect attempts for the restarted chats refused with
`session_not_active` and then `unsupported_surface`.

## Cause

The UI-control broker is in memory. A session's workflow scope is set only when the
chat's tools are registered at a turn. After a restart the scope is empty until the
chat's next turn, and the bind route restored a scope only for Code/Crew projects
(`restoredWorkUIScope`), so a live workflow Builder chat got `unsupported_surface`
and the agent saw no browser. PLAT-434 fixed a different case (a tab the agent cannot
open); this one is a restart.

## Done

- `workflow_ui_policy.go` `restoredWorkflowUIScopeFor`: rebuilds the scope from the live
  session for exactly the callers that would have received the UI tools (an interactive
  Builder chat under `Workflow/`, not bot, child, scheduled or Pulse).
- `ui_control_routes.go`: the bind route uses it after the Work restore, and still
  requires the user to have access to the workflow (`workflowAccessForWorkspacePath`).
- `TestRestoredWorkflowUIScopeForOnlyRestoresAnInteractiveBuilderChat`.

- Second cause, found after the owner restarted with the fix above and website-aeo and
  upwork still said browser_disconnected: the page sends its bind the moment the chat
  looks live (it is streaming), a few milliseconds before the server tracks the session
  (log: `POST /api/query` and `session_not_active` at 20:13:25, `Tracked active session`
  after). The refused bind sent the page dormant, and nothing wakes a dormant page once
  the chat is already live, so the panel never connected. `useWorkspaceUIControl.ts`
  now retries a refused bind up to four times (1, 2, 4, 8 s) while the chat looks live;
  an idle chat still goes dormant at once. Two tests (they fail on the old hook).
  Frontend only: a page reload is enough.

## Left

- Takes effect after the next backend restart; a chat already stuck keeps
  browser_disconnected until its page reconnects once the new server is up (the page
  retries on its own) or the user sends the chat a message.
- Not tried live in the browser.
