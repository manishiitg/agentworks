# Pulse as the goal owner

Builder can retire an obsolete unanswered agent-issued proposal through
`withdraw_human_input_request`, with its reason and evidence references. Pulse
can discuss this with Builder. Withdrawal is visible in history and is separate
from owner approval; it grants no action authority. Go verifies scope/provenance
and protects answered decisions; agents judge whether the request is obsolete.


Scheduled goal-check turns provide the task and permission context. Pulse and
Builder act as colleagues with their own tools: they choose what to read, ask and
share. Execution/measurement summaries are not automatically injected into those
turns. `get_goal_metrics` reads DB history; `get_pulse_state(view="goal_status")`
is an optional aggregate, and run/step read tools allow focused investigation.
Go stores evidence and delivers explicit messages; the agents judge what matters.


Status: phases 0-4 built (0: Workflow Review before runs, backup/publish/notify as schedule options; 1: goal check, silence alarm, one message; 2: enforced autonomy; 3: recommendations on decisions, goal memory in `memory/goal.md`, decision log; 4: Pulse as its own persistent chat kind, see "Phase 4 as built"; QA and Architecture owned by Pulse for workflows with a goal, see that section; ask_builder and more goal facts, see that section); phases 5-6 design. Owner: the Pulse session. Ticket: PLAT-697.

Name (owner, 2026-10-07, second decision): users and agents see **Pulse**, e.g. "Substack Pulse". It was called
"Goal Lead" while phases 0-4 were built; code names (`goal_lead_*` files, tables and types) keep that name.

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

Go supplies DB measurement/freshness facts and recorded execution statuses and
route selections. Pulse and Builder judge which work matters from the objective,
plan and step/source evidence. There is no mandatory metric-to-route mapping;
measurement scope labels do not classify execution or prove causal contribution.
The route-derived goal-work alarms/booleans were removed (PLAT-822). Genuine
measurement gaps and absence of workflow runs remain code facts. Existing saved
scope/history needs no migration, and earlier checks remain historical verdicts.


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

## A new chat kind, not a Crew

| | Crew | Workflow chat (Builder/Run) | Pulse |
|---|---|---|---|
| Lives in | its own Crew folder | the workflow | the workflow (its Pulse area) |
| Access | its own folder | Builder read/write, Run read + execute | the workflow: Run by default, Builder typed tools per autonomy |
| Lifetime | persistent | one chat per task | persistent, one per goal |
| Memory | Crew memory | the chat only | Crew-style memory beside `soul.md` |
| Schedules / wake-ups | yes | no | yes |
| Instructions | the user's | platform + user | platform-defined, the same for all |
| Created by | the user | the user | automatically when a workflow has a goal |
| Shown in | Crew list | the workflow's chats | the workflow's Pulse tab |
| Slack | by slug | no | by slug |

It reuses the Crew runtime pieces (persistent conversation, memory, schedules, function calls, Slack slugs) and the
workflow chat's access model. It is not a Crew: Crews are user-owned, shared and listed; making the Pulse one
would mean hiding and locking Crew features and routing workflow access through Crew sharing.

### Access to the workflow

Today a Crew links only to its own folder; workflow chats link to the workflow with kernel-enforced roles (Builder
read/write, Run read-only + server-executed actions; `project_instruction_files.md`).

| Need | How |
|---|---|
| Read runs, outputs, db, plan, `soul.md` | the workflow link, read access |
| Run steps or the workflow | existing run tools; only when `run` is auto |
| Change plan, steps, schedules | Builder typed tools, only when `change` is auto; otherwise a prepared recommendation |
| Larger plan changes | ask the workflow's own Builder chat by function call; Workflow Review checks before the next run |
| Its memory and focus areas | its own runtime folder in the workflow's Pulse area |
| `soul.md` | read only; edits are proposed to the owner |

The sandbox role (Run by default, Builder tools per action when allowed) enforces the autonomy levels, replacing
prompt-only enforcement. A later cross-workflow Pulse gets read links to each workflow and changes them only by
asking that workflow's Pulse or Builder.

## Job description

For anything the goal needs, the Pulse does it within its autonomy, gets the right skill or agent to do it, or
turns it into one clear ask for the owner. Nothing just sits in a list.

