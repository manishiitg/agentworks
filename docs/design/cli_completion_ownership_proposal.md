# Proposal to prevent duplicate CLI replies

**Status:** Draft proposal, implementation pending.

**Date:** 12 October 2026.

**Scope:** CLI chat completion ownership, event persistence, and recovery.

AgentWorks can publish one Muse answer several times after the person sends follow-up messages during an active response. The proposed repair gives each active CLI conversation one completion owner and prevents recovery from republishing a consumed CLI completion. Steer remains the default; Queue next retains its existing dispatcher. The chat continues to display user and CLI messages as they arrive.

## Expected chat behavior

Steer delivers a message to the running CLI without waiting for the current answer. The CLI may incorporate several messages into one response or process them as separate responses. The platform must display the CLI's actual output without requiring one assistant answer for every user message.

Queue next is an explicit instruction to finish the active task before starting the queued message as its own turn. Its durable queue, ordering, and owner controls remain responsible for that behavior.

Delivery acceptance, assistant output, and execution completion are separate facts. An acceptance receipt updates the submitted message. A CLI output event contributes to the transcript. A completion updates lifecycle state and may carry the answer already represented by the stream; it must not create another copy of that answer.

## Confirmed local incident

The local event store recorded the following sequence on 12 October 2026. All times are IST. Message contents and account identifiers are omitted.

| Time | Event sequence | Observation |
|---|---|---|
| 17:17:00.732 | 6093 | The SDK emitted a canonical completion with the final answer and a turn ID. |
| 17:17:01.849 | 6094 | A host fallback emitted an empty completion for a pending request. |
| 17:17:19.666 | 6095 | The host emitted the same final answer under another request ID. |
| 17:17:37.998 | 6096 | The host emitted that answer again under another request ID. |
| 17:17:56.143 | 6097 | The host emitted that answer again under another request ID. |

The local diagnosis uses `mcpagent` commit `c13ab2a754b3` and provider commit `f1f4a375101c`. The proposal PR starts from main commit `4594d0fde`, which retains the shared host promotion path but pins newer provider commit `535a3f7f4193`. Revalidate the Muse reader behavior against that newer pin before implementation; the incident evidence establishes the local runtime behavior.

The non-empty final results were identical. The host copies used `source: coding_agent_sidecar`, different execution IDs, and no canonical turn ID. Those request IDs came from follow-up messages already accepted into Muse, rather than messages waiting in Queue next.

The inspected event interval contained two email-send tool calls before the canonical completion and no further tool calls during the duplicate completions. This establishes repeated reporting in this incident; it is not a guarantee about external delivery or every agent run.

## Existing layer responsibilities

| Layer | Existing responsibility |
|---|---|
| Chat composer | Defaults to `steer`, remembers the selected mode, and includes `delivery_mode` in the submission. |
| Submission coordinator | Coalesces concurrent submissions of the same session and text to prevent accidental double submission. It is not the durable Queue next dispatcher. |
| Backend dispatcher | Routes explicit Queue next submissions into the durable conversation queue and claims them when the conversation is free. |
| `mcpagent.Session.Send` | Serializes delivery operations into the existing session and coordinates the SDK's retained response monitoring. It does not wait for the assistant answer before acknowledging delivery. |
| Provider adapter | Delivers input through the provider's native transport and reads its transcript using provider-specific boundaries. |
| Host lifecycle observer | Tracks busy state and execution receipts; it also supplies compatibility monitoring when no SDK session owns the retained terminal. |

The SDK already supports a follow-up that was answered by the preceding response: `completeRetainedTurnAnsweredByPrevious` emits a completion with empty final text and the `answered_by_previous_response` marker. It also marks an earlier completion with `live_input_followup` when a later SDK response still needs monitoring.

## Root cause

AgentWorks initially recognizes SDK completion ownership in `markMCPAgentSessionTurnRunning`, which avoids starting a second host watcher. However, accepted follow-ups are still appended to `retainedMainTurnPendingExecutionIDs`.

