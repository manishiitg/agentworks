[← goals / pulse / reviews](index.md)

# PLAT-115 — decouple Gate/Review+Fix/Finalize from every run's own session

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | goals |
| Area | pulse/reviews |
| Summary | P2 — not a bug fix, a structural option. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — periodic mode is now mandatory for every workflow (policy change, see below); bootstrap is owned by Gate's own normal pass, not a dedicated migration turn |
| Last synchronized | `2026-08-16` |

- **Priority:** P2 — not a bug fix, a structural option. Adopting it on a
  frequently-run workflow is what actually prevents recurrence of two real
  incidents this register already tracks.
- **Owner:** scheduler Pulse orchestration (`runPostRunMonitor`,
  `scheduler.go`), workflow manifest (`workflow_manifest.go`), schedule
  tools (`interactive_workshop_manager.go`, `workflow_schedule_tools.go`)

## Why

Every scheduled workflow run has always been followed by one continuing
session that runs Gate → Review+Fix → Finalize before the next thing can
fire. For a workflow run frequently enough that sessions get reused across
many hours, this directly caused two real, separately-investigated bugs:

- **PLAT-113**: a Pulse-adjacent session stayed open for ~5 hours and
  deadlocked on its own input lane, blocking the next scheduled run.
- **PLAT-114**: the same kind of long, reused session evicted its own
  background agents' completion records once a 200-event UI cache filled
  from later activity in the same session — verified live while checking the
  quality of a completed Pulse pass, where the only way to reconstruct what
  actually happened was reading the git diff by hand.

Both trace to the same root shape: heavy review work sharing a session with
the run it's reviewing. The fix is a genuine split: every run keeps a fast,
reliable backup+notify pass; the full review runs separately, on its own
cadence, in its own session.

## What shipped

**A workflow-level toggle** — `post_run_monitor_mode` on `WorkflowManifest`
(`"per_run"`, the default and today's exact unchanged behavior, or
`"periodic"`). Under `"periodic"`, every run's own pass runs only backup +
a short factual notification (`postRunMonitorLightweightFinalizeStep`) — no
Gate, no reviewers, no Fixer, and no findings narrative, since none ran.

**A schedule-level marker** — `PulseReviewOnly` on `WorkflowSchedule`, which
reuses the exact plumbing the manual "Run Pulse now" trigger
(`TriggerPulseNow`) already exercises: skip workflow execution, set
`sctx.PulseOnly`, run the full Gate/Review+Fix/Finalize chain. Unlike the
manual trigger, it never pins Gate to one run folder.

**Gate reviews a backlog, reasoning about what's new itself** — no new
cursor or queue table, by deliberate design decision made in conversation
before implementation. `postRunMonitorBacklogGateStep` hands Gate a listing
of every currently-existing `runs/iteration-N/` folder (name, status,
started_at, completed_at) — `iteration-0` only when its own metadata is
terminal, since it's the live/reused slot, never a stable identity across
time. Gate compares this listing against `get_pulse_state`'s own
`last_checked_at` per module to decide what's genuinely new, the same kind
of judgment call it already makes for mode selection — Go's only job is
handing over what currently exists on disk. `pulse-gate.md` also has Gate
check its own review interval against `run_retention_count`: rotated run
folders beyond that count are permanently deleted by the executor
(`controller_run_manager.go`), so a periodic pass reviewing less often than
folders get deleted from under it would silently see a partial sample.

**Both schedule tools gained `pulse_review_only`** — `create_schedule` and
`update_schedule`, on both the workshop-facing (`interactive_workshop_manager.go`)
and chat-facing (`workflow_schedule_tools.go`) tool surfaces, threaded with
the same `setX`/pointer pattern PLAT-097 established for `messages`. A
`PulseReviewOnly` schedule never runs the workflow, so `group_names` is
exempted at all three places that otherwise require it —
`create_schedule`'s own check, `update_schedule`'s final unconditional
revalidation, and `ValidateManifest` itself; a test written against the
real implementation (not just the tool-layer guard) caught the third one
before it shipped.

