[← goals / plans-contracts](index.md)

# PLAT-561: Edit-time reference map: changes carried through to dependents

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | plans-contracts |
| Summary | Deterministic reference map of steps, evals, notes, soul.md and guides; plan and file edits list dependents, prompt health and Pulse carry open breaks; reports only. |

## What happened

From [PLAT-559](../pulse/general/plat-559.md): about 32% of a 50-issue sample of Upwork's Pulse issues were
changes not carried through to dependents: steps whose Inputs name removed producer steps, evals asserting
moved paths, KB notes and guides teaching retired steps and files. Plan Drift misses these: description-only
and non-plan edits do not flag it, and it checks only the changed step. Owner, 2026-10-06: "lets do it".

## Fix

A deterministic reference map, computed on read from the workflow on disk
(`agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/reference_map.go`). It reads plan steps
(ids, `context_output`, `context_dependencies`, the files their validation schemas declare, and the text of
descriptions, items and routes), `step_config.json` (`additional_read_paths`, entries without a step), the
evaluation plan (descriptions and `applies_to_routes`), KB notes, rules and context, `soul/soul.md`, and
learnings `_global` guides and per-step SKILL.md files. Retired step ids are every id a plan changelog entry
ever named that is no longer in the plan (parsed changelog files are cached by size and mtime).

Checks (all deterministic):

