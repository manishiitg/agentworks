[← coding-agents / sessions](index.md)

# PLAT-105 — retained delivery and per-turn completion must share one transport-neutral session contract

| Field | Value |
|---|---|
| State | open |
| Priority | P0 |
| Product | coding-agents |
| Area | sessions |
| Summary | The 2026-08-19 Sales Outreach chat proves the per-turn contract also fails in |

| Field | Value |
|---|---|
| Status | `blocking live regression; implementation incomplete` — delivery through the durable Session works, but a 2026-08-15 Social Media run proved that a provider can capture its final response and exit while AgentWorks never receives or settles the canonical per-turn completion. The UI remains busy indefinitely. Do not close this ticket from unit tests or delivery-only evidence. |
| Priority | P0 |
| Owner | mcpagent session lifetime and delivery acknowledgement |
| Reported | 2026-08-14 |
| Related | [PLAT-020](plat-020.md), [PLAT-035](plat-035.md), [PLAT-099](plat-099.md), [PLAT-102](plat-102.md), [PLAT-103](plat-103.md) |

## Blocking live regression — Claude interim `tool_use` text ended the turn early

The 2026-08-19 Sales Outreach chat proves the per-turn contract also fails in
the opposite direction from the Social Media reproduction below. Instead of
losing a real completion, mcpagent emitted completion too early.

For AgentWorks session `c2b1bb4c-5a4d-4fd1-9709-ad7329c34a7b`, Claude Code
emitted “Let me check what sequence the workflow actually defines.” as text on
a message whose native `stop_reason` was `tool_use`. The retained Session's
generic transcript reader returned that in-progress text, and mcpagent emitted
`source=mcpagent_session` `unified_completion` at `11:15:47` IST. AgentWorks
truthfully settled what it was given. Claude then ran three more tool rounds
and committed its real `stop_reason=end_turn` answer at `11:16:10`; tmux showed
that answer, but the already-settled Formatted turn could not receive it.

This narrows the producer defect: Claude's retained completion path uses
`readClaudeTranscriptMessages` (an in-progress tool-trail reader) instead of
the existing `completedAssistantResponseFromTranscript` end-of-turn oracle.
It is not a frontend spinner, polling, or deduplication defect.

The mandatory P0 must therefore cover both terminal-signal directions:

1. no completion may be emitted for assistant text attached to `tool_use`;
2. exactly one matching completion must be emitted after `end_turn`;
3. the final answer must be visible, busy must clear only then, and the same
   Session must remain reusable.

The Claude producer half is now repaired in `multi-llm-provider-go`: retained
completion reads only a committed `stop_reason=end_turn`, with a regression
test based on this live transcript. PLAT-105 remains P0 blocking until the
restarted product path proves the canonical completion reaches AgentWorks once,
settles busy at the correct boundary, and leaves the Session reusable; the
older missing-completion direction is also still independently open.

### 2026-08-21 Video Studio recurrence — ordinary interactive turn used a two-second competing clock

Video Studio session `5e79aae3-d8f8-45d4-a29d-12258dba0d1e` reproduced the
same terminal-signal violation outside the retained-turn reader. Claude Code
successfully completed three `api-bridge` tool calls and its tmux pane displayed
final-looking character-generation narration. The native JSONL transcript still
contained only `stop_reason=tool_use`; no current-turn `end_turn` had committed.
The ordinary interactive adapter treated pane readiness as completion, waited a
fixed two-second transcript grace, raised `current turn has no completed
end_turn response`, and discarded the persistent tmux session.

This was not an MCP, shell, credential, or frontend failure. The retained-turn
repair described above already used the correct completion oracle, but
`ClaudeCodeInteractiveAdapter.GenerateContent` still had a second completion
clock: `waitForMarkedResponse` followed by `claudeTranscriptCommitGrace`.

The shared provider repair removes that fixed grace. Once a transcript exists,
the adapter now waits for its authoritative non-empty `stop_reason=end_turn`
until the owning turn context is cancelled. Pane readiness remains a transport
hint and cannot terminate an active tool loop. Context cancellation is returned
as cancellation rather than being rewritten as a transcript-protocol error.
Regression coverage delays the final `end_turn` beyond the former two-second
boundary and separately proves that turn cancellation still bounds the wait.

