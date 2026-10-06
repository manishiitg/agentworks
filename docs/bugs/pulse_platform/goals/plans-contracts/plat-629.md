[← goals / plans-contracts](index.md)

# PLAT-629: Step descriptions move to the standard layout (contract 1.0.46)

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | plans-contracts |
| Summary | Contract 1.0.46 converts every step description to the standard layout; the stamp and a Plan Drift check keep it there |

## What happened

Step descriptions were written in many shapes: long free text mixing the charter with how-to, business rules and
dated incident stories. The step-description guidance now prescribes one layout (`## Goal`, `## Inputs`, `## Output`,
`## Rules`, `## Done when`, `## Guides`), but existing workflows predate it. Owner approved converting all of them
through a contract upgrade (2026-10-06).

## Fix

- Contract **1.0.46** (`StepDescriptionLayoutContractVersion`) is the current and only execution-compatible version.
  Rung `upgrade-step-description-layout` applies to Goals workflows and Relays (Python relays skip the ladder).
- The rung tells the Builder to convert every plan step (nested sub-agent steps too) one at a time, largest first,
  skipping steps already in the layout so a re-run resumes; keep only WHAT; move binding rules to a knowledgebase note
  and reusable HOW to a learnings skill named under Guides; express existing Brain use as `brain:<folder>/<note>` under
  Inputs/Guides, writes under Output, limits under Rules (knowledgebase_access unchanged); drop history; never change
  behaviour, outputs, dependencies, validation or items; `check_plan_no_loss` for each step before saving; stamp last.
- Stamp gate `validateStepDescriptionLayoutStamp` (wired next to the managed-DB gate) reads `planning/plan.json` and
  refuses 1.0.46 while any live step (orphan steps excluded) lacks a `## Goal`, `## Inputs`, `## Output` or
  `## Done when` line (case-insensitive, optional trailing colon), naming each failing step and its missing headings.
- Plan Drift: `CollectPlanDriftCandidates` adds a deterministic `description_layout` check (same rule) to every pending
  candidate; `plan-drift-review.md` says a fail is fixed in the review by converting that step under the same rules.
- Tests: one pins the stamp gate (nested step named, orphan ignored), one the Drift check; existing ladder tests
  updated for the new current version.

## Left

- **Deploying 1.0.46 makes every workflow on that server need this upgrade before it can run** (operator-started in
  Workshop chat). Schedules are globally paused locally, so nothing is lost there; on a server, plan the migrations
  before or right after the deploy. Not deployed.
- The Drift check runs only for steps already due; a description-only edit does not make a step due, and the Drift
  contract version was not bumped (that would re-flag every reviewed step).
