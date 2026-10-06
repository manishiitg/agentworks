[← platform / frontend-chat](index.md)

# PLAT-106 — concurrent Chat and Schedule tabs can display events from the wrong session

| Field | Value |
|---|---|
| State | deployed |
| Priority | P0 |
| Product | app |
| Area | ui |
| Summary | records the locally tested, not-yet-deployed notification ownership repair. |

| Field | Value |
|---|---|
| Status | `runtime_reverify` — original retained-answer and event-isolation repairs retain their pending live-verification requirement; **2026-09-17 notification ownership follow-up implemented and tested locally, not deployed** |
| Priority | P0 |
| Owner | frontend session-event ownership and workflow-tab isolation |
| Reported | 2026-08-15 |
| Related | [PLAT-020](../../coding-agents/bridge/plat-020.md)), [PLAT-095](../../schedules/runs/plat-095.md)), [PLAT-103](../../coding-agents/bridge/plat-103.md)), [PLAT-104](plat-104.md) |

## Notification ownership follow-up — 2026-09-17

**Assigned agent:** Codex. **State:** `runtime_reverify` for this follow-up.
Implementation is local; no commit, deployment, or production reproduction is
claimed for these changes. Keep the broader ticket open until live acceptance
passes.

### Report and agreed behavior

The user reports that a background schedule or external trigger can surface an
auto-notification while they are chatting about unrelated work. A chat should
receive completion notifications for workflows or steps **that chat launched**,
including their children. It should keep receiving those results if the user
starts another question in that same conversation. A schedule/trigger owns its
own execution session; merely sharing a workflow, workspace, or active tab does
not make another chat its recipient.

### Findings and scope

The delayed queue drain in `frontend/src/components/ChatArea.tsx` called the
latest submission callback after a 200 ms timeout without an explicit source
tab. Changing selection before dispatch could therefore target another chat.
The queue also removed messages before submission and only restored them on a
thrown error, losing messages when submission returned `false`.

This is a concrete queue-routing defect and an additional ownership boundary to
the August retained-answer repair. It is **not proof of the exact mechanism of
the newly reported live incident**: legacy frontend auto-notification producers
are disabled in the current code, while the backend normally delivers
completion turns directly. The tests exercise queued notifications explicitly;
a real concurrent scheduled run and interactive chat still need verification.

### Implemented locally

- Delayed queue delivery captures the originating tab and session, passes both
  into submission, and checks ownership again after submission-lane waiting and
  asynchronous request preparation. Closed/reused tabs cannot fall back to the
  selected chat. A bound delivery cannot be redirected into another Work chat
  or rotate into a fresh conversation.
- Rejected submissions restore non-stale messages only to the original queue;
  delayed cleanup does not alter the lock of a replacement session.
- Background-agent registration binds missing legacy owners to the launch
  session and rejects attempts to register an already-owned execution in a
  different session.
- Backend completion filtering (single and batched), live steering, and batched
  start notifications check the registered session and any known tracked parent
  session. A known foreign parent is rejected. A completed parent in the same
  chat remains valid after another human turn starts.
- Compatibility boundary: older records without a tracked parent still rely on
  their registered session owner. This is not a new durable provenance store or
  a migration of historical executions.

Files:

- `frontend/src/components/ChatArea.tsx`
- `frontend/src/components/ChatArea.queueOwnership.test.ts`
- `agent_go/cmd/server/background_agents.go`
- `agent_go/cmd/server/auto_notification_ownership_test.go`

### Validation

Passed locally on 2026-09-17:

```sh
cd frontend
./node_modules/.bin/vitest run src/components/ChatArea.queueOwnership.test.ts src/utils/queuedMessageDelivery.test.ts src/stores/useChatStore.sessionIsolation.test.ts src/utils/workflowTabResolution.test.ts
./node_modules/.bin/tsc -b --pretty false
```

54 frontend tests passed across these four files. The five new queue tests
execute the production effect with controlled timers and store state; they are
not a mounted-browser end-to-end test.

