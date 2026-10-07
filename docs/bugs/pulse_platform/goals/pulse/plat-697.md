[← goals / pulse](index.md)

# PLAT-697: Goal Lead (Pulse as the goal owner)

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | goals |
| Area | pulse |
| Summary | Pulse becomes the goal owner: daily goal check, answers goal questions within its autonomy, goal memory; built on a small Crew-runtime subset. Phases 1-3 on main (goal check; enforced autonomy; recommendations on decisions, goal memory, decision log) |

## What happened

Substack's subscriber goal went unmeasured for three weeks while runs went on, and nothing flagged it (below).

## Fix

**Phase 1 — goal check + silence alarm + one message (on main, not deployed).**

- After-run goal facts, code only: when a workflow run finishes (`finalizeRunMetadata`), `RecordGoalRunFacts`
  stores the route it took (from `route_selection.json`), whether that is the primary metric's route and completed,
  and whether a primary reading was recorded in `pulse_goal_observations` during the run, in `goal_run_facts`
  (db/db.sqlite). Runs before that are read from `schedule-runs.json` with the schedule's route selection
  (`step_based_workflow/goal_run_facts.go`, `cmd/server/pulse_goal_check.go`).
- Silence alarm, code only (`pkg/goalcheck`): not measured by a workflow run for N days (default 3; a builder-chat
  reading does not count as the workflow measuring), goal work ran without a reading, goal work skipped while other
  routes ran, no run for N days. Schedules all paused: said once, then quiet until a run, a reading or the alarms
  change (pause fingerprint stored with the goal check).
- Exposed first to the agent: `get_pulse_state(view="goal_status")`, and inside `view="goal_work"`.
- Daily goal check: one short Pulse turn per workflow with a primary goal metric, at most daily, started by the
  scheduler tick after full Pulse and fix runs. It reuses the full Pulse's schedule ID, so it never runs beside a
  Pulse pass on the same workflow, shares the Pulse concurrency cap, does not move the full Pulse schedule and does
  not consume fast requests. On track: `record_pulse_goal_check(on_track)` and stop. Otherwise it acts within
  `pulse.autonomy` or makes one batched decision with a recommendation and safe default, records the verdict and
  sends one `pulse_summary` notification. The turn's tools are held to `pulse.autonomy` by the phase 2 guard;
  a paused workflow gets one report-only check with Run, Outward and Change all held.
- Strategic review (Goal Work) starts with the goal check (`strategy-auditor.md` step 0); the full Pulse
  finalizer's Pulse summary leads with the goal status.
- Pulse tab: goal status card at the top (on track / at risk / off track / not measured, key number, last
  measured, alarms) from the Pulse worklist API's `goal_status`.

Verification: `go test ./pkg/goalcheck` with a read-only copy of only the relevant Substack rows
(`pkg/goalcheck/testdata/substack_2026-10-07.json`): "not measured by a workflow run for 20 days (last reading
17 Sep)", one reading since from outside a run (builder chat 7 Oct), growth_funnel ran 6 times since 17 Sep
without a reading, growth work has not completed for 11 days (the 10 runs since: publish_review ×6, research ×2,
write_article ×1, one failed growth run), no run for 7 days with schedules paused; the pause is reported once and
a new run ends the quiet period. Server wiring verified on GitHub (verify-remote: build, vet, named tests); the
goal check turn itself is not yet run live.

## Left

- Phase 1: run the daily goal check live on Substack after deploy (owner's call) and check the message.
- Workflows with a goal in `soul.md` but no configured metric get no goal check yet.
- Phase 3 (below): not run live; `pulse.autonomy.answer=act` (Pulse answering within its levels, and a safe
  default applied when its time passes) is not built; the owner's answer reaches memory as a straight copy, so a
  reason the owner gives only in chat is not distilled until Pulse adds it.
- Phases 0 and 4-6.

## What and why

The owner (2026-10-07): "there should be an agent which owns goals". Agents ask him goal questions he cannot answer,
and Strategic Review / Plan Drift do not answer them. Evidence: Substack's subscriber goal went unmeasured and
unworked for three weeks (Sep 18 – Oct 6) while runs took only the draft-approval route, and nothing flagged it.

Design: [Pulse as the goal owner](../../../../design/pulse_goal_owner.md). Pulse itself owns the goal, on a small
subset of the Crew runtime (persistent conversation, schedules, function calls, Slack slug); not a user Crew.

## Plan

Owned by the Pulse session. Phase 0 (owner, 2026-10-07): Workflow Review (Plan Drift)
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

## Phase 3: recommendations on decisions, goal memory, decision log (on main, not deployed)

Owner decision (2026-10-07): recommendations first. Pulse does not answer decisions yet; the owner confirms.

- **Recommendation on every decision request.** `record_pulse_recommendation` attaches the Goal Lead's
  recommendation to a pending decision on the workflow: the option (or a free-text answer when there are no
  options), why, the evidence, confidence, what it blocks, and `safe_default_by` only with high confidence and when
  applying it needs no level held at ask (a workflow change needs `change=auto`). Stored apart from the answer in
  `pulse_recommendations` (db/db.sqlite, beside `report_human_inputs`), with a `recommended` event; list responses
  carry it as `recommendation`. The daily goal check and Goal Work (`strategy-auditor.md`) recommend on each
  pending decision every pass, on track or not. `humanAnswerScope` is unchanged: Pulse, scheduled and background
  sessions still cannot answer.
- **Config switch.** `pulse.autonomy.answer`: `recommend` (default) or `act`; only `recommend` is built, `act`
  behaves the same. The Pulse-tab autonomy save keeps the value.
- **Owner's answer.** Accept (or Change) in the Pulse tab answers through the normal answer route, then sends the
  apply message to the Builder chat as "Apply in chat" does. In the same transaction the recommendation is marked
  accepted, changed or dismissed.
- **Goal memory** in the workflow's memory area: `memory/goal.md`, next to the Builder's `MEMORY.md` (project
  memory, untouched). Sections: owner preferences and answers, decisions and outcomes, lessons, open bets, waiting
  on the owner; one line per entry, `- YYYY-MM-DD [owner answer | result | Pulse inference] text`. Every owner
  answer is copied in by code (question, chosen option, note, and whether it accepted or overruled the Goal Lead).
  Pulse adds results, lessons and bets with `record_pulse_goal_memory(action=add)` and rewrites it with
  `action=consolidate` (at most 60 entries; it is told to consolidate past 45). The goal check, `goal_status` and
  `goal_work` views carry it (`goal_lead`) so Pulse reads it first; soul.md wins on conflict. The owner reads and
  edits it in the Pulse tab (`GET /api/workflow/goal-lead`, `PUT /api/workflow/goal-memory`, write access).
- **Decision log.** The same rows: what the Goal Lead recommended, why, what the owner did, and the result, which a
  later goal check records with `record_pulse_decision_outcome` (answered decisions over a day old are listed in
  `outcomes_due`). Shown in the Pulse tab under the goal status card.
- **Pulse tab.** Under the goal status card: Needs you (each pending decision with the recommendation, Accept and
  Change, how long it has waited, what it blocks), the decision log with each result, and the goal memory editor
  (`GoalLeadPanel.tsx`).

Tests: `TestGoalLeadRecommendsAndOnlyTheOwnerAnswers` (a Pulse session's answer is refused, its recommendation is
stored as Pulse's and the decision stays pending; the owner's Accept answers with the recommended option, marks it
accepted and writes the owner-answer line to `memory/goal.md`), `GoalLeadPanel.test.tsx` (the Needs you card and
Accept). Not run live.
