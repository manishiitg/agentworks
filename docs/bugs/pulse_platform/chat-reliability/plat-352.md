[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-352 — Chat reliability umbrella: durable log, turn delivery, continuity

| Field | Value |
|---|---|
| Status | `umbrella: durable-log refactor complete on main (RTS verification pending, CLI-reading follow-ups 2026-09-24/25 on main); turn-delivery hardening implemented 2026-09-25, push/deploy/verify pending` |
| Priority | P0 reliability track |
| Owner | platform (chat reliability) |
| Reported | 2026-09-22 (umbrella declared 2026-09-25) |
| Related | PLAT-324 (retain conversations across reloads), PLAT-351 (retained-turn settle), PLAT-178 (retained delivery/transcript recovery), PLAT-340 (Stop/resume bindings), commit `df8254c1a` (cursor-only restore) |

## Umbrella scope

PLAT-352 is the single home for chat-reliability work: every chat turn
must execute exactly once, journal durably, and restore identically. It
consolidates the formerly separate turn-delivery ticket (PLAT-359,
folded in 2026-09-25 and deleted) and the duplicate Stop/resume record
(the chat-reliability PLAT-339 pointer was removed on 2026-09-29; the number
belongs to the security-sandbox Crew-invocation ticket). Member tickets
with their own files stay linked, not duplicated: PLAT-324
(continuity), PLAT-178 (delivery/transcript recovery), PLAT-340
(Stop/resume bindings), PLAT-351 (retained-turn settle).

AGY onboarding follows this delivery contract at the provider boundary:
the matching user step in AGY's own conversation SQLite proves that a tmux
submission was taken in; a pane echo cannot acknowledge the turn. The
assistant answer, retained progress and tool trail are read from that same
turn's SQLite rows. The final answer is the last settled assistant row;
earlier assistant rows remain progress and are not folded into the answer.
The provider records `agy_intake_source=sqlite_user_step` and
`agy_completion_source=sqlite_assistant_settled` for diagnostics. Live-input
sends also gain the two-stage receipt: tmux sends quickly and a separate
watcher confirms the exact message against a new SQLite user step, including
repeated identical sends. The provider live test and isolated full application
P0 runner passed against AGY 1.2.12 in Gemini API-key mode. See
[PLAT-354](../coding-agent-bridge/plat-354.html) for the completion guard, and
[durable acknowledgement](../../refactor/durable_ack_p0.md) for the receipt
contract.

## Turn delivery: no silent drops into dead retained runtimes (2026-09-25, implemented, push/deploy/verify pending)

Follow-up chat turns are short-circuited as retained live-input into a
coding-agent CLI runtime that can no longer execute them. The request gets
a 200 ack (`live_input_delivered`-family), but nothing runs: no agent
rebuild, no events journaled (not even the user message), no error, and —
on one rung — no log line at all. The chat looks stuck: the UI shows the
optimistic bubble forever. There is no user escape: the workflow surface
has no new-chat control (`showNewChatAction` is not passed;
`clearSession` has zero UI callers), and the product `NewChatControl`
never renders (`newConversationEnabled` is never set true).

Two live reproductions, same signature:

- **Confida, 2026-09-24 11:00–11:42 CEST.** Workflow builder session
  `ca9a753b` (`Workflow/confida-login`, Saurabh). The watchdog killed the
  pi-cli tmux on a provider wall and failed the session. Four follow-ups
  returned 200/286B in ~280ms with zero agent activity and zero new
  events; the event store confirms the user messages were never
  journaled. A provider switch to `muse-cli` saved to the manifest in the
  middle but never engaged — workflow mode ignores `req.Provider`, and no
  new turn ever started.
- **Local, 2026-09-25 08:03 IST.** Work product conversation
  `work:project:2f63209f` (news-monitor), provider `muse-cli` per the
  product registry. Two `POST /api/agent-profiles/work/query` returned
  200/165B in ~130ms; server logs show nothing after `Request received`
  (`[SHELL]` is the last line); event store ends at the 07:43 scheduled
  briefing. The 4 live tmux sessions all belong to other conversations.

Contributing mechanisms (all verified in code/logs):

1. **Fingerprint check bypass.** `workflowRetainedPolicyCompatible`
   (`agent_go/cmd/server/workflow_retained_policy.go:18`) compares the
   manifest's current provider against the last-bound one and forces a
   fresh turn on mismatch — but returns `true` immediately when the
   request carries no `Workflow/` folder (line 22). Follow-ups that omit
   it skip the check, so a manifest provider change never triggers the
   designed rebind-and-new-turn.
2. **Silent delivery rungs.** `deliverQueryAsLiveInputNow`
   (`agent_go/cmd/server/server.go:9936`) has rungs that return
   "handled" with no log line (cold retained fallback; queued/non-tmux
   accept). Healthy retained turns log `[QUERY->LIVE] … provider=…
   transport=tmux`; the dead turns log nothing, which is how they were
   distinguished.
3. **No liveness gate before retained delivery.** Neither the durable
   mcpagent rung nor the cold fallback verifies the target tmux/CLI can
   actually execute before claiming the message; a dead target yields an
   ack into the void instead of falling through to a normal new turn.
4. **Stopped-guard red herring (ruled out).** `clearSessionStopped`
   (`server.go:4019`) runs on every real user message, and the runtime
   coordinator boundary clears with it — the drop is downstream, in
   retained delivery, not in session status.

Authorized behavior: a chat is an open conversation that never
dead-ends. Every turn resolves the session's effective provider/model
(+connection for product chats) and compares it with the live runtime's
fingerprint; on mismatch the stale CLI runtime is closed and the turn
proceeds as a normal new turn in the same conversation (history is keyed
by session). New tmux, same chat. Retained delivery verifies the target
is live before claiming a message; a dead/stale target falls through to
a new turn. Every retained-delivery decision logs one line. Product
chats get a reachable new-conversation control; workflow chats get a
new-chat/clear control, at minimum when the session's runtime is dead.
Follow-ups that cannot execute surface a visible error with a recovery
action, never a silent 200. Precedent: product chats already implement
the rebind half (`bindRuntimeConfiguration` +
`closeAllCodingCLIInteractiveSessionsForOwner` + runner relaunch).

Acceptance: stale-provider follow-up starts a fresh turn in the same
session; dead-target follow-up falls through (no silent ack); every
accept path logs; re-run both reproductions above. Workarounds until
fixed: server restart clears in-memory terminal/lease state;
`POST /api/agent-profiles/{id}/conversation/new` rotates product
conversations (API only); incognito yields a fresh client session id.

## Problem

Chat restore is correct but structurally complex, and the complexity keeps
producing work (`df8254c1a` removed one raw-window fetch; the reconciliation
it fed remains). Three compounding choices drive it:

1. **Two sources of truth.** A volatile in-memory event window (transport)
   plus durable conversation JSON (record) describe the same transcript in
   different shapes, so every restore reconciles them:
   `combineTranscriptTraceEvents` merges ui_events + currentEvents +
   liveEvents with ordering rules, then `transferLiveTailInputIdentity`,
   `preserveUnconfirmedAcceptedLiveInputs`, and `monotonicConversation`
   patch up identity and ordering. Three inputs to one converter is where
   the bug class lives.
2. **Identity is positional.** The resume cursor is an index into a
   *volatile* window (`baseIndex + len - 1`), so restart/trim shifts
   meaning and dedup needs cross-source ID transfer instead of plain set
   membership.
3. **Reads multiplied per caller.** Preview, resume snapshot, paged
   history, raw window, cursor, SSE replay — each product surface
   assembled its own combination, which is why `hydrateTabEvents` has a
   happy path, a legacy fallback, and a fallback-to-history inside the
   fallback.

## Proposed design

Make the per-session event log the only truth, and make it durable on
append:

- Every event (user message, assistant chunk, tool call, completion,
  status) appends to a per-session log with a **monotonic sequence
  number** assigned at write time, flushed to disk on append (WAL-style;
  fsync can be batched per turn, not per chunk).
- **Stable IDs, not positions.** The client generates an idempotency key
  per user message; the server echoes it on the authoritative entry.
  Dedup is `seen.has(id)` — no identity transfer, no monotonic guards.
- **Render is a pure function of (log, cursor).** Fresh restore =
  `GET log[0..tip]`. Resume = `GET log[cursor..tip]` then SSE from
  `tip`. Pagination = range queries. Preview = `log[0..k]` projected.
  One endpoint shape, one converter, every surface.
- **Optimistic UI the standard way.** The client renders provisional
  entries keyed by client ID; authoritative echo replaces them. No
  "unconfirmed accepted live input" preservation pass — just key
  matching.

What this deletes: the trace combiner, the identity transfer, the
live-tail append, the resume snapshot as a separate artifact (it becomes
a cached range), the cursor-vs-window distinction, and the legacy
fallback branch — roughly half of `sessionRestore.ts` — while the
snapshot-merge machinery (`mergeChatConversationSnapshots`, overwrite
guards) shrinks to "append wins, sequence decides."

## Current durable-journal finding

The repository already contains part of this foundation: one global SQLite
journal at `structured-chat-events.sqlite`. `EventStore.AddEvent` appends
non-streaming structured events with a stable event ID and monotonic
per-session sequence, and hydrates a bounded tail after restart. This means
the remaining work is not "introduce SQLite"; it is to narrow and compact
that journal, make chat readers authoritative on it, and remove the duplicate
conversation/UI-event representations.

The current journal is global and unbounded. It receives events from direct
and product chats, Builder/Crew, scheduled and headless workflow runs,
workflow steps, message-sequence items, and sub-agents. The in-memory
1,500-event limit does not delete SQLite rows, and the current 512 MB threshold
only logs a warning.

Measurements on 2026-09-22 confirmed that this is already material:

- Local: about 270 MB database + WAL in roughly 25 hours, containing 29,139
  events across 30 sessions. Of those, 13,890 were `workflow_step` events.
- RTS: about 67 MB database + WAL in roughly one day, containing 7,742 events
  across 175 sessions.
- Large repeated snapshots, rather than row count alone, drive growth. Local
  `conversation_turn` payloads totaled 46.7 MB and reached 1.04 MB each;
  `llm_generation_end` totaled 36.6 MB and reached 1.21 MB each.
- Scheduled workflow sessions were the largest individual local consumers,
  with one session using about 52 MB.

## Agreed persistence boundary

The durable structured journal is for **interactive conversation history**,
not general execution telemetry.

Persist:

- Workflow Builder chats;
- Crew chats;
- Work/project chats;
- product chats and other direct user conversations;
- compact parent-chat summaries for child/sub-agent lifecycle transitions.

Do not persist to the chat journal:

- scheduled, queued, or manual workflow runs;
- headless/full workflow executions;
- individual workflow-step or message-sequence-item events;
- sub-agent internal streams;
- token/streaming chunks, terminal frames, repeated status lines, or system
  prompts.

Execution products keep using their existing run/execution logs. A parent chat
may retain a compact reference to an execution without copying its internal
event stream into the chat journal.

The server must register an explicit persistence class when it creates a
session, for example `interactive_chat`, `execution`, or `ephemeral`.
Persistence must not be inferred from session-ID prefixes. Only
`interactive_chat` sessions may append to the durable chat journal; unknown
sessions fail closed to non-durable delivery until classified.

Even retained chat events must be semantic and compact. Do not put a complete,
growing conversation snapshot into each `conversation_turn` or
`llm_generation_end` row. Large tool results, terminal captures, screenshots,
and files belong in separate artifacts; the event stores a bounded summary and
stable reference.

## Canonical meaningful chat events

"Meaningful" is an explicit schema decision, not a runtime guess based on an
event name. A durable chat event must satisfy all of these conditions:

1. it changes the conversation state visible to the user;
2. that state must still be visible after refresh or server restart;
3. it has a stable event ID, turn ID, and ordered sequence position; and
4. its stored payload is bounded.

The initial durable allowlist is:

| Canonical type | Durable meaning |
|---|---|
| `user_message` | A user message accepted by the server |
| `assistant_message` | The consolidated assistant text for a turn |
| `assistant_progress` | A useful consolidated progress update, not a token chunk |
| `tool_call_started` | Tool name plus bounded/sanitized arguments |
| `tool_call_completed` | Result summary plus an optional artifact reference |
| `tool_call_failed` | Bounded tool error summary |
| `input_requested` | The chat is durably waiting for user input |
| `background_agent_started` | Compact child-agent identity and purpose |
| `background_agent_completed` | Compact child-agent outcome/reference |
| `turn_completed` | Successful terminal state for a turn |
| `turn_failed` | Failed terminal state for a turn |
| `turn_cancelled` | User/system cancellation terminal state |

The following remain live-only or go to their existing execution/diagnostic
stores and must not enter durable chat history:

- individual streaming tokens/chunks;
- terminal lines, frames, and repeated status-line updates;
- heartbeats, polling events, and token counters;
- system prompts and raw provider traces;
- complete conversation snapshots;
- internal workflow-step/message-sequence events;
- repeated copies of tool arguments or results.

Providers and execution engines may continue producing their existing raw
events. A single **chat-event projector** maps them onto the canonical
allowlist or classifies them as live-only/ignored. For example, hundreds of
assistant chunks become one `assistant_message`; terminal updates remain live;
a tool start/result pair becomes two canonical tool events; workflow-step
details remain in the execution log. Frontend code must consume the canonical
contract and must not repeat provider-specific interpretation.

Every canonical row uses one versioned envelope:

```json
{
  "schema_version": 1,
  "id": "stable-event-id",
  "session_id": "chat-id",
  "turn_id": "turn-id",
  "sequence": 145,
  "type": "tool_call_completed",
  "timestamp": "2026-09-22T12:00:00Z",
  "data": {
    "tool_name": "exec",
    "summary": "Tests passed",
    "artifact_id": "artifact-42"
  }
}
```

Set a hard encoded-row limit (initial proposal: 64 KiB, with stricter limits
for individual text fields). Payloads above the limit are rejected from the
journal until the producer writes the large content to a private artifact and
emits a bounded summary/reference. The projector must never silently truncate
content without marking the event and retaining a way to inspect the complete
artifact.

Adding another durable event type requires an explicit schema change plus
tests covering projection, restart restore, ordering, deduplication, payload
bounds, and redaction. Unknown raw types fail closed to live-only delivery.

## Step and agent developer diagnostics

Removing workflow-step internals from the main chat journal must not make them
uninspectable. Their durable home remains the workflow/run execution artifacts,
including per-step conversation JSON, tool calls/results, errors, outputs, and
metrics. The normal product surface remains **Execution Logs**; the existing
child/step terminal rail remains available only when server runtime diagnostics
are explicitly enabled.

### Current developer-diagnostics mode

The existing rail is not normal product navigation. `ChatArea` mounts
`TerminalCenter` only when both conditions are true:

1. `GET /api/capabilities` reports `runtime_debug: true`, which is controlled
   by the server's explicit `AGENTWORKS_RUNTIME_DEBUG` opt-in (local launcher:
   `--enable-chat-terminal-debugs`); and