Focused provider tests pass. This ticket remains open until the restarted Video
Studio path proves the final answer is visible, exactly one canonical completion
settles busy, and the same Claude session remains reusable.

## Blocking live regression — final response captured, turn never settled

The 2026-08-15 Social Media backlog-audit chat provides a concrete production
reproduction. Session
`86500e07-f362-4d47-8b47-220282b0e981` ran in tmux
`mlp-codex-cli-int-1786777150030462000-719b6a79`. The provider completed the
work, and `server_debug.log` recorded at 12:35:00:

```text
codex interactive response captured owner=86500e07-f362-4d47-8b47-220282b0e981
tmux=mlp-codex-cli-int-1786777150030462000-719b6a79 elapsed=5m47.854s
codex interactive trailing capture skipped ... reason=persistent_session
```

The tmux process subsequently no longer existed and runtime health showed zero
running processes. Nevertheless, AgentWorks still exposed the Chat tab as
busy, kept the cancel action and spinner visible, and showed only `59 tool
calls`; the captured final assistant response was not projected into Formatted
mode.

This proves that successful provider response capture is not reliably followed
by the canonical terminal event that settles the host turn. The defect is at
the mcpagent-to-AgentWorks lifecycle boundary, not in the spinner component.
Frontend timeout, polling, deduplication, or tmux-pane heuristics are not valid
primary fixes.

### Why previous fixes did not close it

The system still has overlapping notions of completion:

1. the coding CLI returned/captured a final response;
2. the provider process or tmux became idle or exited;
3. the mcpagent Session considers a turn active or complete;
4. AgentWorks holds `sessionBusy` and tracked-execution state;
5. the terminal event store projects the state and final answer to the UI.

Earlier fixes reconciled individual consumers or made the durable Session own
retained delivery. They did not prove that every accepted turn emits one
terminal completion and that AgentWorks consumes that event before the
provider session is retained or discarded. In this reproduction, the adapter
captured the answer but the host-side turn-completion defer/settlement never
ran. Whether the producer omitted the event or the bridge dropped it must be
pinned with tracing before changing code; the observable contract failure is
already established.

## Required per-turn lifecycle contract

The durable conversation and an individual turn have different lifetimes. A
tmux/provider session may remain alive for later messages without keeping its
current turn busy.

1. Assign one stable `turn_id` when AgentWorks accepts a user or scheduled
   message. Preserve it through AgentWorks, mcpagent, the provider adapter,
   normalized events, persistence, and UI projection.
2. For every accepted turn, mcpagent must emit exactly one terminal
   `unified_completion` carrying that `turn_id`. A successful final assistant
   response must be included in, or durably correlated with, that completion.
3. AgentWorks must clear `sessionBusy` and complete the tracked execution from
   that canonical event. It must not infer normal completion from tmux pane
   contents, provider process lifetime, polling inactivity, or the continued
   existence of the reusable Session.
4. Provider/tmux lifetime represents conversation transport availability only.
   It must never be used as the busy state of the most recent turn.
5. Completion is idempotent: duplicate terminal signals for the same `turn_id`
   are ignored, while a second accepted message receives a new `turn_id`.
6. Add diagnostic reconciliation as a safety net, not the main lifecycle: if a
   durable final response exists, no provider turn is live, and canonical
   completion is missing after a bounded grace period, record the invariant
   violation and settle that same turn exactly once. Do not silently leave the
   UI busy and do not manufacture a second assistant message.

### Implementation sequence

1. Add trace logging for `turn_id` at response capture, Session terminal-event
   emission, bridge receipt, AgentWorks settlement, and UI-event persistence.
   Use the live reproduction to identify whether the producer or bridge loses
   the event.
2. Repair that single ownership path so provider final-response capture closes
   the mcpagent turn and emits its canonical completion before retained-session
   cleanup or reuse.
3. Make AgentWorks settlement consume the event idempotently and clear busy in
   a defer/failure-safe path.
4. Remove any frontend or server workaround that independently guesses that a
   turn ended, once the contract test proves the canonical path.

### Mandatory P0 regression

Extend IC-11 with a real host-visible retained-conversation test. It must use
the normal product path rather than constructing registry or completion state:

1. start a real first turn and wait for its final response;
2. assert the final assistant response is persisted and visible in Formatted
   mode;