**`update_workflow_config` gained `post_run_monitor_mode`** — the write side
needed to exist for any of the above to be reachable through a normal turn,
not just by hand-editing `workflow.json`.

**Rollout reuses the existing contract-upgrade mechanism, not a new one.**
`/pulse-setup` now considers periodic mode at setup time for new workflows.
For existing workflows, a new upgrade rung (`upgrade-periodic-pulse-review`,
contract version 1.0.26) reads real run history (not nominal cron alone),
and only if a workflow runs frequently enough for the split to matter,
creates the review schedule and switches `post_run_monitor_mode` in the
same turn — never one without the other, since a workflow left in
`"periodic"` mode with no review schedule yet gets a lightweight pass every
run and a full review from nothing, ever. A workflow that reviews cheaply
today is expected to say so and stay on `per_run`, the same "keep the
current model with a stated reason" pattern every other contract upgrade in
this register already allows.

## Policy change, same day: periodic mode is mandatory, not opt-in

Everything above described the original design: a per-workflow, frequency-
gated agentic decision, rolled out through a dedicated contract-upgrade
migration turn. Both halves of that changed after the first real migration
ran and revealed the problem directly.

**What actually happened when the migration ran.** All three real workflows
that reached the rung (social-media, build-in-public, upwork) independently
decided to stay on `per_run` — every one of their schedules tops out at a
handful of runs a day, below the "roughly hourly or more" bar the upgrade
prompt used. That decision was faithful to the rule as written. But it
exposed that the rule itself used the wrong signal: **PLAT-113's original
5-hour deadlocked session — the incident that motivated building this whole
feature — happened on a workflow that doesn't run hourly either.** The real
risk was never "how often does the cron fire"; it's "can this session end up
staying open or reused for a long time regardless of cron frequency"
(background agents, deadlocks, a stalled platform bridge per PLAT-116). Cron
frequency was measuring the wrong thing, so a frequency-gated opt-in was
never going to protect the workflows actually at risk.

**Decision: periodic mode is now mandatory for every workflow, no
exceptions.** A normal scheduled run does backup, publish, and notify —
nothing else. Gate, Review+Fix, and Finalize never run inline with a normal
scheduled run again, regardless of how rarely that workflow runs.
`per_run` stops being a valid end-state for any real workflow going forward.

**The rollout mechanism changed too, not just the policy.** The original
design used a dedicated contract-upgrade migration turn
(`upgrade-periodic-pulse-review`) to make and apply this decision once per
workflow. That is now redundant: the lightweight per-run finalizer already
re-checks the review schedule's cadence on an ongoing basis (see below), but
it can only do that once a workflow is *already* periodic — it can't
bootstrap a `per_run` workflow into periodic mode by itself. Rather than keep
a separate version-gated migration turn around for that one-time transition,
the bootstrap moved into **Gate's own normal-run flow**
(`pulse-gate.md`, "Bootstrapping periodic mode"): the first time Gate runs a
normal `per_run` pass for a workflow not yet on `"periodic"`, it creates the
review schedule and flips the mode itself, as part of that same pass, then
finishes the rest of the pass exactly as any other `per_run` run — one more
full run, then periodic from then on. No dedicated migration turn is needed
at all; the old rung (`workflow_version_upgrades.go`,
`upgradePeriodicPulseReviewHandoff`) is now a trivial version stamp kept
alive only because `scheduledWorkshopTurns` requires every workflow below
current to have a complete upgrade path — its own content explicitly says
Gate owns this now, not the migration.

**A fourth, ongoing responsibility rides along in the lightweight
finalizer.** The review interval chosen at bootstrap (or at `/pulse-setup`
time for new workflows) is a judgment call about actual run volume, and
volume can drift after that choice was made. Every lightweight
backup+publish+notify pass now also cheaply re-checks the review schedule's
cron against the workflow's own recent `get_schedule_runs` history and
adjusts it via `update_schedule` when warranted — explicitly optional, most
passes change nothing, so a workflow that starts running much more or less
often doesn't silently outgrow a stale interval forever.