```sh
cd agent_go
go test ./cmd/server -run 'Test(BackgroundRegistryCannotRehome|BackgroundNotificationOwnership|ForeignCompletion|Steer|Steered|WorkflowStart|WorkflowStepCompletion|ConversationTurn)' -count=1
go test ./cmd/server -run 'Test.*(Background|Notification|WorkflowSubAgent)' -count=1
```

Both backend selections passed. Coverage includes rejection of re-registration
under another session, a known foreign launch parent, live delivery and queued
completion filtering, plus preservation of notifications for chat-owned work.
`git diff --check` also passed.

### Older ticket integration

The 2026-09-17 follow-up is cross-referenced from these existing tickets, with
their original scope, assigned agent, and completion status preserved:

- [PLAT-095](../../schedules/runs/plat-095.md): exact query-rooted lifecycle.
- [PLAT-100](../../coding-agents/bridge/plat-100.md): launch-parent propagation and live continuations.
- [PLAT-113](../../coding-agents/bridge/plat-113.md): occupancy and notification queues.
- [PLAT-117](../../coding-agents/bridge/plat-117.md): progress mirrors and notification liveness.
- [PLAT-255](../../goals/evaluation/plat-255.md): early pre-validation notification recipients.
- [PLAT-293](plat-293.md): report requests using the shared chat queue.

### Remaining live acceptance

1. Run a schedule and an unrelated interactive chat concurrently in the same
   workflow; complete scheduled steps while typing and switching tabs. Only the
   schedule's session receives its automatic continuation.
2. Repeat with an external trigger and with a different workflow.
3. Launch a workflow/step from Chat, then ask a different question in that same
   chat. Its owned completion must still arrive there, including child results.
4. Exercise delayed/queued completion, tab closure/reuse, and reload; no
   notification migrates into the newly selected conversation.
5. Verify the deployed release and record session/execution IDs before closing
   this follow-up or the broader PLAT-106 ticket.

## Problem

When a workflow Schedule and an interactive Chat are open concurrently, the
formatted Chat can display an assistant update produced by the Schedule. This
makes a truthful Schedule message look like the answer to the user's unrelated
Chat question.

This is more serious than duplicate rendering: the UI is violating the session
boundary. A user cannot tell which automation produced an action, warning, or
answer.

## Evidence

The Build in Public reproduction used Schedule session
`schedule-cron--51af4f19_1786764627816018000`. The displayed sentence

> The receipt call returned an opaque transport failure. I’m checking durable
> Pulse state before any retry so I don’t create a duplicate terminal record.

exists in that Schedule's captured turn at
`agent_go/logs/agent_prompts/schedule-cron--51af4f19_1786764627816018000/stream_turn-000_attempt-0_040109.txt`.
The user's interactive Chat text does not exist in the Schedule prompt logs.
Therefore the command was not misrouted to Pulse; a Schedule event was rendered
under the Chat tab.

PLAT-104 is adjacent but not sufficient. It covers HTTP and SSE creating two
copies of one message **within one session**. This issue is an event from session
A becoming visible while session B is selected.

## Required repair

1. Key every event subscription, cache page, optimistic record, stream fragment,
   and terminal selection by the exact tab `session_id`.
2. At event ingestion, require the requested session, transport envelope session,
   and event owner session to agree. Route an event only to its owning session;
   never rebind it to the currently selected workflow or main terminal.
3. On a tab/session change, synchronously reset the selected terminal and visible
   event source before loading the new session. Stale data may remain cached under
   its original session but must never render during the transition.
4. Keep Chat and Schedule as independent tabs even when they share a workflow.
   Workflow identity is not conversation identity.

## Root cause — corrected 2026-08-15

**The frontend was never able to detect this event.** By the time it arrived it
already carried the Chat session's `session_id`, terminal ID, and execution ID:
the backend had attributed the answer to the requesting session. Session-scoped
guards in the UI are therefore defence in depth, not the fix.

