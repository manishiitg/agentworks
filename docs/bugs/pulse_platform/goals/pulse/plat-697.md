[← goals / pulse](index.md)

# PLAT-697: Pulse as the goal owner

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | goals |
| Area | pulse |
| Summary | Pulse becomes the goal owner: daily goal check, answers goal questions within its autonomy, goal memory; built on a small Crew-runtime subset. Phases 0-4 on main (Workflow Review before runs and backup/publish/notify as schedule options; goal check; enforced autonomy; recommendations on decisions, goal memory, decision log; Pulse as one persistent conversation per workflow with chat, focus areas, ask_pulse and a Slack slug; for workflows with a goal it owns QA and Architecture, no separate review turns; the name users see is Pulse, not Goal Lead; ask_builder lets Pulse ask the Builder chat, and the goal check carries plan changes, owner answers, spend, login hints and spikes; the Builder chat treats Pulse as the goal expert in bounded threads and acts on its decision; the Pulse tab's Talk to Pulse box opens the Pulse chat tab, which holds the conversation) |

## What happened

Substack's subscriber goal went unmeasured for three weeks while runs went on, and nothing flagged it (below).

## Fix

**Phase 1 — goal check + silence alarm + one message (on main, not deployed).**

- After-run goal facts, code only: when a workflow run finishes (`finalizeRunMetadata`), `RecordGoalRunFacts`
  stores the route it took (from `route_selection.json`), whether that is the primary metric's route and completed,
  and whether a primary reading was recorded in `pulse_goal_observations` during the run, in `goal_run_facts`
  (db/db.sqlite). Runs before that are read from `schedule-runs.json` with the schedule's route selection
  (`step_based_workflow/goal_run_facts.go`, `cmd/server/pulse_goal_check.go`).
- Silence alarm, code only (`pkg/goalcheck`): not measured by a workflow run for N days (default 3; a builder-chat
  reading does not count as the workflow measuring), goal work ran without a reading, goal work skipped while other
  routes ran, no run for N days. Schedules all paused: said once, then quiet until a run, a reading or the alarms
  change (pause fingerprint stored with the goal check).
- Exposed first to the agent: `get_pulse_state(view="goal_status")`, and inside `view="goal_work"`.
- Daily goal check: one short Pulse turn per workflow with a primary goal metric, at most daily, started by the
  scheduler tick after full Pulse and fix runs. It reuses the full Pulse's schedule ID, so it never runs beside a
  Pulse pass on the same workflow, shares the Pulse concurrency cap, does not move the full Pulse schedule and does
  not consume fast requests. On track: `record_pulse_goal_check(on_track)` and stop. Otherwise it acts within
  `pulse.autonomy` or makes one batched decision with a recommendation and safe default, records the verdict and
  sends one `pulse_summary` notification. The turn's tools are held to `pulse.autonomy` by the phase 2 guard;
  a paused workflow gets one report-only check with Run, Outward and Change all held.
- Strategic review (Goal Work) starts with the goal check (`strategy-auditor.md` step 0); the full Pulse
  finalizer's Pulse summary leads with the goal status.
- Pulse tab: goal status card at the top (on track / at risk / off track / not measured, key number, last
  measured, alarms) from the Pulse worklist API's `goal_status`.

Verification: `go test ./pkg/goalcheck` with a read-only copy of only the relevant Substack rows
(`pkg/goalcheck/testdata/substack_2026-10-07.json`): "not measured by a workflow run for 20 days (last reading
17 Sep)", one reading since from outside a run (builder chat 7 Oct), growth_funnel ran 6 times since 17 Sep
without a reading, growth work has not completed for 11 days (the 10 runs since: publish_review ×6, research ×2,
write_article ×1, one failed growth run), no run for 7 days with schedules paused; the pause is reported once and
a new run ends the quiet period. Server wiring verified on GitHub (verify-remote: build, vet, named tests); the
goal check turn itself is not yet run live.

## Left

