# Eval Removal Plan: Migrate to Producer-Owned Measurement

Goal: delete the file/run-based workflow evaluation ("eval") subsystem from
`mcp-agent-builder-go`, with outcome measurement continuing to come from the
producing steps' own stored outputs — no mandated route, step, or table.

Non-goal: routing evaluation (`routing-evaluation.json`, `RoutingEvaluatedEvent`,
deterministic routing, route tracing) is a separate feature and stays. Note this
does NOT include `routeEvaluations.ts`, which despite its name implements
eval-step route gating only (see Phase 5) — it is deleted.

## Direction: Flexible Contract, Not Mandatory Topology

Durable workflow outputs already land in the database, so Pulse needs
measurement data, not a particular plan shape. Any normal workflow step or
scheduled collector may produce a run-scoped, evidence-backed measurement;
Pulse does not depend on which step or route produced it. Target architecture:

```
ordinary workflow steps (producers)
        ↓
run-scoped database outputs (source of truth, stays in place)
        ↓
Pulse / reports / Goal Advisor read producer outputs directly;
producers call record_goal_observations only when a value should
enter the generic goal-progress history
```

Final review rejected the earlier mandatory-migration design (an earlier draft
of contract 1.0.43:
a `measurement-router` → `measure-outcomes` route plus a `workflow_metrics`
table on every workflow). It imposed topology most workflows don't need,
referenced the nonexistent `add_regular_step` tool, assumed table-scoped DB
authorization that doesn't exist (steps get database-wide read-write), assumed
immutable run identity that producers don't guarantee (`RUN_FOLDER` reuses
`iteration-0`), and described a metrics table no runtime code implements —
Pulse and Goal Advisor consume `workflow_goal_metrics` /
`pulse_goal_observations` through `get_goal_metrics`. That mandatory migration
was deleted. No replacement contract migration is required: producer-owned
measurement is ordinary workflow design guidance, not an unattended pre-run
rewrite. Legacy evaluation dependencies that genuinely need redesign are
handled explicitly in Builder instead of blocking every workflow schedule.

Replacement contract (flexible placement, no mandated topology):

- Reuse an existing step's DB output when it already measures the outcome.
- Add `record_goal_observations` to that producer only when the value should
  enter Pulse's generic goal-progress history.
- Put route-specific measurements in existing route-local steps.
- Use a shared convergence step only when multiple routes genuinely require
  the same calculation.
- Add a dedicated measurement step only when no existing step can safely
  own it.
- Make no topology change when existing measurement is already sufficient.
- Flag ambiguous evals for manual migration rather than inventing steps or
  routes.
- Retain old evaluation artifacts as read-only history unless a separate
  cleanup migration is approved.

What is still lost (no replacement): re-grading an old run after completion,
grading without modifying or influencing the run, hiding learnings from the
grader, keeping evaluation cost/failures separate from execution, and comparing
multiple evaluator versions against the same archived output. Full removal is
only correct if none of these are product requirements.

## Phase 0 — Decisions and Deprecation (Do First)

All compatibility decisions are made here, BEFORE any deletion. Deleting tools
first would strand older workspace upgrades and saved schedules that still
invoke `run_full_evaluation`.

1. Contract migrations (`cmd/server/workflow_version_upgrades.go`): per
   migration that invokes eval tools, decide guard (skip when eval is gone)
   or retire (if every supported workspace is past that contract version).
2. Saved schedules: rewrite schedule instructions that assume an eval pass
   (e.g. Mututal-Fund "Weekly Saturday Portfolio Sync" sequences steps
   "including its eval pass"); confirm no enabled schedule depends on
   `run_full_evaluation` output.
3. Workspace data: leave `evaluation/` dirs, `evaluation_report.json` files,
   `costs/evaluation/` ledgers, and `eval_results` rows inert on disk, or
   ship a one-shot cleanup migration. Either way, decide before deleting the
   code that reads them.