3. assert Chat busy is false, the spinner/cancel state is gone, and tracked
   execution is terminal;
4. assert exactly one `unified_completion` exists for the first `turn_id`;
5. assert the provider Session/tmux remains reusable;
6. send a follow-up through that same Session and repeat assertions 2–4 for a
   distinct `turn_id`;
7. assert exactly one user message, assistant response, and terminal completion
   per turn, with no Agent reconstruction and no duplicate tool receipts.

The P0 must fail if it observes only provider-level response capture while the
host remains busy. Tests that directly invoke `Session.Close`, inject a
completion event, or manually manufacture the retained window do not satisfy
this requirement.

## Problem

AgentWorks has two delivery paths for a follow-up message:

- when a short-lived `mcpagent.Agent` is still registered, it calls
  `mcpagent.DeliverAgentInput`;
- when the Go agent object has ended but its coding-CLI tmux remains live,
  `deliverRetainedMainTerminalInput` detects the provider, reconciles it against
  the stored continuation request, reads
  `GetCodingAgentProviderContract(...).SupportsLiveInput`, and calls
  `llmproviders.SendCodingAgentLiveInput` directly.

The second path is fast and functionally necessary today, but it makes the host
application own provider transport details that mcpagent already owns.

The same split also drops structured tool observability on retained turns. A
real 2026-08-14 Codex follow-up executed `execute_shell_command` and
`agent_browser` repeatedly through the session-scoped MCP bridge, and the
backend logs carried every call under the correct session ID, but Formatted
mode displayed no tools. The initial wrapped turn receives transcript-derived
`tool_call_start/end` events; the direct retained-tmux branch bypasses that
stream and did not publish the bridge's authoritative execution receipts.

## Root cause

**This is a lifetime and reachability defect, not a missing abstraction.**

`mcpagent` already implements transport-neutral submission in
`agent/message_delivery.go` (`deliverUserMessage`). That function normalizes and
validates the message into a typed `CodingAgentDeliveryError`, resolves the
coding-agent contract, routes tmux live-input against structured/API steer
queueing, and returns a typed `UserMessageDeliveryResult{Provider, Transport,
DeliveryStatus}`. It is exported as `mcpagent.DeliverAgentInput`.

Its only defect is its **signature**: it requires a `*Agent`.

AgentWorks registers `runningAgents[sessionID]` immediately after
`FinalizeDefinition` and deletes it when the streaming loop ends — the in-code
comment reads *"Clean up running agent reference (steer injection no longer
possible)"*. The provider tmux is unaffected and stays live. So the map entry is
scoped to the **streaming turn**, while the provider conversation is scoped to
the **tmux process**. The only transport-neutral delivery path in the system
becomes unreachable at exactly the moment it is needed, and the host
re-implements it against provider internals.

The older fallback compounded this by treating a missing Go agent as a missing
provider conversation and rebuilding a complete agent — tools, skills, MCP
configuration, permissions, and provider startup — even when a reusable coding
session still existed. PLAT-102 added the correct fast retained path, but at the
AgentWorks layer rather than by fixing reachability in mcpagent.

## What already exists (do not rebuild)

An implementer must start from these, not from a blank sheet. Three of the four
pieces the previous revision of this ticket proposed are already shipped:

| Proposed responsibility | Already implemented as |
|---|---|
| `Session.Submit(message)` | `Session.Send(ctx, input) (DeliveryResult, error)` — `agent/turn_session.go` |
| `Session.Events()` | `Session.Events() <-chan *events.AgentEvent`, plus `mcpagent.SubscribeAgentEvents` |
| `Session.Close(reason)` | `Session.Close() error` — contract pinned by `TestAgentAndSessionCloseContractsMatch` |
| Opaque continuation state | `AgentSessionHandle` (`agent/session_handle.go`), persisted per conversation by `CodingSessionStore` (`agent/coding_session.go`); provider-native state nested in `llmtypes.CodingProviderSessionHandle` |

`Session` is pinned by `TestSessionPublicMethodSurface` to exactly
`{Close, Events, Run, Send, Snapshot}`.

**The single missing property** is that `Session` is minted by `Agent.Start()`
and holds a `*Agent` field, so its lifetime is bound to the agent object rather
than to the durable provider conversation. That binding — not the API shape — is
what this ticket must remove.