- Phase 1: run the daily goal check live on Substack after deploy (owner's call) and check the message.
- Workflows with a goal in `soul.md` but no configured metric get no goal check yet.
- Phase 3 (below): not run live; `pulse.autonomy.answer=act` (Pulse answering within its levels, and a safe
  default applied when its time passes) is not built; the owner's answer reaches memory as a straight copy, so a
  reason the owner gives only in chat is not distilled until Pulse adds it.
- Phase 0 left: see "Phase 0" below.
- Phase 4 left: see "Phase 4" below.
- QA and Architecture owned by Pulse: see that section below (not run live; the run-failure turn, the pass without
  review turns and the hidden panels need a local check by the owner).
- Rename: `ask_goal_lead` and the `-goal` Slack slug are kept as aliases for one release; remove them after.
- ask_builder and more goal facts: see that section below (not run live).
- Talking to Pulse (Builder chat threads, Talk to Pulse box): see that section below (not run live after the change).
- Slack `<slug>-pulse` still talks to Pulse directly; the owner decides whether it should go through the Builder
  chat too.
- Phases 5-6.

## What and why

The owner (2026-10-07): "there should be an agent which owns goals". Agents ask him goal questions he cannot answer,
and Strategic Review / Plan Drift do not answer them. Evidence: Substack's subscriber goal went unmeasured and
unworked for three weeks (Sep 18 – Oct 6) while runs took only the draft-approval route, and nothing flagged it.

Design: [Pulse as the goal owner](../../../../design/pulse_goal_owner.md). Pulse itself owns the goal, on a small
subset of the Crew runtime (persistent conversation, schedules, function calls, Slack slug); not a user Crew.

## Plan

Owned by the Pulse session. Phase 0 (owner, 2026-10-07): Workflow Review (Plan Drift)
leaves Pulse and runs as a pre-run check before every step/workflow run when the plan changed. Then: daily goal check + silence alarm + one message,
pilot Substack. Prerequisites done in phase 2 (below): `pulse.autonomy` enforced in the tools and the Goal Work
contract text fixed.

## Phase 2: autonomy levels enforced by the tools (on main, not deployed)

- During a scheduled Pulse Goal Work turn (strategic_review, and its receipt
  continuation) the scheduler holds the Pulse session's tools to that turn's
  `pulse.autonomy` levels (`cmd/server/pulse_autonomy_guard.go`). The check runs
  in `bindToolExecutionContext`, the binding every direct tool call of the
  session passes; the session comes from the server, never from tool arguments.
  Delegated helpers of the session are held too; workflow steps the turn starts
  (child MCP sessions registered to the run) keep their normal tools.
- Run at ask refuses `execute_step`, `run_full_workflow`, `debug_step`,
  `send_step_message`, `trigger_schedule`, `ask_platform_crew`. Change at ask
  refuses the Builder plan tools (`ScheduleGuardedTool` list), `update_step_config`,
  schedule create/update, `configure_goal_metrics`, `restore_step_from_changelog`,
  `manage_workflow_webhook`. Always refused in Goal Work: delete steps/schedules,
  `create_plan`, migrations, contract version. Outward at ask refuses
  `send_slack_message` and Slack writes, runs Google (`gog`) read-only, and
  refuses `notify_user` to `email_to`/`email_cc`. Each refusal says to create a
  decision request instead.
- (Superseded by phase 0: Workflow Review no longer holds Run or Change.)
- The tool catalog is not narrowed per turn (that is why PLAT-452 did not keep
  `goalWorkToolAllowed`, a filtered tool list of the removed background agent);
  calls are refused instead.
- The Goal Work contract text now matches the levels (outward/change allowed when
  auto, a decision request when ask); "not enforced by the tools" is gone.
- Not enforced: browser, shell (curl with workflow secrets) and connected MCP
  server actions (not classifiable by name); `soul.md` writes through file tools;
  spending (no purchase tool; prompt rule). The manual `/run-goal-work` path in a
  Builder chat is still prompt-held (its text says so).
- Not exercised in a live Pulse run. Test: `TestGoalWorkAutonomyIsEnforcedAtToolDispatch`.

## Phase 3: recommendations on decisions, goal memory, decision log (on main, not deployed)

Owner decision (2026-10-07): recommendations first. Pulse does not answer decisions yet; the owner confirms.

- **Recommendation on every decision request.** `record_pulse_recommendation` attaches the Pulse's
  recommendation to a pending decision on the workflow: the option (or a free-text answer when there are no
  options), why, the evidence, confidence, what it blocks, and `safe_default_by` only with high confidence and when
  applying it needs no level held at ask (a workflow change needs `change=auto`). Stored apart from the answer in
  `pulse_recommendations` (db/db.sqlite, beside `report_human_inputs`), with a `recommended` event; list responses
  carry it as `recommendation`. The daily goal check and Goal Work (`strategy-auditor.md`) recommend on each
  pending decision every pass, on track or not. `humanAnswerScope` is unchanged: Pulse, scheduled and background
  sessions still cannot answer.
- **Config switch.** `pulse.autonomy.answer`: `recommend` (default) or `act`; only `recommend` is built, `act`
  behaves the same. The Pulse-tab autonomy save keeps the value.
- **Owner's answer.** Accept (or Change) in the Pulse tab answers through the normal answer route, then sends the
  apply message to the Builder chat as "Apply in chat" does. In the same transaction the recommendation is marked
  accepted, changed or dismissed.
- **Goal memory** in the workflow's memory area: `memory/goal.md`, next to the Builder's `MEMORY.md` (project
  memory, untouched). Sections: owner preferences and answers, decisions and outcomes, lessons, open bets, waiting
  on the owner; one line per entry, `- YYYY-MM-DD [owner answer | result | Pulse inference] text`. Every owner
  answer is copied in by code (question, chosen option, note, and whether it accepted or overruled the Pulse).
  Pulse adds results, lessons and bets with `record_pulse_goal_memory(action=add)` and rewrites it with
  `action=consolidate` (at most 60 entries; it is told to consolidate past 45). The goal check, `goal_status` and
  `goal_work` views carry it (`goal_lead`) so Pulse reads it first; soul.md wins on conflict. The owner reads and
  edits it in the Pulse tab (`GET /api/workflow/goal-lead`, `PUT /api/workflow/goal-memory`, write access).
- **Decision log.** The same rows: what the Pulse recommended, why, what the owner did, and the result, which a
  later goal check records with `record_pulse_decision_outcome` (answered decisions over a day old are listed in
  `outcomes_due`). Shown in the Pulse tab under the goal status card.
- **Pulse tab.** Under the goal status card: Needs you (each pending decision with the recommendation, Accept and
  Change, how long it has waited, what it blocks), the decision log with each result, and the goal memory editor
  (`GoalLeadPanel.tsx`).

Tests: `TestGoalLeadRecommendsAndOnlyTheOwnerAnswers` (a Pulse session's answer is refused, its recommendation is
stored as Pulse's and the decision stays pending; the owner's Accept answers with the recommended option, marks it
accepted and writes the owner-answer line to `memory/goal.md`), `GoalLeadPanel.test.tsx` (the Needs you card and
Accept). Not run live.

## Phase 4: the Pulse as its own persistent chat kind (on main, not deployed)

- **One conversation per workflow** with a goal (soul.md plus a primary metric; `workflowHasGoal`), created on the
  first goal check or when the Pulse tab opens (`cmd/server/goal_lead_conversation.go`). Kept in the workflow's
  Pulse state (`goal_lead_conversation`, db/db.sqlite): a stable session id `schedule-goallead--<hash>-g<N>` (the
  `schedule-` prefix keeps it unattended: no workflow-busy lock, never a person's Builder chat, `humanAnswerScope`
  still refuses it). Platform-defined charter sent as the first turn of each conversation; nobody edits it. Shown
  in the Pulse tab, not the Crew list.
- **Continuity.** Every turn uses that session and names it as `restored_conversation_session_id`: a coding CLI
  resumes its native session (`--resume`), an API model replays its saved transcript; the same path Crew
  conversations and workflow asks use. All turns are Pulse turns with the same authority, so the session key never
  changes and native resume is never dropped for a role change. Context growth: the CLI's own compaction; after 30
  days or 90 turns the next goal check starts generation N+1 (logged in the conversation); goal memory carries what
  matters. A new turn clears an earlier Stop (PLAT-130 still guards continuations of the stopped turn).
- **Goal check and Goal Work run in it.** The daily goal check step and the full Pulse's strategic_review turn (and
  its receipt continuation) run in the Pulse conversation instead of the pass's session (`runGoalLeadPassStep`);
  receipts, the goal-check record and the phase 2 guard are unchanged. Workflows without a goal keep the old
  strategic_review turn.
