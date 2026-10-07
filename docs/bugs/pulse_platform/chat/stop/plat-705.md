[← chat / stop](index.md)

# PLAT-705: Background work pill with per-item Stop in chats

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | chat |
| Area | stop |
| Summary | Background work a chat started shows as a "N running" pill with live status and a Stop per item; the composer Stop is only for the CLI's own turn. |

## What happened

After PLAT-699 the composer Stop only interrupts the coding CLI's current turn
(`/api/session/cancel-turn`), but it was still shown when only background work
ran (`useTabTurnActive` counted `hasRunningBgAgents`), so it did nothing
visible, and there was no way to stop one step, workflow run or sub-agent the
chat had started. The owner (2026-10-07) asked for a separate "N running" pill
with a Stop per item. A later user request ("can we display the background
agent's processing directly in the chat so the user can see what it's
currently working on?") is answered by the same list: each item shows a
one-line live status and an Open link.

## Fix

- Data: every such item is already in `bgAgentRegistry` for the session
  (workshop steps and full runs via `workshopExecutionBgNotifier`, delegated
  sub-agents, Crew/Code function calls `function-call-*`, PLAT-648).
- `GET /api/sessions/{id}/background-work`
  (`agent_go/cmd/server/session_background_work.go`): the running top-level
  items (children of another running item and `-step-` progress mirrors are
  folded into their parent) as `{id, kind: step|workflow_run|sub_agent|crew_call,
  label, started_at, can_stop, status}`. `status` is read from what the
  registry already holds: a running tool call, the running child step of a
  workflow run, or the agent's latest assistant line.
- `POST /api/sessions/{id}/background-work/{item}/stop`: a workflow step or run
  goes through the workshop's own stop (`WorkshopChatSession.StopExecution`,
  as `stop_step`); otherwise `CancelExecutionTree` cancels the item and its
  children, settles tracked executions and emits `background_agent_terminated`.
  Both routes answer 404 unless the caller owns the session (a session with no
  owner belongs to the single-user default account); the registry is keyed by
  session, so no other chat's work is reachable.
- Frontend: `useTabTurnActive(tabId, 'foreground')` drops background work and
  returns false while the turn waits on the user's answer; the composer Stop
  and `ChatInput` use it, the scheduled-run footer keeps `'any'`.
  `BackgroundWorkPill` sits next to Stop/Send, polls the list only while the
  chat has background work (5s, 3s while open), shows label, status, elapsed
  time, Open (scrolls to the item's start card in the transcript, when it is
  rendered) and Stop.
- Tests: `TestSessionBackgroundWorkIsOwnerScoped`,
  `src/components/BackgroundWorkPill.test.tsx`.

## Left

- Open only scrolls to the item's start card in this chat. There is no link to
  a workflow run tab or a sub-agent's full transcript yet.
- Not checked live in a running chat yet (no local app runs).
