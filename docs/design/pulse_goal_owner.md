# Pulse as the goal owner

Status: design, not built. Owner: the Pulse session. Ticket: PLAT-697.

## Why

Chats and workflows ask the owner questions about his goals that he often cannot answer, and running Strategic
Review or Plan Drift does not answer them: reviewers judge work after the fact; nobody holds the goal.

Evidence (Substack, 2026-10-07): the goal is subscriber growth and `soul.md` asks for a subscriber delta every
run. Readings were recorded almost daily from Sep 1 to Sep 17, then none until a builder chat on Oct 7. From Sep 26
to Sep 30, 9 of the last 10 scheduled runs took the `publish_review` route (draft-approval check, 2-3 steps) and growth
ran once. Schedules were then paused. The goal was neither measured nor worked for three weeks and nothing flagged
it. Per-action attribution ("subscribers gained per action", a success criterion) was never measured at all.

## Decision

Pulse itself becomes the goal owner: "goal owner first, workflow repairer second". It runs on a small subset of
the Crew runtime. It is not a user Crew and does not appear in the Crew list.

From the Crew runtime it uses only:

- one persistent conversation and memory per goal (Pulse passes today start fresh);
- schedules/reminders (the daily goal check);
- function calls to and from other chats (answer a step's question, get the result back);
- reachability by chat and by Slack slug (`@QA Bot <workflow>-goal …`).

Not used: Crew sharing, its own state folder, its own tools catalogue, user-editable instructions. The prompt and
rules are platform-defined and roll out to every workflow at once. It reads the workflow's runs, db and settings
through the existing Pulse scope.

Rejected: a plain Crew per workflow (list clutter, manual setup, goals that span workflows split into owners that
contradict each other, access wired through general Crew sharing, platform rules not upgradable).

## What it does

1. **Goal check first.** Is the goal measured, is it moving, and is the work that drives it running? A failure is
   the top item, ahead of step and plan findings.
2. **Answers goal questions.** Decision requests (`report_human_inputs`) and, later, chat question cards on work
   that belongs to the goal go to Pulse first. Within its autonomy it decides, records why, and the work continues;
   otherwise it asks the owner with a recommendation and, when safe, a default by a time.
3. **Goal memory.** Every owner answer and overrule is saved on the goal and used by every agent on that workflow,
   so the same question is not asked twice.
4. **One message.** Batched: what it decided, what it needs, whether the goal is on track (app, email, Slack).
5. **Decision log.** What it decided in the owner's name, why, and what happened after.

It never edits `soul.md` and never loosens a constraint (existing rule). It must say "I don't know your
preference" instead of guessing one.

## Reviewers become skills and sub-agents

As a small Crew, the Pulse agent has skills (loaded when needed) and sub-agents (separate runs it starts and reads).

- **Skills, in its own context:** goal check; Product / Goal Work (deciding the next goal-advancing work);
  Architecture review (judging structure when a structural question comes up). Short `SKILL.md` files, cut down from
  today's reviewer prompts (about 1,700 lines across gate, fixer, drift, technical, architecture), not copied.
- **Sub-agents, separate context:** QA / Technical review (fixing steps, re-runs, checking outputs; long and noisy work
  that would flood the persistent conversation), returning a short result.
- **Independent check:** whether its own change worked is judged by a separate run, never by the Pulse agent itself
  (proposer is not the evaluator).
- Workflow Review is outside Pulse (phase 0).

## Memory: the goal over time

| | Holds | Changed by |
|---|---|---|
| `soul.md` | what the goal is: objective, success criteria, constraints | the owner only, via the Builder |
| Pulse memory | how the goal is managed: owner preferences and answers, decisions and their outcomes, lessons, open bets, what waits on the owner | the Pulse agent |

Built on Crew memory (the workflow already has `memory/` and `MEMORY.md`). Rules:

1. `soul.md` wins. When memory suggests the goal should change, Pulse proposes a `soul.md` edit to the owner.
2. The owner can see and edit the memory in the Pulse tab.
3. Every entry names its source: an owner answer, a dated result, or Pulse's own inference (marked as such).
4. Kept short: consolidated to one line per preference or lesson.

## Authority: the existing autonomy levels

`pulse.autonomy` already has `run` (default auto), `outward` (default ask) and `change` (default ask); spending
always asks. Pulse uses these as its authority line; no new permission system.

Required first (both found 2026-10-07 on a0d687b63):

- **Enforce the levels in the tools.** Since PLAT-452 they are prompt text only ("hold them yourself";
  `background_review_scope.go`). A goal owner deciding in the owner's name needs real checks.
- **Fix the contradiction.** The fixed Goal Work contract (scheduler.go ~3211) says "never act outward or edit the
  workflow yourself" even when `outward` / `change` are auto.
- `humanAnswerScope` refuses answers from background agents. Allow Pulse to answer decision requests within its
  levels, recorded as Pulse's answer, never as the owner's.

## Triggers

| Trigger | Cost | What happens |
|---|---|---|
| Run finished | code only | mark whether the goal-driving steps ran and the goal was measured |
| Daily goal check | one short turn per active workflow | measured? moving? work running? Act within autonomy or message the owner; "on track" ends the turn |
| A question is raised | short turn, immediate | answer within authority, else ask the owner with recommendation + default |
| The owner answers | short turn, immediate | apply, save to goal memory, resume the waiting work |
| Silence alarm | code only | no run or no measurement for N days (default 3) raises it, even with schedules paused; a deliberate pause is reported once |

The full review (Drift, Technical, Architecture) keeps its own self-deciding schedule (at most daily, at least
weekly).

## Phase 0: Workflow Review leaves Pulse and runs before every run

Plan Drift ("Workflow Review") checks that the plan, steps, references and contracts still fit together. That is a
change check, not goal work. Inside Pulse it runs on Pulse's clock (late after a change, wasted when nothing
changed) and, when due, it runs alone and withholds Run/Change from Goal Work, so housekeeping delays the goal.

