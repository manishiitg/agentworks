---
name: "plan-design"
description: "Plan-design playbook: step boundaries, step-type selection, context flow, validation/failure design, anti-patterns. Load when designing a new plan or restructuring one."
external: true
external_note: "External mapping: use plan action=list for the current native plan and step schemas. Read get_plan for expected_revision, then pass a stable request_id and the listed native arguments. Internal plan operation names are plan action values, not standalone external tools. Fetch referenced guidance with help action=topic and its listed topic name. Builder action=chat is an optional delegation path when listed; it is not required for native plan edits."
---
**Plan-editing tool arguments:** Before a plan mutation, read `builder-reference/references/plan-editing-tools.md`. Step fields described below belong inside `add_step.step` or `update_step.changes`; route/group/maintenance fields belong inside `parameters`. Use the live type/action-specific schema; these field descriptions do not authorize flat arguments or extra fields.

## PLAN DESIGN — From Requirements to Steps (DESIGN phase)

For human review, approval, or missing-input boundaries, first read
`read_skill(skills=[{"name":"builder-reference","path":"references/human-in-the-loop.md"}])`.
Choose deliberately between a planned checkpoint, a durable decision, and a
report/chat handoff; unattended schedules must not wait for a human to return.

When a user describes what they want to automate, design and create the best-practice workflow structure using the information available. Do not ask broad planning questions or wait for confirmation before adding steps unless the missing answer would materially change behavior, safety, credentials, scheduling, external side effects, or irreversible actions. When you make a reasonable assumption, state it briefly and proceed.

### Step 1: Identify Durable Workflow Boundaries

Modern agents can handle long context and many tool calls. Do not make one workflow step per tool call, screen action, file read, or small transformation. A step is a durable workflow boundary: it has an output contract, validation gate, retry behavior, and persistent-store responsibilities.

Start with **one large `agent` for each coherent shared-context span**. Its description turn completes and proves that span's agentic outcome. Strengthen the step with machine-checkable evidence/provenance fields and a top-level `validation_schema`, whose failures are repaired in the same conversation. Add authored verification turns only when they cover evidence the automatic gate cannot prove. A reviewer must be able to name the context or execution boundary that an extra step protects.

Split into separate steps only when a boundary buys something concrete that cannot live safely inside that large step:
- Distinct durable output or downstream contract
- Independent validation/retry gate that must succeed, fail, or rerun separately
- Independent retry/failure domain
- Different tool, security, credential, or runtime context
- Downstream consumer needs the intermediate artifact
- Different persistent-store contract (learnings HOW, KB WHAT, database structured state, report data)
- Human decision, approval, or routing checkpoint
- Deterministic execution versus agentic judgment: fixed API/SDK calls, CLI commands, data fetching, parsing, normalization, and mechanical writes belong in scripted steps; reasoning over their results belongs in an agent

Combine actions into one step when they share one objective and output contract, use the same tools/security context, fail and retry together, produce only scratch intermediates, and one validation schema can verify the result.

**Rule of thumb**: one large validated step per shared-context span unless a hard boundary proves otherwise. Many tool calls, internal phases, proof checks, and corrections can belong in that step; contexts that should not be shared or genuinely independent durable contracts may use separate large sequences.

### Step 2: Choose the Right Step Type

| Work | Step |
|---|---|
| A coherent task requiring judgment and tools | `agent`: complete task in the description, optional explicitly authored later phases, backend validation and repair in the same conversation |
| Deterministic Python work | `scripted`: saved code, typed inputs, real outputs and validation |
| A specialist that is reused, learns from past runs, or that people want to talk to | `crew`: explicit call to a named Crew agent (see Step 4) |
| A major fixed fork | `routing` |
| A small fixed fork | `branch` |

Default to one Agent per shared-context span. Split for independent outputs,
retries, credentials, tools or human boundaries. Keep its task and policies in
the description. Scripted items and scripted predefined_routes are supported
helpers; LLM child agents are retired. Put required contributions in the task,
under existing store grants. No automatic reflection phase runs.

**Default to one large Agent per shared context.** Give each sequence one coherent agentic outcome: the description should complete the task, re-open its evidence and prove every success criterion. Improve its proof/evidence contract and top-level `validation_schema` before splitting. Automatic validation repairs failures in the same conversation; authored follow-ups are optional. Multiple large sequences are correct when their contexts should not be shared because of credentials, independent outputs/retries, clean-room independence, human/routing boundaries or context contamination. The builder must decide this from the workflow semantics and state the boundary.

**Deterministic fetcher → agentic processor is the default data architecture.** Batch fixed API/SDK/CLI acquisition, pagination, stable parsing and persistence behind one source/auth/retry/output contract; do not create one step per endpoint or command. Persist authoritative results and provenance, then let the Agent interpret them. When selecting the request itself needs judgment, the Agent writes a request specification, a scripted step executes it, and an Agent interprets the result. Do not spend an LLM turn reissuing a known request or parsing a stable response shape. A declared typed scripted helper can support evidence-driven lookups during the task.