The defect is in Codex retained-answer lookup.
`codexcli.ReadRetainedTurnMessages` resolved a transcript through
`findCodexRolloutForTurn(turnStart, workingDir)`, which walks `~/.codex/sessions`,
keeps every rollout modified since `turnStart − 30s`, **sorts by modification
time descending, and returns the first whose `session_meta.payload.cwd` matches**.

A workflow's interactive Chat and its scheduled run execute in the *same*
working directory. Both rollouts match the `cwd` test, so the lookup returns
whichever conversation wrote most recently — routinely the other one. The
selected answer was then returned to the requesting session and stamped with its
identity, which is exactly why the Build in Public reproduction found the
sentence in the Schedule's prompt log while the UI rendered it under Chat.

Working directory is not a conversation identity. Codex's identity is the
thread/rollout ID: it names each file `rollout-<timestamp>-<thread-id>.jsonl` and
repeats the same value in `session_meta.payload.id`.

### Repair

`multi-llm-provider-go/pkg/adapters/codexcli`:

- `codexInteractiveSession` gains `threadID` and `rolloutPath`, pinning a session
  to its exact conversation;
- new `codexcli_rollout_binding.go` resolves in order — (1) a pinned thread ID,
  re-resolved through `findCodexRolloutForThread` so it survives file rotation;
  (2) otherwise a directory/recency match that **excludes rollouts already
  claimed by other live sessions**, which is what prevents two sessions in one
  directory from selecting the same transcript before either has learned its ID;
- the thread ID is recorded on first resolution and also bound as soon as the
  interactive adapter discovers it (`readCodexTranscriptUsage`), so the steady
  state is always the exact path;
- `readCodexRolloutFinalAssistantText(path, turnStart)` reads ONE known rollout,
  so no directory guess participates in choosing whose answer is returned;
- `ReadRetainedTurnMessages` uses the bound path.

Coverage in `codexcli_rollout_binding_test.go`: a Chat lookup returns the Chat
answer when the Schedule wrote more recently to the same directory; the
unbound directory rule is shown selecting the newest file (documenting why
binding is required) and the exclusion set correcting it; and a pinned session
keeps its rollout when a newer foreign one appears. `go build ./...` and all
`pkg/adapters/...` tests pass.

## Frontend defence in depth — 2026-08-15

These guards do **not** fix the reported leak — see Root cause above; the leaked
event already carried the Chat session's identity. They close the separate class
where an event still declares a foreign owner, and they harden the tab
transition. They were written before the backend cause was found and are kept as
defence in depth.

**Repair 1 was already satisfied.** `tabEvents` is keyed `Record<sessionId,
PollingEvent[]>` and SSE connects per session (`/api/sessions/{id}/events/stream`).
Keying was never the defect.

**Repair 2 was the actual hole, and is now closed.** The only ingestion filter,
`retainEventInSessionWorkingSet`, classified events by *cost* (child transcript
detail vs. session lifecycle) and never compared the event's owning session to
the bucket it was being written into. It also failed open:

```ts
const terminalId = event.terminal_id?.trim()
if (!terminalId) return true        // accepted anything without a terminal
```

Meanwhile `ChatArea.tsx` derives `actualSessionId = response.session_id ||
sessionId` — the transport envelope — and never cross-checks each event's own
owner. An event owned by session A arriving under an envelope labelled B was
therefore written into B.

This explains the exact symptom rather than merely being adjacent to it:
`unified_completion`, `agent_end`, and `conversation_end` are **absent** from
`CHILD_TRANSCRIPT_DETAIL_EVENT_TYPES`, so a *finished assistant answer* passed
the filter unconditionally while noisy streaming chunks were dropped. That is
why a completed Schedule sentence surfaced in Chat.

Changes:

- new `eventBelongsToSession(sessionId, event)` in
  `frontend/src/utils/sessionEventWorkingSet.ts` — the ownership boundary,
  deliberately separate from the volume filter;