Introducing a second, parallel session type would leave the codebase with four
overlapping continuation concepts. It must not happen.

## Required change

Make the existing delivery path reachable by **session identity** rather than
**object identity**, and let the retained conversation own a `Session` whose
lifetime matches the provider process.

1. Decouple `Session` from the `*Agent` turn object, so a session created for a
   new conversation survives the streaming loop that created it.
2. Give mcpagent a lookup from `sessionID` to that live session, so a warm
   follow-up resolves a session without an `*Agent` and without reconstructing
   configuration.
3. Move retained completion, reconnection, and sidecar ownership out of
   AgentWorks and into the provider adapters behind the session.
4. Replace both AgentWorks delivery branches with one session submission call.
   AgentWorks must stop identifying providers, reading `SupportsLiveInput`,
   parsing pane readiness, and reading provider sidecars.
5. Delete the superseded provider-specific retained-turn code in AgentWorks only
   after real E2E parity is proven.

## Implementation status — 2026-08-14

The safe first migration slice is implemented and covered by tests:

- `mcpagent.Session` is registered by session ID. `CloseSession` removes it, and
  closing an older replaced session cannot accidentally remove the newer session.
  `LLMAgentWrapper` now owns that session across main-chat turns.
- both warm AgentWorks submission endpoints resolve that durable session first
  and call `Session.Send`; they no longer reconstruct an agent for the normal
  between-turn case;
- the delivery acknowledgement reports the transport actually used, including
  a structured transport selected in spite of a provider contract whose default
  is tmux;
- the MCP bridge publishes canonical direct-execution tool start/end/error
  receipts for retained turns, including arguments, result/error, and duration;
  those receipts are suppressed while a wrapped run is active because that run
  already has transcript-derived tool events. This prevents both forms of the
  observed bug: missing retained tool rows and duplicate ordinary tool rows.
- the one-turn `Agent.Run` convenience path closes its temporary `Session`
  before returning, so it cannot pin an Agent and its tool/MCP state in the
  process registry indefinitely;
- `Agent.Close` invalidates a registered session only when that session still
  belongs to the exact Agent being closed. This prevents both a half-closed
  Agent remaining addressable and an old replaced Agent deleting the newer
  session under the same conversation ID;
- if a warm `Session.Send` fails, AgentWorks now tries the still-live retained
  terminal before rebuilding a complete turn. An accepted structured-transport
  queue result does not trigger that bypass.

### Resolved regression — `Session.Send` delivered input but owned no completion lifecycle

A real Pi continuation exposed a lifecycle hole after the durable delivery work.
Session `5c0ca18f-4b1f-4933-b7f2-be89bc90049c` accepted its follow-up through
`mcpagent.Session` at 13:09:30. Its tools continued until 13:11:59 and the Pi
tmux then exited, but AgentWorks still showed the conversation busy more than
30 minutes later. Delivery was successful; completion was never observed.

The cause was precise: `Session.Send` called the transport-neutral delivery
function and returned its acknowledgement, but only `Session.Run` owned a turn
lifecycle. AgentWorks' older retained-tmux path used its own pane observer, but
the new durable-session path deliberately bypassed that host-owned observer.
The migration had therefore removed the only completion detector without
putting one behind the Session contract.

The repair keeps one owner:

- an idle `Session.Send` that is accepted by a retained tmux starts a Session-
  owned completion watch using the provider adapter's canonical retained final
  response;
- the Session appends the user and assistant messages to its durable history
  and emits exactly one `unified_completion` marked
  `source=mcpagent_session`;
- direct sends are serialized, and `Session.Run` rejects while a retained turn
  is being delivered or remains active. A rapid follow-up therefore steers the
  same retained turn instead of racing a second foreground run;
- AgentWorks marks the host conversation busy after delivery but starts no
  second tmux observer. It settles from that canonical completion instead;
- `BaseEventBridge` does not carry an AgentWorks terminal ID. The settlement
  code therefore resolves the main terminal from the owner session only when
  the nested completion has the exact `mcpagent_session` source marker. A child
  completion cannot accidentally finish the main conversation;