**Composition examples** (adapt to the actual task):
- **Multiple modes:** one routing selector chooses the major mode; connect its alternatives to the shared continuation or end.
- **Readiness:** a small scripted connectivity/auth check can feed a branch when the result must choose a different continuation. Keep it inside the owning step when failure should simply stop that work.
- **Draft → review → action:** supply the actual draft and evidence at the human boundary, then perform the authorized action. Follow `references/human-in-the-loop.md` for existing authorization and unattended schedules.

Before changing a step's behavior, output or database contract, read `references/plan-change-impact.md` and reconcile downstream steps, validation, dashboards and shared knowledge.

### Step 3: Design context flow

Every step reads from prior steps and writes for downstream steps:
- **description** is the complete executable first user turn for description-first-v1 agents, including agents with predefined_routes. It runs ALONE before optional items. task rules belong in the description's Rules section; items[] are useful later phases or semantic checks and may be empty. Unmigrated agents retain their saved charter contract. A scripted `scripted` step describes its deterministic contract and `main.py` implements it. For `routing`, leave it empty because routing never runs an agent. Legacy non-scripted steps temporarily use the description through the compatibility adapter. Before writing or editing any of these descriptions, call `read_skill(skills=[{"name":"builder-reference","path":"references/step-description.md"}])` — it covers writing an optimized description, not just choosing the right step type.
- **context_dependencies**: Files from prior steps this step needs (e.g., ["login_status.json"])
- **context_output**: The file this step produces (e.g., "extracted_data.json")
- **The flow must be forward-only** — no circular dependencies
- Use JSON for structured data consumed by downstream steps. Keep output files < 100KB. For a final human-readable report or analysis, **prefer `.md`** — markdown renders richly in the file viewer (headings, tables, lists), and unlike HTML gets clickable workspace file links; it is also simpler and more robust to author. Reach for HTML only when you genuinely need a rich/branded layout markdown cannot express — and for a real dashboard, author one or more `db/reports/*.html` documents that read live data through `window.report`. For prose appended into learnings/KB, use Markdown.

### Step 4: Scripted helpers or Crew

A Goal Agent can invoke declared Python helpers with typed inputs and outputs.
Keep these as scripted `predefined_routes` or authored scripted batches. LLM
specialists, ongoing memory and multi-agent coordination belong to an explicit
Crew step. Do not put conversational child agents or ad hoc LLM spawning inside
a Goal step. Preserve existing authored phases unless reviewed as redundant.

