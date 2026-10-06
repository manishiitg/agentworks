[← schedules / general](index.md)

# PLAT-210 — schedule `a5f7750a` "no runs found" was PLAT-191's already-fixed gap, confirmed via live `schedule_fire_decisions`

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | schedules |
| Area | general |
| Summary | P4 — no live defect. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `resolved` — correction/closure record only, no code change; already fixed by PLAT-191, never re-verified |
| Last synchronized | `2026-08-29` |

- **Priority:** P4 — no live defect. Same failure-mode class as PLAT-191
  and PLAT-195 this session: a genuinely accurate finding at the time it
  was filed, whose underlying tool gap was already fixed by unrelated work
  that landed before this correction pass, and never re-checked against
  current tooling.
- **Owner:** N/A — no code change made or needed by this ticket.
- **Related:** [PLAT-191](../occurrences/plat-191.md) (`get_schedule_runs` only reading
  actual-run rows, missing `schedule_fire_decisions`' skip/non-run
  occurrences) — the exact fix this finding needed, already shipped this
  session before this correction was written. `schedule:a5f7750a-f6a6-434c-bce0-34633f196a0f`
  (confida-login, low), the finding this closes.

## The finding, and why it was reasonable when filed

*"Review-cadence reassessment: cron left unchanged (correct for current run
volume), but the review schedule reports no execution history at all."*
`get_schedule_runs(job_id=a5f7750a-f6a6-434c-bce0-34633f196a0f)` returned
*"No runs found for this schedule"* — reasonably read as "is this daily
18:00 IST review schedule actually firing at all?"

## Already answered — confirmed via direct `schedule_fire_decisions` query

`workspace-docs/_system/schedule-state.sqlite`'s `schedule_fire_decisions`
table (the durable, ground-truth per-tick evaluation log PLAT-191 traced
and wired into `get_schedule_runs`) shows schedule `a5f7750a` **is**
evaluating correctly every day, exactly the same pattern PLAT-191 found for
confida-login's other schedules:

- `2026-08-18T12:30:00Z`: `decision=started` — the schedule genuinely fired
  and produced run `3418c8f7-97c4-47b5-b40e-be8dc57a807b`.
- `2026-08-19` through `2026-08-28` (10 consecutive days): every occurrence
  is `decision=skipped_paused`, `reason="global scheduler pause is
  active"` — the same multi-day global pause PLAT-191 already diagnosed
  and documented as a human-operator action, not a scheduler defect.
- `2026-08-25`: two `decision=started` manual triggers and one
  `decision=skipped_busy` (`"another schedule owns the workflow"`) — also
  correctly evaluated and recorded.

The schedule was never silently failing to fire. `get_schedule_runs` simply
could not surface any of these skip/non-run decisions before PLAT-191's fix
— the exact tool gap PLAT-191 closed by wiring `ListFireDecisions` into
`get_schedule_runs`'s output as a new "Skipped/Non-Run Occurrences"
section. That fix was already live in this codebase before this
correction pass began; this finding's own evidence (gathered against the
pre-fix tool) never got a chance to reflect it.

## Explicitly not done

- No code change — PLAT-191 already shipped the fix this finding needed.
- Did not re-run `get_schedule_runs` live against a running server to
  visually confirm the new section renders for this specific schedule —
  confirmed instead via the same underlying data source
  (`schedule_fire_decisions`) `get_schedule_runs` now reads, which is the
  authoritative ground truth either way.

## Verification

- Direct SQL query against `schedule_fire_decisions` for
  `schedule_id='a5f7750a-f6a6-434c-bce0-34633f196a0f'`: 15 most recent
  decisions all correctly evaluated (`started`, `skipped_paused`,
  `skipped_busy`), matching PLAT-191's already-documented global-pause
  incident exactly.
