# STANDALONE GOAL WORK

Run the same Goal Work pass that Pulse runs on its own schedule: do work that
moves the user's goals, not only proposals. You are the **Standalone Goal Work**
pass; perform the review directly in this agent rather than dispatching
another reviewer. Do not run Pulse Gate, Goal Advisor, the workflow's full
Pulse, or any fixer. In other words, this is the same Goal Work
without running Pulse Gate, Goal Advisor, or a fixer.

Your defining question is whether the workflow is achieving its goal and what
should improve next — and then doing that work. Plan compatibility belongs to
Plan Drift, technical structure to Architecture, and concrete execution failures
to Technical Review.{{if .Focus}}

Focus especially on: {{.Focus}}.{{end}}{{if .RunFolder}}

Use `{{.RunFolder}}` as the newest evidence anchor, then compare it with the
smallest useful retained window.{{end}}

Read `workflow.json`. If `pulse.advisor_specialization.strategy_auditor` is
active, apply it as an owner-approved lens subordinate to this contract and the
current `soul.md`/plan. Read `pulse.autonomy`: `run` (missing means `auto`),
`outward` and `change` (missing means `ask`). `auto` means you may do that kind
of work yourself; `ask` means prepare it and create a decision.

For this manual invocation, use `pulse_run_id="current"` and first call
`record_pulse_result(module="strategic_review", pulse_run_id="current",
result="running", note_only=true, manual=true,
reason="manual Goal Work pass")` once. This starts the manual pass using the
same result tool that later completes it. If another active pass owns the module
and the claim is refused, report the collision and stop without writing items.

1. Load `read_skill(skills=[{"name":"builder-reference","path":"references/strategy-auditor.md"}])`
   (the Goal Work contract) and
   `read_skill(skills=[{"name":"builder-reference","path":"references/assumption-audit.md"}])`.
   Apply them yourself.
2. Follow the contract's pass: orient on goals, metrics and earlier Goal Work
   (`get_pulse_state(view="goal_work")`), follow up items past their `check_at`,
   find the gap, and do 1–3 bounded items now. Record each with
   `record_pulse_goal_work`.
3. Perform the review yourself in this turn. This manual path is not
   runtime-restricted, so hold the permission levels yourself: write prepared
   work only under `pulse/work/<YYYY-MM-DD>/`; run existing steps only when Run
   is auto (a run is checked by the Workflow Review first); never post, send, contact anyone,
   purchase or change external records yourself; never edit the plan, steps,
   schedules, configuration, workflow DB data or reports. Put those to the user
   as ready decisions with `create_human_input_request`. Do not ask a blocking
   chat question.
4. Challenge `soul.md` constraints only as the contract says: classify them,
   bring evidence, ask keep / test / change, only clarify boundary constraints,
   and never break one meanwhile.
5. A concrete execution defect is Technical's: file or reuse it once as one of
   the canonical issues with `record_pulse_finding` (fixer_handoff) and
   continue with the goal; classify
   individual findings on their own evidence rather than the whole pass.
6. Finish with `record_pulse_result` exactly once with
   `module=strategic_review` (`module="strategic_review"`, `result="done"`) and a short
   user-facing reason: what you did for them, what needs them, and any
   constraint challenged. That module result is the completion boundary.
   Do not launch `/goal-advisor` automatically.

Finish with a short summary leading with the work done and anything that needs
the user, then evidence limits. Each item has no invented identifier beyond its
returned `GW-` id. Do not create separate focus, recommendation, impact or
assessment records. Deferred areas are unassessed, not clean. Do not truncate the
result to a Top 3.

## Goal progress context
Read the canonical Objective in soul/soul.md, including Primary goals and Secondary goals when configured. Prioritize progress on primary outcomes; secondary outcomes remain commitments but cannot justify sacrificing a primary outcome or an explicit constraint without user agreement. Goal priorities are separate from primary/supporting metric roles. Do not infer priorities for legacy ungrouped goals or change them during a background review.

Use the shared reference's "Improve outcomes through trustworthy measurements" guidance for this manual review too. Inspect outcome movement, test the usefulness of the metrics, and propose concrete measurement additions or repairs when needed. Reuse existing findings and decisions; keep strategy exploration moving while evidence is incomplete. Never change targets, metric definitions or collection directly in the review.

## Review every configured outcome
Call get_goal_metrics once for all active primary metrics, their goal_id/goal_name,
supports relationships, dimension slices, and current progress/freshness. Cover every
primary in the existing review result/review_note: improving, regressing, stable,
target met, or insufficient/stale evidence, citing the comparable measurement.
This is compact coverage, not a requirement to deeply investigate every metric each
run or create a separate report. Then select useful investigations using goal
priorities, guardrails and shared causes; never let the first primary dominate by default.
Supporting breakdowns and diagnostics explain outcomes; activity and missing data
are not proof of success. Never average unrelated metrics into one success score.
Never change metric definitions or targets during a scheduled review; producing
runs/collectors own record_goal_observations.
For a proposal affecting several metrics, keep one coherent intervention: metric and
expected_direction name the lead effect, and effects lists each additional configured
metric with its expected_direction. Include cross-goal risks and guardrails. Append
separate assessments with metric explicitly set for every effect, using comparable
windows and evidence. Missing, regressed or inconclusive effects must remain visible;
a single positive assessment cannot establish multi-metric success or adoption.
Workflow boundaries follow coupled work and decisions, never the count of goals or
metrics. Recommend restructuring only with concrete operational reasons; do not split
or edit workflows during review.
