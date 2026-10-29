**Plan-editing tool arguments:** Before a plan mutation, read `builder-reference/references/plan-editing-tools.md`. Step fields described below belong inside `add_step.step` or `update_step.changes`; route/group/maintenance fields belong inside `parameters`. Use the live type/action-specific schema; these field descriptions do not authorize flat arguments or extra fields.

**Saved-code paths:** Read `workflow.json.code_layout_version` first. In this reference, `<script-dir>` means `code/<step-id>` for version 1, or `learnings/<step-id>` for absent/zero (legacy). Resolve the placeholder before using a path; never infer the version from folders or migrate an existing workflow implicitly. Version 1 executes and repairs canonical source directly, with shared helpers under `WORKFLOW_CODE_ROOT`; only legacy workflows copy code into runs and save it back.

## OPTIMIZATION GUIDELINES

**Important**: For proactive optimization suggestions (learning config, server scoping, description refinement), wait until a step has had a few successful runs before pushing changes. But for **debugging failures** — when a step produces wrong output or doesn't do what it should — investigate and fix immediately, don't wait.

**Repair in service of the goal, across the whole plan.** Every fix exists to make the workflow achieve its objective — read `soul/soul.md` (success criteria) and the plan before changing behavior, understand each step's role (what feeds it, what depends on its output), and fix it so it does its real job *in that context*, not merely so it stops erroring or passes validation. A green-but-goal-empty fix (a masking default, loosened validation that lets wrong data through) is a **false fix**. If a contained, plan-coherent repair cannot make a step reliably do its job, return a recommendation for the owner instead of stretching a local repair into strategy work.

When helping users optimize steps, follow these principles:

### 1. Validation Schema vs Success Criteria — They Serve Different Purposes

**validation_schema** is the deterministic output gate. It runs code-based checks with no LLM involved; a failed check follows the step's configured retry/failure policy. Passing validates the checked output, not human approval or proof of every semantic claim. Keep the gate focused on the downstream contract (see `references/step-description.md`):
- **File existence**: Output files must exist
- **Field completeness**: Require the fields consumers actually depend on. For a login check, validate the observed account identity if it matters to the next action; do not require unrelated identity fields simply to enlarge the schema
- **Value constraints**: Types, min/max lengths, regex patterns for format validation, min/max values for numbers
- **Cross-field consistency**: Use "consistency_check" to compare related fields (e.g., array length matches a count field)
- **Anti-staleness**: Check relevant run/source identity and authoritative read-back evidence. Extra field names or non-empty strings alone cannot distinguish a stale or fabricated result from a real observation.

Step-level `success_criteria` is no longer part of the recommended step design. Put semantic completion guidance into `description`, and put machine-checkable requirements into `validation_schema`.
- **validation_schema**: Check login_status.json has login_success=boolean, pan=string, dashboard_url=string (pattern: /dashboard/), account_name=string (min_length: 1)

If a step needs **semantic/LLM-based validation** (e.g., "verify the summary is accurate"), keep it in the same shared context: use or convert to one large `agent`, add a focused user-message turn that re-opens the real evidence and proves the criteria, then a repair/double-check turn. Keep machine-checkable proof/provenance in the top-level `validation_schema`. Add a separate validation step only for genuine clean-room independence, different permissions/tools, or an independently rerunnable artifact/failure domain.

When reviewing a successful step, check whether its evidence establishes freshness and authenticity. Add targeted provenance checks or semantic evidence verification where needed; do not treat an exhaustive schema as proof.

### 2. Knowledge and skills

Goal steps consume selected workflow skills and read knowledge under existing grants. A required KB contribution belongs in the task with KB write access. No automatic post-completion reflection or learning runs. Builder maintain skills; Crew owns ongoing agent learning. Existing guides migrate explicitly through contract 1.0.51, preserving contents and history without changing KB grants.

#### The Per-Step Code Lock — What It Freezes and When To Use

Only saved scripted code has a lock. Steps do not write skills.

| Lock | Scope | Freezes | Prevents | Use when |
| --- | --- | --- | --- | --- |
| `lock_code` | Per-step (scripted only) | `<script-dir>/main.py` | Rewriting of the script by the Builder's own `execute_step` (runs never rewrite scripts whatever this says) | The user asked for it, or the script is proven: 10+ successful scenario-covering runs with eval evidence at target. Otherwise keep `lock_code=false` so you can still repair it with `execute_step`. |

