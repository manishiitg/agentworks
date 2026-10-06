[← goals / pulse / fixer](index.md)

# PLAT-556 — The improvement loop never closes: fixes accrete in step descriptions and nothing consolidates

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | goals |
| Area | pulse/fixer |
| Summary | in progress: Upwork pilot and decision-6 data cleanup applied 2026-10-06; decisions 1-5, learning-access ownership and the success metric on main (not deployed); deployed observation, decision 6 remainder and Brain import open. |

| Coordination | Value |
|---|---|
| State | in progress: decisions 1-5, learning-access ownership and the success metric built on main 2026-10-06 (not deployed); Brain option B import prepared, not run |
| Priority | P1 |
| Date | 2026-10-06 |
| Owner | pulse-governance (Pulse edited from this work with the owner's go; the dedicated Pulse session was not running) |

## Source

Owner, 2026-10-06: "this is an issue we have been struggling with for very long" and "is there some flaw in the overall design we are missing".

## Evidence and design

See [workflow knowledge layers and the improvement loop](../../../../../design/workflow_knowledge_layers.md). In short (Upwork): 334 description edits grew descriptions by 81,855 characters (builder chat +44k, Pulse +38k); Architecture review ran once (2026-09-28) and is since skipped as `plan_drift_review:exclusive_prerequisite`; every Architecture focus including `prompt_design` shows 0 reviews; the size nudge (2026-09-17) is ignored.

Earlier attempts each fixed one piece: PLAT-049 (platform mechanics), PLAT-258 (Plan Drift module), PLAT-285/290 (prompt-health tool), PLAT-303 (exception-driven Technical), PLAT-305 (separate QA/Architecture/Strategy; open: no observed autonomous improvement), PLAT-329 (plan tools), the size nudge, PLAT-555 (layout).

## Done on Upwork (2026-10-06, local workflow data; backup `/private/tmp/claude-501/-Users-mipl-ai-work/baa0f19b-b6e6-40e4-a5c8-e57597e2e0ad/scratchpad/upwork-backup-20261006-101124/`)

- Pilot `bid-pick-job` (see PLAT-555).
- Decision 6, KB stops deferring to descriptions: `knowledgebase/notes/job-scoring-criteria.md` named the `search-find-and-shortlist` description as the authority for the scoring rubric and gave a stale maximum of 19. The live rubric (coarse max8, semantic max18) moved verbatim into the note's "Live scoring rubric" section; the step's description and scoring item now name the note; floors, top15/top3 retention and the fit<5 rejection stay in the description. `drift_review.needs_review` set. Changelog entry in `changelog-2026-10-06-04-44-44.json`.
- Decision 6, per-step learnings folders: 12 metadata-only folders of steps no longer in the plan moved to `archive/knowledge-layers-2026-10-06/learnings/` (bid-draft-letter, bid-read-brief, post-bid-exit, profile-report, profile-suggest, read-flow-mode, search-detail-fetch, search-enrich-shortlist, search-pick-keywords, search-score-jobs, search-scrape-jobs, search-semantic-score). Judgment: the 11 metadata-only folders of live steps stay, because the runtime writes run counters, the execution tier and description-hash runs into `learnings/<step>/.learning_metadata.json` and would recreate them; they are runtime state, not a knowledge place.
- Decision 6, `graph.json`: frozen since 2026-04-20, read by no Go or frontend code; it and its frozen `knowledgebase/index.json` moved to `archive/knowledge-layers-2026-10-06/knowledgebase/`; 30 `[x](../graph.json#…)` links in 8 notes became plain text. `knowledgebase/_freshness.json` stays: it is the code-owned freshness ledger Pulse `knowledgebase_health` reads (stale since 2026-08-07 because no step confirms the KB; that staleness is the change-2 trigger, not a reason to delete it).

## Measurements (Upwork, same script before and after)

| Measure | Before | After |
|---|---|---|
| Description characters, 28 steps | 153,519 | 142,982 |
| Descriptions + items | 163,011 | 152,257 |
| Largest description | `improve-analyze-report` 26,330 | unchanged |
| Steps over budget (3x median 1,550, floor 12,000) | 4 (bid-pick-job, profile-suggest-report, improve-analyze-report, toptal-scan-draft) | 3 (bid-pick-job no longer) |
| Dated-text hits in descriptions (ISO dates, "measured", "observed on", run ids) | 68 | 68 (bid-pick-job had none) |
| `bid-pick-job` description / items | 13,103 / 1,562 | 3,085 / 1,286 |

## Done on main: decision 1, guaranteed delivery (2026-10-06, not deployed)

- At step execution the description's `## Inputs` and `## Guides` sections are parsed (`step_based_workflow/description_references.go`) for workflow paths under `learnings/`, `knowledgebase/`, `code/shared/`, `soul/`, `db/README.md` (a leading `Workflow/<name>/` or docs-root prefix is tolerated) and `brain:<folder>/<note>`.
- Each named path that exists inside the workflow (symlinks resolved; one leading out counts as missing) is added to the step's Folder Guard read paths, whatever `learnings_access` / `knowledgebase_access` says, never to write paths. Hooked into every builder: regular/scripted (`setupExecutionFolderGuard` call sites and `createExecutionOnlyAgent`), message_sequence (`executeMessageSequenceUserMessage`) and todo_task (`setupOrchestratorFolderGuard`).
- The step's system prompt (template and authored prompts) ends with a "Referenced guides" section, built once per sequence: files attached in the order named up to 4,000 characters each and 12,000 in total; larger or over-budget files and folders are listed with path, size and "read it"; a missing path is reported. Every undelivered reference logs `[REFERENCED_GUIDES]` as a warning.
- Brain notes are read by `knowledgebaseReadReferencedNote` (cmd/server) as the run's person (claims in the run context), under that person's folder roles and the project's `brain_access` (off refuses; read/write read everything the person may read; folders reads only bound folders), always read-only. An unreadable note is reported in the prompt and logged.
- `add_*_step`, `update_message_sequence_step` and `update_todo_task_step` responses add a one-line warning when the saved description names a workflow path that does not exist. The step-description guide says naming a path delivers it.
- Judgment calls: a project cut over to shared Brain (`knowledgebase_mode=shared`) does not get local `knowledgebase/` paths back by naming them (the prompt says so); a bare root (`learnings/`) is not a reference; globs and `{{VAR}}` placeholders are skipped; Brain note names without `.md` get it appended; directories are granted but not attached; the edit warning does not check Brain notes (that needs the run's person).
- Evidence: a real in-process workflow run (message_sequence step, Claude Code CLI, isolated workspace API) with `learnings_access: none` saved a system prompt whose Allowed READ held exactly the two named files (not `SKILL.md`), with both attached and the missing KB note reported. The CLI's bridge calls were refused with 401 because an out-of-server harness cannot mint session bridge tokens, so live enforcement of the read grant through the bridge was not observed. Tests: `TestDescriptionNamedGuidesAreReadableAndAttached`, `TestReferencedBrainNoteRespectsPersonAndProjectAccess` (real Brain service: reader reads, non-member refused, `brain_access=off` refused).
- Left for decision 1: observe one deployed run (Upwork `bid-pick-job` names its guides) reading a named guide through the bridge with learnings access off; deploy needs the owner's go.

## Built on main (2026-10-06, Pulse side; not deployed)

**Decision 2, budgets as triggers.** `get_plan_prompt_health` now reports per step a `budget` record: size against the median of the plan's other steps with `over_budget` above 3x that median (floor 12,000, the same constants as the edit-time OVER BUDGET nudge), dated/incident text count with samples (ISO dates, "measured", "observed on", run-id values, UUIDs), characters inside 300+-character spans repeated verbatim in another step (Rabin-Karp over whitespace-collapsed text), and missing layout sections; plan totals and `consolidation_due_steps`. `CollectPromptBudgetDue` (step_based_workflow/prompt_budget.go) reads plan.json from disk, like `CollectPlanDriftCandidates`, and is shown to Gate as `architecture_budget_candidates`. `record_pulse_worklist` makes `architecture_review` due with focus `prompt_design` (and `learning_quality`, below) while that state's fingerprint has not been reviewed (cmd/server/pulse_prompt_budget.go). Never a gate on runs or edits.
- Judgment: "no layout" triggers only when Goal, Output or Done when is missing; Inputs, Rules and Guides can be honestly empty and are only reported. Layout is checked for message_sequence and orchestrator steps; regular steps are scripted by definition (PLAT-287), so only their dated text counts.
- Judgment: a state Architecture already completed (done/changed) is not forced again; any edit to a flagged step changes the fingerprint and makes it due again. Stale KB notes are not measured yet.

**Decision 3, consolidator.** New plan tools `check_plan_no_loss` (step_id, proposed_description, proposed_items, dropped_history) and `restore_step_from_changelog` (change_id, step_id, reason). The check extracts identifiers with underscores (incl. VAR_ names), numbers and ranges, thresholds (symbolic and "at least/at most/no more than/up to"), file paths and quoted literals from the old description and items, and searches the new description, items and every workflow file the new description names under Inputs/Guides (learnings/, knowledgebase/, code/shared/, soul/, db/README.md); pass only when nothing is missing. Dates inside dated sentences may drop; any other history-only token must be acknowledged in `dropped_history`, and a rule token cannot be. Restore reapplies the changelog entry's recorded old description/items through `update_message_sequence_step` (the changelog stores hashes as before_ref/after_ref and the old values in `changes[].old_value`; there is no stored snapshot to restore from). architecture-review.md: apply a pure text move only after the check passes, run the step once, keep it only when validation passes, else restore and propose; record each in `record_pulse_result(consolidations=[...])`. Behaviour, rule, output and topology changes stay owner proposals.
- Judgment: slash lists ("closed/unavailable/applied") are prose, not paths, and are not required tokens; the identifiers inside them still are. `brain:` references are listed but not read (no project Brain access in this tool).

**Decision 4, scheduling.** Plan Drift still defers Architecture, but only up to two passes in a row (`architecture_drift_deferrals:N` in the worklist evidence); on the third, or immediately when every budget-flagged step is outside Drift's candidate set, Architecture stays due in the same pass with `plan_drift_review:architecture_scoped` and `architecture_scope_excludes:<Drift's steps>`; the scheduler dispatches it after Drift and its contract says not to touch those steps. Technical and Goal Work are unchanged.

**Decision 5, routing.** pulse-fixer-practices.md (step 4), pulse-review-fixer.md, technical-review.md, workflow-chat.md (builder chat fixes) and workshop-mode-flow.md: technique to a skill reference named under Guides, rule or decision to the knowledge layer or Rules, evidence to the change reason; moving the text a repair touches is part of the smallest complete repair.

**Learning access (owner addition, 2026-10-06).** Architecture (`learning_quality`) alone decides `learnings_access`; Plan Drift and the Fixer only flag. A read-write step with 5+ successful runs on one description hash and no new learning in the latest 5 detections (`.learning_metadata.json`) makes Architecture due with `learning_quality`; it may set read-write to read itself with a reason (reversible); read to read-write still needs a learning_objective and a decision. PLAT-263's contract sentence updated.

**Success metric.** `pulse_prompt_budget_metrics` (module-state DB) gets one row per Pulse pass at worklist time: total and largest description, steps over budget, dated text, duplicated characters, steps without layout, due steps, settled learning steps, cumulative Architecture runs (done/changed audit rows), consolidations applied/kept/restored and validation before/after from `pulse_consolidation`. `get_pulse_state(view="module")` returns the latest 10 as `prompt_budget_metrics`.

**Evidence.** `TestCheckPlanNoLossUpworkPilot` (PLAT556_PILOT_DIR, data kept outside the repo): the reviewed bid-pick-job rewrite passes (119 tokens, 6 named guides resolved); the same rewrite without the $300 floor fails naming it; without `hard_floors_cleared` fails naming it. `TestPromptBudgetMakesArchitectureDueAndDriftDefersAtMostTwice` drives four real worklist passes (module DB, plan files on disk). Measured on the Upwork backup: median 2,893, 4 steps over budget, 68 dated-text hits, 14,515 duplicated characters, 15 agentic steps without the layout, 16 steps due for prompt_design.

## Left

1. Decision 1: observe one deployed run reading a named guide through the bridge (above). `check_plan_no_loss` has its own small Inputs/Guides parser (`NoLossNamedFiles`); it could reuse `description_references.go`.
2. Decisions 2-5: stale-KB-note budget trigger; deploy (owner's go per server) and watch the first Upwork passes: Architecture runs, consolidations kept vs restored, metric rows.
3. Success metric: validation "before" is the reviewer-reported latest validation; a windowed before/after rate from run history is not computed.
4. Decision 6: one ownership map; fold `context.md` and `rules.md` into the knowledge layer (Upwork data side otherwise done, above).
Plus Brain option B (shared/org facts in Brain, workflow-only facts local; no cutover for these projects), tracked with PLAT-538: Upwork import prepared there, not run.
5. Upwork: the remaining over-budget steps (`improve-analyze-report`, `profile-suggest-report`, `toptal-scan-draft`) and the 68 dated-text hits, through Architecture `prompt_design` (now triggered by the budgets), not in one sweep.

## Register notes

[PLAT-556](plat-556.md), P1, in progress: design accepted; Upwork pilot and cleanup applied; decisions 1-5, learning-access ownership and the success metric on main 2026-10-06 (not deployed); deployed observation, decision 6 remainder and Brain import open.