`/pulse-setup` (`workflow-tools.md`) was updated to match: a brand-new
workflow gets both schedules created together, unconditionally, at setup
time — no frequency judgment offered there either.

## Follow-up fix: publish was skipped too broadly

The first version of `postRunMonitorLightweightFinalizeStep` marked `publish`
skipped unconditionally, mirroring `postRunMonitorNoRunSteps`'s precedent.
Checking `WorkflowPublishConfig.Targets` and `publish-strategy.md` properly
(prompted by review) showed that was wrong: `publish` covers two independent
targets — `"pulse"` (the Pulse findings dashboard, genuinely stale under
periodic mode's per-run pass) and `"report"` (this run's own execution
output, fresh every run regardless of Pulse) — and `publish-strategy.md`
recommends publishing both by default. Blanket-skipping would have left any
workflow publishing a `"report"` target stale for the entire gap between
periodic Pulse passes, which has nothing to do with periodic mode's actual
justification (short Pulse-adjacent sessions) and reintroduces exactly the
kind of staleness this feature isn't meant to cause.

Fixed: the lightweight finalizer now instructs publishing the `"report"`
target normally, every run, and skips only the `"pulse"` target specifically
— the whole command is marked skipped only when `"pulse"` is the sole
configured target.

## Not fixed here

- **No real workflow has been bootstrapped into `"periodic"` mode yet.**
  Social-media, build-in-public, and upwork all still show
  `post_run_monitor_mode: null` as of this writing — the bootstrap fires
  inside Gate's next normal `per_run` pass, which hasn't happened again
  since this policy change landed. This is a prompt-guidance change, not
  something applied directly to any live workflow's state.
