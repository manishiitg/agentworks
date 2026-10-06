[← platform / coding-agent-bridge](index.md)

# PLAT-114 — background agents (Pulse's Gate/reviewers/Fixer included) have no durable execution record

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | P2 — not a correctness bug (nothing misbehaves), but it makes |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — durable log shipped and tested; not yet wired into a query surface |
| Last synchronized | `2026-08-16` |

- **Priority:** P2 — not a correctness bug (nothing misbehaves), but it makes
  every background agent, Pulse's own review/fix agents included,
  unauditable after the fact: you cannot reliably answer "what did this
  agent actually do" once enough time or session reuse has passed.
- **Owner:** background-agent lifecycle (`background_agents.go`)
- **Found while:** manually verifying the quality of a completed Pulse
  Review+Fix pass on social-media (2026-08-12). The work itself was real and
  good — confirmed only by reading the git diff by hand, because both of
  Pulse's own records of it were unreliable.

## Evidence

Checking what a specific Pulse run's background agents (Gate, reviewers,
Fixer) actually did turned up **two independent record-keeping paths, both
lossy, for different reasons**:

1. **`pulse_review_log` (the typed reviewer receipt) had zero rows** for a
   run whose `pulse_module_state.last_result` said `changed`. The module's
   own Fixer turn can simply never write the receipt — this is the same gap
   `validatePulseDueModuleReviewReceipts` (landed in the PLAT-095 follow-up,
   2026-08-15) was written to catch.

2. **The session's `ui_events` cache had it too, once — but it doesn't
   anymore.** Each chat session persists to
   `builder/conversation/<date>/session-<id>-conversation.json`, and that
   file's `ui_events` array does include structured `background_agent_started`
   / `background_agent_completed` events with `agent_id`, `name`, `status`,
   and `result`. But `trimChatHistoryUIEvents` hard-caps it at the last 200
   entries (`chat_history_persistence.go:85`,
   `maxPersistedChatHistoryUIEvents = 200`) and overwrites the file on every
   persist. This session got reused later the same day for the workflow's
   next scheduled run; that later activity filled the 200-entry window and
   silently evicted every event from the earlier Pulse pass.

Workflow steps don't have this problem — `runs/iteration-N/default/` gives
every run a durable, un-capped folder with a subfolder per step
(`logs/<step-id>/`, `execution/<step-id>/`), designed from the start to be
inspected after the fact. Pulse's own background agents, and any workflow's
`call_sub_agent`/`run_in_background` delegations more generally, never got
the equivalent.

## Why the cap doesn't matter for its actual purpose (and why that's the bug)

`ui_events` was never designed as an activity log. The only consumer that
reads it back (`chatHistoryTerminalSnapshotsFromUIEvents`) uses it purely to
reconstruct the last terminal frame so a resumed chat session doesn't render
blank — you only need the tail of the stream for that. 200 is a correct bound
for that purpose. Background-agent completion events ride in the same array
only because they're persisted through the same mechanism, not because
anyone designed a durable record for them. The bug is relying on a
screen-redraw cache as if it were an audit trail.

## Fix shipped

A new table, `background_agent_log`, created in every workflow's
`db/db.sqlite` (piggybacked on the existing `ensurePulseModuleStateSchema`
open/create path, so no new DB-open plumbing was needed):

```sql
CREATE TABLE IF NOT EXISTS background_agent_log (
	workspace_path TEXT NOT NULL,
	session_id TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL DEFAULT '',
	parent_execution_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	result TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	duration TEXT NOT NULL DEFAULT '',
	started_at TEXT NOT NULL DEFAULT '',
	completed_at TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL,
	PRIMARY KEY (workspace_path, session_id, agent_id)
)
```

`recordBackgroundAgentLogStarted`/`recordBackgroundAgentLogCompleted` are
called from the exact same two places that already emit the
`background_agent_started`/`background_agent_completed` UI events
(`emitBackgroundAgentStarted`, `emitBackgroundAgentCompleted`,
`background_agents.go`), so every background agent gets this for free —
Pulse's Gate/reviewers/Fixer, and any workflow's own
`call_sub_agent`/`run_in_background` delegations alike. Started and completed
upsert the **same row** (keyed by `workspace_path, session_id, agent_id`), so
a row stuck at `status='running'` is itself a real signal — that agent
started and nothing ever recorded it finishing.