Keep workflow agent steps simple and put specialist work in a Crew, where people
can see, chat with and tune it (owner, 2026-10-19). Suggest moving a step to a
Crew when any one is true: the same kind of work is needed by more than one
workflow or route; it gets better by remembering past runs; or a person would
want to ask it questions or tune it. Length or skills alone are not a reason.
Keep mechanical work (fetch, transform, validate, deploy) and writing this
workflow's own records in the workflow. Suggest, never move on your own: the
owner decides. A Crew cannot write this workflow's database or files, so the
Crew step returns its result (saved as the step's output) and a short workflow
step after it saves that result where the workflow keeps its records.

### Step 5: When to Use Agent

`agent` is the **default** step type (see Step 2). Use it whenever ordered agent turns share the same working context and build on each other, and the boundary between turns is not a durable workflow boundary — which is most agentic work, since the default shape is **execute the description, then automatic validation/repair; add authored follow-ups only when useful**. This is better than several scripted steps that re-read the same files, need each other's transient reasoning, and produce only one final output.

- **Description is the first turn alone.** Put the complete runnable task in `description`, task-specific rules in its Rules section, and use `prompt_contract="description-first-v1"`. `items: []` is a normal one-turn agent.
- **Give the description the complete outcome.** It performs all routine sub-actions, tool calls and checks before any authored follow-up runs. Do not copy it or a generic execute instruction into item zero.
- **Consume deterministic evidence.** Fixed API/SDK/CLI acquisition and stable parsing belong in upstream scripted steps. The agent spends its turn on judgment and synthesis.
- **Choose extra turns deliberately.** Automatic top-level validation runs after all work, with its existing repair loop. A semantic verifier may be useful for research or business evidence that structural checks cannot prove. It is optional; never require an execute/verify/repair sequence.
- Add a separate sequence item only when it changes the agent's job: validation or critique, correction from observed evidence, a real intermediate gate, new external input, or a deliberate role/perspective change. A sequence of tiny routine instructions recreates the same fragmentation as too many scripted steps.
- Prefer it when turns share the same inputs, tools, credentials, runtime, and security context; fail/retry together; and can be validated as one unit.
- Keep separate scripted steps only when deterministic work has its own durable artifact, independently rerunnable validation/failure domain, tool/security context, or downstream consumer. A desire to double-check the same final output is not a separate-step reason.
- Add learning / knowledgebase / db update items as user messages at the exact point they should happen.
- Add explicit reference-check, hallucination-check, critique, or self-validation items when reliability needs it.
- Plain items inherit the step-level KB, DB, and learnings permissions, just like scripted steps. Use a non-empty `write_access` object or `kind` only to narrow a particular turn; an item can never escalate beyond the step configuration. See `read_skill(skills=[{"name":"builder-reference","path":"references/agent.md"}])`.
- Deterministic code lives in saved scripted script definitions with explicit inputs, outputs, and validation. Run them as standalone plan steps or reference orphan scripts in a `scripted` batch item; never embed code in a message item.
- For row/item iteration, use a `foreach` item inside agent when one shared conversation should process every row. A SQL-selected worklist does not by itself require delegation. For known script batches, use a `scripted` item; add routes only when the parent owns substantive adaptive strategy.

### Step 6: When to Use Routing or Branch (brief)

Use `routing` or `branch` when the next step must be **exactly one of N mutually exclusive paths** (e.g., "did login succeed, hit MFA, or fail?"). Both are deterministic: a caller or prior step must provide `route_selection.json` (or `route_selections`) with the selected route. For running every known sub-task, use an agent or explicit plan steps. Use Crew when ongoing specialist work is needed.

**Routing is now the "route" concept: a major, self-contained sub-workflow fork** — use it when the alternatives lead to substantially different continuations of the plan. **Branch is the small in-flow decision** — use it for a lightweight fork that converges back quickly. File-based selection mechanics are shared; branch additionally supports `route_source="human"` for fixed-choice decisions. **A plan has at most one routing step** — the mode selector whose route schedules pick via `route_selections`, each route a sub-workflow of many steps. Every further fixed choice — any simple if-condition, anything with an option that goes straight to `end` — is a branch; `add_step` rejects a second routing step and any route to `end`.

Both have one mode: leave `description` and `context_output` empty, read an existing route file/source, then switch. The common case is that the builder/caller selects the fixed option from the user's request with `route_selections`. If an agent/probe/judgment is needed, add a prior `agent` step that writes `route_selection.json` and have the routing/branch step consume it with `route_source_file` or `context_dependencies: ["route_selection.json"]`. Each `routes` entry needs a stable `route_id`, a `condition` explaining when that route should be selected, and a `next_step_id` that points to another step in the plan (routing/branch routes do **not** define inline sub-agents — they branch to existing steps); set `default_route_id` only as a missing-file fallback.

For full route structure, file contract, and anti-patterns, call `read_skill(skills=[{"name":"builder-reference","path":"references/routing.md"}])` for routing (the "route"/major-fork concept) or `read_skill(skills=[{"name":"builder-reference","path":"references/branch.md"}])` for branch (the small in-flow decision) — load before designing or repairing either kind of step.

### Step 7: Design Validation

Every step MUST have a **validation_schema** — the automated gate that pass/fails the step:
- Check file existence, required fields, value types, patterns, and lengths
- Include enough checks that stale/leftover files from previous runs can't pass
- For agents with routes: validation passing is still the completion signal; successful child calls alone are not completion
- For agent steps: the runtime automatically runs the step-level schema after the final work turn and repairs failures in the same conversation. Add explicit prevalidation items only for intermediate gates.
- **When a field's `value_type` is `object`, also add nested `json_checks` for its expected keys** (e.g. `$.semantic_balance.real_journey_count`, not just `$.semantic_balance`). A bare `{"value_type": "object"}` only rejects the wrong outer type — it accepts any shape at all, including one with none of the fields anything downstream actually reads. Worse, it gives the authoring agent no way to know what the object should contain, so it has to guess; a wrong guess (e.g. writing a descriptive string instead, since the field name alone doesn't say "object") then fails validation with no clue what shape was actually expected, and the automatic repair turn that follows has to go searching elsewhere in the workspace for a definition that was never written down. Name every required key up front instead.

Step-level `success_criteria` is deprecated. Rely on a strong `description` plus `validation_schema` instead.

### Step 8: Think About Failure Modes

- If a step might fail due to external factors (login, API), add clear error handling in the description
- If the description turn and automatic gate cannot prove particular semantic evidence, add a focused optional verification turn inside its `agent`. Do not require a generic proof/verify/repair sequence. Use a separate validation sequence only when the context must be isolated for clean-room independence, different permissions/tools, or its own rerunnable artifact/failure domain.
- If a step is flaky, first add explicit retry/polling and proof checks inside the step; split the unstable part only when it needs independent retries or isolation

