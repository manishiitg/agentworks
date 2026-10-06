[← goals / human-decisions](index.md)

# PLAT-093 — answered decisions were applied after the run, so the run they were meant to change had already happened

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | human-decisions |
| Summary | P1 — the operator's decision lands a full cycle late, and for a |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — structured pre-run routing and targeted-Fixer contract shipped; runtime reverify pending |
| Last synchronized | `2026-08-25` |

- **Priority:** P1 — the operator's decision lands a full cycle late, and for a
  cadence decision that means the problem they asked to fix repeats every run
  until then
- **Owner:** scheduler pre-run turn sequence (`scheduledWorkshopTurns`,
  `executeWorkshopJob`)
- **Follows:** [PLAT-092](plat-092.md), which stopped answered decisions being
  silently re-read forever but drained them in the **post-run** Pulse pass

## The gap PLAT-092 left

PLAT-092 gave Review+Fix an explicit drain contract. That fixed *whether*
decisions get applied, but not *when*: Pulse runs after the workflow, so an
approved change lands on the next cycle at the earliest. The run that was
supposed to benefit had already executed on the old behavior.

The stranded backlog shows why that ordering is wrong. **24 of the 26 answered
decisions require an action, not an acknowledgement** — only 2 are pure
rejections. And what they change is what a run *does*:

- `ops-hourly-cadence-overrun-2026-08-05` → `evals-once-daily`. Tectonicus's
  hourly runs overrun the hour, so the operator gets one snapshot a day instead
  of six. Applied post-run, the overrun repeats every hour until the next Pulse.
- `plan-proposal-panel-label-and-benchmark-backfill` → `approve-both`. Switches
  the scoring unit from R-multiples to percent-vs-benchmark. Applied post-run,
  the run that just finished was still scored the way the operator said was
  "stacked against the very trades it is judging".
- Others: `activate`, `turn-on`, `provide_token`, `B-rank-based`,
  `graded_all_three`, `recent_anchor`, `approve-v2`, `approve_rebuild`.

## Why "try, don't classify"

There is no `apply_timing` field, and adding one — or inferring pre/post from
keywords — would be guesswork. Theme-based classification has already misfired
here: the PLAT-072 triage sweep matched 11 of 81 findings and misattributed
several.

It is not needed, because the asymmetry is one-sided. If a decision changes
what the run does, applying it before the run is strictly better. If it does
not, applying it before the run is harmless. There is no case where waiting is
better. So the drain attempts everything at the earliest opportunity, and the
exceptions identify themselves at attempt time: needs evidence from a run that
has not happened, premise went stale, or intent is ambiguous. **The attempt is
the classification.**

## Original fix shipped

`scheduledDecisionDrainTurn` builds a blocking pre-run turn, inserted by
`executeWorkshopJob` **after** any contract upgrade and **before** the first
schedule message. It only exists when there are answered decisions, so an
ordinary run pays nothing.

Applying is an agent turn, not Go, for a concrete reason: the decision's
`context` field holds a prose "what happens next if you approve" section
written for the operator, not a machine-readable patch, and the typed plan,
config, eval and schedule tools it needs already exist. The contract-upgrade
preflight is the proven precedent for mutating plan artifacts before the first
schedule message.

Ordering is deliberate — an upgrade can rewrite the very artifacts a decision
edits, so upgrades go first.

## Decision history