- **Access.** Turns run as the workflow's execution owner on its Builder runtime (model, MCP servers, skills), held
  on every turn by the phase 2 tool guard to `pulse.autonomy` (Builder typed tools only with change=auto). Kernel
  Run mode (Landlock read-only) is not used: Goal Work writes drafts under `pulse/work/`, which Run cannot.
- **Skills and sub-agents.** `goal-lead-check.md`, `goal-lead-work.md`, `goal-lead-architecture.md` (reference
  docs, about 40 lines each, cut from the goal check prompt, `strategy-auditor.md` and `architecture-review.md`).
  QA / technical review: `record_pulse_qa_request` records what to check; the tick starts a Pulse fix run (its own
  session) when the workflow is free and writes its short result (status, technical review result and reason)
  back to the conversation log and the next turn's context (`goal_lead.qa_results`) (`goal_lead_qa.go`). The full
  Pulse's Architecture and Technical turns and `strategy-auditor.md` are unchanged (kept for workflows without a
  goal and `/run-goal-work`).
- **Talk to it.** The Pulse tab's "Talk to Pulse" box (`POST /api/workflow/goal-lead/message`, write access; the
  turn runs in the background). Since 2026-10-08 the Pulse tab shows no conversation; the reply is read in the
  "<workflow> Pulse" chat tab, which the box opens after sending (see "Talking to Pulse" below). The turn's
  instructions: lasting direction to goal memory, time-boxed direction as a proposed focus area, goal changes as a
  proposed soul.md edit.
