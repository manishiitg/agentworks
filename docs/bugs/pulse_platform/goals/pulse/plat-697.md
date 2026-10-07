[← goals / pulse](index.md)

# PLAT-697: Goal Lead (Pulse as the goal owner)

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | goals |
| Area | pulse |
| Summary | Pulse becomes the goal owner: daily goal check, answers goal questions within its autonomy, goal memory; built on a small Crew-runtime subset. Phases 0-3 on main (Workflow Review before runs and backup/publish/notify as schedule options; goal check; enforced autonomy; recommendations on decisions, goal memory, decision log) |

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
- Phase 0 left: see "Phase 0" below.
- Phases 4-6.

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
- (Superseded by phase 0: Workflow Review no longer holds Run or Change.)
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

## Phase 0: Workflow Review before runs; housekeeping out of Pulse (on main, not deployed)

Workflow Review (Plan Drift) is a pre-run check (`cmd/server/workflow_review_prerun.go`):

- Before every run (scheduled runs in `runJob`; `execute_step` / `run_full_workflow` from any chat, Builder, Run or
  a Pulse turn, through `workflowReviewPreRunRegistrar`) a code check reads the due set Pulse's Gate used to be
  forced to act on: `CollectPlanDriftDueItems` (no current drift review, a plan-edit flag, an old contract version,
  new reference-map breaks under the current flags version) plus plan changes without dependency receipts.
  Clean: the run starts, no AI call.
- Otherwise the review runs first: the same reviewer contract (`plan-drift-review.md`) and typed receipts, in its own
  "Workflow Review" conversation with only the `plan_drift_review` worklist row. A scheduled run waits for it; a chat's
  run tool returns at once with "Reviewing the workflow before running…", the review shows as a background job of
  that chat, and its completion notifies the chat to run again. A break the review leaves stops the run with the
  reason (only runs that touch the broken step); a review that does not finish lets the run go.
- One review per plan revision (a hash of the due set and plan.json / step_config.json), stored in `planning/workflow_review.json`; the same state
  is never reviewed twice, so a run never loops. One review per workflow at a time.
- After changes: a tick launcher reviews workflows with an enabled schedule once the plan has been quiet for 10
  minutes (Builder edits, contract upgrades and flags-version bumps all change the due set), one at a time.
- Pulse no longer schedules, waits for or holds anything for it: `pulsemodules.ExecutionOrder` is Goal Work,
  Architecture, Technical; Gate's worklist records `plan_drift_review` not due; Goal Work keeps its levels; the
  fix run is Technical only. `get_pulse_state(view="module")` and the Pulse status API carry the latest review
  (`workflow_review`) as an input.

Backup, publish and notify are each schedule's after-run options (`cmd/server/after_run.go`):

- `after_run: {backup, publish, notify}` on each schedule (and `after_manual_run` for full runs from a chat) replaces
  `pulse_mode`. Notify is code: a run summary from the run's facts through the normal notification path (failures and
  status changes to the channels, routine successes recorded in the dashboard only). Backup and publish are skipped in
  code when the source hash matches the last backup / publish; only when there is something to do does one short
  housekeeping turn run, limited to those actions (provider steps live in `backup-strategy.md` /
  `publish-strategy.md`). The full Pulse finalizer no longer backs up or publishes.
- Migration at server start: basic, and the retired full, → all three on (what the basic finalizer did after a normal
  run); off → none; a legacy full keeps the workflow's own Pulse on. `pulse_mode` is read for one release where
  `after_run` is absent and kept in step with it (so older servers and the contract stamp read the same choice).
  These edits do not write plan changelog entries, so they never start a review. The daily goal check and the full
  Pulse start from `pulse.enabled`, not `pulse_mode`.
- UI: three checkboxes per workflow schedule (list and table views) and a "After a manual run" row; the Pulse tab no
  longer shows backup/publish/notify statuses or a "Workflow Review due" state.

Left / risks:

- Backup and publish to remote providers still need an agent turn when something changed; turning the provider
  playbooks into code is not done.
- The code run summary does not apply the workflow's run-summary content instructions, and no longer offers the
  fast Pulse request after a run.
- Workflows that never ran Plan Drift (Pulse off) get a full review on their first run after deploy; workflows with
  enabled schedules are reviewed ahead by the tick launcher (cost on first deploy).
- A break the review leaves goes to the Workflow Review chat (a Builder-mode chat of the workflow) and the stopped
  run's error; it is not posted into an existing Builder chat.
- The Pulse tab has no separate "recent activity" list; nothing moved to an Activity tab.
- Webhook and Relay deliveries are not reviewed before they run.
- Not run live. Tests: `TestWorkflowReviewRunsOncePerPlanRevisionBeforeARun`,
  `TestPulseModeMigratesToAfterRunOptions`, `ScheduleListView.test.tsx`.

