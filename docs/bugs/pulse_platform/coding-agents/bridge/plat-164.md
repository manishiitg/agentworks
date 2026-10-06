[← platform / coding-agent-bridge](index.md)

# PLAT-164 — background agents retain only lifecycle summaries, not their own durable message and tool transcript

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | coding-agents |
| Area | bridge |
| Summary | P1 — a completed or failed background agent cannot be |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `open` |
| Last synchronized | `2026-08-21` |

- **Priority:** P1 — a completed or failed background agent cannot be
  forensically inspected after its live UI events age out. This affects every
  background execution, not only Pulse.
- **Owner:** generic background-agent lifecycle, conversation persistence, and
  provider event normalization.
- **Related:** [PLAT-114](plat-114.md) (durable lifecycle summary),
  [PLAT-160](plat-160.md) (provider tool-event completeness), and
  [PLAT-149](plat-149.md) (one reliable tool-event source).

## Evidence and root cause

`run_in_background` creates an isolated child agent and tags its live events
with a child correlation/session ID. Its start and completion are written to
the durable `background_agent_log` table introduced by PLAT-114; Pulse also
writes typed findings, receipts, and fix attempts to SQLite. Those are useful
outcomes, but they are not a transcript.

The child does not receive a persisted conversation path and does not call the
normal chat-history writer. Its intermediate assistant messages and tool
calls exist only in the parent session's live/UI-event cache, which is capped
and reused. Once that cache is trimmed or the server restarts, they cannot be
inspected. The same shape applies to generic `run_in_background` work,
todo-task delegation, scheduled children, and future product background jobs.

The restored Social Media Pulse run `schedule-cron--fba2d19b_1787243435990159000`
demonstrates the impact: its parent schedule conversation survives, and the
Measurement Validator's final lifecycle failure survives, but no JSON
transcript exists for child session `workshop-background-task-1787245962052297000`.
The operator cannot determine from durable evidence which messages or tool
calls led to the failure.

## Decision

Every background agent gets its own immutable structured transcript. This is a
generic platform contract, not a Pulse feature and not a second lifecycle
database.

For each workspace-scoped background execution, create a file below the
conversation-storage root, for example:

```text
sessions/<parent-session-id>/background/<agent-id>.json
```

The exact directory may vary for a product-owned conversation store, but the
identity and contents must not. A transcript contains only normalized:

- metadata: parent session ID, child ID, execution ID, kind, model/provider,
  start/end timestamps and terminal status;
- user/message-sequence prompts delivered to the child;
- assistant messages; and
- tool calls and final tool results, including failure details.

The parent conversation stores child transcript references/identities. It does
not copy child text into one unbounded parent JSON. SQLite remains the
authority for Pulse findings, decisions, receipts, cost and lifecycle state;
the child JSON is durable diagnostic evidence.

## Required behavior

1. Create the child transcript before its first provider turn. A child that
   fails during setup still leaves a terminal diagnostic record.
2. Append canonical events as they happen, then atomically mark the transcript
   terminal exactly once for completion, failure, cancellation, or timeout.
3. Reuse the same normalized event contract across all coding-CLI and API
   providers. Provider adapters may supply events differently, but consumers
   must not need provider-specific transcript formats.
4. Persist a reference in the parent/session index and retain the existing
   `background_agent_log` summary for fast lifecycle queries.
5. A storage write failure must be visible as a persistence failure in the
   child outcome/diagnostics; it must not silently claim that a fully auditable
   transcript exists.
6. Restoring a conversation may load child transcripts on demand for a later
   UI, but this ticket does not require a new UI. Direct file inspection must
   already make debugging possible after restart.

## Acceptance tests

1. A successful generic `run_in_background` execution leaves one child JSON
   containing its prompt, assistant response, and a paired tool call/result.
2. A failed child leaves the same record with its error and terminal status.
3. A scheduled/Pulse child and a todo-task delegate use the same persistence
   contract without special-case Pulse code.
4. A real provider fixture for every supported transport proves the stored
   event shape contains a final assistant response and paired tool result.
5. Restart the server, load the persisted parent/session index, and locate the
   child transcript without relying on `ui_events`, tmux scrollback, or an
   in-memory registry.

## Non-goals

- Do not duplicate typed workflow/Pulse state into JSON.
- Do not make a giant run-wide transcript the only storage object.
- Do not block useful work solely because optional UI projection is absent;
  the durable transcript write itself is the required observability boundary.

## Implementation (2026-08-21)

`implemented` — a generic, provider-agnostic transcript now covers every
background execution, not just the workshop-background-task example above.

