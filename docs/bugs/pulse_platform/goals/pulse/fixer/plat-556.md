[← goals / pulse / fixer](index.md)

# PLAT-556 — The improvement loop never closes: fixes accrete in step descriptions and nothing consolidates

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | goals |
| Area | pulse/fixer |
| Summary | open: design accepted; Upwork data side of decision 6 and the bid-pick-job pilot applied 2026-10-06; decision 1 (delivery) on main, not deployed; budgets, consolidator, scheduling, routing and Brain import open. |

| Coordination | Value |
|---|---|
| State | open: design accepted (owner: "implement the full redesign"); Upwork data cleanup applied 2026-10-06; platform changes 1-5 in progress in other sessions; Brain option B import prepared, not run |
| Priority | P1 |
| Date | 2026-10-06 |
| Owner | pulse-governance (dedicated Pulse session; coordinate before editing Pulse code) |

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

## Done on main: decision 1, guaranteed delivery (2026-10-06, not deployed)

- At step execution the description's `## Inputs` and `## Guides` sections are parsed (`step_based_workflow/description_references.go`) for workflow paths under `learnings/`, `knowledgebase/`, `code/shared/`, `soul/`, `db/README.md` (a leading `Workflow/<name>/` or docs-root prefix is tolerated) and `brain:<folder>/<note>`.
- Each named path that exists inside the workflow (symlinks resolved; one leading out counts as missing) is added to the step's Folder Guard read paths, whatever `learnings_access` / `knowledgebase_access` says, never to write paths. Hooked into every builder: regular/scripted (`setupExecutionFolderGuard` call sites and `createExecutionOnlyAgent`), message_sequence (`executeMessageSequenceUserMessage`) and todo_task (`setupOrchestratorFolderGuard`).
- The step's system prompt (template and authored prompts) ends with a "Referenced guides" section, built once per sequence: files attached in the order named up to 4,000 characters each and 12,000 in total; larger or over-budget files and folders are listed with path, size and "read it"; a missing path is reported. Every undelivered reference logs `[REFERENCED_GUIDES]` as a warning.
- Brain notes are read by `knowledgebaseReadReferencedNote` (cmd/server) as the run's person (claims in the run context), under that person's folder roles and the project's `brain_access` (off refuses; read/write read everything the person may read; folders reads only bound folders), always read-only. An unreadable note is reported in the prompt and logged.
- `add_*_step`, `update_message_sequence_step` and `update_todo_task_step` responses add a one-line warning when the saved description names a workflow path that does not exist. The step-description guide says naming a path delivers it.
- Judgment calls: a project cut over to shared Brain (`knowledgebase_mode=shared`) does not get local `knowledgebase/` paths back by naming them (the prompt says so); a bare root (`learnings/`) is not a reference; globs and `{{VAR}}` placeholders are skipped; Brain note names without `.md` get it appended; directories are granted but not attached; the edit warning does not check Brain notes (that needs the run's person).
- Evidence: a real in-process workflow run (message_sequence step, Claude Code CLI, isolated workspace API) with `learnings_access: none` saved a system prompt whose Allowed READ held exactly the two named files (not `SKILL.md`), with both attached and the missing KB note reported. The CLI's bridge calls were refused with 401 because an out-of-server harness cannot mint session bridge tokens, so live enforcement of the read grant through the bridge was not observed. Tests: `TestDescriptionNamedGuidesAreReadableAndAttached`, `TestReferencedBrainNoteRespectsPersonAndProjectAccess` (real Brain service: reader reads, non-member refused, `brain_access=off` refused).
- Left for decision 1: observe one deployed run (Upwork `bid-pick-job` names its guides) reading a named guide through the bridge with learnings access off; deploy needs the owner's go.

## Measurements (Upwork, same script before and after)

| Measure | Before | After |
|---|---|---|
| Description characters, 28 steps | 153,519 | 142,982 |
| Descriptions + items | 163,011 | 152,257 |
| Largest description | `improve-analyze-report` 26,330 | unchanged |
| Steps over budget (3x median 1,550, floor 12,000) | 4 (bid-pick-job, profile-suggest-report, improve-analyze-report, toptal-scan-draft) | 3 (bid-pick-job no longer) |
| Dated-text hits in descriptions (ISO dates, "measured", "observed on", run ids) | 68 | 68 (bid-pick-job had none) |
| `bid-pick-job` description / items | 13,103 / 1,562 | 3,085 / 1,286 |

## Left (the six changes of the design note)

1. Guaranteed delivery: on main (above); deployed-run observation left.
2. Budgets as triggers (size vs plan median, dated text, duplication, stale KB) making consolidation due; never a gate.
3. Architecture may apply a consolidation under a no-loss check plus one comparison run.
4. Plan Drift must not starve Architecture; over-budget steps make Architecture due with `prompt_design` focus.
5. Repairs routed by type in fixer and builder-chat guidance.
6. One ownership map; fold `context.md` and `rules.md` into the knowledge layer (Upwork data side otherwise done, above).
Plus Brain option B (shared/org facts in Brain, workflow-only facts local; no cutover for these projects), tracked with PLAT-538: Upwork import prepared there, not run.
7. Upwork: the remaining over-budget steps (`improve-analyze-report`, `profile-suggest-report`, `toptal-scan-draft`) and the 68 dated-text hits, through Architecture `prompt_design` once changes 2-4 exist, not in one sweep.

## Register notes

[PLAT-556](plat-556.md), P1, open: design accepted (layers, Brain option B, Pulse roles, budgets, delivery); decision 1 delivery on main 2026-10-06, not deployed; changes 2-6 open.