- **Retention and cadence are not automatically kept in sync.** Gate's
  self-check (and now the lightweight finalizer's ongoing re-check) surfaces
  a mismatch as a `workflow_review`/`llm_ops_review` finding, or adjusts the
  schedule directly via `update_schedule`; nothing enforces a floor beyond
  that agentic judgment.
- **A workflow's own `post_run_monitor_mode` and its schedules'
  `PulseReviewOnly` markers can still be set independently and
  inconsistently** — e.g. `"periodic"` with no `PulseReviewOnly` schedule.
  Gate's bootstrap pairs them atomically when it fires, but nothing detects
  or repairs a mismatch that arises some other way (manual edit, a schedule
  deleted later).
- **`update_workflow_config`'s field has no dedicated unit test** — it
  follows the exact same manual-JSON-manipulation pattern as its
  already-untested sibling `post_run_monitor` in the same file; the
  underlying read-side logic it writes (`PostRunMonitorIsPeriodic`) is
  thoroughly tested instead.

## Follow-up — adaptive fast Pulse requests on the dedicated lane

The mandatory dedicated schedule remains correct: a full Pulse lifecycle must
never resume inside the workflow session it reviews. But waiting blindly for
the next cron occurrence also delays feedback after a materially changed run.

**Decision:** the ordinary run's lightweight finalizer now receives factual
Pulse timing context and makes the semantic decision itself. It may call
`record_pulse_fast_request` only when the completed run contains material new
evidence (for example a meaningful plan/schema/evaluation change, serious
regression, or anomalous cost/runtime). Routine or no-change runs make no
request and accumulate evidence for the ordinary periodic Pulse pass.

The request is a workflow-local SQLite row, not a cron rewrite or inline
review. The scheduler coalesces requests and, on its next tick, triggers the
already-configured `pulse_review_only` schedule immediately. That schedule
creates its own Pulse-only session and uses the normal Gate → Review/Fix →
Finalize path. If a cron/manual Pulse begins first, it consumes the pending
request so the same evidence is not reviewed twice.

Go only delivers/coalesces the agent's durable decision. It does not classify
what is material, mutate cron cadence, or run reviewers in the ordinary
workflow session.

- `TestFastPulseRequestCoalescesAndConsumes` covers durable coalescing and
  consumption.
- `TestScheduledRunFinalizerOffersSeparateFastPulseDecision` ensures the
  finalizer has the bounded agentic choice while retaining the no-cron-mutation
  boundary.

## Verification

- `go build ./...` clean throughout.
- New tests for every decision point: `TestPostRunMonitorIsPeriodicFailsSafeToPerRun`
  (unknown/empty values never accidentally opt out of review),
  `TestPostRunMonitorUsesLightweightFinalizeRequiresRealEvidence` (the
  periodic review pass itself must never take the lightweight path),
  `TestLightweightFinalizeStepNeverRunsGateOrPublishesFindings`,
  `TestPulseReviewOnlyScheduleContractMirrorsManualPulseTrigger`,
  `TestPulseReviewBacklogSummaryExcludesNonTerminalIterationZero`,
  `TestBacklogGateStepDefersWhatsNewReasoningToGate`,
  `TestCreateAndUpdatePulseReviewOnlyScheduleSkipsGroupNamesRequirement`
  (against the real `CreateSchedule`/`UpdateSchedule` implementations, not
  just the tool-layer guard — this is what caught the `ValidateManifest`
  gap), `TestUpgradePeriodicPulseReviewHandoffPromptShape`, and
  `TestUpgradeQueriesNeverNamePlatTickets`.
- Every existing `workflowVersionUpgradePlan`-dependent test updated for the
  new terminal rung and reverified passing.
- **Policy-change verification (same day)**: `TestLightweightFinalizeStepReconsidersReviewScheduleCadence`
  pins the fourth, ongoing cadence-recheck responsibility; the handoff-rung
  test above pins that the migration prompt no longer offers `per_run` as an
  option and explicitly names the Gate handoff instead of re-implementing
  the migration itself. Full suite reverified after this change: the same
  23 pre-existing failures as the baseline captured earlier in this session,
  byte-for-byte identical — zero new failures.
- Full suite reverified after every part: 26 failures throughout, byte-for-byte
  identical to the baseline captured before this work started — zero new
  failures introduced by any of six incremental changes.
- **Caught mid-implementation, not shipped**: an early test draft for the
  durable background-agent log (a related PLAT-114 follow-up surfaced during
  this same investigation) wrote a real row into social-media's actual
  production database before proper sandboxing was added — caught by
  inspection, deleted, and the test rewritten. Recorded on PLAT-114, not
  repeated here, but the same class of risk (a Go-side path resolution that
  walks up from cwd to a real `workspace-docs` directory when a workspace
  env var isn't set) is worth remembering for any future test in this file.
- Internal ticket numbers were caught and removed from every live,
  agent-facing prompt string added by this change (`workflow-tools.md`, the
  new upgrade rung) after explicit review — this codebase runs on
  individual users' own machines, and an internal tracking number in a
  prompt those users' agents execute is meaningless noise to them, not
  context. One pre-existing violation in an unrelated, already-shipped
  upgrade rung (`upgradeLearningsLockAudit`, PLAT-055) was found by the new
  regression test and fixed alongside it.

## Acceptance

- An unmodified workflow (no `post_run_monitor_mode` set) is byte-for-byte
  unaffected until its next normal Gate pass — proven by the fail-safe test
  on the mode-reading accessor; the transition to `"periodic"` happens
  inside that pass, not before.
- A `"periodic"` workflow's normal run schedule never runs Gate, reviewers,
  or Fixer, in any session.
- The periodic review pass reviews the real current backlog, decided by
  Gate's own reasoning against durable state, never a Go-selected single
  folder.
- Creating or updating a `PulseReviewOnly` schedule never requires
  `group_names`, at the tool layer, the Go implementation, and manifest
  validation alike.
- Gate's bootstrap never leaves a workflow in `"periodic"` mode without a
  paired review schedule, or vice versa, the same atomic pairing the
  original migration rung enforced.
- No live prompt (the Gate bootstrap, the handoff rung, `/pulse-setup`)
  offers staying on `per_run` as a legitimate outcome — periodic is
  mandatory for every workflow, not a frequency-based judgment call.
