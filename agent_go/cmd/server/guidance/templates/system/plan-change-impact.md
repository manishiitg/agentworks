---
name: "plan-change-impact"
description: "Plan-change impact analysis: trace and reconcile the blast radius across downstream steps, measurement, reports, db, learnings, and KB. Load before treating a plan change as done."
external: true
external_note: "External mapping: use help action=topic for listed reference topics, files action=search/read for source evidence, and query_database for read-only managed database inspection. Plan/configuration edits and changelog dispositions remain with builder action=chat when available. Do not call an internal operation merely because this reference names it, and never edit changelog files directly."
---
# Plan-change impact analysis

A workflow is a web of **implicit dependencies**: a step's output feeds downstream steps, evals score it, the report dashboard queries its data, db stores it, and learnings/KB describe its behavior. Change a step and those silently rot — a downstream step reads a field you renamed, a report query returns nothing, an eval scores the wrong thing, a learning teaches the old behavior.

**Whenever you change a plan** — add / remove / reorder a step, or change a step's output contract, behavior, or description — you are not done until you have checked and reconciled the blast radius. This applies whether the change came from an active Workshop edit, an approved owner decision, or a repair requested by AgentWorks.

## Interactive edits and test runs

Do one combined compatibility check in the current agent after the related edits for a repair are complete, before the targeted test. Do not launch a separate drift reviewer for each edit or test retry.

- **Small internal or cosmetic edits:** when outputs, DB writes, routing, and behavior remain compatible, confirm that briefly and proceed to the test. Judge the actual change, not just the field name: description/message edits can change behavior. Record unaffected surfaces as `not_applicable` with a short reason; no broad workspace audit is needed.
- **Output, DB, routing, or behavior changes:** trace the affected dependencies using the procedure below and reconcile them before testing. Cover downstream steps, reports, evals, validation, and saved guidance only where the change reaches them. A known broken dependency needed by the test must be fixed first.
- **Full drift audit:** reserve `run-plan-drift` for scheduled AgentWorks or an explicit user request. A targeted test request does not request a full audit. The `drift_review.needs_review` flag tracks stale evidence for AgentWorks; it does not block interactive testing or require an immediate background reviewer.

Combine all changelog entries from the same repair into that one check and record dispositions for each inspected entry. Reuse evidence while the relevant artifacts remain unchanged. If a test needs another edit, recheck only its new impact; if no artifacts changed, a retry needs no new dependency review. Never mark a full drift audit complete based only on this targeted check.

**Stop when the changed step and its affected consumers are compatible.** Do not scan unrelated routes, drain old changelog/backlog entries, start a reviewer, or expand a small repair into a workflow redesign. An unrelated finding belongs in AgentWorks's backlog and must not delay the targeted test. Missing audit receipts alone are not a test blocker; a concrete incompatible dependency needed by the test is.

## 1. Name the change surface
First, pin down exactly what other artifacts key off — the **surface** of the change:
- the **step id**,
- the **output file / artifact** it produces (path + the JSON fields / shape),
- the **db tables / columns** it writes,
- its **topic / behavior** (what learnings & KB describe).

If you renamed a field, removed an output, changed a file path, changed what's written to db, or changed what the step does — that's the surface to trace. A change with no surface change (see Scope note) has no blast radius.

## 2. Trace affected dimensions — search, don't guess
Start with the changed step and its declared consumers. Search the relevant artifacts for actual changed references (output file/field, db table/column, route, behavior). Expand only when a concrete reference shows another affected consumer; the list below is a map of possible dependencies, not a mandatory full-workspace sweep. Skip dimensions that the change does not affect.