4. Backup scopes: drop `evaluation` / `evaluation-plan` from the trading and
   check-form backup/sync folder lists (or accept syncing stale dirs).
5. Deprecate at runtime: disable automatic eval by default and observe for one
   or two release cycles to surface hidden consumers before deleting anything.

Deprecation mechanics — `DisableEval` is a plain `bool`
(`controller_types.go`), and omitted tool args decode to `false`
(`planning_exports.go`), so "default disabled with explicit opt-in" cannot be
expressed with the current flag: omitted and explicit-false are
indistinguishable. Pick one before implementing:

- (a) Add `EnableEval *bool` (`nil` = default disabled during deprecation,
  explicit `true` opts in), or
- (b) Keep `DisableEval` but explicitly default it to `true` at every entry
  point (`run_full_workflow`, single-step execute paths, scheduler, webhook).

Also hide eval mode in the frontend (`workflowMode: 'eval'`, eval canvas,
`EvaluationPopup`); leave components in place until Phase 5.

## Phase 1 — Write the Flexible Measurement Guidance

Guidance only; no topology or contract migration is mandated. Must complete
before Phases 2–5 delete what it replaces.

1. Per-legacy-eval placement follows the Direction rules: reuse a producer's
   DB output, add `record_goal_observations` only for Pulse history,
   route-localize, converge only when shared, dedicate a step only when no
   producer can own it, otherwise change nothing, and flag ambiguity for
   manual migration.
2. Repoint Goal Advisor and report/cost guidance from eval reports /
   `eval_results` to producer outputs and `get_goal_metrics`.
3. Write the measurement guidance (replacement for `evaluation-plan.md`):
   `guidance/templates/system/measurement-plan.md` — placement rules, run
   scope and evidence, and legacy artifacts as read-only history.
4. Do not add a contract migration for this retirement. A universal pre-run
   agent turn cannot safely infer whether legacy measurements remain relevant
   and must not block unrelated workflow execution. Use Builder for an
   explicit, operator-visible redesign when a workflow still depends on a
   retired evaluation artifact.

## Phase 2 — Delete Eval-Only Backend Files

Delete wholesale (no callers outside the eval subsystem except the entry
points removed in Phase 3):

- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/evaluation_execution.go`
  (`ExecuteEvaluationOnly`, `MaybeRunAutoEvaluation`, report phase)
- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/evaluation_plan_tool.go`
  (`add_evaluation_step`, `update_evaluation_plan`, `delete_evaluation_step`)
- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/evaluation_helpers.go`
  (`validate_evaluation_plan` registration)
- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/evaluation_types.go`
- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/eval_results_storage.go`
- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/evaluation_score_storage.go`
- `agent_go/cmd/server/evaluation_score_storage.go`
- Eval-only tests: `evaluation_output_content_test.go`,
  `evaluation_plan_tool_test.go`, `evaluation_report_enrichment_test.go`,
  `evaluation_run_metadata_order_test.go`, `evaluation_skipped_sentinel_test.go`,
  `eval_results_storage_test.go`, `evaluation_score_storage_test.go`
  (both dirs).

## Phase 3 — Remove Engine Integration

- `controller_batch_execution.go`: delete the auto-eval block after group
  completion (keep `finalizeRunMetadata`, which is needed regardless).
- `controller_types.go`: delete the `DisableEval` option (or the `EnableEval`
  replacement from Phase 0, once nothing reads it).
- `webhook_step.go`: delete the `DisableEval = true` line (standalone steps
  simply never had eval).