When a completion settles the tracked execution, `observeRetainedMainTurnEvent` promotes the next pending execution and starts `observeRetainedMainTurnStreamMode`. This starts host monitoring even though the messages were delivered through the SDK and its follow-up logic already owns the response lifecycle. The host has inferred future CLI turns from submitted message IDs.

Muse's retained reader makes this overlap visible. `ReadRetainedTurnMessages` ignores the supplied turn-start argument and reads assistant commits after its stored submission sequence baseline. Host promotion advances its own time boundary but does not establish a new provider submission baseline. The reader can therefore return the answer just consumed by the SDK. The host durable-completion check accepts it before the safeguard for a promoted input with no new output runs.

Existing transcript suppression is scoped to execution and turn identity. Since the host copies have new execution IDs and omit the original turn ID, they survive those checks. Suppressing identical text globally would also suppress legitimate answers from different turns.

## Proposed repair

### Preserve delivery ownership

Leave the Steer and Queue next routing decisions in their existing layers. Do not introduce a new delivery queue, require one answer per input, or change the default mode. Keep delivery failures and uncertain acceptance distinct from completion failures so this repair cannot resend a message or repeat a tool action.

### Make completion ownership explicit

Track SDK ownership separately from compatibility monitoring. Retain message receipt IDs for delivery and execution bookkeeping, but do not interpret each ID as a future CLI response.

For an SDK-owned session, consume its canonical output and lifecycle events. A completion must not promote pending message IDs into new host watchers. Respect `live_input_followup` so an earlier response cannot clear a genuinely active follow-up. Respect `answered_by_previous_response` without rendering another reply.

Use the SDK's turn identity, session identity, and lifecycle generation to associate events and reject late completions from an older owner. `Session.Send` already returns `DeliveryResult.TurnID`; the host must preserve the association rather than retaining only the query ID. The implementation must also account for SDK follow-up handoffs that create a new canonical lifecycle, rather than assuming the initial delivery turn ID never changes.

Close receipts when the SDK lifecycle establishes their outcome. A completion alone must not mark every outstanding message answered: a later accepted input may belong to a newer lifecycle. If the SDK does not expose enough information to distinguish those cases, extend its lifecycle contract before removing the host's inference. Do not replace that contract with text comparisons or a timer.

### Fence recovery monitoring

A retained terminal without a live SDK session still needs host recovery monitoring. Preserve that path, with an explicit ownership handoff and a generation fence so an old host watcher cannot emit after SDK ownership resumes. A temporary absence from the running-agent map is not sufficient evidence to transfer ownership.

Recovery must track the last consumed provider completion using a stable native ID or transcript sequence where available. Re-reading a completion is allowed; republishing it under a fresh event ID is not. Persist enough identity with the structured event history to reconstruct the consumed boundary after restart. Keep progress and final-completion consumption distinct so streaming an answer does not accidentally discard its lifecycle event.

### Harden the Muse reader

The Muse reader must distinguish a new completion from a previously consumed one. Prefer native completion identity and sequence boundaries captured before submission. Its idle and quiet checks establish readiness, not ownership of an answer.

Do not simply filter everything before watcher creation time: a fast valid response can be committed before the watcher starts. The reader and its callers need an agreed boundary that preserves such responses while excluding older completions. Other adapters require their own contract checks; ignoring a time argument alone does not prove another adapter has the same defect.

## Implementation boundaries

| Component | Change to evaluate |
|---|---|
| `agent_go/cmd/server/server.go` | Preserve SDK turn associations; separate completion owner from receipt tracking; prevent SDK-owned pending requests from starting host watchers. |
| `agent_go/cmd/server/structured_completion_persistence.go` | Preserve canonical or native completion identity so repeat ingestion cannot create another final record. |
| `mcpagent/agent/turn_session.go` | Verify ownership and follow-up contracts; extend receipt-to-lifecycle association only if the host lacks a reliable outcome signal. |
| `multi-llm-provider-go/pkg/adapters/musecli/musecli_retained_turn.go` | Exclude consumed or unrelated completions while retaining valid fast responses. |
| Backend and SDK regression suites | Exercise the SDK-to-host handoff with an old non-empty answer available, rather than only an empty reader. |

