[← platform / learnings-knowledge](index.md)

# PLAT-190 — proposal: make sure agents act on skill guidance for plan/report/config tools

| Field | Value |
|---|---|
| State | in progress |
| Priority | P2 |
| Product | platform |
| Area | learnings-knowledge |
| Summary | P2 — no live defect; a requested reliability improvement. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `partially implemented` — primary fix (embed the load-bearing rule directly in the tool description) applied across a full audit of every registered tool (50+), not just the original five categories; the narrow hard-gate option for `set_workflow_llm_config` remains unbuilt, correctly, per the revised recommendation below |
| Last synchronized | `2026-08-27` |

- **Priority:** P2 — no live defect; a requested reliability improvement. Today
  every "read the reference doc/skill before doing X" rule (plan-mutation
  tools, report tools, LLM-config tools, schedule tools, secret tools) is
  prompt-steering only, and [PLAT-189](../../goals/evaluation/plat-189.md)) found at least one case
  (`message_sequence`'s prior lack of proactive schema surfacing) where an
  agent could plausibly act without the context it needed.
- **Owner:** guidance/tool-schema text (`cmd/server/guidance`, tool
  descriptions in `planning_agent.go` and friends) for the primary fix;
  `set_workflow_llm_config`'s call site only, if a hard gate is ever rebuilt
  for that one tool.
- **Related:** grew out of [PLAT-189](../../goals/evaluation/plat-189.md))'s investigation. Revised
  after re-reading two documents that should have been checked before the
  first draft: `docs/design/agent_tool_surface_single_source.md` and
  [PLAT-125](../../goals/step-execution/plat-125.md)), the platform's own shipped answer to a near-identical
  problem.

## Original proposal (first draft, 2026-08-27) — demoted, not deleted

The first draft proposed a hard runtime gate: block a tool's execution until
a session-scoped tracker observed a `read_skill` call for that tool's
required doc, enforced at `LLMAgentWrapper.RegisterCustomToolWithTimeout`.
Kept below for the record and because its narrowest form (see "What's still
worth building," below) is not fully ruled out — but the broad "all tools"
version does not fit how this platform has decided to handle this class of
problem, for reasons found after the draft was written, not before.

## Why the broad hard-gate version is the wrong shape here

Two things this codebase already learned the hard way, at real incident cost,
argue against it:

1. **`docs/design/agent_tool_surface_single_source.md`**: *"Focus rules live
   in the prompt; code enforces authority only."* Its test for what belongs
   in Go: is this something the agent should not be **trusted** to decide, or
   merely something it might get wrong? Only the first belongs in code. "Did
   you read the doc first" is a focus/discipline rule, not an authority
   boundary like "a reviewer must not mutate the workflow DB." Putting it in
   Go as well as the prompt is exactly the "one-decision-two-places" pattern
   that document exists to eliminate — three separate incidents (Video Studio
   secrets, `list_llm_capabilities`, a builder background-child registration
   bug) all trace back to a rule living in two places that quietly disagreed.
2. **PLAT-125** already shipped the platform's real answer to "make sure the
   agent has the context it needs before it acts," for a directly comparable
   incident: a step agent was handed the wrong 41-doc bundle, followed
   guidance to call a tool it didn't actually have, and invented provider
   names when the call failed. The fix was **not** a call-time gate — it was
   capability-derived doc *attachment at construction time*: a doc is
   attached to a session only when (and always when) the session actually has
   the tools that doc describes. There is no "did they read it" question to
   enforce, because the content is simply already present whenever the tool
   is. Measured result: 40 attached docs down to 4, zero `tools_unavailable`
   afterward.

PLAT-125's pattern is the better fit for **step-execution** agents (narrow
tool surface, docs selected from real construction-time signals). It doesn't
transfer directly to the **workshop/builder** tools this ticket is actually
about (`update_message_sequence_step`, report tools, etc.) — `builder-reference`
is already attached wholesale there, so the problem isn't "does the agent
have the right doc," it's "did it act on the right section of a doc it
already has." That's a narrower, still-real problem, just not one PLAT-125's
exact mechanism solves.

## Revised recommendation

**Primary fix — embed the load-bearing rule directly in the tool's own
description**, not in a separately-fetched doc the agent has to remember to
open. A tool's schema/description is shown to the model every time the tool
is offered, in every turn — unlike a reference doc, which only reaches
context if the agent chooses to call `read_skill` for it. This has no
enforcement gap by construction, because there's nothing to forget: the rule
travels with the tool. Several tool schemas already do this partially (e.g.
`add_message_sequence_step`'s description already tells the model to
`read_skill(...)` inline) — the fix is to go further and state the *actual
rule* (not just a pointer to go read it elsewhere) wherever it's short enough
to state directly, reserving `read_skill` pointers for genuinely large
reference material (patterns, worked examples) that can't reasonably live in
a tool description.

**What's still worth building — narrowly, not "all tools."** The original
gate design (session-scoped `SkillReadTracker` observing `read_skill` via
`mcpagent.AgentEventListener`, enforced at
`LLMAgentWrapper.RegisterCustomToolWithTimeout`) stays architecturally sound
*if* a specific tool clears the design doc's own authority bar — genuinely
consequential, not just "would be nice if the agent read the doc first."
`set_workflow_llm_config` is the one candidate already identified: it's the
exact tool the original, now-removed `WithDocPrecondition` gate protected,
and changing a workflow's LLM config is a real, hard-to-casually-reverse
action. If a hard gate gets built at all, scope it to that one tool first,
prove it doesn't repeat the old gate's failure (recognizing `read_skill`
correctly, refusing with an instructive message, not silently hiding the
tool — see "Read channels" below), and expand only if a second tool
independently clears the same bar. Do not build the general engine for a
broad tool set up front.

