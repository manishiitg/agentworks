[← brain / learnings-knowledge](index.md)

# PLAT-173 — the KB half of the merged reflection turn had none of the anti-append guidance the learnings half carries, and `stores.md` promised an automatic compaction that has never existed

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | brain |
| Area | learnings-knowledge |
| Summary | P2 — no run fails from this, but every run makes the KB |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` — both halves shipped with a fail-before/pass-after test; live reverify pending on a fresh survey cycle |
| Last synchronized | `2026-08-21` |

- **Priority:** P2 — no run fails from this, but every run makes the KB
  measurably worse, and the degradation is silent until a note is large enough
  that a step notices and files a concern about it.
- **Owner:** `pkg/orchestrator/agents/workflow/step_based_workflow/reflection_turn.go`
  (`buildReflectionKBSection`), `cmd/server/guidance/templates/system/stores.md`
- **Related:** [PLAT-055](../../goals/learnings/plat-055.md) (created the merged reflection turn this
  ticket completes — it fixed routing *between* stores but left the KB half's
  own write discipline unstated), [PLAT-058](../../goals/learnings/plat-058.md) (the same
  append-forever pattern, caught and fixed for learnings only),
  [PLAT-123](../../goals/step-execution/plat-123.md)) (the `record_run_concern` path that surfaced this).

## How it surfaced

2026-08-21, workflow `confida-login`, group `confida-staging`. Two concerns
filed by two different steps in the same cycle, both landing in `run_concerns`
with full structured detail:

- `11:56:07`, `survey-app-and-refresh-knowledge` — *"knowledgebase/notes/
  app-structure.md has grown past the KB condensation thresholds (20KB / 30
  sections) across many prior survey-app-and-refresh-knowledge cycles, each
  appending a new dated per-cycle section instead of updating in place."*
- `13:09:23`, `execute-browser-and-capture-apis` — an unreconciled
  contradiction inside `page-agreement-workflows.md`: a 2026-07-19 entry
  declaring AI Genome Review Finalize a confirmed MEMBER no-op (GitHub #4035)
  sitting alongside later entries in the same file showing that exact path
  succeeding, with nothing marking the earlier claim superseded.

The second is the first one's consequence. A file nobody ever corrects in
place accumulates contradictions by construction, and a reader going
top-to-bottom lands on the stale claim as if it were current. The step that
found it had read-only KB access and correctly filed rather than fixed — and,
notably, *did* fix the same stale claim where it had write access
(`learnings/_global/references/browser-automation.md`), after independently
re-verifying the path works twice. The permission split behaved correctly;
what failed is upstream of it.

## Root cause A — the two halves of one turn get unequal instruction

`BuildStepReflectionTurn` renders a learnings half and a KB half. The
learnings half (`reflection_turn.go:185-215`) states the anti-append duty five
separate ways:

- *"Keep every file you touch compact, precise, and informative — a reference,
  not a growing log"*
- *"Restated facts. Two or more entries establishing the same behaviour —
  merge them into one"*
- *"Update in place; do not stack confirmations"*
- *"A date is metadata on an entry (`last verified 2026-07-02`), never its
  identity"*
- *"Four entries saying the same thing on four dates is a defect, not a
  history"*

`buildReflectionKBSection` (`reflection_turn.go:243-266`, pre-fix) stated
**none of them**. Its entire content was: the target folder, read the registry
first, topic-id conventions, use `diff_patch_workspace_file`, don't write
`context/`, the contribution contract, don't invent facts.

So inside one turn, a step is told at length not to stack dated sections in
learnings, and told nothing at all about doing exactly that in the KB. The
step is not misbehaving — it is following the only instruction it was given.
PLAT-058 diagnosed and fixed this precise pattern for learnings; the KB half
was never revisited.

## Root cause B — `stores.md` documented a mechanism that does not exist

`stores.md:26` read:

> *Compaction: notes files compact themselves when they exceed 20KB or 30
> sections — older sections get condensed into a "Historical context"
> preamble, recent sections stay verbatim. Bounded growth without losing the
> long-range narrative.*

There is no such mechanism. Searched for every plausible implementation —
`20480`, `20 * 1024`, `maxSections`, `compactNote`, `condense`, any threshold
constant — and found nothing outside unrelated code (a Gmail attachment cap, a
Slack section cap). The only compaction machinery that exists is:

- `kb_update_agent.go`'s "Compact a topic file" capability — belongs to the
  post-step KB update agent, which is **retired for this path**
  (`buildReflectionKBSection` states plainly: *"You are the sole KB writer for
  this step; no separate update agent runs"*).
- `/improve-knowledge` — a **workshop-mode** skill
  (`guidance.go:81`, `Modes: []string{"workshop"}`), manually or Pulse
  triggered, not part of any run.
- `mutate_knowledgebase` — a manual natural-language repair tool.

This is worse than a documentation gap: it is an active reason to keep
appending. A step that reads `stores.md` and believes compaction is handled
automatically has been told, in effect, that letting the file grow is fine.
The concern the survey step filed even cites "the KB condensation thresholds
(20KB / 30 sections)" as though they were real enforcement — the step believed
the documentation, exactly as intended.

Same class as the comment PLAT-153 corrected (*"this workflow permits 90m tool
executions"* — never verified, and wrong): a confidently-stated mechanism that
nothing implements, load-bearing for behavior downstream of it.

## Fix

**A.** `buildReflectionKBSection` now carries the KB equivalent of the
learnings half's discipline:

- Read the whole topic file before writing; correct or strengthen the section
  that already covers the observation — including replacing a claim this run
  disproved — rather than appending a new dated one.
- Names the specific defect: a new dated section that restates, contradicts,
  or supersedes an existing one *without reconciling it*, because a reader
  going top-to-bottom lands on the stale claim as current. This is the
  `page-agreement-workflows.md` failure stated as a rule.
- *"Nothing compacts these files for you"* — no automatic pass, no separate
  agent behind this turn. Folding near-duplicate sections together is this
  turn's job even when the turn's own addition is small (the framing PLAT-058
  found necessary for learnings: *"a file only gets this way because every turn
  treated cleanup as someone else's job"*).
- Points at `## Historical context` for demoting genuinely superseded
  point-in-time observations, so the instruction has a destination rather than
  implying deletion.