- `retainEventInSessionWorkingSet` now checks ownership **first** and never
  fails open for a foreign event, so every `addTabEvent` / `addTabEvents` /
  `_addTabEventsImmediate` write path is covered at one chokepoint;
- `handleLiveStreamingEvent` in `ChatArea.tsx` returns early for a foreign
  event, so streaming text is covered too (the ticket names streaming text, and
  it is separate state from `tabEvents`).

An event that declares **no** `session_id` is still accepted: optimistic local
records and legacy events carry none, and rejecting those would silently drop
the user's own messages. Only a *disagreeing* owner is rejected — which is the
only case that can cross a session boundary.

Regression coverage in `sessionEventWorkingSet.test.ts` was verified to fail
against the previous behaviour before the guard was added (5 failures), then
pass with it. The full frontend suite passes (476 tests).

**Repair 3 is now implemented.** Two stale-render vectors existed, both because
the reset happened one render too late:

- `TerminalCenter` cleared `terminals` / `selectedID` / `userSelectedID` in a
  `useEffect` keyed on `currentSessionId`. Effects run *after* render, so the
  first render for the newly selected tab still painted the previous session's
  terminals and selection. The reset now also runs **during render** via React's
  documented "adjust state when a prop changes" idiom, so React discards that
  render and re-renders with cleared state; stale terminals are never committed.
  The effect is retained for the ref/non-state cleanups it also owns.
- `ChatArea.displayEvents` kept a ref-stability cache (`displayEventsRef`) whose
  reuse test is `length + first ID + last ID` — a same-session heuristic that
  says nothing about ownership — and it was never cleared on a session change.
  The cache is now stamped with the session it belongs to and dropped when that
  changes, with `activeSessionId` added to the memo dependencies.

**Concurrent-session coverage added** in
`frontend/src/stores/useChatStore.sessionIsolation.test.ts`, following the P0
acceptance directly: distinct user/assistant/tool/completion events in both a
Chat and a Schedule session for one workflow (2, 3); 25 interleaved rounds with
deliberate cross-envelope writes on every other tick while both stream (4); and
a late history page for the Schedule session delivered while Chat is selected
(5). Assertions are on exact event IDs and session ownership, never message text
(6). Three of the four were verified to fail against the previous behaviour
before the guard was added. Full frontend suite passes (481 tests).

**Still required before this can be marked implemented:** runtime verification
of the *backend* repair — a real concurrent Chat + Schedule run on one workflow,
confirming each session's retained answer comes from its own Codex thread. Unit
tests prove the binding contract with synthetic rollouts; they do not prove a
live Codex process behaves as modelled. The store-level test likewise proves the
ownership contract, not that the live UI holds the invariant while rapidly
switching real streaming tabs (acceptance 4).

Do **not** mark this fixed or commit it as fixed until that live verification
passes.

## P0 acceptance

1. Start Schedule session A and interactive Chat session B for one workflow.
2. Emit distinct user, assistant, tool, progress, and completion events into both.
3. Selecting B shows no event from A; selecting A shows no event from B.
4. Switch rapidly while both sessions stream and while older history is loading;
   the invariant still holds.
5. Reload and resume both tabs; no event migrates or duplicates across sessions.
6. The test asserts exact event/session ownership, not message text heuristics.

## Register notes

[PLAT-106](plat-106.md) records the locally tested,
not-yet-deployed notification ownership repair. Integration notes are now on
[PLAT-095](../../schedules/runs/plat-095.md),
[PLAT-100](../../coding-agents/bridge/plat-100.md),
[PLAT-113](../../coding-agents/bridge/plat-113.md),
[PLAT-117](../../coding-agents/bridge/plat-117.md),
[PLAT-255](../../goals/evaluation/plat-255.md), and
[PLAT-293](plat-293.md). Their original lifecycle,
queueing, early-notice, and report-security acceptance remains separate; none
is closed by the routing fix. Live concurrent Schedule/trigger + Chat
verification is still required.