| Area | Owns | Stops at |
|---|---|---|
| Measurement | the goal's numbers measured every run; adds missing ones | new tracking steps need `change` |
| Work running | the goal-driving work actually runs, not starved by another route | schedule changes within `run`; held schedules need the owner |
| Blockers | finds and chases what is stuck (approvals, failing steps, logins, credits) | owner-only actions become one ask |
| Resources and budget | spend per goal (credits, tokens, paid tools) | never spends over budget or buys |
| Accounts and integrations | the logins and connections the goal needs still work | cannot log in for the owner |
| Experiments | proposes and runs small tests, judged by results | outward tests need `outward` |
| Workflow improvements | finds what limits the goal; the workflow's agents fix it | plan edits only within `change` |
| Owner inputs | approvals and questions, batched, with recommendations and deadlines | never decides the owner's preferences |
| Risks and safety | account safety, cost spikes, bad outputs; pauses when risky | irreversible actions ask |
| Other workflows | notices effects on this goal (shared credits, same audience) | cross-goal trade-offs are the owner's |
| Learning | records what worked | `soul.md` stays the owner's |
| Reporting | daily message and dashboard | |

## Focus areas

`pulse.focus_areas` today only steers attention and is set once and forgotten. The Pulse owns their lifecycle:

1. **Proposes** them from the goal check, results, or the owner's words in chat; at most 2-3 active.
2. **Turns each into work:** what to run, which skills or sub-agents, and a check of its own ("drafts waiting 3 → 0").
3. **Tracks** each daily: moving, stuck (escalated with a clear ask), or done.
4. **Closes** it: done (lesson to memory), expired (says why; proposes extend, change or drop), or no longer relevant.
5. **Learns** from closed focuses.

Every focus has an end date. The owner confirms, changes or rejects proposals with one click and can add his own; the
Pulse never starts one silently. Focus areas change what it attends to, never what it is allowed to do.

| Layer | Answers | Changes | Set by |
|---|---|---|---|
| `soul.md` | what the goal is | rarely | owner |
| Focus areas | what matters now | about weekly | Pulse proposes, owner confirms |
| Memory | what was tried, decided, learned | constantly | Pulse |

## Dashboard

Per workflow (the Pulse tab, top to bottom): goal status (on track / at risk / off track / not measured, key number,
trend, last measured); Needs you (each with a recommendation, Accept / Change, waiting time, what it blocks); focus
areas; what the Pulse did (decision, why, result, undo where possible); and behind a details tab the QA and
Architecture findings, Workflow Review results, the editable memory and the full decision log. The conversation is
in the "<workflow> Pulse" chat tab; the Pulse tab keeps a "Talk to Pulse" box that opens it (as built, below).

Across workflows: one row per goal, sorted by needs-you then off track: goal, status, needs-you count, last measured,
actions in the last 7 days. The same summary goes out as the daily Slack or email message, answerable there.

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

Required first (both found 2026-10-07 on a0d687b63; the first two are done in phase 2, see PLAT-697):

- **Enforce the levels in the tools.** Since PLAT-452 they are prompt text only ("hold them yourself";
  `background_review_scope.go`). A goal owner deciding in the owner's name needs real checks.
- **Fix the contradiction.** The fixed Goal Work contract (scheduler.go ~3211) says "never act outward or edit the
  workflow yourself" even when `outward` / `change` are auto.