**After hand-editing an artifact**: do not lock it automatically. Verify the edit against real scenario-covering runs first. Lock only when the user explicitly asks or the artifact has enough evidence to be treated as stable; otherwise leave it unlocked so later runs can expose and repair drift. Record the decision in `review_notes`.

**Description changes and lock state**: The step description is the source of truth that learnings and scripted code were generated against.

- **`lock_code` does NOT auto-unlock.** If you changed the description semantically and `lock_code` is set, the frozen main.py may now be wrong for the new intent — clear it explicitly: `update_step_config(step_id, lock_code=false)`.
- Pure **rewording** (clarifying existing instructions without changing intent) may still change the description hash used in metadata. Treat the hash as review evidence, not as an automatic lock lifecycle.
- When you meaningfully change a step's description, clear `description_reviewed` so future reviewers know the description needs a fresh eyeballing.
- **Reconcile the blast radius.** When a bounded fix changes a step's output contract, db writes, or behavior, run `read_skill(skills=[{"name":"builder-reference","path":"references/plan-change-impact.md"}])` and reconcile the dependents (downstream steps, evals, report dashboard, db, learnings, KB) before treating the fix as done — do not repair one step and silently break what reads it.

**Reviewing descriptions during repair**: Treat each touched step's `description` as a first-class review target, not just something to fix when it is obviously stale. Ask: does it still describe what the step actually does and should produce this run? A drifted or vague description silently corrupts the learnings and scripted code generated against it. Realign it when it no longer matches, review relevant skill selections, and clear `lock_code` plus `description_reviewed` when the saved script must regenerate.

**Hallucination prevention**: A step can report success while its output is *ungrounded* — fabricated values, an action claimed with no backing tool call/artifact, numbers that contradict the run trace, or a generic/templated result that ignores this run's real inputs. That is a reliability bug even when the step “passed.” Repair a hallucination-prone step by making fabrication hard to pass:
- **Demand evidence in `validation_schema`** — require real, run-specific fields (IDs, URLs, timestamps, counts that must trace to this run) and anti-staleness checks, not a bare `success: true`, so a made-up or leftover output can't validate.
- **Add verification inside the owning agent**: a follow-up turn re-opens actual artifacts/tool results/source data, reconciles every claim, and repairs mismatches before the top-level gate. Use a separate verifier only when clean-room independence or a different execution boundary is materially required.
- **Require grounding in the description** — instruct the step to derive values only from real tool output / fetched data and to cite where each value came from, never to infer or fill them in.
Trust output you can trace back to real evidence, not a self-reported success.

**KB writes are step-based**: A step writes KB notes only when `knowledgebase_access` is `write` or `read-write` and its task explicitly asks for notes. When a contributor no longer produces durable new facts, change that step to `knowledgebase_access="read"`. Do not freeze unrelated KB writers workflow-wide. Inspect relevant KB evidence directly; Builder applies bounded intentional curation within current grants.

### 3. Maintaining workflow skills

Use the existing workflow-local skill format and selection tools. Read the short `skills/<name>/SKILL.md` index and targeted references before a repair. Builder update reusable methods in place, preserving evidence and avoiding conflicting duplicate instructions. Do not create or tune a separate learning store. For existing guides use `migrate_workflow_skills`, inspect the preserved references and copy hashes, then stamp 1.0.51. Keep old files as history for the transition; legacy executable code uses the separate code-layout upgrade first.

### 3b. Debugging & Fixing Scripted Code Steps (scripted)

When patching `<script-dir>/main.py`, also load the full main.py authoring rules:
`read_skill(skills=[{"name":"builder-reference","path":"references/code-authoring.md"}])` — covers env access, sys.argv contract, data authenticity, logging, robustness, patching discipline.

For steps in scripted mode, the saved Python script at `<script-dir>/main.py` is the primary artifact. When a scripted step fails, follow this workflow:

