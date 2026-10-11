**Plan-editing tool arguments:** Before a plan mutation, read `builder-reference/references/plan-editing-tools.md`. Step fields described below belong inside `add_step.step` or `update_step.changes`; route/group/maintenance fields belong inside `parameters`. Use the live type/action-specific schema; these field descriptions do not authorize flat arguments or extra fields.

## PLAN DESIGN — From Requirements to Steps (DESIGN phase)

For human review, approval, or missing-input boundaries, first read
`read_skill(skills=[{"name":"builder-reference","path":"references/human-in-the-loop.md"}])`.
Choose deliberately between a planned checkpoint, a durable decision, and a
report/chat handoff; unattended schedules must not wait for a human to return.

When a user describes what they want to automate, design and create the best-practice workflow structure using the information available. Do not ask broad planning questions or wait for confirmation before adding steps unless the missing answer would materially change behavior, safety, credentials, scheduling, external side effects, or irreversible actions. When you make a reasonable assumption, state it briefly and proceed.

### Step 1: Identify Durable Workflow Boundaries

Modern agents can handle long context and many tool calls. Do not make one workflow step per tool call, screen action, file read, or small transformation. A step is a durable workflow boundary: it has an output contract, validation gate, retry behavior, and persistent-store responsibilities.

Start with **one large `agent` for each coherent shared-context span**. It should complete that span's agentic outcome, then prove and repair it without discarding conversation context. Strengthen the step with machine-checkable evidence/provenance fields, a top-level `validation_schema`, and focused verify-and-repair turns before adding another workflow step. A reviewer must be able to name the context or execution boundary that the extra step protects.

Split into separate steps only when a boundary buys something concrete that cannot live safely inside that large step:
- Distinct durable output or downstream contract
- Independent validation/retry gate that must succeed, fail, or rerun separately
- Independent retry/failure domain
- Different tool, security, credential, or runtime context
- Downstream consumer needs the intermediate artifact
- Different persistent-store contract (learnings HOW, KB WHAT, database structured state, report data)
- Human decision, approval, or routing checkpoint
- Deterministic execution versus agentic judgment: fixed API/SDK calls, CLI commands, data fetching, parsing, normalization, and mechanical writes belong in scripted regular steps; reasoning over their results belongs in an agent

Combine actions into one step when they share one objective and output contract, use the same tools/security context, fail and retry together, produce only scratch intermediates, and one validation schema can verify the result.

**Rule of thumb**: one large validated step per shared-context span unless a hard boundary proves otherwise. Many tool calls, internal phases, proof checks, and corrections can belong in that step; contexts that should not be shared or genuinely independent durable contracts may use separate large sequences.

### Step 2: Choose the Right Step Type

**`agent` is the agent step.** Its authored items define the durable
conversation phases. Optional `predefined_routes` expose bounded specialists;
the same agent interprets evidence, decides what to delegate next, changes direction, and judges whether the goal has been met.

The sequence's own LLM conversation performs reasoning, verification, repair,
and reporting. Scripted children perform deterministic work under declared
input/output contracts. Agentic routes are optional capabilities selected by
the sequence agent at runtime. Do not add routes merely to run a fixed agent
checklist; use explicit agent plan steps when isolated agentic work
is predetermined.
Parallel scripts or waiting for every result do not by themselves require an orchestrator.

**Supported scripted execution:** an authored `scripted` item references saved
regular scripts in `orphan_steps`, validates typed parameters, runs the finite
batch with `max_parallel` (default sequential, maximum 8), and waits for every
result before advancing. Scripts retain their own permissions and validation;
there is no child LLM, automatic script repair, or automatic retry in this path.
A failed batch stops the sequence with per-call outcomes. See
`references/agent.md` for authoring and remaining limits.

Add `predefined_routes` only for bounded agentic or scripted specialists the
sequence agent may choose adaptively. The runtime supplies the sub-agent tools
and asynchronous completion lifecycle when routes exist. SQL `foreach` remains
sequential conversational iteration; known deterministic batches use a
`scripted` item rather than agentic delegation.

Examples:
- **Agent:** run ten specified scripts, account for all ten results,
  then use the parent conversation to analyze and report.
- **Agent with routes:** investigate falling performance, choose analyses from the
  evidence, resolve contradictions, and revise the investigation until the
  explanation is supported. The parent owns that reasoning and may write the report.

For any batch, define the expected task IDs, require a terminal result for every
task, and distinguish success, failure, cancellation, and missing work. A report
is not proof that all tasks ran. Completion tracking and waiting should be
enforced by the runtime; validation must check coverage against the expected set.