- **break**: a context dependency no step produces; one a step writes (validation_schema) but no
  `context_output` lists, on a scripted/agent step (it resolves to the consumer's own folder; the
  bid-verify "input file not found: job_brief.json" failure on Upwork was this); a step folder
  (`$VAR_TARGET_RUN_PATH/<id>/`, `../<id>/<file>`) that is not in the plan; a retired step id named anywhere;
  a workflow path (`code/`, `learnings/`, `knowledgebase/`, `soul/`, `db/`, `evaluation/`, `reports/`,
  `variables/`) that does not exist; a bare file in `## Inputs`/`## Guides` (same section parser as
  PLAT-556's referenced guides) that no step produces and no workflow file has; an eval's
  `applies_to_routes` naming a missing routing step or route; a missing `additional_read_paths` entry.
- **warn**: the unlisted dependency on a message_sequence step (it gets only the bare name); a file read
  from a step folder that the step does not declare.
- **info**: an output nothing reads; a step_config entry without a step.

Where it reports (never a gate, never blocks an edit or a run):

1. Plan edit responses (add/update/delete step, routes, `update_step_config`, `update_validation_schema`,
   `change_step_type`, restore) append, when the edit changed plan.json or step_config.json, the
   dependents of each edited step: consumers of its outputs (including outputs it had before the edit),
   steps, evals, notes and guides naming it or its files, and open breaks involving it, plus workflow
   totals. Wrapped once in the consolidated plan registrar, so Builder, Pulse and external clients get it.
2. Builder writes with `diff_patch_workspace_file` to a KB note, soul.md, the evaluation plan or a learnings
   markdown file inside the workflow append the breaks that file now carries
   (`agent_go/cmd/server/reference_map_notes.go`). Native CLI edits and shell writes are not hooked; they
   surface through 3.
3. `get_plan_prompt_health` returns the full `reference_map`; `get_pulse_state(view=module)` carries
   `reference_map` (counts plus the first 40 rows) with a note, and plan-drift-review.md tells Drift to treat
   breaks naming a due step as dependents to fix or file.

Evidence, a copy of the live Upwork workflow (2026-10-06, read-only source, copy in the session scratchpad):
75 breaks, 7 warnings, 5 info, built in 100-180 ms. Real ones include: eval-bid reads
`$VAR_TARGET_RUN_PATH/bid-approve/approval.json` (no such step); eval-bid, eval-search and eval-improve name
retired steps (bid-read-and-draft, search-semantic-score, improve-report); scripted bid-record,
bid-record-skip and bid-record-no-bid depend on `selected_job.json`, `draft.json`, `job_brief.json` and
`verify.json`, which bid-pick-job and bid-submit write but do not list in `context_output`; 11 steps'
`additional_read_paths` name `db/assets/market-study-access-restriction.json`, which does not exist;
KB notes (flow-bidding, upwork-positioning, job-scoring-criteria) and seven `_global` references
(selectors, behavioral-quirks, proposal-form, ...) still teach retired steps; flow-bidding.md names
`db/proposals.json` and `db/connects_ledger.json` from before the database. On the Upwork plan at git
6b06a5e (2026-08-08) the map reports search-save-jobs naming the removed search-semantic-score and
search-detail-fetch, the known QA issue. A simulated rename of search-find-and-shortlist's `context_output`
through the real update executor appended its five consumers, both evals, seven notes/guides and the new
breaks to the edit response; a rejected delete appended nothing.

Judgment calls: retired ids come from the changelog, not git or revisions (cheap, and every plan-mod is
logged); notes named like a change log or history are not checked for retired step ids (they name old steps
on purpose) but are checked for paths; a path without a file extension or trailing slash is treated as prose
("soul/plan"); `../<x>/` counts as a step folder only with a file after it; DB tables named in descriptions
are left to Plan Drift's existing `db_readme_contract` check rather than duplicated; Brain (`brain:`) notes
are not checked here; no due rule was added: breaks are evidence for Drift, and code due rules come with
PLAT-559's migration. One test (`reference_map_test.go`) pins the core cases.

## Left

- **AI pass (next):** business-rule contradictions need judgment: a KB note's rate ($50/hr) against soul.md
  ($25-35), guidance teaching retired behaviour in prose, evals asserting old values or lengths. A short
  model pass over the map's dependents of a change, reporting only.
- Make plan_drift_review due on new breaks once PLAT-559's code due rules land.
- Native CLI file edits and shell writes in Builder are not hooked (they show in prompt health and Pulse).
- Not deployed.
- Native CLI and shell writes are now seen: the Go-side flag in [PLAT-565](plat-565.md) recomputes the map when the workflow files change and makes Plan Drift due for new breaks.

## Checked on every local workflow (2026-10-06)

Run read-only on all 18 local workflows: 11 had no breaks. The rest showed two kinds of noise, now fixed: template
paths (`db/.../YYYY-MM-DD-x.json`) were reported as missing files, and missing `db/` or `reports/` files (which runs
create) counted as breaks. Template paths are skipped and runtime data paths are now warnings
(`missing_data_file`), so they never make Plan Drift due. Breaks after the fix: substack 33 to 4, build-in-public
18 to 5, linkedin 14 to 5, upwork 64 to 49 (15 now warnings). Real finds kept, for example websiteaeo's five steps
naming `knowledgebase/context/context.md`, which does not exist.

Still noisy: a note that names a retired step as history (for example instagram's `failure-patterns.md`) counts as a
break. Only new ones flag Plan Drift (PLAT-565), so the cost is an occasional review, not a block.

## Upwork incident and a new check (2026-10-06)

Plan Drift fixed Upwork's unstaged dependencies by rewriting six steps' bare `context_dependencies` to `../step/file`.
For the three scripted steps (`bid-record`, `bid-record-skip`, `bid-record-no-bid`) that breaks the next real run: the
platform passes a dependency containing a slash to the script unchanged and runs it from `code/<step>`, and those
`main.py` files open their arguments directly. `outreach-record` uses the form safely because its `main.py` resolves
relative arguments against `STEP_OUTPUT_DIR`. The map was right that something was broken: the 2026-10-05 17:48 merge
of `bid-read-and-draft` into `bid-pick-job` left `selected_job.json`, `draft.json`, `job_brief.json`,
`job_candidates_raw.json`, `job_details_final.json` and `verify.json` without a producer listing them in
`context_output`, and there was no real run since. The map's advice was incomplete, so Drift chose the wrong fix.

Repaired on the live workflow (backup `upwork-backup-20261006-114503-predrift`): the six dependency lists are bare
names again and the producers list the files (`search-find-and-shortlist`, `bid-pick-job`, `bid-submit`). Drift's one
description tweak on `bid-pick-job` was kept. Code: the `dependency_not_staged` detail now says how to fix it, a new
`relative_dependency_unresolved` break flags a scripted step whose `../` dependency its `main.py` does not resolve
(checked: it flags the post-Drift plan and nothing on the repaired one), and the Drift guidance forbids the rewrite.
Not yet run end to end: `bid-record` in test mode with `source_run=iteration-26-sched/daily-bid`.

