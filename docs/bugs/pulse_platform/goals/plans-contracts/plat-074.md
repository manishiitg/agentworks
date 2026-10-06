[← goals / plans-contracts](index.md)

# PLAT-074 — 6 of 16 plan-mutation call sites never fed the changelog writer real data

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | plans-contracts |
| Summary | P2 — provenance gap, not data loss; makes changelog-based |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — 4 of 6 call sites fixed and tested; 2 reclassified (not platform bugs) |
| Last synchronized | `2026-08-10` |

- **Priority:** P2 — provenance gap, not data loss; makes changelog-based
  review/revert unreliable for a known subset of tools
- **Owner:** plan changelog writer (`writePlanChangelogEntry` /
  `completePlanChangelogEntry`, `planning_agent.go`)
- **Found on:** cross-workflow, PLAT-073 Cluster C (9 findings: build-in-public
  x3, linkedin x2, rtslatency, social-media, tectonicusadaytrading, upwork).
  Headline evidence: *"159 of 231 plan-mutation entries carry neither a
  `changes[]` array nor `before_ref`/`after_ref`"* (tectonicusadaytrading
  `69f4a8a7`).

## Root cause

One shared writer, not six independent bugs (PLAT-056 shape). Every
plan-mutation tool funnels through `logPlanChange` →
`writePlanChangelogEntry` → `completePlanChangelogEntry`, which computes
`before_ref`/`after_ref` in priority order: an explicit `BeforeSnapshot`/
`AfterSnapshot` first, then (before this fix) a `Changes[]` field-diff, and
if neither was supplied, both refs collapsed to the same
`sha256("[]")` placeholder — a value that is real and stable, but carries no
information (this is the exact placeholder PLAT-033 already named and fixed
for `update_step_config`).

10 of 16 call sites already supply `Changes` or a real snapshot (fixed under
PLAT-033/PLAT-055). The remaining 6 did not — but 4 of those 6 already had
the real before/after content sitting in scope at the call site (deleted-step
JSON, added-step JSON, route JSON, full plan.json before/after a rewrite); it
just was never wired into the entry. Only 2 of the 6 needed new data capture.

## Fixed (2026-08-10)

**`completePlanChangelogEntry` fallback** (`planning_agent.go:1028-1057`) — when
no explicit snapshot is given, fall back to hashing `DeletedSteps` for
`before_ref` and `AddedSteps` for `after_ref` before collapsing to the
placeholder. This single change fixes 3 call sites at once, since all three
already populate one of those fields with real content:

- `delete_plan_steps` (`planning_agent.go:3852`) — already captured full
  deleted-step JSON as `DeletedSteps`.
- `add_scripted_step` / `add_routing_step` / `add_message_sequence_step` /
  `add_human_input_step` / `add_todo_task_step` (shared adder,
  `planning_agent.go:4768`) — already captured full added-step JSON as
  `AddedSteps`.
- `delete_todo_task_route` (`planning_agent.go:5555`) — already captured the
  deleted route's JSON as `DeletedSteps`.

**`add_todo_task_route`** (`planning_agent.go:5302`) — now marshals the route as
it actually landed in the plan (post orphan-ref resolution) and passes it as
`AddedSteps`.

**`update_todo_task_route`** (`planning_agent.go:5458`) — the only site with no
pre-existing capture of any kind. Now marshals `*routeToUpdate` before any
field mutation and again after, passing both as `BeforeSnapshot`/
`AfterSnapshot`.

**`migrate_message_sequence_code_items`** (`message_sequence_code_migration.go:188`)
— `planContent` (pre-migration) and `migratedPlan` (post-migration) were
already read/built in scope for the write+rollback path; now also passed as
`BeforeSnapshot`/`AfterSnapshot`.

All four fixes use data the caller already had — none required a new read,
diff, or capture pass, so there is no added I/O cost.

## Reclassified, not fixed here

- `add_todo_task_route` / `update_todo_task_route` needed the new capture
  above (not pre-existing data) — see fix list; included for completeness
  since the investigation initially treated them as "not structurally
  blocked" alongside the other four, which undersold the actual gap.
- **Cluster C's 3 build-in-public and 2 linkedin findings, plus rtslatency,
  social-media, tectonicusadaytrading, upwork** — not independently
  triaged per-finding against which of the 6 call sites produced each one;
  the fix addresses the mechanism, and closure of individual fingerprints
  should happen via `pulse_close_stale.py` once this is live and a fresh run
  reproduces (or fails to reproduce) each one.

## Verification

- `go build ./...` clean.
- New tests: `plat074_changelog_added_deleted_steps_test.go` — 5 tests
  covering the `DeletedSteps`/`AddedSteps` fallback (including that an
  explicit snapshot still wins over both, and that a pure delete's
  `after_ref` and a pure add's `before_ref` correctly stay at the placeholder
  since nothing real exists on that side), plus one exercising the
  `migrate_message_sequence_code_items` snapshot shape end-to-end through
  `logPlanChange`.
- Full baseline (`go test ./cmd/server/... ./pkg/orchestrator/agents/workflow/step_based_workflow/...`)
  still shows exactly 22 pre-existing failures (19 guidance, 1 virtual-tools,
  2 step_based_workflow) — no new failures.
- **Not yet reverified live** — requires a server restart (these are
  `go run`-loaded via the `replace` directive) and a workflow run that
  exercises `delete_plan_steps`/an adder tool/a route tool/the migration tool
  before any of the 9 Cluster C findings can be closed with
  `pulse_close_stale.py`.

## Acceptance

- A `delete_plan_steps`, `add_*_step`, `add_todo_task_route`,
  `update_todo_task_route`, or `migrate_message_sequence_code_items` call
  produces a changelog entry whose `before_ref`/`after_ref` reflect real
  content, not the `sha256("[]")` placeholder, whenever real before/after
  content exists.
- `delete_todo_task_route` is covered transitively by the same
  `completePlanChangelogEntry` fallback that fixes `delete_plan_steps`.
