[← ops / cost](index.md)

# PLAT-136 — every run in the schedule popup showed the same cost, because every run claimed the same folder

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | ops |
| Area | cost |
| Summary | P1 — the run history is the surface used to judge whether a |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — rotation now repoints run history; live reverify pending |
| Last synchronized | `2026-08-18` |

- **Priority:** P1 — the run history is the surface used to judge whether a
  scheduled workflow is healthy and what it costs. Every row reported the same
  spend, so it could not answer either question.
- **Owner:** `pkg/orchestrator/agents/workflow/step_based_workflow/controller_run_manager.go`
  (`rotatePairedIterationZero`).

## How it surfaced

The schedule popup for `hetzner-ssh` showed seven runs with wildly different
durations — 5m 44s, 43m 14s, 1h 19m, 1h 39m, 23m 57s — and an identical
`$255.48 / 300.92Mt / [1g]` on **every** row.

## Root cause

Every run executes in `runs/iteration-0`, and its history entry records exactly
that. But `iteration-0` is only the live slot: the *next* run rotates it to a
permanent `iteration-N` (`rotatePairedIterationZero`). Nothing updated the
history entry when that happened.

Measured on `hetznerssh`: **24 of 25 entries recorded `run_folder: iteration-0`**
while the folders on disk were `iteration-0, 21, 22, 23, 24, 25`. One entry's own
error text names `iteration-25` while the entry itself says `iteration-0` — the
contradiction is visible inside a single record.

The popup looks cost and tokens up **per run folder**. Rotation already archives
the cost records (`ArchiveRunCostPaths`) and the evaluation scores
(`archiveEvaluationScoreRunFolder`) to the new name — both correct. So every
history row resolved to the one folder still called `iteration-0` and rendered
the **current** run's spend. The rows were not stale; they were all reading the
same live cell.

Run history was simply the third thing that names the folder and the only one
rotation never repointed.

## Fix

`ArchiveScheduleRunFolder`, called from `rotatePairedIterationZero` alongside the
two archive calls that already existed, repoints the history entry that owned
`iteration-0` at its new permanent name.

Only the newest **finished** claimant is repointed. Runs still holding the live
slot are skipped — a running run is obviously in it, and a capacity-suspended
run resumes into the *same* folder when the provider window reopens (PLAT-101),
so repointing it would send the resume looking for evidence under a name it
never wrote to.

The file is decoded as generic maps rather than a typed struct: the typed entry
lives in `cmd/server`, which this package cannot import, and a local struct would
silently drop every field it does not know about on write.

## What this does not fix

Historical entries stay wrong. The runs that produced `iteration-21..24` recorded
`iteration-0`, and the mapping from those entries to those folders is not
recoverable — the only evidence would have been the rotation that already
happened. Guessing would replace a visible wrong answer with an invisible one.

Rows for runs from **before** this change will therefore keep showing the live
slot's cost. Rows from here on will show their own.

## Acceptance

- After two scheduled runs, the older run's history entry names the rotated
  `iteration-N` folder and the newer one names `iteration-0`.
- The schedule popup shows different cost/token totals per row.
- A capacity-suspended run's entry still names `iteration-0` after an unrelated
  rotation, and resumes into that folder correctly.