### Read channels (still true, kept from the first draft)

If any gate is built, it must still recognize every current valid read
channel or it repeats why `WithDocPrecondition` was removed
(`get_reference_doc` is confirmed fully gone from production code in all
three repos as of this investigation — grep-verified, zero non-test
references):

- `read_skill` — intrinsic, described in its own tool description as working
  "on every transport" (`mcpagent/agent/skill.go:19`).
- A direct shell/file read of the projected file — `instructions.go:68`
  explicitly sanctions this as an equally legitimate path for CLI-native
  sessions with shell access. A gate that only watches `read_skill` will
  falsely block a session that did the right thing this way.

## Implemented (2026-08-27)

Audited all 50+ registered tools (four parallel research passes, this ticket's
five original categories plus execution/config/skill-management,
DB/browser/media, and Pulse-lifecycle/remaining plan-step tools), classifying
each description as already-inline, pointer-only-worth-inlining,
pointer-only-genuinely-large, or a real gap. Most tools — `set_workflow_llm_config`,
every media/provider tool, `agent_browser`, `query_workflow_db`,
`query_workflow_costs`, every Pulse lifecycle tool, execution/inspection
tools, workflow-config tools — were already best-in-class: precise, inline,
self-contained. Fixes applied where a real gap was found:

- `update_message_sequence_step`: every field was bare (only
  `existing_step_id`/`reason` had descriptions); now mirrors
  `add_message_sequence_step`'s inline rules for `description`/`items`, with
  "if omitted, preserved" framing and a `message-sequence.md` pointer added to
  `items`.
- `update_scripted_step`: `description` field now states the same
  deterministic-execution-contract rule `add_scripted_step` already had.
- `add_message_sequence_step`: `items` gained the `message-sequence.md`
  pointer it was missing.
- `update_todo_task_route`: `sub_agent_step.description` was completely bare
  (unlike `add_todo_task_route`'s equivalent field); now restates the "this
  IS EXECUTED as the opening instruction" rule, and `items` gained the same
  `message-sequence.md` pointer.
- `create_schedule`, `update_schedule`, `create_calendar_schedule`: added a
  pointer to `references/workflow-tools.md` — **after catching that the
  originally-planned target, `references/schedule-management.md`, does not
  exist anywhere** (no `guidance.go` registry entry, no physical file). That
  stale name was traced to three places — `multi_agent_chat_refdoc_e2e_real_test.go`
  (two variants) and a doc-comment — all referencing a multi-agent-chat-level
  global schedule feature that was deliberately removed (see
  `TestGetMultiAgentDelegationInstructionsLazyLoadsSecret`'s "removed"
  assertions in `delegation_tools_test.go`), left stale behind those tests'
  cost/binary gates (`RUN_MULTIAGENT_REFDOC_E2E`/`RUN_MULTIAGENT_REFDOC_CC_E2E`,
  off by default) with nobody noticing the drift. Removed the corresponding
  stale test cases (one per test variant, plus a stale conversation turn in
  the multi-turn e2e) rather than leaving them pointing at a doc that will
  never exist.
- `mutate_workflow_db`: description previously listed `INSERT, UPDATE,
  DELETE` as unremarkable equals, contradicting `stores.md`'s repeated
  "upsert by primary key, never DELETE/overwrite" rule. Now states the
  upsert-over-DELETE rule and points to `db/README.md` for the table's PK.
- `list_skills`, `import_skill`, `uninstall_skill`, `search_skills`,
  `install_skill`: none stated that installing a skill does not attach it
  anywhere — a step only receives a skill via explicit `enabled_skills`,
  confirmed against `skill-management.md`'s "step execution does not inherit
  workflow-selected skills" rule. All five now state the
  install → `add_skills` → `enabled_skills` chain (or the corresponding
  removal-order caution for `uninstall_skill`).

**Verification:** `go build ./...` clean across the whole repo.
`TestAllSchemaFunctionsReturnValidJSON` passes (catches malformed JSON in the
hand-edited schema strings). Full `step_based_workflow` and `cmd/server`
package suites run; confirmed via `git stash`/re-run that every failing test
in both packages (2 in `step_based_workflow`, 15 in `cmd/server`, 2 in
`cmd/server/guidance`) fails identically on the pre-edit baseline — none
introduced by this pass. **New side finding, not investigated further here:**
`cmd/server`'s pre-existing failure count (15, spanning Pulse
finding/verification/result tools, `TestGateCanRunOpsWithoutEngineering`,
`TestToolSetInvariants`, several others) is larger than previously known in
this session — only `TestDesignPlanGuidanceSupportsReadOnlyPulseChecklist`
had been spot-checked before. Worth its own triage pass; out of scope here.

## Explicitly out of scope for this ticket

- The broad "all tools and skills" hard-gate engine, as originally scoped, is
  not being built — see "Why the broad hard-gate version is the wrong shape
  here."
- The narrow hard-gate for `set_workflow_llm_config` remains unbuilt — this
  ticket only argues it's the one candidate that could justify one, not that
  it should be built now.
- Triaging `cmd/server`'s 15 pre-existing test failures — separate finding,
  needs its own pass.

## Verification

See "Implemented" above.