- **Focus areas** (`goal_lead_focus_areas.go`): `pulse.focus_areas` stays the active list every reader uses;
  `pulse.focus_area_details` adds end date, check, status (proposed, active, done, expired, dropped), daily
  tracking and the closing lesson. `record_pulse_focus_area` proposes (at most three open), tracks and closes (the
  lesson also goes to goal memory); the owner confirms, rejects, adds, extends or drops in the Pulse tab
  (`POST /api/workflow/goal-lead/focus-areas`). Shown at the top of the Pulse tab under the goal status card.
- **ask_pulse** (`goal_lead_ask.go`): workflow chats (Builder and Run) and steps (with platform stores) ask the
  workflow's Pulse through `startCrewFunctionCall` with a chat target, like `ask_project_chat`: a call id and
  record, a turn in the Pulse conversation, the final reply as the answer, which the turn's text frames as a
  recommendation. Waits up to `wait_seconds` (default 60); 20 asks per workflow per hour; the Pulse cannot ask
  itself.
- **Slack**: `<workflow-slug>-pulse` (was `-goal`, still accepted) on any app that reaches the workflow (channel list or DM targets; Slack
  membership decides channels), parsed only against the workflow targets there (`services/slack_goal_lead.go`). The
  thread or DM is bound to it (`SlackTargetRef.Agent = goal_lead`), and its messages are answered by the Pulse
  conversation, not a bot session. Only people with access to the workflow: a DM's account, or a channel sender
  whose Slack email maps to an account with access (`handleGoalLeadSlack`).

Left / risks:

- Not run live: the goal check in the persistent conversation, asks and Slack.
- Kernel Run mode for the Pulse (see Access); shell, browser and MCP actions stay prompt-held as in phase 2.
- `ask_pulse` in steps depends on the step's tool session resolving its workflow (`pulseToolScope`); not
  checked live. A pending ask is read again with the same message and `submission_id`.
- Focus areas live in workflow.json (read-modify-write); a Builder save at the same moment could drop one change.
- An owner relay from a co-owner's or editor's Builder chat runs as the workflow's execution owner (as schedules
  do).
- Slack threads bound to the Pulse answer plain replies; the thread history is not replayed into the turn.
- Tests: `TestGoalLeadCheckContinuesItsConversationAndAnswersAsks`, `GoalLeadPanel.test.tsx`.

## Talking to Pulse: Builder chat threads and the Pulse chat tab (on main, not deployed)

Owner, 2026-10-08, testing locally: the conversation in the Pulse tab duplicated the "<workflow> Pulse" chat tab,
which he likes; he first wanted to talk to Pulse only through the Builder chat, then decided to keep the "Talk to
Pulse" box with the reply read in the Pulse chat tab. In his Substack test (Pulse session `schedule-goallead--dc77e3afb5bbf2d7-g1`)
the Builder asked Pulse once, then asked him again and forwarded his "yes, go ahead" as a second one-off ask; "the
builder is not using pulse as an expert … they are not having a conversation, just exchanging one-off msgs". Pulse
then edited `step-growth-summary` in that ask turn: Substack has run, outward and change all auto, which the owner
intended. Its shell had refused a direct edit, so it scripted `update_step` against the session's HTTP tool route
(`POST /s/<session>/tools/custom/update_step` with `$MCP_API_TOKEN`, from python urllib).

- **Pulse tab.** No conversation (the "<workflow> Pulse" chat tab shows it). The "Talk to Pulse" box stays, with one
  line of help ("Ask Pulse why, or give it direction. Replies appear in the Pulse chat tab."); it sends straight into
  Pulse's conversation (`POST /api/workflow/goal-lead/message`, kept) and then opens or focuses the Pulse chat tab
  (`openPulseChatTab`, by the conversation's session id). Focus-area confirm/reject, Needs you Accept/Change and the
  editable memory stay.
- **Builder → Pulse.** `ask_pulse`'s description and `workflow-chat.md` say: goal status, why Pulse did something,
  which option serves the goal and direction for Pulse go to `ask_pulse`; Pulse is the goal expert. An ask from a
  person's Builder chat (attended, not scheduled, not a bot channel, workshop mode, write access;
  `goalLeadAskCaller`) is an owner relay: the turn carries their words and the Slack-style rules (lasting
  direction to goal memory as `owner_answer`, time-boxed direction as a proposed focus area) and is logged as the
  owner's. Steps, Run chats, bot channels and readers only ask for a recommendation.