### Design Anti-Patterns to Avoid

- **Monster boundaries**: A single step owns unrelated durable outputs, validation gates, failure domains, or persistent stores — split at those boundaries. Many tool calls alone are not a reason to split.
- **Trivial steps**: A step that just reads a file and passes it through — merge with the consumer
- **Over-splitting same-context turns**: Several scripted steps mostly reread the same context and depend on each other's transient reasoning. Collapse into one `agent`; verification, critique, double-checking, and repair belong inside it unless a check truly needs an independent durable artifact, retry domain, or tool/security context.
- **Missing validation**: No validation_schema means no automated quality gate
- **Vague or bloated descriptions**: "Process the data appropriately" is too vague — state WHAT the step must achieve, its scope and success criteria, and WHERE its result belongs. Put reusable HOW-to-execute guidance in accessible skills/learnings and output structure in `validation_schema`. The opposite failure is just as real: restating the same instruction from several angles, copy-pasting shared policy into every step that needs it, or spelling out a rigid procedure a judgment call didn't need. See `references/step-description.md`.
- **Over-sequencing**: Steps that don't depend on each other can potentially run in parallel via independent step groups
- **Hidden specialist agents**: use an explicit Crew step for LLM specialists; predefined_routes are typed scripted helpers only.

### Step Types Reference

- **Agent** (type: "agent") — **the canonical agent step**: one description-only first user turn plus optional subsequent `items` in a persistent conversation. Complete the coherent job in the description turn; Builder chooses any necessary semantic verification or later phases. Task-specific rules belong only in the description. It supports foreach turns, intermediate prevalidation gates, scripted batches, and optional scripted `predefined_routes` for typed Python helpers. Its top-level validation_schema is automatically enforced as the final gate with same-conversation repair retries. The task and its authored phases run once per step execution.
- **Scripted** (type: "scripted"): an explicitly scripted deterministic boundary for fixed API/CLI/data work. New scripted steps are automatically declared `scripted`; use `agent` for every conversational or judgment-heavy step, including one-turn work.
- A reusable scripted route may declare `script_parameters`: a small named, typed contract (`type`, `description`, optional `required`, `default`, `enum`) for controlled per-call variation. Keep credentials in Secrets and file dependencies in `context_dependencies`; do not encode dynamic route behavior as rewritten code or narrative instructions.
- **Routing** (type: "routing"): N-way deterministic branching for a major sub-workflow fork. Reads `route_selection.json` (or caller `route_selections`) and picks exactly one **routes[]** entry. Each route has **route_id**, **condition**, and **next_step_id** (pointer to an existing step). Optional **default_route_id** is a missing-file fallback. Optional **route_source_file** points at a prior step's route file.
- **Branch** (type: "branch"): shares file-based selection with Routing, for a small in-flow next-step decision instead of a major fork. Uses **branch_question**; additionally supports **route_source="human"** for a fixed-choice prompt.
- **Orphan** (is_orphan: true): Not part of the main execution flow. Orphan steps are plan-local reusable definitions and manual utility agents. Use them for data checks, environment validation, one-off investigations, or shared sub-agent definitions that multiple agents in the same plan may reuse. Reuse is explicit: an orphan step must declare `shared_with.agent_ids`, and an agent route must point to it with `orphan_step_ref`. Do not assume every orphan step is shared with every agent.

Known values belong in declared launch variables. Set `required_variables` on their first consumer so missing values fail before work starts on the selected route. Use `human_feedback` inside an agent for a live CAPTCHA or other urgent interaction; use `create_human_input_request` for a durable request.

### Inner Steps

Inner steps live inside an agent's `predefined_routes[].sub_agent_step` (including
the legacy orchestrator compatibility shape). They have their own step IDs and
can be individually executed and configured via **execute_step** and
**update_step_config** using the inner step ID. Routing steps do not have inner
steps—their `routes[].next_step_id` points to existing plan steps.

### Reusable Orphan Route Pattern

When an agent route should reuse an orphan step:
- Put the reusable step definition in `orphan_steps[]`.
- On that orphan step, set `shared_with.agent_ids` to the IDs of the parent agents allowed to reuse it.
- On the route, set `orphan_step_ref` to the orphan step ID instead of embedding an inline `sub_agent_step`.
- Use inline `sub_agent_step` only when the route needs its own dedicated definition.

### Workflow boundaries and metrics
A workflow groups work that needs to be planned, executed, and evaluated together.
Multiple goals or primary metrics do not require multiple workflows. Keep work together
when investigations, changes or routine tradeoff decisions overlap. Suggest splitting
when work operates independently, especially across owners or permission boundaries;
sharing a server alone is insufficient. Different schedules can use separate routes.
Use /setup-goals to configure primary metrics and linked supporting measurements;
explain boundary recommendations and preserve existing boundaries unless authorized.