**1. Diagnose** — Understand what went wrong:
- Read the script: `cat <script-dir>/main.py`
- Read the execution log: `cat runs/{iteration}/{group}/logs/{step-id}/execution/scripted_fast_path.json` — contains exit_code, stdout output, and error
- Read script_metadata.json: `cat <script-dir>/script_metadata.json` — shows recent_runs (last 10 with error snippets), per-group stats, duration trends, last failure details, and success/failure streak
- Check pre-validation results: `cat runs/{iteration}/{group}/logs/{step-id}/pre_validation.json`
- Use `debug_step(step_id)` for a comprehensive analysis including the script metadata

**1b. Live diagnosis with MCP tools** — You share the same agent-browser session and MCP tools as the step execution. You can directly call browser tools and other MCP servers to investigate issues interactively:
- Use `agent_browser(command="snapshot", args=["-i"])` to see the current browser state
- Use `agent_browser(command="open", args=[url])` to reproduce navigation
- Use `agent_browser(command="eval", args=[read_only_javascript])` to inspect page state when snapshots are insufficient
- Use `agent_browser` commands such as `click`, `fill`, and `press` to step through the UI flow and find where it breaks
- You can also call any other MCP tools the step uses (e.g., google-sheets) to verify API behavior
- This is the fastest way to diagnose issues like changed selectors, timing problems, unexpected page states, or API response changes — you see exactly what the script would see at runtime

**2. Fix** — Patch the script directly:
- Make a targeted edit to `<script-dir>/main.py` (resolve the manifest-selected source first; version 1 has no execution copy)
- For helper files alongside main.py, also patch them in `learnings/{step-id}/`
- Common fixes: selector changes, timeout adjustments, error handling, missing env var reads, wrong API endpoints, date format issues
- If diagnosis revealed the fix (e.g., a selector changed), apply it directly. If the issue is complex, use your live MCP access to prototype the fix interactively before patching.

**3. Test** — Run the patched script:
- Use `execute_step(step_id, group_name, fast_path_only=true)` to test the fix directly — this runs ONLY the saved script without invoking the step agent, so you see exactly what your patch does
- Or use `execute_step(step_id, group_name)` to let the selected step agent handle a script failure
- After running, use `agent_browser(command="snapshot", args=["-i"])` to confirm the expected page state, or read output files to check correctness
- Check the output files and logs to confirm the fix

**4. Validate across groups** — If the workflow has multiple groups, test the fix against other groups too. Check `script_metadata.json` group_stats to see which groups were failing.

**5. Lock code when proven** — After confirming the fix works:
- `update_step_config(step_id, lock_code=true)` to freeze `<script-dir>/main.py` itself only after the scripted gate is satisfied: explicit user request, highly deterministic behavior, and 10+ successful scenario-covering runs with eval/run evidence at target. Runs always use the saved script as-is and never rewrite it; `lock_code=true` additionally stops your own `execute_step` from rewriting it.
- **Do not lock code just because you hand-patched it.** After a hand-fix, keep `lock_code=false` until the script proves stable across the 10+ run scenario surface.

**Key principle**: Always edit `<script-dir>/main.py`, never `execution/{step-id}/code/main.py`. In builder/reviewer turns edit canonical source; controller-managed legacy repair instead uses its explicitly supplied execution directory. Only legacy workflows copy source into runs.

**Force complete rewrite**: If the saved script has fundamental issues (wrong approach, bad patterns like JavaScript injection instead of ref-based browser interaction), delete the learnings script to force the LLM to write from scratch:
- `rm <script-dir>/main.py` — deletes the saved script
- Then run `execute_step(step_id, group_name)` — the LLM will generate a fresh main.py using the step description, skill files, and proper tool discovery via get_api_spec
- Do NOT just delete `execution/{step-id}/code/main.py` — in legacy workflows this disposable copy is restored from saved source; version 1 has no such copy

### 4. Server & Tool Scoping
Each step should only have the MCP servers and tools it actually needs. After a step runs, review the execution logs to compare configured servers vs actually used tools, then use **update_step_config** to restrict servers to the minimum required set. This reduces tool discovery noise and speeds up execution.

### 4a. Skill Scoping

Installed skills are reusable capability instructions under `<workspace-root>/skills/{folder}/SKILL.md`. Both installed capabilities and workflow-specific methods use this same skill format.