2. the active chat tab is switched from formatted conversation to Terminal /
   Live view.

When `runtime_debug` is false, the same Live-view control shows only
`MainAgentTerminal`; individual workflow-step/child terminals are not exposed.
Normal users inspect completed or running step records through Execution Logs.
This distinction must remain explicit so an internal terminal inventory is not
accidentally promoted into every product chat.

The proposed Crew-style diagnostics surface should remain capability-gated and
developer/admin-oriented initially. It must:

- scope every terminal, step, artifact, and JSON read to the active authorized
  session/workspace and never provide a cross-user "view all" mode;
- lazy-load only after the developer opens Diagnostics, so ordinary chat startup
  performs no terminal inventory or execution-log polling;
- offer an explicit **Diagnostics** entry rather than silently changing the
  normal **Open live view** behavior;
- preserve the formatted conversation as the default view and make returning to
  it immediate;
- read existing execution artifacts, terminal/SSE state, and the cost ledger
  without creating a new persistence stream;
- clearly label checkpointed JSON versus live terminal data when the two have
  not yet converged.

Add a Crew-style developer diagnostics view over those existing artifacts:

```text
┌ Agents / steps ───────┬ Selected step ────────────────────┐
│ Main agent      ●     │ Conversation | Tools | Terminal   │
│ Research step   ✓     │ Raw JSON | Costs | Artifacts      │
│ Writer step     ●     │                                   │
│ Reviewer        !     │ User instruction                  │
│   Sub-agent     ✓     │ Assistant progress                │
│                       │ Tool call and result               │
│                       │ Final response                     │
└───────────────────────┴───────────────────────────────────┘
```