- **Decision lines.** Every ask turn ends with `question: …` or `decision: …` plus `owner_needed: yes|no (why)`.
  The call result carries `decision`, `owner_needed`, `pulse_question`, `thread_open`, and `next` tells the Builder:
  `no` → act without asking the owner again, one line "Pulse recommended X because Y; done."; `yes` (beyond its
  levels, a preference only the owner knows, spending, irreversible, soul.md) → ask the owner once, quoting Pulse.
- **Threads.** `ask_pulse(thread_id)` continues a topic: Pulse's turn says "round N of at most 6 of thread T" and
  answers as the next step. Pulse asks back for a fact in its reply (`question:`), the Builder answers in the
  thread; the decision lines close it; a closed thread or round 7 is refused. Every round counts toward the
  20-an-hour cap. The chain guard and ask_builder's no-ping-pong rule are unchanged: Pulse never calls the chat
  back. Threads are in memory (a restart starts a new one).
- **Approval.** When the owner approves something Pulse recommended: at `change=auto` the Builder passes the
  go-ahead to Pulse in the thread and Pulse makes the change and reports it; at ask the Builder edits and tells
  Pulse "done: …" for goal memory. ask_pulse turns follow `pulse.autonomy` like every Pulse turn.
- **Builder chat view.** All ask_pulse rounds of one thread render as one expandable "Builder ↔ Pulse: N rounds"
  block with the last decision (`groupPulseThreads`, `PulseThreadBlock`).
- **HTTP tool route.** Checked by reading the code: `/s/<session>/tools/custom/<tool>` runs the session's
  registered executor, and `agentwrapper.RegisterCustomToolWithTimeout` wraps every direct tool with the session's
  `ToolExecutionContext` (`bindToolExecutionContext`, which holds the autonomy levels), so a scripted call is held
  exactly like a direct one; at change=auto it was allowed, as the owner intended. The shell has `MCP_API_URL` and
  `MCP_API_TOKEN` because the coding CLI's MCP bridge calls the same route. The charter now tells Pulse never to
  script tool-API calls from the shell.

Tests: `TestPulseThreadAsksBackThenDecidesWithoutTheOwner` (round 1 Pulse asks a fact, round 2 decides with
`owner_needed: no`, the thread closes, `next` says act without asking the owner, no decision request is created),
`TestGoalLeadCheckContinuesItsConversationAndAnswersAsks` (Builder-chat caller relays, Run chat does not; the relay
turn carries the memory and focus-area rules), `TestGoalWorkAutonomyHoldsOnTheSessionHTTPToolRoute` (change=ask
refuses update_step directly and over the HTTP route; change=auto allows both), `GoalLeadPanel.test.tsx`.

Left / risks:

- Not run live after the change; the Builder's handling of `next` is prompt-held.
- The test wraps the executor the way agentwrapper does; the agentwrapper link itself is by code reading.
- `openPulseChatTab` opens the Pulse session through the scheduled-workflow chat path when no tab holds it yet;
  not checked in the running app.
- Threads do not survive a server restart.
- `selectWorkspacePaneWorkflowTab` picks the active or most recent interactive workflow chat, which can be a Run
  chat when one is focused; that chat then asks Pulse for a recommendation, not as the owner.

## QA and Architecture owned by Pulse; the name is Pulse (on main, not deployed)

Owner decisions (2026-10-07): for workflows with a goal, QA and Architecture belong to the workflow's Pulse
conversation only; and users and agents see "Pulse", not "Goal Lead" (code names keep `goal_lead`).

- **Pass order** (`pulsemodules.PassOrder`, `goal_lead_owns_reviews.go`): a workflow with a goal (soul.md plus a
  primary metric) runs Gate, Goal Work in its Pulse conversation, Finalize; no Architecture or Technical turn. Gate
  is told so and its worklist records both not due; a row a backend rule still makes due (recovery, prompt budget,
  protected boundary) is closed as skipped by code after Gate, which clears its recovery. Workflows without a goal:
  unchanged (Goal Work, Architecture, Technical).
- **Architecture** is the conversation's skill (`goal-lead-architecture.md`, now saying it is the only architecture
  review such a workflow gets). **QA** runs only on `record_pulse_qa_request` (fix run, result back into the
  conversation). The automatic fix runs (`launchDueFixRuns`: open issues, new concerns, failed runs) skip these
  workflows.
- **Safety net.** Each tick, for these workflows, code looks for workflow runs (not Pulse passes) that failed
  (error, failed, interrupted) in the last day and were not seen yet (`goal_lead_run_failures`); new ones get one
  short Pulse turn (`run_failed`, Run/Outward/Change held): request QA when the failure blocks or threatens the goal,
  otherwise one line why it can wait. At most once per failed run. The goal check's and Goal Work's context
  (`goal_lead.run_health`) carries failed runs with their error, steps' `CONCERNS:` lines, the open issue count and
  schedule run health since the last goal check; `goal-lead-check.md` has a "Failed runs" section with the rule.
