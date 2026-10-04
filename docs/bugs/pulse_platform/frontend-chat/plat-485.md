[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-485 — A send refused by the one-builder-chat-per-workflow guard shows the raw "Request failed with status code 409"

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
