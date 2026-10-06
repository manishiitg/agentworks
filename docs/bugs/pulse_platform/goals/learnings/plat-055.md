[← goals / learnings](index.md)

# PLAT-055 — the reflection turn can only write learnings, so learnings absorbs every store's content

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | goals |
| Area | learnings |
| Summary | step post-completion turns, learnings/KB contribution contract |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-09` |

- **Priority:** P1
- **Owner:** step post-completion turns, learnings/KB contribution contract
- **Source workflow:** Social Media, RTS Latency, Upwork, LinkedIn

## The defect

A step's learnings contribution fires as a dedicated turn *after* execution,
with its folder guard narrowed to `learnings/` only
(`controller_execution.go`, direct-learnings block). The knowledgebase turn has
already closed by then, and the database was never reachable from a reflection
turn at all — its tool list is `execute_shell_command` (read-only inspection)
plus `diff_patch_workspace_file` scoped under `learnings/`.

So at the exact moment an agent works out what it learned, **learnings is the
only writable destination**. Everything it recognized lands there regardless of
which store owns it.

The split itself causes the misrouting: during the KB turn the agent has not yet
worked out its learnings, and by the learnings turn the KB door is shut. "Which
store does this belong in?" is never a decision the agent gets to make — it is
decided by whichever door happens to be open.

## Measured effect

| Workflow | `SKILL.md` | Worst `references/` file | Locked writers |
|---|---|---|---|
| LinkedIn | 1.9 KB | 23 KB | 6 / 6 — inert, not healthy |
| Social Media | 7.6 KB | **110 KB** | 0 / 11 |
| RTS Latency | **89 KB** | **117 KB** | 0 / 6 |
| Upwork | 48 KB → 9 KB (Pulse compacted it) | — | 2 / 6 |

Two distinct leakage motivations were confirmed from file contents:

**1. Nowhere to go.** Social Media's `reply-target-execution.md` contains a
multi-paragraph validator-bug analysis — three tested hypotheses, a root-cause
theory, a discovery technique — in which the agent writes:

> *"…stop iterating from the executor side and **surface it as a
> harness/validator bug report** instead of continuing to burn validation
> rounds."*

It then filed that report into learnings, because the only sanctioned outlet was
a single `CONCERNS:` line, which cannot carry a structured finding. One entry
pair in that file is 7,435 characters, of which roughly one sentence is durable
HOW.

**2. Already written, copied anyway.** RTS Latency pasted latency percentile
tables, AWS cost baselines and a live security-findings inventory into learnings.
All three already live in purpose-built tables — `latency_baselines`,
`cost_daily_metrics`, `security_findings` — with *fresher* data (DB has
2026-08-05/07 rows; the learnings copies are May and June). Nothing tells a step
which tables exist, so caching into `SKILL.md` feels safer than betting a future
run will query. The section even warns readers not to trust itself:
*"Don't assume any prior day's 'standing findings' list is current."*

Both categories go stale. Genuine HOW does not: `ce:*` needs `--region
us-east-1` is still true; `~$40–60/day` was wrong within weeks.

## Why more prompt rules alone will not fix it

Social Media's `learning_objective` already bans exactly this, by name:

> *"Never contribute incident narratives, action/run IDs, operator decision
> receipts, fix history or CONCERNS text (Pulse owns those); current values,
> counts or run status (the DB and run artifacts own those)…"*

The agent wrote all four anyway. The rule was **unfollowable, not ignored** — it
instructs routing to stores the turn cannot reach, and the one outlet it does
have is too low-bandwidth to carry the content. Fixing the destinations is
therefore the prerequisite for the rules to become effective.

## Concern bandwidth is the binding constraint

`CONCERNS:` is a text convention scraped by Go (`ParseConcernLines`). It
guarantees capture of *lines*, not concerns: the validator-bug finding was never
put on one, so the scrape had nothing to find. Hard evidence of the cost —
**39 of 103 active Upwork concerns have no detail row at all**, only a text
string with no severity, classification, or evidence.

## Acceptance boundary

1. One reflection turn, with learnings write, `knowledgebase/notes` write, `db/`
   **read**, and a structured concern tool all available simultaneously, so
   routing is a real decision.
2. `record_run_concern` carries the same shape as `pulse_finding_details`
   (`severity`, `classification`, `summary`, `impact`, `evidence[]`,
   `reproduction`) and writes through the existing `RecordRunConcerns` path so
   fingerprint dedup and the `open → acknowledged → resolved` lifecycle are
   unchanged.
3. The prompt states an explicit routing rule, names the step's own
   `references/<step-id>.md` target, reports current file sizes against the
   stated index budget, and requires updating an existing entry rather than
   appending a new dated confirmation.

Reads stay broad: a step may still read any topic file for cross-step knowledge.
Only writes become scoped.

## Explicitly not in this ticket

- **Workflow-owned data integrity** surfaced during investigation (Upwork's NULL
  `fit_score` rows, `proposals.job_id` holding run identities, `observed_count`
  inflated 100–700×). Pulse Engineering Review owns those and they are queued.
- **Compaction of already-bloated files** — an agentic per-workflow pass, not a
  platform change. Pulse did Upwork unprompted.
- **Retiring the Go `CONCERNS:` scrape** — deferred until the tool is proven in
  production. Note Go itself authors `CONCERNS:` strings in three places and
  then re-parses them; that round-trip should become direct `RecordRunConcerns`
  calls, and the text line currently doubles as the notification surface.
- **`concernFingerprint` includes `stepID`**, so a step-raised symptom and a
  Pulse-raised root cause never auto-merge. Live pair in Upwork: `426bc243…`
  (the failing gate) and `90bcb968…` (the root-cause analysis), both
  `awaiting_verification`, unlinked. Merging stays agentic via
  `merge_pulse_issues`.

## Implementation — 2026-08-09

**One reflection turn.** `BuildStepReflectionTurn` replaces the KB self-review
turn followed by the direct-learnings turn, and `runStepReflectionTurn` owns the
orchestration. Everything the two turns did is preserved: the shared-file mutex,
continuation-phase bookkeeping, both freshness ledgers, the canonical
artifact-change log, and learning metadata. The guard widens to
`knowledgebase/notes` (write) and `db/` (read) for the turn only, through the
existing `prepareDirectLearningTurn`.

The **message_sequence** path had the identical split — a learnings item followed
by a KB item — and got the same merge. That path matters most here, since
message_sequence is the default step type in these workflows.

**`record_run_concern`.** Structured fields mirroring `pulse_finding_details`,
written through the existing lifecycle rows so fingerprint dedup and the
`open → acknowledged → resolved` progression are unchanged. Step identity comes
from trusted per-session state (`common.RunConcernSessionContext`), not tool
arguments — the same reason the workflow-DB tools resolve their workspace that
way. Filing twice in one run is idempotent, because `seen_count` is what Gate
weighs when deciding a root cause deserves repair; genuine recurrence on a later
run still accumulates. The tool is capability-derived like the DB tools, so a
custom allowlist cannot silence the channel.

**Prompt contract.** Routing table naming every store plus the staleness test
("if it will be wrong in a month, it is not a learning"); the workflow's actual
table names injected so "reference the table, never paste its values" is
actionable; `references/<step-id>.md` named as the step's own write target with
`SKILL.md` reduced to one owned index line; current index size stated against the
budget, with an explicit warning when over; and update-in-place required instead
of stacking dated confirmations.

### Two deliberate scope decisions

- **No turn when no store is due.** Emitting one purely for the concern outlet
  would add an LLM call to every step of a `lock_learnings` workflow (LinkedIn
  has 6 of 6 locked), where the previous code emitted nothing.
  `record_run_concern` is available during main execution regardless.
- **`lock_learnings` suppresses only the learnings half.** A locked step can
  still contribute KB and still report a defect; freezing learnings was never
  meant to silence those.

## Files changed

- `pkg/.../step_based_workflow/reflection_turn.go` (new) — prompt builder,
  `LoadWorkflowDBTableNames`
- `pkg/.../step_based_workflow/reflection_turn_run.go` (new) — orchestration
- `pkg/.../step_based_workflow/run_concern_tool.go` (new) — `RecordStepRunConcern`
- `pkg/.../step_based_workflow/controller_execution.go` — two blocks collapsed to one
- `pkg/.../step_based_workflow/controller_message_sequence.go` — two items collapsed to one
- `pkg/.../step_based_workflow/controller_agent_factory.go` — tool enablement + session identity
- `pkg/common/run_concern_session.go` (new) — trusted step identity
- `cmd/server/virtual-tools/run_concern_tools.go` (new) — tool definition
- `cmd/server/run_concern_tool_binding.go` (new), `cmd/server/tool_setup.go` — registration

## Test command

```
cd agent_go && go test ./pkg/orchestrator/agents/workflow/step_based_workflow/ -run 'Reflection|RunConcern|MessageSequenceClosing'
```

17 tests: 9 pinning the prompt contract, 8 covering the concern lifecycle
(structured detail persisted, idempotent within a run, recurrent across runs,
incomplete findings rejected, steps separated by fingerprint). `go build ./...`
clean; `go vet` introduces no new findings; the full suite's failures are
byte-identical to the pre-existing baseline (`guidance`, `virtual-tools`, and two
`step_based_workflow` prompt tests from an unrelated in-flight Pulse-v2
refactor).

## K & J — 2026-08-09

### K. `resolveKnowledgebaseAccess` unset default

`resolveLearningsAccess` already auto-promoted an unset field to `read-write`
when an objective was staged; KB had no equivalent and unset silently meant
`KBAccessNone` — no read, no write. That is a distinct trap from an operator's
deliberate `kb_access: "none"` (rtslatency's actual case for its two
worst-offending steps): explicit values still always win, so this change does
not touch that case at all, it only removes the trap for steps nobody
configured either way.

Made KB's defaulting symmetric with learnings' already-safe pattern rather than
jumping straight to `read-write`: baseline `read` when unset (matching
learnings' baseline visibility for everyone), promoted to `read-write` only
when `knowledgebase_contribution` is already staged. A blind default straight to
`read-write` would have injected KB content into every step's prompt regardless
of whether it ever writes — real token cost the learnings precedent never
carried, since learnings already defaulted to `read` for everyone before this
ticket existed.

6 tests in `resolve_kb_access_test.go`, including one pinning that the default
still does not enable write without a staged contribution.

### J. Learnings-lock audit — new contract version 1.0.22

Implemented as a new mandatory version-upgrade preflight
(`upgradeLearningsLockAudit`, `workflow_version_upgrades.go`) rather than a
standalone tool, reusing the existing blocking-migration machinery — including
the fail-open-after-3-failures safety net already in place, so a stuck audit
cannot re-deadlock every workflow's schedule the way the original 1.0.21
migration did before that fix existed.

**Scope was narrower than originally planned, deliberately.** The original plan
called for auditing objective quality for routing language and target-file
naming. Both are now moot: the reflection turn (this same ticket, above)
injects the routing table, the table names, and the step's `references/<id>.md`
target from the platform itself, regardless of what the objective says. Auditing
objectives for content the platform now supplies unconditionally would have
been auditing something no longer meaningful. The prompt states this explicitly
so a future edit does not un-knowingly resurrect the check.

What remained genuinely worth auditing, unaffected by the reflection-turn fix:
`lock_learnings=true` steps whose `review_notes` gives no learnings-specific
justification. LinkedIn has 6 such steps; none of its `review_notes` mention
learnings at all. The audit turn reports each via `record_pulse_finding`
(`recommended_route="decision_required"`) — the same tool the workshop already
uses for reviewer findings — rather than a new one; it never calls
`update_step_config` to clear the lock. A documented lock survives untouched.

**Real regression caught before shipping**: `workflowContractArtifactPurityVersion`
("1.0.21") sits at array index 20 in `workflowContractVersionRank`'s known-version
list, not 21. An initial `rank < 21` guard would have re-run the already-completed
1.0.21 purification turn for every workflow that had already passed it — on
every future preflight, forever. Fixed to `rank < 20` with the index called out
in a comment; `TestWorkflowVersionUpgradePlanSkipsArtifactPurityAlreadyReached`
pins the exact case (`version: "1.0.21"` → exactly one step, the new audit,
never the artifact-purity step again).

4 new tests in `workflow_version_upgrades_learnings_lock_test.go`, plus
`TestScheduledWorkshopTurnsRunAllMissingUpgradesBeforeFirstScheduleMessage`
updated from 5 to 6 required upgrades for a workflow starting at an unset
version — the correct consequence of the new mandatory step, not a regression.

**Blast radius, stated plainly:** `WorkflowContractCurrentVersion` moved
1.0.21 → 1.0.22. Every one of the 21 workflows is now below current and will
hit this new blocking preflight on its next scheduled run — a cheap, mostly
read-only turn, but a mandatory one, platform-wide.

## G self-heal correction — 2026-08-09

Started to launch an agent to hand-compact the existing bloated files
(`reply-target-execution.md` 110 KB, `daily-collector-runbook.md` 117 KB, etc.).
Stopped before it wrote anything (it ran in an isolated worktree; the live
files were never touched) after being asked directly why the reflection turn's
own self-heal wouldn't do this.

It wouldn't have, and that was a real gap in the initial implementation, not a
timing question. "Extend it" was the *only* instruction an existing step file
ever got. Rule F only merges NEW content into existing entries when it
overlaps — nothing ever obligated a turn to revisit the other, unrelated
entries already sitting in the file. That is exactly how one file reached 41
headings: no turn was ever required to look at the 38 that its own new
observation didn't happen to touch.

**First fix attempt was also wrong**, in a way worth recording. A byte-count
threshold ("over 8 KB, consolidate") is the same category of mistake the
original 80-100 line `SKILL.md` budget already was: size is a poor proxy for
quality. A large file can be genuinely dense, well-organized coverage of real
complexity (linkedin's `cdp-browser.md` is a healthy 23 KB covering all browser
automation for that workflow); a small one can already be redundant. A fixed
number is also structurally wrong for the index specifically — it legitimately
scales with how many steps contribute to it, so a cap sized for a 6-step
workflow is wrong for a 50-step one.

**Final fix**: both signals became qualitative and unconditional rather than
threshold-gated.

- **Step's own file**: every turn — regardless of current size — is instructed
  to read the whole file and check for three named patterns (restated facts
  across multiple dated entries, narrative/trial-and-error content instead of
  technique, content that belongs to another store) and fix them as part of
  that turn, not gated behind "is this file currently over some number."
- **Index**: judged structurally, not by line count — every entry should read
  as a link plus a one-line description; any real detail sitting in the index
  instead of behind a link is the defect, independent of the index's overall
  size.

This means the *existing* bloat is not fixed by this ticket — it is fixed the
next several times each step actually runs and reflects, which is the correct
test of whether the platform-level fix works end-to-end rather than a one-off
manual pass papering over a mechanism that doesn't actually self-heal. Tracked
under G for measurement: `SKILL.md` and `references/*.md` sizes for
social-media and rtslatency should trend down over the next 5-10 runs of their
respective writing steps. If they do not, the qualitative instruction is not
sufficient and needs revisiting — that would be real evidence, not a guess.

9 reflection-turn tests updated/added to pin the qualitative behavior
(`TestReflectionTurnRequiresCleanupJudgmentRegardlessOfSize`,
`TestReflectionTurnJudgesIndexStructurallyNotBySize`), asserting the same
instruction fires at both a small and a large file/index size rather than only
past a threshold. Full suite re-verified clean against the same baseline.

## Runtime evidence required

Prompt changes are probabilistic; this must be measured rather than assumed.
After 2–3 real runs: `SKILL.md` and `references/*.md` flat or shrinking for
Social Media and RTS Latency; new content landing in `references/<step-id>.md`
rather than the index; `run_concerns` gaining `execution`/`learnings`-phase rows
that carry detail rows.