- `humanAnswerScope` refuses answers from background agents. Phase 3 (owner, 2026-10-07): recommendations first.
  Pulse attaches a recommendation (recorded as Pulse's, never as an answer) and the owner accepts or changes it;
  `humanAnswerScope` still refuses Pulse. Answering within its levels waits for `pulse.autonomy.answer=act`.

## Triggers

| Trigger | Cost | What happens |
|---|---|---|
| Run finished | code only | mark whether the goal-driving steps ran and the goal was measured |
| Daily goal check | one short turn per active workflow | measured? moving? work running? Act within autonomy or message the owner; "on track" ends the turn |
| A question is raised | short turn, immediate | answer within authority, else ask the owner with recommendation + default |
| The owner answers | short turn, immediate | apply, save to goal memory, resume the waiting work |
| Silence alarm | code only | missing/unavailable primary DB measurements, stale readings beyond each metric's freshness, or no run for N days (default 3); execution attribution is separate; a deliberate pause is reported once |

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

## What leaves Pulse

Pulse today also carries housekeeping: its finalizer runs Backup → Publish → Notify after each pass, a schedule's
`pulse_mode=basic` means just those three, and the Pulse tab shows recent activity. None of it is goal work.

| Today in Pulse | What it is | Where it goes |
|---|---|---|
| Backup | saving workflow state and history | an after-run option of the workflow (and/or a daily job) in Backup/History settings; code only |
| Publish | refreshing the published dashboard snapshot | an after-run option in Publish settings ("republish after each run") |
| Notify | run finished / failed / report ready | the run's notification settings; separate from the Pulse's daily goal message |
| Recent activity | log of runs, edits, reviews | the workflow's Activity / History tab; the Pulse shows only its own decisions |
| Plan Drift (Workflow Review) | plan consistency after changes | the pre-run check (phase 0) |

- `pulse_mode` (off / basic / full) goes away: a schedule has after-run checkboxes (backup, publish, notify). The
  Pulse runs on its own triggers, never through a schedule's mode.
- Backup and publish no longer wait on or depend on an AI pass.
- Two kinds of messages: run notifications (per run, optional) and the Pulse's goal message (daily, per goal).
- After this "Pulse" as a bundle is gone: Workflow Review before runs, housekeeping after runs, history in Activity,
  and the Pulse owning the goal with QA and Architecture as its skills and sub-agents.

## Scope and phases

Goals are per workflow today (`soul.md`, `configure_goal_metrics`); nothing links workflows that share a goal. Start
with one owner per workflow goal; a goal spanning workflows is a later phase.

0. Workflow Review out of Pulse, as a pre-run check; Backup, Publish, Notify and recent activity out of Pulse (above).
1. Goal check + silence alarm + one message. Pilot: Substack.
2. Enforce autonomy levels in tools; fix the Goal Work contract text.
3. Pulse answers decision requests within its levels; goal memory; decision log.
4. Pulse on the Crew runtime subset: persistent conversation, chat in the workflow's Pulse tab, Slack slug.
5. Chat question cards on goal work routed to Pulse first.
6. Goals spanning several workflows.

## Phase 4 as built

- **Conversation.** One per workflow, stored in its Pulse state (`goal_lead_conversation`): a stable session id
  (`schedule-goallead--<hash>-g<N>`). Every turn names it as the restored conversation, so a coding CLI resumes its
  native session and an API model replays its transcript (the Crew and workflow-ask path). The CLI's own compaction
  bounds it; after 30 days or 90 turns the next goal check starts generation N+1 and goal memory carries over.
- **Turns.** Daily goal check and the full Pulse's Goal Work (scheduler, same receipts as before), the owner's
  messages (the Pulse tab's box, or relayed by the Builder chat with `ask_pulse`), `ask_pulse` (old name `ask_goal_lead`, kept for one release; workflow chats and steps,
  function call, 20 an hour), Slack, and a failed run (below). Each runs as
  the workflow's execution owner on its Builder runtime as a Pulse turn, held by the phase 2 guard to
  `pulse.autonomy`. Kernel Run mode (Landlock read-only) is not used yet: Goal Work writes drafts under `pulse/work/`.
- **Skills and sub-agents.** `goal-lead-check.md`, `goal-lead-work.md`, `goal-lead-architecture.md` (about 40 lines
  each). QA: `record_pulse_qa_request`; the scheduler starts a Pulse fix run when the workflow is free and writes its
  short result back. For a workflow with a goal the full pass runs no Architecture or Technical turn (below).
- **Focus areas.** `pulse.focus_areas` stays the active list; `pulse.focus_area_details` holds each area's end date,
  check, status, tracking and closing lesson. At most three open; proposals wait for the owner's confirm.
- **Pulse tab.** Goal status card, focus areas, Needs you, decision log, memory, a "Talk to Pulse" box. The
  conversation was here at first; since 2026-10-08 it is only in the "<workflow> Pulse" chat tab, which the box
  opens after sending (below).
- **Slack.** `<workflow-slug>-pulse` on any app that reaches the workflow (`-goal` still works for one release);
  the thread or DM stays bound to it.

## QA and Architecture owned by Pulse (as built)

Owner decision (2026-10-07): for a workflow with a goal (a goal plus a primary metric, `workflowHasGoal`), the full
pass ran its own Architecture and Technical turns next to the goal's conversation: duplication, and the goal's
owner did not own its workflow. Now that conversation is the only Pulse of such a workflow.

- **Pass order.** `pulsemodules.PassOrder(hasGoal)`: Gate, Goal Work (in the conversation), Finalize. Gate is told
  Architecture and Technical are not its to select, and its worklist records them not due
  (`keepGoalLeadReviewsOutOfPulse`); one a backend rule still makes due (a pending recovery, the prompt budget, a
  protected boundary) is closed as skipped by code, which also clears its recovery. Workflows without a goal keep
  Goal Work, Architecture, Technical.
- **Architecture** is the conversation's skill (`goal-lead-architecture.md`), used when its checks raise a
  structural question.
- **QA** runs only when the conversation asks (`record_pulse_qa_request` -> a fix run, result back into it). The
  automatic fix runs (open issues, new step concerns, failed runs) no longer start for these workflows.
- **Safety net.** Code decides a run failed (a workflow run, not a Pulse pass, with status error, failed or
  interrupted in the last day); the tick gives the conversation one short turn for it, at most once per failed run
  (`goal_lead_run_failures`), with Run, Outward and Change held: it asks for QA when the failure blocks or threatens
  the goal, and says in one line why the others can wait. The optional goal-status tool returns `run_health` since the
  last check: failed runs with their error, steps' `CONCERNS:` lines, the open issue count and schedule run health,
  what Technical read after runs; its skill says to request QA for a failure that threatens the goal and note the
  others.
- **Fast requests** still start an earlier full pass, which for these workflows is Goal Work in the conversation.
- **Pulse tab.** No Technical or Architecture panels or run-history columns for these workflows; a line says the
  workflow's Pulse handles them. Their output shows in the conversation (QA results, replies) and the decision log.
  Workflow Review and maintenance issues stay.

## ask_builder and more goal facts (as built)

- Delayed `ask_builder` and chat-origin `ask_pulse` replies use the shared
  function-call notification queue. Inline replies need no second turn; joined
  submissions share a watcher. A busy Pulse queues the result, then resumes its
  own conversation through the normal Pulse turn bootstrap with current owner
  authority and autonomy. It reconciles evidence, Goal Work, goal status and
  memory; a reply saying a background run started remains pending verification.
  Disabled, stopped or rotated conversations are not revived. Workflow steps
  retain their own call result rather than redirecting it to a Builder chat.
  Saved call records and `builder_asks` remain available when automatic
  delivery is unavailable. This does not add arbitrary session discovery or
  promise notification recovery across a server restart. PLAT-825.
- **ask_builder.** The Pulse asks the workflow's Builder chat (the owner's most recently active one, or a named one
  of the owner's) by function call, as a normal turn there. `question` changes nothing (the chat's tools are held to
  ask for the turn); `fix` is sent only at `change=auto` (the chat may change, not run or send), otherwise it becomes
  one decision with Pulse's recommendation and Accept applies it in the Builder chat. Never for soul.md, deletions
  or contract migrations, never from a failed-run turn, never back to a Builder chat that asked. No Builder chat: a
  question says so, a fix becomes a decision. Answers go to goal memory as `builder_answer`.
