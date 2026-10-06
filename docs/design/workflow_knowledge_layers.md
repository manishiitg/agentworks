# Workflow knowledge layers and the improvement loop

Status: proposal (2026-10-06), owner-reviewed direction; ticket [PLAT-556](../bugs/pulse_platform/pulse-governance/plat-556.md).
Related: [PLAT-555](../bugs/pulse_platform/step-execution/plat-555.md) (step layout),
[PLAT-538](../bugs/pulse_platform/learnings-knowledge/plat-538.md) (Brain migration),
PLAT-049, PLAT-303, PLAT-305 (Pulse roles), [Brain migration design](knowledgebase-integration-migration.md).

## The model

| Layer | Holds | Lives in |
|---|---|---|
| Step description | WHAT: Goal, Inputs, Output, Rules, Done when, Guides | the plan step |
| Sequence items | the phases of one step's work, one turn each | the plan step |
| Validation schema | the output's shape | the plan step |
| Skill | HOW: procedures, selectors, techniques, pacing | the workflow's `learnings/_global` |
| Knowledge | facts and decisions | shared/org facts in **Brain**; facts used only by this workflow in its local `knowledgebase/` |

Platform mechanics belong in none of these; the runtime supplies them. Evidence and
history of a change belong in the plan changelog's `reason`, not in any layer.

## Why it has not held (evidence, Upwork, 2026-10-06)

- 163,044 characters of step instructions across 28 steps; the largest description is 26,574.
  334 description edits grew them by 81,855 characters: builder chat 220 edits (+44k), Pulse 114 (+38k).
- The selection rules of `bid-pick-job` existed only in its description; `job-scoring-criteria.md`
  even names a step description as its authority.
- Architecture review ran once (2026-09-28, model cost); every `architecture_review` focus,
  including `prompt_design`, shows 0 reviews. Since then it is skipped as
  `plan_drift_review:exclusive_prerequisite`, and Plan Drift is due again after every working-field edit.
- The description size nudge (since 2026-09-17) is shown and ignored.

## Design flaws

1. **Additions are cheap and frequent, cleanup is rare and gated.** Every fixer makes the "smallest
   complete repair", usually a sentence in a description. Only Architecture subtracts, and it waits on
   gate judgment and on Plan Drift. Growth is the expected result.
2. **The description is the only channel guaranteed to reach a step.** Skills and knowledge arrive
   only if the step's access allows it and the agent opens them (`bid-pick-job` has
   `learnings_access: none`), so careful fixers write into the description.
3. **The broad view cannot edit; the narrow views can.** Technical and the builder chat edit the plan;
   Architecture is read-only and its proposals wait for approval, so the system patches but does not
   refactor (PLAT-305's open item: no observed autonomous improvement).
4. **Too many places for truth, no ownership map.** Descriptions, items, schemas, the skill, 29 dead
   per-step learnings folders, KB notes, `rules.md`, `context.md`, `graph.json`, `soul.md`,
   `code/shared/*.md`, step config.

## Changes

1. **Guaranteed delivery.** What a step's Guides and Inputs sections name (skill references, local KB
   notes, Brain notes) is reachable by that step at run time, and attached when small: naming a guide
   grants the read access it needs. This removes the main reason fixes go into descriptions.
2. **Budgets as triggers, not gates.** Deterministic measures (description size against the plan's
   median, dated or incident text in a description, duplicated text across steps, stale KB notes) make
   a consolidation task due. They never block a run or an edit; the agent still decides how to
   restructure. Done for edit time in PLAT-555 (layout nudge, OVER BUDGET above 3x the median).
3. **A consolidator that may edit.** Architecture (prompt design, learning quality, knowledge design)
   applies a consolidation directly when a no-loss check passes: every rule, threshold, identifier and
   file name of the old text appears in the new description or a referenced guide or note, and one
   comparison run follows. Larger redesigns stay proposals for the owner.
4. **Plan Drift does not starve Architecture.** Drift stays per change; Architecture may run after a
   bounded number of drift deferrals or in the same pass, and an over-budget step makes Architecture
   due with a `prompt_design` focus.
5. **Repairs routed by type.** Fixer and builder-chat guidance: a technique goes to a skill reference
   (granting the step read access), a rule or decision to the KB or the description's Rules section,
   the evidence to the changelog reason. "Smallest complete repair" includes moving the text it touches.
6. **One ownership map, fewer places.** Retire per-step learnings folders and `graph.json`; fold
   `context.md` and `rules.md` into the knowledge layer; KB notes stop deferring to step descriptions.

