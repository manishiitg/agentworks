[← platform / scheduler-runs](index.md)

# PLAT-040 — full schedule silently refuses to start while a chat/Pulse lane is active

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | platform |
| Area | scheduler-runs |
| Summary | scheduler concurrency, durable run leases, and Schedule UI |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-06` |

- **Priority:** P1
- **Owner:** scheduler concurrency, durable run leases, and Schedule UI
- **Source workflow:** Hetzner SSH
- **Related tickets:** PLAT-020 (scheduled-session continuation), PLAT-026
  (global activity visibility), and PLAT-035 (retained-chat settlement)

## Problem and runtime evidence

The user opened the Schedule panel and clicked the full Hetzner schedule three
times. No full run appeared in the Global Activity Monitor and the UI showed no
error. The authoritative scheduler state recorded all three attempts as:

```text
schedule_id=b2234610-e3a2-49dd-b406-855e4ee878a8
trigger_source=manual
decision=skipped_busy
reason=schedule run already active for scope: workflow\x1fWorkflow/hetznerssh
fired_at=2026-08-06T06:45:02Z, 06:45:22Z, 06:45:23Z
```

The active owner was manual Pulse run
`7cd3218c-83f5-49d8-ad94-b3a57cf0eb39`, in `pulse_modules`, not another full
workflow execution. The schedule therefore never received a session ID and
could not appear as running. `WorkflowScheduleRunsPanel.handleTrigger` then
discarded the rejected HTTP request with an empty `catch`, making a deliberate
backend refusal look like a dead button.

## Why this kept recurring

The chat/schedule concurrency invariant was implemented in two independent
places:

1. the in-memory tracked-execution check; and
2. the SQLite scheduler lease keyed only by workflow.

Earlier work and `TestChatAndScheduleDoNotBlockEachOther` covered the first
direction for an ordinary workflow-builder chat, but did not exercise a real
manual Pulse lease. Manual Pulse still acquired the same durable lock key as a
producing schedule. Fixing only the active-session predicate therefore could
not make the end-to-end trigger succeed. The silent frontend `catch` hid that
remaining boundary on every recurrence.

## Fix

- Schedule-trigger failures now show the backend reason in an error toast and
  are logged in the browser console; they are no longer silently ignored.
- Manual Pulse uses a dedicated durable `workflow-pulse` lane. It still blocks
  a second Pulse run but no longer owns the producing workflow-schedule lane.
- The tracked-execution guard blocks a schedule only for another running
  `full_workflow`. Interactive chats, Pulse stages, and read-only reviewers do
  not block it.
- Producing schedules retain the existing workflow-scoped durable lock, so a
  second full schedule for the same workflow is still refused.

## Verification

Implemented checks cover the two previously independent boundaries:

```text
go test ./pkg/schedulerstate ./cmd/server -count=1
npx tsc -b --pretty false
```

Focused tests prove:

- a manual Pulse lease and one full schedule lease coexist;
- a second full schedule cannot acquire the workflow lane;
- interactive chat and Pulse reviewer executions do not block a schedule; and
- a running full workflow still blocks another schedule.

## Runtime acceptance

After rebuilding/restarting the backend:

1. start or retain a Hetzner interactive chat/manual Pulse;
2. click **Run** on the primary full schedule;
3. confirm the trigger returns a new `schedule-manual--b2234610...` session;
4. confirm that session remains visible in the Global Activity Monitor for the
   full execution; and
5. while it is running, click the full schedule again and confirm a visible
   busy error is shown rather than a silent no-op.

