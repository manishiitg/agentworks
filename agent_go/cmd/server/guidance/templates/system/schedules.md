---
name: "schedules"
description: "Crew workflow and message schedules, dependency waits, Run now and the global pause."
external: true
external_tools: ["automations"]
external_note: "Use the discovered automations schema with scope=schedule. Read panel-automations for exact current fields; existing access applies."
---
# Schedules

Read `panel-automations`, then its schedules procedure and timing reference.
Those shared procedures own the current schema; do not reconstruct old workflow
scheduler fields or edit schedules.json directly. List first and preserve saved
IDs, timezone, enabled state, inputs, destination and settings not requested.

A Crew schedule either runs its single Python workflow directly (action `run`
with a workflow target, typed inputs and optional saved settings), or sends a
message to an agent. No intermediary model turn is needed for a workflow run.
New message schedules default to their own persistent chat; the person can choose
an existing chat. If unstated, ask “its own chat, or an existing chat?” Keep all
known inputs in unattended messages; never ask an unattended agent to invent an
approval or wait for a person.

Recurring time uses five-field cron and an IANA timezone. `*/5 * * * *` is every
five minutes; `in_minutes:5` is one time in five minutes. Calendar dates are one-time
occurrences, not a recurring rhythm. Read the returned timing_summary and say
what was actually saved. Enabling requires the workflow's required inputs.

Dependencies use the current after condition, outcome, delay and deadline.
Ordinary missed clock slots do not catch up. An already waiting dependency
occurrence retains its original scheduled time through its deadline/local day;
dependency waiting does not replay indefinitely. An ordinary busy-chat queue is
separate and has no local-day/dependency expiry; it may start after midnight. The global pause holds pending
work and resume rechecks the current schedule and prerequisites. Run now is a
requested real run that ignores timing and dependencies, not proof those
conditions succeeded. A save or accepted run receipt is not completion.

There is one global scheduler pause. Administrator `automations` actions
`scheduler_status`, `pause_all` and `resume_all` use `scope=schedule`, no project,
and `users:manage` consent. Ordinary schedule operations remain project-bound.
Read current status; historical skipped rows do not establish a present pause.
Never resume owner-paused work just to verify a change.

Existing standalone workflow timers still require conversion into Crew schedules;
do not claim a rename migrated them. Timers, HTTP webhooks and callable/internal
function bindings are distinct. Compact workflow list counts separate timers,
webhooks and function triggers; input mappings do not add timers.

For HTTP events, read the webhook procedure rather than using Run now on a hook.
For deterministic condition checks, read the Watchers procedure. Watchers are
caller-owned, list before creating, use stable event/request identities and
selected credentials, and report actual delivery separately from target success.
A one-time watch ends after accepted delivery; never keep a model turn open polling.