| Date | Decision | Evidence and reason |
|---|---|---|
| 2026-08-18 | Let the post-run Review+Fix pass consume answered decisions. | This closed the original PLAT-092 loop, but runtime evidence showed the decision only affected the following run; the run it was intended to change had already used the old behavior. Superseded for application timing. |
| 2026-08-20 | Add an agentic pre-run decision drain after contract upgrades and before schedule messages. Do not classify decisions mechanically in Go. | Decision context is prose and the required typed workflow tools already belong to the agent. Trying each answered decision is safer than inventing an `apply_timing` field or keyword rules. This remains the current ownership model. |
| 2026-08-20 | Treat safe static proof as part of application, not future evidence. | Social Media's approved flattening was deferred because a non-producing fixture had not run, causing the expensive old topology to execute again. Only proof requiring a producing run or external side effect may be deferred. |
| 2026-08-20 | Require `validate_plan_change` after structural decisions. | The first flattening repair changed control flow but left stale allocator paths, incomplete dependencies, and obsolete identifiers. The agent still chooses the design; the typed validator proves the invariants it declares. |
| 2026-08-20 | Link measurable applied decisions to Pulse impact using `human_input_id`, and show the joined lifecycle in the Pulse UI. | `outcome_summary` already records the action and the impact ledger already records interventions and assessments. Joining those canonical records avoids a second UI-only status and lets users see decision → action → later measured result. Rejections and non-measurable administrative changes do not fabricate impact. |
| 2026-08-25 | Replace generic prose-driven application with reviewer-authored `apply_contract` routing. | Direct setting changes can still apply before the run, but prompt/plan/route/validation/database/tool/cross-artifact changes get a dedicated Targeted Fixer. Unknown legacy prose is not auto-applied. |
| 2026-08-25 | Make approved Targeted Fixer handoffs mandatory intake for manual `/pulse-fixer`. | A manual Fixer previously selected only `repair_eligible` backlog entries, while an approved decision's linked finding remained `awaiting_user`; it could therefore fix unrelated work and skip the approval. The new durable intake tool resolves answered targeted decisions to their linked PUL issue and makes that bundle first. |

Safety boundaries, all pinned by tests:

- **It cannot cost the run.** If the drain fails to start or fails to settle,
  the scheduler logs it and continues to the schedule message. The decisions
  stay answered-and-unapplied, exactly as before this turn existed, and
  PLAT-092's post-run drain still sees them.
- **It carries the operator's actual answer**, not just the decision id, so a
  rejection cannot be applied as an approval.
- **It must not consume what it did not apply** — consuming to tidy the list
  would erase a decision while recording that it was honored.
- **It must not run the workflow**, execute steps, back up, publish or notify.
- **Staleness is checked** against the decision's own `run_id` and
  `answered_at` versus the current plan, so a month-old proposal is not blindly
  applied to a plan that has moved. 21 of the 26 carry a `run_id`; the 5 that
  do not have no staleness anchor and should be read rather than auto-applied.

The applied action and its later effect are now one visible lifecycle rather
than two unrelated records. `mark_human_input_consumed.outcome_summary` remains
the authority for **what action was taken**. When that action has a defensible
metric, the same agent also opens a `pulse_interventions` record linked by the
decision's `human_input_id`, with an honest baseline, expected direction,
checkpoint, and evidence threshold. It starts as awaiting evidence or
measuring; application never claims success. Later Pulse passes append
observations and an assessment after comparable evidence matures. The Pulse UI
joins those existing records and shows `decision → action taken → impact`,
including improved, unchanged, regressed, inconclusive, or confounded. Pure
rejections and administrative changes do not get invented metrics.

## 2026-08-25 correction — deterministic intake, not "try everything"

The 2026-08-20 decision to attempt every answered decision agentically was a
necessary bridge, but it placed too much authority in operator-facing prose.
It is now superseded for new structured decisions. The scheduler reads the
durable `apply_contract` and routes deterministically **after contract upgrades
and before the first schedule message**:

| Contract mode | Pre-run behavior |
|---|---|
| `no_change` / `direct_apply` | one existing decision-drain turn, limited to the bounded defined change or truthful no-change outcome |
| `targeted_fixer` + `approve` | one dedicated scope-bounded Fixer turn for that decision, including its issue, static checks, proof boundary, and failure policy |
| `targeted_fixer` + reject/defer | direct no-change handling; never invoke a Fixer |
| `external_wait` or legacy prose | no automatic mutation |

The Targeted Fixer may make only the approved repair, must perform the named
non-producing proof, re-read the changed artifacts, validate planning changes,
and consumes the decision only when the applied outcome is truthful. It cannot
run workflow steps, public actions, broad Pulse, backup, publish, or notify.
The contract explicitly chooses `continue_unchanged` (the normal case) or
`block_run` for the rare repair whose failure makes the old plan unsafe to run.

This removes the contradiction in the earlier “try, don't classify” rationale:
the scheduler does not infer a repair from keywords. The reviewer that created
the decision supplies the durable, typed routing contract instead. A narrow
one-time migration assigns the proven prompt-contract-consolidation namespace
to the Targeted Fixer; all other legacy prose stays manual.

### Manual Fixer parity