- `Session.Run` now marks the Agent turn in flight. This makes the existing
  direct-receipt suppression effective and prevents bridge receipts from being
  duplicated alongside transcript-derived tool events during ordinary turns.

Focused regressions prove Session-owned completion/history, direct-receipt
suppression during `Session.Run`, and settlement through the real bridge event
shape with no terminal ID. `go test ./agent/...` and `go build ./...` pass in
`mcpagent`; focused AgentWorks retained-turn tests pass. A backend restart and a
live Pi continuation remain the final runtime proof; this document does not
claim that proof before it is run.

### Resolved regression — the main chat path did not retain its session

Review on 2026-08-14 found that two bullets above were mutually exclusive on the
path that matters. `Agent.Run` closing its temporary session and the durable
session surviving the wrapped turn cannot both hold, because **the main chat
turn runs through `Agent.Run`**:

```
server.go            llmAgent.StreamWithEvents(agentCtx, chatQuery)
  llm_agent.go       runtimeAgent.Run(ctx, mcpagent.Turn{...})   <- Agent.Run
    turn_session.go  Start() registers -> defer session.Close() unregisters
```

`Agent.Run` calls `Start` (which registers) and now defers `Close` (which
unregisters). The session is therefore destroyed at exactly the moment the
retained window opens. `LookupSession(sessionID)` misses, and both AgentWorks
endpoints fall through to `deliverRetainedMainTerminalInput` — the provider-
specific branch this ticket exists to retire. **The new path is inert on the
only flow that reaches it.**

Verified directly rather than by inspection: a probe that called `Agent.Run` on
an agent with a session ID and then queried the registry reported the session
gone. `internal/agentsession` is unaffected because it holds a `Session` from
`Start` and calls `Session.Run`; that subsystem is not the chat turn.

**Why no test caught it.** `turn_session_registry_test.go` calls `Start` and
`Close` directly and never exercises `Agent.Run`, so it proves registry
mechanics against a path the product does not take. Build, unit tests, and the
focused server tests all pass while the feature does nothing. This is precisely
the failure mode IC-11 proof 2's anti-requirement was written to prevent — the
retained window must be reached by letting a real turn end, never by
constructing the state directly.

**Resolution.** The dilemma is false: `Agent.Run` should not have to choose
between pinning an Agent forever and destroying a durable conversation. Session
ownership belongs to the caller that owns the conversation. `LLMAgentWrapper`
now holds one `Session` obtained from `Start` for the lifetime of the chat
session and drives turns through `Session.Run`, exactly as
`internal/agentsession` already does, leaving `Agent.Run` a genuine one-shot
convenience whose deferred `Close` is correct and harmless. Replacing or
removing a stored wrapper closes its prior wrapper-owned Session, so the map no
longer silently drops an Agent/Session graph.

A unit regression test drives the wrapper's turn method and proves the session
remains resolvable after that method returns, then proves wrapper close
unregisters it. This closes the inert-path regression but is not substituted
for IC-11 proof 2: that proof must still complete a real CLI streaming turn
before exercising the retained-window follow-up.

### Resolved regression — retained synthetic turns replayed the first user message

The durable-session migration initially gave `LLMAgentWrapper` and
`mcpagent.Session` separate histories. `StreamWithEvents` appended the current
prompt only to the wrapper history and called `Session.Run` with `Turn.History`
but an empty `Turn.Input`. That works once: after the first run the Session owns
non-empty history and deliberately ignores later `Turn.History`. Every later
background completion therefore resumed the provider with the last human input
from the first turn instead of the supplied auto-notification.

This was reproduced in the live `salesoutreach` session: prompt receipts at
12:01, 12:08, 12:13, and later synthetic turns all recorded `hi`, even though
the server log showed distinct background-agent completion dispatches at those
times. It was a backend replay, not a frontend rendering duplicate.

`LLMAgentWrapper` now snapshots only prior wrapper history and always passes the
current prompt as `Turn.Input`. The Session appends that input to its own durable
history on every run. A focused regression test pins that continuation input so
future session-lifetime changes cannot silently revive stale-message replay.

The implementation is intentionally **not marked complete**. A provider tmux can
survive a backend restart while the new in-memory session registry cannot. The
old AgentWorks retained-terminal branch therefore remains only as cold-restart
compatibility. Retained completion/reconnection and sidecar ownership also
remain in AgentWorks. They can be deleted only after IC-11's real retained-window
E2E proves delivery, tool events, final response, provider exit, and restart
behavior through the shared session contract.