- Workflow-selected skills from `settings(action="update", skills={add:[...]})` are builder/workshop discovery context. They do not automatically reach runtime step agents.
- Runtime step agents receive only skills listed in that step's `enabled_skills`. Use `update_step_config(step_id, enabled_skills=[...])` when a step actually needs an installed skill.
- If a failing step is doing ad-hoc work covered by an installed skill, enable the skill on that step and keep the step description focused on task, inputs, intended outcome, and constraints; keep output structure in its schema or canonical contract.
- If a skill is enabled but irrelevant to the step, remove it from that step to reduce prompt noise.
- If guidance is workflow-specific (selectors discovered in this workflow, account names, run paths, current plan details), put it in a workflow-local skill instead of editing an external skill.

### 4b. LLM Tier Selection
AgentWorks and Builder agree these evidence-based recommendations and measured trials.
Runtime never changes tiers based on run counts; preserve explicit user settings.
In tiered mode, prefer a persistent `execution_tier` when a step should usually run on a cheaper or faster tier, instead of pinning an exact model.

- **Use `execution_tier` for persistent behavior**: `update_step_config(step_id, execution_tier="medium", execution_tier_reason="Finding, evidence, and approved decision")` or `"low"` when the step is stable and you want future runs to default to that tier.
- **Use `execution_llm` only when you need an exact model**: this pins a specific provider/model and overrides tier selection entirely.
- **Use `execute_step(step_id, group_name, tier="...")` for one-off trials**: this is for testing a single run without changing the step's persistent config.
- **Prefer `execution_tier` over exact-model pinning for mature steps**: if the goal is "this step can usually run on medium/low", set the tier, don't hardcode a model.
- **Do not force a cheaper tier while material goals are below target**: preserve the current model/reasoning tier for outcome-bearing, planning, judgment, diagnostic, recovery, eval, and verification work. First make the workflow reliable and achieve representative at-target evidence. Only a mechanical deterministic non-bottleneck step with proven quality-equivalent outputs and no downstream outcome loss may be proposed as an explicitly approved reversible downgrade trial.
- **If a step has `execution_llm` set, `execution_tier` is ignored** until the exact-model override is cleared.

### 5. Step Description Optimization
The step **description** in plan.json is the primary instruction the execution agent receives. A well-written description directly improves output quality.

**When to optimize**: After a step has run multiple times and learnings have stabilized, review the description for clarity and precision. Don't optimize descriptions on steps that are still evolving.

**Principles**:
- **Be specific about the expected outcome**: Instead of "create a report", say "Assess the supplied findings, explain their impact, and save the assessment to report.json in STEP_OUTPUT_DIR." Put field names, types, required keys, and shape in `validation_schema` or an accessible canonical contract, not in the description.
- **Reference context_output files from prior steps**: E.g., "Using the data from step-extract-data's context_output, generate...". The execution agent receives prior step outputs as context.
- **Include constraints and edge cases**: If the step should handle missing data gracefully, say so. If there's a size limit or format requirement, specify it.
- **Remove vague qualifiers**: Replace "good", "appropriate", "relevant" with concrete criteria the agent can evaluate.
- **Keep WHAT and HOW separate**: Keep objectives, task-specific business constraints, authorization boundaries, and success criteria in the description. Curate reusable methods and verified procedures in accessible skills maintained by Builder. See `references/step-description.md`.
- **Keep the boundary coherent**: The description may include many tool calls or sub-actions, but it should still serve one durable output contract. If it starts mixing unrelated outputs, validation gates, retry domains, stores, or approval/routing decisions, split at those boundaries.

**How to update**: Use the plan modification tools (`update_step`, `update_step`, `update_step`, `manage_step_route`, `update_step`, `update_step`, or `update_validation_schema`) to update step descriptions and validation. Do not patch `planning/plan.json` directly; it is system-managed and guarded. The change takes effect on the next execution.

**Description review bookkeeping is required**: After you change or approve a description, immediately call `update_step_config` to record:
- `description_reviewed` + `review_notes`
Plan modification tools automatically clear `description_reviewed` when a reviewed contract changes (including description, dependencies, schema, messages, or routing). Review the final edited contract before marking it reviewed again. If the tool reports a bookkeeping failure, repair that metadata; do not assume the old review is current.