The same contract now applies when the operator deliberately runs
`/pulse-fixer`, rather than waiting for a schedule. At startup the command
reads `list_approved_fixer_decisions` once. Every returned `targeted_fixer` +
`approve` decision is mandatory first intake, even while its linked finding is
still `awaiting_user` and therefore absent from the ordinary repair-eligible
queue. The tool resolves the linked public PUL id from the durable
`awaiting_user` event, so a reviewer can create the human decision before the
finding has its PUL id. The Fixer then reads the exact decision and exact PUL
record, applies only that scope, and leaves the decision unconsumed if proof
does not pass. It must not substitute other eligible repairs for that handoff.

## 2026-08-20 live regression: safe proof was mistaken for future evidence

Social Media exposed a narrower but expensive hole in the original contract.
The operator approved `ops-decision-flatten-execution-pipeline`, which replaced
a predetermined outer `todo_task` with explicit ordered steps. The pre-run
drain read the answer but left it unapplied because a **non-producing structural
fixture** had not run. It then allowed the old outer orchestrator to execute.
That run spent roughly $94 of its $112 total on orchestration overhead before
the topology was repaired later by an unrelated workflow-builder turn.

The deferral was wrong. A static check, dry-run, schema validator, plan review,
or non-producing fixture is proof the pre-run agent can perform itself. It is
not future evidence. Only proof that inherently requires a production run or an
external side effect may be deferred.

The same repair also revealed why "change the next-step links" is insufficient
for structural decisions. The flattened Social Media plan still retained old
nested allocator paths, incomplete `context_dependencies`, and an obsolete step
id in promoted consumers. Because iteration folders are reused, those stale
references can bind a new flattened run to artifacts from the old nested run.

`scheduledDecisionDrainTurn` now requires a structural impact audit for changes
to topology, routes, ids, dependencies, paths, or orchestration shape:

- migrate **control flow and data flow** together;
- declare exact `context_dependencies` for promoted consumers;
- update step config, evaluation, report, schedule, validation, and prompt
  references;
- search for removed step ids and obsolete path prefixes;
- test old/new artifact coexistence so stale output cannot win;
- re-read and validate the resulting plan; and
- leave the decision unconsumed if any unexplained old reference or failed
  proof remains.

The agent now closes that audit with `validate_plan_change`. This is a typed,
non-producing validator, not a Go design heuristic: the agent declares the
removed identifiers/path prefixes and the exact dependency contract it intends,
then the validator proves those invariants against the persisted plan, step
config, evaluation, report, DB contract, workflow config, soul, global learning,
and KB index. It also runs the ordinary plan graph validator. The receipt fails
when a forbidden reference remains, a changed step is absent, or its dependency
set differs. It does not decide which topology is best and does not replace a
decision-specific fixture.

## Not fixed here

- The 26 existing stranded decisions are still stranded. The next scheduled run
  of each workflow will now attempt them, which is the intended path, but
  several are month-old proposals whose premise may not hold — expect some to
  be left with a reason rather than applied.
- Whether `goal_advisor` should be un-suppressed. It remains the designated
  handler for its own answered decisions and is still off.

## Verification

- `go build ./...` clean.
- Decision-drain tests (`plat093_decision_drain_preflight_test.go`): no turn when
  nothing is pending; the operator's selected option travels with each id; the
  prompt forbids running the workflow and forbids consuming what was not
  applied; the turn sits after upgrades and before schedule messages; and safe
  structural proof is performed during application rather than deferred.
- Two typed-validator tests (`plan_change_validation_test.go`): a coherent
  migration produces `passed=true`; stale references plus dependency drift
  produce a failing receipt with each concrete mismatch.
- Full suite: 24 failures, all accounted for (22 known baseline + 2 from
  another session's in-flight work), zero unexplained.
- **Not yet reverified live** — needs a restart, then a scheduled run on a
  workflow that has answered decisions waiting (tectonicusadaytrading has 8).

## Acceptance

- A structured decision the operator approved is applied before the next run of
  that workflow, not after it, by the contract-authorized path.
- A failed repair follows its explicit policy: normally it leaves the verified
  old plan and continues; only `block_run` prevents the run.
- Manual `/pulse-fixer` selects an approved Targeted Fixer handoff before any
  ordinary eligible repair; approval cannot be skipped by its `awaiting_user`
  lifecycle label.
- An ordinary run with no answered decisions runs no extra turn.
- Legacy prose and external-wait decisions never create guessed pre-run edits.
- Applied decisions show the truthful action immediately and, when measurable,
  show an awaiting-evidence checkpoint followed by Pulse's latest assessment.
