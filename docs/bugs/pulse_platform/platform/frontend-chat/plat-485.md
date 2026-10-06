[← platform / frontend-chat](index.md)

# PLAT-485 — A send refused by the one-builder-chat-per-workflow guard shows the raw "Request failed with status code 409"

| Field | Value |
|---|---|
| State | open |
| Priority | P3 |
| Product | platform |
| Area | frontend-chat |
| Summary | open. |

| Coordination | Value |
|---|---|
| State | open (found 2026-10-04 on RTS, workflow `automationtesting`; not fixed) |
| Priority | P3 |
| Owner | frontend-chat |

## What happened

The backend allows one workflow-builder chat per workflow at a time (`server.go`, `workflowBusyGuardApplies`, answer `409 workflow_busy`, "Workflow builder chat is already running on this workflow. Stop the running chat before starting a new one."). On RTS at 18:15:15 a builder chat (session `41373bfd`) started on `Workflow/automationtesting` and ran 75 s. Four sends from OTHER session ids were refused while it ran: three from an API client session (`pat-oauth-...`, 18:15:18, :39, :51) and one from the owner's UI (session `5037ed8d`, 18:15:54). The guard worked; the UI showed the axios text "Request failed with status code 409 ... automation-testing" followed by the server message, and nothing offered to stop the running chat or wait. Once the run ended (18:16:30) a retry worked.

## Why it matters

The friendly kill-and-start dialog exists only for the "+ new chat" pre-check (`ChatArea.tsx`, `/api/workflow/running`). A plain send from a tab whose session id differs from the running one (a second tab, a restored tab, an API/MCP client on the same workflow) falls through to the raw error. A person cannot tell what is running or how to proceed.

## Fix to make

Handle `409 workflow_busy` (the response carries `running.session_id`) on every builder send: show the same "a builder chat is running on this workflow: stop it / wait" choice, never the raw axios text, and offer to retry when the run ends. Related test: `frontend/src/services/turnRunningRetry.test.ts` already covers `turn_running`.

## Update 2026-10-04 18:25 (second case, same workflow)

The owner could not send at all on `automationtesting` from 18:17 to 18:24 on RTS. The tab was bound to session `5037ed8d`: every send from it answered 409 (18:18:03, 18:21:59, 18:22:35, 18:22:45, 18:24:32 `POST /api/query` 409 in 171 ms) while `GET /api/sessions/5037ed8d/events` answered 404 and `UI-CONTROL` said `session_not_active`, i.e. the server no longer knew that session (RTS was restarted by deploys at 17:43 and 17:56) but the UI kept it. No `[WORKFLOW_BUSY]` line was logged for those, so the 409 was another refusal for a session the server does not hold. At 18:24:34 the UI sent `POST /api/session/stop` (404) and rotated to a new session `319aa022`, whose send was accepted at 18:24:37 and whose builder turn completed at 18:25:00. So a tab that survives a server restart can sit on a dead session id and fail every send with a raw 409 until the person stops/re-creates the chat. Fix to make with the item above: on `409`/`session_not_active` for a session the server does not hold, rotate to a fresh session automatically (or offer it) instead of showing the raw error.

## Register notes

[PLAT-485](plat-485.md), P3, open. The one-builder-chat-per-workflow guard works, but a send from a different session id (second tab, API client) shows "Request failed with status code 409" with no stop/wait choice.