**Deliberately scoped to workflow sessions only.** A session with no
`workspace_path` (plain chat) is a silent no-op — this table exists to give
workflow-scoped background agents the same kind of durable record workflow
steps already get, not to log every chat delegation.

**Best-effort by design.** A write failure is logged and swallowed, never
returned to the caller. Blocking or failing a background agent because its
own audit record couldn't be written would be a strictly worse failure mode
than the gap this ticket exists to close.

`backgroundAgentLogForSession(ctx, workspacePath, sessionID)` reads it back,
oldest first.

## Not fixed here

- **No query surface yet.** There's no MCP tool or API endpoint exposing this
  table today — an operator (or an agent) still can't ask "what did this
  Pulse run's background agents do" through a normal interface. Reading it
  currently means `sqlite3 db/db.sqlite` directly, the same way this ticket's
  evidence was gathered. Wiring it into a tool (natural fit: alongside
  `get_pulse_state`/`get_pulse_finding_backlog`) or the Pulse popup is the
  obvious next step, deliberately not bundled here.
- **No retention/pruning.** This follows the same convention as every other
  `pulse_*` table in this schema (`pulse_finding_events`, `pulse_review_log`,
  etc.), none of which prune either. If unbounded growth becomes a real
  problem across all of them, it's a shared fix, not specific to this table.
- **Historical gaps are not backfilled.** This starts recording from the
  first Go process that runs with this change; nothing before it is
  reconstructable, the same as every other durable table added after the
  fact in this register.
- **No correlation to a specific `pulse_run_id`.** Rows are keyed by
  `session_id`, not `pulse_run_id` — the row itself doesn't say "this
  belongs to Gate's 2026-08-12 pass" versus a later workflow-execution pass
  in the same reused session. In practice this is answerable by timestamp
  (as done manually for this ticket's own evidence), but a future pass could
  thread `pulse_run_id` through when one is active, the same way
  `pulse_review_log` already does.

## Verification

- `go build ./...` clean.
- `TestBackgroundAgentLogRecordsStartThenCompletionInOneRow`: start then
  completion upsert one row, not two; result/status/parent_execution_id all
  survive; the write is confirmed durable on disk (not just readable via a
  shared in-memory cache) by asserting the actual `db.sqlite` file exists
  under a temp `WORKSPACE_DOCS_PATH` after the test.
- `TestBackgroundAgentLogWorkspacePathRequiresAWorkflowScopedActiveSession`:
  pins the workflow-only boundary — unknown session and no-workspace chat
  session both resolve to no-op, a real workflow session resolves to its
  workspace path.
- `TestBackgroundAgentLogSkipsSessionsWithNoWorkspace`: proves the no-op path
  never attempts a database open at all, by never configuring
  `WORKSPACE_DOCS_PATH`/`WORKSPACE_API_URL` — an accidental attempt would
  fail loudly instead of silently succeeding against the wrong target.
- Full suite: 26 failures, all pre-existing and unrelated (verified against
  the same set observed immediately before this change, during the PLAT-113
  review) — zero new failures introduced.
- **Caught during test-writing, not shipped**: an early draft of the first
  test used a real workflow name (`Workflow/social-media`) and a real
  historical session/agent id with no `WORKSPACE_DOCS_PATH` override — since
  the underlying DB path resolves by walking up from the process's working
  directory to find a real `workspace-docs` folder, that draft wrote a real
  row into the actual social-media production database. Caught immediately
  by inspecting the row it produced, deleted by hand, and the test was
  rewritten to sandbox `WORKSPACE_DOCS_PATH` to a temp directory with a
  fictitious workspace name before being considered done. Recorded here so
  the next person touching this DB-path resolution knows real workflow data
  is one missing `t.Setenv` away from a test writing into it.

## Acceptance

- Every background agent belonging to a workflow-scoped session gets one
  durable row recording its start and (if it happens) its completion,
  independent of `ui_events`' 200-event cap and independent of whether the
  agent's own module ever writes a `pulse_review_log` receipt.
- A chat session with no workspace never triggers a database write.
- A write failure never affects the background agent's own execution.