**B.** `stores.md:26` now states that compaction is the writing step's own
duty, records that the automatic-compaction claim was false, and correctly
places `/improve-knowledge` and `mutate_knowledgebase` as manual repair passes
rather than a per-run safety net.

## Verification

- `TestReflectionKBSectionRequiresUpdateInPlaceNotDatedAppends` — new, mirrors
  the existing `TestReflectionTurnRequiresCompactionOverDatedAppends` that
  pins the learnings half. Verified fail-before (all four assertions failed on
  the pre-fix prompt) and pass-after.
- All eight pre-existing `TestReflection*` tests still pass unmodified — the
  KB addition does not disturb routing, section omission, or the
  no-store-due short circuit.
- `go build ./...` clean.

Not yet reverified live: the real signal is a subsequent
`survey-app-and-refresh-knowledge` cycle correcting `app-structure.md` in
place instead of appending to it. Worth checking on the next full-qa run.

## Deliberately not done

- **Not compacting `app-structure.md` or reconciling
  `page-agreement-workflows.md` as part of this pass.** Those are existing
  damage in one workflow's KB, repairable with `mutate_knowledgebase` or
  `/improve-knowledge`; this ticket fixes the mechanism that caused them.
  Repairing the files without fixing the guidance would just regrow the same
  shape next cycle.
- **Not building automatic threshold-based compaction.** The claim that it
  existed is now removed rather than made true. A byte threshold is a poor
  proxy for quality — the reasoning already recorded in `reflection_turn.go`
  for deliberately omitting a size threshold from the learnings half applies
  identically here (a large file can be dense and correct; a small one can
  already be redundant). If per-run automatic compaction is wanted later it
  should be designed on its own terms, not inherited from a line of
  documentation that described something nobody wrote.
- **Not widening the read-only step's KB access.** Considered and rejected in
  discussion: the flag path worked exactly as designed — the concern landed
  with severity, classification, evidence, impact, workaround, and a
  `next_check`, which is more than an inline fix would have left behind. The
  cost of the split here was zero; the cost of the missing guidance was the
  whole ticket.

## Acceptance

- [x] The KB half of the reflection turn states the update-in-place duty, the
      unreconciled-contradiction defect, and that compaction is the writing
      step's own job.
- [x] `stores.md` no longer promises automatic compaction.
- [ ] Live: a later `survey-app-and-refresh-knowledge` cycle updates
      `app-structure.md`'s existing sections instead of appending a new dated
      one.