- Delete every `isEvaluationMode` branch (~20 files). Highest-density files:
  `controller_execution.go` (10), `interactive_workshop_manager.go` (7),
  `controller_agent_factory.go` (6), `controller_agent.go` (5),
  `controller_agent_delegation.go` (4), `controller_workshop.go` (3),
  `controller.go` (3, incl. the field itself), `step_config.go` (2, collapse to
  `planning/`), `planning_exports.go` (2), plus single branches in
  `code_layout.go`, `controller_scripted.go`, `plan_snapshot.go`,
  `prompt_health.go`, `reflection_turn_run.go`, `run_provenance.go`,
  `workflow_continuation_recovery.go`, `workshop_retry_recovery.go`, and
  `cmd/server/workflow_phase_tools.go`.
- `controller_agent_factory.go`: delete `TARGET_RUN_PATH` read-path grants and
  the eval medium-tier default.
- `controller_execution.go`: delete `TARGET_RUN_PATH` prompt injection and the
  `IsEvaluationMode` template var.
- `controller_learning_helpers.go`: delete the `isEvalMode` parameter from
  `canReadLearnings` / `canWriteLearnings` /
  `resolveExecutionLearningsAccess` / `shouldDirectWriteLearnings` (keep the
  routing-step exclusions).
- `controller_agent_factory.go`: `resolveEffectiveDBAccess` already ignores its
  eval parameters — simplify its signature.
- `agent_go/pkg/workflowtypes/types.go`: delete `WorkflowStatusEvalExecution`
  and `WorkflowStatusEvalBuilder`.
- `agent_go/pkg/orchestrator/types/workflow_orchestrator.go`: delete the
  `ExecuteEvaluationOnly` call site.
- `planning_exports.go`: delete `RegisterRunFullEvaluationTool` and the eval
  controller call site.
- `planning_agent.go`: delete `add_evaluation_step` registration.
- `schedule_collision_guard.go`: delete eval tool entries.

## Phase 4 — Server APIs and Accounting

- `cmd/server/workflow_review_data.go`: delete the `evaluations` section
  (`EvaluationReport*` structs, `CostScopeEvaluation` reads,
  `evaluation/runs` timing load, `loadWorkflowEvaluationReports`).
- Routes: delete `/workflow/pulse-eval-results` (`server.go`, handler
  `handleGetPulseEvalResults` in `pulse_worklist.go`) and
  `/workflow/evaluation-reports` (`server.go`, handler
  `handleGetEvaluationReports` in `workflow.go`).
- `cmd/server/workspace_state.go`: delete `eval_data` from overview responses.
- `cmd/server/external_tools.go`: drop `evaluation/*` from
  `externalPlanPaths`.
- `cmd/server/workflow_backup.go`: drop `evaluation/evaluation_plan.json`.
- `cmd/server/workflow_manifest.go`, `report_preview_metrics*`,
  `slack_retention.go`, `webhook_retention.go`: delete eval references.
- Cost ledger/observer: delete `CostScopeEvaluation` and `costs/evaluation`
  handling.
- `cmd/server/workflow_phase_tools.go`: delete the eval-session registration.
- `cmd/server/toolset_invariant_test.go`: drop the five eval tools from the
  expected set.
- `cmd/server/workflow_version_upgrades.go`: execute the Phase 0 decision
  (guard or retire each eval-invoking migration).
- `schemas/auto-improvement.schema.json`: remove the deprecated `eval_step`
  source, leaving `measurement` and `telemetry`.

## Phase 5 — Frontend

Delete:

- `frontend/src/components/workflow/hooks/useEvaluationPlanData.ts`
- `frontend/src/components/workflow/hooks/useEvaluationPlanToFlow.ts`
- `frontend/src/components/workflow/nodes/EvaluationNode.tsx`
- `frontend/src/components/workflow/nodes/CompactEvaluationNodes.tsx`
- `frontend/src/components/workflow/EvaluationPopup.tsx`
- `frontend/src/components/workflow/PulseEvalSummary.tsx`
- `frontend/src/components/workflow/canvas/evaluationLayout.ts`
  (+ test)
