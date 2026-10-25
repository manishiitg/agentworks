## Assumption Audit — Keep The Workflow Evolvable

Use this bounded check whenever reviewing or improving the plan, eval, report,
knowledgebase notes, learnings/skills, database contracts, or saved code. An
artifact can be internally correct and still preserve an old assumption that
prevents the workflow from reaching its goal.

### Classify before changing

For each consequential restriction, classify it as exactly one of:

1. **Explicit user constraint** — the user stated or approved it as durable.
   Preserve it. `soul/soul.md ## Constraints`, a User rule card, or direct user
   evidence should support this classification.
2. **Verified external constraint** — a current platform/API/legal/business fact
   supported by evidence. Preserve it with source/freshness; schedule a re-check
   when it can change.
3. **Current design choice** — an evidence-backed tactic or architecture that is
   useful now but remains replaceable. Keep it out of `soul.md`; describe it as
   the current approach, not a permanent truth.
4. **Agent-inferred assumption** — hardcoded or inherited without user approval or
   current evidence. Challenge, remove, generalize, parameterize, or propose a
   better approach according to this command's mutation boundaries.

### What to look for

- `must`, `only`, `never`, `always`, fixed lists, thresholds, cadence,
  geography/time windows, audience/channel/source restrictions, and
  architecture/provider/tool/model choices with no provenance;
- step descriptions, eval rubrics, reports, DB schemas, KB notes, learnings, or
  code that optimize for the current plan's method instead of `soul.md` success;
- targets or thresholds intended as minimum acceptable outcomes that have
  silently become ceilings preventing exploration of materially better results;
- literals that should be variables/config, schemas that assume one source/type,
  and reports/evals that make today's implementation look like the goal;
- old workarounds, limitations, or decision text whose evidence expired;
- the same assumption copied across artifacts, making it look authoritative only
  through repetition.

A guard, filter, or abstention can be operationally correct while the surrounding
strategy is still too narrow. Repeated `no_job`, `no_match`, `no_candidate`,
`stand_aside`, or other valid no-output results are not evidence that the current
channel, criteria, or tactic can achieve the goal. Preserve explicit user safety
boundaries, but challenge the search breadth, source mix, positioning, offer,
cadence, and other agent-chosen constraints around them.

Hardcoded credentials, local paths, account ids, and run ids are Bugs and should
be fixed within the command's normal boundaries. A strategy/tactic assumption is
a Goal concern. Do not confuse the two.

### Bounded action

1. Read `soul/soul.md` for stable intent, but do not trust architecture or an
   unproven assumption merely because older soul text contains it.
2. Inspect only assumptions relevant to this command's artifact and direct
   consumers/producers. Do not turn targeted maintenance into a full audit.
3. Apply a bounded correction without asking when it preserves business meaning:
   remove stale prose, parameterize a literal, stop treating a tactic as a goal,
   or mark a current choice as revisable.
4. If changing it would alter business intent, risk, cost, external behaviour or
   a material plan path, ask the owner through `create_human_input_request`.
   Include the assumption, source, evidence for and against, and what would
   validate it. Keep one pending question for the same decision.
5. Record authorized work and its evidence in the existing plan changelog or
   requesting conversation. Close obsolete proposals through the normal
   withdrawal tools; a withdrawal never grants approval.

### Store-specific ownership

- **Plan/design:** challenge fixed architecture, channels, sequence, thresholds,
  cadence, and step boundaries that are not required by the goal.
- **Eval:** measure success criteria, not compliance with the current tactic or
  artifact shape unless that shape is explicitly part of success.
- **Report:** show goal/outcome truth; do not make the current implementation or
  an inferred proxy look like the user's target.
- **KB notes:** distinguish durable domain evidence from workflow-design beliefs.
  Never rewrite user-owned `knowledgebase/context/` without the owner asking.
- **Learnings/skills:** keep reusable HOW; remove business policy, strategy, and
  stale architecture disguised as execution guidance.
- **DB:** challenge schemas/enums/cardinality that unnecessarily lock the workflow
  to one source, channel, entity, or tactic. Do not perform speculative row
  migrations.
- **Code:** parameterize unjustified literals and separate verified platform
  constraints from temporary workarounds.

The output is not a second review report. Fold bounded fixes into the command's
normal result and surface only consequential unresolved questions to the owner.