The cold-restart decision is now explicit: the fallback is **not** the desired
permanent architecture. A server restart must rehydrate the mcpagent session
from the persisted `AgentSessionHandle` / `CodingSessionStore` before the static
neutrality gate can turn green. Session identity alone is insufficient while
the registry is process-local. Until rehydration and IC-11 exist, removing the
fallback would regress live tmux conversations that survive a backend restart.

IC-11 proof 2 is now implemented in `coding_agent_chat_e2e.go` and is required
for every selected provider by `scripts/run-coding-cli-p0.sh`. It lets the real
first turn finish, verifies the foreground steer target is gone while tmux is
live, submits a tool-backed follow-up, and requires the durable mcpagent source,
actual transport, sub-second acknowledgement, exactly one paired tool receipt,
exactly one final response, no `agent_start`, and a reusable tmux. The harness
has now passed authenticated live runs for Codex and Claude Code. The Claude run
used `claude-sonnet-5`. Cursor accepted model `auto` and completed turn one, but
the live logs showed `Executing Cursor CLI structured: cursor-agent --print`
and no retained tmux existed, despite the provider contract declaring tmux.
That is a real contract/adapter mismatch; proof 2 correctly remains red for
Cursor rather than pretending a structured one-shot process was retained.

The mismatch was traced to multi-provider commit `4c9e1825`: it changed
`CursorCLIAdapter.GenerateContent` to unconditionally execute structured
`--print`, but left the shared provider contract and AgentWorks interactive-chat
configuration on tmux. The repair does not force every Cursor use case onto one
transport. It restores the existing explicit selector:

- ordinary interactive chat follows the provider contract and uses retained
  tmux, so live input and the IC-11 retained window are real;
- workflow/background execution continues to pass
  `WithCursorStructuredTransport(true)` and receives typed stream-json events.

Regression coverage pins unset/false to tmux and true to structured. Focused
provider, mcpagent, wrapper, and server tests pass, as do `go build ./...` in
both `multi-llm-provider-go` and AgentWorks. The authenticated real Cursor tmux
contract also passes with model `auto` (`TestCursorCLIRealInteractiveTmuxFullContract`,
44.64s), including reuse of the same tmux session for its second turn. After the
backend restart, the cross-repository Cursor IC-11 run passed too: the real first
turn completed, the retained session accepted the follow-up, exactly one tool
receipt and one final response were observed, no Agent reconstruction occurred,
and the tmux remained reusable.

Verification completed for this slice:

- `go test ./agent/... -count=1` in `mcpagent`;
- focused AgentWorks live-input/retained/coding-agent server tests;
- the `LLMAgentWrapper` session-lifecycle regression test;
- `go build ./...` in `agent_go`.

### Two defects this ticket must absorb

**a. The typed acknowledgement can report the wrong transport.**
`deliverUserMessage` sets `result.Transport = contract.Transport` — the
*declared* contract transport — but routes on `a.usesStructuredTransport()`,
which is the union of `wantsStructuredTransport()`, the per-provider
`CodexStructuredTransport` / `CursorStructuredTransport` / `PiStructuredTransport`
flags, and a non-tmux contract. A Codex run on the structured transport
therefore returns `Transport: "tmux"` alongside `DeliveryStatus:
"queued_for_injection"`. The acknowledgement contradicts itself.

This is not hypothetical: `legacyCodingProviderSessionHandle`
(`agent/session_handle.go`) already carries a comment documenting the identical
bug class found live on the continuation handle, where recording
`Transport=tmux` for a structured turn silently lost all turn-1 context on
Codex/Cursor. The delivery result needs the same fix — report the transport the
call **actually used**.

**b. There is no `rejected` status.** `UserMessageDeliveryStatus` has exactly
two values, `sent_to_cli` and `queued_for_injection`. Rejection is an `error`
return carrying `CodingAgentDeliveryError{Kind, Provider, Reason}`. Either add
an explicit rejected status or state that rejection stays a typed error — but
the contract must say which, because acceptance depends on it.