| Scenario | Step Type | Why |
|----------|-----------|-----|
| Agent does work, then verifies/fixes it against the success criteria (the common case) | **Agent** (default) | One shared conversation: do → verify → fix, as ordered items. Keeps the agent's full working context instead of handoff artifacts between regular steps |
| Fixed API/CLI/data work with deterministic inputs, outputs, and validation | **Regular** (`scripted`) | Runs checked-in code without spending an LLM turn; new regular steps are reserved for this boundary |
| Parent must interpret evidence and decide or revise the work needed to reach the goal | **Agent with predefined routes** | The sequence agent owns strategy and substantive reasoning; isolation, parallelism, and worker type alone do not qualify |
| Incident debugging / root-cause investigation: which source to query next (error tracker, logs, commits, code) and with what window depends on what the evidence so far shows | **Agent with predefined routes** to small scripted fetchers | Each fetch stays a deterministic script taking `script_parameters` (issue ID, log group + time window, repo + since), so the agent can re-query narrower or elsewhere as the hypothesis shifts without inventing data. Take the issue link or symptom as run input (`variables` / `human_inputs`), not a blocking Human Input step, so Slack and scheduled runs can start it |
| Need to branch based on prior step output or context | **Routing** | Supported branch primitive — reads `route_selection.json` and picks a route |
| A person must decide between a few fixed options mid-run (approve/hold, yes/no, pick one) | **Branch** with `route_source: "human"` | Routes are the options; schedules answer up front via `route_selections`, unattended runs use `default_route_id`, interactive runs ask. Successor to `yesno`/`multiple_choice` human input. |
| Need a free-form value from the user before proceeding (an ID, a month, a note to format) | **Human Input** (`text`) | Blocks until the user responds; captures into `variable_name`. `yesno`/`multiple_choice` are no longer accepted for new steps — use the human branch above. |
| Work belongs to a persistent specialist with its own memory, skills, and files (reviewer, researcher, operator) | **Crew** (`crew`) | Invokes one Crew trigger and waits for its final text response. The Crew keeps its context across runs; the step only carries the instruction and runtime input. Top-level steps only. |
| User already told the builder which fixed branch to run | **Routing** / **Branch** | The builder/caller passes `route_selections` to `run_workflow` / `run_full_workflow`; do not add a step just to ask the same choice again. |
| Utility/debug tool available but not auto-run | **Orphan** (is_orphan: true) | Not in main flow; manual execution from workshop only |

