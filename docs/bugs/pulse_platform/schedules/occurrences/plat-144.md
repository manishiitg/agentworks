[← schedules / occurrences](index.md)

# PLAT-144 — schedules that logically depend on another schedule can only use wall-clock offsets, so normal runtime variance makes them collide

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | schedules |
| Area | occurrences |
| Summary | P1 for operational/financial workflows; P2 generally. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented and regression-tested` — durable occurrence-linked fan-in, terminal policy, delay, deadline, authoring validation, and restart-safe release; deployment pending |
| Last synchronized | `2026-09-14` |

- **Priority:** P1 for operational/financial workflows; P2 generally.
- **Owner:** schedule schema, scheduler trigger graph, and workflow-builder
  schedule guidance/validation.
- **Observed on:** Tectonic USA Day Trading.
- **Related:** [PLAT-041](plat-041.md) (durable expected occurrences),
  [PLAT-145](plat-145.md) (what happens after a busy collision).

## Live evidence

The market-close schedule is configured for 15:55 ET and Daily Pulse for
16:10 ET. The latest close invocation needed about 18m42s for workflow work and
about 22m26s including finalization. Daily Pulse consequently fired while close
still owned the workflow and was recorded `skipped_busy`.

Moving Pulse by another guessed number of minutes reduces the immediate risk but
does not create a correctness guarantee: evaluation, provider latency, or
retries can change the close duration again.

## Root cause

The product can express only independent cron times. It cannot express the real
relationship: "run Daily Pulse after this market-close occurrence reaches a
terminal state." The builder therefore approximates a dependency with a clock
offset and the scheduler treats the resulting overlap as an unrelated collision.

## Fix reasoning

Add a dependent schedule trigger rather than a larger magic delay. One schedule
may declare:

```text
after_schedule_id = <market-close schedule>
after_terminal_status = completed | any_terminal
delay = 10m
deadline = 17:30 America/New_York
```

The scheduler should create the dependent occurrence durably when its parent
occurrence becomes terminal. It must use occurrence IDs, not merely schedule
IDs, so yesterday's close cannot release today's Pulse. A failed parent should
follow the declared `after_terminal_status` policy and remain visible rather
than silently disappearing.

The workflow builder and schedule editor should warn when two independent
schedules for the same workflow are closer than the predecessor's recent p95
duration.

Immediate workflow mitigation: move Tectonic Daily Pulse to 16:35–16:45 ET
until the dependency contract exists.

## Acceptance

- A dependent Daily Pulse occurrence cannot start before the matching close
  occurrence is terminal.
- The dependency survives restart and releases exactly once.
- Parent failure/cancellation follows an explicit policy and is visible.
- A deadline prevents a stale dependent operation from running the next day.
- Builder validation warns about fixed-time overlaps using observed durations.

## Implemented on 2026-08-19

Schedules now persist `after_schedule_id`, `after_terminal_status`,
`after_delay_minutes`, and `dependency_deadline` as typed manifest fields.
The dependency lookup follows the exact durable
`scheduled_for -> fire decision -> run_id` link for the matching local day; it
does not select whichever run happened to start most recently. Waiting is a
durable pending occurrence and its eventual queue consumption is atomic with
the run lease, so restart cannot release twice or lose the occurrence between
those operations. Failed/stopped parents follow the explicit terminal policy,
and delay/deadline decisions are returned through schedule status rather than
silently skipped.

The builder rejects unknown dependency IDs, self-dependencies, dependency
cycles, invalid terminal policies, negative delays, and malformed deadlines.
Observed-p95 overlap advice remains a useful authoring enhancement, but is no
longer required for correctness because the runtime dependency is authoritative.

## 2026-09-14 fan-in extension

The singular `after_schedule_id` contract now has a canonical list form,
`after_schedule_ids`. The dependent occurrence waits for **all** named
prerequisite occurrences on the same local calendar date. The singular field
remains readable for old manifests; replacing the list clears a legacy singular
value unless the caller explicitly supplies both in the same update. Manifest
validation rejects empty/unknown IDs, self-dependencies and cycles across the
full directed graph.

Builder schedule tools, list output, schedule UI details, Pulse Gate,
Architecture Review, Technical Review and Fixer guidance expose the fan-in and
collision policies. Dependencies still express ordering rather than permission to
overlap; the workflow-wide active-execution lease remains in force. Immutable
scheduled run folders and the later concurrency boundary are tracked separately
by [PLAT-320](plat-320.md).

A briefly introduced generic schedule wall-clock cap was removed on 2026-09-14
by owner direction. Schedule coordination must not rely on a generic kill
switch; cancellation remains explicit, while collision and dependency policies
govern when occurrences may start.
