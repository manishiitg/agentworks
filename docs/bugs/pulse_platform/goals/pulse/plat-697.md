[← goals / pulse](index.md)

# PLAT-697: Goal Lead (Pulse as the goal owner)

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | goals |
| Area | pulse |
| Summary | Pulse becomes the goal owner: daily goal check, answers goal questions within its autonomy, goal memory; built on a small Crew-runtime subset (design only) |

## What happened

## Fix

## Left

## What and why

The owner (2026-10-07): "there should be an agent which owns goals". Agents ask him goal questions he cannot answer,
and Strategic Review / Plan Drift do not answer them. Evidence: Substack's subscriber goal went unmeasured and
unworked for three weeks (Sep 18 – Oct 6) while runs took only the draft-approval route, and nothing flagged it.

Design: [Pulse as the goal owner](../../../../design/pulse_goal_owner.md). Pulse itself owns the goal, on a small
subset of the Crew runtime (persistent conversation, schedules, function calls, Slack slug); not a user Crew.

## Left

Everything; design only. Owned by the Pulse session. Phase 0 (owner, 2026-10-07): Workflow Review (Plan Drift)
leaves Pulse and runs as a pre-run check before every step/workflow run when the plan changed. Then: daily goal check + silence alarm + one message,
pilot Substack. Prerequisites done in phase 2 (below): `pulse.autonomy` enforced in the tools and the Goal Work
contract text fixed.

## Phase 2: autonomy levels enforced by the tools (on main, not deployed)

- During a scheduled Pulse Goal Work turn (strategic_review, and its receipt
  continuation) the scheduler holds the Pulse session's tools to that turn's
  `pulse.autonomy` levels (`cmd/server/pulse_autonomy_guard.go`). The check runs
  in `bindToolExecutionContext`, the binding every direct tool call of the
  session passes; the session comes from the server, never from tool arguments.
  Delegated helpers of the session are held too; workflow steps the turn starts
  (child MCP sessions registered to the run) keep their normal tools.
- Run at ask refuses `execute_step`, `run_full_workflow`, `debug_step`,
  `send_step_message`, `trigger_schedule`, `ask_platform_crew`. Change at ask
  refuses the Builder plan tools (`ScheduleGuardedTool` list), `update_step_config`,
  schedule create/update, `configure_goal_metrics`, `restore_step_from_changelog`,
  `manage_workflow_webhook`. Always refused in Goal Work: delete steps/schedules,
  `create_plan`, migrations, contract version. Outward at ask refuses
  `send_slack_message` and Slack writes, runs Google (`gog`) read-only, and
  refuses `notify_user` to `email_to`/`email_cc`. Each refusal says to create a
  decision request instead.
- A due Plan Drift holds Run and Change (as `docs/design/pulse_goal_work.md`
  says) in both the text and the tools; moving Drift out of Pulse is phase 0.
- The tool catalog is not narrowed per turn (that is why PLAT-452 did not keep
  `goalWorkToolAllowed`, a filtered tool list of the removed background agent);
  calls are refused instead.
- The Goal Work contract text now matches the levels (outward/change allowed when
  auto, a decision request when ask); "not enforced by the tools" is gone.
- Not enforced: browser, shell (curl with workflow secrets) and connected MCP
  server actions (not classifiable by name); `soul.md` writes through file tools;
  spending (no purchase tool; prompt rule). The manual `/run-goal-work` path in a
  Builder chat is still prompt-held (its text says so).
- Not exercised in a live Pulse run. Test: `TestGoalWorkAutonomyIsEnforcedAtToolDispatch`.
