[← goals / plans-contracts](index.md)

# PLAT-579: Strict input/output graph preflight

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | plans-contracts |
| Summary | Strict input/output graph: a step or run whose inputs cannot be read is refused before it starts (warn by default, enforce by switch) |

## What happened

Upwork's 2026-10-05 merge of `bid-read-and-draft` into `bid-pick-job` left six inputs of the recorders without a
producer listing them in `context_output`; nothing failed until the next run would have, and
[PLAT-561](plat-561.md)'s map only reported it. Owner, 2026-10-06: the input and output graph should be strict, a run
or step with a broken graph should fail, and structural checks belong on the Go side rather than with the agent.

## Fix

- `step_based_workflow.GraphPreflight(workspace, stepID)` reuses the reference map and returns the strict problems:
  `dependency_unproduced`, `dependency_not_staged` (also for message_sequence steps, which would otherwise hunt for
  the file), `dependency_step_without_output`, `relative_dependency_unresolved`, `missing_step_ref`. For one step it
  returns that step's; for a whole run only the steps that always run (before the first routing or branch step), since
  a step behind a route may never run and is checked when it starts.
- `workflowGraphPreflight` runs it before `execute_step` and `run_full_workflow` (the manual guard) and before the
  full workflow run path in `server.go`, next to the crew step preflight.
- `AGENTWORKS_GRAPH_STRICT=off|warn|enforce`, default **warn**: it logs `[GRAPH STRICT] would refuse ...` and puts the
  same text at the top of the `execute_step` result. `enforce` refuses with `workflow_graph_check_failed` and the
  exact inputs before anything starts. Flip to enforce once the log of real runs is clean.
- Plan structure (routing targets, step fields) was already enforced by `ValidatePlanStructure` on every write and at
  execution; this adds the data-flow side.
- One test pins the three modes.

Measured on the 18 local workflows: 15 clean; enforce would refuse `toptal-submit` (Upwork: `toptal_selected.json` is
declared by `toptal-scan-draft` but not listed in its `context_output`) and `step-reddit-scan-draft` (build-in-public:
`reddit_cdp_preflight.json`, same shape) when reached. No whole-run refusal anywhere.

## Left

- Scheduled runs: confirm they go through the full run path in `server.go`; if the scheduler starts runs another way,
  add the same call there.
- Refusing a plan edit that adds a new graph error is not built; edits still report (PLAT-561) and flag Plan Drift
  (PLAT-565). A refusal needs the edit rolled back after the tool has written it.
- Other deterministic checks that could join the preflight: the DB and report contract checks Plan Drift already runs.
- Producer order is not checked: the platform's execution-plan lookup finds producers placed after the consumer.
- Fix the two real gaps above in their workflows (list the files in the producers' `context_output`).