**Dependencies after changes**: Follow `builder-reference/references/plan-change-impact.md`. After related edits, do one combined compatibility check in the current agent before the targeted test; inspect only affected dependencies. Do not launch a separate drift reviewer for each edit or test retry. Reserve the full `/run-plan-drift` audit for scheduled AgentWorks or an explicit user request. Record inspected changelog entries through typed review metadata.

### 6. Post-Execution Step Review
After running a step, review it for optimization — but follow this priority order. Fix fundamentals first before worrying about efficiency.

**Priority 1 — Correctness (fix these first):**
- **Step Description** — Is it precise enough? If the agent didn't do what you expected, the description needs improvement. This is the #1 lever.
- **Pre-Validation Schema** — Does the schema catch bad output? Could a stale/fake file pass? Tighten field checks, add anti-staleness fields.
- **Context I/O** — Are context_dependencies and context_output correct? Missing deps cause failures; incomplete outputs break downstream steps.

**Priority 2 — Knowledge (fix after step works correctly):**
- **When a result points to stale guidance, review the selected skill and relevant references.** Check:
  - Are they **specific and actionable**? Vague learnings like "be careful with the API" waste tokens. Good learnings describe exact patterns: "The /api/v2/data endpoint returns paginated results — always follow next_page_token until null."
  - Do they **contradict the step description**? If so, either update the description or delete the misleading learning.
  - Do they **match the current step config**? Cross-check learnings against the step's configured servers, tools, and description. Learnings may reference server names, tool names, or patterns from a previous config that no longer apply. Stale references cause the execution agent to search for non-existent servers/tools, wasting turns and causing failures. Fix by updating the learning file with the correct names.
  - Are they **repetitive**? If the same verified method appears across multiple learning files, consolidate it into the canonical workflow skill and update references before removing redundant copies. Do not move procedures into the step description.
- Review existing skills against actual results and remove stale advice. KB contributions need the write grant and an explicit task; they add no hidden closing turn.

**Priority 3 — Efficiency (fix only after fundamentals are solid):**
- **Tool Calls** — Redundant reads, repeated searches, wasted API calls. Usually a symptom of a vague description — fix the description first, then check if tool waste drops.
- **Workflow Structure** — Merge, split, delete, add, or reorder steps for a more optimal overall workflow:
  - **Merge**: Sequential steps with the same context/objective/output should usually become one large `agent`
  - **Strengthen before splitting**: Improve the description, proof/provenance output, validation schema, retry instructions, and verify/repair turns first
  - **Split**: Use multiple large sequences only when contexts should not be shared or an output/retry/security/human/routing boundary must be independent
  - **Delete**: A step whose output is never consumed downstream is dead weight
  - **Validate in context**: If output needs semantic validation, add evidence-based proof, double-check, and repair turns inside the owning sequence
  - **Reorder**: If dependencies aren't ready, step ordering may need adjustment

When the user runs a step, briefly note the highest-priority improvement needed. Don't dump all dimensions at once — focus on what matters most right now.

### 7. Execution Modes: Agentic vs Scripted

A step's execution mode is its plan type: `scripted` runs a saved script and `agent` runs a conversation. Choose the type with `add_step`; `update_step_config` does not change it:

- **Scripted** (`scripted` type): Agent writes a reusable `main.py` that is saved and tried first on future runs (0 LLM tokens when stable). If the saved script fails, the LLM repairs it. This is the default execution mode for deterministic API/SDK calls, CLI commands, known pagination, data fetching, stable parsing/normalization/transforms, and mechanical persistence. Create or move that work to scripted immediately; no run-count gate applies to mode selection. The 10+-scenario-covering-runs evidence gates only *trusting and freezing* the script with `lock_code`.

  **Keep scripted steps coherent, not microscopic.** A good scripted fetcher owns one source/auth/retry/output contract and may batch several related endpoints, CLI commands, pagination passes, and transforms before writing its authoritative rows/artifact. Do not create one script per endpoint or tiny transform. Do not cram adaptive judgment or a branching business workflow into `main.py`; feed the validated deterministic output to one large agentic `agent` instead.

  Good candidates for scripted (each kept small):
  - One coherent source fetched and written to its canonical tables (e.g. related API endpoints + pagination → parse/normalize → idempotent `db` upserts)
  - Deterministic data processing: iterating rows, matching columns, extracting/transforming — a tight Python loop in one shot, no per-row "thinking"
  - A focused transform that benefits from Python libraries (parsing, calculations, formatting)
