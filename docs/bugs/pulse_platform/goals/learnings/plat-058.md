[← goals / learnings](index.md)

# PLAT-058 — per-step learnings files fragmented one workflow skill

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | learnings |
| Summary | step reflection turn — learnings write target |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-09` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.

- **Priority:** P1
- **Owner:** step reflection turn — learnings write target
- **Source workflow:** Social Media, first live run after PLAT-055 (2026-08-09)
- **Supersedes:** the per-step file ownership half of PLAT-055 (item **D**). The
  rest of PLAT-055 — routing table, staleness test, `record_run_concern`,
  index-is-an-index, compaction — stands and is working.

## Problem

PLAT-055/D told each step to own `references/<step-id>.md`. On its first live
run that fragmented one skill by **execution structure** instead of by
**subject**:

| step-id file created | bytes | topic file it should have extended | bytes |
|---|---|---|---|
| `execute-actions-step-exec-reply-targets.md` | 1,811 | `reply-target-execution.md` | 110,230 |
| `execute-actions-step-exec-like-targets.md` | 3,172 | `like-target-execution.md` | 61,641 |
| `execute-actions-step-exec-follow.md` | 2,370 | `follow-accounts.md` | 72,667 |
| `execute-actions-step-exec-quote-tweet.md` | 2,630 | `quote-tweet-execution.md` | 18,067 |
| `execute-actions-step-exec-post-tweet.md` | 361 | `post-original-tweet.md` | 46,415 |

`SKILL.md` grew 7,662 → 8,533 bytes carrying four honest but wrong pointers of
the form *"Newer `reply_to_targets` execution additions **not yet folded into**
`references/reply-target-execution.md`"*.

**The steps themselves diagnosed it**, through the `record_run_concern` tool
PLAT-055/A had just added — which is also this ticket's strongest evidence that
the rest of PLAT-055 works. `execute-unfollow-cleanup` filed it as
`issue_kind=harness_issue`, `target_key=reflection-turn:learnings-file-path-template`,
with four pieces of evidence:

> "This turn instructed writing to `references/execute-unfollow-cleanup.md` and
> asserted it does not exist yet, but the real file this step has maintained
> across 7+ prior iterations (467+ lines, actively cross-referenced) is
> `references/unfollow-cleanup.md`, which is the exact path SKILL.md line 89
> links to. Following the reflection turn literally would have forked the
> content into a second, orphaned file invisible from the SKILL.md index."
>
> Impact: "new technique notes would silently stop being discoverable via the
> index, and the two files would drift out of sync with no mechanism to
> reconcile them."

That step **refused the instruction**, wrote to the correct topic file, and
raised a concern instead — which is why `unfollow-cleanup.md` is absent from the
damaged set above. `execute-remediate` independently filed the same defect from
another angle (`target_key=learnings/_global/references/execute-remediate.md-vs-content-remediation.md`),
and the deliberation itself showed up as reflection-turn cost.

## Why the original rule was wrong

D was introduced to stop Upwork's `SKILL.md` reaching 48 KB with six steps
writing into it. But that growth was caused by content landing in the **index**
instead of topic files, by absent compaction, and by facts/incidents being
routed into learnings at all. Those are fixed by PLAT-055's other rules, none of
which require carving the skill up per step. D was an over-correction, and it
also:

- produced non-skill names (`execute-actions-step-exec-reply-targets.md`),
- broke down whenever one subject is touched by several steps (browser session,
  DB access) or a step is renamed, split, or merged,
- orphaned the topic files `SKILL.md` already links to.

A workflow's learnings **are a skill**: one artifact, standard format
(frontmatter → index → `references/`), organised by topic, authored and improved
by every step over time.

## Implementation (2026-08-09)

`reflection_turn.go`, `buildReflectionLearningsSection`:

- Removed the per-step write target entirely. The turn now states the skill is
  one shared, topic-organised artifact and directs the contribution to the topic
  file that owns the subject — extending it however many steps also write there.
- A new topic file may only be named for the **subject**, never a step, step id,
  or run, and must gain an index line.
- Added a self-healing instruction: a file named after a step is a leftover —
  fold it into the owning topic file, delete it, remove its index line.
- Reads reframed from "unrestricted" to "read widely before writing", because in
  a shared skill another step's entry on the same surface is what prevents a
  duplicate.
- Added skill-identity upkeep: the frontmatter `description` must stay accurate
  about what the skill covers. Social Media's still read *"Minimal reset
  learning baseline"* while holding ~300 KB across a dozen topics; RTS Latency's
  89 KB `SKILL.md` has no frontmatter at all.
- Dropped the now-meaningless `StepFileBytes`/`StepFileExists` inputs and
  narrowed `reflectionLearningsSizes` to `reflectionSkillIndexLines`.

No new prompt-discovery cost: `SKILL.md` is already injected into every step's
prompt under `## Skill`, so the topic index is in context before the turn runs.

## Regression tests

`reflection_turn_test.go`:

- `TestReflectionTurnTreatsSkillAsOneSharedTopicOrganisedArtifact` — replaces
  `TestReflectionTurnScopesWritesToTheStepsOwnFile`. Asserts the step id is
  **never** presented as a write target, that topic ownership and the
  subject-naming rule are stated, that step-named orphans are folded and
  deleted, and that reading widely is encouraged.
- `TestReflectionTurnRequiresCleanupJudgmentOnEveryFileItTouches` — replaces the
  per-step-size variant; cleanup duty attaches to every file the turn touches.

## Acceptance

A workflow's learnings remain one topic-organised skill that every step
improves. Runtime reverify: the next Social Media run should fold the five
orphaned step-named files into their topic files, delete them, and remove the
four "not yet folded into" lines from `SKILL.md` — without being asked, since
the fold instruction is unconditional.