Decision (owner, 2026-10-07): it becomes a normal pre-run check, like a compile step.

- **Before every run** (run step, run workflow, scheduled run, Pulse-started run): the code-only reference map
  (`CollectReferenceMap`, flags version) decides whether the plan changed or has new breaks since the last review.
  Unchanged and clean: the run starts immediately, no AI call.
- **Changed or broken:** the Workflow Review agent runs first, fixes what it safely can, and the run starts on the
  reviewed plan. A break it cannot fix stops the run with the reason, and the Builder chat gets the finding (the
  workflow's agents fix workflows).
- **Also on change:** after a Builder edit, an upgrade, or a platform rule change (flags version bump), so most runs
  find it already done.
- Pulse no longer schedules or waits for Drift. It reads the latest Workflow Review result as one input to "is the
  goal moving?".

Cost: one code check per run; an AI review only when something changed. Guard against loops: one review per plan
revision; a run never re-triggers a review for the same revision.

## Scope and phases

Goals are per workflow today (`soul.md`, `configure_goal_metrics`); nothing links workflows that share a goal. Start
with one owner per workflow goal; a goal spanning workflows is a later phase.

0. Workflow Review out of Pulse, as a pre-run check (above).
1. Goal check + silence alarm + one message. Pilot: Substack.
2. Enforce autonomy levels in tools; fix the Goal Work contract text.
3. Pulse answers decision requests within its levels; goal memory; decision log.
4. Pulse on the Crew runtime subset: persistent conversation, chat in the workflow's Pulse tab, Slack slug.
5. Chat question cards on goal work routed to Pulse first.
6. Goals spanning several workflows.

## Risks

- Confident wrong answers in the owner's name: the "I don't know" rule, enforced levels, the decision log, and a
  default-by-time only when the default is safe.
- Two goals competing for one budget: always the owner's call.
- Questions only the owner can answer stay his; Pulse makes them fewer and clearer, it cannot remove them.