## Brain: option B (owner, 2026-10-06)

- Shared and org knowledge goes to Brain: the person, the company, projects, positioning patterns,
  org-wide decisions (Upwork's `person-*`, `project-*`, `pattern-*` notes), each in one shared folder
  read by every project instead of a copy per workflow.
- Facts used only by one workflow stay in its local `knowledgebase/` next to its steps.
- So these projects are **not cut over** under the migration design (cutover makes a project
  shared-only and denies its local `knowledgebase/`). They import the shared notes into shared Brain
  folders, set `brain_access=read`, remove their local copies of those notes after a checked import,
  and keep the rest local. A full cutover stays possible per project later, once delivery from Brain
  is guaranteed (change 1).
- Brain gets the same rules as descriptions: decisions stored once and updated in place with who
  decided and when, a curator (Architecture's knowledge focus over Brain folders), and freshness or
  size triggers per folder.

## Order

1. Done: step layout guidance and edit-time nudges (PLAT-555).
2. Pilot `bid-pick-job` (owner review of the draft first) and measure one run.
3. Guaranteed delivery of named guides and notes (change 1).
4. Pulse: drift/architecture scheduling, budget triggers, edit-capable consolidation, repair routing
   (changes 2–5). Pulse belongs to its dedicated session; coordinate before editing.
5. Brain option B for Upwork: import shared notes, set Read, remove local copies.
6. Retire dead places (change 6).

## Implementation decisions (owner go, 2026-10-06: "implement the full redesign")

Pulse is edited from the session doing this work (the dedicated Pulse session is not running).
Deploys still need the owner's go per server.

**1. Delivery.** At step execution the platform reads the description's `## Inputs` and `## Guides`
sections and resolves every path they name under `learnings/`, `knowledgebase/`, `code/shared/`,
`soul/`, `db/README.md`, and Brain references written `brain:<folder>/<note>`.
- Naming a workflow path grants that step read access to it (Folder Guard), whatever its
  `learnings_access` / `knowledgebase_access` says; naming never grants write.
- Small references are attached to the step's system prompt under "Referenced guides" in the order
  named: at most 4,000 characters per file and 12,000 in total; anything larger or over the total is
  listed with its path and size and the instruction to read it. Brain notes are read through the
  project's Brain access; when the project cannot read them the step prompt says so and the run log
  records a warning (never a silent drop).
- A named path that does not exist is reported in the step prompt and the plan edit response.

**2. Budgets (triggers, never gates).** Measured deterministically by `get_plan_prompt_health` per step:
- description over 3x the plan's median description size, with a floor of 12,000 characters;
- dated or incident text in a description (ISO dates, "measured", "observed on", run ids);
- 300+ characters repeated verbatim across steps;
- a description without the layout sections.
Any of these makes Architecture due with focus `prompt_design` for those steps. The measures are also
recorded per Pulse pass as the success metric (below).

**3. Consolidator.** Architecture with focus `prompt_design` (or `learning_quality` / `knowledge_design`)
may apply a consolidation itself when:
- the new tool `check_plan_no_loss` passes: every rule-bearing token of the old description and items
  (identifiers, numbers, thresholds, file paths, VAR_ names, quoted literals) appears in the new
  description, items, or a file the new text names in Inputs/Guides;
- it then runs the step once (`execute_step`, test mode where available) and validation passes.
If the run fails, it restores the previous step from the changelog `before_ref` and files the result.
Anything beyond moving text (changing behaviour, rules, outputs, topology) stays a proposal for the owner.

**4. Scheduling.** Plan Drift remains an exclusive prerequisite only for steps it has flagged. Architecture
may not be deferred by Drift more than two Pulse passes in a row; on the third it runs, scoped away from
the flagged steps. Over-budget steps (decision 2) make Architecture due regardless of elapsed time.

**5. Repair routing.** Fixer (Technical) and builder-chat guidance: a technique goes to a skill reference
named in the step's Guides; a rule or decision to the knowledge layer or the description's Rules; the
evidence and history to the changelog `reason`. "Smallest complete repair" includes moving the text it
touches. The edit-time nudges (PLAT-555) already apply to both.

**6. Ownership cleanup.** Per workflow: per-step learnings folders that hold only metadata are removed;
`graph.json` and `_freshness.json` are retired when stale; KB notes stop naming step descriptions as their
authority. Brain option B as above.

**Success metric.** Per workflow, recorded each Pulse pass and comparable over time: total and largest
description size, steps over budget, dated-text count, duplicated text, Architecture runs and applied
consolidations, and validation pass rate of consolidated steps before and after.