**Decision implemented:** `DeliveryResult` represents accepted delivery only;
rejection remains a typed `CodingAgentDeliveryError`. `Session.Send` no longer
replaces an empty-message rejection with a generic formatting error, so hosts
can branch on the stable error kind without parsing text.

## Provider adapter responsibilities

Adapters implement the mechanics against the transport recorded on the handle
(`llmtypes.CodingProviderTransportTmux` / `CodingProviderTransportStructured`),
plus API-backed providers:

- **interactive tmux:** inject input, observe provider readiness, read the
  authoritative transcript/sidecar;
- **structured JSON/event:** submit through the provider channel and normalize
  its structured stream;
- **API:** continue through the provider conversation identifier and normalize
  streamed or bounded responses.

## Migration plan

1. Fix the transport-reporting defect in `deliverUserMessage` and decide the
   rejection representation. Both are prerequisites for a stable contract.
2. Decouple `Session` lifetime from `*Agent` and add session lookup by
   `sessionID`.
3. Have agent creation register the durable session handle for the conversation.
4. Move retained completion, reconnection, and sidecar ownership from AgentWorks
   into the provider adapters.
5. Replace both AgentWorks delivery branches with the single session call.
6. Update the pinned API golden files **in the same commit** (see Gates).
7. Land IC-11 proof 1 (static neutrality assertion) with step 5, and IC-11
   proof 2 (retained-window E2E) before step 8.
8. Remove the superseded AgentWorks retained-turn code only after E2E parity.
   IC-11 proof 1 turning green is the objective signal that this is complete.

## Proposed certification — IC-11: retained submission neutrality

The existing P0 certification suite cannot catch this defect, and it is worth
being precise about why before adding coverage.

`requiredP0CertificationIDs` includes `CertLiveInput` and `CertBusyLiveInput`,
both enforced as real E2Es. Neither helps here, for two independent reasons:

- **Wrong layer.** Those certifications live in `multi-llm-provider-go` and
  prove Boundary 3 (adapter ↔ CLI). They pass identically whether AgentWorks
  routes through mcpagent or bypasses it and calls
  `llmproviders.SendCodingAgentLiveInput` itself — which is what it does today.
  The broken boundary is the one they do not observe.
- **Wrong moment.** Every live-input certification and the IC-10 chat E2E
  exercise the *in-flight* case: `coding_agent_chat_e2e.go` starts a query,
  waits for the session to become steerable, and steers mid-turn.
  `CertBusyLiveInput` is explicitly a follow-up during a slow tool call. This
  ticket's defect lives in the window *between* turns, after
  `runningAgents[sessionID]` is deleted and while the tmux is still alive. No
  suite enters that state.

Also note `RequiredP0CodingAgentCertificationIDs` returns `nil` for any
non-tmux contract, so structured-transport runs currently have **no required P0
floor at all** — directly relevant to acceptance 3 and 6.

This coverage therefore belongs at Boundary 1 as a new integration-contract
area in `agent_go/docs/cross_repo_integration_contract.md`, **not** as a new ID
in the `multi-llm-provider-go` certification registry: that registry is keyed
per provider adapter and its runner asserts the proof lives under
`pkg/adapters/<provider>/`, which is the wrong repo and the wrong layer.

### IC-11 — Boundary 1 (coding-agent-loop → mcpagent)

*A follow-up to a live provider conversation is submitted through one mcpagent
API, with no provider-specific knowledge in the host.*

**Proof 1 — static neutrality assertion (always on, no CLI required).**
An AST test over `agent_go/cmd/server` asserting that the package contains zero
references to `llmproviders.SendCodingAgentLiveInput`,
`GetCodingAgentProviderContract`, and the retained sidecar readers. This mirrors
the approach already used in `mcpagent/agent/public_api_golden_test.go`, runs in
ordinary CI, and is the assertion that actually prevents regression — a runtime
spy can only prove one path was not taken on one run, whereas this proves the
capability is absent. It also gives migration step 7 an objective completion
signal.

**Proof 2 — retained-window live E2E (opt-in, gated).**
The state no current suite reaches. Following the `cmd/testing/` opt-in live
command pattern used by IC-10:

1. Start a turn and let it complete.
2. Wait until `runningAgents[sessionID]` has been deleted **and** the provider
   tmux is still live — the assertion setup, not an incidental precondition.
