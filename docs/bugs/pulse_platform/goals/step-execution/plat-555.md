[← goals / step-execution](index.md)

# PLAT-555 — Step descriptions grow into manuals: give them a fixed layout (Goal, Inputs, Output, Rules, Done when, Guides)

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | goals |
| Area | step-execution |
| Summary | guidance on main; Upwork `bid-pick-job` pilot applied to the local workflow (2026-10-06), comparison run and review-side enforcement open. |

| Coordination | Value |
|---|---|
| State | guidance on main (2026-10-06); Upwork `bid-pick-job` pilot applied to the owner's local workflow data 2026-10-06 (owner go: "implement the full redesign"); comparison run and review-side enforcement open |
| Date | 2026-10-06 |
| Owner | step-execution |

## Source

Owner: "step description is like a user message ... what to do -> skills is how to best do it -> knowledgebase is overall global facts, decisions. Is this getting followed?" and "should we divide the step description into a proper logic format".

## Evidence (Upwork, read 2026-10-06)

- 28 steps carry 163,044 characters of instructions; the largest are `improve-analyze-report` (26,574), `toptal-scan-draft` (21,611), `profile-suggest-report` (21,273) and `bid-pick-job` (14,667).
- Descriptions hold procedure (shell working-directory rules, route guards), platform mechanics, and dated incident history: 37 of 158 sentences in `improve-analyze-report` state dates or decisions ("MEASURED 2026-08-10 ...").
- In message sequences the charter does the turns' job: `bid-pick-job` has a 13,103-character description and two items of 551 and 1,011 characters.
- KB notes `flow-bidding.md` and `flow-profile-update.md` mix decisions with procedure; the skill (`learnings/_global`) is mostly true how-to.
- Why it is not cleaned up: Technical review (476 records on Upwork) repairs failures, usually by adding to descriptions; Plan Drift checks prompt quality only for changed working fields and only the diff (description-only edits do not trigger it since 2026-09-26); Architecture review, which owns prompt clarity, KB freshness and learning applicability, has never run on Upwork (0 records).

## Done

- `agent_go/cmd/server/guidance/templates/system/step-description.md`: a required section layout for descriptions (Goal, Inputs, Output, Rules, Done when, Guides), where everything else belongs (phases in items, procedures in skills/learnings, facts and decisions in the KB, platform mechanics nowhere), and "a description holds no history": a repair changes the rule, item or guide, it does not append a story. Guidance tests pass.

- Plan edit tool responses (`planning_agent.go`): the description size nudge now names the layout and where content goes (how-to to a skill reference, facts and decisions to a KB note, dated history deleted). Above 3x the plan's median description size (floor 12,000 characters) it says OVER BUDGET and asks for the restructure in the same session, with a no-loss check; the edit is still saved. The ~700-character compatibility paragraph is sent on the first plan edit of a workflow and replaced by a one-line pointer for edits within the next 30 minutes of activity. Tests `TestStepDescriptionSizeNudgeOverBudgetAsksForTheMove`, `TestPlanEditImpactGuidanceIsSentOncePerSession`; the package suite passes except the pre-existing `TestValidateStepLLMConfigEnforcesAgyAlphaGate`.

- Upwork pilot applied 2026-10-06 (local workflow `workspace-docs/Workflow/upwork`, user data, not in git). Backup before any change: `/private/tmp/claude-501/-Users-mipl-ai-work/baa0f19b-b6e6-40e4-a5c8-e57597e2e0ad/scratchpad/upwork-backup-20261006-101124/` (`planning/`, `learnings/`, `knowledgebase/`, `workflow.json`).
  - `bid-pick-job` description replaced by the layout (Goal, Inputs, Output, Rules, Done when, Guides); its two items rewritten with the same ids and kinds.
  - Rules moved to `knowledgebase/notes/bid-admission-and-claim-rules.md` (indexed in `notes/_index.json`); procedure to `learnings/_global/references/bid-claim-protocol.md` (listed in `learnings/_global/SKILL.md`). `learnings_access` none → read so the Guides are reachable; `drift_review.needs_review` set because items changed.
  - No-loss check (script: every identifier, number, VAR_ name and file name of the old description + items must appear in the new description, items or the files they name): all 181 rule-bearing tokens present; the only unmatched pieces are prose words glued to numbers in the old text (`max18`, `permits120`, `scopes500`, `simple3-integration+AI`, `machine-produced`) whose numbers are present.
  - Size: description 13,103 → 3,085 characters, items 1,562 → 1,286 (was 14,665 in total, now 4,371).
  - Changelog: `planning/changelog/changelog-2026-10-06-04-44-44.json` (tool, reason, step_ids, old/new values, before/after refs; actor `owner_directed:direct_file_edit`).
- Judgment calls: there is no sanctioned local plan-mutation route (the `agentworks` CLI states "Plan mutations are not exposed" and is connected to another server; the local backend needs auth and no token was extracted), so the plan and step config were edited by a JSON-safe script that keeps Go's encoding byte-for-byte, with a changelog entry in the tools' format. A few phrases the draft had compressed were restored in the KB note and skill (15-minute-call close, lowercase-i and apostrophes, no boilerplate tool pitch or forced link, no hourly-to-fixed terms, the exact `no_bid` / `skipped_draft` gate requirements). The earlier review note "historical global MCP learnings remain excluded" no longer holds for this step; SKILL.md already marks the old UI references historical and grants no browser access.

## Left

- Comparison run of the pilot (not run: it needs a same-run discovery and would claim a real `proposals` row). Owner test: run the `daily-bid` group once (job-search then bid) from the UI or `run_full_workflow` with `VAR_FLOW_MODE=bid`; check that `bid-pick-job` reads `bid-admission-and-claim-rules.md` and `bid-claim-protocol.md`, that `preparation_gate.json` comes from `code/shared/bid_preparation_gate.py`, and that validation passes; compare turn count and outcome with the last successful run. Restore from the backup or the changelog `old_value` if it regresses.
- Review side (Pulse; the Pulse session is not running): let a description edit trigger a prompt-quality-only Plan Drift check, and a whole-step review when a description is large or carries dated text; find out why Architecture review never runs on Upwork.
- Migrate other long descriptions step by step through the reviews, not in one sweep.

## Register notes

[PLAT-555](plat-555.md), guidance on main; Upwork pilot and review-side enforcement open.