The view should reuse the existing execution-log API, `ConversationViewer`,
step status/metrics, artifact loader, and terminal renderer rather than create
another persistence format. Its left rail groups the main agent, workflow
steps, Crew members, and sub-agents; the detail pane provides formatted
conversation, tool calls, terminal, raw JSON, cost/token metrics, artifacts,
errors, and per-step search.

Durable step JSON is checkpoint-oriented and can lag while an execution is
running. The diagnostics view may overlay the existing live terminal/SSE feed
for current activity, then converge on the execution JSON when the step
completes. Live transport data must not be written into the main chat journal
to support this view.

The user-facing separation is therefore:

- main chat SQLite: the user's conversation plus compact run/child summaries
  and links;
- workflow execution JSON/logs: complete step and agent diagnostics;
- terminal/SSE: live low-level inspection;
- cost ledger: authoritative usage and cost data.

PLAT-352 only needs to preserve the data contract and links required by this
diagnostics surface. Shipping or promoting the diagnostics UI beyond the
existing runtime-debug capability can land separately without blocking the
single-source chat migration.

## Retention and migration requirements

- Deleting a conversation deletes its journal rows and referenced private
  artifacts.
- Define an explicit retention/archive policy for old conversations, plus a
  total-size guard; a warning alone is insufficient.
- Checkpoint the WAL and provide deliberate compaction after bulk deletion.
- Archive or rotate the existing mixed-scope journal once during migration.
  Existing conversation JSON remains the restoration fallback while chat
  sessions are moved to the filtered journal.
- Keep the inspectable conversation JSON as a derived/export projection during
  migration, not an independent competing source of truth.

## Migration (not a flag day)

1. Add the explicit session persistence class and stop journaling execution,
   workflow-step, message-sequence, and internal sub-agent streams.
2. Implement the canonical chat-event projector and move large payloads to
   referenced artifacts.
3. Archive/rotate the existing mixed journal, then retain/checkpoint the new
   chat-only journal under an explicit size policy.
4. Move readers over one surface at a time (chat, then Work/Crew/product
   chats), each switching to range reads + stable-ID dedup.
5. Delete the converters and duplicate persisted `ui_events` last, once no
   caller passes a second source.

### Implementation progress (2026-09-22)

The first chat-only journal cutover is implemented locally; the canonical
schema and retention requirements below are not yet complete:

- every query session is classified as `interactive_chat`, `execution`, or
  `ephemeral` from typed request metadata rather than ID-prefix-only guesses;
- only `interactive_chat` sessions append to the versioned
  `structured-chat-events-v2.sqlite` journal;
- scheduled/webhook/headless workflow sessions and typed child/runtime
  sessions remain live in SSE/terminal memory but create no chat-journal rows;
- an unknown session fails closed to live-only, and a session cannot be
  promoted to durable after it has already emitted events; and
- direct, Builder, Crew, Work/product, and bot conversations retain the
  interactive class;
- a single allowlist projector keeps whole assistant transcript messages and
  semantic user/tool/turn/child-summary events while excluding token chunks,
  terminal frames, system prompts, workflow steps, and raw execution streams;
- encoded rows are bounded to 64 KiB. Oversized payloads become a marked
  summary carrying the original event ID and `conversation_json` diagnostic
  source instead of copying a megabyte-scale payload into SQLite;
- the polling endpoint supports SQLite-native tail, `after_sequence`, and
  `before_sequence` ranges, and SSE reconnect uses the same stable sequence;
- `ChatArea`, Workflow Builder, Crew, Work, Video Studio, and SparkQuill chat
  restore now render the SQLite page directly. The JSON/live-window trace
  combiner, monotonic JSON snapshot guard, preview paint, tool-argument repair,
  and JSON fallback branches were removed from the normal render path;
- read-only scheduled/workflow-run history uses a separately named execution
  diagnostics reader for its JSON artifact and cannot promote that run into
  the interactive chat journal;
- conversation deletion removes events, sequence state, ownership, and
  migration state, then checkpoints the WAL;
- durable ownership is stored beside the journal and inactive-session reads
  fail closed when the authenticated user is not the owner; and
- legacy JSON import is deliberately **not** lazy. `server
  migrate-chat-events` scans only interactive conversation files, selects the
  newest copy per session, skips schedule/bot execution sessions, imports with
  stable IDs, checkpoints SQLite, and writes a one-time marker. RTS and shared
  rootless deployment scripts run it while the old agent is stopped; the local
  logging launcher runs it before starting the server. Therefore a user's
  first refresh never pays for parsing a multi-megabyte legacy transcript.

Conversation JSON remains a derived diagnostic/export artifact for agent
review, raw execution/tool inspection, and cost analysis. It no longer repairs,
overwrites, or races the rendered chat. The original mixed journal remains on
disk under its old filename for rollback/inspection and is not opened by the
new server.

### Correctness follow-up (2026-09-23)

- Read-only execution tabs now use their JSON diagnostics reader for restore
  and pagination, and raw SSE/polling for live activity. Interactive chats
  alone request `durable_chat=1`.
- Durable SSE backfill reads every forward page before live delivery. Live-only
  frames remain visible while running but carry no durable cursor; reconnect
  replays only SQLite rows. Forward polling from sequence zero starts at the
  first row rather than the newest page.
- `live_input_confirmed` receipts are retained with interactive chat so a
  delivery confirmation can survive refresh.
- Fresh legacy imports anchor bounded UI tool/approval traces between matching
  provider-history messages instead of appending every trace event after the
  final answer. Historical files without a matching carrier have no exact
  ordering evidence; the original JSON remains available for inspection.
- The migration marker is one-time. An already-imported v2 database is **not**
  silently rewritten by this importer change. Repair of any such database
  needs an explicit, separately verified migration before deployment.

### Review fixes (2026-09-23)

A full review of the cutover found deploy, restore and performance defects.
All of the following are on `main`:

Read path (`9346aeab2`):
- An empty forward poll returns the requested cursor clamped to the journal
  tip instead of `0`. With `since=0` meaning "from the first row", the old
  value made every quiet poll re-walk the whole history.
- The client never moves a durable cursor backwards and only treats `has_more`
  as "older rows exist" on backward reads.
- Every conversation-deleting path (single delete even when the JSON is already
  gone, bulk cleanup, agent-profile, project and workflow-folder deletes)
  removes the session's journal rows.
- Durable reads reuse the history list's access rule (`durableChatReadAllowed`)
  for legacy `default` owners and workflow bot chats. On multi-user servers
  `default` chats stay hidden, as the list already does.

Journal (`9346aeab2`):
- The SQLite append runs under a per-session lock instead of the global
  event-store lock, so one chat's fsync no longer blocks every session.
- A journal-known session is adopted as `interactive_chat` after a restart even
  when a live event arrives before the first query or restore poll.
- An already-classified interactive chat accepts later automated turns instead
  of returning 409; execution to interactive promotion is still refused.
- Answered or expired blocking feedback emits a durable
  `human_feedback_resolved` marker (request id); the pending list and the inline
  card honour it, so a refresh no longer resurrects an answered approval.

Migration and deploy (`9346aeab2`):
- The legacy import decodes each message once (identical output, ~0.01s on a
  6k x 6k session), reads files one at a time, and skips unreadable or
  malformed files instead of aborting.
- The import also runs at server startup before the listener, so every launch
  path (Dominion, dedicated VM, Azure, desktop, local) is covered.
- When `AGENTWORKS_STATE_ROOT` is newly pinned, state under the old default
  root (access tokens, native-resume recovery, CLI runtimes) is copied over
  once without overwriting. Before this, every `aw_pat_` token died on deploy.