3. Send a follow-up.
4. Assert: it is accepted; the acknowledgement is the typed session result with
   the transport that was actually used; exactly one non-empty
   `unified_completion.final_result` is persisted; the session settles while the
   tmux stays reusable; and delivery latency stays within PLAT-102's envelope.
5. Assert no agent reconstruction occurred — no tool-registry or MCP-bridge
   rebuild for the warm turn. PLAT-102 established that reconstruction costs
   seconds and is visible in the startup-timing stages, so this is observable
   rather than inferred.

Run it per transport, enumerated: Claude Code, Codex, Cursor, and Pi have
retained sidecar recovery today. Structured and API transports are explicitly
out of scope for proof 2 until acceptance 6's gap is closed; that exclusion must
be recorded rather than left implicit.

**Anti-requirement.** The E2E must reach the retained window by letting a real
turn end, never by deleting the agent registration to manufacture the state.
PLAT-035 records the precedent — a regression test that injected `streaming_end`
itself proved what happened *after* an event arrived but not that a real
retained input produced one — and `CertReplyFormattingFidelity` carries the same
warning that *"a green test that proved nothing is how this defect shipped in
the first place."*

## Gates

- `agent/public_api_golden_test.go` pins the public surface and its own comment
  requires that *"any deliberate change updates this list in the same commit."*
  A new exported function must be added to `TestPackageFunctionSurfaceDoesNotRegrow`,
  and — if its signature mentions `*Agent` — also to `TestAgentFacadeFunctionSurface`.
  New `Session` methods change `TestSessionPublicMethodSurface`. `*Agent` itself
  is pinned to exactly four methods and zero exported fields; this work must not
  regrow it.
- PLAT-099 hazard: `deliverRetainedMainTerminalInput` currently clears `modelID`
  when the live tmux provider disagrees with the stored continuation provider,
  which is what stops a message reaching a stale provider after Update
  Automation switches providers. Moving that logic behind the session must
  preserve it, or PLAT-099 reopens. Acceptance 5 owns this.

## Acceptance

1. AgentWorks uses one mcpagent API for follow-up submission regardless of
   transport, and contains no provider identification, `SupportsLiveInput`
   check, pane-readiness parsing, or sidecar read. Proven by IC-11 proof 1,
   which fails if any of that capability remains in `agent_go/cmd/server`.
2. A warm follow-up does not rebuild an `Agent`, tool registry, skills, MCP
   configuration, or provider process.
3. Every transport returns the **same acknowledgement type**, with transport
   differences expressed as field values, never as different call shapes. The
   reported `Transport` matches the transport actually used, including when a
   per-provider structured flag overrides a tmux contract. Codex legitimately
   accepting a follow-up as the *next* turn while Claude Code, Cursor, and Pi
   accept it in-turn is a value difference, not a contract violation.
4. Final responses, tool calls, errors, queueing, cancellation, rate limits,
   reconnection, and process exit have transport-independent host semantics.
   In particular, a warm retained turn emits exactly one visible tool row per
   real bridge execution, carrying the authoritative arguments, result/error,
   and duration; it must neither omit the call nor duplicate it with a
   transcript-intent event.
5. Rapid messages are serialized per session and never merged or delivered to a
   stale provider after a configuration change. The PLAT-099 provider-switch
   case has explicit regression coverage.
6. Real E2E covers first turn, warm continuation, concurrent submission,
   provider exit, reconnection, and final-response recovery for **each supported
   transport**, enumerated explicitly. Note the current gap:
   `ReadCodingAgentRetainedTurnMessages` implements retained-turn recovery for
   Claude Code, Codex, Cursor, and Pi only, and returns `nil` by default — so
   structured and API transports have no retained-response path yet. Either
   close that gap here or scope this acceptance to the tmux transports and open
   a follow-up.
7. PLAT-102's warm retained latency does not regress; the measured sub-second
   delivery path is preserved.

## Non-goals

- Do not introduce a new session type alongside `Session`, `AgentSessionHandle`,
  and `CodingProviderSessionHandle`.
- Do not force every provider to use tmux or pretend API calls are persistent
  processes.
- Do not standardize provider-internal mechanics; standardize host-visible
  submission and lifecycle semantics.
- Do not remove the current fast path before the shared contract has equivalent
  runtime coverage.
