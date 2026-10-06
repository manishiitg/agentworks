[← platform / scheduler-runs](index.md)

# PLAT-145 — `skipped_busy` is terminal and unrecoverable, so important cron occurrences disappear instead of waiting for the workflow lease

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | platform |
| Area | scheduler-runs |
| Summary | P1 — scheduled financial/operational work can be silently lost |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — skip, bounded queue-latest, retry, and coalesce policies are durable, restart-safe, atomic, and visible through API/UI |
| Last synchronized | `2026-08-19` |

- **Priority:** P1 — scheduled financial/operational work can be silently lost
  even though the scheduler observed the occurrence correctly.
- **Owner:** scheduler fire-decision lifecycle, durable lease queue, and schedule
  configuration/UI.
- **Observed on:** Tectonic USA Day Trading.
- **Related:** [PLAT-040](plat-040.md) (correct full-workflow exclusion),
  [PLAT-041](plat-041.md) (durable occurrence identity),
  [PLAT-080](plat-080.md) (cursor bootstrap), [PLAT-144](plat-144.md)
  (dependent schedules).

## Live evidence

All expected Tectonic occurrences were written to
`workspace-docs/_system/schedule-state.sqlite`, but three were terminally
discarded as busy:

- 21:25 IST signals — `skipped_busy`;
- 23:25 IST signals — `skipped_busy`;
- 01:40 IST Daily Pulse — `skipped_busy`.

PLAT-040 correctly prevents two full schedules from running concurrently. The
defect is what happens next: the only durable outcome is a permanent skip. No
queue or retry policy exists, and the ordinary run-history view primarily shows
launches rather than making missed work actionable.

## Root cause

Lease exclusion and occurrence disposition are coupled. Failure to acquire the
workflow lease is treated as a final business decision instead of temporary
capacity state. The scheduler therefore cannot distinguish "this occurrence is
intentionally disposable" from "this occurrence must run when safe."

## Fix reasoning

Add an explicit collision policy to every schedule:

- `skip` — discard intentionally;
- `queue_latest` — retain one newest occurrence and start it after the lease;
- `retry` — retry the same occurrence with bounded backoff/deadline;
- `coalesce` — merge several missed occurrences into one catch-up run.

Persist queued lifecycle states:

```text
scheduled → waiting_for_workflow → claimed → running → terminal
```

Queue ownership must be atomic and reconstructed on restart. A later cron must
coalesce or supersede according to policy rather than creating an unbounded
backlog. For Tectonic, signals should use bounded `queue_latest`, market close
must use high-priority `queue_latest`, and Daily Pulse should wait behind close.

## Acceptance

- Busy no longer implies lost unless the schedule explicitly selects `skip`.
- A queued occurrence survives restart and is claimed exactly once.
- Multiple stale signal occurrences coalesce; they do not replay sequentially.
- Deadlines prevent obsolete market operations from running later.
- UI and API distinguish waiting, skipped, superseded, and running.
- Existing PLAT-040 concurrency guarantees remain intact.

## Implemented on 2026-08-19

Every workflow schedule can now select `skip`, `queue_latest`, `retry`, or
`coalesce` plus a maximum start delay. SQLite retains one bounded pending row
per schedule: queue-latest replaces stale work, retry preserves the exact first
occurrence/deadline, and coalesce preserves the first occurrence while recording
the newest occurrence and folded count.

Most importantly, queue removal and workflow-lease acquisition are now one
transaction. A lease conflict rolls both back, and a process crash cannot land
between “removed from queue” and “claimed as a run.” In-process launch
deduplication prevents two queue scanners from replaying the same row. Waiting,
expiry, reason, and coalesced count are exposed by the schedule API and rendered
in the schedule UI.