- **Downstream steps** — search `planning/plan.json` and step descriptions for the step id / output file / changed field. A later step that consumes a field you changed must have its expectation **and** its `validation_schema` updated. (`read_skill(skills=[{"name":"builder-reference","path":"references/step-config.md"}])`)
- **Measurement** — search the producing steps' stored-measurement queries and goal observations for the step id / output path. A query reading the changed output must be updated so it still resolves and scores the right thing. (`read_skill(skills=[{"name":"builder-reference","path":"references/measurement-plan.md"}])`)
- **Report dashboard** — search `db/reports/index.html` and its `window.report.query` SQL for the db tables/columns and output fields. A query that reads changed data must be fixed. (`read_skill(skills=[{"name":"builder-reference","path":"references/reporting-policy.md"}])`)
- **db** — read `db/README.md` (it already lists each table's writers + shape). If this step writes db and the shape changed, update the schema, the README contract, and any readers. (`read_skill(skills=[{"name":"builder-reference","path":"references/stores.md"}])`)
- **Skills** — inspect the selected workflow skills and references touched by the changed behavior. Builder correct stale methods; steps consume them. Keep legacy learning files as migration history. Clear `lock_code` only when saved code must change under the existing code-lock rule.
- **KB** — search `knowledgebase/notes/` for the step's topic. Notes describing the old behavior must be updated or flagged.

It is tractable **because the contracts already exist** — `db/README.md` lists writers and shape, report HTML declares its queries, `get_goal_metrics` returns configured metric scope and observation history, and the plan holds the step handoffs. You are cross-linking what is already written, not inventing a dependency graph.

## 3. Reconcile or flag
For each affected dependent:
- **Fix it now** if the fix is clear and contained — update the consumer's schema, fix the report query, repair the eval source, update the db contract, or correct stale skill selections. (Back up first, or rely on the surrounding pass's backup.)
- **Flag an open finding** if the reconciliation needs judgment — a downstream step needs real redesign, a metric definition is now ambiguous. Record it through typed AgentWorks tools so it isn't lost.

**Never leave a plan change with a silently broken dependent.**

## 4. Record structured dependency closure
When closing inspected changelog entries, call `mark_changelog_artifact_reviewed` with one evidence-backed disposition for every required surface: `downstream_steps`, `validation`, `evaluation`, `reporting`, `database`, and `learnings_and_knowledge`. Use only `updated`, `already_compatible`, `not_applicable`, `blocked`, or `broken`. A cosmetic change can honestly use `not_applicable`, with a short reason explaining why the surface is unaffected; do not run six audits just to populate these fields. Every `blocked` or `broken` surface must include the durable AgentWorks `issue_ids` that own the unresolved repair. If broader evidence is unavailable, leave that entry open for AgentWorks instead of expanding the interactive check or inventing evidence. A bare `artifact_review.done=true` is no longer sufficient. Recording audit closure is not a prerequisite for the targeted test once its affected dependencies are compatible.

When the change is intended to affect a goal or metric, state that expectation and
the future comparison boundary in the canonical issue or concise review result.
Workflow steps, evaluation steps and collectors own the actual measurements. Do not
create a parallel AgentWorks impact record, and do not claim the change worked until a
later AgentWorks check has comparable outcome evidence.

## The changelog is your work-list — keep it lean
Every plan-mod tool call is auto-written to `planning/changelog/changelog-*.json` (tool, `reason`, affected step ids, old/new values). Treat entries without `artifact_review.done=true` as the **ledger of changes whose blast radius may not be reconciled yet** — your work-list. When you reconcile a change (steps 1–3), record its concrete compatibility result.

Do **not** edit or delete changelog files directly. The current agent records evidence-backed findings/dispositions and marks only inspected entries through `mark_changelog_artifact_reviewed`. AgentWorks uses those saved records to avoid repeating unchanged checks.

The full `run-plan-drift` audit is a backstop inside the same AgentWorks or Builder conversation when due or explicitly requested. Inspect changed steps and affected dependent artifacts directly, asking Builder for repairs as needed. It is not a prerequisite for a normal test run.

## Scope note
The discipline **scales to the change**. A change that is purely internal to a step (same output contract, same db writes, same described behavior) has no blast radius — confirm that quickly and move on. A renamed output field touches downstream + report + eval + db; a reworded description that keeps the same contract may only touch learnings. Trace what the surface actually reaches, not a fixed checklist for its own sake.