- **More facts in the goal check** (code only, since the last check): plan changes from planning/changelog, decisions
  the owner answered (not Builder chat messages: too costly to scan), spend for the last 7 days against the 7 before
  from the cost ledger (no budget field exists, so trend only), runs costing twice the 14-day median, the failure
  share against its 14-day median, and text-matched login/connection hints. `goal-lead-check.md` says what to do with
  each.

## Talking to Pulse: Builder chat threads and the Pulse chat tab (as built)

Owner, 2026-10-08: the Builder must use Pulse as the goal expert in a real exchange, not one-off messages, and the
conversation belongs in the "<workflow> Pulse" chat tab.

- The Pulse tab shows no conversation; its "Talk to Pulse" box sends straight into Pulse's conversation and opens
  the Pulse chat tab.
- The Builder asks Pulse with `ask_pulse` for goal status, why Pulse did something, which option serves the goal,
  and direction. An ask from a person's Builder chat carries the owner's words: Pulse records lasting direction in
  goal memory or proposes a focus area.
- Pulse ends each answer with `question: …` (a fact it needs; the Builder answers in the same `thread_id`) or
  `decision: …` and `owner_needed: yes|no (why)`. With `no` the Builder acts without asking the owner again and
  reports in one line; with `yes` it asks the owner once, quoting Pulse. A thread has at most 6 rounds; Pulse asks
  back in its reply, never by calling the chat.
- On the owner's approval: at `change=auto` Pulse makes the change; at ask the Builder edits and tells Pulse.
  ask_pulse turns follow `pulse.autonomy` like every Pulse turn. Tool calls over the session's HTTP tool route are
  held by the same guard; Pulse is told never to script them from the shell.
- The Builder chat shows one thread as one "Builder ↔ Pulse: N rounds" block.
- Slack `<slug>-pulse` still reaches Pulse directly (owner to decide).

## Risks

- Confident wrong answers in the owner's name: the "I don't know" rule, enforced levels, the decision log, and a
  default-by-time only when the default is safe.
- Two goals competing for one budget: always the owner's call.
- Questions only the owner can answer stay his; Pulse makes them fewer and clearer, it cannot remove them.