- `frontend/src/components/workflow/canvas/routeEvaluations.ts`
  (+ test) — despite the name this is eval-only: it imports
  `EvaluationStep` and implements `applies_to_routes` gating. Keeping it
  after deleting `EvaluationStep` breaks compilation.
- `frontend/src/utils/evaluationReport.ts` (+ test)

Scrub (keep ordinary routing logic, delete eval branches):

- `canvas/routeTrace.ts` (imports `evaluationMatchesRoute`; handles
  `isEvaluationStep` / `evaluation-group` nodes)
- `canvas/triggerLayout.ts` (imports `EvaluationStep`; handles eval nodes)
- `canvas/WorkflowCanvas.tsx`, `canvas/WorkspaceViewHost.tsx`,
  `canvas/workspaceViewData.ts`, `hooks/usePlanToFlow.ts`,
  `nodes/RoutingStepNode.tsx`, `nodes/StepNode.tsx`, `nodes/index.ts`,
  `utils/stepConfigMatching.ts`
- `components/WorkflowsOverviewPage.tsx`: remove `evalData` state,
  `EvaluationPopup` usage, `onOpenEval` handlers.
- `components/workflow/ReportViewer.tsx`: remove pulse-eval-results consumption.
- `services/api.ts`: remove `getPulseEvalResults`, `getEvaluationReports`,
  `eval_data` handling.
- `services/api-types.ts`: remove `EvaluationPlan`, `EvaluationStep`,
  `EvaluationReportsResponse`, and related report types.
- `stores/useWorkflowStore.ts`: remove `'eval'` from `workflowMode`,
  `evaluationPlan` state, `loadEvaluationPlan`.
- Keep `routeTrace.ts` / `triggerLayout.ts` route-tracing logic and
  `routeEvaluations`-free routing display — only the eval-gating code goes.
- Rebuild frontend and delete the stale
  `agent_go/static/assets/PulseEvalSummary-*.js` bundle.

## Phase 6 — Guidance, Prompts, Docs

- Delete: `agent_go/cmd/server/guidance/templates/system/evaluation-plan.md`,
  `agent_go/cmd/server/guidance/templates/improve/improve-evaluation.md`.
- Scrub eval references in: `report/improve-report.md`,
  `review/review-artifact-drift.md`, `system/file-layout.md`,
  `system/plan-drift-review.md`, `system/strategy-auditor.md`,
  `system/workshop-mode-flow.md`, `system/workspace-views.md`,
  `guidance/guidance.go`, `cmd/server/instructions.go`
  (incl. Goal Advisor prompt, `evaluation/runs/` layout,
  `run_retention_count` wording), and
  `interactive_workshop_manager.go` prompts referencing eval plans/reports.
  Repoint each to the Phase 1 measurement guidance.
- Docs: delete `docs/workflow/evaluation_system.md`; scrub eval mentions in the
  remaining `docs/workflow/*.md` (about 20 files reference it).
- This file (`docs/workflow/eval_removal_plan.md`) should be deleted once the
  removal is complete.

## Phase 7 — Existing Workspaces and Data

Execute the Phase 0 decision: leave `evaluation/` dirs,
`evaluation_report.json` files, `costs/evaluation/` ledgers, and `eval_results`
rows inert, or run the one-shot cleanup migration. Locally affected: 7
workflows with live eval plans (trading has 18 steps), 122
`evaluation_report.json` files (~165 MB), `eval_results` rows in 6 DBs, and
eval-created domain tables (HDFC `eval_behavior_audit` / `eval_login_download`;
trading `eval_profile_integrity` / `eval_runtime_duration` /
`eval_trace_integrity` / `eval_sc`). Schedules that previously got an automatic
eval pass keep outcome tracking through their producers' existing outputs
(plus `record_goal_observations` where Pulse history is wanted); no new step
or route is required.

## Phase 8 — Tests

