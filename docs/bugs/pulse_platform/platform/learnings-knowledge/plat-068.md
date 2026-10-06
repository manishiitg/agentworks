[← platform / learnings-knowledge](index.md)

# PLAT-068 — the step-type checklist names an automated owner that never loads it

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | platform |
| Area | learnings-knowledge |
| Summary | P2 — no incorrect behaviour; an entire review dimension simply never runs |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — container review plus direct standalone execution and typed completion enforcement shipped; live slash-command verification pending |
| Last synchronized | `2026-08-19` |

- **Priority:** P2 — no incorrect behaviour; an entire review dimension simply never runs
- **Owner:** Pulse guidance templates (`review/ops-review.md`, `builder/design-plan.md`)
- **Found on:** cross-workflow, while asking why no step-type findings exist anywhere

## What happened

The plan-design guidance is not missing. It is thorough, and it explicitly claims an owner.

`builder/design-plan.md` line 3:

> `llm_ops_review` is this checklist's automated owner (engineering correctness — **step-type**/prevalidation/schema fitness, not tactic quality)

and it carries a full **PART 3 — STEP-TYPE FITNESS** section, including a well-reasoned mode rule (line 101):

> deterministic API/CLI/SDK/data-fetch/parse/transform steps start **scripted** … Judgment, synthesis, adaptive discovery, and browser/UI work stay **agentic**. The 10+-run evidence bar is only for *freezing* a proven script with `lock_code`, not for declaring an obviously deterministic step scripted. When `llm_ops_review` has recorded tool-call trace evidence that a step's actual behavior is deterministic and identical in shape across runs, ground the scripted-candidate finding in that real evidence rather than the description alone — a description that reads as agentic can still be masking a step whose real tool-call sequence never varies.

**The ownership claim is one-directional.** `review/ops-review.md` — the guidance `llm_ops_review` actually runs — contains **zero** references to `design-plan`, `review_plan`, "checklist", "structural", or "scripted". Confirmed by grep. No Pulse lens template invokes `review_plan` either.

So the checklist declares an owner; the owner's own instructions never load it. Nothing in a Pulse pass ever asks:

- should this step be scripted rather than agentic (or the reverse)?
- should these adjacent steps be one `message_sequence` sharing context?
- should this oversized sequence be split at a real context boundary?

`pulse-fixer-practices.md` has an "Applying an Ops config recommendation" section covering `declared_execution_mode`, but it is explicit that the Fixer is only a writer: *"Never apply one without an owning Ops finding. … If no finding recommends the change, the change is not justified."* With no lens producing those findings, that playbook is unreachable by construction.

## Evidence this is real, not theoretical

Upwork's own numbers make the missing dimension concrete. `search-find-and-shortlist` is a 6-turn `message_sequence` costing **$29.26 and 32.4 minutes in one run**, with **197 tool calls** — 70 of them inside a single turn. Whether that is the right shape is exactly the PART 3 question, and no reviewer is asked it. Meanwhile the *builder* is told to apply these rules when authoring a plan, so a plan is held to the standard once at creation and never again as it drifts.

## Compounding factor

`llm_ops_review` is currently **disabled at the Gate** (`system/pulse-gate.md` core-system verification block permits only `workflow_review`). So even the half that exists does not run today. Re-enabling the module is necessary but **not sufficient** — without the linkage below it would come back still not reading the checklist.

## Suggested fix

1. In `review/ops-review.md`, load the checklist explicitly for the structural pass and state that step-type/shape fitness is in scope, attributing findings as `llm_ops_review` per the existing rule in `design-plan.md` line 3.
2. Ground it in the evidence Ops uniquely holds: it already sees per-step cost, tool-call counts and tool-call traces, which is precisely the "real evidence rather than the description alone" that line 101 asks for. A step whose trace never varies in shape is a scripted candidate; a turn with 70 tool calls is a shape question.
3. Keep the Fixer contract unchanged — it stays the only writer, and still refuses changes with no owning finding.

## What shipped (2026-08-10)

`review/ops-review.md` gained item 7, "Judge structural fitness against the plan-design checklist, which names this module as its owner". It loads the checklist explicitly and applies PART 3 to the current plan, attributing findings to `llm_ops_review`, under the checklist's own read-only parent contract (findings to the parent, no workspace edits, Fixer stays the only writer). Later items renumbered 8/9.

Rather than restate the checklist, the new item supplies the thing only this lens has — behavioural evidence — and directs it at three questions:

- **Scripted candidates** grounded in the trace, never the description: a step whose real tool-call sequence never varies in shape is a candidate even when its description reads agentic. Judgment/synthesis/adaptive/browser work stays agentic explicitly, so "save cost" cannot be used to script something that should not be.
- **Sequence shape**, with the actual mechanism named: every tool call is a serial model round-trip that re-reads the whole accumulated context, so *call count* is the multiplier, not payload size. Merging requires genuinely shared context; splitting requires a boundary the checklist recognises, and "it is long" is explicitly not one.
- **Reflection yield**, now newly answerable: `reflection:<step-id>` is separated from `execution_only:<step-id>` in the cost ledger and `reflection-timing.json` sits beside the execution timing files (shipped in 28cca5b8e / fd7444e30). A reflection turn burning time with `wrote_learnings`/`wrote_kb` false is overhead — sharpen the objective or drop to `learnings_access: "read"` rather than speed the turn up.

The item closes by re-binding to the evidence bar and stating the asymmetric risk directly: scripting a step that is not actually deterministic breaks it, so where the trace does not settle the question, say so and leave it agentic.

**One defect found while writing it**, of exactly the PLAT-062 class (a prompt naming a path that does not resolve): the checklist is projected as `workflow-commands/references/design-plan.md`, not under `builder-reference`, which separately carries a different authoring-time `plan-design.md`. The first draft named the wrong skill and would have failed at read time. Corrected, with the distinction noted inline so the next editor does not repeat it.

**Enablement (updated 2026-08-10).** This shipped while `llm_ops_review` was still disabled, deliberately: the instruction was to get it into the best possible shape *for* enablement. The operator then chose to re-enable it ahead of `goal_advisor` and `strategy_auditor`, on the grounds that the same workflows are run repeatedly, so per-run inefficiency compounds every cycle and this is the only lens that can see it — or judge the same day's default-tier retune against outcome.

The Gate block now makes `workflow_review` and `llm_ops_review` the two selectable modules, which makes the two-module cap reachable for the first time. Two guards were added with it, because drainage rather than detection is the current bottleneck: Engineering Review keeps priority whenever it has real work (production failure or awaited verification outranks cost advisory), and `llm_ops_review` must report narrowly on its first passes rather than emptying a long-accumulated backlog of cost evidence into a system already carrying 352 findings awaiting a producing run.

## Acceptance

- A Pulse pass with `llm_ops_review` due produces step-type/shape findings where warranted, citing trace evidence, not just tier/model findings.
- The reachable-but-unused `declared_execution_mode` path in `pulse-fixer-practices.md` becomes reachable.
- Verification is blocked until `llm_ops_review` is re-enabled at the Gate; record that rather than claiming verification from a pass that never invoked the lens.

## Live recurrence and extension (2026-08-19)

The original checklist wiring shipped, but a real Social Media plan showed that
the coverage was still too narrow. Its top-level `step-execution-pipeline` is a
`todo_task` whose description prescribes exactly ten named routes in an exact
order. The parent therefore has little or no dynamic selection to perform, yet
it adds its own model context, reads, tool calls, failure surface, and handoff
cost. By contrast, the nested `execute-actions` todo genuinely selects work at
runtime from the action queue, so removing every todo container would also be
wrong. Ops Review had enough evidence to make this distinction but never asked
the parent-necessity question and did not surface the issue.

The extension now requires Ops Review to assess every cost/time-material
container and its children as one execution unit, name the real decision the
parent makes, and distinguish a fixed prescribed child set/order from genuine
dynamic selection, conditional fan-out, retry/recovery, concurrency,
human/approval boundaries, or necessary synthesis. Repeated broad reads across
the parent and children are evidence of a weak handoff, while an independent
clean-room verification read is not automatically waste. The reviewer uses
agent judgment—there is no Go semantic classifier, numeric cutoff, or automatic
plan rewrite.

The same canonical checklist is now explicitly loaded by both the standalone
`/ops-review` command and the Operations portion of `/engineering-review` and
scheduled Pulse Review+Fix. The latter two override only the standalone
dispatch/read-only wrapper and retain the normal typed finding and bounded
Fixer ownership. The Social Media plan was deliberately left unchanged so the
operator can verify that the slash-command reviewer discovers and recommends
the structural change itself.

Extended acceptance:

- A material todo/routing/sequence container is reviewed as parent plus children,
  not as an isolated parent step.
- The finding states the runtime decision that justifies retaining the parent,
  or identifies that the plan already prescribes the complete child set/order.
- `/engineering-review` loads the same Ops structural checklist rather than
  relying on an implicit ownership claim.