The existing UI grouping and matching rules are not the primary repair. Existing stored duplicate messages are also a separate data-cleanup decision; this proposal prevents new duplicate records and output.

## Validation required before release

The shared host change reaches CLI agents that use the SDK-owned session path. Validate Muse, Claude Code, Codex, Cursor, and Pi, plus Agy or any other enabled provider using that path. Provider-specific differences in completion timing and queued follow-up behavior must remain supported. API and structured transports retain their existing injection and turn behavior.

| Scenario | Required result |
|---|---|
| Three Steer inputs incorporated into one response | All user messages remain visible; one actual answer is displayed; no host watcher manufactures extra replies. |
| Steer inputs answered separately by the CLI | Each actual response remains visible and the later response stays monitored. |
| Input arrives as a completion is being read | The late completion cannot settle the newer lifecycle or hide its output. |
| Two genuine turns return identical text | Both replies appear, with different turn identities. |
| Stream, canonical completion, and repeated ingestion represent one answer | One visible answer and one durable final record, with lifecycle state still completed. |
| SDK marks a follow-up answered by the previous response | Receipt state settles without additional assistant text. |
| Queue next during an active response | The message remains queued and executes once after release; queued messages drain in order. |
| Stop, crash, restart, or ownership handoff | No stale watcher publishes; unseen output is recovered; consumed completions are not replayed. |
| Delivery is uncertain | No automatic resend or repeated tool action is introduced. |
| Valid final commits before watcher startup | The final is preserved despite the fast completion. |

Add a failing regression for the observed sequence first: SDK-owned live inputs, one canonical completion, and a reader that continues returning that same non-empty final. Assert the persisted events, transcript output, receipt outcomes, busy state, and Queue next release. Unit coverage must include both delivery/completion race orders.

Then run the affected host and SDK lifecycle suites and provider reader tests. Real CLI checks must cover follow-ups during work and at turn completion; stand-in readers alone cannot prove native timing. Live checks should use harmless local tasks with an action counter to distinguish duplicate display from duplicate execution.

## Delivery sequence and release condition

1. Capture the failing backend regression and agree the SDK ownership and receipt contract.
2. Implement the bounded host ownership repair, including late-event fencing and persistence identity.
3. Add the Muse reader boundary repair if required by the contract; publish and pin dependency changes when needed.
4. Validate the provider matrix, explicit Queue next, and restart recovery.
5. Release only when actual later replies remain visible, busy state settles correctly, and the reproduced path creates no duplicate completion records.

The diagnosis is confirmed from the local incident. Cross-provider safety remains a release condition, rather than an assumption based on the Muse result.

## Related platform tickets

- [PLAT-178](https://github.com/runloop-workflows/deployments/blob/main/tickets/pulse_platform/chat/reliability/plat-178.md) is open and documents the trigger where rapid inputs are answered together. The current incident should be recorded as a recurrence under this ticket.
- [PLAT-351](https://github.com/runloop-workflows/deployments/blob/main/tickets/pulse_platform/coding-agents/sessions/plat-351.md) is open and introduced durable-response settlement when the provider pane does not report idle.
- [PLAT-539](https://github.com/runloop-workflows/deployments/blob/main/tickets/pulse_platform/chat/reliability/plat-539.md) records an older stale-answer replay fixed by refusing host completion when no turn is tracked. This incident still has tracked pending executions, so that guard does not cover it.
- [PLAT-422](https://github.com/runloop-workflows/deployments/blob/main/tickets/pulse_platform/coding-agents/muse/plat-422.md) concerns duplicate Muse input caused by resubmission after a stale refusal notice. It is a separate mechanism from the completion replay established here.

Ticket states were checked against the deployments repository main branch on 12 October 2026. This proposal does not modify ticket status or claim implementation is complete.