**Storage path corrected from this ticket's own proposal.** The `sessions/`
root above has zero other usage anywhere in this codebase and does not match
how conversations are actually stored. The real, only existing convention is
`workflowBuilderConversationLogPath`
(`<workspacePath>/builder/conversation/<date>/session-<id>-conversation.json`,
`cmd/server/workflow_builder_session_routes.go`). Transcripts now live at
`<workspacePath>/builder/conversation/background/<sessionID>/<agentID>.json`
(`orchestrator/events.BackgroundAgentTranscriptPath`) — below the same storage
root, in a dedicated subtree keyed by session then agent, rather than a second
top-level convention.

**Two integration points, not one, after tracing the actual event flow:**

- `cmd/server/background_agent_transcript.go` — `createBackgroundAgentTranscript`
  writes the initial `"running"` record from `emitBackgroundAgentStarted`,
  which fires unconditionally the moment a background agent is *registered*,
  before any orchestrator-level construction that could itself fail. This is
  what actually satisfies requirement 1 ("create before the first provider
  turn... a child that fails during setup still leaves a terminal diagnostic
  record") — the granular event-append path below cannot guarantee that on
  its own, because a setup failure can occur before an observer is even
  attached. `finalizeBackgroundAgentTranscript` marks the transcript terminal
  from both `emitBackgroundAgentCompleted` (normal completion/failure) and
  `emitBackgroundAgentTerminated` (cancel/timeout — a genuinely separate
  terminal path in this codebase that never calls
  `emitBackgroundAgentCompleted` and does not update `background_agent_log`
  either; that gap is pre-existing and out of scope here, but the transcript
  itself must not — and does not — stay `"running"` forever for a canceled
  agent).
- `pkg/orchestrator/context_aware_bridge.go` — `ContextAwareEventBridge.HandleEvent`
  is confirmed, by tracing `setupStandardAgent`'s unconditional
  `baseAgent.AddObserver(cab)`, to be the universal fan-out every background
  agent's intermediate events (tool calls, user/assistant messages) already
  pass through, not only the workshop-background-task path this ticket's
  motivating example used. A new `BackgroundAgentTranscriptWriter` interface
  (implemented by `BaseOrchestrator`, mirroring the existing `TokenPersister`
  pattern exactly — same `ReadWorkspaceFile`/`WriteWorkspaceFile` I/O, same
  best-effort-with-visible-status philosophy) appends one normalized event per
  call. Scope is exactly `hasExecutionOwner && !strings.HasPrefix(executionOwnerID,
  "workflow-step:")` — the same condition `HandleEvent` already uses to tag
  `execution_kind: "background_agent"` into event metadata, so this requires
  no new classification logic and automatically covers `run_in_background`,
  todo-task delegation, and scheduled/Pulse children alike (requirement 3)
  without special-casing any of them.

**Normalized event contract** (`pkg/orchestrator/events/background_transcript.go`):
`BackgroundAgentTranscript` (metadata + terminal status + ordered events) and
`BackgroundAgentTranscriptEvent` (`user_message` | `assistant_message` |
`tool_call`). Tool calls reuse `mcpagent/events.ToolCallRecord` — the same
contract every other tool-call consumer in this codebase already reads —
rather than inventing a second shape. `NormalizeBackgroundTranscriptEvent`
maps `UserMessageEvent`, the orchestrator's own `OrchestratorAgentStart/End`
events (which carry the actual prompt/result text — the generic
`AgentEnd`/`unified_completion` events are deliberately suppressed upstream
for step-scoped agents to avoid duplicating UI output, so these are the
correct source), and `ToolCallStart/End/Error` via the existing
`events.ToolCallRecordFromEvent` helper.

**Requirement 5 (write failures must be visible).** `background_agent_log`
gained `transcript_path`/`transcript_status` columns (migrated via the same
`PRAGMA table_info` + `ALTER TABLE` pattern `ensureReportHumanInputColumn`
already uses, so existing databases pick it up automatically). Every
create/finalize/append call records its outcome — `"ok"` or `"error: ..."` —
so a reader of `background_agent_log` can tell a real gap from an assumed
one instead of a completed agent silently implying a fully auditable
transcript exists.

**Verified:** `go build ./...` clean. New tests: `pkg/orchestrator/events/background_transcript_test.go`
(path convention, marshal/parse round trip, per-event-type normalization,
including that unrelated event types like `token_usage` are correctly
excluded), `pkg/orchestrator/plat164_background_transcript_test.go` (bridge
appends for a background-owned execution, correctly skips a
`workflow-step:`-owned one and a no-execution-owner top-level chat turn),
`cmd/server/background_agent_transcript_test.go` (full start→append→terminal
lifecycle against a mock workspace API, the cancel/terminate path, and the
write-failure-is-visible case). `go test ./pkg/orchestrator/... ./cmd/server/...`
— zero new failures versus a clean `origin/main` baseline (three pre-existing,
unrelated guidance/markdown-template failures reproduce identically on both).

**Not done / explicitly out of scope this pass:**

- Live reverify against a real restored run (e.g. re-checking the
  `schedule-cron--fba2d19b_...` example above) is still pending — this pass
  is build/test verified only, following the same discipline other tickets in
  this register mark "implemented, live reverify pending".
- Requirement 6 (loading child transcripts on demand for a later UI) is
  explicitly not required by this ticket and was not built — direct file
  inspection under `builder/conversation/background/` is sufficient today.
- `background_agent_log`'s own pre-existing gap (the terminate/cancel path
  never sets that table's `status`/`completed_at`) was left as found; only the
  transcript itself was made to correctly reach a terminal state on that path.

## Scope-fix follow-up (2026-08-21)

A user question — "does Pulse's engineering review actually look at workflow
step children?" — sent this back into the code and surfaced a real bug in the
scoping this ticket shipped with.

**What ops-review (`llm_ops_review`) already needs.** `ops-review.md` item 7
("Container necessity") explicitly requires inspecting "the parent and its
owned children as one execution unit" for every `todo_task`/`routing`/
`message_sequence` container, plus "representative parent/child traces" more
generally. That evidence already existed before this ticket:
`controller_todo_task.go` writes a full `<attempt>-conversation.json` +
`<attempt>-timing.json` per child execution (complete `conversation_history`,
`llm_calls`, `tool_calls`), and `controller_execution.go` does the same for
plain step attempts. Neither of those pre-existing mechanisms is this
ticket's concern or was touched by it.

**The bug.** `HandleEvent`'s scoping condition for the transcript-append path
was `hasExecutionOwner && !strings.HasPrefix(executionOwnerID,
"workflow-step:")` — reusing a pre-existing classification block (commit
`ec8ad1eb`, 2026-07-15) that this ticket assumed correctly excluded
step-owned executions. It does not: grepping the whole codebase, **nothing
anywhere constructs an id with a literal `"workflow-step:"` colon prefix.**
The real conventions are `workflow-step-<n>-...` (hyphen) and bare
`exec-<step-id>-<timestamp>`, and the latter is exactly the
`ParentExecutionIDKey` value `controller_todo_task.go` and
`controller_execution.go` set for message-sequence/todo-task step-level and
child-agent contexts. The prefix check has therefore probably never matched
anything, before or after this ticket.

The consequence: because `AppendBackgroundAgentTranscriptEvent`
(`pkg/orchestrator/base_orchestrator_background_transcript.go`) fabricated a
fresh transcript on the fly whenever no header existed yet — reasoning that a
genuine background agent's granular event could legitimately race ahead of
cmd/server's own header write — it had no way to tell a real background
agent's id apart from a workflow step's own id. In practice this meant the
bridge was also creating spurious, orphaned, never-terminal transcript files
under `builder/conversation/background/` for plain step/message-sequence/
todo-task step-level executions that already have their own proper trace via
the mechanisms above, and that were never registered in
`background_agent_log` at all.

**The fix.** `emitBackgroundAgentStarted` (`cmd/server`) is the *only* writer
that creates a transcript header, and it does so synchronously at background-
agent registration — before the agent it registers can produce any provider
event. So a missing header when the bridge tries to append is not a race to
tolerate; it reliably means this `ParentExecutionIDKey` was never registered
as a background agent. `AppendBackgroundAgentTranscriptEvent` now skips
(returns `nil`, no write) instead of fabricating a transcript when no header
exists — "does a registered header already exist" is what actually
distinguishes a background agent from a step execution, not the id's shape.
A registered-but-corrupt header now returns an error instead of being
silently replaced (protects requirement 5 the same way this matters for a
missing write). The `execution_kind` metadata classification itself (used
elsewhere, e.g. UI/cost attribution) was left untouched — fixing that
pre-existing dead prefix check is a separate, broader concern outside this
ticket's scope.

**Accepted trade-off.** Every event on every workflow step/message-sequence/
todo-task execution — not just genuine background agents — now costs one
`ReadWorkspaceFile` round trip that resolves to "not a background agent, skip"
before this fix even engages. That is strictly cheaper than before (no
fabricated write on top of it), and it runs async/best-effort off the
persistence goroutine already used for every other event, so it does not add
latency to a user-facing turn. If this proves material under real load, the
efficient fix is a synchronous local signal (no network call) to identify a
registered background agent before hitting the bridge at all — not built
here.

**Verified:** `go build ./...` clean. New tests in
`pkg/orchestrator/plat164_scope_fix_test.go` pin the three cases directly
against `BaseOrchestrator.AppendBackgroundAgentTranscriptEvent` with a fake
workspace-documents API: no header → skip, no write; header exists → appends
correctly and preserves header metadata; corrupt header → returns an error
without silently overwriting it. `go test ./pkg/orchestrator/...
./cmd/server/...` — zero new failures; the ones present (in
`cmd/server`, `cmd/server/guidance`, and
`pkg/orchestrator/agents/workflow/step_based_workflow`) were individually
verified to reproduce identically on a clean pre-PLAT-164 `origin/main`
(`77d184de3`), unrelated to this ticket.