- Running the slash command against the unchanged Social Media plan surfaces a
  justified recommendation (or cites contrary runtime evidence); verification
  remains pending until that live run.

## Authoring-guidance consolidation (2026-08-19)

The recurrence also exposed conflicting emphasis in the builder references.
The canonical plan checklist required dynamic orchestration, while
`optimize-playbook.md` and `plan-design.md` listed different tools, independent
learnings, progress tracking, and easier debugging under “when to use
todo_task.” Those are useful properties of an already-justified delegation
boundary, but none proves that an LLM parent adds value. Read literally, the
secondary guides could recreate exactly the fixed ten-route parent that Ops is
now expected to flag.

The guidance now shares one eligibility rule: use `todo_task` only when the
parent makes a real runtime orchestration decision the static plan cannot
directly express—dynamic discovery, conditional selection/fan-out, material
runtime parallel coordination, adaptive retry/recovery, an approval boundary,
or an interim synthesis decision that changes subsequent delegation. A fixed
child set/order is explicitly insufficient. Different tools, separate
learnings, progress visibility, and easier debugging are supporting properties
only after that gate. Known same-context work maps to `message_sequence`, known
deterministic work to scripted steps, known independent fixed work to explicit
plan steps/dependencies, and a fixed exclusive branch to `routing`.

`plan-design.md` is now named as the authoritative authoring reference;
`todo-task.md`, `optimize-playbook.md`, `workflow-patterns.md`, and the Ops
checklist preserve the same negative invariant. The scripted-orchestrator fast
path is explicitly an optimization of an already-justified orchestrator, not a
back door for creating one around a fixed sequence. A focused regression test
renders every relevant guide and fails if this invariant drifts again.

## Standalone slash-command lifecycle recurrence — 2026-08-19

A live `/ops-review` exposed a separate defect in the standalone wrapper. The
main chat had already launched `ops-review review` as an isolated background
agent, but `review/ops-review.md` instructed that agent to launch a second
`Standalone Operations Review`. The outer agent collected evidence for roughly
ten minutes, then attempted the nested dispatch through a large shell/curl
heredoc. Shell quoting failed before `run_in_background` was called, so no child
execution existed. The outer agent nevertheless returned prose claiming the
review had been dispatched, and the generic background lifecycle marked that
normal LLM return completed. No `llm_ops_review` receipt or findings existed.

The standalone contract now makes the first background agent the authoritative
reviewer. It performs the review directly, records typed findings and matured
verifications, and must call `complete_pulse_review` once for
`modules=["llm_ops_review"]`. The slash dispatcher declares
`required_pulse_review_modules=["llm_ops_review"]`; after the LLM returns, the
backend reads the receipt by the exact child MCP session ID and refuses to mark
the execution successful when the receipt is missing, failed, or belongs to a
different session. Presentation prose is no longer completion authority.

Focused tests cover direct/no-nested guidance, slash dispatch, and missing,
failed, and completed SQLite receipt states. Live `/ops-review` verification
after a backend restart remains the final acceptance step.

### Decision record — standalone Ops execution boundary

| Question | Decision and reasoning |
|---|---|
| What evidence forced the decision? | The outer background agent ran for about ten minutes, its shell-wrapped nested launch failed before creating any child, no finding or review receipt was stored, yet ordinary assistant prose caused the outer execution to be presented as completed. |
| Keep the nested reviewer and only fix shell quoting? | Rejected. A direct tool call would remove the quoting failure but retain two agents, duplicated context/cost, two completion authorities, and no product value: the first agent is already isolated and owns the complete review contract. |
| Let the backend infer success from prose or finding count? | Rejected. Prose is not a lifecycle receipt, and a legitimate clean review may record zero findings. Neither proves the review reached its terminal persistence boundary. |
| Detect Ops reviews from the background task name? | Rejected. User-visible names and labels are presentation data and can change; they are not a durable execution contract. |
| Selected design | The slash dispatcher explicitly declares `required_pulse_review_modules=["llm_ops_review"]`. The existing background agent reviews directly. The backend validates a completed receipt written by that exact child MCP session before reporting success. |
| Why may a read-only reviewer write this receipt? | Read-only protects workflow artifacts and configuration. Typed Pulse findings, verifications, and the terminal receipt are audit metadata and are required to make the read-only judgment durable. |
| What would justify revisiting it? | Evidence that the first background agent lacks a capability that only an independently isolated reviewer can safely provide. Any reintroduction must retain one unambiguous receipt owner and prove the additional isolation benefit exceeds its cost and failure surface. |
