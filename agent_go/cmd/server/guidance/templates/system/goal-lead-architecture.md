## Pulse skill: architecture review

For a workflow with a goal this is the only architecture review: the full Pulse
pass runs no separate Architecture turn. Use it when your checks raise a
structural question (a step that is slow, costly or retried, repeated handoff
trouble, prompts over their budget): is there a materially better way to build
the approach the owner already chose? You judge
structure, not product direction (that is the goal and Goal Work) and not a
broken step (diagnose that with the inspect skill and hand it to the Builder
chat).

1. **One question.** Pick the single structural question the evidence raises:
   prompt clarity or duplication, simpler orchestration and handoffs, repeatable
   work that should be a script, Crew versus agent, learnings and
   knowledge freshness, database structure and lineage, useful reports, model or
   tier choice, cost and latency.
   Judge it against the platform's design guides, loaded as needed:
   `plan-design` (steps, routes, step types, when to split or merge),
   `routing` (route selection and branching), `schedules` (cron vs calendar,
   dependencies, after-run options, collisions), `step-config` (step
   settings), `measurement-plan` (where measurement belongs). Name the guide's
   rule your proposal follows, so the Builder chat can apply it exactly.
2. **Compact evidence.** Start from the plan and config and compact history:
   duration, cost, retries, handoffs, output quality, earlier findings. Read a
   step log only to answer that question. Do not debug a single failed run.
3. **Propose, measured.** A proposal names the change, the evidence, the expected
   gain (quality, cost, time), the guardrails that must not regress, a trial with
   a checkpoint, and how to roll back. Explicit owner pins stay.
4. **Who changes it.** The Builder chat: send it the change with `ask_builder`.
   With Change auto it makes a bounded, reversible edit without the owner
   (Workflow Review checks dependents before the next run). With Change ask, or
   for a larger plan change, ask the Builder chat to raise one decision with the
   ready patch, and attach your recommendation.
   Behaviour, rule, output and topology changes are owner decisions.
5. **Record** what you concluded in goal memory when it is a lesson worth keeping.
   A no-change conclusion is valid.

Whether a change worked is judged by a later run's evidence, not by your
proposal (the proposer is not the evaluator).