**Default to one large Agent per shared context.** Give each sequence one coherent agentic outcome. Modern agents do a lot in a single long-running turn, so begin with one shared-context conversation for each coherent agentic span: `[do the whole span] → [re-open source evidence and prove every criterion] → [repair every gap and double-check the final result]`. Improve its description, proof/evidence contract, top-level `validation_schema`, and verify/repair turns before considering more steps. Multiple large sequences are correct when their contexts should not be shared—for example because they have different credentials/security exposure, independent outputs/retries, clean-room independence, human or routing boundaries, or unrelated context that would distract or contaminate the next agent. The builder must decide this from the workflow semantics and state the boundary. Use **Agent even for one-turn conversational work**. Add **predefined routes** only when the sequence agent owns substantive runtime strategy and must choose specialists dynamically. Use **Regular** only when deterministic work is implemented as a checked-in script, and **Branch**/**Routing** only for real fixed branch choices — **Branch** for a small in-flow decision, **Routing** when the choice forks into a major, self-contained sub-workflow.

**Deterministic fetcher → agentic processor is the default data architecture.** Put fixed API/SDK requests, CLI commands, pagination with known rules, parsing, normalization, and mechanical database/file writes in one or a few `regular` steps declared `scripted`. Batch related calls when they share credentials, retry policy, source, and output contract; do not create one step per endpoint or command. Where an agent may need to decide what to fetch (a lookup, an investigation, a narrower re-query), parameterize the fetcher (typed `script_parameters` for the ID, time window, filter or scope, with defaults for the main flow) so the same script can serve that agent. When nothing about the call is ever decided by an agent, keep it a pure script in the fixed flow with no invented parameters. Give each fetcher an explicit authoritative output (prefer canonical rows in `db/db.sqlite`, otherwise a compact JSON artifact), provenance/freshness fields, fail-closed error handling, idempotency where relevant, and deterministic validation. Then let one large `agent` read those persisted results and perform the judgment-heavy analysis, synthesis, critique, and repair. Do not spend an LLM turn reissuing a known request or parsing a stable response shape.

**When the fixed pipeline is the wrong shape:** it fetches the same bundle every run. If which sources to query, and with what parameters, depends on evidence found along the way (incident debugging, root-cause analysis, open-ended investigation), do not pre-fetch a fixed bundle. Give the agent `predefined_routes` to small scripted fetchers that take `script_parameters` (Step 4), so fetching stays deterministic while the agent decides what to look at next.

If selecting the next call genuinely requires live judgment, keep the decision agentic but isolate deterministic execution: the agent produces an explicit request/specification, a scripted regular step executes it, and a later agent interprets the result. Browser/UI navigation remains agentic unless it is genuinely stable and the user explicitly wants a scripted browser path. Human approval still precedes consequential side effects even when the approved API/CLI action itself is deterministic.

**Composition examples** (adapt these to the task; they are not additional step-type rules):

- **Multiple workflow modes:** one `routing` selector chooses a major mode. Keep shared setup outside its alternatives where practical, and explicitly connect each alternative's terminal step to the shared continuation or `end`. Use `references/routing.md` for the contract.
- **Readiness before expensive work:** use a small scripted API/auth/connectivity check, or a conversational check when judgment is needed. If its result must choose retry/reauthenticate/continue, follow it with a `branch` consuming its validated status or route-selection file. If failure should simply stop the owning step, keep the check there instead of adding a separate probe and switch. Small checks are branches, not additional major routing selectors.
- **Draft → review → action:** prepare the actual draft and supporting evidence before the human decision. A known API/CLI action after approval belongs in a saved script; use another agentic span only when the approved work needs further judgment. For existing authorization, unattended schedules, and durable per-item decisions, use `references/human-in-the-loop.md`.

**Whenever you change an existing plan** — add / remove / reorder a step, or change a step's output contract, db writes, or behavior — run `read_skill(skills=[{"name":"builder-reference","path":"references/plan-change-impact.md"}])` before treating it as done and reconcile the blast radius (downstream steps, evals, report dashboard, db, learnings, KB). A step change ripples into everything that reads it; don't leave silent breakage behind.

### Step 3: Design Context Flow

Every step reads from prior steps and writes for downstream steps:
- **description** defines the durable system-level charter for an agent step: its objective, boundaries, and definition of done. For `agent` (including agents with `predefined_routes`), `items[]` are the ordered user messages that tell the agent how to execute and verify that charter. A scripted `regular` step describes its deterministic contract and `main.py` implements it. For `routing`, leave it empty because routing never runs an agent. Legacy non-scripted regular steps temporarily use the description through the compatibility adapter. Before writing or editing any of these descriptions, call `read_skill(skills=[{"name":"builder-reference","path":"references/step-description.md"}])` — it covers writing an optimized description, not just choosing the right step type.
- **context_dependencies**: Files from prior steps this step needs (e.g., ["login_status.json"])
- **context_output**: The file this step produces (e.g., "extracted_data.json")
- **Flow must be forward-only** — no circular dependencies
- Use JSON for structured data consumed by downstream steps. Keep output files < 100KB. For a final human-readable report or analysis, **prefer `.md`** — markdown renders richly in the file viewer (headings, tables, lists), and unlike HTML gets clickable workspace file links; it is also simpler and more robust to author. Reach for HTML only when you genuinely need a rich/branded layout markdown cannot express — and for a real dashboard, author one or more `db/reports/*.html` documents that read live data through `window.report`. For prose appended into learnings/KB, use Markdown.

### Step 4: When an Agent Needs Specialist Routes

The plan shape is `agent` with optional `predefined_routes`. Routes add specialist capabilities to the same agent conversation.

**Eligibility gate:** add `predefined_routes` to an `agent` only when the agent makes a real runtime
orchestration decision the static plan cannot directly express. Examples are:
- The parent interprets runtime evidence to decide which investigations are needed
- Incident debugging: an error, log excerpt or commit points to the next source to query and its time window
- The parent weighs competing hypotheses and changes subsequent delegation
- The parent reasons about failures and chooses a different recovery strategy
- Interim synthesis exposes missing evidence and changes the plan

The parent may self-execute analysis, decisions, synthesis, and report writing.
Delegate bounded specialist work when isolation or parallel progress helps.
Do not reduce the parent to a dispatcher that only starts, waits, and reports.
Known task lists, SQL-selected rows, parallel fan-out, and fixed retry rules
alone are not substantive strategy decisions.

**A fixed child set and order does not justify adaptive routes.** Different tools,
separate learnings, progress visibility, and easier debugging are supporting
properties after the eligibility gate, not reasons to add routes by
themselves. Use explicit plan steps/dependencies for known independent fixed
work; use one `agent` for known same-context work; use scripted
steps for known deterministic work; use `branch` or `routing` for a fixed
exclusive branch (`branch` for a small in-flow decision, `routing` for a
major sub-workflow fork).

**Create predefined sub-agents (routes)** for tasks that are:
- **Predictable** — same pattern every run, even if inputs change
- **Self-contained** — clear inputs/outputs, can be validated independently
- **Worth optimizing** — complex enough that accumulated learnings improve reliability

Route sub-agents use `agent` for conversational work, including
stateless one-turn work; `regular` is reserved for explicitly scripted
deterministic routes. A routed agent specialist may itself own routes
for one nested delegation layer.

Use an `agent` route when the parent agent should be able to call the
same specialist repeatedly with memory. Normal repeated calls reuse the route
session and send the new instructions as the re-entry user message. Use
`agent_restart=true` only when the parent intentionally needs a clean
rerun that archives the existing route session and replays the configured queue.

**Use the generic agent** (no predefined route) for tasks that are:
- **Dynamic** — unpredictable at design time
- **Trivial** — too simple for a dedicated sub-agent

**Non-example**: A known list such as "process the income, deductions, and credits pages" is not enough to justify three sub-agents. Keep it in one large agent unless those pages require independent tools, retry domains, outputs, or runtime delegation.

### Step 5: When to Use Agent

`agent` is the **default** step type (see Step 2). Use it whenever ordered agent turns share the same working context and build on each other, and the boundary between turns is not a durable workflow boundary — which is most agentic work, since the natural shape is **do the task, then verify and fix it in follow-up items of the same conversation**. This is better than several regular steps that re-read the same files, need each other's transient reasoning, and produce only one final output.

- **Separate charter from turns.** Put the stable objective, boundaries, and
  definition of done in `description`; it becomes the common system-level Step
  Charter. Put execution and verification instructions in `items[]`; each item
  is a user message. Do not copy the description into item 0.
- **Give the work turn the complete outcome.** Let the agent perform all routine sub-actions, tool calls, and internal checks needed for that outcome in one substantial turn; do not create one item per checklist line, source, or tool call.
- **Consume deterministic evidence; do not fetch it conversationally.** Fixed API/SDK/CLI acquisition and stable parsing belong in an upstream scripted regular step. The sequence reads the resulting DB rows/artifacts and spends its turns on judgment, synthesis, semantic validation, and repair.
- **Make verification a follow-up item, not a separate step.** A typical sequence is `[complete the outcome] → [re-open the evidence and verify every success criterion; identify anything unsupported or incomplete] → [repair every verified gap and recheck]`. The verifier turn has the full context of what was just done, so it catches more than a fresh regular step reconstructing from artifacts.
- Add a separate sequence item only when it changes the agent's job: validation or critique, correction from observed evidence, a real intermediate gate, new external input, or a deliberate role/perspective change. A sequence of tiny routine instructions recreates the same fragmentation as too many regular steps.
- Prefer it when turns share the same inputs, tools, credentials, runtime, and security context; fail/retry together; and can be validated as one unit.
- Keep separate scripted steps only when deterministic work has its own durable artifact, independently rerunnable validation/failure domain, tool/security context, or downstream consumer. A desire to double-check the same final output is not a separate-step reason.
- Add learning / knowledgebase / db update items as user messages at the exact point they should happen.
- Add explicit reference-check, hallucination-check, critique, or self-validation items when reliability needs it.
- Plain items inherit the step-level KB, DB, and learnings permissions, just like regular steps. Use a non-empty `write_access` object or `kind` only to narrow a particular turn; an item can never escalate beyond the step configuration. See `read_skill(skills=[{"name":"builder-reference","path":"references/agent.md"}])`.
- Deterministic code lives in saved regular script definitions with explicit inputs, outputs, and validation. Run them as standalone plan steps or reference orphan scripts in a `scripted` batch item; never embed code in a message item.
- As a predefined route, an agent behaves like a reusable specialist sub-agent: reuse the same route for critique, test feedback, validation feedback, or follow-up work that should keep prior context; restart only when the prior conversation is stale, wrong, or contaminated.
- For row/item iteration, use a `foreach` item inside agent when one shared conversation should process every row. A SQL-selected worklist does not by itself require delegation. For known script batches, use a `scripted` item; add routes only when the parent owns substantive adaptive strategy.

### Step 6: When to Use Routing or Branch (brief)

Use `routing` or `branch` when the next step must be **exactly one of N mutually exclusive paths** (e.g., "did login succeed, hit MFA, or fail?"). Both are deterministic: a caller or prior step must provide `route_selection.json` (or `route_selections`) with the selected route. For running every known sub-task, use an agent or explicit plan steps. Give an agent `predefined_routes` when it must reason about what specialist work is needed.

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
- If a step's output needs semantic validation (not just structural), add proof, verification, and repair items inside its `agent` — use a separate validation sequence only when the context must be isolated for clean-room independence, different permissions/tools, or its own rerunnable artifact/failure domain
- If a step is flaky, first add explicit retry/polling and proof checks inside the step; split the unstable part only when it needs independent retries or isolation

### Design Anti-Patterns to Avoid

- **Monster boundaries**: A single step owns unrelated durable outputs, validation gates, failure domains, or persistent stores — split at those boundaries. Many tool calls alone are not a reason to split.
- **Trivial steps**: A step that just reads a file and passes it through — merge with the consumer
- **Over-splitting same-context turns**: Several regular steps mostly reread the same context and depend on each other's transient reasoning. Collapse into one `agent`; verification, critique, double-checking, and repair belong inside it unless a check truly needs an independent durable artifact, retry domain, or tool/security context.
- **Missing validation**: No validation_schema means no automated quality gate
- **Vague or bloated descriptions**: "Process the data appropriately" is too vague — state WHAT the step must achieve, its scope and success criteria, and WHERE its result belongs. Put reusable HOW-to-execute guidance in accessible skills/learnings and output structure in `validation_schema`. The opposite failure is just as real: restating the same instruction from several angles, copy-pasting shared policy into every step that needs it, or spelling out a rigid procedure a judgment call didn't need. See `references/step-description.md`.
- **Over-sequencing**: Steps that don't depend on each other can potentially run in parallel via independent step groups
- **Inline specialist contracts in the parent charter**: If the description contains detailed instructions for one bounded specialist task, move that contract into a route and keep only the parent agent's objective and decision boundaries in the description

### Step Types Reference

- **Agent** (type: "agent") — **the canonical agent step**: one system-level description charter plus ordered user-message `items` in a persistent conversation. Do the coherent job, then verify and fix it in focused follow-up items. It supports foreach turns, intermediate prevalidation gates, scripted batches, and optional `predefined_routes` for adaptive specialist delegation. Its top-level validation_schema is automatically enforced as the final gate with same-conversation repair retries. As a top-level step the queue runs once; as a route it can be re-entered during the same workflow run and receive new instructions without replaying the queue.
- **Regular** (type: "regular"): an explicitly scripted deterministic boundary for fixed API/CLI/data work. New regular steps are automatically declared `scripted`; use `agent` for every conversational or judgment-heavy step, including one-turn work.
- A reusable scripted route may declare `script_parameters`: a small named, typed contract (`type`, `description`, optional `required`, `default`, `enum`) for controlled per-call variation. Keep credentials in Secrets and file dependencies in `context_dependencies`; do not encode dynamic route behavior as rewritten code or narrative instructions.
- **Routing** (type: "routing"): N-way deterministic branching for a major sub-workflow fork. Reads `route_selection.json` (or caller `route_selections`) and picks exactly one **routes[]** entry. Each route has **route_id**, **condition**, and **next_step_id** (pointer to an existing step). Optional **default_route_id** is a missing-file fallback. Optional **route_source_file** points at a prior step's route file.
- **Branch** (type: "branch"): shares file-based selection with Routing, for a small in-flow next-step decision instead of a major fork. Uses **branch_question**; additionally supports **route_source="human"** for a fixed-choice prompt.
- **Human Input** (type: "human_input"): Captures a free-form value with **response_type="text"**. Existing yesno/multiple_choice steps remain supported; new fixed-choice decisions use a human-decided Branch.
- **Orphan** (is_orphan: true): Not part of the main execution flow. Orphan steps are plan-local reusable definitions and manual utility agents. Use them for data checks, environment validation, one-off investigations, or shared sub-agent definitions that multiple agents in the same plan may reuse. Reuse is explicit: an orphan step must declare `shared_with.agent_ids`, and an agent route must point to it with `orphan_step_ref`. Do not assume every orphan step is shared with every agent.

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
