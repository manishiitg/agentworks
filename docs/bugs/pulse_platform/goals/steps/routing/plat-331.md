[← goals / steps / routing](index.md)

# PLAT-331 — concurrent webhook runs shared one mutable route decision

| Field | Value |
|---|---|
| State | closed |
| Priority | - |
| Product | goals |
| Area | steps/routing |
| Summary | records the proven RTS PR Reviewer race where #87's eligible run branched correctly, then its review step reread a shared `db/assets/route_selection.json` overwritten by #82's closed delivery. |

| Coordination | Value |
|---|---|
| State | Runtime and plan guards deployed; the only identified unsafe workflow was repaired; the remaining v1.0.42 scheduled migration is retired |
| Date | 2026-09-18 |
| Owner | step execution / deterministic routing |
| Related | [PLAT-328](plat-328.md) |

## Incident

RTS PR Reviewer run `iteration-68-hook` received `pull_request/opened` for
`course_designer#87` (`339f74cb-4cd2-51e2-9b94-9f9e47aae9b0`) at
15:41:09 UTC. Its gate correctly wrote a run-scoped `route_selection.json`
with `select_route=review`, and the branch selected `review` at 15:41:10.

Run `iteration-69-hook` then received `pull_request/closed` for PR #82 and
wrote `select_route=skip` at 15:41:19. The review step from iteration 68 was
instructed to reread the workflow-shared `db/assets/route_selection.json`, so
it saw iteration 69's PR identity and skipped #87. The two immutable run
artifacts were correct; the compatibility mirror introduced for PLAT-328 was
the race.

## Platform contract

Step outputs, including gate decisions and resolved routing/branch decisions,
belong under:

`runs/<iteration>/<group>/execution/<step-id>/`

`db/assets` remains available for deliberately shared workflow inputs, but a
dynamic route produced earlier in the same run must be consumed through
`context_dependencies: ["route_selection.json"]`.

The executor now persists every resolved routing or branch decision as its own
run-scoped `route_selection.json`. Plan mutations reject the narrow unsafe
case where a prior step declares run-scoped `route_selection.json` but the
router points at `db/assets/route_selection.json`. Legacy plans remain loadable
so Builder can repair them.

Contract v1.0.42 originally scheduled a managed cleanup turn for every older
workflow. That turn was retired after the only unsafe production plan found by
the census was repaired: asking an unattended agent to rewrite plan and script
artifacts blocked otherwise valid schedules and was disproportionate to the
remaining risk. The runtime isolation and plan-mutation rejection remain the
platform contract. Version 1.0.42 remains recognized as a historical marker so
already-migrated workflows continue forward normally.

A read-only production census on 2026-09-18 found one unsafe plan,
`rtsprreviweer/pr-review-branch`. Three explicit route sources in
`automationtesting` use the ordinary `route_selection.json` contract and are
left unchanged. The identified unsafe plan was repaired before the scheduled
migration was retired.

## RTS migration and acceptance

- Release `e63b6c7-20260918161036` (`e63b6c783`) was deployed with user
  approval on 2026-09-18. The planner health endpoint and the agent, browser,
  gateway, and workspace services were healthy after deployment.
- `rtsprreviweer` was migrated from v1.0.41 to v1.0.42. The branch and both
  destinations now declare `context_dependencies: ["route_selection.json"]`;
  the branch no longer has a shared `route_source_file`.
- The gate's obsolete `db/assets/route_selection.json` compatibility write and
  the skip step's shared-path fallback were removed. The gate still writes
  `STEP_OUTPUT_DIR/route_selection.json` and preserves append-only
  `pr_gate_decisions` audit rows in SQLite.
- Static production validation passed: the workflow plan and affected scripts
  contain no shared route path, both Python scripts parse, and all three
  consumers declare the run-scoped dependency. A timestamped server backup
  was retained before migration.
- Remaining acceptance: send two overlapping eligible/skip webhook deliveries
  and verify each downstream step retains its own PR and run ID.

Focused and full `step_based_workflow` package tests pass locally.

## Register notes

[PLAT-331](plat-331.md) records the proven RTS PR Reviewer race where #87's eligible run branched correctly, then its review step reread a shared `db/assets/route_selection.json` overwritten by #82's closed delivery. Routing and branch steps now persist their resolved decision in their own iteration execution folder, plan mutations reject that specific shared-mirror pattern when a prior run-scoped producer exists, and the only unsafe workflow found by the production census was repaired. The remaining v1.0.42 scheduled cleanup turn is retired; the runtime and mutation guards remain. Intentional shared route inputs remain supported. Release `e63b6c7-20260918161036` is deployed and RTS PR Reviewer is migrated to v1.0.42; overlapping-delivery live acceptance remains pending.
