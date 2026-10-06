[← platform / frontend-chat](index.md)

# PLAT-472 — After a backend restart a workflow chat's panel stays disconnected (browser_disconnected) until its next turn

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | app |
| Area | ui |
| Summary | fixed on `main`, needs a restart: the UI-control scope of a live Builder chat is restored from the session after a restart, so the agent no longer sees browser_disconnected until its next turn. |

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

- Third cause, from the log after the retry fix: after a restart the page still holds a
  binding the server no longer knows, so its next sync and its release both answer
  `inactive_scope` (20:36:58, two lines). The page then dropped the binding and waited
  for the next five-minute renewal; the agent saw browser_disconnected meanwhile.
  `useWorkspaceUIControl.ts` now re-binds at once (up to three times, reset by a good
  sync). A test fails on the old hook.
- The server now logs successful bind and unbind and a lease expiry
  (`[UI-CONTROL] ... operation=bind ok`, `binding lease expired`), where it used to log
  failures only, so the next browser_disconnected can be traced from the log.

## Left

- Takes effect after the next backend restart; a chat already stuck keeps
  browser_disconnected until its page reconnects once the new server is up (the page
  retries on its own) or the user sends the chat a message.
- Not tried live in the browser. The owner reported browser_disconnected unchanged after
  the retry fix. Facts so far: after the second restart the server log showed no bind
  attempt after the chat went live, so the page either never ran the new code or never
  re-bound; a binding expires after 6 minutes (`uiControlBindingLease`) and the page
  renews every 5 minutes (`UI_CONTROL_BACKUP_POLL_MS`), and a hidden tab releases its
  binding. Still open: confirm in the browser whether a bind succeeds after a reload.

## Register notes

[PLAT-472](plat-472.md), fixed on `main`, needs a restart:
the UI-control scope of a live Builder chat is restored from the session after a
restart, so the agent no longer sees browser_disconnected until its next turn.