- ~46 Go test files mention evaluation. Delete the eval-only tests listed in
  Phase 2; update the rest (toolset invariants, workshop registration, plan
  snapshot/changelog, execution-only, webhook, scheduler, report metrics
  tests) to drop eval fixtures and assertions.
- Frontend: delete `evaluationLayout.test.ts`, `evaluationReport.test.ts`,
  `routeEvaluations.test.ts`, `RouteEvaluations.test.tsx`,
  `routeTrace.test.ts` eval cases, `triggerLayout.test.ts` eval cases; update
  store/canvas tests.

## Verification Gates

1. Module-scoped builds and tests pass (the repo root holds only `go.work`;
   there are no root packages, so bare `go build ./...` fails):
   - `(cd agent_go && go build ./... && go test ./...)`
   - `(cd workspace && go build ./... && go test ./...)`
2. Frontend `tsc` + unit tests pass; production bundle contains no
   `PulseEvalSummary` chunk.
3. Grep gates. These strings must return zero in non-test Go and
   non-test `frontend/src`:
   - `evaluation_plan`, `isEvaluationMode`, `IsEvaluationMode`,
     `TARGET_RUN_PATH`, `ExecuteEvaluationOnly`, `MaybeRunAutoEvaluation`
   - `run_full_evaluation`, `validate_evaluation_plan`,
     `add_evaluation_step`, `update_evaluation_plan`,
     `delete_evaluation_step`
   - `CostScopeEvaluation`, `EvaluationStep`, `EvaluationPlan`,
     `EvaluationReportsResponse`, `EvalExecution`
   - `pulse-eval-results`, `evaluation-reports`, `eval_data`, `EvalData`,
     `getEvaluationReports`, `getPulseEvalResults`, `applies_to_routes`,
     `routeEvaluations`, `evaluationMatchesRoute`
   - `measurement-router`, `measure-outcomes`, `workflow_metrics`,
     `upgrade-measurement-route` (the rejected mandatory topology)
   - `eval_step`, `costs/evaluation`, `evaluation/runs`,
     `evaluation_report`, `eval_results`, `eval_route_scores`,
     `improve-evaluation`, `evaluation-plan`
   In guidance templates, prompts, schemas, tests, and active docs, the same
   strings survive only on lines that also say legacy/retired/read-only
   history (or in assertions pinning retired behavior). Explicitly reviewed
   exceptions: this plan; dated historical records under `docs/bugs/` and
   `docs/audits/`; the `pulse_v2` proposal docs and the retired design
   section in `persistent_stores_design.md`; `registry.test.ts` (asserts the
   commands are retired); `external_tools_test.go` (asserts the retired plan
   path stays write-protected); `declared_execution_mode_strip_test.go`
   (asserts retired files stay byte-identical); `slack_review_test.go`
   (asserts retention leaves legacy evidence inert);
   `ManualWorkspaceRefresh.test.ts` (asserts the eval refresh is absent);
   the upgrade-chain guard test and the measurement-guidance render test
   (assert the rejected topology is absent); `reflection_turn_test.go`
   (asserts the retired table stays platform-owned);
   `isEvaluationAgentEvent` / `inferTrackedExecutionKind` (classify
   historical records only); `isPlatformOwnedTable` /
   `planDriftReservedTables` (keep retired eval tables out of step reach).
4. Allowed survivors: `routing-evaluation.json`, `RoutingEvaluatedEvent`,
   `routeTrace.ts` / `triggerLayout.ts` routing logic,
   `RoutingEvaluationCounts`, cron "evaluation" wording in `scheduler.go`,
   `ToolResponseEvaluation` (external mcpagent context-editing type —
   later removed 2026-09-22 with the whole context-editing feature),
   pulse-review `evaluation` argument aliases (legacy compat).
5. End-to-end: full workflow run, scheduled run, and webhook run complete with
   no eval phase; review/costs/timing pages render without an eval section;
   Goal Advisor and reports read producer outputs and goal observations with
   no mandated measurement route, step, or table.