- **Agentic** (`agent` type): the LLM acts each turn and no persistent script is saved. Use it for judgment, synthesis, fuzzy extraction, adaptive discovery, or action selection that genuinely varies with live evidence. A fixed API/CLI call is scripted even when it is only one call; simplicity is a reason to make the script small, not a reason to spend an LLM turn on it. Browser/UI steps should generally stay agentic unless the user explicitly wants scripted browser automation and representative evidence proves the flow stable enough. If an agentic step has leftover `<script-dir>/main.py`, delete it; that file is stale mode debt and should not be patched.

**Mode-selection rule:** Create or convert deterministic API/CLI/SDK/data-fetch/parse/transform/persist work as `scripted` on Workshop's own initiative; this is architecture selection, not freezing. Treat 10+ scenario-covering successful runs (with eval/run evidence at target) as the bar only for **freezing the saved script with `lock_code`**. Keep `lock_code=false` until that evidence exists so you can still repair drift with `execute_step`. Keep judgment, adaptive discovery, and browser/UI work agentic.

**There is no mode field to fill in**: the plan type is the declaration. When the user asks to make a step scripted, create a scripted step with `add_step`, then author and test `<script-dir>/main.py`. `use_code_execution_mode` is a separate, independent toggle — an `agent` can use code execution without being scripted.

**Workshop agent behavior for code-exec steps**: When you (the workshop agent) are asked to explore, investigate, or do manual work related to a step marked with code execution mode, you should also adopt the code-exec approach — use **execute_shell_command** to write and run Python/shell scripts that combine multiple MCP tool calls together, rather than making individual tool calls one by one. This mirrors how the step's execution agent works and helps you build reusable scripts and patterns that can inform the step's learnings.

**Code-exec efficiency goal**: The goal of code execution mode is to minimize tool calls. Ideally, the agent should run the entire step in a **single execute_shell_command call** — one Python script that handles everything (API calls, data processing, output writing). After a code-exec step runs, review the learnings and check: did the agent use multiple tool calls where a single script would suffice? If so, update the learnings to consolidate into fewer calls. Good code-exec learnings produce steps that complete in 1-2 tool calls instead of 10+.

**Variable handling in code-exec learnings**: When writing or reviewing learnings for code execution steps, **never hardcode variable values** (account IDs, URLs, credentials, etc.) in the code. Variables are available in the step description as resolved values — the generated code should use sys.argv or argparse to accept them as CLI arguments. There is no automatic learning agent to rewrite these values; author runtime inputs explicitly. If you notice hardcoded values in code learnings, fix them immediately.

### 8. Evidence-Based Locking And Review

Review actual outputs, validation and execution logs before setting description_reviewed or locking saved code. Existing guides may inform the task, but a successful run does not automatically produce learning files. Fix observed tool-use errors in the task or its relevant guide, then rerun only as needed to confirm the change.

`lock_code` freezes only the Builder's own repairs to a scripted step. Lock when requested or when representative evidence proves the script stable; runs already execute saved code without rewriting it. Clear stale code locks and description review state after a meaningful change.

Knowledge-base writes are explicit task work under that step's access. Builder can curate existing notes using their file tools and evidence. Do not require retired contribution objectives, a learning-file generation pass, or a separate KB writer mode.

### 9. Script helpers and Crew

Use typed scripted helpers when a Goal agent chooses deterministic Python work.
Use an explicit Crew step for LLM specialists, persistent skills/memory or
multi-agent coordination. Goal agents have no LLM sub-agent routes.

### 9a. Deterministic batches do not need specialist routes

If a routed agent always calls a fixed set in a fixed order and only branches on
success/failure, it is not making a runtime delegation decision. Express the
work as explicit plan steps, or use a `scripted` agent item for a
declared batch of saved scripts. Keep the parent conversation only for reasoning,
verification, and repair over those deterministic results.