- **Fast requests** (`record_pulse_fast_request`) still start an earlier full pass, which for these workflows is Goal
  Work in the conversation; nothing else schedules Technical or Architecture for them.
- **Pulse tab.** The status API's `goal_status.goal_lead` hides the Technical and Architecture cards, their detail
  and run-history columns for these workflows; a line says the workflow's Pulse handles QA and architecture and that
  results show in its conversation. Workflow Review and the maintenance issue list stay.
- **Name.** UI (panel, conversation, Needs you, focus areas, decision log), charter, turn headers (`PULSE TURN`),
  skills, tool descriptions and notifications say Pulse (e.g. "Substack Pulse"). `ask_goal_lead` is now `ask_pulse`;
  `ask_goal_lead` stays as a working alias for one release. Slack: `<workflow-slug>-pulse`; `-goal` still works for
  one release.

Risks:

- A failure the Pulse turn judges harmless gets no repair until its goal check or a later failure asks for QA;
  before, a fix run started within 90 minutes. Open issues no longer trigger fix runs on their own for these
  workflows.
- `run_health` reads the run's error text and `CONCERNS:` lines, not per-step error logs; the turn reads the run
  folder when it needs more.
- A failed run during a full pass gets both the pass's Goal Work turn and the short failure turn.
- Not run live. Tests: `TestGoalWorkflowPassHasNoReviewTurnsAndAFailedRunWakesPulseOnce` (pass order with and
  without a goal; one turn for one failed run over three ticks), `TestGoalLeadCheckContinuesItsConversationAndAnswersAsks`,
  `TestToolSetInvariants`, `GoalLeadPanel.test.tsx`.

## ask_builder and more goal facts (on main, not deployed)

**ask_builder** (`goal_lead_ask_builder.go`): the Pulse asks the workflow's Builder chat, the reverse of `ask_pulse`,
by the same function-call mechanism (call id and saved record, chain guard, 20 an hour per workflow, waits up to
120 s, default 90).

- **Target:** the owner's most recently active Builder chat for the workflow (live first, else the latest saved one,
  the chat the web Builder restores), or a named one of the owner's Builder chats (`builder_session_id`, e.g. the
  session a plan change came from). None: a question returns `no_builder_chat`; a fix becomes a decision. No chat is
  created. A Builder chat busy with another turn is refused (ask later).
- **What the owner sees:** the request runs in that Builder chat as a normal turn ("Pulse (question)" / "Pulse
  (fix)"), with the reply under it; the Pulse tab logs the ask and the answer. The answer comes back as the call
  result and is kept in `goal_lead_builder_asks`, so a late answer reaches the next goal check (`builder_asks`).
- **question:** answers from the chat's own conversation; during that turn the Builder chat's tools are held by the
  phase 2 guard with Run, Outward and Change at ask, so it changes, runs and sends nothing. Pulse records the answer
  in goal memory with the new source `builder_answer` ("Builder answer", dated).
- **fix** (message, evidence, a plain title and why): sent only when the turn holds `pulse.autonomy.change=auto`;
  the Builder chat's tools are then held to Change only (no run, no send; deletions, plan replacement and
  migrations stay refused by the guard). At ask (and when there is no Builder chat) it becomes one decision
  (`strategic_review`, `targeted_fixer` with the request as approved scope) with Pulse's recommendation "Make this
  fix"; the owner's Accept sends it to the Builder chat through the existing apply-in-chat path. Refused for
  soul.md, deletions and contract migrations (a narrow text check on the request, plus the guard) and from a
  failed-run turn (questions only there).
- **No ping-pong:** the chain guard refuses asking back the chat that asked; ask_builder also refuses when any
  Builder chat started the exchange with `ask_pulse` (a step that asked may still be followed by a question).

**More goal facts** (`goal_lead_facts.go`, in the goal check context and `get_pulse_state(view="goal_status")`,
code only, since the last check):

- `plan_changes`: planning/changelog entries (tool, reason, steps, who, session); the skill says to ask_builder about
  changes touching goal-driving steps or the metric.
- `owner_answers`: decision requests the owner answered. Owner messages in Builder chats are not collected (their
  transcripts can be tens of MB per chat); ask_builder covers them.
- `spend`: the workflow's cost ledger (PLAT-184 `costs/costs.sqlite`), last 7 days against the 7 before, and runs
  costing at least twice the 14-day median run (`cost_spikes`). Workflows have no budget field, so the trend is
  reported without one.