- Deploy scripts restart the agent when a migration step fails, and rootless
  runs the chat import after the Builder-chat move.

Follow-ups found while testing locally and on RTS:
- CORS allows `X-Client-Submitted-At` (`9346aeab2`). Local dev sends
  cross-origin, and the rejected preflight turned every chat send into a bare
  "Network Error".
- The continuity notice no longer states a message count (`397c1ad77`). After a
  restart the in-memory history holds only the current turn, so it claimed
  "1 earlier message". The banner now reads "Previous conversation loaded".
- Durable SSE never starts below the newest sequence the tab already holds
  (`0c737fdae`). The submit flow reconnected with a reset cursor of 0, which
  replayed the whole journal before every reply.
- A never-used chat whose durable read returns 404 restores as an empty
  conversation instead of an error that kept "Loading conversation..." up
  until a give-up timer.
- Composer keystrokes no longer re-render the whole transcript:
  `loadOlderConversationPage` depended on the entire tab object (replaced on
  every keystroke) and is a prop of the memoized transcript. The approval card's
  resolution lookup now recomputes only when events change.

RTS deploy verification (release `5cef1d8`, 2026-09-23): 61 interactive chats
imported, 0 failed (72 schedule/bot sessions skipped by design); 9,832 files
carried from the legacy state root, including the access-token store; all
services active with no restarts. The deploy reported a failed prune only
because the first boot took ~55s while the prune health check gave up early.

Decisions:
- The versioned canonical envelope and type renames (`schema_version`,
  `tool_call_started`, ...) are **dropped**. The existing wire types already
  work end to end through the allowlist projector; renaming them would churn
  every consumer for no behavioural gain.
- The historical batch-4 note that `workflow_error` has no emitter was wrong:
  `server.go` emits it and it stays live.

### Final phase (2026-09-23)

Message IDs and ordering (`eb899b9e4`):
- The submission id (`Idempotency-Key`) is the `client_message_id`. Keyed user
  rows use event id `user:<id>` (retries dedupe on the journal key) and carry
  it in metadata, as does `live_input_confirmed`. The browser's provisional
  bubble shares the id and is replaced by its durable echo in arrival order;
  content-based identity transfer is deleted.
- A message steered into a busy CLI is journalled when the CLI confirms it
  took the message (durable ack), not when it was sent, so the in-flight
  answer no longer renders under the new question.

Large messages and retention (this change):
- Rows whose encoded size exceeds 64 KiB are written in full to a private
  artifact (`<state>/chat-artifacts/<hash(session)>/<hash(event)>.json`, 0700
  directories, 0600 files, atomic rename) and the journal keeps a bounded
  summary with `truncated`, `artifact_id` and `original_size_bytes`. This
  replaces the old silent 48 KiB cut. Live appends and the legacy import share
  the path (it lives in `SQLiteEventJournal.Append`).
- `GET /api/sessions/{id}/chat-artifacts/{artifact_id}` returns the full
  event under exactly the durable-read authorization (one shared helper,
  `authorizeDurableChatRead`, now also used by event pages). Artifact ids are
  hashes validated as 32 hex characters, so no caller text reaches a path.
  The transcript shows "Show full response" / "Load full output" on
  summarized rows.
- Deleting a chat removes its artifact directory with its rows.
- Retention keeps every conversation. A maintenance pass (two minutes after
  start, then daily) moves rows older than `CHAT_JOURNAL_COMPACT_AFTER_DAYS`
  (30) and larger than `CHAT_JOURNAL_COMPACT_MIN_BYTES` (8 KiB) into artifacts
  in place (same event id and sequence; never `user_message`; idempotent),
  under each session's append lock. Above `CHAT_JOURNAL_MAX_BYTES` (2 GB) it
  logs an alert and steps the age threshold down (14 d, 7 d, 1 d, 0), oldest
  rows first, until under the cap. It then checkpoints the WAL and runs an
  incremental vacuum. New journals are created with
  `auto_vacuum=INCREMENTAL`; an existing journal reports
  `needs_one_time_vacuum=true` in the maintenance log line instead of
  blocking startup.

Risks from the message-ID work:
- Fast-answer race: resolved. While a deferred steer awaits its ack, answer
  rows pass until the in-flight answer's `unified_completion`; later main-agent
  answer rows (transcript messages, tool calls, completion) are held and
  released right after the steered user row (20 s safety timeout). Covered by
  a journal-level test where the answer is written before the ack.
- Reload / other tabs: a keyed message still waiting in the durable turn
  queue is returned as `pending_messages` on the restore read and shown as a
  queued provisional bubble until its durable row replaces it. Accepted: a
  message already handed to a busy CLI but not yet taken appears in other tabs
  only once the CLI takes it (a second or less after the in-flight answer).

Dropped from scope: the versioned canonical envelope and type renames.

Remaining: none beyond verifying this final phase on RTS.

Small follow-ups (done, 2026-09-23):
- `orchestrator_agent_error` is visible again in the diagnostics rail; it is
  the failure that explains why a child execution stopped.
- SparkQuill history pages backwards through the whole journal
  (`fetchCompleteSessionEvents`, 500 rows per page, 20-page cap) instead of
  showing only the newest 300 events.
- The workflow-orchestrator prompts (`RequestHumanFeedback`,
  `RequestYesNoFeedback`, `RequestMultipleChoiceFeedback`) also emit
  `human_feedback_resolved`. `request_human_feedback` and `plan_approval` have
  no producer (`EmitPlanApproval` has no callers), so they need no marker and
  are deletion candidates.
- Per-session append locks are reference counted and pruned once idle.
- `prune-releases.py` waits up to 180s for health; a failed prune on a healthy
  release is a warning, an unhealthy agent still fails the deploy.

### Related infra (2026-09-23)

The slow conversation loading investigated under this ticket also had
infrastructure causes, fixed on RTS the same day:
- Caddy had been hand-switched to HTTP/1.1 on 2026-09-22, so each open chat's
  SSE stream consumed one of the browser's six per-origin connections and
  other requests queued. HTTP/2 is re-enabled and pinned in
  `deploy/aws-ec2/server/Caddyfile`.
- The box used `cubic`; to lossy long-haul clients (India to us-west-2, ~44%
  retransmits) that collapsed throughput. BBR is enabled
  (`/etc/sysctl.d/90-bbr.conf`, also in the Caddy container namespace).
- `video.realtrainingsys.com` is now served through CloudFront
  (distribution `E1OYOJGT2ZANUB`; `/assets/*` cached at the edge, everything
  else passed through). Every `./deploy.sh rts` reports its usage against the
  free tier.

## Caveats

- It grew this way for real reasons: the event store predates durable
  history, tmux CLIs emit unstructured output that must be
  *interpreted* into events, and each product needed restore before a
  shared primitive existed. A log-first rewrite does not remove the
  provider-interpretation layer — it stops duplicating its output in
  two shapes.
- Bounding still matters: 1.3 MB sessions mean range queries must stay
  paged and fat frames must stay out of the hot path (the existing
  `include_ui_events` split already proves this). Terminal snapshots
  stay opaque blobs attached to the log, not log entries.
- Open question for design review: per-turn fsync batching vs
  per-append durability for the crash-during-turn case, and whether the
  log file replaces the conversation JSON on disk or sits beside it
  during migration.

## Verification

Design acceptance requires an approved compact schema + endpoint shape (`GET
log` ranges, sequence/ID semantics), explicit session persistence classes,
retention/deletion behavior, and an archive/rotation plan for the existing
mixed journal. Implementation verification must additionally prove that
workflow-step and scheduled-run events do not create journal rows, interactive
chat restore survives restart, large payloads are referenced rather than
copied, and conversation deletion removes its durable rows.

## Follow-up 2026-09-24/25 — the same rule for reading coding-CLI output

The chat is now one ordered log, and turns run one at a time (PLAT-178
FIFO). Several fixes in these two days apply the same rule, "no second
source, nothing to reconcile", to how each turn's output is read from the
coding CLIs. Each came from a real failure on RTS or local.

