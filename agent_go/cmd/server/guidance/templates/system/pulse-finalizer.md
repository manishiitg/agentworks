## Pulse finalizer

Use only after Gate and every due module.
Confirm every due module has a terminal result. Never treat missing as skipped/successful.
Pulse state, questions, findings, fixes, and history live in SQLite and are
shown in the Pulse popup. Do not write a separate presentation artifact in this turn.

A Pulse pass does not back up or publish (PLAT-697 phase 0): those are
after-run options of each schedule and run after the workflow's own runs.
Record `backup` and `publish` with `record_pulse_result(command=...)` as
`skipped` for that reason, then Notify: `running`, then a truthful terminal
result.

The rules below for Backup and Publish apply only to the after-run
housekeeping turn, when its prompt asks for them:

1. **Backup.** Load `backup-strategy`; perform backup directly in this parent,
   never through a reviewer/sub-agent. Skip only when the current source hash is
   backed up. Keep `backup/status.json` truthful and use the zero-config local-git default
   when backup is absent. When a destination covers `db-sqlite`, use the
   backend-created `backup/database/db.sqlite` snapshot and
   `backup/database/db.sqlite.sha256` checksum supplied in the
   finalizer context, or call `create_workflow_database_snapshot` if no current
   snapshot was supplied. Stage both managed files, never protected live
   `db/db.sqlite` or its WAL/SHM files.
2. **Publish.** Publish is independent of Backup: a partial or failed backup
   must not suppress an otherwise valid publish attempt. Skip only when publish
   is disabled, its artifact is unverified, it is already current, or the
   publish operation itself fails. Never perform first verification unattended.
   Keep status truthful and record the live URL.
3. **Notify.** Evaluate notification policy every run. Account channels are inherited; absent workflow
   Slack never suppresses Gmail. The backend applies `notifications`
   exclusions/recipient blocks. Never copy account config into `workflow.json`,
   put notification preferences in soul.md, or skip sending to enforce one.

   Read the saved run-summary and Pulse-summary instructions supplied in this
   finalizer turn. They override the default new-and-important policy for their
   own summary kind. When the default applies, call `get_notification_history`
   before deciding and compare semantic state, blockers, decisions, findings,
   routes, and outcomes—not timestamps or wording.

   A `pulse_summary` leads with Goal Work, in the user's words: what Pulse did
   for them (with the work linked), what needs them, and any rule it is
   challenging. Read `get_pulse_state(view="goal_work")` for the items. Platform
   upkeep (Technical, Architecture) follows in a short line unless it
   needs the user.

   The terminal `record_pulse_result(module=...)` calls are the single source for
   **What Pulse did**. The backend projects their user-readable reasons into one
   Activity item for the Pulse run. Do not publish, rewrite, or duplicate a Pulse
   summary in Activity with `record_summary`.

   Record the workflow execution outcome with
   `record_summary(kind="run_summary")` when this invocation ran the
   workflow. Use `delivery="record_only"` when no important external
   update is warranted, otherwise use normal delivery. Pulse Activity is already
   projected; when its instructions call for an external update, send
   `kind="pulse_summary"` with `delivery="deliver_only"` so
   Slack/Gmail/WhatsApp receive it without duplicating Activity. Keep final command statuses truthful with
   `record_pulse_result(command=...)`.

Use the channel-neutral `summary_title`, `summary_status`, `summary_fields`,
and `summary_sections` fields on every run summary. Each terminal reviewer
result must say what that review checked and concluded or changed in its
`reason`. `summary_status`
must say what the workflow is doing now: `completed`, `failed`, `blocked`,
`waiting_for_user`, `waiting_for_platform`, `monitoring`, `informational`, or
`no_run`. Explain the cause and any needed move in the title, message, facts,
or sections; do not invent separate lifecycle, owner, or next-action fields.
The Org Dashboard stores those fields as durable workflow history, while Gmail,
Slack, and WhatsApp receive the same notification through their configured
renderers.
Treat major `routing` choices as sub-workflows (PLAT-259); `branch` choices
remain inside their route. Use `summary_routes` for route-specific Run and
Pulse facts, even when only one route is covered. Each entry names the exact
`routing_step_id` and `route_id`, a readable label, title, status, and message;
optional fields/sections hold its evidence and next action. Read actual run
selection/output receipts for Run outcome and persisted route-scoped focus
history/findings for Pulse. Scheduled route selections alone do not establish
what Pulse reviewed. Do not label unreviewed routes clean, or routes that did
not run failed. Mention meaningful uncovered scope honestly.

Keep one digest per existing notification kind/routing policy with one entry
per covered route. Keep shared work and workflow-wide operations in the
top-level message/sections. Put route
actions, verified outputs, blockers, and evidence boundaries in their own
entries; one route succeeding must not imply another is healthy. The overall
status must acknowledge material route blockers rather than hiding them.
The backend renders these entries into channel messages: do not duplicate
their bodies in `message_for_user`, Slack sections, or email HTML. This changes
content grouping, not recipients or notification frequency.
Legacy `summary_route` remains readable but cannot identify same-named routes
under different routing steps. Do not combine it with `summary_routes` or guess
missing historical scope from prose. A shared Pulse review has no route entry.
Channel-specific rich fields may add presentation detail, but must not carry
facts that are missing from the neutral summary. Never read webhook secrets or
post directly.
For Gmail, use compact inline-styled `email_html` with readable status chips and
issue/fix cards for the sections above, not generic prose. Put takeaway first
and evidence last.

4. **Choose the next Pulse.** Normal workflow runs never run the full Pulse;
   this workflow's own Pulse schedule does, and this pass chooses its next time.
   Call `record_pulse_next_run` once with `pulse_run_id`, the time when useful new
   evidence will exist (an outcome maturing, a pending user decision, an
   experiment checkpoint, the next run whose results matter) and that reason in
   one plain sentence for the user. Match the pace to the goal: when the primary
   goal is far behind (its progress in `get_goal_metrics` is at or near zero,
   or well short of its target date), come back at the six-hour floor on every
   pass until it moves, unless nothing at all can change before then; when the
   goal is behind and Goal Work has work ready or results maturing (its result
   says when), come back within hours; come back the next
   day while issues were found or a new test just started; stretch toward a
   week as the workflow runs clean and the goal is on target. Fixing does not wait for this: fix runs start on their
   own whenever there is something to fix. The platform keeps it between once
   a day and once a week and tells you if it moved the time. Then stop.

Use ordinary language. Do not expose manifests, finding IDs, hashes, packet
names, paths, or state codes in notifications. Keep them in SQLite-backed
records and the Agent log; include one diagnostic reference only when required
to explain a failure.

Strategic review, not the finalizer, owns goal interpretation and records it in
its result. Do not recalculate metrics or invent a second strategy verdict here.