- `error_rate`: failure share since the last check against the 14-day median daily share (`spike`).
- `login_hints`: runs carry no structured error kind, so a narrow, labelled text match on run errors and
  `CONCERNS:` lines (401/403, unauthorized, invalid_grant, expired token/session/login, re-authenticate, MCP
  connection failures).

`goal-lead-check.md` has one short paragraph per fact and one on ask_builder. Test:
`TestPulseAskBuilderHonoursChangeLevelAndCheckSeesPlanChanges` (fix at change=ask makes a decision whose Accept
message applies it in the Builder chat; fix at change=auto and a question at ask run in the Builder chat with the
right tool hold; fix from a failed-run turn refused; a plan change appears in the check context).

Risks:

- The Builder turn's tool hold is keyed on the chat's session for the turn: an owner message queued behind it waits.
  A busy chat is refused rather than queued, so the hold never lands on the owner's own running turn, but a turn the
  owner starts in the same moment could still race it.
- `userWorkflowChat` reads the saved Builder transcripts to pick the latest; slow on workflows with very large
  transcripts (the same path the bot DMs use).
- Spend per run groups the ledger's `run_id`; events without one (chats, Pulse turns) count in the weekly totals
  only. Login hints are text matches and will miss errors worded differently.
- Not run live.

## Phase 0: Workflow Review before runs; housekeeping out of Pulse (on main, not deployed)

Workflow Review (Plan Drift) is a pre-run check (`cmd/server/workflow_review_prerun.go`):

- Before every run (scheduled runs in `runJob`; `execute_step` / `run_full_workflow` from any chat, Builder, Run or
  a Pulse turn, through `workflowReviewPreRunRegistrar`) a code check reads the due set Pulse's Gate used to be
  forced to act on: `CollectPlanDriftDueItems` (no current drift review, a plan-edit flag, an old contract version,
  new reference-map breaks under the current flags version) plus plan changes without dependency receipts.
  Clean: the run starts, no AI call.
- Otherwise the review runs first: the same reviewer contract (`plan-drift-review.md`) and typed receipts, in its own
  "Workflow Review" conversation with only the `plan_drift_review` worklist row. A scheduled run waits for it; a chat's
  run tool returns at once with "Reviewing the workflow before running…", the review shows as a background job of
  that chat, and its completion notifies the chat to run again. A break the review leaves stops the run with the
  reason (only runs that touch the broken step); a review that does not finish lets the run go.
- One review per plan revision (a hash of the due set and plan.json / step_config.json), stored in `planning/workflow_review.json`; the same state
  is never reviewed twice, so a run never loops. One review per workflow at a time.
- After changes: a tick launcher reviews workflows with an enabled schedule once the plan has been quiet for 10
  minutes (Builder edits, contract upgrades and flags-version bumps all change the due set), one at a time.
- Pulse no longer schedules, waits for or holds anything for it: `pulsemodules.ExecutionOrder` is Goal Work,
  Architecture, Technical; Gate's worklist records `plan_drift_review` not due; Goal Work keeps its levels; the
  fix run is Technical only. `get_pulse_state(view="module")` and the Pulse status API carry the latest review
  (`workflow_review`) as an input.

Backup, publish and notify are each schedule's after-run options (`cmd/server/after_run.go`):

- `after_run: {backup, publish, notify}` on each schedule (and `after_manual_run` for full runs from a chat) replaces
  `pulse_mode`. Notify is code: a run summary from the run's facts through the normal notification path (failures and
  status changes to the channels, routine successes recorded in the dashboard only). Backup and publish are skipped in
  code when the source hash matches the last backup / publish; only when there is something to do does one short
  housekeeping turn run, limited to those actions (provider steps live in `backup-strategy.md` /
  `publish-strategy.md`). The full Pulse finalizer no longer backs up or publishes.
- Migration at server start: basic, and the retired full, → all three on (what the basic finalizer did after a normal
  run); off → none; a legacy full keeps the workflow's own Pulse on. `pulse_mode` is read for one release where
  `after_run` is absent and kept in step with it (so older servers and the contract stamp read the same choice).
  These edits do not write plan changelog entries, so they never start a review. The daily goal check and the full
  Pulse start from `pulse.enabled`, not `pulse_mode`.
- UI: three checkboxes per workflow schedule (list and table views) and a "After a manual run" row; the Pulse tab no
  longer shows backup/publish/notify statuses or a "Workflow Review due" state.

Left / risks:

- Backup and publish to remote providers still need an agent turn when something changed; turning the provider
  playbooks into code is not done.
- The code run summary does not apply the workflow's run-summary content instructions, and no longer offers the
  fast Pulse request after a run.
