# Workflow improvement through Pulse

Implemented first release: 2026-09-10. Platform tracking: PLAT-305, PLAT-303
and PLAT-326 (persistence simplification, 2026-09-18). Last synchronized with
code: 2026-09-23.

> **2026-09-23:** Strategy is now **Goal Work**, Pulse's main job: it does work
> that moves the user's goals within permission levels, follows up whether it
> worked, and challenges `soul.md` constraints with evidence. It runs right
> after Plan Drift and is not blocked by it. The full Pulse runs on its own
> self-deciding schedule; normal schedules only back up, publish and notify.
> See [design/pulse_goal_work.md](design/pulse_goal_work.md). Sections below
> describe the platform-upkeep reviewers and older history.
>
> **2026-09-24: find, fix, close.** Every issue ends the pass that finds it as
> fixed, not a problem, the user's decision, or platform-owned; the old
> queued, blocked, proposal, waiting-for-run and waiting-for-evidence states
> are retired. Fix runs (a short Technical Review+Fix) start automatically when
> a workflow has open issues, new step concerns or a failed run. The user sets
> Pulse autonomy with one slider, Goal Work runs on the Pulse model and upkeep
> reviews on the Medium tier, and the chat keeps only `/pulse` and
> `/run-goal-work`. Details: the "Find, fix, close" and "Models" sections of
> [design/pulse_goal_work.md](design/pulse_goal_work.md). Where the sections
> below mention queueing or waiting, the new rules win.

## Responsibilities

| Perspective | Question | Output |
|---|---|---|
| Plan Drift | Did plan changes leave dependent contracts inconsistent? | Scoped drift results and safe corrections |
| Technical | Is required behavior correct? | Repairs of concrete correctness failures and regressions, or a canonical open issue |
| Architecture | Can this working approach be built better? | Evidence-backed prompt, orchestration, script, learning, KB, DB, report, model-tier or efficiency proposals |
| Strategy | Is this the right approach to the goal? | Goal-derived conclusions, researched alternatives, measurement proposals |

Plan Drift preserves an approved plan after a change; Technical repairs concrete
execution failures; Architecture proposes a better technical structure for an
otherwise working plan; Strategy owns goals, outcomes, measurement, assumptions
and alternatives. Execution-tier/model choices belong to Architecture.

Architecture has a canonical `architecture_review` identity. Existing
technical/strategic history and legacy aliases retain their meaning. Historical
technical optimization reviews are not silently reassigned to Architecture.

## Pass order and scheduling

A Pulse pass runs on the workflow's own Pulse schedule (`pulse.schedule`,
self-deciding by default: each pass picks its next run with
`record_pulse_next_run`, bounded to once a day and once a week) or from a
manual launch. Normal schedules are `off` or `basic` (backup, publish, notify)
only. Code: `pulse_schedule.go`, `EffectivePulseMode` in
`workflow_manifest.go`, `pulsemodules.ExecutionOrder` and
`cmd/server/scheduler.go`.

1. **Gate** reads compact evidence and records one worklist with a decision per
   module. It does not review, fix, or launch reviewers.
2. **Plan Drift** is due whenever `plan_drift_candidates` is non-empty (the
   backend rejects a worklist that says otherwise). When it is due,
   Architecture and Technical wait for the next pass so they never judge a plan
   already known to drift.
3. Then the due reviewers run in order **Goal Work (strategic_review) →
   Architecture → Technical**, each in its own blocking turn of the Pulse
   conversation: the Pulse agent does the review itself and may use its own
   subagents (PLAT-452). While Plan Drift is due only Goal Work runs, without
   permission to run workflow steps.
4. **Finalizer**: Backup, Publish, Notify.

Each stage checks only its own result and records only its own interrupted
recovery. Technical repair debt cannot fail a strategic/architecture completion.
A failed earlier review leaves the pass partial but does not cancel later
research. Explicit user cancellation does.