| Area | Failure | Fix | Commit | Verified |
|---|---|---|---|---|
| Cursor turn recording | An auto-notification turn saved every assistant message since the session started as its reply (RTS, automationtesting). Two per-session "already recorded" bookmarks drifted: live-typed turns advanced one, normal turns read the other. | Each turn records only what Cursor committed after the store snapshot taken before its prompt. The per-session recording bookmark is removed; the live display keeps its own display-only reader. | multi-llm `6262fe0` (stop-gap `f360a47` removed) | Live: MultiTurnNoHistoryLeakage, LiveInputProcessesQueuedFollowup, CrossRestartResume, TranscriptStreamNoHistoryReplay |
| Claude backgrounded tool calls | A tool call over 120 s was moved to the background; Claude ended its turn, then continued on its own when the task finished, and none of that reached the chat (RTS, SDE crew). | The turn stays open while any backgrounded task has no `<task-notification>` yet, the same as pending background agents. | multi-llm `79b7c32` | Unit (this incident's transcript); not live |
| Claude live input lost | Pasted text vanished, leaving two blank lines, and the message was lost (RTS 13:20). | If the submit check cannot confirm and Claude's transcript has neither a user row nor a queue row within 6 s, the composer is emptied and the message sent once more. Blank lines are cleared before every paste. | multi-llm `89a7c49` | Live: RealDurableAckContract, LiveInputProcessesQueuedFollowup |
| Claude Escape presses | Two Escapes at a ready prompt opened Claude's Rewind list; its `❯ (current)` entry was taken for a stale draft, and two sends failed for a minute each (local 07:49). Likely trigger: the startup popup check misread the redrawn conversation of a resumed session. | Escape is sent only to interrupt (Stop) and to close a Rewind list. Startup popups are prevented at launch (`--no-chrome`, classic renderer, pre-seeded trust, onboarding and theme), never dismissed; an unexpected one is reported with the screen. The pre-send question-menu Escape is gone (`AskUserQuestion` is not in `--tools`). | multi-llm `bc50ef2`, `89880ae` | Live on Claude Code 2.1.282: RealDurableAckContract, NativeResume, LiveInputProcessesQueuedFollowup, RealCrossRestartResume |
| Failed send in the UI | A lost message showed only a faint red "!". | The row says "Not delivered · Resend"; Resend uses the chat's normal send path. | builder `838d0cdfa` | Unit |
| Blocked native tool call | Muse's policy-blocked `read_file` showed a green check and read as a policy leak (QA #218). | A "tool blocked by hook" result renders as an error. | builder `d2d5e6277` | Unit |

Still open:
- Codex still presses keys to accept its hook-trust dialog. It is tied to
  that dialog's exact wording, so it is lower risk, but it should also be
  prevented by pre-trusting hooks in Codex's config.
- Claude Code auto-updated locally from the certified 2.1.278 to 2.1.282
  without a P0 run. CLI versions on servers should either be pinned or
  trigger P0 when they change (owner decision pending).
- The P0 fixtures use short synthetic conversations. Add a resume test
  whose conversation contains a numbered list and "no thanks", and a test
  that sends a message after a Rewind list was opened.
- Control keys sent to a CLI (Escape, Enter, Ctrl-C) are not logged with a
  reason, so the local 07:49 trigger could only be inferred.

## Appendix: event-type audit (2026-09-22)

Per-type producer/consumer file lists live in
[docs/core/event-catalog.md](../../../core/event-catalog.md) (133 types
after the deletion batch below added `comprehensive_cache_event` and
tombstoned 14 REMOVED, same method). Summary findings:

All cataloged types were checked for backend producers (mcpagent,
agent_go, provider repo) and frontend consumers (outside tests/generated).
Method: const-name references plus raw wire-string grep; stale binaries
excluded. A dispatcher branch alone does not count as live — emission was
verified per type. The catalog grew during review from 118 to **~130**:
branching on `event.type` surfaced `workflow_step_started/completed`,
`workflow_end/error`, `human_input`, `take_control`, `viewer_control/error`,
`work_workflow_references_updated`, `fix_applied`, and `context_canceled`
(all with backend emitters). Result: **8 dead consts, 11 dead
handlers/renderers, 13 produced-then-dropped, 12 telemetry demotions, 15
filtered by design; the rest remain genuinely live, of which ~12 families
become durable canonical.** (`context_editing_started` appeared in an
early draft of this audit by assumed symmetry and exists nowhere — not a
type at all.) Three more frontend-only strings (`decision_request_missing`,
`work_identity_updated`, `learning_completed/failed`) are not yet
classified (client-synthesized vs dead handling).

Delete outright — defined, never produced or consumed anywhere:

- `step_execution_start`, `step_execution_end`, `step_execution_failed`
- `decision_evaluated`, `prerequisite_navigation`
- `agent_processing`, `large_tool_output_server_unavailable` (sole
  reference is the schema-gen registry, not an emitter)

Delete the consts, generated TS types, and referencing tests.

Dead frontend handling — rendered or retained, nothing emits them:

- `live_execution_streaming` (full `EventDispatcher` renderer, dead)
- `cache_event` (renderer, dead)
- `phase_started`, `phase_completed` (retention-list entries only, dead)
- `work_identity_updated` (checked in `WorkSurface`, zero emitters in any
  repo; its sibling `work_workflow_references_updated` is live)

Not dead but filtered — `NON_TRANSCRIPT_TYPES` in
`shared/session/transcript/terminalEventTranscript.ts` drops these from
the displayed transcript by design (delivery-assembled, diagnostics, or
product side-channels read by their own surfaces):

- `token_usage`, `status_line`, `system_prompt`
- `conversation_start/end/turn`, `llm_generation_start/with_retry`
- `streaming_start/chunk/end`, `product_interaction`
- `work_identity_updated`, `work_workflow_references_updated`

The canonical allowlist should align with this: if the transcript never
shows a type, the journal has no reason to keep it for chat restore.

Dead renderers — dispatcher branch exists, zero emitters (verified, not
just unreferenced):

- `mcp_server_connection`, `mcp_server_discovery`,
  `mcp_server_connection_error` (only the `_start` variant is emitted,
  which itself has no renderer)
- `model_change` (`NewModelChangeEvent` is never called)

Never displayed — emitted but hidden or nulled before the screen:

- `system_prompt` (in `HIDDEN_EVENTS`, never displayed anywhere)
- `mcp_server_selection` (dispatcher explicitly returns null: "internal
  per-turn routing decision, never rendered")

Produced then thrown away — `SKIP_EVENTS` in `base_bridge.go` already drops
these ("no UI component, pure waste"), but the backend still constructs and
emits them; direct `AddEvent` paths can also bypass the bridge into the
journal. Delete the emit calls at source, don't extend the skip list:

- `tool_execution`, `tool_output`, `tool_response` (9 producing files),
  `tool_call_progress`
- 8 cache events (`cache_write` emitted from 17 files across the agent
  core; `cache_hit/miss/expired/cleanup/error/operation_start`,
  `comprehensive_cache`)

Naming bugs (two): the backend emits `comprehensive_cache`, the bridge
skips `comprehensive_cache_event`, and the frontend renders
`comprehensive_cache_event` — three spellings, none matching. The emitted
event is invisible and unskipped: it rides the bus and the journal for
nobody. Delete or fix the spelling plus the dead renderer. Separately,
both `context_canceled` (1 L) and `context_cancelled` (2 L) are emitted;
consumers must not assume one spelling — canonicalize on migration.

Key migration constraint — canvas derives execution state from chat
events: `WorkflowLayout` scans tab events to rebuild batch context, step
statuses, and current step on every hydrate; `useRunningWorkflowsStore`
tracks running work from orchestrator/agent boundaries; `cleanConversation`
builds activity summaries from routing/todo/batch events. The step family
therefore cannot be deleted or unjournaled until the canvas reads
execution logs instead. That reorder — canvas first, journal narrowing
second — is the hard part of migration step 4, not the converter
deletion.

Write-only telemetry — emitted, never read; demote to logs/metrics, not bus
events (consistent with the persistence boundary above):

- `json_validation_start/end`, `llm_messages`, `llm_token_usage`,
  `error_detail`, `performance`
- `mcp_server_connection_start/end`
- `streaming_connection_lost/error/progress`
- `large_tool_output_file_write_error` (emitted from the parallel-tool
  and conversation paths, never read)

Keep as live-only — genuinely consumed by Execution Logs and live views,
but must never enter the chat journal (~40 types: streaming, progress,
workflow-step internals, orchestrator boundaries, MCP status, etc.).

Open allowlist items from the restore inventory (not yet resolved):

- `input_requested` needs kind + payload schema + requested→resolved
  pairing, or refresh resurrects answered approvals as pending.
- Context-lifecycle markers (summarization, max-turns/limits) need an
  explicit home (`assistant_progress` kind vs new marker type).
- In-chat step collapse/expand and canvas restore depend on
  orchestrator/step boundary events; moving them to Execution Logs is a
  UI + canvas-data change to acknowledge (see migration constraint
  above), not just a storage move.
- `background_agent_started` schema must carry what delegation cards
  render (id, instruction, depth, model, servers, template).

Observed-UI mapping (2026-09-22) — what the current transcript actually
shows, via `TerminalEventTranscript` (payload-based components first,
`EventDispatcher` as fallback):

- User bubbles + `AssistantTranscriptMessage` (payload fields, not event
  types) for all assistant carriers.
- Minimized tools: `ToolCallCard` behind a disclosure, built by
  `pairToolCalls` from exactly `tool_call_start/end/error`, paired by
  `tool_call_id`. The canonical schema must preserve that pairing.
- Turn completion/failure cards, delegation/bg-agent cards, input
  requests when pending, `large_tool_output_*` artifact rows.

That is ~8 visible families — fewer than the 12 canonical types, so the
allowlist covers the screen with room to spare. Conversely,
`tool_call`/`tool_result`/`tool_execution`/`tool_output` reach no
renderer even in the new transcript and join the delete/demote list, not
the canonical one. Token/context widgets have reachable renderers but no
events arrive on retained-CLI tabs (CLIs report no per-turn usage), which
confirms keeping them out of the journal as ledger/diagnostics data.

## Appendix: deletion batch 1 (2026-09-22, uncommitted)

Rule applied: every event must serve a frontend or backend purpose;
emission verified per type (a dispatcher branch alone does not prove
live). Deleted, all with zero remaining references in either repo:

- Backend consts + structs + ctors (`mcpagent/events/`):
  `mcp_server_discovery`, `mcp_server_connection_error`,
  `comprehensive_cache`, `step_execution_start/end/failed`,
  `prerequisite_navigation`, `agent_processing`, `model_change`,
  `large_tool_output_server_unavailable`, `decision_evaluated`.
- Langfuse/LangSmith handler cases + span functions for
  `mcp_server_discovery` / `mcp_server_connection_error`, plus the
  `docs/tracing.md` mention.
- schema-gen registry/union entries + regenerated
  `agent_go/schemas/*.schema.json` and frontend `src/generated/*`
  (`cache_event` payload key dropped; unused `mcpcache` import removed).
- Frontend renderers + dispatcher branches: `MCPServerDiscoveryEvent`,
  `MCPServerConnectionEvent` (all three branches incl. the bare
  `mcp_server_connection`, which is never on the wire — the struct
  re-types to `_start` before emit), `SystemPromptEvent`,
  `ModelChangeEvent`, `CacheEvent`, `ComprehensiveCacheEvent`,
  `LiveExecutionStreamingEventCard` (+ `formatLiveStreamingPreview`),
  and the dead union literals in `generated/event-types.ts`.
- Retention-allowlist entries: `tool_output`, `phase_started`,
  `phase_completed`.
- `cache_event` added to `NON_TRANSCRIPT_TYPES` (defense-in-depth
  beside the bridge skip; covered by a new
  `terminalEventTranscript.test.ts` case).

Corrections to the audit above, found while verifying emission:

- `cache_event` is NOT dead: it is emitted on the wire from
  `agent/parallel_tool_execution.go` and `agent/conversation.go`
  (3 sites; every `CacheEvent` constructor returns wire type
  `cache_event`). It is bridge-skipped (`SKIP_EVENTS` +
  `NEVER_SHOW_EVENTS`), so still correctly out of the journal.
  The `cache_hit/miss/write/expired/cleanup/error/operation_start`
  wire strings, conversely, are never produced — the "17 producing
  files" were handler/code references, not emitters.
- There is no three-way `comprehensive_cache` spelling split: the
  backend never emitted bare `comprehensive_cache` (dead const,
  deleted). `comprehensive_cache_event` is a `*mcpcache.ComprehensiveCacheEvent`
  sent to observability tracers only — the streaming tracer forwards
  solely `*events.AgentEvent`, so it never reaches polling/SSE.
  The `context_canceled`/`context_cancelled` split is real and stands.
- `tool_output` is never produced either (constructor has zero
  callers); its bridge skip entry is moot. Const/struct/schema kept
  as a follow-up deletion candidate, not part of this batch.
- `system_prompt` hiding works via backend `HIDDEN_EVENTS` + frontend
  `NON_TRANSCRIPT_TYPES`, not via the frontend `HIDDEN_EVENTS` set
  (which only feeds counts in `useChatStore`, no render path).

Verification: `go build ./...` clean in both `mcpagent` and `agent_go`
(the one `libonnxruntime` link warning pre-exists), `go vet` +
`go test ./events/` pass, `npm run types:events` + `npx tsc -b` clean,
vitest 26/26 in `components/events` + `shared/session/transcript` and
95/95 in `terminalEventTranscript.test.ts`. Not committed or pushed —
awaiting explicit instruction.

## Appendix: deletion batch 2 (2026-09-22, uncommitted)

Same rule (every event serves a frontend or backend purpose; emission
verified per type). Prompted by "why do we need/keep/have these" review
of `step_progress_updated`, the cache consts, the MCP connection
structs, `todo_steps_extracted`, `batch_group_start`, and
`stepStatusMap`. Result: 13 more REMOVED (27 total in the catalog).

Deleted as never-produced (no emitters anywhere, dead handling only):

- The 7 per-operation cache wire strings (`cache_hit/miss/write/
  expired/cleanup/error/operation_start`): consts, distinct structs,
  5 uncalled ctors, unreachable Langfuse handlers. Kept: unified
  `CacheEvent` + `GenericCache` + the 2 called ctors (live
  `cache_event` tracer traffic, bridge-skipped).
- The 4 batch wrappers (`batch_execution_start/end`,
  `batch_group_start/end`): consts (both repos), structs + ctors,
  schema-gen entries, `planning_exports.go` reader + notify func (+
  its test), tree labels, HIDDEN entries, 4 dispatcher branches,
  renderers, `extractWorkflowInfo`, WorkflowLayout batch restore,
  `useWorkflowStore` batch slice, `BatchProgressHeader`, retention +
  SUMMARY entries.
- `todo_steps_extracted`: sole emit path uncalled; emit funcs + 7
  exclusive helpers + struct + consts + schema + renderer +
  activity-tree label.

Deleted as produced-but-unread:

- `step_progress_updated` (5 emit sites, zero readers; the "required
  for canvas" comment was stale — ChatArea had removed processing and
  WorkflowLayout never read it). Emit wrappers kept as
  webhook-persistence hooks for their 13 call sites.

Deleted as downstream-only machinery with no source left:

- `stepStatusMap` / `currentStepId` store slice + setters, canvas
  node-coloring (subscription, stabilization, sync effect,
  `usePlanToFlow` status branches), WorkflowLayout step scan.

Kept, with the leak closed:

- `mcp_server_connection_start/end`: genuinely emitted per connect
  and consumed by Langfuse/LangSmith span handlers (backend purpose).
  Added to bridge `SKIP_EVENTS`, store `NEVER_SHOW_EVENTS`, and
  frontend `NON_TRANSCRIPT_TYPES` (+ test) so they stop reaching the
  transcript as "Unknown Event Type" cards. Tracers get them before
  the bridge, so spans are unaffected.
- `batch_execution_canceled`: the only live batch event (context-cancel
  path); survives with renderer + retention + STRUCTURAL intact. Found
  only because the build broke when the batch deletion briefly removed
  its struct — the batch sweep initially missed the canceled ctor.

Verification: `go build ./...` clean in both repos, `npm run
types:events` + `npx tsc -b` clean (hand-fixed `event-types.ts` union
again for the removed types), vitest 144/144 across transcript +
events + history-log suites and 96/96 in
`terminalEventTranscript.test.ts` (2 new filter cases). Not committed
or pushed — awaiting explicit instruction.

## Appendix: deletion batch 3 (2026-09-22, uncommitted)

Same rule (every event serves a frontend or backend purpose; emission
verified per type). Prompted by "where are these used / is this used
anywhere" review of the Learning, Delegation, Context & limits, and
context-editing families. Result: 10 more REMOVED (37 total in the
catalog: 133 entries, 96 kept).

Deleted as never-produced:

- `learning_completed` / `learning_failed`: legacy eval-subsystem
  leftovers (eval retired 2026-09-19); no constructor ever existed,
  zero producers. `learning_skipped`: const + struct + `GetEventType`
  removed; struct never constructed. All three schema-gen entries
  removed. NOTE: `StepContent.tsx` badge matches on these strings are
  a separate *file-log* namespace (`learning-execution.json` run
  files via `/workflow/logs`) and are kept for historical run data.
- `orchestrator_start` / `orchestrator_error`: never emitted (only
  `orchestrator_end` ever was). Removed consts + structs, schema-gen
  entries, both renderers + dispatcher branches, `runningWorkflows.ts`
  ERROR/IMPORTANT entries (`ERROR` is now `['workflow_error']`),
  `useChatStore` important/retain matches, union/map entries.
- `throttling_detected` / `token_limit_exceeded`: schema-only — at
  HEAD the only backend references were schema-gen itself. Removed
  schema entries, renderers, dispatcher branches, exports, union/map
  entries. These were exposed by regen: stale generated types had
  masked the dead components until `types:events` re-ran; `tsc -b`
  now passes.
- `context_canceled` (single L): never a real wire event — a
  misspelling in two backend match arms that could never match the
  actual `context_cancelled` wire string. Both arms fixed.

Deleted as a whole dead feature, not just events:

- Context editing end-to-end: `mcpagent/agent/context_editing.go`,
  `context_editing_routes.go` (`/compact` route + handler),
  `agent_tuning.go` flags/thresholds, `ContextEditing*`
  consts/structs/constructors, `enable_context_editing` request +
  preset fields, `ContextEditingCompleted/ErrorEvent.tsx`, dispatcher
  branches, `agentApi.compactContext` + request/response types,
  `handleCompact` (dead: no `/compact` command or button ever called
  it), `chatSubmitHelpers` preset override. `context_editing_completed`
  / `context_editing_error` tombstoned.

Kept deliberately (producer + consumer verified):

- `learn_code_script_execution`: emitted at 4 call sites
  (`controller_execution.go` via `emitScriptedExecutionEvent`),
  consumed by `useChatStore` (purge/dedup), `ChatArea`,
  `EventDispatcher`, `eventModeUtils`; STRUCTURAL retention. The
  `learn_*` name is legacy; the signal is live scripted-mode
  execution. Not the same family as the deleted `learning_*` events.
- `orchestrator_end`, `variables_extracted`, `workflow_error`: all
  have live emitters and consumers (verified before keeping).

Contract bugs fixed along the way:

- `session_execution_tree_test.go` asserted terminal status for the
  deleted `batch_execution_end`/`batch_group_end` and used the
  misspelled `context_canceled`; updated to the live contract
  (`context_cancelled`, deleted rows removed with a tombstone
  comment). Production code already had the correct spelling.
- Stale comments reworded (`controller.go`, `controller_batch_execution.go`
  still referenced `step_progress_updated` for live batch-context fields).

Verification: `go build ./...` + `go vet` clean (builder + mcpagent),
`go test` clean for `pkg/orchestrator/events`, `internal/events`,
mcpagent `events`, and the fixed `session_execution_tree` test;
`npm run types:events` + `npx tsc -b` clean; vitest 203/203 across the
touched suites (transcript, commands, history-logs, execution-logs,
stores, workflow utils). Full `cmd/server` suite has 3 failures also
present without these changes (tmux CLI-lifecycle timing fails
identically against pristine HEAD mcpagent; slack-allocator and tool-
topology pass in isolation — flaky under full runs). Not committed or
pushed — awaiting explicit instruction.

## Appendix: batch-3 follow-up — `orchestrator_agent_start` card removed (2026-09-22, uncommitted)

Prompted by "which card is this / I never see it / it should not be
visible anywhere". Verified: product chats never painted it
(`isProductMainConversationEvent` excludes child-execution starts);
the only render path was the diagnostics rail. Removed the card from
code: dispatcher branch now returns null (explicit, so it cannot fall
through as an "Unknown Event Type" JSON card) and
`OrchestratorAgentStartEventDisplay.tsx` + both index exports deleted.
The wire event stays live for non-rendering consumers (store
retention/heartbeat/restore, STRUCTURAL window, bot text narration,
planning match); catalog reclassified LIVE -> RETENTION-ONLY. End/error
cards untouched (end carries the result). No test changes needed:
`tsc -b` clean, transcript suites 112/112. Not committed or pushed —
awaiting explicit instruction.

## Appendix: minimal diagnostics rail (2026-09-22, uncommitted)

Prompted by "the dev rail should mainly have user/assistant and tools
plus very important things" + "product chats are perfect [leave them]".
`selectTerminalEvents` (terminal path only — product path untouched)
gained `TERMINAL_RAIL_HIDDEN_TYPES`, a fail-open denylist hiding
lifecycle/status banners from the rail: 8 sub-agent types,
`orchestrator_end`, todo/workflow status, summarization trio, usage/
routing/variables, retry/broken-pipe/max-turns, `mcp_server_selection`,
`synthetic_turn_ready`, `auto_notification_steered`,
`conversation_resumed`, `learn_code_script_execution`. Rail keeps:
user/assistant/tool rows, human gates, errors, completions, and
content-bearing results (`pre_validation_completed`,
`batch_execution_canceled`, thinking). No dispatcher changes in this
step (the `orchestrator_agent_start` null + component deletion from the
prior follow-up stands alone); no backend changes — all hidden types
are still emitted, stored, and retained.
Tests: 4 rail-visibility cases in `terminalEventTranscript.test.ts`
rewrote old "keeps lifecycle card" assertions to the mandated
hide-behavior (explicit contract change per above); ordering test kept
its assertion with a rail-visible fixture swap. `tsc -b` clean, 96/96
transcript, full frontend suite 1786/1788 (sole failure is the
pre-existing `formsKitAdoption` settings-kit case, untouched files).
Not committed or pushed — awaiting explicit instruction.

## Appendix: post-deletion producer/consumer audit (2026-09-22)

Review after the 37-event deletion found another class of false
positives in the earlier catalog: a type declaration, schema-gen entry,
renderer, store match arm, or tracer handler is not proof of a producer.
For this pass, an event counts as produced only when live code constructs
the payload (or an equivalent generic envelope) and sends it through an
event listener/bridge. This stricter check found the candidates below.

### Candidate deletion batch 4: no current producer

Core/schema-only contracts (14):

- `error_detail`
- `json_validation_start`
- `json_validation_end`
- `llm_messages`
- `llm_token_usage`
- `performance`
- `streaming_error`
- `streaming_progress`
- `streaming_connection_lost`
- `tool_output`
- `tool_response`
- `tool_call_progress`
- `debug`
- bare `mcp_server_connection` (the carrier struct is live for the
  separately typed `mcp_server_connection_start/end` tracer events, but
  the bare discriminator is never emitted)

Orchestrator contracts with downstream handling but no emitter (4):

- `independent_steps_selected`
- `human_verification_response`
- `synthetic_turn_ready`
- `auto_notification_steered`

Dead workflow event family (4):

- `workflow_start`
- `workflow_progress`
- `workflow_end`
- `workflow_error`

The workflow family has no Go emitter. Frontend renderers, generated
union/map entries, retention/status arrays, `cleanConversation` cases,
and workflow-store completion/error branches are downstream-only. The
live completion contract is `orchestrator_end`/`unified_completion`;
live errors use the agent/conversation/orchestrator-agent error paths.
This corrects the batch-3 note above that classified `workflow_error`
as having a verified live emitter.

One additional terminal discriminator is downstream-only:

- `background_agent_failed`: current background failures are reported
  through `background_agent_completed` with `status: "failed"`; no code
  emits a separate `background_agent_failed` event.

That makes **23 candidate event contracts** for the next deletion batch.
Before deletion, add every removed wire name to the legacy tombstone
filter described below, then remove its const/struct/constructor,
schema-gen entry, generated type, renderer, store match arms, retention
entry, and tests as applicable.

Two more frontend branches are dead event interpretations, but the
strings themselves are live in a different namespace:

- `workflow_step_started`
- `workflow_step_completed`

They are `BotNotificationKind` values, not structured chat events.
Remove the `cleanConversation` event branches without deleting the bot
notification constants.

### Active events that should not be durable chat rows

These have real producers, so do not delete them blindly. They should be
demoted from the chat journal while preserving their actual consumer:

- `system_prompt`: emitted with the complete effective prompt, stored in
  SQLite, and hidden from every transcript. Keep it in the conversation
  debug artifact and observability trace; bridge-skip it from chat
  persistence. This is potentially a large row for every Crew/sub-agent.
- `mcp_server_selection`: emitted for the initial query, consumed by
  Langfuse as routing telemetry, and explicitly rendered as `null` in the
  frontend. Add it to bridge `SKIP_EVENTS` and store
  `NEVER_SHOW_EVENTS`; tracer delivery happens before the UI bridge and
  remains intact.
- `status_line`: live and required to update terminal model/token/cost
  metadata, but potentially emitted many times and excluded from readable
  transcripts. Preserve delivery to `terminalStore.HandleEvent`, then
  avoid the durable SQLite append. This needs a small transient-event
  path rather than simple deletion or bridge skipping because the
  terminal store currently receives it through the EventStore callback.

`large_tool_output_file_write_error` is also live but has no meaningful
frontend presentation. Do not delete the failure signal. Convert it to a
normal tool/server error plus structured logging, or add an intentional
error renderer; it must not fall through as an unknown JSON card.

### Required historical compatibility

Deleting a type from current producers/renderers does not remove rows
already present in `structured-chat-events.sqlite`. `ShouldShowEvent`
currently fails open for unrecognized types, so old removed rows can:

- consume the initial 300-event restoration window;
- increase restore payload and reconciliation work; and
- render as `Unknown Event Type` cards after their renderer is deleted.

Maintain a compact `LEGACY_REMOVED_EVENT_TYPES`/tombstone set at the
server polling/restoration boundary. It must include all 37 names from
deletion batches 1-3 plus the 23 names above when batch 4 lands. Test
that each tombstoned historical event is excluded from polling and does
not consume the structural-event window.

Landed 2026-09-22 (batch 4, 20 names, 57 total): the tombstone set is
server-side — `LEGACY_REMOVED_EVENT_TYPES` in
`agent_go/internal/events/event_store.go`, enforced by `ShouldShowEvent`
(fail-closed), so tombstoned rows are excluded from polling and never
consume the structural-event window. Guarded by
`TestLegacyRemovedEventTypesExcludedFromPolling`, which asserts every
tombstoned name fails `ShouldShowEvent` and that legacy
`step_progress_updated` / `workflow_end` / `tool_response` rows are not
returned by polling. Batch 5 added the 3 summarization names (60 total).

### Diagnostics-rail correction found during review

The minimal-rail comment says content-bearing errors remain visible, but
`TERMINAL_RAIL_HIDDEN_TYPES` currently includes
`orchestrator_agent_error`. That event is actively emitted for routing,
todo, message-sequence, human-input, and Crew failures. Remove it from
the hidden set and add a terminal-selection regression test so developer
diagnostics never suppress the failure that explains why a child
execution stopped.

### Verification required for batch 4

For every proposed removal:

1. search raw wire strings and typed constructors across builder,
   `mcpagent`, and the provider repo;
2. prove there is no `emitTypedEvent`, event-listener, bridge, or generic
   envelope construction path (schema registration is not a producer);
3. distinguish structured chat events from file-log and bot-notification
   namespaces that reuse similar strings;
4. regenerate schemas/types and run TypeScript plus event/transcript
   suites; and
5. restore a fixture containing every legacy name and verify that no row
   is returned or rendered.

## Appendix: deletion batch 4 (2026-09-22, uncommitted)

Accepted the external review's safe-to-delete list (17 + 3 conditional)
with per-event producer verification, not reference matching. Deleted 20
events (57 REMOVED total across 133 -> 135 catalog entries):

- False-positive class, confirmed unproduced: `debug` (60-file producer
  list was logger-`Debug()` calls), `error_detail` (provider logging
  helpers), `llm_messages` (`compactedInLLMMessages` counter variable),
  `performance` (telemetry-field/SSE-flag strings), `tool_response`
  (message-part-namespace matches).
- Schema/Langfuse-only: `json_validation_start/end`, `llm_token_usage`,
  `streaming_progress`, `streaming_error`, `streaming_connection_lost`
  (tracer handlers with no emitter), `tool_call_progress`, `tool_output`.
- Ghost/phantom: `background_agent_failed` (const never defined),
  `human_verification_response` (no constructor either side;
  `HumanVerificationDisplay` renders `request_human_feedback`).
- Trio: `workflow_start`/`workflow_progress`/`workflow_end` (never
  emitted; `orchestrator_end` is the live completion signal,
  `workflow_error` separately live and kept).
- Bare name only: `mcp_server_connection` (carrier struct + ctor stay as
  the payload for the live `mcp_server_connection_start/end` tracer
  events; const kept as the payload tag).

Verification per the review's 5 steps: raw wire strings + typed
constructors searched across builder/mcpagent/provider; no emit,
listener, bridge, or envelope path for any of the 20; file-log and
bot-notification namespaces (`logErrorDetails*`, `case "tool_response"`)
left untouched; schemas + generated types regenerated with `tsc -b`
clean; server-side tombstone extended to 57 with
`TestLegacyRemovedEventTypesExcludedFromPolling` (see note under Required
historical compatibility). Full-suite failures observed during the batch
were proved pre-existing via a HEAD worktree and isolation runs.

## Appendix: deletion batch 5 — context summarization feature removed (2026-09-22, uncommitted)

3 more REMOVED (60 total). Unlike batch 4's dead wire names, this deletes
a whole feature: `context_summarization_started/completed/error` plus
everything that produced, configured, or displayed them.

Why it was dead: the automatic path (`mcpagent/agent/conversation.go`)
requires `enableContextSummarization`, which defaults false everywhere
and is force-disabled for every coding-agent CLI provider ("handled
natively by CLI") — and only coding agents run now. The manual path
(`POST /sessions/{id}/summarize`) had a route, handler, API client, and
`ChatInput.handleSummarize`, but zero UI callers: no slash command, no
button.

Removed: `mcpagent/agent/context_summarization.go` + doc + README entry,
agent fields/options/defaults/provider-disable blocks, conversation
auto-trigger, event consts/structs/ctors, Langfuse consts/arms/handlers,
grpc config field + mapping (generated `pb/agent.pb.go` field left;
harmless), `summarization_routes.go` + route, store `AddSummarization*`
methods + payload structs, config plumbing
(server/agent_tuning/llm_agent/interfaces/base_agent/factory/orchestrator/
preset), `fixed_threshold_*` metadata + the 3 token displays that read
it, frontend client/request/response/preset/query/request fields,
`isSummarizing` state + indicator + `handleSummarize`, command-context
fields, EventDispatcher branches + 3 debug components, rail-denylist
entries; schemas + generated types regenerated. Tombstone extended to 60
(existing polling-exclusion test covers the new names by iterating the
map). `agent_tuning_test.go` rewritten for the surviving knobs; mcpagent
golden surface test updated.
