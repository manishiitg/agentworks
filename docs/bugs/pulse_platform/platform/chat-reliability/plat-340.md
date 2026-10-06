[← platform / chat-reliability](index.md)

# PLAT-340 — Stopping an interactive workflow response stranded the next message

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | platform |
| Area | chat-reliability |
| Summary | records the Confida workflow-chat failure where Stop canceled the response but also discarded the request and workspace binding needed by the next message. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented; regression tests green; deployment pending` |
| Last synchronized | `2026-09-21` |
| Priority | `P2 chat reliability` |

## Report and server evidence

Vaibhav reported that after sending a workflow-chat message, pressing **Stop**
while the reply was processing, and sending another message, the same chat
became unusable. Expected: the current response stops and the next user
message starts a normal turn in that conversation.

Confida's agent log confirmed a reproduction on 2026-09-21. At 07:22:20 UTC,
`POST /api/session/stop?cancelAgents=true` returned 200 and closed the running
workflow session. Subsequent `POST /api/sessions/{id}/live-input` requests at
07:22:23, :26, :28 and :36 UTC each returned 409. This is a server-side
rejection, not merely a stuck browser display.

## Cause

The chat Stop button used the full `handleStopSession` teardown. That handler
correctly canceled the active turn, workflow steps, background work, MCP
sessions and CLI panes, and marked the session stopped to prevent an automatic
restart. It also deleted `lastQueryRequests` and `sessionWorkspaceFolders`.
The workflow composer kept the chat session and sent the next message through
`/live-input`. Without the saved request/workspace context, the server could
not safely construct the next workflow turn and returned 409.

This is different from stopping a schedule, webhook or bot run: those runs
must remain terminal and must not be reopened by an internal continuation.

## Local fix

- The interactive chat Stop button requests `preserveConversation=true` while
  still requesting cancellation of background agents. The scheduled-run
  footer and other existing full-stop callers do not request it.
- The stop handler retains only the resumable request template and workspace
  binding for an active interactive conversation. It still cancels current
  work, clears pending notifications and synthetic-turn state, closes live
  sessions, marks the session stopped, and clears shell permission grants.
- The server ignores the preserve option for scheduled, webhook and bot
  sessions, so they retain the full teardown behavior.

Changed paths: `agent_go/cmd/server/session_lifecycle.go`,
`frontend/src/components/SessionStopButton.tsx`, and
`frontend/src/services/api.ts`.

## Verification and remaining work

- `go test ./cmd/server -run 'Test.*(StopSession|LiveInput|CancelCurrentTurn|Scheduled).*' -count=1` passed.
- A new endpoint-level regression stops a workflow chat and sends the next
  message through `/live-input`; the server accepts it and starts a turn with
  the same workflow binding. Schedule, webhook and bot guard tests pass.
- ESLint passed for the changed frontend files; `git diff --check` passed.
- This older local checkout's full frontend typecheck still fails in the
  unrelated `GlobalActivityMonitor.dropdown.test.tsx` mock assertion. That
  assertion was fixed on remote `main` in `b1d3b3d8f`, but this working tree
  has not been rebased because it contains unrelated local changes.

After integration with current `main`, run the full frontend build, deploy,
and reverify Stop → send again in the
same Confida workflow chat. Confirm that stopped schedules/triggers stay
stopped and cannot resume from internal notifications.

## Register notes

[PLAT-340](plat-340.md) records the Confida
workflow-chat failure where Stop canceled the response but also discarded the
request and workspace binding needed by the next message. Interactive chat Stop
now preserves those bindings while still canceling current work; scheduled,
webhook, and bot runs retain full teardown. Endpoint-level and guard regressions
are green; deployment and live acceptance remain pending.