Research horizons are asymmetric without new cron jobs. Architecture normally
waits across several comparable producing runs for stable structural evidence.
Strategy is reconsidered at the next meaningful goal, outcome, feedback,
experiment, decision or measurement checkpoint. Material evidence can override
either wait.

## Per-reviewer controls

Technical, Architecture and Strategy each have a durable **Run automatically**
toggle and an independent **Run now** action; a disabled reviewer can still run
once by hand. Plan Drift is mandatory, cannot be disabled, and has its own
manual drift-check action. While Plan Drift is due or unavailable, the three
downstream Run now actions are disabled. Manual actions reuse the same guided
review contracts in the workflow's Builder chat.

Installed playbook custom focus applies to Strategy only; Technical and
Architecture keep their platform-owned scopes.

Strategy has a symptom-loop guard: a recurring operational label is Technical
context, not a strategic agenda. The reviewer hands off the concrete defect,
assumes it is fixed, and asks what would still limit the primary goal. Every
completed Strategic Review must contain a goal-derived conclusion (an honest
no-change conclusion counts); a technical handoff alone is not completion.

Use `next_check_at` for protected Architecture/Strategy boundaries. A skipped
Gate cannot push an outstanding saved date forward; at the next Pulse pass after
that date it becomes due for an evidence assessment. A selected unfinished review
also remains due. An explicit `defer_reason` with a future date can postpone the
assessment for a concrete evidence/critical-condition reason. It is recorded in
the worklist reason. A completed review may establish a new boundary.

This reuses existing full/periodic Pulse triggers; it does not create new cron
jobs or invent a fixed universal frequency. Date checks happen at a Pulse pass,
not via an additional wall-clock daemon. Legacy evidence/run-count boundaries
remain Gate judgments; choose dates for the protected scheduling behavior.

## Research access and write boundaries

Scheduled review dispatch is a turn in the Pulse conversation, not a separately
launched reviewer (the `run_in_background` tool was removed, PLAT-452). The review
therefore holds the Pulse conversation's tool set. Architecture's read-only
limit is stated in the review step's text and held by the agent. Goal Work's
`pulse.autonomy` levels are enforced at tool dispatch for the length of its turn
(PLAT-697 phase 2, `cmd/server/pulse_autonomy_guard.go`): with Run at ask the run
tools refuse, with Change at ask the Builder plan, step-config and schedule tools
refuse, with Outward at ask Slack posts, Google writes and notifications to other
recipients refuse; a due Plan Drift holds Run and Change. The catalog itself is
never narrowed per turn; the call is refused with a hint to create a decision
request. Browser, shell and connected-MCP actions are not classified and stay
held by the agent. Architecture and Strategy use the
workflow's configured MCP connections and browser setup. Raw implementation
writes remain blocked by the existing workspace guard, and Run mode's stricter
write limit remains.

External MCP permissions remain those of the configured connection. This does
not introduce a universal read-only proxy for arbitrary external tools or browser
UI actions. The research contract prohibits external business mutations and
preserves existing authorization checks. Browser interaction still uses the
workflow's shared profile and existing browser policies.

Research starts with a question, saves dated sources and distinguishes observed
facts from hypotheses. Concise reasoning is saved through review_note in SQLite and appears directly
in the Pulse report reader. Runtime interruption tracking references the source
run and saved notes; legacy Markdown files are optional historical evidence. Reuse current
research instead of automatically repeating it.

## Persistence: reviews, issues, decisions (PLAT-326)

The 2026-09-10 release introduced an impact ledger (`fix_bundle`,
`architecture_improvement`, `strategy_experiment`) with a
proposal → approval → application → assessment → adoption lifecycle. PLAT-326
(2026-09-17/18) collapses Pulse to a much smaller product lifecycle:

```text
review concludes -> issue open -> action taken -> issue closed
```

There is no separate verification, monitoring or fix-attempt lifecycle. If
Pulse cannot complete a fix, the issue stays open; a later review updates the
same canonical issue instead of filing a second one.