- Workflows that never ran Plan Drift (Pulse off) get a full review on their first run after deploy; workflows with
  enabled schedules are reviewed ahead by the tick launcher (cost on first deploy).
- A break the review leaves goes to the Workflow Review chat (a Builder-mode chat of the workflow) and the stopped
  run's error; it is not posted into an existing Builder chat.
- The Pulse tab has no separate "recent activity" list; nothing moved to an Activity tab.
- Webhook and Relay deliveries are not reviewed before they run.
- Not run live. Tests: `TestWorkflowReviewRunsOncePerPlanRevisionBeforeARun`,
  `TestPulseModeMigratesToAfterRunOptions`, `ScheduleListView.test.tsx`.


## Run goal check now (2026-10-08)

The daily goal check is skipped while schedules are globally paused (`launchDueGoalChecks` returns early), so the owner never saw one. The goal status card has a **Run goal check now** button (`POST /api/scheduler/workflows/goal-check-run`, write access) that starts the same run as the scheduled check (`TriggerGoalCheck`). With all schedules paused that check is report-only, as designed.
- 2026-10-08: the Pulse chat tab showed whole turn instructions as the owner message; Pulse turns now mark the words a person sent and the chat shows only those (automatic turns show a short label). Enter sends in Talk to Pulse. "Run Goal Work now" on a goal workflow starts Goal Work in the Pulse conversation instead of `/run-goal-work` in the Builder chat.

## Two agents talking (2026-10-08)

The owner: "i just want two agents talking with each other". The Builder ↔ Pulse
protocol (threads, rounds, `decision:` / `owner_needed:` lines, question/fix kinds,
per-message rules, the grouped transcript block) is removed. `ask_pulse` and
`ask_builder` take one plain message; it arrives with its sender and the reply comes
back. Pulse's charter, its owner-message rules and its permission levels are in its
system prompt (`goalLeadSystemSection`, added to the workflow phase prompt; its hash
joins the chat policy fingerprint). The owner's messages reach Pulse as typed. While
the Builder chat handles Pulse's message, its tools are held to Pulse's levels.
Tests: `TestGoalLeadCheckContinuesItsConversationAndAnswersAsks` and
`TestPulseTalksToTheBuilderChatWithinItsLevels` check the behaviour, not wording.
- 2026-10-08 (owner): Pulse's icon is a person at work (`UserRoundCog`, `components/workflow/pulseIcon.ts`) instead of the heartbeat line, in the Pulse view, its toolbar entry, SparkQuill, and on the "<workflow> Pulse" chat tab.
- 2026-10-08 (owner's live Substack test: "expecting more autonomy"): (1) the PLAT-608 shadow tool gate set a surface name that made every Goals Builder/Run chat and Pulse lose `notify_user` since 2026-10-06; fixed. (2) Pulse's charter forbade calling tools through the bridge's HTTP path, which is how its CLI reaches them, so it could not record its goal check; the line is removed (the server guard still checks those calls). (3) Paused schedules no longer drop Pulse to report-only; it keeps its permission levels and only leaves the schedules to the owner.
- 2026-10-08 (owner): Pulse's system prompt is its own, about the goal only (the Pulse section, a short workspace note and the runtime pointers), not the Builder prompt with a Pulse section appended (`buildWorkflowPhaseSystemPrompt`, `PulseConversation`).
- 2026-10-08 (owner: "pulse can itself just have access to read only stuff; it can ask builder to do all these things"): Pulse's tools are an allow-list (`pulseTools`): read tools, its own records, `notify_user`, `create_human_input_request` and `ask_builder`. It changes and runs nothing itself; its level text, charter, check prompt and skills say to ask the Builder chat. Left: the CLI's shell can still write files in the workflow folder (the sandbox is the Builder's); a read-only sandbox for Pulse is a follow-up.
- 2026-10-08 (owner: "enable all this via product yml"): Pulse's tools and writable folders live in product.yaml `pulse:` (`tools`, `write_paths: [pulse/, memory/]`), loaded by `agentworksproduct.PulseTools` / `PulseWritePaths`. Its shell folder guard and CLI sandbox write only those folders; the rest of the workflow is read-only to it (closes the shell gap).
- 2026-10-08 (owner: "pulse mainly talks to builder"): Pulse has no `notify_user`; when the owner should know, it tells the Builder chat, which decides whether to notify. It gained `get_function_call` to read a late `ask_builder` reply by call_id.
- 2026-10-08 (owner): five read-only Pulse skills in builder-reference: goal-lead-inspect (runs, iterations, did a step work, debugging, hand-off to the Builder), goal-lead-measure, goal-lead-funnel, goal-lead-experiment, goal-lead-costs. The charter lists all eight skills; the goal check points at them; Goal Work defers test rules to the experiment skill.
