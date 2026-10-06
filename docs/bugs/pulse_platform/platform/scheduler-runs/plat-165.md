[← platform / scheduler-runs](index.md)

# PLAT-165 — Schedule history is a filtered chat archive, not an occurrence ledger

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | platform |
| Area | scheduler-runs |
| Summary | P1 — operators cannot tell which schedule occurrences actually |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `partially implemented` — Schedule activity now projects durable run records; occurrence-level due timestamps, stage aggregation, and server-side pagination remain |
| Last synchronized | `2026-08-21` |

- **Priority:** P1 — operators cannot tell which schedule occurrences actually
  ran, were missed, failed, or can safely resume. This makes schedule health
  and cost impossible to judge from the product UI.
- **Owner:** durable scheduler run history, schedule occurrence projection, and
  `PreviousChatHistoryPanel`/workflow history UI.
- **Related:** [PLAT-136](../cost-telemetry/plat-136.md)) (per-occurrence folder/cost identity),
  [PLAT-145](plat-145.md) (collision queue/skip policy),
  [PLAT-164](../coding-agent-bridge/plat-164.md)) (conversation restoration), and
  [PLAT-040](plat-040.md) (schedule trigger visibility).

## Evidence and root cause

The current **Schedules** filter is a filtered list of saved conversation
sessions. Each row uses the first message as its title, plus chat timestamp,
message count, and provider label. One real scheduled occurrence can therefore
surface as several indistinguishable rows such as `PULSE RUN CONTEXT` and
`PULSE FINALIZER`, while a workflow execution is another row. The screen shows
neither the scheduled/due time nor an authoritative terminal outcome.

That is why the operator sees `Schedules 21` but cannot answer basic questions:

- Which configured schedules actually fired?
- Was an occurrence completed, missed, queued for capacity, skipped by a
  collision policy, stopped, or failed?
- Which execution/Pulse stages belong to the same occurrence?
- Is there a retained interrupted session that can resume, or would the action
  begin a new manual run?

The persistence model already distinguishes these concepts in scheduler and
run records. The UI projects generic chat history instead, so it loses the
occurrence relationship and exposes internal prompt text as the primary label.

## Product decision

Replace the Schedule chat filter with **Schedule activity**, driven by durable
schedule occurrences rather than conversation sessions.

At the top, show truthful status counts for the selected workflow:

```text
Schedule activity   All 21   Running 1   Completed 12   Missed 3   Failed 2
```

Each primary row represents one scheduled occurrence:

```text
Completed  Daily execution · default
Due 09:00 · started 09:02 · finished 09:46 · 12 steps · $4.18
Execution completed · Pulse review completed · Fixer skipped
                                                   Open   ⋯
```

For non-success outcomes, state the reason plainly:

```text
Missed  Evening engagement
Due 21:00 · did not start: prior execution still active
                                                   Why?   Run now   ⋯
```

Pulse Gate/Review/Fix/Finalize and child conversations are expandable stages
inside their parent occurrence, not independent schedule rows. `Open` leads to
the restored parent conversation (PLAT-164) and its available trace.

## Interaction rules

1. **Open** is always available.
2. **Resume** appears only for a genuinely interrupted occurrence with a
   retained/reusable session. It must never imply that a completed run can be
   resumed.
3. **Run now** explicitly starts a new manual occurrence and follows normal
   schedule side-effect/collision rules; it is not a synonym for Resume.
4. **Delete conversation record** belongs under an overflow menu. It removes
   only the chat artifact after confirmation, never the durable scheduler
   occurrence, its result, cost, or audit trail.
5. A paged `Load older activity` control pages occurrence rows, not arbitrary
   chat sessions.

## Backend/UI contract

Expose a read model keyed by stable occurrence ID with at least:

- configured schedule ID/name and trigger source;
- due time, actual start/end time, terminal status and human-readable reason;
- execution/run identity, folder, duration, cost and token summary where
  available;
- linked Pulse stage terminal states and linked conversation/session IDs; and
- explicit `can_resume`/`resume_reason` and `can_run_now` capability fields.

The frontend must render this model directly. It must not infer outcomes from
first prompts, terminal text, message counts, or whether a conversation JSON
happens to exist.

### 2026-08-21 first delivery

The former Schedule chat filter now loads the workflow's durable scheduled-job
run records and presents them as **Schedule activity**. It has explicit
Running, Completed, Missed, Failed, Partial, Interrupted, and Waiting states;
uses the configured schedule name rather than a first prompt; links a stored
conversation only when its `session_id` matches the occurrence; and provides
Open, truthful Resume for an interrupted retained conversation, Run now for a
new occurrence, and conversation-only delete.

The backend already exposes durable per-job run history and scheduler missed
state, so this first delivery does not invent a second persistence store. It
still has two deliberate limits: each job's history is fetched separately when
the tab opens, and historic missed occurrences are currently summarized by the
scheduler's latest-missed/count fields rather than individual immutable rows.
Those limits are why the unified occurrence read model and stage aggregation
remain open.

### 2026-08-21 restored-conversation paging correction

Formatted restoration already requests the newest bounded conversational page
and receives a durable `history_pagination` cursor. The transcript renderer
incorrectly hid **Load earlier messages** until it observed a physical
scroll-away-and-back gesture. A short restored page can fit completely in the
viewport, so that gesture is impossible even though another backend page is
available. The reader saw `Previous conversation (N events)` with no action.

The renderer now treats an initially visible first item as the top of the
loaded transcript. It exposes **Load earlier messages** only when the server
cursor says another durable page exists, and the existing cursor-based request
prepends that page. This does not fabricate missing background-agent
transcripts: a run can only page conversation history that was actually
persisted; PLAT-164 remains responsible for making every background agent
durably inspectable.

## Acceptance tests

1. A completed execution with Pulse stages renders as one occurrence row with
   the correct due/start/end timing and stage summary.
2. A collision-skipped, capacity-waiting, failed, and missed occurrence render
   distinct status/reason labels; none are misreported as generic chats.
3. A retained interrupted session shows Resume; completed rows do not.
4. Deleting a conversation artifact leaves the durable occurrence visible with
   its outcome and an honest "conversation removed" state.
5. Paging returns older occurrences without duplicating child Pulse stages or
   reclassifying chat sessions as schedule runs.

## Non-goals

- This ticket does not alter scheduler collision/retry policy; it presents the
  policy outcome already recorded by the scheduler.
- This ticket does not replace durable child transcript storage (PLAT-164).
- This ticket does not hide the ordinary Chat history view.

## Decision history

| Date | Decision | Why |
|---|---|---|
| 2026-08-21 | Make schedule history occurrence-first, with conversations reachable as details. | An operator schedules work, not prompts. A chat archive cannot truthfully answer whether a scheduled occurrence ran or why it did not. |
| 2026-08-21 | Ship the UI against existing durable job/run records before adding a new aggregate store. | It removes the misleading chat-title projection immediately while retaining one clear backend follow-up for richer occurrence history. |
| 2026-08-21 | Show the existing restore pager when a short initial page is already at the top. | The action must be reachable without a scroll gesture that a short transcript cannot produce. |