| Table | Holds |
|---|---|
| `pulse_reviews` | One user-readable result per Plan Drift / Technical / Architecture / Strategic review. Activity reads these directly. |
| `pulse_issues` | Canonical issue: description, evidence, `open`/`closed`, `action_taken` when closed. |
| `pulse_decisions` | Human decisions, each tied to an open issue. Approval leaves the issue open until the action is applied; rejection records the choice as `action_taken` and closes it. |

A reviewer now has three persistence actions: record one concise review result,
open or update a canonical issue, and record a human decision only when one is
genuinely required. `record_pulse_result` is also the Activity summary; there is
no separate publish/update step. `record_pulse_impact` is no longer registered
for reviewers or Workshop.

**Rollout state (2026-09-23):**

- Done: schema v2 migration (runs at server startup and on each Pulse DB open,
  backs up to `db/migrations/.backups/` first), backfill of the three tables,
  and the reviewer skill/tool cutover. All local workflow databases are migrated.
- Pending: switching every Gate/UI/Activity read to the three tables, removing
  the legacy compatibility writes, and dropping retired tables after the
  rollback window. RTS and other hosts migrate when the code is deployed there.
- Until then, legacy tables (module audit, review notes, finding details,
  impact records, and so on) remain and are still written behind the compact
  contract, so some UI and API fields below still come from them.

## UI

The Pulse workspace leads with goals, metrics and Strategy, which opens by
default. Strategic proposals appear directly under the Strategy card. Technical,
Architecture and Plan Drift are grouped as platform health and stability, with a
lower **Platform improvements** section for Technical and Architecture work.
Goal progress shows primary metrics first; supporting metrics stay collapsed
until their primary is opened. Review history stays collapsed.

## Validation and rollout (2026-09-10 release)

Automated integration coverage exercises four due modules, independent receipts,
protected dates across actual worklist persistence, research custom-tool filtering,
Architecture finding/decision identity, and proposal → approval → application
receipt → assessment → adoption. It rejects application without a consumed
approval and adoption without outcome evidence. UI tests distinguish approval,
application and inconclusive outcomes and cover the three areas plus Drift.

Production rollout and observation of an actual autonomous workflow improvement
remain required. Synthetic lifecycle evidence tests persistence and transitions;
it is not a claim that business outcomes have improved.

## Remaining foundation work

- PLAT-047/089: immutable physical partial-attempt evidence and uncontaminated
  execution attribution. This release does not restore deleted/overwritten data.
- PLAT-257/298: broader builder prevention and legacy workflow migration. Existing
  plan/dependency validation is reused; comprehensive pre-schedule acceptance and
  automatic learning/KB contradiction prevention are not added by this release.
- Continue measuring proposal application and outcomes on real workflows rather
  than treating the number of reports or ideas as success.

## Minimal review recording — PLAT-306

`pulse_review_notes` is on PLAT-326's retirement list, but reviewers still read
and write notes this way until the cutover completes.

Reviewers save information once. `record_pulse_result` already records the short
`reason`, evidence and actual outcomes; optional `review_note` adds only reasoning,
limitations and next questions that are not in the existing records. It is stored
in SQLite atomically with completion and shown directly in the report reader.
There is no mandatory Markdown file, fixed template or separate reporting turn.

At review start, `get_pulse_state(view="review_notes", module=...)` returns the
latest three notes/conclusions; optional `pulse_run_id` selects an exact prior
run. Missing older notes are normal, not a request to reconstruct history.

Only when a long investigation needs working context saved, the same result tool
accepts `note_only=true, result="running", reason=..., review_note=...` with the
normal workspace/module/run identity. This does not mark completion or change
findings. The next completion can replace that note; omitting review_note preserves
it. Runtime interruption state remains separate. Legacy Markdown reports remain
readable; no files are deleted or bulk-converted. See the ticket for validation
and the remaining live overhead measurement.
